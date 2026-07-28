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
	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/httpapi"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/publisher"
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
	coreCLI, err := mihomo.NewCLI(runtimeSettings.MihomoBinaryPath)
	if err != nil {
		return fmt.Errorf("initialize Mihomo CLI: %w", err)
	}
	controller, err := mihomo.NewController(
		runtimeSettings.ControllerAddress,
		runtimeSettings.ControllerSecret,
	)
	if err != nil {
		return fmt.Errorf("initialize Mihomo Controller: %w", err)
	}
	process, err := mihomo.NewSystemdProcess(runtimeSettings.MihomoServiceName)
	if err != nil {
		return fmt.Errorf("initialize Mihomo systemd adapter: %w", err)
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
		Store:      managedStore,
		Publisher:  configurationPublisher,
		CLI:        coreCLI,
		Controller: controller,
		Process:    process,
		Runtime:    runtimeMonitor,
	})
	if err != nil {
		return fmt.Errorf("initialize management service: %w", err)
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
	go runtimeMonitor.Run(ctx)
	go expiryScheduler.Run(ctx)

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
