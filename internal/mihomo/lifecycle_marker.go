package mihomo

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// RuntimeLifecycleMarkerPath is shared with the native service post-start
// hook. It marks a lifecycle operation initiated by m-ui itself so that the
// hook does not wait on the coordinator held by that same operation.
const RuntimeLifecycleMarkerPath = "/run/m-ui/mihomo-lifecycle.marker"

// RuntimeLifecycleMarkerProbe is a shared-locked inspection of a marker file.
// A probe is returned only when the marker exists but is not locked by a live
// lifecycle owner; the caller owns the shared observer lock until Close.
// Marker files are persistent lock files: their pathname is never unlinked
// after an owner releases it, which prevents an unlock/unlink inode-reuse
// window from creating two owners for one logical marker path.
type RuntimeLifecycleMarkerProbe struct {
	file *os.File
}

// ProbeRuntimeLifecycleMarker reports whether a live lifecycle owner holds
// the marker. If live is false and probe is non-nil, the marker is stale and
// the caller holds a shared observer lock until the probe is closed.
func ProbeRuntimeLifecycleMarker() (
	probe *RuntimeLifecycleMarkerProbe,
	live bool,
	err error,
) {
	return ProbeRuntimeLifecycleMarkerAt(RuntimeLifecycleMarkerPath)
}

// ProbeRuntimeLifecycleMarkerAt is the path-parameterized form used by tests
// and by the native finalizer preflight. It never creates a marker file and
// uses a shared observer lock, so concurrent observers cannot mistake one
// another for a live exclusive owner.
func ProbeRuntimeLifecycleMarkerAt(path string) (
	probe *RuntimeLifecycleMarkerProbe,
	live bool,
	err error,
) {
	file, err := openRuntimeLockFile(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	busy, err := trySharedRuntimeLockFile(file)
	if err != nil {
		_ = file.Close()
		return nil, false, err
	}
	if busy {
		_ = file.Close()
		return nil, true, nil
	}
	return &RuntimeLifecycleMarkerProbe{file: file}, false, nil
}

// Close releases a stale-marker observer lock without removing its path.
func (probe *RuntimeLifecycleMarkerProbe) Close() error {
	if probe == nil || probe.file == nil {
		return nil
	}
	unlockErr := unlockRuntimeLockFile(probe.file)
	closeErr := probe.file.Close()
	probe.file = nil
	return errors.Join(unlockErr, closeErr)
}

// Remove is retained for callers of the old probe API. Persistent marker
// files are not removed; releasing the shared observer lock is sufficient and
// avoids a pathname replacement race. Callers that need to retire a stale
// marker must do so while holding the runtime coordinator, but the next owner
// will safely overwrite its diagnostic contents.
func (probe *RuntimeLifecycleMarkerProbe) Remove() error {
	return probe.Close()
}

func runWithRuntimeLifecycleMarker(action func() error) error {
	cleanup, err := beginRuntimeLifecycleMarker()
	if err != nil {
		return err
	}
	defer cleanup()
	return action()
}

func beginRuntimeLifecycleMarker() (func(), error) {
	return beginRuntimeLifecycleMarkerAt(RuntimeLifecycleMarkerPath)
}

func beginRuntimeLifecycleMarkerAt(path string) (func(), error) {
	file, err := openRuntimeLockFile(path, true)
	if err != nil {
		return nil, fmt.Errorf("create Mihomo lifecycle marker: %w", err)
	}
	busy, err := tryLockRuntimeLockFile(file)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock Mihomo lifecycle marker: %w", err)
	}
	if busy {
		_ = file.Close()
		return nil, errors.New("Mihomo lifecycle marker is held by another owner")
	}
	if err := file.Truncate(0); err != nil {
		_ = unlockRuntimeLockFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("truncate Mihomo lifecycle marker: %w", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = unlockRuntimeLockFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("seek Mihomo lifecycle marker: %w", err)
	}
	if _, err := file.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		_ = unlockRuntimeLockFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("write Mihomo lifecycle marker: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = unlockRuntimeLockFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("sync Mihomo lifecycle marker: %w", err)
	}
	return func() {
		_ = unlockRuntimeLockFile(file)
		_ = file.Close()
	}, nil
}

// ClearRuntimeLifecycleMarker releases observation of an unlocked marker. The
// persistent lock file itself is intentionally retained.
func ClearRuntimeLifecycleMarker() error {
	probe, live, err := ProbeRuntimeLifecycleMarker()
	if err != nil {
		return err
	}
	if live {
		return errors.New("Mihomo lifecycle marker is held by a live owner")
	}
	if probe == nil {
		return nil
	}
	return probe.Close()
}
