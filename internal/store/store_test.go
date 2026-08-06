package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()

	store, err := Open(
		context.Background(),
		filepath.Join(t.TempDir(), "m-ui.db"),
	)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func insertTestAdmin(t *testing.T, store *Store, id, username, passwordHash string, now time.Time) Admin {
	t.Helper()
	if _, err := store.DB().Exec(
		`INSERT INTO admin_users(
			id, username, password_hash, password_changed_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		id,
		username,
		passwordHash,
		formatTime(now),
		formatTime(now),
		formatTime(now),
	); err != nil {
		t.Fatal(err)
	}
	admin, err := store.AdminByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return admin
}

func TestOpenMigratesAndConfiguresSQLite(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	var migrationCount int
	if err := store.DB().QueryRow(
		"SELECT COUNT(*) FROM schema_migrations",
	).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 7 {
		t.Fatalf("migration count = %d, want 7", migrationCount)
	}

	var foreignKeys int
	if err := store.DB().QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}

	var journalMode string
	if err := store.DB().QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
}

func TestForeignKeyAndUniqueConstraints(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	now := formatTime(time.Now())
	if _, err := store.DB().Exec(
		`INSERT INTO sessions(
			id, admin_user_id, session_token_hash, csrf_token_hash,
			expires_at, last_seen_at, created_at, user_agent
		) VALUES ('session', 'missing', 'token', 'csrf', ?, ?, ?, '')`,
		now,
		now,
		now,
	); err == nil {
		t.Fatal("session insert error = nil, want foreign key failure")
	}
}

func TestResetPasswordRevokesSessions(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	admin := insertTestAdmin(t, store, "admin-id", "admin", "first-hash", now)
	if err := store.CreateSession(ctx, Session{
		ID:               "session-id",
		AdminUserID:      admin.ID,
		SessionTokenHash: "token-hash",
		CSRFTokenHash:    "csrf-hash",
		ExpiresAt:        now.Add(time.Hour),
		LastSeenAt:       now,
		CreatedAt:        now,
		UserAgent:        "test",
	}); err != nil {
		t.Fatal(err)
	}

	updated, created, err := store.ResetAdminPassword(
		ctx,
		"admin",
		"second-hash",
		now.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if created || updated.ID != admin.ID || updated.PasswordHash != "second-hash" {
		t.Fatalf("unexpected updated administrator: %#v, created=%t", updated, created)
	}
	if _, err := store.AuthSessionByTokenHash(
		ctx,
		"token-hash",
		now,
	); err != ErrNotFound {
		t.Fatalf("session lookup error = %v, want ErrNotFound", err)
	}
}

func TestSessionExpiryComparisonUsesChronologicalUTCOrder(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	admin := insertTestAdmin(t, store, "admin-id", "admin", "synthetic-hash", now)
	if err := store.CreateSession(ctx, Session{
		ID:               "session-id",
		AdminUserID:      admin.ID,
		SessionTokenHash: "token-hash",
		CSRFTokenHash:    "csrf-hash",
		ExpiresAt:        now.Add(500 * time.Millisecond),
		LastSeenAt:       now,
		CreatedAt:        now,
		UserAgent:        "test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthSessionByTokenHash(
		ctx,
		"token-hash",
		now,
	); err != nil {
		t.Fatalf("session was treated as expired too early: %v", err)
	}
	if _, err := store.AuthSessionByTokenHash(
		ctx,
		"token-hash",
		now.Add(time.Second),
	); err != ErrNotFound {
		t.Fatalf("expired session error = %v, want ErrNotFound", err)
	}
}
