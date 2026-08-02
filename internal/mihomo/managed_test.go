package mihomo

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagedProcessChild(t *testing.T) {
	if os.Getenv("M_UI_MANAGED_CHILD") != "1" {
		return
	}
	configPath := ""
	for index := 0; index+1 < len(os.Args); index++ {
		if os.Args[index] == "-f" {
			configPath = os.Args[index+1]
			break
		}
	}
	content, _ := os.ReadFile(configPath)
	if string(content) == "exit" {
		os.Exit(0)
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestManagedProcessRecoveryUsesApplicationBoundary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process, err := NewManagedProcess(
		ctx,
		filepath.Join(t.TempDir(), "missing-mihomo"),
		filepath.Join(t.TempDir(), "config.yaml"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	if err := process.SetRecovery(func(context.Context) error {
		calls.Add(1)
		process.mutex.Lock()
		process.active = true
		process.mutex.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("SetRecovery() error = %v", err)
	}
	process.mutex.Lock()
	process.desired = true
	process.mutex.Unlock()

	process.restartLoop()
	if got := calls.Load(); got != 1 {
		t.Fatalf("recovery callback calls = %d, want 1", got)
	}
	process.mutex.Lock()
	defer process.mutex.Unlock()
	if !process.desired {
		t.Fatal("successful application recovery cleared desired state")
	}
	if process.restarting {
		t.Fatal("restartLoop left restarting state set")
	}
}

func TestManagedProcessRecoveryRetriesAfterImmediateExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process, err := NewManagedProcess(
		ctx,
		filepath.Join(t.TempDir(), "missing-mihomo"),
		filepath.Join(t.TempDir(), "config.yaml"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	process.recoveryBackoff = 0
	var calls atomic.Int32
	if err := process.SetRecovery(func(context.Context) error {
		if calls.Add(1) == 1 {
			// Model the process exiting immediately after the application
			// boundary reports a healthy start. wait() records this same
			// pending event while restarting remains true.
			process.mutex.Lock()
			process.active = false
			process.exitGeneration++
			process.recoveryPending = true
			process.mutex.Unlock()
			return nil
		}
		process.mutex.Lock()
		process.active = true
		process.mutex.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("SetRecovery() error = %v", err)
	}
	process.mutex.Lock()
	process.desired = true
	process.restarting = true
	process.recoveryGeneration = 1
	process.exitGeneration = 1
	process.mutex.Unlock()

	process.restartLoop()

	if got := calls.Load(); got != 2 {
		t.Fatalf("recovery callback calls = %d, want 2", got)
	}
	process.mutex.Lock()
	defer process.mutex.Unlock()
	if !process.active || !process.desired {
		t.Fatalf("recovery state = active:%v desired:%v, want both true", process.active, process.desired)
	}
	if process.restarting || process.recoveryPending {
		t.Fatalf("recovery loop state left set: restarting:%v pending:%v", process.restarting, process.recoveryPending)
	}
}

func TestManagedProcessRecoveryUsesRealChildBarrier(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	childPath := filepath.Join(t.TempDir(), "managed-child"+filepath.Ext(executable))
	content, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childPath, content, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "managed-child.config")
	if err := os.WriteFile(configPath, []byte("exit"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("M_UI_MANAGED_CHILD", "1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process, err := NewManagedProcess(ctx, childPath, configPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	process.recoveryBackoff = 0
	var calls atomic.Int32
	if err := process.SetRecovery(func(ctx context.Context) error {
		call := calls.Add(1)
		if call == 2 {
			if err := os.WriteFile(configPath, []byte("block"), 0o600); err != nil {
				return err
			}
		}
		if _, err := process.StartAttempt(ctx); err != nil {
			return err
		}
		if call == 1 {
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			for {
				process.mutex.Lock()
				active := process.active
				process.mutex.Unlock()
				if !active {
					return nil
				}
				select {
				case <-deadline.C:
					return context.DeadlineExceeded
				case <-time.After(5 * time.Millisecond):
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("SetRecovery() error = %v", err)
	}
	process.mutex.Lock()
	process.desired = true
	process.restarting = true
	process.mutex.Unlock()

	process.restartLoop()
	if got := calls.Load(); got != 2 {
		t.Fatalf("real-child recovery callback calls = %d, want 2", got)
	}
	process.mutex.Lock()
	active := process.active
	process.mutex.Unlock()
	if !active {
		t.Fatal("second real recovery child is not active")
	}
	process.mutex.Lock()
	command := process.command
	process.mutex.Unlock()
	if command == nil || command.Process == nil {
		t.Fatal("real recovery child disappeared before cleanup")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatalf("kill real recovery child: %v", err)
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		process.mutex.Lock()
		active = process.active
		process.mutex.Unlock()
		if !active {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("real recovery child remained active after cleanup")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestManagedProcessRejectsNilRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process, err := NewManagedProcess(
		ctx,
		filepath.Join(t.TempDir(), "mihomo"),
		filepath.Join(t.TempDir(), "config.yaml"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.SetRecovery(nil); err == nil {
		t.Fatal("SetRecovery(nil) returned nil error")
	}
}
