package service

import (
	"context"
	"errors"
	"testing"
	"time"

	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/operation"
	"github.com/Aethersailor/m-ui/internal/publisher"
	"github.com/Aethersailor/m-ui/internal/store"
)

func TestRuntimeActionRejectsReloadWhileMihomoRestartIsPending(t *testing.T) {
	manager, managed, process, _ := newRuntimeEndpointManager(t, true)

	err := manager.RuntimeAction(context.Background(), "", "reload")
	if !errors.Is(err, publisher.ErrMihomoRestartRequired) {
		t.Fatalf("RuntimeAction(reload) error = %v, want restart-required", err)
	}
	if process.reloadCalls != 0 {
		t.Fatalf("reload calls = %d, want 0", process.reloadCalls)
	}
	state, err := managed.EndpointSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Pending == nil || !state.Pending.RequiresMihomoRestart {
		t.Fatalf("reload changed pending endpoint state = %#v", state.Pending)
	}
}

func TestRuntimeActionReloadRequiresHealthyController(t *testing.T) {
	manager, managed, process, controller := newRuntimeEndpointManager(t, false)
	controller.versionErr = errors.New("synthetic wrong controller secret")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := manager.RuntimeAction(ctx, "", "reload")
	if err == nil {
		t.Fatal("RuntimeAction(reload) error = nil, want health-check failure")
	}
	if process.reloadCalls != 1 {
		t.Fatalf("reload calls = %d, want 1", process.reloadCalls)
	}
	if controller.versionCalls == 0 {
		t.Fatal("reload did not health-check the Controller")
	}
	state, err := managed.EndpointSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Pending != nil {
		t.Fatalf("reload health failure changed endpoint state = %#v", state.Pending)
	}
}

func TestRuntimeActionStartAppliesPendingMihomoEndpointWithCAS(t *testing.T) {
	manager, managed, process, controller := newRuntimeEndpointManager(t, true)

	if err := manager.RuntimeAction(context.Background(), "", "start"); err != nil {
		t.Fatalf("RuntimeAction(start) error = %v", err)
	}
	if process.startCalls != 1 {
		t.Fatalf("start calls = %d, want 1", process.startCalls)
	}
	if controller.versionCalls == 0 {
		t.Fatal("start did not health-check the Controller")
	}
	state, err := managed.EndpointSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Pending != nil {
		t.Fatalf("successful start left pending endpoint state = %#v", state.Pending)
	}
	if !state.LastApplied.MihomoExternalControllerBind.Equal(
		state.Active.MihomoExternalControllerBind,
	) {
		t.Fatalf("last-applied Mihomo bind = %#v, active = %#v", state.LastApplied, state.Active)
	}
}

func TestRuntimeActionStartHealthChecksEvenWithoutPendingEndpoint(t *testing.T) {
	manager, managed, process, controller := newRuntimeEndpointManager(t, false)
	controller.versionErr = errors.New("synthetic controller unavailable")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := manager.RuntimeAction(ctx, "", "start")
	if err == nil {
		t.Fatal("RuntimeAction(start) error = nil, want health-check failure")
	}
	if process.startCalls != 1 {
		t.Fatalf("start calls = %d, want 1", process.startCalls)
	}
	if controller.versionCalls == 0 {
		t.Fatal("start without pending endpoint did not health-check the Controller")
	}
	state, err := managed.EndpointSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Pending != nil {
		t.Fatalf("health failure changed absent pending endpoint state = %#v", state.Pending)
	}
}

func TestRuntimeActionStartKeepsPendingAfterHealthFailure(t *testing.T) {
	manager, managed, process, controller := newRuntimeEndpointManager(t, true)
	controller.versionErr = errors.New("synthetic controller unavailable")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := manager.RuntimeAction(ctx, "", "start")
	if err == nil {
		t.Fatal("RuntimeAction(start) error = nil")
	}
	if process.startCalls != 1 {
		t.Fatalf("start calls = %d, want 1", process.startCalls)
	}
	state, stateErr := managed.EndpointSettings(context.Background())
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if state.Pending == nil || !state.Pending.RequiresMihomoRestart {
		t.Fatalf("health failure cleared pending endpoint state = %#v", state.Pending)
	}
}

func TestRuntimeActionStartRejectsAlreadyActivePendingProcess(t *testing.T) {
	manager, managed, process, _ := newRuntimeEndpointManager(t, true)
	process.active = true

	err := manager.RuntimeAction(context.Background(), "", "start")
	if !errors.Is(err, publisher.ErrMihomoRestartRequired) {
		t.Fatalf("RuntimeAction(start) error = %v, want restart-required", err)
	}
	if process.startCalls != 0 {
		t.Fatalf("start calls = %d, want 0", process.startCalls)
	}
	state, err := managed.EndpointSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Pending == nil || !state.Pending.RequiresMihomoRestart {
		t.Fatalf("active start changed pending endpoint state = %#v", state.Pending)
	}
}

func TestRuntimeActionRestartAppliesPendingMihomoEndpointWithCAS(t *testing.T) {
	manager, managed, process, controller := newRuntimeEndpointManager(t, true)

	if err := manager.RuntimeAction(context.Background(), "", "restart"); err != nil {
		t.Fatalf("RuntimeAction(restart) error = %v", err)
	}
	if process.restartCalls != 1 {
		t.Fatalf("restart calls = %d, want 1", process.restartCalls)
	}
	if controller.versionCalls == 0 {
		t.Fatal("restart did not health-check the Controller")
	}
	state, err := managed.EndpointSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Pending != nil {
		t.Fatalf("successful restart left pending endpoint state = %#v", state.Pending)
	}
}

func TestRuntimeActionRestartKeepsPendingAfterHealthFailure(t *testing.T) {
	manager, managed, process, controller := newRuntimeEndpointManager(t, true)
	controller.versionErr = errors.New("synthetic controller unavailable")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := manager.RuntimeAction(ctx, "", "restart")
	if err == nil {
		t.Fatal("RuntimeAction(restart) error = nil")
	}
	if process.restartCalls != 1 {
		t.Fatalf("restart calls = %d, want 1", process.restartCalls)
	}
	state, stateErr := managed.EndpointSettings(context.Background())
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if state.Pending == nil || !state.Pending.RequiresMihomoRestart {
		t.Fatalf("health failure cleared pending endpoint state = %#v", state.Pending)
	}
}

func TestRuntimeBoundaryFinalizeRequiresHealthyActiveProcess(t *testing.T) {
	manager, managed, process, controller := newRuntimeEndpointManager(t, true)
	boundary, err := NewRuntimeBoundary(RuntimeBoundaryOptions{
		Store:          managed,
		Controller:     controller,
		Process:        process,
		Coordinator:    manager.coordinator,
		HealthTimeout:  100 * time.Millisecond,
		HealthInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := boundary.Finalize(context.Background()); err == nil {
		t.Fatal("Finalize() error = nil for inactive process")
	}
	state, err := managed.EndpointSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Pending == nil || !state.Pending.RequiresMihomoRestart {
		t.Fatalf("inactive finalize cleared pending endpoint state = %#v", state.Pending)
	}

	process.active = true
	if err := boundary.Finalize(context.Background()); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if controller.versionCalls == 0 {
		t.Fatal("Finalize() did not health-check the Controller")
	}
	state, err = managed.EndpointSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Pending != nil {
		t.Fatalf("successful finalize left pending endpoint state = %#v", state.Pending)
	}
}

func TestRuntimeActionStopDoesNotClearPendingEndpoint(t *testing.T) {
	manager, managed, process, _ := newRuntimeEndpointManager(t, true)

	if err := manager.RuntimeAction(context.Background(), "", "stop"); err != nil {
		t.Fatalf("RuntimeAction(stop) error = %v", err)
	}
	if process.stopCalls != 1 {
		t.Fatalf("stop calls = %d, want 1", process.stopCalls)
	}
	state, err := managed.EndpointSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Pending == nil || !state.Pending.RequiresMihomoRestart {
		t.Fatalf("stop changed pending endpoint state = %#v", state.Pending)
	}
}

func newRuntimeEndpointManager(
	t *testing.T,
	pending bool,
) (*Manager, *store.ManagedStore, *runtimeEndpointProcess, *runtimeEndpointController) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{11, 12, 13})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := store.NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	initial := store.InitialSettings{
		PanelTitle:                       "m-ui",
		UILanguage:                       "en-US",
		PublicHost:                       "node.example.com",
		PanelListenAddress:               "127.0.0.1",
		PanelListenPort:                  2095,
		MihomoExternalControllerBindHost: "127.0.0.1",
		MihomoExternalControllerBindPort: 9090,
		MihomoControllerConnectHost:      "127.0.0.1",
		MihomoControllerConnectPort:      9090,
		MihomoBinaryPath:                 "/usr/local/bin/mihomo",
		MihomoConfigDir:                  "/etc/mihomo",
		MihomoConfigPath:                 "/etc/mihomo/config.yaml",
		BootstrapSecret:                  "synthetic-controller-secret",
		MihomoServiceName:                "mihomo.service",
		HistoryLimit:                     20,
	}
	if err := managed.EnsureInitialSettings(ctx, initial, now); err != nil {
		t.Fatal(err)
	}
	state := domain.DesiredState{
		AsOf:                         now,
		PanelUIBind:                  domain.Endpoint{Host: "127.0.0.1", Port: 2095},
		MihomoExternalControllerBind: domain.Endpoint{Host: "127.0.0.1", Port: 9090},
		MihomoControllerConnect:      domain.Endpoint{Host: "127.0.0.1", Port: 9090},
		ControllerSecret:             "synthetic-controller-secret",
		PublicHost:                   "node.example.com",
	}
	if pending {
		state.AsOf = now.Add(time.Minute)
		state.MihomoExternalControllerBind = domain.Endpoint{Host: "0.0.0.0", Port: 9090}
		state.MihomoControllerConnect = domain.Endpoint{Host: "127.0.0.1", Port: 9090}
	}
	transaction, err := managed.BeginImmediate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.ReplaceDesiredState(ctx, state); err != nil {
		_ = transaction.Rollback(ctx)
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	process := &runtimeEndpointProcess{}
	controller := &runtimeEndpointController{}
	manager := &Manager{
		store:       managed,
		process:     process,
		controller:  controller,
		coordinator: operation.NewCoordinator(),
		readyGuard: func(context.Context) (func() error, error) {
			return func() error { return nil }, nil
		},
		clock: func() time.Time { return now },
	}
	return manager, managed, process, controller
}

type runtimeEndpointProcess struct {
	active       bool
	startCalls   int
	stopCalls    int
	restartCalls int
	reloadCalls  int
}

func (process *runtimeEndpointProcess) IsActive(context.Context) (bool, error) {
	return process.active, nil
}

func (process *runtimeEndpointProcess) Start(context.Context) error {
	process.startCalls++
	process.active = true
	return nil
}

func (process *runtimeEndpointProcess) Stop(context.Context) error {
	process.stopCalls++
	process.active = false
	return nil
}

func (process *runtimeEndpointProcess) Restart(context.Context) error {
	process.restartCalls++
	process.active = true
	return nil
}

func (process *runtimeEndpointProcess) Reload(context.Context) error {
	process.reloadCalls++
	process.active = true
	return nil
}

func (*runtimeEndpointProcess) RecentLogs(context.Context, int) ([]mihomo.LogEntry, error) {
	return nil, nil
}

type runtimeEndpointController struct {
	versionErr   error
	versionCalls int
}

func (controller *runtimeEndpointController) Version(context.Context) (mihomo.Version, error) {
	controller.versionCalls++
	if controller.versionErr != nil {
		return mihomo.Version{}, controller.versionErr
	}
	return mihomo.Version{Meta: true, Version: "v1.19.29"}, nil
}

func (*runtimeEndpointController) Traffic(context.Context) (mihomo.TrafficSnapshot, error) {
	return mihomo.TrafficSnapshot{}, nil
}

func (*runtimeEndpointController) Memory(context.Context) (mihomo.MemorySnapshot, error) {
	return mihomo.MemorySnapshot{}, nil
}

func (*runtimeEndpointController) Connections(context.Context) (mihomo.ConnectionsSnapshot, error) {
	return mihomo.ConnectionsSnapshot{}, nil
}

func (*runtimeEndpointController) Reload(context.Context, string) error  { return nil }
func (*runtimeEndpointController) Restart(context.Context, string) error { return nil }
