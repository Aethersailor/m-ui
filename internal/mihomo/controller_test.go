package mihomo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestControllerUsesAuthenticatedOfficialEndpoints(t *testing.T) {
	t.Parallel()
	const secret = "controller-test-secret"
	var reloadPath string
	var restartCalls int
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/version":
			_, _ = writer.Write([]byte(`{"meta":true,"version":"v1.19.29"}`))
		case "/traffic":
			_, _ = writer.Write([]byte(`{"up":1,"down":2,"upTotal":3,"downTotal":4}`))
		case "/memory":
			_, _ = writer.Write([]byte(`{"inuse":5,"oslimit":6}`))
		case "/connections":
			_, _ = writer.Write([]byte(
				`{"downloadTotal":7,"uploadTotal":8,"connections":[{"id":"one","upload":9,"download":10}]}`,
			))
		case "/configs":
			if request.Method != http.MethodPut ||
				request.URL.Query().Get("force") != "true" {
				http.Error(writer, "bad reload request", http.StatusBadRequest)
				return
			}
			var body struct {
				Path string `json:"path"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(writer, "bad body", http.StatusBadRequest)
				return
			}
			reloadPath = body.Path
			writer.WriteHeader(http.StatusNoContent)
		case "/restart":
			if request.Method != http.MethodPost {
				http.Error(writer, "bad restart request", http.StatusBadRequest)
				return
			}
			restartCalls++
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	controller, err := NewController(strings.TrimPrefix(server.URL, "http://"), secret)
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	ctx := context.Background()
	version, err := controller.Version(ctx)
	if err != nil || version.Version != "v1.19.29" || !version.Meta {
		t.Fatalf("Version() = %#v, %v", version, err)
	}
	traffic, err := controller.Traffic(ctx)
	if err != nil || traffic.UpTotal != 3 || traffic.DownTotal != 4 {
		t.Fatalf("Traffic() = %#v, %v", traffic, err)
	}
	memory, err := controller.Memory(ctx)
	if err != nil || memory.InUse != 5 || memory.OSLimit != 6 {
		t.Fatalf("Memory() = %#v, %v", memory, err)
	}
	connections, err := controller.Connections(ctx)
	if err != nil || len(connections.Connections) != 1 ||
		connections.Connections[0].ID != "one" {
		t.Fatalf("Connections() = %#v, %v", connections, err)
	}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := controller.Reload(ctx, configPath); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if reloadPath != configPath {
		t.Fatalf("reload path = %q, want %q", reloadPath, configPath)
	}
	if err := controller.Restart(ctx, configPath); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	if restartCalls != 1 {
		t.Fatalf("restart calls = %d", restartCalls)
	}
}

func TestControllerRejectsNonLoopbackAndRelativeConfigPath(t *testing.T) {
	t.Parallel()
	for _, address := range []string{
		"example.com:9090",
		"127.0.0.1:not-a-port",
		"127.0.0.1:70000",
	} {
		if _, err := NewController(address, "secret"); err == nil {
			t.Errorf("NewController(%q) error = nil", address)
		}
	}
	controller, err := NewController("127.0.0.1:9090", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Reload(context.Background(), "relative.yaml"); err == nil {
		t.Fatal("Reload() error = nil for relative path")
	}
}

func TestControllerErrorsDoNotExposeSecretOrResponseBody(t *testing.T) {
	t.Parallel()
	const secret = "secret-that-must-not-leak"
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		http.Error(writer, "body "+secret, http.StatusInternalServerError)
	}))
	defer server.Close()
	controller, err := NewController(strings.TrimPrefix(server.URL, "http://"), secret)
	if err != nil {
		t.Fatal(err)
	}
	_, err = controller.Version(context.Background())
	if err == nil {
		t.Fatal("Version() error = nil")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "body") {
		t.Fatalf("Version() error leaks sensitive response: %v", err)
	}
}
