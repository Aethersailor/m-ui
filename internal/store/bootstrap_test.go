package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBootstrapLifecycleIsAtomicAndOneTime(t *testing.T) {
	t.Parallel()

	database := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	seed := BootstrapSeed{
		TokenHash:       "token-hash",
		TokenCiphertext: "sealed-token",
		CreatedAt:       now,
	}
	if err := database.EnsureBootstrap(ctx, seed, now); err != nil {
		t.Fatal(err)
	}
	state, err := database.BootstrapState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Required || state.TokenHash != seed.TokenHash {
		t.Fatalf("unexpected initial bootstrap state: %#v", state)
	}

	completion := BootstrapCompletion{
		Admin: Admin{
			ID:                "admin-id",
			Username:          "admin",
			PasswordHash:      "synthetic-password-hash",
			PasswordChangedAt: now,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		Session: Session{
			ID:               "session-id",
			AdminUserID:      "admin-id",
			SessionTokenHash: "session-hash",
			CSRFTokenHash:    "csrf-hash",
			ExpiresAt:        now.Add(time.Hour),
			LastSeenAt:       now,
			CreatedAt:        now,
			UserAgent:        "synthetic",
		},
		Audit: AuditEntry{
			ID:              "audit-id",
			ActorAdminID:    "admin-id",
			Action:          "auth.bootstrap_complete",
			ResourceType:    "administrator",
			ResourceID:      "admin-id",
			Result:          "success",
			SummaryRedacted: "Initial administrator was created through the local bootstrap flow.",
			CreatedAt:       now,
		},
	}
	if err := database.CompleteBootstrap(ctx, seed.TokenHash, completion, now); err != nil {
		t.Fatal(err)
	}
	state, err = database.BootstrapState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Required || state.ConsumedAt == nil || state.TokenHash != "" || state.TokenCiphertext != "" {
		t.Fatalf("unexpected completed bootstrap state: %#v", state)
	}
	var adminCount, sessionCount, auditCount int
	for query, target := range map[string]*int{
		"SELECT COUNT(*) FROM admin_users": &adminCount,
		"SELECT COUNT(*) FROM sessions":    &sessionCount,
		"SELECT COUNT(*) FROM audit_logs":  &auditCount,
	} {
		if err := database.DB().QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if adminCount != 1 || sessionCount != 1 || auditCount != 1 {
		t.Fatalf("atomic bootstrap counts = admin:%d session:%d audit:%d", adminCount, sessionCount, auditCount)
	}
	if err := database.CompleteBootstrap(ctx, seed.TokenHash, completion, now); !errors.Is(err, ErrBootstrapCompleted) {
		t.Fatalf("second bootstrap error = %v, want ErrBootstrapCompleted", err)
	}
}

func TestEnsureBootstrapMarksExistingAdministratorComplete(t *testing.T) {
	t.Parallel()

	database := openTestStore(t)
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	insertTestAdmin(t, database, "admin-id", "admin", "synthetic", now)
	if err := database.EnsureBootstrap(
		context.Background(),
		BootstrapSeed{},
		now,
	); err != nil {
		t.Fatal(err)
	}
	state, err := database.BootstrapState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Required || state.ConsumedAt == nil {
		t.Fatalf("existing administrator left bootstrap required: %#v", state)
	}
}

func TestConsumedBootstrapWithoutAdministratorFailsClosed(t *testing.T) {
	t.Parallel()

	database := openTestStore(t)
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	if err := database.EnsureBootstrap(
		context.Background(),
		BootstrapSeed{
			TokenHash:       "synthetic-token-hash",
			TokenCiphertext: "synthetic-token-ciphertext",
			CreatedAt:       now,
		},
		now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB().Exec(
		"UPDATE bootstrap_state SET token_hash = '', token_ciphertext = '', consumed_at = ?",
		formatTime(now),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.BootstrapState(context.Background()); !errors.Is(
		err,
		ErrBootstrapUnavailable,
	) {
		t.Fatalf("consumed bootstrap state error = %v, want ErrBootstrapUnavailable", err)
	}
	if err := database.EnsureBootstrap(
		context.Background(),
		BootstrapSeed{
			TokenHash:       "synthetic-new-token-hash",
			TokenCiphertext: "synthetic-new-token-ciphertext",
			CreatedAt:       now,
		},
		now,
	); !errors.Is(err, ErrBootstrapUnavailable) {
		t.Fatalf("bootstrap re-open error = %v, want ErrBootstrapUnavailable", err)
	}
}
