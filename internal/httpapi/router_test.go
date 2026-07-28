package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Aethersailor/m-ui/internal/version"
)

func TestHealth(t *testing.T) {
	t.Parallel()

	handler := New(Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Build: version.Info{
			Version: "test",
			Commit:  "test-commit",
			Date:    "2026-07-28T00:00:00Z",
		},
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var body healthResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "ok" || body.Build.Version != "test" {
		t.Fatalf("unexpected response: %#v", body)
	}
	if body.Time.IsZero() {
		t.Fatal("health time is zero")
	}
}

func TestUnknownFrontendRouteServesSPA(t *testing.T) {
	t.Parallel()

	handler := New(Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	request := httptest.NewRequest(http.MethodGet, "/listeners/example", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if body := response.Body.String(); !strings.Contains(body, "m-ui") {
		t.Fatalf("response does not contain application shell: %q", body)
	}
}

func TestUnknownAPIRouteReturnsJSON404(t *testing.T) {
	t.Parallel()

	handler := New(Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusNotFound; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if body := response.Body.String(); !strings.Contains(body, `"code":"NOT_FOUND"`) {
		t.Fatalf("unexpected response: %q", body)
	}
}
