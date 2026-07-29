package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/publisher"
)

func TestBackgroundServicesWaitForStartupReconciliation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reconciler := &blockingStartupReconciler{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	runtime := &signalRunner{started: make(chan struct{})}
	expiry := &signalRunner{started: make(chan struct{})}
	result := make(chan error, 1)

	go func() {
		result <- startBackgroundServices(
			ctx,
			reconciler,
			staticSystemStateReader{},
			runtime,
			expiry,
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		)
	}()

	<-reconciler.entered
	assertNotStarted(t, runtime.started, "runtime monitor")
	assertNotStarted(t, expiry.started, "expiry scheduler")
	close(reconciler.release)
	if err := <-result; err != nil {
		t.Fatalf("startBackgroundServices() error = %v", err)
	}
	assertStarted(t, runtime.started, "runtime monitor")
	assertStarted(t, expiry.started, "expiry scheduler")
}

func TestBackgroundServicesSkipExpiryWhenReconciliationDegrades(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := &signalRunner{started: make(chan struct{})}
	expiry := &signalRunner{started: make(chan struct{})}

	err := startBackgroundServices(
		ctx,
		staticStartupReconciler{err: publisher.ErrStartupDegraded},
		staticSystemStateReader{
			state: domain.SystemState{Degraded: true},
		},
		runtime,
		expiry,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("startBackgroundServices() error = %v", err)
	}
	assertStarted(t, runtime.started, "runtime monitor")
	assertNotStarted(t, expiry.started, "expiry scheduler")
}

func assertNotStarted(t *testing.T, started <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-started:
		t.Fatalf("%s started before reconciliation completed", name)
	default:
	}
}

func assertStarted(t *testing.T, started <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("%s did not start", name)
	}
}

type blockingStartupReconciler struct {
	entered chan struct{}
	release chan struct{}
}

func (reconciler *blockingStartupReconciler) ReconcileStartup(context.Context) error {
	close(reconciler.entered)
	<-reconciler.release
	return nil
}

type staticStartupReconciler struct {
	err error
}

func (reconciler staticStartupReconciler) ReconcileStartup(context.Context) error {
	return reconciler.err
}

type staticSystemStateReader struct {
	state domain.SystemState
	err   error
}

func (reader staticSystemStateReader) SystemState(
	context.Context,
) (domain.SystemState, error) {
	return reader.state, reader.err
}

type signalRunner struct {
	started chan struct{}
}

func (runner *signalRunner) Run(ctx context.Context) {
	close(runner.started)
	<-ctx.Done()
}
