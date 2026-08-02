package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/publisher"
	"github.com/Aethersailor/m-ui/internal/redact"
	"github.com/Aethersailor/m-ui/internal/store"
)

var errSchedulerDependency = errors.New("scheduler dependency is required")

var errNoExpiredUsers = errors.New("no expired users require publication")

type configurationPublisher interface {
	Publish(context.Context, publisher.Request) (domain.Revision, error)
}

type Options struct {
	Interval         time.Duration
	ErrorLogInterval time.Duration
	Clock            func() time.Time
	Logger           *slog.Logger
	SafetyGate       publisher.SafetyGate
}

type BatchResult struct {
	BatchTime         time.Time
	UsersDisabled     int
	ListenersDisabled int
	Revision          domain.Revision
}

type Expiry struct {
	publisher        configurationPublisher
	interval         time.Duration
	errorLogInterval time.Duration
	clock            func() time.Time
	logger           *slog.Logger
	safetyGate       publisher.SafetyGate
	logMutex         sync.Mutex
	lastErrorLog     time.Time
}

func NewExpiry(
	configurationPublisher configurationPublisher,
	options Options,
) (*Expiry, error) {
	if configurationPublisher == nil {
		return nil, errors.New("configuration publisher is required")
	}
	if options.Interval <= 0 {
		options.Interval = time.Minute
	}
	if options.ErrorLogInterval <= 0 {
		options.ErrorLogInterval = 5 * time.Minute
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Expiry{
		publisher:        configurationPublisher,
		interval:         options.Interval,
		errorLogInterval: options.ErrorLogInterval,
		clock:            options.Clock,
		logger:           options.Logger,
		safetyGate:       options.SafetyGate,
	}, nil
}

func (scheduler *Expiry) Run(ctx context.Context) {
	scheduler.runAndReport(ctx)
	ticker := time.NewTicker(scheduler.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scheduler.runAndReport(ctx)
		}
	}
}

func (scheduler *Expiry) RunOnce(
	ctx context.Context,
) (BatchResult, error) {
	if scheduler.safetyGate != nil {
		blocked, err := scheduler.safetyGate.SafetyBlocked(ctx)
		if err != nil {
			return BatchResult{}, fmt.Errorf("check fail-closed safety state: %w", err)
		}
		if blocked {
			return BatchResult{}, publisher.ErrDegraded
		}
	}
	batchTime := scheduler.clock().UTC()
	result := BatchResult{BatchTime: batchTime}
	revision, err := scheduler.publisher.Publish(ctx, publisher.Request{
		Reason:        "disable expired listener users",
		AuditAction:   "scheduler.expiry_batch",
		AuditResource: "listener_users",
		EffectiveAt:   &batchTime,
		AuditSummaryFunc: func() string {
			return fmt.Sprintf(
				"Disabled %d expired listener users and %d affected empty listeners.",
				result.UsersDisabled,
				result.ListenersDisabled,
			)
		},
		Mutate: func(
			ctx context.Context,
			transaction store.PublicationTransaction,
		) error {
			state, err := transaction.DesiredState(ctx, batchTime)
			if err != nil {
				return err
			}
			affectedListeners := make(map[string]struct{})
			for listenerIndex := range state.Listeners {
				listener := &state.Listeners[listenerIndex]
				for userIndex := range listener.Users {
					user := &listener.Users[userIndex]
					if !user.Enabled ||
						user.ExpiresAt == nil ||
						user.ExpiresAt.After(batchTime) {
						continue
					}
					user.Enabled = false
					user.UpdatedAt = batchTime
					affectedListeners[listener.ID] = struct{}{}
					result.UsersDisabled++
				}
			}
			if result.UsersDisabled == 0 {
				return errNoExpiredUsers
			}
			for listenerIndex := range state.Listeners {
				listener := &state.Listeners[listenerIndex]
				if _, affected := affectedListeners[listener.ID]; !affected {
					continue
				}
				if listener.Enabled &&
					len(listener.EffectiveUsers(batchTime)) == 0 {
					listener.Enabled = false
					listener.UpdatedAt = batchTime
					result.ListenersDisabled++
				}
			}
			state.AsOf = batchTime
			if err := state.Validate(); err != nil {
				return fmt.Errorf(
					"validate expiry batch candidate: %w",
					err,
				)
			}
			return transaction.ReplaceDesiredState(ctx, state)
		},
	})
	if errors.Is(err, errNoExpiredUsers) {
		return BatchResult{BatchTime: batchTime}, nil
	}
	if err != nil {
		return BatchResult{BatchTime: batchTime}, err
	}
	result.Revision = revision
	return result, nil
}

func (scheduler *Expiry) runAndReport(ctx context.Context) {
	result, err := scheduler.RunOnce(ctx)
	if err != nil {
		scheduler.logFailure(err)
		return
	}
	if result.UsersDisabled != 0 {
		scheduler.logger.Info(
			"expired listener users disabled",
			"users_disabled",
			result.UsersDisabled,
			"listeners_disabled",
			result.ListenersDisabled,
			"revision",
			result.Revision.RevisionNumber,
		)
	}
}

func (scheduler *Expiry) logFailure(err error) {
	now := scheduler.clock().UTC()
	scheduler.logMutex.Lock()
	defer scheduler.logMutex.Unlock()
	if !scheduler.lastErrorLog.IsZero() &&
		now.Sub(scheduler.lastErrorLog) < scheduler.errorLogInterval {
		return
	}
	scheduler.lastErrorLog = now
	scheduler.logger.Error(
		"expiry publication failed; expired credentials may remain active until a retry succeeds",
		"error",
		redact.Text(err.Error()),
	)
}
