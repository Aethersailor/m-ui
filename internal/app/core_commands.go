package app

import (
	"context"
	"errors"
	"time"

	"github.com/Aethersailor/m-ui/internal/config"
	coremanagement "github.com/Aethersailor/m-ui/internal/core"
)

func ConfigureCore(
	ctx context.Context,
	cfg config.Config,
	channel coremanagement.Channel,
	autoUpdate bool,
	checkInterval time.Duration,
) error {
	plane, err := openCommandControlPlane(ctx, cfg)
	if err != nil {
		return err
	}
	defer plane.Close()
	settings, err := plane.core.Settings(ctx)
	if err != nil {
		return err
	}
	settings.Channel = channel
	settings.AutoUpdate = autoUpdate
	settings.CheckInterval = checkInterval
	return plane.core.UpdateSettings(ctx, "", settings)
}

func CoreStatus(
	ctx context.Context,
	cfg config.Config,
) (coremanagement.Status, error) {
	plane, err := openCommandControlPlane(ctx, cfg)
	if err != nil {
		return coremanagement.Status{}, err
	}
	defer plane.Close()
	return plane.core.Status(ctx)
}

func CoreCheck(
	ctx context.Context,
	cfg config.Config,
) (coremanagement.ReleaseIdentity, error) {
	plane, err := openCommandControlPlane(ctx, cfg)
	if err != nil {
		return coremanagement.ReleaseIdentity{}, err
	}
	defer plane.Close()
	return plane.core.Check(ctx, "")
}

func CoreUpdate(
	ctx context.Context,
	cfg config.Config,
) (coremanagement.Manifest, bool, error) {
	plane, err := openCommandControlPlane(ctx, cfg)
	if err != nil {
		return coremanagement.Manifest{}, false, err
	}
	defer plane.Close()
	return plane.core.Update(ctx, "")
}

func CoreRollback(
	ctx context.Context,
	cfg config.Config,
) (coremanagement.Manifest, error) {
	plane, err := openCommandControlPlane(ctx, cfg)
	if err != nil {
		return coremanagement.Manifest{}, err
	}
	defer plane.Close()
	return plane.core.Rollback(ctx, "")
}

func CoreBootstrap(
	ctx context.Context,
	cfg config.Config,
	binaryPath, manifestPath string,
) (coremanagement.Manifest, bool, error) {
	if binaryPath == "" || manifestPath == "" {
		return coremanagement.Manifest{}, false, errors.New(
			"bootstrap binary and manifest paths are required",
		)
	}
	plane, err := openCommandControlPlane(ctx, cfg)
	if err != nil {
		return coremanagement.Manifest{}, false, err
	}
	defer plane.Close()
	manifest, changed, err := plane.core.Bootstrap(
		ctx,
		binaryPath,
		manifestPath,
	)
	if err != nil {
		return coremanagement.Manifest{}, false, err
	}
	if err := plane.managed.SetMihomoBinaryPath(
		ctx,
		coremanagement.ManagedBinaryPath,
		manifest.InstalledAt,
	); err != nil {
		return coremanagement.Manifest{}, false, err
	}
	return manifest, changed, nil
}
