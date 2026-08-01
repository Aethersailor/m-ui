package scheduler

import (
	"context"
	"log/slog"
	"time"

	coremanagement "github.com/Aethersailor/m-ui/internal/core"
	"github.com/Aethersailor/m-ui/internal/redact"
)

type CoreUpdateManager interface {
	Due(context.Context, time.Time) (bool, error)
	Update(context.Context, string) (coremanagement.Manifest, bool, error)
}

type SchedulerClock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

type CoreOptions struct {
	Clock        SchedulerClock
	PollInterval time.Duration
	MaxBackoff   time.Duration
	Logger       *slog.Logger
}

type Core struct {
	manager      CoreUpdateManager
	clock        SchedulerClock
	pollInterval time.Duration
	maxBackoff   time.Duration
	logger       *slog.Logger
	wake         chan struct{}
}

type realSchedulerClock struct{}

func (realSchedulerClock) Now() time.Time {
	return time.Now().UTC()
}

func (realSchedulerClock) After(duration time.Duration) <-chan time.Time {
	return time.After(duration)
}

func NewCore(manager CoreUpdateManager, options CoreOptions) (*Core, error) {
	if manager == nil {
		return nil, errSchedulerDependency
	}
	if options.Clock == nil {
		options.Clock = realSchedulerClock{}
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Minute
	}
	if options.MaxBackoff <= 0 {
		options.MaxBackoff = time.Hour
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Core{
		manager:      manager,
		clock:        options.Clock,
		pollInterval: options.PollInterval,
		maxBackoff:   options.MaxBackoff,
		logger:       options.Logger,
		wake:         make(chan struct{}, 1),
	}, nil
}

func (scheduler *Core) Run(ctx context.Context) {
	backoff := time.Duration(0)
	for {
		due, err := scheduler.manager.Due(ctx, scheduler.clock.Now().UTC())
		if err == nil && due {
			_, _, err = scheduler.manager.Update(ctx, "")
		}
		wait := scheduler.pollInterval
		if err != nil {
			if backoff == 0 {
				backoff = scheduler.pollInterval
			} else {
				backoff *= 2
			}
			if backoff > scheduler.maxBackoff {
				backoff = scheduler.maxBackoff
			}
			wait = backoff
			scheduler.logger.Warn(
				"automatic Mihomo core update check failed",
				"error",
				redact.Text(err.Error()),
			)
		} else {
			backoff = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-scheduler.wake:
		case <-scheduler.clock.After(wait):
		}
	}
}

func (scheduler *Core) Wake() {
	select {
	case scheduler.wake <- struct{}{}:
	default:
	}
}
