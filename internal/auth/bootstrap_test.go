package auth

import (
	"bytes"
	"context"
	"errors"
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
