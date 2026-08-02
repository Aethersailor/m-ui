package mihomo

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	// RuntimeStartupLeasePath is held while m-ui is still performing startup
	// recovery. Native Mihomo startup waits for the separate ready lock instead
	// of competing with that recovery for the runtime coordinator.
	RuntimeStartupLeasePath = "/run/m-ui/startup.lease"
	// RuntimeReadyPath is a persistent lock file held exclusively only after
	// m-ui has completed recovery and bound its panel HTTP listener.
	RuntimeReadyPath = "/run/m-ui/ready"

	runtimeReadinessPollInterval = 50 * time.Millisecond
	runtimeTokenSize             = 16
	runtimeTokenMaxBytes         = 128
	runtimeLockDataOffset        = 1
)

// RuntimeStartup owns the startup lease until m-ui has completed recovery. It
// then publishes a live ready lock that remains held for the server lifetime.
// The two persistent lock files use a shared token so an observer can reject
// a short-lived stale-file reset lock as readiness.
type RuntimeStartup struct {
	lease     *os.File
	ready     *os.File
	leasePath string
	readyPath string
	token     string
	published bool
}

// RuntimeReadyGuard holds a shared startup-lease lock while a caller uses the
// m-ui runtime. Holding this guard prevents a new m-ui process from resetting
// the readiness token and acquiring the coordinator after the caller has
// observed readiness. The guard must remain held until the caller has released
// its runtime coordinator.
type RuntimeReadyGuard struct {
	lease *os.File
}

// BeginRuntimeStartup starts the m-ui startup lease used by native service
// finalizers and installation helpers.
func BeginRuntimeStartup() (*RuntimeStartup, error) {
	return BeginRuntimeStartupAt(RuntimeStartupLeasePath, RuntimeReadyPath)
}

// BeginRuntimeStartupAt is the path-parameterized form used by tests.
func BeginRuntimeStartupAt(leasePath, readyPath string) (*RuntimeStartup, error) {
	lease, err := openRuntimeLockFile(leasePath, true)
	if err != nil {
		return nil, fmt.Errorf("create m-ui startup lease: %w", err)
	}
	busy, err := tryLockRuntimeLockFile(lease)
	if err != nil {
		_ = lease.Close()
		return nil, fmt.Errorf("lock m-ui startup lease: %w", err)
	}
	if busy {
		_ = lease.Close()
		return nil, errors.New("m-ui startup lease is held by another owner")
	}

	// A previous process may have terminated before publishing or after
	// publishing. Keep the lock file pathname stable, take its exclusive lock
	// while resetting the old token. The lease token is updated only after this
	// check, so a failed second startup cannot invalidate a currently published
	// ready owner.
	ready, err := openRuntimeLockFile(readyPath, true)
	if err != nil {
		_ = unlockRuntimeLockFile(lease)
		_ = lease.Close()
		return nil, fmt.Errorf("create m-ui readiness lock: %w", err)
	}
	readyBusy, err := tryLockRuntimeLockFile(ready)
	if err != nil {
		_ = ready.Close()
		_ = unlockRuntimeLockFile(lease)
		_ = lease.Close()
		return nil, fmt.Errorf("inspect m-ui readiness lock: %w", err)
	}
	if readyBusy {
		_ = ready.Close()
		_ = unlockRuntimeLockFile(lease)
		_ = lease.Close()
		return nil, errors.New("m-ui readiness is held by another owner")
	}
	token, err := newRuntimeToken()
	if err != nil {
		_ = unlockRuntimeLockFile(ready)
		_ = ready.Close()
		_ = unlockRuntimeLockFile(lease)
		_ = lease.Close()
		return nil, fmt.Errorf("generate m-ui startup token: %w", err)
	}
	if err := writeRuntimeLockToken(lease, token); err != nil {
		_ = unlockRuntimeLockFile(ready)
		_ = ready.Close()
		_ = unlockRuntimeLockFile(lease)
		_ = lease.Close()
		return nil, fmt.Errorf("write m-ui startup token: %w", err)
	}
	if err := writeRuntimeLockToken(ready, ""); err != nil {
		_ = unlockRuntimeLockFile(ready)
		_ = ready.Close()
		_ = unlockRuntimeLockFile(lease)
		_ = lease.Close()
		return nil, fmt.Errorf("reset m-ui readiness token: %w", err)
	}
	if err := unlockRuntimeLockFile(ready); err != nil {
		_ = ready.Close()
		_ = unlockRuntimeLockFile(lease)
		_ = lease.Close()
		return nil, fmt.Errorf("release m-ui readiness reset lock: %w", err)
	}
	if err := ready.Close(); err != nil {
		_ = unlockRuntimeLockFile(lease)
		_ = lease.Close()
		return nil, fmt.Errorf("close m-ui readiness reset lock: %w", err)
	}

	return &RuntimeStartup{
		lease:     lease,
		leasePath: leasePath,
		readyPath: readyPath,
		token:     token,
	}, nil
}

// PublishReady creates and holds the readiness lock. Call it only after
// startup reconciliation has completed and the panel listener is bound.
func (startup *RuntimeStartup) PublishReady() error {
	if startup == nil || startup.lease == nil {
		return errors.New("m-ui startup lease is not active")
	}
	if startup.published {
		return nil
	}
	ready, err := openRuntimeLockFile(startup.readyPath, true)
	if err != nil {
		return fmt.Errorf("open m-ui readiness lock: %w", err)
	}
	busy, err := tryLockRuntimeLockFile(ready)
	if err != nil {
		_ = ready.Close()
		return fmt.Errorf("lock m-ui readiness: %w", err)
	}
	if busy {
		_ = ready.Close()
		return errors.New("m-ui readiness is held by another owner")
	}
	if err := writeRuntimeLockToken(ready, startup.token); err != nil {
		_ = unlockRuntimeLockFile(ready)
		_ = ready.Close()
		return fmt.Errorf("write m-ui readiness token: %w", err)
	}
	startup.ready = ready
	startup.published = true
	if err := startup.releaseLease(); err != nil {
		_ = writeRuntimeLockToken(ready, "")
		_ = unlockRuntimeLockFile(ready)
		_ = ready.Close()
		startup.ready = nil
		startup.published = false
		return fmt.Errorf("release m-ui startup lease: %w", err)
	}
	return nil
}

// Close releases all startup/readiness locks. Lock file pathnames and their
// last tokens are intentionally retained; an unlocked persistent lock is not
// readiness, and retaining the inode prevents path replacement races.
func (startup *RuntimeStartup) Close() error {
	if startup == nil {
		return nil
	}
	var errs []error
	if startup.ready != nil {
		errs = append(errs, unlockRuntimeLockFile(startup.ready))
		errs = append(errs, startup.ready.Close())
		startup.ready = nil
	}
	if startup.lease != nil {
		errs = append(errs, startup.releaseLease())
	}
	return errors.Join(errs...)
}

func (startup *RuntimeStartup) releaseLease() error {
	if startup == nil || startup.lease == nil {
		return nil
	}
	var errs []error
	errs = append(errs, unlockRuntimeLockFile(startup.lease))
	errs = append(errs, startup.lease.Close())
	startup.lease = nil
	return errors.Join(errs...)
}

// AcquireRuntimeReadyGuard waits for a live m-ui readiness generation and
// keeps a shared lock on its startup lease until Close is called.
func AcquireRuntimeReadyGuard(ctx context.Context) (*RuntimeReadyGuard, error) {
	return AcquireRuntimeReadyGuardAt(ctx, RuntimeStartupLeasePath, RuntimeReadyPath)
}

// AcquireRuntimeReadyGuardAt is the path-parameterized form used by tests.
func AcquireRuntimeReadyGuardAt(
	ctx context.Context,
	leasePath string,
	readyPath string,
) (*RuntimeReadyGuard, error) {
	for {
		guard, live, err := tryAcquireRuntimeReadyGuardAt(leasePath, readyPath)
		if err != nil {
			return nil, fmt.Errorf("inspect m-ui readiness: %w", err)
		}
		if live {
			return guard, nil
		}
		timer := time.NewTimer(runtimeReadinessPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("wait for m-ui startup readiness: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

// Close releases the shared startup-lease lock. It is safe to call more than
// once so callers can defer it immediately after acquiring the guard.
func (guard *RuntimeReadyGuard) Close() error {
	if guard == nil || guard.lease == nil {
		return nil
	}
	unlockErr := unlockRuntimeLockFile(guard.lease)
	closeErr := guard.lease.Close()
	guard.lease = nil
	return errors.Join(unlockErr, closeErr)
}

// WaitForRuntimeReady waits for a live m-ui readiness lock. A stale or absent
// path is never accepted as readiness. The caller's context bounds the wait,
// allowing a service-manager hook to fail closed if m-ui cannot complete
// startup.
func WaitForRuntimeReady(ctx context.Context) error {
	return WaitForRuntimeReadyAt(ctx, RuntimeStartupLeasePath, RuntimeReadyPath)
}

// WaitForRuntimeReadyAt is the path-parameterized form used by tests.
func WaitForRuntimeReadyAt(
	ctx context.Context,
	leasePath string,
	readyPath string,
) error {
	guard, err := AcquireRuntimeReadyGuardAt(ctx, leasePath, readyPath)
	if err != nil {
		return err
	}
	return guard.Close()
}

func runtimeReadyLive(leasePath, readyPath string) (bool, error) {
	guard, live, err := tryAcquireRuntimeReadyGuardAt(leasePath, readyPath)
	if err != nil {
		return false, err
	}
	if !live {
		return false, nil
	}
	return true, guard.Close()
}

func tryAcquireRuntimeReadyGuardAt(
	leasePath string,
	readyPath string,
) (*RuntimeReadyGuard, bool, error) {
	lease, err := openRuntimeLockFile(leasePath, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	leaseBusy, err := trySharedRuntimeLockFile(lease)
	if err != nil {
		_ = lease.Close()
		return nil, false, err
	}
	if leaseBusy {
		// The m-ui startup owner still holds the lease. A ready lock from an
		// older generation, or the brief reset lock of this generation, is not
		// sufficient to let a native finalizer proceed.
		_ = lease.Close()
		return nil, false, nil
	}
	leaseToken, err := readRuntimeLockTokenFromFile(lease)
	if err != nil {
		_ = unlockRuntimeLockFile(lease)
		_ = lease.Close()
		return nil, false, err
	}
	if leaseToken == "" {
		_ = unlockRuntimeLockFile(lease)
		_ = lease.Close()
		return nil, false, nil
	}

	ready, err := openRuntimeLockFile(readyPath, false)
	if errors.Is(err, os.ErrNotExist) {
		_ = unlockRuntimeLockFile(lease)
		_ = lease.Close()
		return nil, false, nil
	}
	if err != nil {
		_ = unlockRuntimeLockFile(lease)
		_ = lease.Close()
		return nil, false, err
	}
	busy, err := trySharedRuntimeLockFile(ready)
	if err != nil {
		_ = ready.Close()
		_ = unlockRuntimeLockFile(lease)
		_ = lease.Close()
		return nil, false, err
	}
	readyToken, readErr := readRuntimeLockTokenFromFile(ready)
	if busy {
		_ = ready.Close()
		if readErr != nil {
			_ = unlockRuntimeLockFile(lease)
			_ = lease.Close()
			return nil, false, readErr
		}
		// An exclusive lock can be either the real published owner or the
		// short reset lock held by a new startup. Only the matching token
		// represents a published ready owner.
		if readyToken == "" || readyToken != leaseToken {
			_ = unlockRuntimeLockFile(lease)
			_ = lease.Close()
			return nil, false, nil
		}
		return &RuntimeReadyGuard{lease: lease}, true, nil
	}
	unlockErr := unlockRuntimeLockFile(ready)
	closeErr := ready.Close()
	if readErr != nil {
		_ = unlockRuntimeLockFile(lease)
		_ = lease.Close()
		return nil, false, readErr
	}
	if err := errors.Join(unlockErr, closeErr); err != nil {
		_ = unlockRuntimeLockFile(lease)
		_ = lease.Close()
		return nil, false, err
	}
	_ = unlockRuntimeLockFile(lease)
	_ = lease.Close()
	return nil, false, nil
}

func newRuntimeToken() (string, error) {
	buffer := make([]byte, runtimeTokenSize)
	if _, err := io.ReadFull(cryptorand.Reader, buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func writeRuntimeLockToken(file *os.File, token string) error {
	if err := file.Truncate(runtimeLockDataOffset); err != nil {
		return err
	}
	if _, err := file.Seek(runtimeLockDataOffset, 0); err != nil {
		return err
	}
	if token != "" {
		if _, err := file.WriteString(token + "\n"); err != nil {
			return err
		}
	}
	return file.Sync()
}

func readRuntimeLockToken(path string) (string, error) {
	file, err := openRuntimeLockFile(path, false)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return readRuntimeLockTokenFromFile(file)
}

func readRuntimeLockTokenFromFile(file *os.File) (string, error) {
	if _, err := file.Seek(runtimeLockDataOffset, 0); err != nil {
		return "", err
	}
	content, err := io.ReadAll(io.LimitReader(file, runtimeTokenMaxBytes))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(content)), nil
}
