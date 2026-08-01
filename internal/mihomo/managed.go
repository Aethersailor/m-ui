package mihomo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Aethersailor/m-ui/internal/redact"
)

type ManagedProcess struct {
	rootContext context.Context
	binaryPath  string
	configPath  string
	logger      *slog.Logger

	mutex      sync.Mutex
	command    *exec.Cmd
	active     bool
	desired    bool
	restarting bool
	logs       []LogEntry
}

func NewManagedProcess(
	ctx context.Context,
	binaryPath, configPath string,
	logger *slog.Logger,
) (*ManagedProcess, error) {
	if ctx == nil {
		return nil, errors.New("managed process context is required")
	}
	if strings.TrimSpace(binaryPath) == "" ||
		strings.TrimSpace(configPath) == "" {
		return nil, errors.New("managed process binary and config paths are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	process := &ManagedProcess{
		rootContext: ctx,
		binaryPath:  binaryPath,
		configPath:  configPath,
		logger:      logger,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = process.Stop(shutdown)
	}()
	return process, nil
}

func (process *ManagedProcess) IsActive(context.Context) (bool, error) {
	process.mutex.Lock()
	defer process.mutex.Unlock()
	return process.active, nil
}

func (process *ManagedProcess) Start(ctx context.Context) error {
	process.mutex.Lock()
	defer process.mutex.Unlock()
	if process.active {
		process.desired = true
		return nil
	}
	if err := process.rootContext.Err(); err != nil {
		return errors.New("managed Mihomo supervisor is shutting down")
	}
	process.desired = true
	return process.startLocked(ctx)
}

func (process *ManagedProcess) Stop(ctx context.Context) error {
	process.mutex.Lock()
	process.desired = false
	command := process.command
	if command == nil || command.Process == nil || !process.active {
		process.mutex.Unlock()
		return nil
	}
	process.mutex.Unlock()
	if err := signalTerminate(command.Process); err != nil {
		return errors.New("signal managed Mihomo process")
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		process.mutex.Lock()
		active := process.active
		process.mutex.Unlock()
		if !active {
			return nil
		}
		select {
		case <-ctx.Done():
			if killErr := command.Process.Kill(); killErr != nil {
				return errors.New("kill managed Mihomo process after shutdown timeout")
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (process *ManagedProcess) Restart(ctx context.Context) error {
	if err := process.Stop(ctx); err != nil {
		return err
	}
	return process.Start(ctx)
}

func (process *ManagedProcess) Reload(ctx context.Context) error {
	// Restart is the portable managed-mode reload boundary. Configuration
	// publication still verifies the candidate and health-checks the result.
	return process.Restart(ctx)
}

func (process *ManagedProcess) RecentLogs(
	_ context.Context,
	limit int,
) ([]LogEntry, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("log limit must be between 1 and 1000")
	}
	process.mutex.Lock()
	defer process.mutex.Unlock()
	start := len(process.logs) - limit
	if start < 0 {
		start = 0
	}
	result := make([]LogEntry, len(process.logs[start:]))
	copy(result, process.logs[start:])
	return result, nil
}

func (process *ManagedProcess) startLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	command := exec.CommandContext(
		process.rootContext,
		process.binaryPath,
		"-d",
		"/var/lib/mihomo",
		"-f",
		process.configPath,
	)
	writer := &managedLogWriter{process: process}
	command.Stdout = writer
	command.Stderr = writer
	if err := command.Start(); err != nil {
		return errors.New("start managed Mihomo process")
	}
	process.command = command
	process.active = true
	go process.wait(command)
	return nil
}

func (process *ManagedProcess) wait(command *exec.Cmd) {
	err := command.Wait()
	process.mutex.Lock()
	if process.command != command {
		process.mutex.Unlock()
		return
	}
	process.active = false
	process.command = nil
	desired := process.desired && process.rootContext.Err() == nil
	if !desired || process.restarting {
		process.mutex.Unlock()
		return
	}
	process.restarting = true
	process.mutex.Unlock()

	if err != nil {
		process.logger.Warn("managed Mihomo process exited", "error", "process exited")
	}
	go process.restartLoop()
}

func (process *ManagedProcess) restartLoop() {
	defer func() {
		process.mutex.Lock()
		process.restarting = false
		process.mutex.Unlock()
	}()
	backoff := 250 * time.Millisecond
	for attempt := 0; attempt < 5; attempt++ {
		timer := time.NewTimer(backoff)
		select {
		case <-process.rootContext.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		process.mutex.Lock()
		if !process.desired {
			process.mutex.Unlock()
			return
		}
		err := process.startLocked(process.rootContext)
		process.mutex.Unlock()
		if err == nil {
			return
		}
		backoff *= 2
		if backoff > 5*time.Second {
			backoff = 5 * time.Second
		}
	}
	process.mutex.Lock()
	process.desired = false
	process.mutex.Unlock()
	process.logger.Error("managed Mihomo restart limit reached")
}

type managedLogWriter struct {
	process *ManagedProcess
	buffer  bytes.Buffer
	mutex   sync.Mutex
}

func (writer *managedLogWriter) Write(value []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	written, _ := writer.buffer.Write(value)
	for {
		line, err := writer.buffer.ReadString('\n')
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return written, err
			}
			_, _ = writer.buffer.WriteString(line)
			return written, nil
		}
		writer.process.appendLog(strings.TrimSpace(line))
	}
}

func (process *ManagedProcess) appendLog(message string) {
	if message == "" {
		return
	}
	process.mutex.Lock()
	defer process.mutex.Unlock()
	process.logs = append(process.logs, LogEntry{
		Timestamp: time.Now().UTC(),
		Message:   redact.Text(message),
	})
	if len(process.logs) > 1000 {
		process.logs = append([]LogEntry(nil), process.logs[len(process.logs)-1000:]...)
	}
}

var _ io.Writer = (*managedLogWriter)(nil)
