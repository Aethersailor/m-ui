package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

func TestIndependentStoresCompleteAndRotateRace(t *testing.T) {
	t.Run("complete and rotate", func(t *testing.T) {
		databasePath := filepath.Join(t.TempDir(), "m-ui.db")
		now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
		seed := BootstrapSeed{
			TokenHash:       "old-token-hash",
			TokenCiphertext: "old-token-ciphertext",
			CreatedAt:       now,
		}
		prepareBootstrapDatabase(t, databasePath, seed, now)
		first := openStoreAt(t, databasePath)
		second := openStoreAt(t, databasePath)

		results := make(chan error, 2)
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			results <- first.CompleteBootstrap(
				context.Background(),
				seed.TokenHash,
				testBootstrapCompletion("complete-race", now),
				now,
			)
		}()
		go func() {
			defer group.Done()
			results <- second.RotateBootstrap(
				context.Background(),
				BootstrapSeed{
					TokenHash:       "rotated-token-hash",
					TokenCiphertext: "rotated-token-ciphertext",
					CreatedAt:       now,
				},
				now.Add(time.Second),
			)
		}()
		group.Wait()
		close(results)

		var successes int
		for err := range results {
			if err == nil {
				successes++
				continue
			}
			if !errors.Is(err, ErrBootstrapCompleted) &&
				!errors.Is(err, ErrInvalidBootstrapToken) {
				t.Fatalf("complete/rotate race error = %v", err)
			}
		}
		if successes != 1 {
			t.Fatalf("complete/rotate race successes = %d, want exactly one", successes)
		}

		finalStore := openStoreAt(t, databasePath)
		state, err := finalStore.BootstrapState(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if state.Required && state.TokenHash != "rotated-token-hash" {
			t.Fatalf("rotation winner state = %#v", state)
		}
		if !state.Required && state.ConsumedAt == nil {
			t.Fatalf("completion winner did not consume bootstrap state: %#v", state)
		}
		if err := finalStore.CompleteBootstrap(
			context.Background(),
			seed.TokenHash,
			testBootstrapCompletion("old-token-replay", now),
			now.Add(2*time.Second),
		); !errors.Is(err, ErrBootstrapCompleted) &&
			!errors.Is(err, ErrInvalidBootstrapToken) {
			t.Fatalf("old token replay error = %v", err)
		}
	})

	t.Run("rotate and rotate", func(t *testing.T) {
		databasePath := filepath.Join(t.TempDir(), "m-ui.db")
		now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
		prepareBootstrapDatabase(t, databasePath, BootstrapSeed{
			TokenHash:       "old-token-hash",
			TokenCiphertext: "old-token-ciphertext",
			CreatedAt:       now,
		}, now)
		first := openStoreAt(t, databasePath)
		second := openStoreAt(t, databasePath)
		seeds := []BootstrapSeed{
			{TokenHash: "rotated-token-hash-a", TokenCiphertext: "rotated-token-ciphertext-a"},
			{TokenHash: "rotated-token-hash-b", TokenCiphertext: "rotated-token-ciphertext-b"},
		}
		results := make(chan error, 2)
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			results <- first.RotateBootstrap(context.Background(), seeds[0], now.Add(time.Second))
		}()
		go func() {
			defer group.Done()
			results <- second.RotateBootstrap(context.Background(), seeds[1], now.Add(2*time.Second))
		}()
		group.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatalf("rotate/rotate race error = %v", err)
			}
		}

		finalStore := openStoreAt(t, databasePath)
		state, err := finalStore.BootstrapState(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !state.Required || state.ConsumedAt != nil {
			t.Fatalf("rotate/rotate race state = %#v", state)
		}
		if state.TokenHash != seeds[0].TokenHash && state.TokenHash != seeds[1].TokenHash {
			t.Fatalf("rotate/rotate winner hash = %q", state.TokenHash)
		}
		if err := finalStore.CompleteBootstrap(
			context.Background(),
			"old-token-hash",
			testBootstrapCompletion("old-token-after-rotate", now),
			now.Add(3*time.Second),
		); !errors.Is(err, ErrInvalidBootstrapToken) {
			t.Fatalf("old token after rotation error = %v, want ErrInvalidBootstrapToken", err)
		}
	})
}

func TestIndependentProcessesAllowExactlyOneBootstrapWinner(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "m-ui.db")
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	seed := BootstrapSeed{
		TokenHash:       "process-token-hash",
		TokenCiphertext: "process-token-ciphertext",
		CreatedAt:       now,
	}
	prepareBootstrapDatabase(t, databasePath, seed, now)

	fixtureDirectory := t.TempDir()
	releasePath := filepath.Join(fixtureDirectory, "release")
	commands := make([]*exec.Cmd, 0, 2)
	outputs := make([]*bytes.Buffer, 0, 2)
	readyPaths := make([]string, 0, 2)
	for index := 0; index < 2; index++ {
		readyPath := filepath.Join(fixtureDirectory, fmt.Sprintf("ready-%d", index))
		output := new(bytes.Buffer)
		command := exec.Command(os.Args[0], "-test.run=^TestBootstrapCompletionProcessHelper$")
		command.Env = append(os.Environ(),
			"M_UI_BOOTSTRAP_PROCESS_HELPER=1",
			"M_UI_BOOTSTRAP_DB="+databasePath,
			"M_UI_BOOTSTRAP_TOKEN_HASH="+seed.TokenHash,
			"M_UI_BOOTSTRAP_ID="+fmt.Sprintf("process-admin-%d", index),
			"M_UI_BOOTSTRAP_READY="+readyPath,
			"M_UI_BOOTSTRAP_RELEASE="+releasePath,
		)
		command.Stdout = output
		command.Stderr = output
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
		outputs = append(outputs, output)
		readyPaths = append(readyPaths, readyPath)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		ready := true
		for _, path := range readyPaths {
			if _, err := os.Stat(path); err != nil {
				ready = false
				break
			}
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bootstrap helper processes did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(releasePath, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	winners := 0
	losers := 0
	for index, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("bootstrap helper %d error = %v, output = %s", index, err, outputs[index].String())
		}
		result := strings.Fields(outputs[index].String())
		if len(result) == 0 {
			t.Fatalf("bootstrap helper %d output is empty", index)
		}
		switch result[0] {
		case "winner":
			winners++
		case "completed":
			losers++
		default:
			t.Fatalf("bootstrap helper %d output = %q", index, outputs[index].String())
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("process bootstrap results = winners:%d losers:%d", winners, losers)
	}

	finalStore := openStoreAt(t, databasePath)
	count, err := finalStore.AdminCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("process bootstrap administrator count = %d, want 1", count)
	}
}

func TestBootstrapCompletionProcessHelper(t *testing.T) {
	if os.Getenv("M_UI_BOOTSTRAP_PROCESS_HELPER") != "1" {
		return
	}
	databasePath := os.Getenv("M_UI_BOOTSTRAP_DB")
	readyPath := os.Getenv("M_UI_BOOTSTRAP_READY")
	releasePath := os.Getenv("M_UI_BOOTSTRAP_RELEASE")
	if databasePath == "" || readyPath == "" || releasePath == "" {
		t.Fatal("bootstrap process helper environment is incomplete")
	}
	database, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close bootstrap process helper database: %v", err)
		}
	}()
	if err := os.WriteFile(readyPath, []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(releasePath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("bootstrap process helper release timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	err = database.CompleteBootstrap(
		context.Background(),
		os.Getenv("M_UI_BOOTSTRAP_TOKEN_HASH"),
		testBootstrapCompletion(os.Getenv("M_UI_BOOTSTRAP_ID"), now),
		now,
	)
	switch {
	case err == nil:
		_, _ = os.Stdout.WriteString("winner\n")
	case errors.Is(err, ErrBootstrapCompleted):
		_, _ = os.Stdout.WriteString("completed\n")
	default:
		t.Fatal(err)
	}
}

func prepareBootstrapDatabase(t *testing.T, databasePath string, seed BootstrapSeed, now time.Time) {
	t.Helper()
	database, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureBootstrap(context.Background(), seed, now); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func openStoreAt(t *testing.T, databasePath string) *Store {
	t.Helper()
	database, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return database
}

func testBootstrapCompletion(id string, now time.Time) BootstrapCompletion {
	return BootstrapCompletion{
		Admin: Admin{
			ID:                id,
			Username:          "admin",
			PasswordHash:      "synthetic-password-hash",
			PasswordChangedAt: now,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		Session: Session{
			ID:               id + "-session",
			AdminUserID:      id,
			SessionTokenHash: id + "-session-hash",
			CSRFTokenHash:    id + "-csrf-hash",
			ExpiresAt:        now.Add(time.Hour),
			LastSeenAt:       now,
			CreatedAt:        now,
			UserAgent:        "synthetic",
		},
		Audit: AuditEntry{
			ID:              id + "-audit",
			ActorAdminID:    id,
			Action:          "auth.bootstrap_complete",
			ResourceType:    "administrator",
			ResourceID:      id,
			Result:          "success",
			SummaryRedacted: "Initial administrator was created through the local bootstrap flow.",
			CreatedAt:       now,
		},
	}
}
