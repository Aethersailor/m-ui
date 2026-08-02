package mihomo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeStartupPublishesLiveReadinessAfterRecovery(t *testing.T) {
	root := t.TempDir()
	leasePath := filepath.Join(root, "startup.lease")
	readyPath := filepath.Join(root, "ready")
	startup, err := BeginRuntimeStartupAt(leasePath, readyPath)
	if err != nil {
		t.Fatalf("BeginRuntimeStartupAt() error = %v", err)
	}
	defer func() { _ = startup.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = WaitForRuntimeReadyAt(ctx, leasePath, readyPath)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForRuntimeReadyAt() error = %v, want context deadline", err)
	}

	if err := startup.PublishReady(); err != nil {
		t.Fatalf("PublishReady() error = %v", err)
	}
	if err := WaitForRuntimeReadyAt(context.Background(), leasePath, readyPath); err != nil {
		t.Fatalf("WaitForRuntimeReadyAt() after publish error = %v", err)
	}
	if _, err := os.Stat(leasePath); err != nil {
		t.Fatalf("persistent startup lease stat error = %v", err)
	}
}

func TestRuntimeStartupRejectsLiveReadinessOwner(t *testing.T) {
	root := t.TempDir()
	leasePath := filepath.Join(root, "startup.lease")
	readyPath := filepath.Join(root, "ready")
	first, err := BeginRuntimeStartupAt(leasePath, readyPath)
	if err != nil {
		t.Fatalf("first BeginRuntimeStartupAt() error = %v", err)
	}
	defer func() { _ = first.Close() }()
	if err := first.PublishReady(); err != nil {
		t.Fatalf("first PublishReady() error = %v", err)
	}

	second, err := BeginRuntimeStartupAt(leasePath, readyPath)
	if err == nil {
		_ = second.Close()
		t.Fatal("second BeginRuntimeStartupAt() error = nil")
	}
	if err := WaitForRuntimeReadyAt(context.Background(), leasePath, readyPath); err != nil {
		t.Fatalf("failed second startup invalidated live readiness: %v", err)
	}
}

func TestRuntimeReadyGuardPinsGenerationAcrossOwnerClose(t *testing.T) {
	root := t.TempDir()
	leasePath := filepath.Join(root, "startup.lease")
	readyPath := filepath.Join(root, "ready")
	first, err := BeginRuntimeStartupAt(leasePath, readyPath)
	if err != nil {
		t.Fatalf("first BeginRuntimeStartupAt() error = %v", err)
	}
	if err := first.PublishReady(); err != nil {
		_ = first.Close()
		t.Fatalf("first PublishReady() error = %v", err)
	}
	guard, err := AcquireRuntimeReadyGuardAt(context.Background(), leasePath, readyPath)
	if err != nil {
		_ = first.Close()
		t.Fatalf("AcquireRuntimeReadyGuardAt() error = %v", err)
	}
	if err := first.Close(); err != nil {
		_ = guard.Close()
		t.Fatalf("first Close() error = %v", err)
	}

	second, err := BeginRuntimeStartupAt(leasePath, readyPath)
	if err == nil {
		_ = second.Close()
		_ = guard.Close()
		t.Fatal("second BeginRuntimeStartupAt() succeeded while readiness guard was held")
	}
	if err := guard.Close(); err != nil {
		t.Fatalf("RuntimeReadyGuard.Close() error = %v", err)
	}
	second, err = BeginRuntimeStartupAt(leasePath, readyPath)
	if err != nil {
		t.Fatalf("second BeginRuntimeStartupAt() after guard release error = %v", err)
	}
	defer func() { _ = second.Close() }()
}

func TestRuntimeStartupRemovesStaleReadiness(t *testing.T) {
	root := t.TempDir()
	leasePath := filepath.Join(root, "startup.lease")
	readyPath := filepath.Join(root, "ready")
	if err := os.WriteFile(readyPath, []byte("stale\n"), 0o600); err != nil {
		t.Fatalf("write stale readiness: %v", err)
	}

	startup, err := BeginRuntimeStartupAt(leasePath, readyPath)
	if err != nil {
		t.Fatalf("BeginRuntimeStartupAt() error = %v", err)
	}
	defer func() { _ = startup.Close() }()
	if _, err := os.Stat(readyPath); err != nil {
		t.Fatalf("persistent readiness stat error = %v", err)
	}
	if token, err := readRuntimeLockToken(readyPath); err != nil || token != "" {
		t.Fatalf("stale readiness token = %q, error = %v; want empty", token, err)
	}
}

func TestRuntimeReadinessIgnoresResetLockWithMismatchedToken(t *testing.T) {
	root := t.TempDir()
	leasePath := filepath.Join(root, "startup.lease")
	readyPath := filepath.Join(root, "ready")
	lease, err := openRuntimeLockFile(leasePath, true)
	if err != nil {
		t.Fatalf("open lease: %v", err)
	}
	if busy, err := tryLockRuntimeLockFile(lease); err != nil || busy {
		t.Fatalf("lock lease = (%v, %v)", busy, err)
	}
	if err := writeRuntimeLockToken(lease, "new-startup-token"); err != nil {
		t.Fatalf("write lease token: %v", err)
	}
	if err := unlockRuntimeLockFile(lease); err != nil {
		t.Fatalf("release lease: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("close lease: %v", err)
	}

	ready, err := openRuntimeLockFile(readyPath, true)
	if err != nil {
		t.Fatalf("open readiness: %v", err)
	}
	if busy, err := tryLockRuntimeLockFile(ready); err != nil || busy {
		t.Fatalf("lock readiness = (%v, %v)", busy, err)
	}
	defer func() {
		_ = unlockRuntimeLockFile(ready)
		_ = ready.Close()
	}()
	if err := writeRuntimeLockToken(ready, "old-startup-token"); err != nil {
		t.Fatalf("write stale readiness token: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := WaitForRuntimeReadyAt(ctx, leasePath, readyPath); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForRuntimeReadyAt() error = %v, want context deadline", err)
	}
}
