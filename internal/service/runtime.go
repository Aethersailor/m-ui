package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/redact"
)

type RuntimeMonitorOptions struct {
	Interval         time.Duration
	ErrorLogInterval time.Duration
	Clock            func() time.Time
	Logger           *slog.Logger
}

type RuntimeMonitor struct {
	controller       mihomo.CoreController
	process          mihomo.CoreProcess
	interval         time.Duration
	errorLogInterval time.Duration
	clock            func() time.Time
	logger           *slog.Logger
	statusMutex      sync.RWMutex
	status           RuntimeStatus
	logMutex         sync.Mutex
	lastErrorLog     time.Time
}

func NewRuntimeMonitor(
	controller mihomo.CoreController,
	process mihomo.CoreProcess,
	options RuntimeMonitorOptions,
) (*RuntimeMonitor, error) {
	if controller == nil {
		return nil, fmt.Errorf("Mihomo Controller is required")
	}
	if process == nil {
		return nil, fmt.Errorf("Mihomo process adapter is required")
	}
	if options.Interval <= 0 {
		options.Interval = 2 * time.Second
	}
	if options.ErrorLogInterval <= 0 {
		options.ErrorLogInterval = time.Minute
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &RuntimeMonitor{
		controller:       controller,
		process:          process,
		interval:         options.Interval,
		errorLogInterval: options.ErrorLogInterval,
		clock:            options.Clock,
		logger:           options.Logger,
	}, nil
}

func (monitor *RuntimeMonitor) Run(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	monitor.CollectOnce(ctx)
	ticker := time.NewTicker(monitor.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			monitor.CollectOnce(ctx)
		}
	}
}

func (monitor *RuntimeMonitor) CollectOnce(ctx context.Context) RuntimeStatus {
	observedAt := monitor.clock().UTC()
	active, err := monitor.process.IsActive(ctx)
	if err != nil {
		return monitor.recordOffline(
			observedAt,
			fmt.Errorf("check Mihomo process: %w", err),
		)
	}
	if !active {
		return monitor.store(RuntimeStatus{ObservedAt: observedAt})
	}

	version, err := monitor.controller.Version(ctx)
	if err != nil {
		return monitor.recordOffline(
			observedAt,
			fmt.Errorf("collect Mihomo version: %w", err),
		)
	}
	traffic, err := monitor.controller.Traffic(ctx)
	if err != nil {
		return monitor.recordOffline(
			observedAt,
			fmt.Errorf("collect Mihomo traffic: %w", err),
		)
	}
	memory, err := monitor.controller.Memory(ctx)
	if err != nil {
		return monitor.recordOffline(
			observedAt,
			fmt.Errorf("collect Mihomo memory: %w", err),
		)
	}
	connections, err := monitor.controller.Connections(ctx)
	if err != nil {
		return monitor.recordOffline(
			observedAt,
			fmt.Errorf("collect Mihomo connections: %w", err),
		)
	}
	return monitor.store(RuntimeStatus{
		Active:          true,
		Version:         version,
		Traffic:         traffic,
		Memory:          memory,
		ConnectionCount: len(connections.Connections),
		DownloadTotal:   connections.DownloadTotal,
		UploadTotal:     connections.UploadTotal,
		ObservedAt:      observedAt,
	})
}

func (monitor *RuntimeMonitor) Snapshot() RuntimeStatus {
	monitor.statusMutex.RLock()
	defer monitor.statusMutex.RUnlock()
	return monitor.status
}

func (monitor *RuntimeMonitor) recordOffline(
	observedAt time.Time,
	err error,
) RuntimeStatus {
	status := monitor.store(RuntimeStatus{ObservedAt: observedAt})
	if err == nil {
		return status
	}
	monitor.logMutex.Lock()
	defer monitor.logMutex.Unlock()
	if !monitor.lastErrorLog.IsZero() &&
		observedAt.Sub(monitor.lastErrorLog) < monitor.errorLogInterval {
		return status
	}
	monitor.lastErrorLog = observedAt
	monitor.logger.Warn(
		"runtime status collection failed; Mihomo marked offline",
		"error",
		redact.Text(err.Error()),
	)
	return status
}

func (monitor *RuntimeMonitor) store(status RuntimeStatus) RuntimeStatus {
	monitor.statusMutex.Lock()
	defer monitor.statusMutex.Unlock()
	monitor.status = status
	return status
}
