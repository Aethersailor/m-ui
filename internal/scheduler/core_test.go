package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	coremanagement "github.com/Aethersailor/m-ui/internal/core"
	"github.com/Aethersailor/m-ui/internal/operation"
)

func TestCoreSchedulerUsesFakeClockWakeAndStops(t *testing.T) {
	t.Parallel()
	manager := &fakeCoreUpdateManager{
		due:     true,
		updates: make(chan struct{}, 2),
		checks:  make(chan struct{}, 3),
	}
	clock := newFakeSchedulerClock(time.Unix(100, 0))
	scheduler, err := NewCore(manager, CoreOptions{
		Clock:        clock,
		PollInterval: time.Minute,
		MaxBackoff:   4 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		scheduler.Run(ctx)
		close(done)
	}()
	<-manager.updates
	manager.setDue(false)
	scheduler.Wake()
	<-manager.checks
	cancel()
	<-done
}

func TestCoreSchedulerBoundsFailureBackoffWithoutSleeping(t *testing.T) {
	t.Parallel()
	manager := &fakeCoreUpdateManager{
		due:       true,
		updateErr: errors.New("synthetic network failure"),
		updates:   make(chan struct{}, 4),
		checks:    make(chan struct{}, 4),
	}
	clock := newFakeSchedulerClock(time.Unix(100, 0))
	scheduler, err := NewCore(manager, CoreOptions{
		Clock:        clock,
		PollInterval: time.Minute,
		MaxBackoff:   2 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go scheduler.Run(ctx)
	<-manager.updates
	clock.advanceWant(t, time.Minute)
	<-manager.updates
	clock.advanceWant(t, 2*time.Minute)
	<-manager.updates
	clock.advanceWant(t, 2*time.Minute)
}

func TestCoreSchedulerRetriesBusyOperationPromptlyWithoutBackoff(t *testing.T) {
	t.Parallel()
	manager := &fakeCoreUpdateManager{
		due:       true,
		updateErr: operation.ErrBusy,
		updates:   make(chan struct{}, 2),
		checks:    make(chan struct{}, 2),
	}
	clock := newFakeSchedulerClock(time.Unix(100, 0))
	scheduler, err := NewCore(manager, CoreOptions{
		Clock:        clock,
		PollInterval: time.Minute,
		MaxBackoff:   2 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go scheduler.Run(ctx)
	<-manager.updates
	clock.advanceWant(t, coreBusyRetryInterval)
	<-manager.updates
}

type fakeCoreUpdateManager struct {
	mutex     sync.Mutex
	due       bool
	updateErr error
	updates   chan struct{}
	checks    chan struct{}
}

func (manager *fakeCoreUpdateManager) Due(
	context.Context,
	time.Time,
) (bool, error) {
	manager.checks <- struct{}{}
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	return manager.due, nil
}

func (manager *fakeCoreUpdateManager) Update(
	context.Context,
	string,
) (coremanagement.Manifest, bool, error) {
	manager.updates <- struct{}{}
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	return coremanagement.Manifest{}, false, manager.updateErr
}

func (manager *fakeCoreUpdateManager) setDue(value bool) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	manager.due = value
}

type fakeSchedulerClock struct {
	mutex     sync.Mutex
	now       time.Time
	durations chan time.Duration
	waiters   chan chan time.Time
}

func newFakeSchedulerClock(now time.Time) *fakeSchedulerClock {
	return &fakeSchedulerClock{
		now:       now,
		durations: make(chan time.Duration, 8),
		waiters:   make(chan chan time.Time, 8),
	}
}

func (clock *fakeSchedulerClock) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return clock.now
}

func (clock *fakeSchedulerClock) After(duration time.Duration) <-chan time.Time {
	waiter := make(chan time.Time, 1)
	clock.durations <- duration
	clock.waiters <- waiter
	return waiter
}

func (clock *fakeSchedulerClock) advanceWant(
	t *testing.T,
	want time.Duration,
) {
	t.Helper()
	duration := <-clock.durations
	if duration != want {
		t.Fatalf("fake clock wait = %s, want %s", duration, want)
	}
	waiter := <-clock.waiters
	clock.mutex.Lock()
	clock.now = clock.now.Add(duration)
	now := clock.now
	clock.mutex.Unlock()
	waiter <- now
}
