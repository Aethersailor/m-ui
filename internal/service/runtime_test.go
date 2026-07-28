package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Aethersailor/m-ui/internal/mihomo"
)

func TestRuntimeMonitorCachesMetricsAndRateLimitsOfflineFailures(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	var logs bytes.Buffer
	controller := &runtimeController{}
	process := &runtimeProcess{active: true}
	monitor, err := NewRuntimeMonitor(
		controller,
		process,
		RuntimeMonitorOptions{
			Clock:            func() time.Time { return now },
			ErrorLogInterval: time.Minute,
			Logger: slog.New(slog.NewTextHandler(
				&logs,
				&slog.HandlerOptions{Level: slog.LevelDebug},
			)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	status := monitor.CollectOnce(context.Background())
	if !status.Active ||
		status.Version.Version != "v1.19.29" ||
		status.Traffic.Up != 1 ||
		status.Memory.InUse != 2 ||
		status.ConnectionCount != 1 ||
		status.DownloadTotal != 3 ||
		status.UploadTotal != 4 ||
		status.ObservedAt != now {
		t.Fatalf("collected status = %#v", status)
	}
	if snapshot := monitor.Snapshot(); snapshot != status {
		t.Fatalf("snapshot = %#v, want %#v", snapshot, status)
	}

	sensitiveUUID := "2bf189fe-ec56-497d-9069-68bf32c4425b"
	controller.err = errors.New("synthetic failure " + sensitiveUUID)
	now = now.Add(10 * time.Second)
	firstOffline := monitor.CollectOnce(context.Background())
	now = now.Add(10 * time.Second)
	secondOffline := monitor.CollectOnce(context.Background())
	if firstOffline.Active || secondOffline.Active {
		t.Fatal("failed collection did not mark Mihomo offline")
	}
	if strings.Contains(logs.String(), sensitiveUUID) {
		t.Fatal("runtime collection log exposed a synthetic UUID")
	}
	if count := strings.Count(
		logs.String(),
		"runtime status collection failed",
	); count != 1 {
		t.Fatalf("runtime failure log count = %d, want 1", count)
	}
}

type runtimeController struct {
	err error
}

func (controller *runtimeController) Version(
	context.Context,
) (mihomo.Version, error) {
	if controller.err != nil {
		return mihomo.Version{}, controller.err
	}
	return mihomo.Version{Meta: true, Version: "v1.19.29"}, nil
}

func (*runtimeController) Traffic(
	context.Context,
) (mihomo.TrafficSnapshot, error) {
	return mihomo.TrafficSnapshot{Up: 1}, nil
}

func (*runtimeController) Memory(
	context.Context,
) (mihomo.MemorySnapshot, error) {
	return mihomo.MemorySnapshot{InUse: 2}, nil
}

func (*runtimeController) Connections(
	context.Context,
) (mihomo.ConnectionsSnapshot, error) {
	return mihomo.ConnectionsSnapshot{
		DownloadTotal: 3,
		UploadTotal:   4,
		Connections:   []mihomo.Connection{{ID: "synthetic"}},
	}, nil
}

func (*runtimeController) Reload(context.Context, string) error { return nil }
func (*runtimeController) Restart(context.Context, string) error {
	return nil
}

type runtimeProcess struct {
	active bool
}

func (process *runtimeProcess) IsActive(context.Context) (bool, error) {
	return process.active, nil
}

func (*runtimeProcess) Start(context.Context) error   { return nil }
func (*runtimeProcess) Stop(context.Context) error    { return nil }
func (*runtimeProcess) Restart(context.Context) error { return nil }
func (*runtimeProcess) Reload(context.Context) error  { return nil }
func (*runtimeProcess) RecentLogs(
	context.Context,
	int,
) ([]mihomo.LogEntry, error) {
	return nil, nil
}
