package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
		settings.ControllerAddress,
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
