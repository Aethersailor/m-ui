//go:build !windows

package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/Aethersailor/m-ui/internal/config"
	"github.com/Aethersailor/m-ui/internal/operation"
	"github.com/Aethersailor/m-ui/internal/store"
	"golang.org/x/sys/unix"
)

func TestApplyMihomoRuntimeFinalizerReturnsBeforeSQLiteWhenInitiatorOwnsLifecycle(t *testing.T) {
	ctx := context.Background()
	databasePath := t.TempDir() + "/m-ui.db"
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	databaseTransaction, err := database.DB().BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := databaseTransaction.ExecContext(
		ctx,
		"CREATE TABLE IF NOT EXISTS synthetic_endpoint_write_lock (id INTEGER)",
	); err != nil {
		_ = databaseTransaction.Rollback()
		t.Fatal(err)
	}
	defer func() { _ = databaseTransaction.Rollback() }()

	markerPath := t.TempDir() + "/mihomo-lifecycle.marker"
	lockPath := t.TempDir() + "/runtime-operation.lock"
	marker, err := os.OpenFile(markerPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(marker.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = marker.Close()
		t.Fatal(err)
	}
	defer func() {
		_ = unix.Flock(int(marker.Fd()), unix.LOCK_UN)
		_ = marker.Close()
		_ = os.Remove(markerPath)
	}()

	owner, err := operation.NewFileCoordinator(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	releaseOwner, err := owner.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseOwner()

	err = applyMihomoRuntime(
		ctx,
		config.Config{
			Storage: config.Storage{
				DatabasePath:  databasePath,
				MasterKeyPath: t.TempDir() + "/missing-master-key",
			},
			Mihomo: config.Mihomo{ProcessMode: "native"},
		},
		"finalize",
		markerPath,
		lockPath,
	)
	if err != nil {
		t.Fatalf("applyMihomoRuntime(finalize) error = %v, want live-owner fast path", err)
	}
}

func TestPreflightNativeFinalizerLiveMarkerReturnsBeforeDatabase(t *testing.T) {
	markerPath := t.TempDir() + "/mihomo-lifecycle.marker"
	lockPath := t.TempDir() + "/runtime-operation.lock"
	marker, err := os.OpenFile(markerPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(marker.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = marker.Close()
		t.Fatal(err)
	}
	defer func() {
		_ = unix.Flock(int(marker.Fd()), unix.LOCK_UN)
		_ = marker.Close()
		_ = os.Remove(markerPath)
	}()

	owner, err := operation.NewFileCoordinator(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	releaseOwner, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseOwner()

	handled, coordinator, release, err := preflightNativeFinalizer(markerPath, lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || coordinator != nil || release != nil {
		t.Fatalf("preflight result = (%v, %#v, %v), want handled fast path", handled, coordinator, release != nil)
	}
}

func TestPreflightNativeFinalizerStaleMarkerDoesNotBypassBusyCoordinator(t *testing.T) {
	markerPath := t.TempDir() + "/mihomo-lifecycle.marker"
	lockPath := t.TempDir() + "/runtime-operation.lock"
	if err := os.WriteFile(markerPath, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	owner, err := operation.NewFileCoordinator(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	releaseOwner, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseOwner()

	handled, coordinator, release, err := preflightNativeFinalizer(markerPath, lockPath)
	if !errors.Is(err, operation.ErrBusy) {
		t.Fatalf("preflight error = %v, want runtime busy", err)
	}
	if handled || coordinator != nil || release != nil {
		t.Fatalf("preflight result = (%v, %#v, %v), want fail-closed result", handled, coordinator, release != nil)
	}

	// The stale observer must be closed before the coordinator attempt. An
	// exclusive marker lock is therefore available even though the coordinator
	// remains owned by the competing runtime operation.
	markerProbe, err := os.OpenFile(markerPath, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(markerProbe.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = markerProbe.Close()
		t.Fatalf("exclusive marker lock after busy preflight = %v", err)
	}
	_ = unix.Flock(int(markerProbe.Fd()), unix.LOCK_UN)
	_ = markerProbe.Close()
}
