package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Aethersailor/m-ui/internal/auth"
	"github.com/Aethersailor/m-ui/internal/config"
	coremanagement "github.com/Aethersailor/m-ui/internal/core"
	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/httpapi"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/operation"
	"github.com/Aethersailor/m-ui/internal/publisher"
	"github.com/Aethersailor/m-ui/internal/redact"
	"github.com/Aethersailor/m-ui/internal/scheduler"
	"github.com/Aethersailor/m-ui/internal/service"
	"github.com/Aethersailor/m-ui/internal/store"
	"github.com/Aethersailor/m-ui/internal/version"
)

func Run(ctx context.Context, cfg config.Config, build version.Info) error {
	logger, err := newLogger(cfg.Logging)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	readHeaderTimeout, err := cfg.ReadHeaderTimeout()
	if err != nil {
		return err
	}
	shutdownTimeout, err := cfg.ShutdownTimeout()
	if err != nil {
		return err
	}
	sessionTTL, err := cfg.SessionTTL()
	if err != nil {
		return err
	}
	startup, err := mihomo.BeginRuntimeStartup()
	if err != nil {
		return fmt.Errorf("initialize m-ui startup readiness: %w", err)
	}
	defer func() {
		if closeErr := startup.Close(); closeErr != nil {
			logger.Error("close m-ui startup readiness", "error", closeErr)
		}
	}()

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
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			logger.Error("close store", "error", closeErr)
		}
	}()
	if err := auth.EnsureBootstrap(
		ctx,
		database,
		sealer,
		nil,
		time.Now,
	); err != nil {
		return fmt.Errorf("initialize administrator bootstrap: %w", err)
	}
	authService, err := auth.NewService(database, auth.Options{
		SessionTTL: sessionTTL,
	})
	if err != nil {
		return fmt.Errorf("initialize authentication: %w", err)
	}
	if err := database.DeleteExpiredSessions(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	managedStore, err := store.NewManagedStore(database, sealer)
	if err != nil {
		return fmt.Errorf("initialize managed store: %w", err)
	}
	if err := managedStore.EnsureInitialSettings(
		ctx,
		initialSettings(cfg),
		time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("initialize managed settings: %w", err)
	}
	runtimeSettings, err := managedStore.Settings(ctx)
	if err != nil {
		return fmt.Errorf("load managed settings: %w", err)
	}
	coreDefaults := coremanagement.Settings{
		Channel:       coremanagement.ChannelRelease,
		AutoUpdate:    false,
		CheckInterval: coremanagement.DefaultCheckInterval,
		Managed:       cfg.Mihomo.ManagedCore,
	}
	if !coreDefaults.Managed {
		coreDefaults.ExternalPath = runtimeSettings.MihomoBinaryPath
	}
	if err := managedStore.EnsureCoreSettings(
		ctx,
		coreDefaults,
		time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("initialize core settings: %w", err)
	}
	coreSettings, err := managedStore.CoreSettings(ctx)
	if err != nil {
		return fmt.Errorf("load core settings: %w", err)
	}
	coordinator, err := operation.NewFileCoordinator(
		"/var/lib/m-ui/runtime-operation.lock",
	)
	if err != nil {
		return fmt.Errorf("initialize runtime operation coordinator: %w", err)
	}
	controller, err := mihomo.NewController(
		domain.Endpoint{
			Host: runtimeSettings.MihomoControllerConnectHost,
			Port: runtimeSettings.MihomoControllerConnectPort,
		}.Address(),
		runtimeSettings.ControllerSecret,
	)
	if err != nil {
		return fmt.Errorf("initialize Mihomo Controller: %w", err)
	}
	processBinaryPath := coreSettings.ExternalPath
	if coreSettings.Managed {
		processBinaryPath = coremanagement.ManagedBinaryPath
	}
	process, err := mihomo.NewProcess(
		ctx,
		cfg.Mihomo.ProcessMode,
		processBinaryPath,
		runtimeSettings.MihomoConfigPath,
		runtimeSettings.MihomoServiceName,
		logger,
	)
	if err != nil {
		return fmt.Errorf("initialize Mihomo process adapter: %w", err)
	}
	coreFiles, err := coremanagement.NewFileStore("/var/lib/m-ui/core")
	if err != nil {
		return fmt.Errorf("initialize managed core filesystem: %w", err)
	}
	upstream, err := coremanagement.NewGitHubClient(
		coremanagement.GitHubClientOptions{
			UserAgent: "m-ui/" + build.Version,
		},
	)
	if err != nil {
		return fmt.Errorf("initialize Mihomo upstream client: %w", err)
	}
	coreManager, err := coremanagement.NewManager(
		coremanagement.ManagerOptions{
			Repository:     managedStore,
			Upstream:       upstream,
			Files:          coreFiles,
			Process:        process,
			Controller:     controller,
			Coordinator:    coordinator,
			EndpointGate:   managedStore,
			ConfigPath:     runtimeSettings.MihomoConfigPath,
			HealthTimeout:  10 * time.Second,
			HealthInterval: 250 * time.Millisecond,
			Logger:         logger,
		},
	)
	if err != nil {
		return fmt.Errorf("initialize Mihomo core manager: %w", err)
	}
	if err := coreManager.Recover(ctx); err != nil {
		return fmt.Errorf("recover managed Mihomo core state: %w", err)
	}
	if coreSettings.Managed {
		if _, err := coreFiles.Current(); errors.Is(err, os.ErrNotExist) {
			if runtimeSettings.MihomoBinaryPath == coremanagement.ManagedBinaryPath {
				return errors.New(
					"managed Mihomo core is missing; run m-ui core bootstrap",
				)
			}
			if _, _, err := coreManager.AdoptExternal(
				ctx,
				runtimeSettings.MihomoBinaryPath,
			); err != nil {
				return fmt.Errorf("adopt existing Mihomo core: %w", err)
			}
			if err := managedStore.SetMihomoBinaryPath(
				ctx,
				coremanagement.ManagedBinaryPath,
				time.Now().UTC(),
			); err != nil {
				return err
			}
			runtimeSettings.MihomoBinaryPath = coremanagement.ManagedBinaryPath
		} else if err != nil {
			return fmt.Errorf("verify managed Mihomo core: %w", err)
		}
	} else {
		runtimeSettings.MihomoBinaryPath = coreSettings.ExternalPath
	}
	coreCLI, err := mihomo.NewCLI(runtimeSettings.MihomoBinaryPath)
	if err != nil {
		return fmt.Errorf("initialize Mihomo CLI: %w", err)
	}
	configurationPublisher, err := publisher.New(
		managedStore,
		publisher.YAMLCompiler{},
		coreCLI,
		controller,
		process,
		publisher.Options{
			ConfigPath:        runtimeSettings.MihomoConfigPath,
			RevisionDirectory: cfg.Mihomo.RevisionDirectory,
			HistoryLimit:      runtimeSettings.HistoryLimit,
			HealthTimeout:     10 * time.Second,
			HealthInterval:    250 * time.Millisecond,
			Coordinator:       coordinator,
			SafetyGate:        coreManager,
		},
	)
	if err != nil {
		return fmt.Errorf("initialize configuration publisher: %w", err)
	}
	runtimeMonitor, err := service.NewRuntimeMonitor(
		controller,
		process,
		service.RuntimeMonitorOptions{
			Interval: 2 * time.Second,
			Logger:   logger,
			ShouldCollect: func(ctx context.Context) (bool, error) {
				endpointState, err := managedStore.EndpointSettings(ctx)
				if err != nil {
					return false, err
				}
				return endpointState.Pending == nil ||
					!endpointState.Pending.RequiresMihomoRestart, nil
			},
		},
	)
	if err != nil {
		return fmt.Errorf("initialize runtime monitor: %w", err)
	}
	manager, err := service.NewManager(service.ManagerOptions{
		Store:       managedStore,
		Publisher:   configurationPublisher,
		CLI:         coreCLI,
		Controller:  controller,
		Process:     process,
		Runtime:     runtimeMonitor,
		Core:        coreManager,
		Coordinator: coordinator,
		ReadyGuard: func(guardContext context.Context) (func() error, error) {
			guard, guardErr := mihomo.AcquireRuntimeReadyGuard(guardContext)
			if guardErr != nil {
				return nil, guardErr
			}
			return guard.Close, nil
		},
	})
	if err != nil {
		return fmt.Errorf("initialize management service: %w", err)
	}
	// Reconcile the durable revision/YAML relationship before any managed
	// Mihomo process can be started. In particular, a crash after atomic YAML
	// replacement but before the SQLite commit must be repaired from the
	// durable revision before the next process observes the file. The later
	// background startup pass remains intentional: it performs runtime health
	// verification after managed startup and preserves native service ordering.
	startupReconcileErr := configurationPublisher.ReconcileStartupBeforeRuntime(ctx)
	if startupReconcileErr != nil &&
		!errors.Is(startupReconcileErr, publisher.ErrStartupDegraded) {
		return fmt.Errorf(
			"reconcile startup publication state before Mihomo start: %w",
			startupReconcileErr,
		)
	}
	if cfg.Mihomo.ProcessMode == "managed" {
		recoveryConfigurer, ok := process.(mihomo.RecoveryConfigurer)
		if !ok {
			return errors.New(
				"managed Mihomo process does not expose the application recovery boundary",
			)
		}
		if err := recoveryConfigurer.SetRecovery(
			manager.StartManagedProcess,
		); err != nil {
			return fmt.Errorf("configure managed Mihomo recovery boundary: %w", err)
		}
		if startupReconcileErr == nil {
			if err := manager.StartManagedProcess(ctx); err != nil {
				return fmt.Errorf("start supervised Mihomo process: %w", err)
			}
		} else {
			logger.Warn(
				"managed Mihomo start is held while startup publication remains degraded",
				"error",
				redact.Text(startupReconcileErr.Error()),
			)
		}
	}
	expiryScheduler, err := scheduler.NewExpiry(
		configurationPublisher,
		scheduler.Options{
			Interval:   time.Minute,
			Logger:     logger,
			SafetyGate: coreManager,
		},
	)
	if err != nil {
		return fmt.Errorf("initialize expiry scheduler: %w", err)
	}
	coreScheduler, err := scheduler.NewCore(
		coreManager,
		scheduler.CoreOptions{
			PollInterval: time.Minute,
			Logger:       logger,
		},
	)
	if err != nil {
		return fmt.Errorf("initialize core update scheduler: %w", err)
	}
	coreManager.SetWake(coreScheduler.Wake)
	if cfg.Mihomo.ProcessMode == "managed" {
		if err := startBackgroundServicesWithSafetyGate(
			ctx,
			configurationPublisher,
			managedStore,
			runtimeMonitor,
			expiryScheduler,
			logger,
			coreManager,
			coreScheduler,
		); err != nil {
			return err
		}
	}
	server := &http.Server{
		Addr: domain.Endpoint{
			Host: runtimeSettings.PanelListenAddress,
			Port: runtimeSettings.PanelListenPort,
		}.Address(),
		Handler: httpapi.New(httpapi.Options{
			Logger:       logger,
			Build:        build,
			Auth:         authService,
			Management:   manager,
			CookieSecure: cfg.Security.CookieSecure,
		}),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	endpointState, err := managedStore.EndpointSettings(ctx)
	if err != nil {
		return fmt.Errorf("read m-ui endpoint restart state: %w", err)
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("bind m-ui HTTP listener: %w", err)
	}
	if err := managedStore.ClearEndpointRestartRequirements(
		ctx,
		true,
		false,
		endpointState.Active.Generation,
		endpointState.Active,
	); err != nil {
		_ = listener.Close()
		return fmt.Errorf("clear applied m-ui endpoint restart state: %w", err)
	}
	if err := startup.PublishReady(); err != nil {
		_ = listener.Close()
		return fmt.Errorf("publish m-ui startup readiness: %w", err)
	}
	if cfg.Mihomo.ProcessMode != "managed" {
		// Native service managers are allowed to start Mihomo only after this
		// readiness token. Defer all runtime reconciliation and schedulers until
		// the service manager has crossed that boundary and authenticated the
		// Controller; the pre-runtime phase above must remain side-effect free.
		go func() {
			if err := waitForRuntimeHealthy(ctx, process, controller); err != nil {
				if ctx.Err() == nil {
					logger.Error(
						"native Mihomo did not become healthy; background services remain stopped",
						"error",
						redact.Text(err.Error()),
					)
				}
				return
			}
			if err := startBackgroundServicesWithSafetyGate(
				ctx,
				configurationPublisher,
				managedStore,
				runtimeMonitor,
				expiryScheduler,
				logger,
				coreManager,
				coreScheduler,
			); err != nil {
				logger.Error(
					"start native post-runtime background services",
					"error",
					redact.Text(err.Error()),
				)
			}
		}()
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info(
			"m-ui server listening",
			"address",
			server.Addr,
			"version",
			build.Version,
		)
		errCh <- server.Serve(listener)
	}()

	select {
	case serveErr := <-errCh:
		if errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", serveErr)
	case <-ctx.Done():
		logger.Info("shutting down m-ui server")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down HTTP server: %w", err)
	}
	return nil
}

func waitForRuntimeHealthy(
	ctx context.Context,
	process mihomo.CoreProcess,
	controller mihomo.CoreController,
) error {
	for {
		healthContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		active, processErr := process.IsActive(healthContext)
		_, controllerErr := controller.Version(healthContext)
		cancel()
		if processErr == nil && active && controllerErr == nil {
			return nil
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type startupReconciler interface {
	ReconcileStartup(context.Context) error
}

type systemStateReader interface {
	SystemState(context.Context) (domain.SystemState, error)
}

type backgroundRunner interface {
	Run(context.Context)
}

func startBackgroundServices(
	ctx context.Context,
	reconciler startupReconciler,
	stateReader systemStateReader,
	runtimeMonitor backgroundRunner,
	expiryScheduler backgroundRunner,
	logger *slog.Logger,
	additionalSchedulers ...backgroundRunner,
) error {
	return startBackgroundServicesWithSafetyGate(
		ctx,
		reconciler,
		stateReader,
		runtimeMonitor,
		expiryScheduler,
		logger,
		nil,
		additionalSchedulers...,
	)
}

type safetyGate interface {
	SafetyBlocked(context.Context) (bool, error)
}

func startBackgroundServicesWithSafetyGate(
	ctx context.Context,
	reconciler startupReconciler,
	stateReader systemStateReader,
	runtimeMonitor backgroundRunner,
	expiryScheduler backgroundRunner,
	logger *slog.Logger,
	gate safetyGate,
	additionalSchedulers ...backgroundRunner,
) error {
	reconcileErr := reconciler.ReconcileStartup(ctx)
	reconcileDegraded := errors.Is(reconcileErr, publisher.ErrStartupDegraded)
	if reconcileErr != nil && !reconcileDegraded {
		return fmt.Errorf("reconcile startup publication state: %w", reconcileErr)
	}
	if reconcileErr != nil {
		logger.Warn(
			"startup reconciliation completed in degraded mode",
			"error",
			redact.Text(reconcileErr.Error()),
		)
	}
	systemState, err := stateReader.SystemState(ctx)
	if err != nil {
		return fmt.Errorf("read system state after startup reconciliation: %w", err)
	}
	if reconcileDegraded && !systemState.Degraded {
		return errors.New(
			"startup reconciliation returned degraded but durable system state is not degraded",
		)
	}
	go runtimeMonitor.Run(ctx)
	if systemState.Degraded {
		logger.Warn(
			"expiry scheduler is disabled while configuration publishing is degraded",
		)
		return nil
	}
	if gate != nil {
		blocked, err := gate.SafetyBlocked(ctx)
		if err != nil {
			return fmt.Errorf("read fail-closed safety state after startup reconciliation: %w", err)
		}
		if blocked {
			logger.Warn(
				"expiry scheduler is disabled while the managed core is fail-closed",
			)
			return nil
		}
	}
	go expiryScheduler.Run(ctx)
	for _, runner := range additionalSchedulers {
		go runner.Run(ctx)
	}
	return nil
}

func newLogger(cfg config.Logging) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToLower(cfg.Level))); err != nil {
		return nil, fmt.Errorf("parse logging level: %w", err)
	}

	options := &slog.HandlerOptions{Level: level}
	switch strings.ToLower(cfg.Format) {
	case "text":
		return slog.New(slog.NewTextHandler(os.Stderr, options)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stderr, options)), nil
	default:
		return nil, fmt.Errorf("unsupported logging format %q", cfg.Format)
	}
}
