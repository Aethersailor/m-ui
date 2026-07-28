package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Aethersailor/m-ui/internal/config"
	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/publisher"
	"github.com/Aethersailor/m-ui/internal/store"
)

type commandControlPlane struct {
	database   *store.Store
	managed    *store.ManagedStore
	cli        *mihomo.CLI
	controller *mihomo.Controller
	publisher  *publisher.Publisher
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
	cli, err := mihomo.NewCLI(settings.MihomoBinaryPath)
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
	process, err := mihomo.NewSystemdProcess(settings.MihomoServiceName)
	if err != nil {
		return nil, fmt.Errorf("initialize systemd adapter: %w", err)
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
				info, err := os.Stat(cfg.Mihomo.BinaryPath)
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
			name: "mihomo.service",
			run: func() error {
				command := exec.CommandContext(
					ctx,
					"/usr/bin/systemctl",
					"show",
					"--property=LoadState",
					"--value",
					"mihomo.service",
				)
				value, err := command.Output()
				if err != nil {
					return errors.New("query mihomo.service")
				}
				if strings.TrimSpace(string(value)) != "loaded" {
					return errors.New("mihomo.service is not loaded")
				}
				return nil
			},
		},
		{
			name: "restricted sudoers command",
			run: func() error {
				command := exec.CommandContext(
					ctx,
					"/usr/bin/sudo",
					"-n",
					"-l",
					"/usr/bin/systemctl",
					"restart",
					"mihomo.service",
				)
				if err := command.Run(); err != nil {
					return errors.New("fixed Mihomo systemctl command is not allowed")
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
