package app

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Aethersailor/m-ui/internal/config"
	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/operation"
	"github.com/Aethersailor/m-ui/internal/store"
)

func TestPanelHealthEndpointNormalizesWildcardBinds(t *testing.T) {
	tests := []struct {
		name string
		bind string
		want string
	}{
		{name: "IPv4 wildcard", bind: "0.0.0.0", want: "127.0.0.1"},
		{name: "IPv6 wildcard", bind: "::", want: "::1"},
		{name: "IPv4 loopback", bind: "127.0.0.1", want: "127.0.0.1"},
		{name: "IPv6 loopback", bind: "::1", want: "::1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			endpoint, err := panelHealthEndpoint(store.RuntimeSettings{
				InitialSettings: store.InitialSettings{
					PanelListenAddress: test.bind,
					PanelListenPort:    3095,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if endpoint != (domain.Endpoint{Host: test.want, Port: 3095}) {
				t.Fatalf("endpoint = %#v, want host %q and port 3095", endpoint, test.want)
			}
			if _, _, err := net.SplitHostPort(endpoint.Address()); err != nil {
				t.Fatalf("endpoint address %q is not a socket address: %v", endpoint.Address(), err)
			}
		})
	}
}

func TestPanelHealthEndpointRejectsNonIPBind(t *testing.T) {
	_, err := panelHealthEndpoint(store.RuntimeSettings{
		InitialSettings: store.InitialSettings{
			PanelListenAddress: "panel.example.test",
			PanelListenPort:    2095,
		},
	})
	if err == nil {
		t.Fatal("panel health endpoint accepted a non-IP bind")
	}
}

func TestPanelHealthUsesActiveCustomPort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/api/v1/health" {
			http.NotFound(writer, request)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	masterKeyPath := filepath.Join(root, "master.key")
	if err := os.WriteFile(masterKeyPath, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Storage.DatabasePath = "file:panel-health-test?mode=memory&cache=shared"
	cfg.Storage.MasterKeyPath = masterKeyPath
	cfg.Server.ListenAddress = host
	cfg.Server.Port = uint16(port)
	if err := PanelHealth(context.Background(), cfg); err != nil {
		t.Fatalf("PanelHealth() error = %v", err)
	}
}

func TestPreflightNativeFinalizerReleasesStaleMarkerProbeAndKeepsCoordinatorLease(t *testing.T) {
	markerPath := t.TempDir() + "/mihomo-lifecycle.marker"
	lockPath := t.TempDir() + "/runtime-operation.lock"
	if err := os.WriteFile(markerPath, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	handled, coordinator, release, err := preflightNativeFinalizer(markerPath, lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if handled || coordinator == nil || release == nil {
		t.Fatalf("preflight result = (%v, %#v, %v), want stale recovery lease", handled, coordinator, release != nil)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("persistent marker stat error = %v", err)
	}
	release()

	reacquire, err := coordinator.TryAcquire()
	if err != nil {
		t.Fatalf("coordinator was not released: %v", err)
	}
	reacquire()
}

func TestPreflightNativeFinalizerWithoutMarkerTakesLeaseBeforeDatabase(t *testing.T) {
	markerPath := t.TempDir() + "/mihomo-lifecycle.marker"
	lockPath := t.TempDir() + "/runtime-operation.lock"

	handled, coordinator, release, err := preflightNativeFinalizer(markerPath, lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if handled || coordinator == nil || release == nil {
		t.Fatalf("preflight result = (%v, %#v, %v), want coordinator lease", handled, coordinator, release != nil)
	}
	release()

	reacquire, err := coordinator.TryAcquire()
	if err != nil {
		t.Fatalf("coordinator was not released: %v", err)
	}
	reacquire()
}

func TestNativeFinalizerWaitsForMUIReadinessBeforeCoordinator(t *testing.T) {
	root := t.TempDir()
	markerPath := filepath.Join(root, "mihomo-lifecycle.marker")
	lockPath := filepath.Join(root, "runtime-operation.lock")
	leasePath := filepath.Join(root, "startup.lease")
	readyPath := filepath.Join(root, "ready")
	startup, err := mihomo.BeginRuntimeStartupAt(leasePath, readyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = startup.Close() }()

	owner, err := operation.NewFileCoordinator(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	releaseOwner, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	resultCh := make(chan error, 1)
	go func() {
		_, coordinator, release, err := preflightNativeFinalizerWithReadinessAt(
			context.Background(),
			markerPath,
			lockPath,
			leasePath,
			readyPath,
		)
		if release != nil {
			release()
		}
		if err == nil && coordinator == nil {
			err = os.ErrInvalid
		}
		resultCh <- err
	}()

	select {
	case err := <-resultCh:
		releaseOwner()
		t.Fatalf("native finalizer returned before readiness, error = %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := startup.PublishReady(); err != nil {
		releaseOwner()
		t.Fatal(err)
	}
	releaseOwner()
	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("native finalizer after readiness error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native finalizer did not acquire the coordinator after readiness")
	}
}
