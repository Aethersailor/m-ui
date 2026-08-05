package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSPAHandlerFallbackBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "client route",
			method:     http.MethodGet,
			path:       "/listeners/example",
			wantStatus: http.StatusOK,
			wantBody:   "m-ui",
		},
		{
			name:       "missing asset",
			method:     http.MethodGet,
			path:       "/assets/missing.js",
			wantStatus: http.StatusNotFound,
			wantBody:   "404 page not found",
		},
		{
			name:       "missing root static file",
			method:     http.MethodGet,
			path:       "/missing.css",
			wantStatus: http.StatusNotFound,
			wantBody:   "404 page not found",
		},
		{
			name:       "unknown mutation",
			method:     http.MethodPost,
			path:       "/listeners/example",
			wantStatus: http.StatusNotFound,
			wantBody:   "404 page not found",
		},
	}

	handler := SPAHandler()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequest(test.method, test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if got := response.Code; got != test.wantStatus {
				t.Fatalf("status = %d, want %d", got, test.wantStatus)
			}
			if body := response.Body.String(); !strings.Contains(body, test.wantBody) {
				t.Fatalf("body = %q, want it to contain %q", body, test.wantBody)
			}
		})
	}
}
