//go:build !windows

package operation

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestFileCoordinatorsExcludeIndependentInstances(t *testing.T) {
	t.Parallel()
	lockPath := filepath.Join(t.TempDir(), "runtime.lock")
	first, err := NewFileCoordinator(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFileCoordinator(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	release, err := first.TryAcquire()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.TryAcquire(); !errors.Is(err, ErrBusy) {
		t.Fatalf("second TryAcquire() error = %v, want ErrBusy", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := second.Acquire(cancelled); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("cancelled Acquire() error = %v", err)
	}
	release()
	secondRelease, err := second.TryAcquire()
	if err != nil {
		t.Fatal(err)
	}
	secondRelease()
}
