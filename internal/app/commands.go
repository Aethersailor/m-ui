package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Aethersailor/m-ui/internal/config"
	coremanagement "github.com/Aethersailor/m-ui/internal/core"
	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/operation"
	"github.com/Aethersailor/m-ui/internal/publisher"
	"github.com/Aethersailor/m-ui/internal/service"
	"github.com/Aethersailor/m-ui/internal/store"
)

type commandControlPlane struct {
	database   *store.Store
	managed    *store.ManagedStore
	cli        *mihomo.CLI
	controller *mihomo.Controller
	publisher  *publisher.Publisher
	core       *coremanagement.Manager
	process    mihomo.CoreProcess
}

type runtimeControlPlane struct {
	database    *store.Store
	managed     *store.ManagedStore
	publisher   *publisher.Publisher
	controller  *mihomo.Controller
	process     mihomo.CoreProcess
	coordinator *operation.Coordinator
}

const runtimeOperationLockPath = "/var/lib/m-ui/runtime-operation.lock"

// WaitForRuntimeReady is the read-only native service-manager gate. It waits
// for m-ui to finish its durable pre-runtime reconciliation and publish the
// live readiness generation before Mihomo is allowed to execute.
func WaitForRuntimeReady(ctx context.Context) error {
	return mihomo.WaitForRuntimeReady(ctx)
}

func openRuntimeControlPlane(
	ctx context.Context,
	cfg config.Config,
	coordinatorOverride *operation.Coordinator,
) (*runtimeControlPlane, error) {
	masterKey, err := muicrypto.LoadMasterKey(cfg.Storage.MasterKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load master key: %w", err)
	}
	sealer, err := muicrypto.NewSealer(masterKey)
	if err != nil {
		return nil, fmt.Errorf("initialize field encryption: %w", err)
	}
	database, err := store.Open(ctx, cfg.Storage.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	closeOnFailure := true
	defer func() {
		if closeOnFailure {
			_ = database.Close()
		}
	}()
	managed, err := store.NewManagedStore(database, sealer)
	if err != nil {
		return nil, fmt.Errorf("initialize managed store: %w", err)
	}
	if err := managed.EnsureInitialSettings(
		ctx,
		initialSettings(cfg),
		time.Now().UTC(),
	); err != nil {
		return nil, fmt.Errorf("initialize managed settings: %w", err)
	}
	settings, err := managed.Settings(ctx)
	if err != nil {
		return nil, fmt.Errorf("load managed settings: %w", err)
	}
	controller, err := mihomo.NewController(
		domain.Endpoint{
			Host: settings.MihomoControllerConnectHost,
			Port: settings.MihomoControllerConnectPort,
		}.Address(),
		settings.ControllerSecret,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize Mihomo Controller: %w", err)
	}
	process, err := mihomo.NewProcess(
		ctx,
		cfg.Mihomo.ProcessMode,
		settings.MihomoBinaryPath,
		settings.MihomoConfigPath,
		settings.MihomoServiceName,
		slog.Default(),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize Mihomo process adapter: %w", err)
	}
	cli, err := mihomo.NewCLI(settings.MihomoBinaryPath)
	if err != nil {
		return nil, fmt.Errorf("initialize Mihomo CLI: %w", err)
	}
	coordinator := coordinatorOverride
	if coordinator == nil {
		coordinator, err = operation.NewFileCoordinator(runtimeOperationLockPath)
		if err != nil {
			return nil, fmt.Errorf("initialize runtime operation coordinator: %w", err)
		}
	}
	configurationPublisher, err := publisher.New(
		managed,
		publisher.YAMLCompiler{},
		cli,
		controller,
		process,
		publisher.Options{
			ConfigPath:        settings.MihomoConfigPath,
			RevisionDirectory: cfg.Mihomo.RevisionDirectory,
			HistoryLimit:      settings.HistoryLimit,
			HealthTimeout:     10 * time.Second,
			HealthInterval:    250 * time.Millisecond,
			Coordinator:       coordinator,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("initialize configuration publisher: %w", err)
	}
	closeOnFailure = false
	return &runtimeControlPlane{
		database:    database,
		managed:     managed,
		publisher:   configurationPublisher,
		controller:  controller,
		process:     process,
		coordinator: coordinator,
	}, nil
}

func (plane *runtimeControlPlane) Close() error {
	return plane.database.Close()
}

// ApplyMihomoRuntime is intentionally narrower than the authenticated
// management API. It is used by native service scripts and service hooks to
// apply the durable endpoint boundary without exposing arbitrary process or
// shell operations.
func ApplyMihomoRuntime(
	ctx context.Context,
	cfg config.Config,
	action string,
) error {
	return applyMihomoRuntime(
		ctx,
		cfg,
		action,
		mihomo.RuntimeLifecycleMarkerPath,
		runtimeOperationLockPath,
	)
}

func applyMihomoRuntime(
	ctx context.Context,
	cfg config.Config,
	action string,
	markerPath string,
	operationLockPath string,
) error {
	switch action {
	case "start", "restart", "finalize":
	default:
		return fmt.Errorf("unsupported Mihomo runtime boundary action %q", action)
	}
	if cfg.Mihomo.ProcessMode == "managed" {
		return errors.New("managed Mihomo startup is owned by m-ui server")
	}
	var finalizerRelease func()
	var finalizerCoordinator *operation.Coordinator
	if action == "finalize" {
		handled, coordinator, release, err := preflightNativeFinalizerWithReadiness(
			ctx,
			markerPath,
			operationLockPath,
		)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
		finalizerCoordinator = coordinator
		finalizerRelease = release
		defer func() {
			if finalizerRelease != nil {
				finalizerRelease()
			}
		}()
	} else {
		readyGuard, err := mihomo.AcquireRuntimeReadyGuard(ctx)
		if err != nil {
			return err
		}
		// Keep the readiness generation pinned until the runtime boundary has
		// released its coordinator. A one-shot readiness sample would allow a
		// new m-ui startup to reset the token in the middle of this action.
		defer func() { _ = readyGuard.Close() }()
	}
	plane, err := openRuntimeControlPlane(ctx, cfg, finalizerCoordinator)
	if err != nil {
		return err
	}
	defer func() { _ = plane.Close() }()
	boundary, err := service.NewRuntimeBoundary(service.RuntimeBoundaryOptions{
		Store:          plane.managed,
		Controller:     plane.controller,
		Process:        plane.process,
		Coordinator:    plane.coordinator,
		HealthTimeout:  10 * time.Second,
		HealthInterval: 100 * time.Millisecond,
	})
	if err != nil {
		return err
	}
	if action == "finalize" {
		if finalizerRelease != nil {
			if err := boundary.FinalizeLocked(ctx); err != nil {
				return err
			}
			return plane.publisher.ReconcileStartupLocked(ctx)
		}
		if err := boundary.Finalize(ctx); err != nil {
			return err
		}
		return plane.publisher.ReconcileStartup(ctx)
	}
	return boundary.Run(ctx, action)
}

// preflightNativeFinalizer is the marker/coordinator-only primitive used by
// tests and callers that already established application readiness.
func preflightNativeFinalizer(
	markerPath string,
	operationLockPath string,
) (
	handled bool,
	coordinator *operation.Coordinator,
	release func(),
	err error,
) {
	return preflightNativeFinalizerCore(
		markerPath,
		operationLockPath,
		context.Background(),
		nil,
	)
}

// preflightNativeFinalizerWithReadiness performs the native service finalizer
// preflight before encrypted SQLite access and waits for m-ui startup
// readiness before it can acquire the runtime coordinator. The readiness guard
// is deliberately skipped for a live lifecycle marker: that path belongs to
// the m-ui process which will perform health/CAS itself, and waiting there
// would recreate the service-manager cycle this marker protocol prevents.
func preflightNativeFinalizerWithReadiness(
	ctx context.Context,
	markerPath string,
	operationLockPath string,
) (
	handled bool,
	coordinator *operation.Coordinator,
	release func(),
	err error,
) {
	return preflightNativeFinalizerCore(
		markerPath,
		operationLockPath,
		ctx,
		func(guardContext context.Context) (func() error, error) {
			guard, err := mihomo.AcquireRuntimeReadyGuard(guardContext)
			if err != nil {
				return nil, err
			}
			return guard.Close, nil
		},
	)
}

func preflightNativeFinalizerWithReadinessAt(
	ctx context.Context,
	markerPath string,
	operationLockPath string,
	leasePath string,
	readyPath string,
) (
	handled bool,
	coordinator *operation.Coordinator,
	release func(),
	err error,
) {
	return preflightNativeFinalizerCore(
		markerPath,
		operationLockPath,
		ctx,
		func(waitContext context.Context) (func() error, error) {
			guard, err := mihomo.AcquireRuntimeReadyGuardAt(
				waitContext,
				leasePath,
				readyPath,
			)
			if err != nil {
				return nil, err
			}
			return guard.Close, nil
		},
	)
}

type runtimeReadyGuardAcquirer func(context.Context) (func() error, error)

func preflightNativeFinalizerCore(
	markerPath string,
	operationLockPath string,
	ctx context.Context,
	acquireReady runtimeReadyGuardAcquirer,
) (
	handled bool,
	coordinator *operation.Coordinator,
	release func(),
	err error,
) {
	probe, live, err := mihomo.ProbeRuntimeLifecycleMarkerAt(markerPath)
	if err != nil {
		return false, nil, nil, fmt.Errorf("inspect Mihomo lifecycle marker: %w", err)
	}
	if probe != nil {
		// Never retain a stale shared observer while waiting for readiness or
		// acquiring the coordinator. The m-ui owner must be able to take the
		// marker exclusively after its own coordinator is acquired.
		if err := probe.Close(); err != nil {
			return false, nil, nil, fmt.Errorf("release stale Mihomo lifecycle marker probe: %w", err)
		}
		probe = nil
	}

	// A live marker is the m-ui-owned lifecycle fast path. It must be checked
	// before taking a readiness guard, otherwise the native service hook would
	// wait on the same m-ui startup generation that is waiting for the hook.
	if live {
		coordinator, err = operation.NewFileCoordinator(operationLockPath)
		if err != nil {
			return false, nil, nil, fmt.Errorf("initialize runtime operation coordinator: %w", err)
		}
		coordinatorRelease, lockErr := coordinator.TryAcquire()
		if errors.Is(lockErr, operation.ErrBusy) {
			// The live marker is held by the m-ui process that owns the
			// coordinator. It performs the health check and CAS after the
			// native service command returns.
			return true, nil, nil, nil
		}
		if lockErr != nil {
			return false, nil, nil, lockErr
		}
		coordinatorRelease()
		return false, nil, nil, errors.New(
			"Mihomo lifecycle marker is live without the runtime coordinator",
		)
	}

	var readyRelease func() error
	if acquireReady != nil {
		readyRelease, err = acquireReady(ctx)
		if err != nil {
			return false, nil, nil, err
		}
	}
	coordinator, err = operation.NewFileCoordinator(operationLockPath)
	if err != nil {
		if readyRelease != nil {
			_ = readyRelease()
		}
		return false, nil, nil, fmt.Errorf("initialize runtime operation coordinator: %w", err)
	}
	coordinatorRelease, lockErr := coordinator.TryAcquire()
	if lockErr != nil {
		if readyRelease != nil {
			_ = readyRelease()
		}
		return false, nil, nil, lockErr
	}

	// Re-probe after both guards are held. This closes the stale-marker to
	// coordinator window and fail-closes if another lifecycle owner appeared
	// while readiness was being acquired.
	secondProbe, secondLive, probeErr := mihomo.ProbeRuntimeLifecycleMarkerAt(markerPath)
	if probeErr != nil {
		coordinatorRelease()
		if readyRelease != nil {
			_ = readyRelease()
		}
		return false, nil, nil, fmt.Errorf("recheck Mihomo lifecycle marker: %w", probeErr)
	}
	if secondProbe != nil {
		if closeErr := secondProbe.Close(); closeErr != nil {
			coordinatorRelease()
			if readyRelease != nil {
				_ = readyRelease()
			}
			return false, nil, nil, fmt.Errorf("release stale Mihomo lifecycle marker probe: %w", closeErr)
		}
	}
	if secondLive {
		coordinatorRelease()
		if readyRelease != nil {
			_ = readyRelease()
		}
		return false, nil, nil, errors.New(
			"Mihomo lifecycle marker became live during finalizer preflight",
		)
	}
	return false, coordinator, func() {
		coordinatorRelease()
		if readyRelease != nil {
			_ = readyRelease()
		}
	}, nil
}

func openCommandControlPlane(
	ctx context.Context,
	cfg config.Config,
) (*commandControlPlane, error) {
	masterKey, err := muicrypto.LoadMasterKey(cfg.Storage.MasterKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load master key: %w", err)
	}
	sealer, err := muicrypto.NewSealer(masterKey)
	if err != nil {
		return nil, fmt.Errorf("initialize field encryption: %w", err)
	}
	database, err := store.Open(ctx, cfg.Storage.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	closeOnFailure := true
	defer func() {
		if closeOnFailure {
			_ = database.Close()
		}
	}()
	managed, err := store.NewManagedStore(database, sealer)
	if err != nil {
		return nil, fmt.Errorf("initialize managed store: %w", err)
	}
	if err := managed.EnsureInitialSettings(
		ctx,
		initialSettings(cfg),
		time.Now().UTC(),
	); err != nil {
		return nil, fmt.Errorf("initialize managed settings: %w", err)
	}
	settings, err := managed.Settings(ctx)
	if err != nil {
		return nil, fmt.Errorf("load managed settings: %w", err)
	}
	coreDefaults := coremanagement.Settings{
		Channel:       coremanagement.ChannelRelease,
		AutoUpdate:    false,
		CheckInterval: coremanagement.DefaultCheckInterval,
		Managed:       cfg.Mihomo.ManagedCore,
	}
	if !coreDefaults.Managed {
		coreDefaults.ExternalPath = settings.MihomoBinaryPath
	}
	if err := managed.EnsureCoreSettings(
		ctx,
		coreDefaults,
		time.Now().UTC(),
	); err != nil {
		return nil, fmt.Errorf("initialize core settings: %w", err)
	}
	coreSettings, err := managed.CoreSettings(ctx)
	if err != nil {
		return nil, err
	}
	binaryPath := coreSettings.ExternalPath
	if coreSettings.Managed {
		binaryPath = coremanagement.ManagedBinaryPath
	}
	cli, err := mihomo.NewCLI(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("initialize Mihomo CLI: %w", err)
	}
	controller, err := mihomo.NewController(
		domain.Endpoint{
			Host: settings.MihomoControllerConnectHost,
			Port: settings.MihomoControllerConnectPort,
		}.Address(),
		settings.ControllerSecret,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize Mihomo Controller: %w", err)
	}
	process, err := mihomo.NewProcess(
		ctx,
		cfg.Mihomo.ProcessMode,
		binaryPath,
		settings.MihomoConfigPath,
		settings.MihomoServiceName,
		slog.Default(),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize process adapter: %w", err)
	}
	coordinator, err := operation.NewFileCoordinator(
		"/var/lib/m-ui/runtime-operation.lock",
	)
	if err != nil {
		return nil, err
	}
	files, err := coremanagement.NewFileStore("/var/lib/m-ui/core")
	if err != nil {
		return nil, err
	}
	upstream, err := coremanagement.NewGitHubClient(
		coremanagement.GitHubClientOptions{},
	)
	if err != nil {
		return nil, err
	}
	coreManager, err := coremanagement.NewManager(
		coremanagement.ManagerOptions{
			Repository:  managed,
			Upstream:    upstream,
			Files:       files,
			Process:     process,
			Controller:  controller,
			Coordinator: coordinator,
			ConfigPath:  settings.MihomoConfigPath,
		},
	)
	if err != nil {
		return nil, err
	}
	if err := coreManager.Recover(ctx); err != nil {
		return nil, err
	}
	configurationPublisher, err := publisher.New(
		managed,
		publisher.YAMLCompiler{},
		cli,
		controller,
		process,
		publisher.Options{
			ConfigPath:        settings.MihomoConfigPath,
			RevisionDirectory: cfg.Mihomo.RevisionDirectory,
			HistoryLimit:      settings.HistoryLimit,
			HealthTimeout:     10 * time.Second,
			HealthInterval:    250 * time.Millisecond,
			Coordinator:       coordinator,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("initialize configuration publisher: %w", err)
	}
	closeOnFailure = false
	return &commandControlPlane{
		database:   database,
		managed:    managed,
		cli:        cli,
		controller: controller,
		publisher:  configurationPublisher,
		core:       coreManager,
		process:    process,
	}, nil
}

func (plane *commandControlPlane) Close() error {
	return plane.database.Close()
}

func ValidateConfiguration(
	ctx context.Context,
	cfg config.Config,
) (string, error) {
	plane, err := openCommandControlPlane(ctx, cfg)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = plane.Close()
	}()
	content, err := plane.publisher.ValidateCurrent(ctx, time.Now().UTC())
	if err != nil {
		return "", err
	}
	return publisher.SHA256(content), nil
}

func RollbackConfiguration(
	ctx context.Context,
	cfg config.Config,
	revisionID string,
) (domain.Revision, error) {
	if strings.TrimSpace(revisionID) == "" {
		return domain.Revision{}, errors.New("revision ID is required")
	}
	plane, err := openCommandControlPlane(ctx, cfg)
	if err != nil {
		return domain.Revision{}, err
	}
	defer func() {
		_ = plane.Close()
	}()
	return plane.publisher.Rollback(ctx, revisionID, "")
}

type doctorCheck struct {
	name string
	run  func() error
}

func panelHealthEndpoint(
	settings store.RuntimeSettings,
) (domain.Endpoint, error) {
	host := settings.PanelListenAddress
	switch host {
	case "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	if net.ParseIP(host) == nil {
		return domain.Endpoint{}, fmt.Errorf(
			"panel listen address %q is not an IP literal",
			host,
		)
	}
	if settings.PanelListenPort == 0 {
		return domain.Endpoint{}, errors.New("panel listen port is invalid")
	}
	return domain.Endpoint{
		Host: host,
		Port: settings.PanelListenPort,
	}, nil
}

func checkPanelHealth(
	ctx context.Context,
	settings store.RuntimeSettings,
) error {
	endpoint, err := panelHealthEndpoint(settings)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"http://"+endpoint.Address()+"/api/v1/health",
		nil,
	)
	if err != nil {
		return fmt.Errorf("create panel health request: %w", err)
	}
	client := http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request panel health endpoint: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf(
			"panel health endpoint returned HTTP status %d",
			response.StatusCode,
		)
	}
	return nil
}

// PanelHealth is the restricted package/service health boundary. It opens
// only the m-ui store, reads the durable active panel endpoint, and performs
// the panel HTTP health request. It deliberately does not initialize or test
// Mihomo, so package restoration can validate a panel-only active state.
func PanelHealth(ctx context.Context, cfg config.Config) error {
	masterKey, err := muicrypto.LoadMasterKey(cfg.Storage.MasterKeyPath)
	if err != nil {
		return fmt.Errorf("load master key: %w", err)
	}
	sealer, err := muicrypto.NewSealer(masterKey)
	if err != nil {
		return fmt.Errorf("initialize field encryption: %w", err)
	}
	database, err := store.Open(ctx, cfg.Storage.DatabasePath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = database.Close() }()
	managed, err := store.NewManagedStore(database, sealer)
	if err != nil {
		return fmt.Errorf("initialize managed store: %w", err)
	}
	if err := managed.EnsureInitialSettings(
		ctx,
		initialSettings(cfg),
		time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("initialize managed settings: %w", err)
	}
	settings, err := managed.Settings(ctx)
	if err != nil {
		return fmt.Errorf("load managed settings: %w", err)
	}
	return checkPanelHealth(ctx, settings)
}

func Doctor(
	ctx context.Context,
	cfg config.Config,
	output io.Writer,
) error {
	plane, err := openCommandControlPlane(ctx, cfg)
	if err != nil {
		return fmt.Errorf("initialize doctor checks: %w", err)
	}
	defer func() {
		_ = plane.Close()
	}()

	checks := []doctorCheck{
		{
			name: "database and master key",
			run: func() error {
				_, err := plane.managed.SystemState(ctx)
				return err
			},
		},
		{
			name: "m-ui panel listener",
			run: func() error {
				settings, err := plane.managed.Settings(ctx)
				if err != nil {
					return err
				}
				return checkPanelHealth(ctx, settings)
			},
		},
		{
			name: "Mihomo binary",
			run: func() error {
				coreSettings, err := plane.managed.CoreSettings(ctx)
				if err != nil {
					return err
				}
				binaryPath := coreSettings.ExternalPath
				if coreSettings.Managed {
					binaryPath = coremanagement.ManagedBinaryPath
				}
				info, err := os.Stat(binaryPath)
				if err != nil {
					return err
				}
				if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
					return errors.New("binary is not a regular executable file")
				}
				_, err = plane.cli.Version(ctx)
				return err
			},
		},
		{
			name: "Mihomo configuration directory",
			run: func() error {
				file, err := os.CreateTemp(
					cfg.Mihomo.ConfigDirectory,
					".m-ui-doctor-*",
				)
				if err != nil {
					return err
				}
				name := file.Name()
				if closeErr := file.Close(); closeErr != nil {
					_ = os.Remove(name)
					return closeErr
				}
				return os.Remove(name)
			},
		},
		{
			name: "generated configuration validation",
			run: func() error {
				_, err := plane.publisher.ValidateCurrent(ctx, time.Now().UTC())
				return err
			},
		},
		{
			name: "active configuration matches desired state",
			run: func() error {
				compiled, _, err := plane.publisher.CompileCurrent(
					ctx,
					time.Now().UTC(),
				)
				if err != nil {
					return err
				}
				active, err := os.ReadFile(cfg.Mihomo.ConfigPath)
				if err != nil {
					return err
				}
				if publisher.SHA256(active) != publisher.SHA256(compiled) {
					return errors.New("active YAML SHA-256 differs from desired state")
				}
				return nil
			},
		},
		{
			name: "Mihomo Controller",
			run: func() error {
				_, err := plane.controller.Version(ctx)
				return err
			},
		},
		{
			name: "Mihomo process adapter",
			run: func() error {
				active, err := plane.process.IsActive(ctx)
				if err != nil {
					return err
				}
				if !active {
					return errors.New("mihomo process is not active")
				}
				return nil
			},
		},
	}

	failures := 0
	for _, check := range checks {
		if err := check.run(); err != nil {
			failures++
			if _, writeErr := fmt.Fprintf(
				output,
				"[FAIL] %s: %v\n",
				check.name,
				err,
			); writeErr != nil {
				return fmt.Errorf("write doctor result: %w", writeErr)
			}
			continue
		}
		if _, err := fmt.Fprintf(output, "[ OK ] %s\n", check.name); err != nil {
			return fmt.Errorf("write doctor result: %w", err)
		}
	}
	if failures != 0 {
		return fmt.Errorf("doctor found %d failed check(s)", failures)
	}
	return nil
}
