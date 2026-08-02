package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Aethersailor/m-ui/internal/auth"
	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/store"
)

type setupTestEnvironment struct {
	handler http.Handler
	store   *store.Store
	token   string
}

func newSetupTestEnvironment(t *testing.T) setupTestEnvironment {
	t.Helper()
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	var key muicrypto.MasterKey
	for index := range key {
		key[index] = 0x51
	}
	sealer, err := muicrypto.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.EnsureBootstrap(
		context.Background(),
		database,
		sealer,
		bytes.NewReader(bytes.Repeat([]byte{0x18}, 64)),
		time.Now,
	); err != nil {
		t.Fatal(err)
	}
	service, err := auth.NewService(database, auth.Options{
		SessionTTL: 12 * time.Hour,
		PasswordParams: auth.PasswordParams{
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
	token, err := auth.ReadBootstrapToken(state, sealer)
	if err != nil {
		t.Fatal(err)
	}
	return setupTestEnvironment{
		handler: New(Options{
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Auth:   service,
		}),
		store: database,
		token: token,
	}
}

func TestSetupStatusDoesNotExposeCapability(t *testing.T) {
	environment := newSetupTestEnvironment(t)
	request := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1:2095/api/v1/setup/status",
		nil,
	)
	response := httptest.NewRecorder()
	environment.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status response = %d, body=%s", response.Code, response.Body)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	body := response.Body.String()
	if body == "" || !bytes.Contains([]byte(body), []byte(`"state":"required"`)) {
		t.Fatalf("unexpected setup status body: %s", body)
	}
	if bytes.Contains([]byte(body), []byte(environment.token)) || bytes.Contains([]byte(body), []byte("token_hash")) {
		t.Fatalf("setup status exposed capability: %s", body)
	}
}

func TestSetupRejectsRemoteEvenWithCapability(t *testing.T) {
	environment := newSetupTestEnvironment(t)
	response := performSetupRequest(
		t,
		environment.handler,
		"192.0.2.10:40000",
		environment.token,
		"http://127.0.0.1:2095",
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("remote setup status = %d, want 403; body=%s", response.Code, response.Body)
	}
}

func TestSetupCreatesSessionAndRequiresOrigin(t *testing.T) {
	environment := newSetupTestEnvironment(t)
	missingOrigin := performSetupRequest(
		t,
		environment.handler,
		"127.0.0.1:40000",
		environment.token,
		"",
	)
	if missingOrigin.Code != http.StatusForbidden {
		t.Fatalf("missing Origin status = %d, want 403", missingOrigin.Code)
	}

	response := performSetupRequest(
		t,
		environment.handler,
		"127.0.0.1:40000",
		environment.token,
		"http://127.0.0.1:2095",
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, body=%s", response.Code, response.Body)
	}
	var body loginResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Admin.Username != "admin" || body.CSRFToken == "" {
		t.Fatalf("unexpected setup response: %#v", body)
	}
	if len(response.Result().Cookies()) != 2 {
		t.Fatalf("setup cookie count = %d", len(response.Result().Cookies()))
	}
	status := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1:2095/api/v1/setup/status",
		nil,
	)
	environment.handler.ServeHTTP(status, request)
	if !bytes.Contains(status.Body.Bytes(), []byte(`"state":"complete"`)) {
		t.Fatalf("completed setup status body: %s", status.Body)
	}
}

func performSetupRequest(
	t *testing.T,
	handler http.Handler,
	remoteAddress string,
	token string,
	origin string,
) *httptest.ResponseRecorder {
	t.Helper()
	payload := bytes.NewBufferString(`{"username":"admin","password":"synthetic-setup-password"}`)
	request := httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1:2095/api/v1/setup/complete",
		payload,
	)
	request.RemoteAddr = remoteAddress
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(setupTokenHeaderName, token)
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
