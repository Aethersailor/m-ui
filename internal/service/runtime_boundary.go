package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/operation"
	"github.com/Aethersailor/m-ui/internal/publisher"
	"github.com/Aethersailor/m-ui/internal/store"
)

// RuntimeBoundary is the single lifecycle boundary for a native or managed
// Mihomo process. A successful start/restart/finalize health-check is the only
// operation which may clear the durable Mihomo endpoint pending marker.
type RuntimeBoundary struct {
	store       *store.ManagedStore
	controller  mihomo.CoreController
	process     mihomo.CoreProcess
	coordinator *operation.Coordinator
	healthLimit time.Duration
	healthStep  time.Duration
}

type RuntimeBoundaryOptions struct {
	Store          *store.ManagedStore
	Controller     mihomo.CoreController
	Process        mihomo.CoreProcess
	Coordinator    *operation.Coordinator
	HealthTimeout  time.Duration
	HealthInterval time.Duration
}

func NewRuntimeBoundary(options RuntimeBoundaryOptions) (*RuntimeBoundary, error) {
	switch {
	case options.Store == nil:
		return nil, errors.New("managed store is required")
	case options.Controller == nil:
		return nil, errors.New("mihomo Controller is required")
	case options.Process == nil:
		return nil, errors.New("mihomo process adapter is required")
	case options.Coordinator == nil:
		return nil, errors.New("runtime operation coordinator is required")
	}
	if options.HealthTimeout <= 0 {
		options.HealthTimeout = 10 * time.Second
	}
	if options.HealthInterval <= 0 {
		options.HealthInterval = 100 * time.Millisecond
	}
	return &RuntimeBoundary{
		store:       options.Store,
		controller:  options.Controller,
		process:     options.Process,
		coordinator: options.Coordinator,
		healthLimit: options.HealthTimeout,
		healthStep:  options.HealthInterval,
	}, nil
}

// Run acquires the cross-process coordinator before applying a lifecycle
// action. The CLI uses this method; the HTTP manager uses runLocked after it
// has acquired the coordinator to preserve its fail-fast busy response.
func (boundary *RuntimeBoundary) Run(
	ctx context.Context,
	action string,
) error {
	release, err := boundary.coordinator.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return boundary.runLocked(ctx, action)
}

func (boundary *RuntimeBoundary) Start(ctx context.Context) error {
	release, err := boundary.coordinator.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return boundary.startLocked(ctx)
}

func (boundary *RuntimeBoundary) Restart(ctx context.Context) error {
	release, err := boundary.coordinator.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return boundary.restartLocked(ctx)
}

// Finalize verifies a process that was started by an external service manager
// and then clears only the Mihomo portion of the pending endpoint state. It
// deliberately never starts or restarts the process itself.
func (boundary *RuntimeBoundary) Finalize(ctx context.Context) error {
	release, err := boundary.coordinator.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return boundary.finalizeLocked(ctx)
}

// FinalizeLocked is used by the native service finalizer after app-level
// preflight has acquired the coordinator before opening SQLite. The caller
// must release that coordinator lease after this method returns.
func (boundary *RuntimeBoundary) FinalizeLocked(ctx context.Context) error {
	return boundary.finalizeLocked(ctx)
}

func (boundary *RuntimeBoundary) runLocked(
	ctx context.Context,
	action string,
) error {
	switch action {
	case "start":
		return boundary.startLocked(ctx)
	case "stop":
		return boundary.process.Stop(ctx)
	case "restart":
		return boundary.restartLocked(ctx)
	case "reload":
		required, err := boundary.store.MihomoRestartRequired(ctx)
		if err != nil {
			return err
		}
		if required {
			return publisher.ErrMihomoRestartRequired
		}
		return boundary.reloadWithHealth(ctx)
	default:
		return fmt.Errorf("unsupported Mihomo runtime action %q", action)
	}
}

func (boundary *RuntimeBoundary) startLocked(ctx context.Context) error {
	expected, generation, pending, err := boundary.mihomoRestartExpectation(ctx)
	if err != nil {
		return err
	}
	if pending {
		active, activeErr := boundary.process.IsActive(ctx)
		if activeErr != nil {
			return activeErr
		}
		if active {
			// Starting an already active service is a no-op and cannot be the
			// explicit boundary which applies a new controller endpoint.
			return publisher.ErrMihomoRestartRequired
		}
	}
	if err := boundary.startWithHealth(ctx); err != nil {
		return err
	}
	if !pending {
		return nil
	}
	return boundary.store.ClearEndpointRestartRequirements(
		ctx,
		false,
		true,
		generation,
		expected,
	)
}

func (boundary *RuntimeBoundary) restartLocked(ctx context.Context) error {
	expected, generation, pending, err := boundary.mihomoRestartExpectation(ctx)
	if err != nil {
		return err
	}
	if err := boundary.restartWithHealth(ctx); err != nil {
		return err
	}
	if !pending {
		return nil
	}
	return boundary.store.ClearEndpointRestartRequirements(
		ctx,
		false,
		true,
		generation,
		expected,
	)
}

func (boundary *RuntimeBoundary) startWithHealth(ctx context.Context) error {
	if attemptProcess, ok := boundary.process.(mihomo.AttemptProcess); ok {
		attempt, err := attemptProcess.StartAttempt(ctx)
		if err != nil {
			return err
		}
		if err := boundary.waitHealthy(ctx); err != nil {
			return errors.Join(err, attemptProcess.AbortAttempt(attempt))
		}
		return nil
	}
	if err := boundary.process.Start(ctx); err != nil {
		return err
	}
	return boundary.waitHealthy(ctx)
}

func (boundary *RuntimeBoundary) restartWithHealth(ctx context.Context) error {
	if attemptProcess, ok := boundary.process.(mihomo.AttemptProcess); ok {
		attempt, err := attemptProcess.RestartAttempt(ctx)
		if err != nil {
			return err
		}
		if err := boundary.waitHealthy(ctx); err != nil {
			return errors.Join(err, attemptProcess.AbortAttempt(attempt))
		}
		return nil
	}
	if err := boundary.process.Restart(ctx); err != nil {
		return err
	}
	return boundary.waitHealthy(ctx)
}

func (boundary *RuntimeBoundary) reloadWithHealth(ctx context.Context) error {
	if attemptProcess, ok := boundary.process.(mihomo.AttemptProcess); ok {
		attempt, err := attemptProcess.ReloadAttempt(ctx)
		if err != nil {
			return err
		}
		if err := boundary.waitHealthy(ctx); err != nil {
			return errors.Join(err, attemptProcess.AbortAttempt(attempt))
		}
		return nil
	}
	if err := boundary.process.Reload(ctx); err != nil {
		return err
	}
	return boundary.waitHealthy(ctx)
}

func (boundary *RuntimeBoundary) finalizeLocked(ctx context.Context) error {
	expected, generation, pending, err := boundary.mihomoRestartExpectation(ctx)
	if err != nil {
		return err
	}
	active, err := boundary.process.IsActive(ctx)
	if err != nil {
		return err
	}
	if !active {
		return errors.New("Mihomo process is not active after service start")
	}
	if err := boundary.waitHealthy(ctx); err != nil {
		return err
	}
	if !pending {
		return nil
	}
	return boundary.store.ClearEndpointRestartRequirements(
		ctx,
		false,
		true,
		generation,
		expected,
	)
}

func (boundary *RuntimeBoundary) mihomoRestartExpectation(
	ctx context.Context,
) (store.EndpointSettings, int64, bool, error) {
	state, err := boundary.store.EndpointSettings(ctx)
	if err != nil {
		return store.EndpointSettings{}, 0, false, err
	}
	if state.Pending != nil && state.Pending.RequiresMihomoRestart {
		return state.Pending.EndpointSettings, state.Pending.Generation, true, nil
	}
	return state.Active, state.Active.Generation, false, nil
}

func (boundary *RuntimeBoundary) waitHealthy(ctx context.Context) error {
	healthContext, cancel := context.WithTimeout(ctx, boundary.healthLimit)
	defer cancel()
	for {
		active, processErr := boundary.process.IsActive(healthContext)
		_, controllerErr := boundary.controller.Version(healthContext)
		if processErr == nil && active && controllerErr == nil {
			return nil
		}
		timer := time.NewTimer(boundary.healthStep)
		select {
		case <-healthContext.Done():
			timer.Stop()
			return errors.New("Mihomo did not become healthy after lifecycle action")
		case <-timer.C:
		}
	}
}
