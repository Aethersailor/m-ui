package auth

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/store"
)

func newBootstrapTestService(t *testing.T) (*store.Store, *Service, string) {
	t.Helper()
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	var key muicrypto.MasterKey
	for index := range key {
		key[index] = 0x37
	}
	sealer, err := muicrypto.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureBootstrap(
		context.Background(),
		database,
		sealer,
		bytes.NewReader(bytes.Repeat([]byte{0x21}, 64)),
		time.Now,
	); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(database, Options{
		SessionTTL: 12 * time.Hour,
		PasswordParams: PasswordParams{
			Memory:      8 * 1024,
			Iterations:  1,
			Parallelism: 1,
			SaltLength:  16,
			KeyLength:   32,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := database.BootstrapState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	token, err := ReadBootstrapToken(state, sealer)
	if err != nil {
		t.Fatal(err)
	}
	return database, service, token
}

func TestCompleteSetupCreatesSessionAndDisablesBootstrap(t *testing.T) {
	database, service, token := newBootstrapTestService(t)
	credentials, err := service.CompleteSetup(
		context.Background(),
		token,
		"admin",
		"synthetic-bootstrap-password",
		"127.0.0.1",
		"synthetic-agent",
	)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.Admin.Username != "admin" || credentials.SessionToken == "" || credentials.CSRFToken == "" {
		t.Fatalf("unexpected setup credentials: %#v", credentials)
	}
	status, err := service.SetupStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Required {
		t.Fatal("bootstrap remains required after completion")
	}
	if _, err := service.CompleteSetup(
		context.Background(),
		token,
		"other-admin",
		"another-synthetic-password",
		"127.0.0.1",
		"synthetic-agent",
	); !errors.Is(err, ErrBootstrapCompleted) {
		t.Fatalf("replayed setup error = %v, want ErrBootstrapCompleted", err)
	}
	if _, err := service.Login(
		context.Background(),
		"admin",
		"synthetic-bootstrap-password",
		"127.0.0.1",
		"synthetic-agent",
	); err != nil {
		t.Fatalf("setup password cannot login: %v", err)
	}
	var auditAction string
	if err := database.DB().QueryRow(
		"SELECT action FROM audit_logs WHERE action = 'auth.bootstrap_complete'",
	).Scan(&auditAction); err != nil {
		t.Fatal(err)
	}
	if auditAction != "auth.bootstrap_complete" {
		t.Fatalf("audit action = %q", auditAction)
	}
}

func TestResetPasswordDoesNotCreateFirstAdministrator(t *testing.T) {
	_, service, _ := newBootstrapTestService(t)
	if _, _, err := service.ResetPassword(
		context.Background(),
		"admin",
		"synthetic-recovery-password",
	); !errors.Is(err, ErrNoAdministrator) {
		t.Fatalf("reset without administrator error = %v, want ErrNoAdministrator", err)
	}
}

func TestConcurrentSetupAllowsExactlyOneWinner(t *testing.T) {
	database, service, token := newBootstrapTestService(t)
	type result struct {
		credentials Credentials
		err         error
	}
	results := make(chan result, 2)
	for index := 0; index < 2; index++ {
		go func(index int) {
			sourceIP := "127.0.0.1"
			if index == 1 {
				sourceIP = "127.0.0.2"
			}
			credentials, err := service.CompleteSetup(
				context.Background(),
				token,
				"admin",
				"synthetic-concurrent-password",
				sourceIP,
				"synthetic-agent",
			)
			results <- result{credentials: credentials, err: err}
		}(index)
	}
	winners := 0
	completed := 0
	for index := 0; index < 2; index++ {
		outcome := <-results
		if outcome.err == nil {
			winners++
			continue
		}
		if errors.Is(outcome.err, ErrBootstrapCompleted) {
			completed++
			continue
		}
		t.Fatalf("concurrent setup error = %v", outcome.err)
	}
	if winners != 1 || completed != 1 {
		t.Fatalf("concurrent setup results = winners:%d completed:%d", winners, completed)
	}
	count, err := database.AdminCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("administrator count = %d, want 1", count)
	}
}

func TestResetPasswordRollsBackWhenAuditInsertionFails(t *testing.T) {
	database, service, token := newBootstrapTestService(t)
	if _, err := service.CompleteSetup(
		context.Background(),
		token,
		"admin",
		"synthetic-bootstrap-password",
		"127.0.0.1",
		"synthetic-agent",
	); err != nil {
		t.Fatal(err)
	}
	before, err := database.AdminByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB().Exec(`
		CREATE TRIGGER reset_audit_failure
		BEFORE INSERT ON audit_logs
		WHEN NEW.action = 'auth.password_reset'
		BEGIN SELECT RAISE(ABORT, 'synthetic audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ResetPassword(
		context.Background(),
		"admin",
		"synthetic-recovery-password",
	); err == nil {
		t.Fatal("password reset unexpectedly succeeded")
	}
	after, err := database.AdminByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if after.PasswordHash != before.PasswordHash {
		t.Fatal("password hash changed after audit failure")
	}
	var sessions int
	if err := database.DB().QueryRow(
		"SELECT COUNT(*) FROM sessions WHERE admin_user_id = ?",
		before.ID,
	).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 {
		t.Fatalf("session count after audit failure = %d, want 1", sessions)
	}
}

func TestIndependentStoresAllowExactlyOneBootstrapWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m-ui.db")
	first, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	now := time.Now().UTC()
	seed := store.BootstrapSeed{
		TokenHash:       HashToken("synthetic-independent-token"),
		TokenCiphertext: "synthetic-sealed-token",
		CreatedAt:       now,
	}
	if err := first.EnsureBootstrap(context.Background(), seed, now); err != nil {
		t.Fatal(err)
	}
	completion := func(id string) store.BootstrapCompletion {
		return store.BootstrapCompletion{
			Admin: store.Admin{
				ID: id, Username: "admin", PasswordHash: "synthetic-hash",
				PasswordChangedAt: now, CreatedAt: now, UpdatedAt: now,
			},
			Session: store.Session{
				ID: id + "-session", AdminUserID: id,
				SessionTokenHash: id + "-session-hash", CSRFTokenHash: id + "-csrf-hash",
				ExpiresAt: now.Add(time.Hour), LastSeenAt: now, CreatedAt: now,
			},
			Audit: store.AuditEntry{
				ID: id + "-audit", ActorAdminID: id,
				Action: "auth.bootstrap_complete", ResourceType: "administrator",
				ResourceID: id, Result: "success",
				SummaryRedacted: "synthetic bootstrap", CreatedAt: now,
			},
		}
	}
	results := make(chan error, 2)
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		results <- first.CompleteBootstrap(
			context.Background(), seed.TokenHash, completion("first"), now,
		)
	}()
	go func() {
		defer group.Done()
		results <- second.CompleteBootstrap(
			context.Background(), seed.TokenHash, completion("second"), now,
		)
	}()
	group.Wait()
	close(results)
	winners := 0
	for result := range results {
		if result == nil {
			winners++
			continue
		}
		if !errors.Is(result, store.ErrBootstrapCompleted) {
			t.Fatalf("independent store completion error = %v", result)
		}
	}
	if winners != 1 {
		t.Fatalf("independent store winners = %d, want 1", winners)
	}
	count, err := first.AdminCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("administrator count = %d, want 1", count)
	}
}
