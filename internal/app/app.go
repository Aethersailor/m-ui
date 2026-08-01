package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
		runtimeSettings.ControllerAddress,
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
	})
	if err != nil {
		return fmt.Errorf("initialize management service: %w", err)
	}
	if cfg.Mihomo.ProcessMode == "managed" {
		if err := process.Start(ctx); err != nil {
			return fmt.Errorf("start supervised Mihomo process: %w", err)
		}
	}
	expiryScheduler, err := scheduler.NewExpiry(
		configurationPublisher,
		scheduler.Options{
			Interval: time.Minute,
			Logger:   logger,
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
	if err := startBackgroundServices(
		ctx,
		configurationPublisher,
		managedStore,
		runtimeMonitor,
		expiryScheduler,
		logger,
		coreScheduler,
	); err != nil {
		return err
	}
	server := &http.Server{
		Addr: cfg.Address(),
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

	errCh := make(chan error, 1)
	go func() {
		logger.Info(
			"m-ui server listening",
			"address",
			server.Addr,
			"version",
			build.Version,
		)
		errCh <- server.ListenAndServe()
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
