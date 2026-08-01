package operation

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"time"
)

var ErrBusy = errors.New("a Mihomo runtime operation is already in progress")

// Coordinator serializes all operations that can change the published
// configuration, the managed core, or the Mihomo process. A channel-backed
// semaphore makes waiting context-cancellable and avoids goroutine leaks.
type Coordinator struct {
	token    chan struct{}
	active   atomic.Bool
	lockPath string
}

func NewCoordinator() *Coordinator {
	coordinator := &Coordinator{token: make(chan struct{}, 1)}
	coordinator.token <- struct{}{}
	return coordinator
}

func NewFileCoordinator(lockPath string) (*Coordinator, error) {
	if !filepath.IsAbs(lockPath) {
		return nil, errors.New("runtime operation lock path must be absolute")
	}
	coordinator := NewCoordinator()
	coordinator.lockPath = filepath.Clean(lockPath)
	return coordinator, nil
}

func (coordinator *Coordinator) Acquire(
	ctx context.Context,
) (func(), error) {
	if coordinator == nil {
		return nil, errors.New("runtime operation coordinator is required")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-coordinator.token:
	}
	for {
		unlock, busy, err := tryPlatformFileLock(coordinator.lockPath)
		if err != nil {
			coordinator.releaseLocal()
			return nil, err
		}
		if !busy {
			return coordinator.activate(unlock), nil
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			coordinator.releaseLocal()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (coordinator *Coordinator) TryAcquire() (func(), error) {
	if coordinator == nil {
		return nil, errors.New("runtime operation coordinator is required")
	}
	select {
	case <-coordinator.token:
		unlock, busy, err := tryPlatformFileLock(coordinator.lockPath)
		if err != nil {
			coordinator.releaseLocal()
			return nil, err
		}
		if busy {
			coordinator.releaseLocal()
			return nil, ErrBusy
		}
		return coordinator.activate(unlock), nil
	default:
		return nil, ErrBusy
	}
}

func (coordinator *Coordinator) Active() bool {
	return coordinator != nil && coordinator.active.Load()
}

func (coordinator *Coordinator) activate(unlock func()) func() {
	coordinator.active.Store(true)
	var released atomic.Bool
	return func() {
		if released.CompareAndSwap(false, true) {
			if unlock != nil {
				unlock()
			}
			coordinator.active.Store(false)
			coordinator.token <- struct{}{}
		}
	}
}

func (coordinator *Coordinator) releaseLocal() {
	coordinator.token <- struct{}{}
}
