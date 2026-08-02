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

	mutex             sync.Mutex
	command           *exec.Cmd
	commandGeneration uint64
	nextGeneration    uint64
	active            bool
	desired           bool
	restarting        bool
	// exitGeneration and recoveryGeneration make the recovery owner explicit:
	// an exit observed while a boundary callback is running is a new event, not
	// something the callback may accidentally lose when it returns successfully.
	exitGeneration     uint64
	recoveryGeneration uint64
	recoveryPending    bool
	recovery           func(context.Context) error
	recoveryBackoff    time.Duration
	logs               []LogEntry
}

// RecoveryConfigurer lets the application install the single managed-mode
// restart boundary after all of the durable store, Controller, and operation
// coordinator dependencies have been assembled. The callback must own the
// full RuntimeBoundary: coordinator acquisition, process/controller health,
// and endpoint-generation CAS.
type RecoveryConfigurer interface {
	SetRecovery(func(context.Context) error) error
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
		rootContext:     ctx,
		binaryPath:      binaryPath,
		configPath:      configPath,
		logger:          logger,
		recoveryBackoff: 250 * time.Millisecond,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = process.Stop(shutdown)
	}()
	return process, nil
}

func (process *ManagedProcess) IsActive(ctx context.Context) (bool, error) {
	process.mutex.Lock()
	active := process.active
	process.mutex.Unlock()
	if active {
		return true, nil
	}
	return managedProcessActive(ctx, process.binaryPath, process.configPath)
}

// SetRecovery installs the application-owned restart boundary. It must be
// configured before the first managed start; allowing the low-level process
// adapter to restart itself would bypass RuntimeBoundary and its health/CAS
// invariants.
func (process *ManagedProcess) SetRecovery(
	recovery func(context.Context) error,
) error {
	if recovery == nil {
		return errors.New("managed process recovery callback is required")
	}
	process.mutex.Lock()
	defer process.mutex.Unlock()
	if process.active || process.desired {
		return errors.New("managed process recovery must be configured before start")
	}
	process.recovery = recovery
	return nil
}

func (process *ManagedProcess) Start(ctx context.Context) error {
	_, err := process.StartAttempt(ctx)
	return err
}

// StartAttempt starts Mihomo and returns an opaque token for the exact child
// process. The RuntimeBoundary uses that token to abort a failed health-check
// without ever targeting a later process generation.
func (process *ManagedProcess) StartAttempt(
	ctx context.Context,
) (ProcessAttempt, error) {
	process.mutex.Lock()
	if process.active {
		process.desired = true
		process.mutex.Unlock()
		return ProcessAttempt{}, nil
	}
	if err := process.rootContext.Err(); err != nil {
		process.mutex.Unlock()
		return ProcessAttempt{}, errors.New("managed Mihomo supervisor is shutting down")
	}
	binaryPath := process.binaryPath
	configPath := process.configPath
	process.mutex.Unlock()

	// A previous m-ui instance can leave Mihomo alive after an unclean exit.
	// Refuse to create a second process; the application-level coordinator and
	// service boundary decide how that state is recovered.
	active, err := managedProcessActive(ctx, binaryPath, configPath)
	if err != nil {
		return ProcessAttempt{}, err
	}
	if active {
		return ProcessAttempt{}, errors.New("managed Mihomo process is already active")
	}

	process.mutex.Lock()
	defer process.mutex.Unlock()
	if process.active {
		process.desired = true
		return ProcessAttempt{}, nil
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
	_, err := process.RestartAttempt(ctx)
	return err
}

func (process *ManagedProcess) Reload(ctx context.Context) error {
	// Restart is the portable managed-mode reload boundary. Configuration
	// publication still verifies the candidate and health-checks the result.
	_, err := process.ReloadAttempt(ctx)
	return err
}

func (process *ManagedProcess) RestartAttempt(
	ctx context.Context,
) (ProcessAttempt, error) {
	if err := process.Stop(ctx); err != nil {
		return ProcessAttempt{}, err
	}
	return process.StartAttempt(ctx)
}

func (process *ManagedProcess) ReloadAttempt(
	ctx context.Context,
) (ProcessAttempt, error) {
	return process.RestartAttempt(ctx)
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

func (process *ManagedProcess) startLocked(
	ctx context.Context,
) (ProcessAttempt, error) {
	if err := ctx.Err(); err != nil {
		return ProcessAttempt{}, err
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
		return ProcessAttempt{}, errors.New("start managed Mihomo process")
	}
	process.nextGeneration++
	process.command = command
	process.commandGeneration = process.nextGeneration
	process.active = true
	go process.wait(command)
	return ProcessAttempt{generation: process.commandGeneration}, nil
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
	process.commandGeneration = 0
	process.exitGeneration++
	desired := process.desired && process.rootContext.Err() == nil
	if !desired {
		process.mutex.Unlock()
		return
	}
	if process.restarting {
		process.recoveryPending = true
		process.mutex.Unlock()
		return
	}
	if process.recovery == nil {
		process.desired = false
		process.mutex.Unlock()
		process.logger.Error(
			"managed Mihomo exited without an application recovery boundary",
		)
		return
	}
	process.restarting = true
	process.recoveryGeneration = process.exitGeneration
	process.recoveryPending = false
	process.mutex.Unlock()

	if err != nil {
		process.logger.Warn("managed Mihomo process exited", "error", "process exited")
	}
	go process.restartLoop()
}

func (process *ManagedProcess) restartLoop() {
	process.mutex.Lock()
	backoff := process.recoveryBackoff
	process.mutex.Unlock()
	for attempt := 0; attempt < 5; attempt++ {
		if backoff > 0 {
			timer := time.NewTimer(backoff)
			select {
			case <-process.rootContext.Done():
				timer.Stop()
				process.finishRecoveryLoop(false)
				return
			case <-timer.C:
			}
		}
		process.mutex.Lock()
		if !process.desired || process.rootContext.Err() != nil {
			process.mutex.Unlock()
			process.finishRecoveryLoop(false)
			return
		}
		// An exit caused while the previous boundary attempt was being
		// stopped/replaced belongs to the next attempt. The successful
		// callback below will re-check the generation and active state while
		// holding this same mutex.
		process.recoveryPending = false
		process.recoveryGeneration = process.exitGeneration
		recovery := process.recovery
		process.mutex.Unlock()
		if recovery == nil {
			process.logger.Error(
				"managed Mihomo recovery boundary disappeared",
			)
			process.mutex.Lock()
			process.desired = false
			process.mutex.Unlock()
			process.finishRecoveryLoop(false)
			return
		}
		err := recovery(process.rootContext)
		if err == nil {
			process.mutex.Lock()
			stillDesired := process.desired && process.rootContext.Err() == nil
			active := process.active
			newExit := process.exitGeneration != process.recoveryGeneration
			pending := process.recoveryPending || newExit
			if stillDesired && (!active || pending) {
				// Keep the current recovery owner. This closes the window where
				// wait observes an immediate post-health-check exit while the
				// callback is returning: the next attempt remains scheduled.
				process.recoveryPending = false
				process.recoveryGeneration = process.exitGeneration
				process.mutex.Unlock()
				continue
			}
			process.restarting = false
			process.recoveryPending = false
			process.mutex.Unlock()
			return
		}
		process.logger.Warn(
			"managed Mihomo recovery boundary failed",
			"error", redact.Text(err.Error()),
		)
		backoff *= 2
		if backoff > 5*time.Second {
			backoff = 5 * time.Second
		}
	}
	process.mutex.Lock()
	process.desired = false
	process.mutex.Unlock()
	process.finishRecoveryLoop(false)
	process.logger.Error("managed Mihomo restart limit reached")
}

func (process *ManagedProcess) finishRecoveryLoop(clearDesired bool) {
	process.mutex.Lock()
	if clearDesired {
		process.desired = false
	}
	process.restarting = false
	process.recoveryPending = false
	process.mutex.Unlock()
}

// AbortAttempt terminates only the command represented by attempt. It keeps
// desired=true so the supervisor's retry loop remains responsible for the
// next application-owned recovery attempt.
func (process *ManagedProcess) AbortAttempt(attempt ProcessAttempt) error {
	if attempt.generation == 0 {
		return nil
	}
	process.mutex.Lock()
	if process.commandGeneration != attempt.generation ||
		process.command == nil || process.command.Process == nil ||
		!process.active {
		process.mutex.Unlock()
		return nil
	}
	command := process.command
	process.mutex.Unlock()

	if err := signalTerminate(command.Process); err != nil {
		if killErr := command.Process.Kill(); killErr != nil {
			return errors.New("terminate failed managed Mihomo attempt")
		}
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		process.mutex.Lock()
		sameAttempt := process.command == command &&
			process.commandGeneration == attempt.generation &&
			process.active
		process.mutex.Unlock()
		if !sameAttempt {
			return nil
		}
		select {
		case <-deadline.C:
			if killErr := command.Process.Kill(); killErr != nil {
				return errors.New("kill failed managed Mihomo attempt")
			}
			return errors.New("managed Mihomo attempt did not exit after termination")
		case <-ticker.C:
		}
	}
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
var _ AttemptProcess = (*ManagedProcess)(nil)
