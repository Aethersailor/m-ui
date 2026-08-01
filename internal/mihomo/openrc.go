package mihomo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Aethersailor/m-ui/internal/redact"
)

const (
	openRCServiceName = "mihomo"
	rcServicePath     = "/sbin/rc-service"
	doasPath          = "/usr/bin/doas"
	openRCLogPath     = "/var/log/mihomo.log"
)

type OpenRCProcess struct {
	executor commandExecutor
}

func NewOpenRCProcess(serviceName string) (*OpenRCProcess, error) {
	if serviceName != managedServiceName && serviceName != openRCServiceName {
		return nil, errors.New("OpenRC service name must be mihomo")
	}
	return &OpenRCProcess{executor: osCommandExecutor{}}, nil
}

func (process *OpenRCProcess) IsActive(ctx context.Context) (bool, error) {
	_, err := process.executor.Run(
		ctx,
		5*time.Second,
		defaultOutputLimit,
		rcServicePath,
		openRCServiceName,
		"status",
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, errCommandExit) {
		return false, nil
	}
	return false, errors.New("check Mihomo OpenRC service state")
}

func (process *OpenRCProcess) Start(ctx context.Context) error {
	return process.lifecycle(ctx, "start")
}

func (process *OpenRCProcess) Stop(ctx context.Context) error {
	return process.lifecycle(ctx, "stop")
}

func (process *OpenRCProcess) Restart(ctx context.Context) error {
	return process.lifecycle(ctx, "restart")
}

func (process *OpenRCProcess) Reload(ctx context.Context) error {
	return process.lifecycle(ctx, "reload")
}

func (process *OpenRCProcess) RecentLogs(
	_ context.Context,
	limit int,
) ([]LogEntry, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("log limit must be between 1 and 1000")
	}
	content, err := os.ReadFile(openRCLogPath)
	if err != nil {
		return nil, errors.New("read Mihomo OpenRC logs")
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	entries := make([]LogEntry, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			entries = append(entries, LogEntry{Message: redact.Text(line)})
		}
	}
	return entries, nil
}

func (process *OpenRCProcess) lifecycle(ctx context.Context, action string) error {
	switch action {
	case "start", "stop", "restart", "reload":
	default:
		return fmt.Errorf("unsupported OpenRC action %q", action)
	}
	_, err := process.executor.Run(
		ctx,
		20*time.Second,
		defaultOutputLimit,
		doasPath,
		"-n",
		rcServicePath,
		openRCServiceName,
		action,
	)
	if err != nil {
		return fmt.Errorf("%s Mihomo OpenRC service", action)
	}
	return nil
}
