package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aethersailor/m-ui/internal/auth"
	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/store"
)

type authTestEnvironment struct {
	handler http.Handler
	store   *store.Store
}

func newAuthTestEnvironment(t *testing.T) authTestEnvironment {
	t.Helper()

	database, err := store.Open(
		context.Background(),
		filepath.Join(t.TempDir(), "m-ui.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
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
	seedAdministrator(t, database, service)
	return authTestEnvironment{
		handler: New(Options{
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Auth:   service,
		}),
		store: database,
	}
}

func seedAdministrator(t *testing.T, database *store.Store, service *auth.Service) {
	t.Helper()
	var key muicrypto.MasterKey
	for index := range key {
		key[index] = 0x42
	}
	sealer, err := muicrypto.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.EnsureBootstrap(
		context.Background(),
		database,
		sealer,
		bytes.NewReader(bytes.Repeat([]byte{0x19}, 64)),
		time.Now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteSetup(
		context.Background(),
		"admin",
		"initial-test-password",
		"127.0.0.1",
		"test",
	); err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticationAndCSRFFlow(t *testing.T) {
	t.Parallel()

	environment := newAuthTestEnvironment(t)
	login := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]string{
			"username": "admin",
			"password": "initial-test-password",
		},
		nil,
		"",
	)
	if got, want := login.Code, http.StatusOK; got != want {
		t.Fatalf("login status = %d, want %d; body=%s", got, want, login.Body)
	}
	if got := login.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("login Cache-Control = %q, want no-store", got)
	}

	var loginBody loginResponse
	if err := json.NewDecoder(login.Body).Decode(&loginBody); err != nil {
		t.Fatal(err)
	}
	if loginBody.Admin.Username != "admin" || loginBody.CSRFToken == "" {
		t.Fatalf("unexpected login response: %#v", loginBody)
	}
	sessionCookie := responseCookie(t, login.Result(), sessionCookieName)
	if !sessionCookie.HttpOnly ||
		sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe session cookie: %#v", sessionCookie)
	}
	var storedSessionHash, storedCSRFHash string
	if err := environment.store.DB().QueryRow(
		"SELECT session_token_hash, csrf_token_hash FROM sessions LIMIT 1",
	).Scan(&storedSessionHash, &storedCSRFHash); err != nil {
		t.Fatal(err)
	}
	if storedSessionHash == sessionCookie.Value ||
		storedCSRFHash == loginBody.CSRFToken {
		t.Fatal("database contains a raw session or CSRF token")
	}
	var auditText string
	if err := environment.store.DB().QueryRow(
		`SELECT GROUP_CONCAT(
			COALESCE(summary_redacted, '') || COALESCE(resource_id, ''),
			' '
		) FROM audit_logs`,
	).Scan(&auditText); err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{
		"initial-test-password",
		sessionCookie.Value,
		loginBody.CSRFToken,
	} {
		if strings.Contains(auditText, sensitive) {
			t.Fatalf("audit data contains sensitive value %q", sensitive)
		}
	}

	me := performJSONRequest(
		t,
		environment.handler,
		http.MethodGet,
		"/api/v1/auth/me",
		nil,
		sessionCookie,
		"",
	)
	if got, want := me.Code, http.StatusOK; got != want {
		t.Fatalf("me status = %d, want %d; body=%s", got, want, me.Body)
	}

	blockedLogout := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/auth/logout",
		nil,
		sessionCookie,
		"",
	)
	if got, want := blockedLogout.Code, http.StatusForbidden; got != want {
		t.Fatalf("logout without CSRF = %d, want %d", got, want)
	}

	logout := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/auth/logout",
		nil,
		sessionCookie,
		loginBody.CSRFToken,
	)
	if got, want := logout.Code, http.StatusNoContent; got != want {
		t.Fatalf("logout status = %d, want %d; body=%s", got, want, logout.Body)
	}

	expiredMe := performJSONRequest(
		t,
		environment.handler,
		http.MethodGet,
		"/api/v1/auth/me",
		nil,
		sessionCookie,
		"",
	)
	if got, want := expiredMe.Code, http.StatusUnauthorized; got != want {
		t.Fatalf("me after logout = %d, want %d", got, want)
	}
}

func TestLoginFailureIsRateLimited(t *testing.T) {
	t.Parallel()

	environment := newAuthTestEnvironment(t)
	first := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]string{
			"username": "missing",
			"password": "incorrect-password",
		},
		nil,
		"",
	)
	if got, want := first.Code, http.StatusUnauthorized; got != want {
		t.Fatalf("first login status = %d, want %d", got, want)
	}
	second := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]string{
			"username": "missing",
			"password": "incorrect-password",
		},
		nil,
		"",
	)
	if got, want := second.Code, http.StatusTooManyRequests; got != want {
		t.Fatalf("second login status = %d, want %d", got, want)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("rate-limited response has no Retry-After header")
	}
}

func TestSetupTransportRequiresOneStructuralSameOrigin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		host       string
		remote     string
		origin     []string
		forwarded  string
		wantAccept bool
	}{
		{
			name:       "remote ipv4",
			host:       "192.0.2.20:2095",
			remote:     "192.0.2.10:40000",
			origin:     []string{"http://192.0.2.20:2095"},
			wantAccept: true,
		},
		{
			name:       "localhost default port",
			host:       "localhost",
			remote:     "127.0.0.1:40000",
			origin:     []string{"http://localhost"},
			wantAccept: true,
		},
		{
			name:       "remote ipv6",
			host:       "[2001:db8::20]:2095",
			remote:     "[2001:db8::10]:40000",
			origin:     []string{"http://[2001:db8::20]:2095"},
			wantAccept: true,
		},
		{
			name:       "https reverse proxy",
			host:       "panel.example.com",
			remote:     "127.0.0.1:40000",
			origin:     []string{"https://panel.example.com"},
			forwarded:  "for=192.0.2.10;proto=https;host=panel.example.com",
			wantAccept: true,
		},
		{
			name:   "origin path",
			host:   "127.0.0.1:2095",
			remote: "127.0.0.1:40000",
			origin: []string{"http://127.0.0.1:2095/setup"},
		},
		{
			name:   "origin userinfo",
			host:   "127.0.0.1:2095",
			remote: "127.0.0.1:40000",
			origin: []string{"http://user@127.0.0.1:2095"},
		},
		{
			name:   "origin port mismatch",
			host:   "127.0.0.1:2095",
			remote: "127.0.0.1:40000",
			origin: []string{"http://127.0.0.1:80"},
		},
		{
			name:   "duplicate origin",
			host:   "127.0.0.1:2095",
			remote: "127.0.0.1:40000",
			origin: []string{"http://127.0.0.1:2095", "http://evil.test"},
		},
		{
			name:   "cross origin",
			host:   "192.0.2.20:2095",
			remote: "192.0.2.10:40000",
			origin: []string{"http://evil.test:2095"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://"+test.host+"/", nil)
			request.RemoteAddr = test.remote
			request.Host = test.host
			for _, origin := range test.origin {
				request.Header.Add("Origin", origin)
			}
			if test.forwarded != "" {
				request.Header.Set("Forwarded", test.forwarded)
			}
			if got := setupTransportAllowed(request); got != test.wantAccept {
				t.Fatalf("setup transport allowed = %v, want %v", got, test.wantAccept)
			}
		})
	}
}

func TestChangePasswordRevokesCurrentSession(t *testing.T) {
	t.Parallel()

	environment := newAuthTestEnvironment(t)
	login := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]string{
			"username": "admin",
			"password": "initial-test-password",
		},
		nil,
		"",
	)
	var loginBody loginResponse
	if err := json.NewDecoder(login.Body).Decode(&loginBody); err != nil {
		t.Fatal(err)
	}
	sessionCookie := responseCookie(t, login.Result(), sessionCookieName)

	change := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/auth/change-password",
		map[string]string{
			"current_password": "initial-test-password",
			"new_password":     "replacement-test-password",
		},
		sessionCookie,
		loginBody.CSRFToken,
	)
	if got, want := change.Code, http.StatusNoContent; got != want {
		t.Fatalf("change password status = %d, want %d; body=%s", got, want, change.Body)
	}

	me := performJSONRequest(
		t,
		environment.handler,
		http.MethodGet,
		"/api/v1/auth/me",
		nil,
		sessionCookie,
		"",
	)
	if got, want := me.Code, http.StatusUnauthorized; got != want {
		t.Fatalf("old session status = %d, want %d", got, want)
	}
}

func performJSONRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body any,
	cookie *http.Cookie,
	csrfToken string,
) *httptest.ResponseRecorder {
	t.Helper()

	var content io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		content = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, content)
	request.RemoteAddr = "192.0.2.10:40000"
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if csrfToken != "" {
		request.Header.Set(csrfHeaderName, csrfToken)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func responseCookie(
	t *testing.T,
	response *http.Response,
	name string,
) *http.Cookie {
	t.Helper()

	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response cookie %q not found", name)
	return nil
}
