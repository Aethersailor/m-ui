package operation

import (
	"context"
	"errors"
	"testing"
)

func TestCoordinatorSerializesAndCancelsWaiters(t *testing.T) {
	t.Parallel()
	coordinator := NewCoordinator()
	release, err := coordinator.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !coordinator.Active() {
		t.Fatal("coordinator is not active after acquisition")
	}
	if _, err := coordinator.TryAcquire(); !errors.Is(err, ErrBusy) {
		t.Fatalf("TryAcquire() error = %v, want ErrBusy", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.Acquire(cancelled); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("Acquire(cancelled) error = %v", err)
	}
	release()
	release()
	if coordinator.Active() {
		t.Fatal("coordinator remained active after release")
	}
	nextRelease, err := coordinator.TryAcquire()
	if err != nil {
		t.Fatal(err)
	}
	nextRelease()
}
