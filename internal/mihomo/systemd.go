package mihomo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Aethersailor/m-ui/internal/redact"
)

const (
	managedServiceName = "mihomo.service"
	systemctlPath      = "/usr/bin/systemctl"
	journalctlPath     = "/usr/bin/journalctl"
	sudoPath           = "/usr/bin/sudo"
)

type SystemdProcess struct {
	executor commandExecutor
}

func NewSystemdProcess(serviceName string) (*SystemdProcess, error) {
	if serviceName != managedServiceName {
		return nil, fmt.Errorf("service name must be %q", managedServiceName)
	}
	return &SystemdProcess{executor: osCommandExecutor{}}, nil
}

func (process *SystemdProcess) IsActive(ctx context.Context) (bool, error) {
	_, err := process.executor.Run(
		ctx,
		5*time.Second,
		defaultOutputLimit,
		systemctlPath,
		"is-active",
		"--quiet",
		managedServiceName,
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, errCommandExit) {
		return false, nil
	}
	return false, errors.New("check Mihomo systemd service state")
}

func (process *SystemdProcess) Start(ctx context.Context) error {
	return process.lifecycle(ctx, "start")
}

func (process *SystemdProcess) Stop(ctx context.Context) error {
	return process.lifecycle(ctx, "stop")
}

func (process *SystemdProcess) Restart(ctx context.Context) error {
	return process.lifecycle(ctx, "restart")
}

func (process *SystemdProcess) Reload(ctx context.Context) error {
	return process.lifecycle(ctx, "reload")
}

func (process *SystemdProcess) RecentLogs(ctx context.Context, limit int) ([]LogEntry, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("log limit must be between 1 and 1000")
	}
	output, err := process.executor.Run(
		ctx,
		5*time.Second,
		256*1024,
		journalctlPath,
		"-u",
		managedServiceName,
		"-n",
		strconv.Itoa(limit),
		"--no-pager",
		"-o",
		"short-iso-precise",
	)
	if err != nil {
		return nil, errors.New("read Mihomo systemd logs")
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	entries := make([]LogEntry, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			entries = append(entries, LogEntry{Message: redact.Text(line)})
		}
	}
	return entries, nil
}

func (process *SystemdProcess) lifecycle(ctx context.Context, action string) error {
	_, err := process.executor.Run(
		ctx,
		20*time.Second,
		defaultOutputLimit,
		sudoPath,
		"-n",
		systemctlPath,
		action,
		managedServiceName,
	)
	if err != nil {
		return fmt.Errorf("%s Mihomo systemd service", action)
	}
	return nil
}
