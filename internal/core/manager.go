package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/operation"
	"github.com/Aethersailor/m-ui/internal/redact"
)

var (
	ErrDegraded              = errors.New("core updates are disabled while the system is degraded")
	ErrExternal              = errors.New("external Mihomo core is not managed by m-ui")
	ErrNoBackup              = errors.New("no previous managed Mihomo core backup is available")
	ErrMihomoRestartRequired = errors.New("mihomo restart is required before core mutation")
)

// EndpointRestartGate is implemented by the durable settings store. Core
// activation restarts Mihomo, so it must not be allowed to apply a different
// binary while a previously saved endpoint candidate still awaits its
// explicit restart boundary.
type EndpointRestartGate interface {
	MihomoRestartRequired(context.Context) (bool, error)
}

type ManagerOptions struct {
	Repository     Repository
	Upstream       Upstream
	Files          *FileStore
	Process        mihomo.CoreProcess
	Controller     mihomo.CoreController
	Coordinator    *operation.Coordinator
	EndpointGate   EndpointRestartGate
	ConfigPath     string
	Architecture   string
	Clock          func() time.Time
	HealthCheck    func(context.Context) error
	HealthTimeout  time.Duration
	HealthInterval time.Duration
	Logger         *slog.Logger
	NewCLI         func(string) (mihomo.CoreCLI, error)
}

type Manager struct {
	repository     Repository
	upstream       Upstream
	files          *FileStore
	process        mihomo.CoreProcess
	controller     mihomo.CoreController
	coordinator    *operation.Coordinator
	endpointGate   EndpointRestartGate
	configPath     string
	architecture   string
	clock          func() time.Time
	healthCheck    func(context.Context) error
	healthTimeout  time.Duration
	healthInterval time.Duration
	logger         *slog.Logger
	newCLI         func(string) (mihomo.CoreCLI, error)
	wake           atomic.Value
	failClosed     atomic.Bool
}

// settingsStateRepository is implemented by the production SQLite store.  It
// keeps a settings change and its scheduler state in one database transaction;
// the optional shape preserves small command/test repositories that only need
// the original Repository contract.
type settingsStateRepository interface {
	UpdateCoreSettingsAndState(context.Context, Settings, State, time.Time) error
}

func NewManager(options ManagerOptions) (*Manager, error) {
	switch {
	case options.Repository == nil:
		return nil, errors.New("core repository is required")
	case options.Upstream == nil:
		return nil, errors.New("core upstream client is required")
	case options.Files == nil:
		return nil, errors.New("managed core file store is required")
	case options.Process == nil:
		return nil, errors.New("mihomo process adapter is required")
	case options.Controller == nil:
		return nil, errors.New("mihomo controller is required")
	case options.Coordinator == nil:
		return nil, errors.New("runtime operation coordinator is required")
	case !filepath.IsAbs(options.ConfigPath):
		return nil, errors.New("mihomo configuration path must be absolute")
	}
	if options.Architecture == "" {
		options.Architecture = runtime.GOARCH
	}
	if options.Architecture != "amd64" && options.Architecture != "arm64" {
		return nil, errors.New("only linux amd64 and arm64 are supported")
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.HealthTimeout <= 0 {
		options.HealthTimeout = 10 * time.Second
	}
	if options.HealthInterval <= 0 {
		options.HealthInterval = 250 * time.Millisecond
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.NewCLI == nil {
		options.NewCLI = func(path string) (mihomo.CoreCLI, error) {
			return mihomo.NewCLI(path)
		}
	}
	manager := &Manager{
		repository:     options.Repository,
		upstream:       options.Upstream,
		files:          options.Files,
		process:        options.Process,
		controller:     options.Controller,
		coordinator:    options.Coordinator,
		endpointGate:   options.EndpointGate,
		configPath:     options.ConfigPath,
		architecture:   options.Architecture,
		clock:          options.Clock,
		healthCheck:    options.HealthCheck,
		healthTimeout:  options.HealthTimeout,
		healthInterval: options.HealthInterval,
		logger:         options.Logger,
		newCLI:         options.NewCLI,
	}
	if manager.healthCheck == nil {
		manager.healthCheck = manager.defaultHealthCheck
	}
	return manager, nil
}

// SetWake installs the scheduler wake-up hook after both components have been
// constructed.  It is deliberately optional so short-lived CLI control planes
// do not need a scheduler.
func (manager *Manager) SetWake(wake func()) {
	if wake != nil {
		manager.wake.Store(wake)
	}
}

func (manager *Manager) wakeScheduler() {
	if wake, ok := manager.wake.Load().(func()); ok && wake != nil {
		wake()
	}
}

func (manager *Manager) FailClosed() bool {
	return manager.failClosed.Load()
}

// SafetyBlocked exposes the same durable and in-memory fail-closed decision
// used by every core operation.  Configuration publication and background
// schedulers use this gate as well, so a rollback failure cannot leave another
// mutating path active when persisting degraded state also failed.
func (manager *Manager) SafetyBlocked(ctx context.Context) (bool, error) {
	return manager.systemDegraded(ctx)
}

func (manager *Manager) systemDegraded(ctx context.Context) (bool, error) {
	if manager.failClosed.Load() {
		return true, nil
	}
	marker, err := manager.files.FailClosed()
	if err != nil {
		return false, err
	}
	if marker {
		manager.failClosed.Store(true)
		return true, nil
	}
	return manager.repository.CoreSystemDegraded(ctx)
}

func (manager *Manager) rejectPendingMihomoRestart(ctx context.Context) error {
	if manager.endpointGate == nil {
		return nil
	}
	required, err := manager.endpointGate.MihomoRestartRequired(ctx)
	if err != nil {
		return err
	}
	if required {
		return ErrMihomoRestartRequired
	}
	return nil
}

func (manager *Manager) Recover(ctx context.Context) error {
	release, err := manager.coordinator.TryAcquire()
	if err != nil {
		return err
	}
	defer release()
	if err := manager.files.Recover(); err != nil {
		return err
	}
	marker, err := manager.files.FailClosed()
	if err != nil {
		return err
	}
	if marker {
		manager.failClosed.Store(true)
		markContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		markErr := manager.repository.MarkDegraded(
			markContext,
			"managed Mihomo core recovery requires operator intervention",
			"",
			manager.clock().UTC(),
		)
		cancel()
		if markErr != nil {
			return errors.Join(
				errors.New("managed Mihomo core fail-closed state could not be persisted"),
				markErr,
			)
		}
	}
	state, err := manager.repository.CoreState(ctx)
	if err != nil {
		return err
	}
	state.UpdateInProgress = false
	manifest, err := manager.files.Current()
	if errors.Is(err, os.ErrNotExist) {
		// Preserve the last durable manifest when the managed files are absent.
		// Clearing it here would erase useful recovery evidence and make the
		// database claim a clean state while the managed core is missing.
		return manager.repository.SaveCoreState(ctx, state)
	}
	if err != nil {
		return err
	}
	state.Current = &manifest
	return manager.repository.SaveCoreState(ctx, state)
}

func (manager *Manager) Status(ctx context.Context) (Status, error) {
	settings, err := manager.repository.CoreSettings(ctx)
	if err != nil {
		return Status{}, err
	}
	active, processErr := manager.process.IsActive(ctx)
	state, err := manager.repository.CoreState(ctx)
	if err != nil {
		return Status{}, err
	}
	path := settings.ExternalPath
	if settings.Managed {
		path = manager.files.BinaryPath()
	}
	cli, err := manager.newCLI(path)
	if err != nil {
		return Status{}, err
	}
	actual, err := cli.Version(ctx)
	if err != nil {
		return Status{}, err
	}
	controllerVersion := ""
	controllerReachable := false
	if processErr == nil && active {
		version, versionErr := manager.controller.Version(ctx)
		if versionErr == nil {
			controllerVersion = version.Version
			controllerReachable = true
		}
	}
	status := Status{
		Settings:            settings,
		State:               state,
		ActualVersion:       actual,
		ControllerVersion:   controllerVersion,
		ProcessActive:       processErr == nil && active,
		ControllerReachable: controllerReachable,
		Managed:             settings.Managed,
	}
	if settings.Managed {
		manifest, manifestErr := manager.files.Current()
		if manifestErr != nil {
			return Status{}, manifestErr
		}
		status.CurrentBinarySHA256 = manifest.BinarySHA256
		status.RuntimeVersionMatches = status.ProcessActive &&
			status.ControllerReachable &&
			runtimeVersionsMatch(actual, manifest.BinaryReportedVersion) &&
			runtimeVersionsMatch(
				status.ControllerVersion,
				manifest.BinaryReportedVersion,
			)
		if state.Available != nil {
			status.UpdateAvailable =
				state.Available.AssetDigestSHA256 != manifest.CompressedSHA256
		}
	}
	return status, nil
}

func (manager *Manager) Settings(
	ctx context.Context,
) (Settings, error) {
	return manager.repository.CoreSettings(ctx)
}

func (manager *Manager) UpdateSettings(
	ctx context.Context,
	actorAdminID string,
	settings Settings,
) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	current, err := manager.repository.CoreSettings(ctx)
	if err != nil {
		return err
	}
	if !current.Managed {
		return ErrExternal
	}
	degraded, err := manager.systemDegraded(ctx)
	if err != nil {
		return err
	}
	if degraded {
		return ErrDegraded
	}
	// The management API may change channel, auto-update, and interval, but it
	// cannot turn an arbitrary path into an update target.
	settings.Managed = current.Managed
	settings.ExternalPath = current.ExternalPath
	now := manager.clock().UTC()
	state, err := manager.repository.CoreState(ctx)
	if err != nil {
		return err
	}
	if settings.Channel != current.Channel {
		state.Available = nil
	}
	if !settings.AutoUpdate {
		state.NextCheckAt = nil
	} else {
		anchor := now
		if state.LastCheckAt != nil {
			anchor = state.LastCheckAt.UTC()
		}
		next := anchor.Add(settings.CheckInterval)
		if next.Before(now) {
			next = now
		}
		state.NextCheckAt = &next
	}
	if transactional, ok := manager.repository.(settingsStateRepository); ok {
		if err := transactional.UpdateCoreSettingsAndState(ctx, settings, state, now); err != nil {
			return err
		}
	} else {
		if err := manager.repository.UpdateCoreSettings(ctx, settings, now); err != nil {
			return err
		}
		if err := manager.repository.SaveCoreState(ctx, state); err != nil {
			return err
		}
	}
	manager.wakeScheduler()
	return manager.repository.RecordCoreAudit(
		ctx,
		actorAdminID,
		"core.settings.update",
		"success",
		fmt.Sprintf(
			"Updated Mihomo core settings for channel %s.",
			settings.Channel,
		),
		now,
	)
}

func (manager *Manager) Check(
	ctx context.Context,
	actorAdminID string,
) (ReleaseIdentity, error) {
	release, err := manager.coordinator.TryAcquire()
	if err != nil {
		return ReleaseIdentity{}, err
	}
	defer release()
	settings, err := manager.repository.CoreSettings(ctx)
	if err != nil {
		return ReleaseIdentity{}, err
	}
	if !settings.Managed {
		return ReleaseIdentity{}, ErrExternal
	}
	degraded, err := manager.systemDegraded(ctx)
	if err != nil {
		return ReleaseIdentity{}, err
	}
	if degraded {
		return ReleaseIdentity{}, ErrDegraded
	}
	return manager.checkLocked(ctx, actorAdminID, settings)
}

func (manager *Manager) checkLocked(
	ctx context.Context,
	actorAdminID string,
	settings Settings,
) (ReleaseIdentity, error) {
	now := manager.clock().UTC()
	identity, err := manager.upstream.Resolve(
		ctx,
		settings.Channel,
		manager.architecture,
	)
	state, stateErr := manager.repository.CoreState(ctx)
	if stateErr != nil {
		return ReleaseIdentity{}, stateErr
	}
	state.LastCheckAt = &now
	next := now.Add(settings.CheckInterval)
	state.NextCheckAt = &next
	if err != nil {
		state.LastCheckResult = "failed"
		state.LastErrorRedacted = redact.Text(err.Error())
		if saveErr := manager.repository.SaveCoreState(ctx, state); saveErr != nil {
			return ReleaseIdentity{}, errors.Join(err, saveErr)
		}
		_ = manager.repository.RecordCoreAudit(
			context.WithoutCancel(ctx),
			actorAdminID,
			"core.check",
			"failure",
			"Mihomo core update check failed: "+redact.Text(err.Error()),
			now,
		)
		return ReleaseIdentity{}, err
	}
	state.Available = &identity
	state.LastCheckResult = "success"
	state.LastErrorRedacted = ""
	if err := manager.repository.SaveCoreState(ctx, state); err != nil {
		return ReleaseIdentity{}, err
	}
	if err := manager.repository.RecordCoreAudit(
		ctx,
		actorAdminID,
		"core.check",
		"success",
		fmt.Sprintf(
			"Checked Mihomo %s channel; upstream tag %s.",
			settings.Channel,
			identity.TagName,
		),
		now,
	); err != nil {
		return ReleaseIdentity{}, err
	}
	return identity, nil
}

func (manager *Manager) Update(
	ctx context.Context,
	actorAdminID string,
) (Manifest, bool, error) {
	release, err := manager.coordinator.TryAcquire()
	if err != nil {
		return Manifest{}, false, err
	}
	defer release()
	if err := manager.rejectPendingMihomoRestart(ctx); err != nil {
		return Manifest{}, false, err
	}
	settings, err := manager.repository.CoreSettings(ctx)
	if err != nil {
		return Manifest{}, false, err
	}
	if !settings.Managed {
		return Manifest{}, false, ErrExternal
	}
	degraded, err := manager.systemDegraded(ctx)
	if err != nil {
		return Manifest{}, false, err
	}
	if degraded {
		return Manifest{}, false, ErrDegraded
	}
	state, err := manager.repository.CoreState(ctx)
	if err != nil {
		return Manifest{}, false, err
	}
	state.UpdateInProgress = true
	if err := manager.repository.SaveCoreState(ctx, state); err != nil {
		return Manifest{}, false, err
	}
	defer manager.clearUpdateMarker()

	identity, err := manager.checkLocked(ctx, actorAdminID, settings)
	if err != nil {
		manager.recordUpdateFailure(ctx, actorAdminID, err)
		return Manifest{}, false, err
	}
	current, currentErr := manager.files.Current()
	if currentErr == nil &&
		current.CompressedSHA256 == identity.AssetDigestSHA256 {
		now := manager.clock().UTC()
		state, _ = manager.repository.CoreState(ctx)
		state.Current = &current
		state.LastUpdateAt = &now
		state.LastUpdateResult = "no-op"
		state.LastErrorRedacted = ""
		state.UpdateInProgress = false
		if saveErr := manager.repository.SaveCoreState(ctx, state); saveErr != nil {
			return Manifest{}, false, saveErr
		}
		return current, false, nil
	}
	if currentErr != nil && !errors.Is(currentErr, os.ErrNotExist) {
		manager.recordUpdateFailure(ctx, actorAdminID, currentErr)
		return Manifest{}, false, currentErr
	}

	stagingDirectory, archivePath, err := manager.files.CreateDownloadStage()
	if err != nil {
		manager.recordUpdateFailure(ctx, actorAdminID, err)
		return Manifest{}, false, err
	}
	defer manager.files.RemoveStage(stagingDirectory)
	archive, err := os.OpenFile(
		archivePath,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o640,
	)
	if err != nil {
		manager.recordUpdateFailure(ctx, actorAdminID, err)
		return Manifest{}, false, errors.New("create staged Mihomo archive")
	}
	compressedSHA, _, downloadErr := manager.upstream.Download(
		ctx,
		identity,
		archive,
	)
	syncErr := archive.Sync()
	closeErr := archive.Close()
	if downloadErr != nil || syncErr != nil || closeErr != nil {
		err = errors.Join(downloadErr, syncErr, closeErr)
		manager.recordUpdateFailure(ctx, actorAdminID, err)
		return Manifest{}, false, errors.New("download and sync Mihomo core asset")
	}
	manifest, err := manager.files.FinalizeDownloadedStage(
		stagingDirectory,
		identity,
		compressedSHA,
		"pending-validation",
		manager.clock().UTC(),
	)
	if err != nil {
		manager.recordUpdateFailure(ctx, actorAdminID, err)
		return Manifest{}, false, err
	}
	candidate, err := manager.newCLI(filepath.Join(stagingDirectory, "mihomo"))
	if err != nil {
		manager.recordUpdateFailure(ctx, actorAdminID, err)
		return Manifest{}, false, err
	}
	reportedVersion, err := candidate.Version(ctx)
	if err == nil {
		err = candidate.Validate(ctx, manager.configPath)
	}
	if err != nil {
		manager.recordUpdateFailure(ctx, actorAdminID, err)
		return Manifest{}, false, err
	}
	manifest, err = manager.files.SetStagedVersion(
		stagingDirectory,
		manifest,
		reportedVersion,
	)
	if err != nil {
		manager.recordUpdateFailure(ctx, actorAdminID, err)
		return Manifest{}, false, err
	}

	activation, err := manager.files.Activate(stagingDirectory)
	if err != nil {
		if activation.activated {
			err = manager.failAfterActivation(
				ctx,
				activation,
				current,
				currentErr == nil,
				err,
			)
		}
		manager.recordUpdateFailure(ctx, actorAdminID, err)
		return Manifest{}, false, err
	}
	if err := manager.restartAndVerify(ctx, reportedVersion); err != nil {
		fatal := manager.failAfterActivation(
			ctx,
			activation,
			current,
			currentErr == nil,
			err,
		)
		manager.recordUpdateFailure(ctx, actorAdminID, fatal)
		return Manifest{}, false, fatal
	}

	now := manager.clock().UTC()
	state, err = manager.repository.CoreState(ctx)
	if err != nil {
		fatal := manager.failAfterActivation(
			ctx,
			activation,
			current,
			currentErr == nil,
			err,
		)
		manager.recordUpdateFailure(ctx, actorAdminID, fatal)
		return Manifest{}, false, fatal
	}
	state.Current = &manifest
	state.Available = &manifest.Identity
	state.LastUpdateAt = &now
	state.LastUpdateResult = "success"
	state.LastErrorRedacted = ""
	state.UpdateInProgress = false
	if err := manager.repository.SaveCoreState(ctx, state); err != nil {
		fatal := manager.failAfterActivation(
			ctx,
			activation,
			current,
			currentErr == nil,
			err,
		)
		manager.recordUpdateFailure(ctx, actorAdminID, fatal)
		return Manifest{}, false, fatal
	}
	if err := manager.repository.RecordCoreAudit(
		ctx,
		actorAdminID,
		"core.update",
		"success",
		fmt.Sprintf(
			"Updated managed Mihomo core to %s (%s).",
			manifest.Identity.TagName,
			manifest.Identity.AssetDigestSHA256[:12],
		),
		now,
	); err != nil {
		fatal := manager.failAfterActivation(
			ctx,
			activation,
			current,
			currentErr == nil,
			err,
		)
		manager.recordUpdateFailure(ctx, actorAdminID, fatal)
		return Manifest{}, false, fatal
	}
	return manifest, true, nil
}

func (manager *Manager) Rollback(
	ctx context.Context,
	actorAdminID string,
) (result Manifest, resultErr error) {
	defer func() {
		if resultErr != nil {
			manager.recordRollbackFailure(actorAdminID, resultErr)
		}
	}()
	release, err := manager.coordinator.TryAcquire()
	if err != nil {
		return Manifest{}, err
	}
	defer release()
	if err := manager.rejectPendingMihomoRestart(ctx); err != nil {
		return Manifest{}, err
	}
	settings, err := manager.repository.CoreSettings(ctx)
	if err != nil {
		return Manifest{}, err
	}
	if !settings.Managed {
		return Manifest{}, ErrExternal
	}
	degraded, err := manager.systemDegraded(ctx)
	if err != nil {
		return Manifest{}, err
	}
	if degraded {
		return Manifest{}, ErrDegraded
	}
	backupPath, backupManifest, err := manager.files.LatestBackup()
	if err != nil {
		return Manifest{}, ErrNoBackup
	}
	staged, err := manager.files.StageBackup(backupPath)
	if err != nil {
		return Manifest{}, err
	}
	defer manager.files.RemoveStage(staged)
	current, currentErr := manager.files.Current()
	activation, err := manager.files.Activate(staged)
	if err != nil {
		if activation.activated {
			err = manager.failAfterActivation(
				ctx,
				activation,
				current,
				currentErr == nil,
				err,
			)
		}
		return Manifest{}, err
	}
	if err := manager.restartAndVerify(
		ctx,
		backupManifest.BinaryReportedVersion,
	); err != nil {
		fatal := manager.failAfterActivation(
			ctx,
			activation,
			current,
			currentErr == nil,
			err,
		)
		return Manifest{}, fatal
	}
	now := manager.clock().UTC()
	state, err := manager.repository.CoreState(ctx)
	if err != nil {
		return Manifest{}, manager.failAfterActivation(
			ctx,
			activation,
			current,
			currentErr == nil,
			err,
		)
	}
	state.Current = &backupManifest
	state.LastUpdateAt = &now
	state.LastUpdateResult = "rollback-success"
	state.LastErrorRedacted = ""
	if err := manager.repository.SaveCoreState(ctx, state); err != nil {
		return Manifest{}, manager.failAfterActivation(
			ctx,
			activation,
			current,
			currentErr == nil,
			err,
		)
	}
	if err := manager.repository.RecordCoreAudit(
		ctx,
		actorAdminID,
		"core.rollback",
		"success",
		"Rolled back to the previous verified managed Mihomo core.",
		now,
	); err != nil {
		return Manifest{}, manager.failAfterActivation(
			ctx,
			activation,
			current,
			currentErr == nil,
			err,
		)
	}
	return backupManifest, nil
}

func (manager *Manager) Bootstrap(
	ctx context.Context,
	binaryPath, manifestPath string,
) (Manifest, bool, error) {
	release, err := manager.coordinator.TryAcquire()
	if err != nil {
		return Manifest{}, false, err
	}
	defer release()
	staged, manifest, err := manager.files.StageBootstrap(binaryPath, manifestPath)
	if err != nil {
		return Manifest{}, false, err
	}
	defer manager.files.RemoveStage(staged)
	if current, currentErr := manager.files.Current(); currentErr == nil &&
		current.BinarySHA256 == manifest.BinarySHA256 {
		return current, false, nil
	}
	candidate, err := manager.newCLI(filepath.Join(staged, "mihomo"))
	if err != nil {
		return Manifest{}, false, err
	}
	version, err := candidate.Version(ctx)
	if err == nil && version == manifest.BinaryReportedVersion {
		err = candidate.Validate(ctx, manager.configPath)
	}
	if err != nil {
		return Manifest{}, false, errors.New("bootstrap core validation failed")
	}
	if version != manifest.BinaryReportedVersion {
		return Manifest{}, false, errors.New("bootstrap core version does not match manifest")
	}
	activation, err := manager.files.Activate(staged)
	if err != nil {
		if activation.activated {
			_ = manager.files.RevertActivation(activation)
		}
		return Manifest{}, false, err
	}
	state, err := manager.repository.CoreState(ctx)
	if err != nil {
		_ = manager.files.RevertActivation(activation)
		return Manifest{}, false, err
	}
	state.Current = &manifest
	if err := manager.repository.SaveCoreState(ctx, state); err != nil {
		_ = manager.files.RevertActivation(activation)
		return Manifest{}, false, err
	}
	if manager.failClosed.Load() {
		if err := manager.files.ClearFailClosed(); err == nil {
			manager.failClosed.Store(false)
		}
	}
	return manifest, true, nil
}

func (manager *Manager) AdoptExternal(
	ctx context.Context,
	sourcePath string,
) (Manifest, bool, error) {
	release, err := manager.coordinator.TryAcquire()
	if err != nil {
		return Manifest{}, false, err
	}
	defer release()
	if current, currentErr := manager.files.Current(); currentErr == nil {
		return current, false, nil
	}
	cli, err := manager.newCLI(sourcePath)
	if err != nil {
		return Manifest{}, false, err
	}
	version, err := cli.Version(ctx)
	if err != nil {
		return Manifest{}, false, err
	}
	staged, manifest, err := manager.files.StageAdopted(
		sourcePath,
		version,
		manager.clock().UTC(),
	)
	if err != nil {
		return Manifest{}, false, err
	}
	defer manager.files.RemoveStage(staged)
	activation, err := manager.files.Activate(staged)
	if err != nil {
		if activation.activated {
			_ = manager.files.RevertActivation(activation)
		}
		return Manifest{}, false, err
	}
	state, err := manager.repository.CoreState(ctx)
	if err != nil {
		_ = manager.files.RevertActivation(activation)
		return Manifest{}, false, err
	}
	state.Current = &manifest
	if err := manager.repository.SaveCoreState(ctx, state); err != nil {
		_ = manager.files.RevertActivation(activation)
		return Manifest{}, false, err
	}
	if manager.failClosed.Load() {
		if err := manager.files.ClearFailClosed(); err == nil {
			manager.failClosed.Store(false)
		}
	}
	return manifest, true, nil
}

func (manager *Manager) restartAndVerify(
	ctx context.Context,
	expectedVersion string,
) error {
	if err := manager.process.Restart(ctx); err != nil {
		return err
	}
	healthContext, cancel := context.WithTimeout(ctx, manager.healthTimeout)
	defer cancel()
	for {
		if err := manager.healthCheck(healthContext); err == nil {
			cli, cliErr := manager.newCLI(manager.files.BinaryPath())
			if cliErr != nil {
				return cliErr
			}
			actual, versionErr := cli.Version(healthContext)
			if versionErr != nil {
				return versionErr
			}
			controllerVersion, controllerErr := manager.controller.Version(healthContext)
			if controllerErr != nil {
				return controllerErr
			}
			if !runtimeVersionsMatch(actual, expectedVersion) ||
				!runtimeVersionsMatch(controllerVersion.Version, expectedVersion) {
				return errors.New("running Mihomo version does not match activated candidate")
			}
			return nil
		}
		timer := time.NewTimer(manager.healthInterval)
		select {
		case <-healthContext.Done():
			timer.Stop()
			return errors.New("mihomo did not become healthy after core activation")
		case <-timer.C:
		}
	}
}

func (manager *Manager) defaultHealthCheck(ctx context.Context) error {
	active, err := manager.process.IsActive(ctx)
	if err != nil {
		return err
	}
	if !active {
		return errors.New("mihomo process is not active")
	}
	_, err = manager.controller.Version(ctx)
	return err
}

func (manager *Manager) restoreAfterFailure(
	_ context.Context,
	activation Activation,
	previous Manifest,
	hadPrevious bool,
) error {
	if !hadPrevious {
		revertErr := manager.files.RevertActivation(activation)
		stopErr := manager.process.Stop(context.Background())
		return errors.Join(
			errors.New("no prior managed core exists for automatic rollback"),
			revertErr,
			stopErr,
		)
	}
	recoveryContext, cancel := context.WithTimeout(
		context.Background(),
		manager.healthTimeout+15*time.Second,
	)
	defer cancel()
	if err := manager.files.Restore(activation); err != nil {
		return err
	}
	if err := manager.restartAndVerify(
		recoveryContext,
		previous.BinaryReportedVersion,
	); err != nil {
		return err
	}
	state, err := manager.repository.CoreState(recoveryContext)
	if err != nil {
		return err
	}
	state.Current = &previous
	state.UpdateInProgress = false
	return manager.repository.SaveCoreState(recoveryContext, state)
}

func (manager *Manager) failAfterActivation(
	ctx context.Context,
	activation Activation,
	previous Manifest,
	hadPrevious bool,
	cause error,
) error {
	rollbackErr := manager.restoreAfterFailure(
		ctx,
		activation,
		previous,
		hadPrevious,
	)
	if rollbackErr == nil {
		return cause
	}
	manager.failClosed.Store(true)
	markerErr := manager.files.MarkFailClosed()
	markContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	markErr := manager.repository.MarkDegraded(
		markContext,
		"automatic Mihomo core rollback failed; operator intervention is required",
		"",
		manager.clock().UTC(),
	)
	var stopErr error
	if markerErr != nil {
		stopContext, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		stopErr = manager.process.Stop(stopContext)
		stopCancel()
	}
	return errors.Join(cause, rollbackErr, markerErr, markErr, stopErr)
}

func (manager *Manager) clearUpdateMarker() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	state, err := manager.repository.CoreState(ctx)
	if err != nil {
		return
	}
	state.UpdateInProgress = false
	_ = manager.repository.SaveCoreState(ctx, state)
}

func (manager *Manager) recordUpdateFailure(
	ctx context.Context,
	actorAdminID string,
	cause error,
) {
	saveContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := manager.clock().UTC()
	state, err := manager.repository.CoreState(saveContext)
	if err == nil {
		state.LastUpdateAt = &now
		state.LastUpdateResult = "failed"
		state.LastErrorRedacted = redact.Text(cause.Error())
		state.UpdateInProgress = false
		_ = manager.repository.SaveCoreState(saveContext, state)
	}
	_ = manager.repository.RecordCoreAudit(
		saveContext,
		actorAdminID,
		"core.update",
		"failure",
		"Mihomo core update failed: "+redact.Text(cause.Error()),
		now,
	)
}

func (manager *Manager) recordRollbackFailure(
	actorAdminID string,
	cause error,
) {
	saveContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = manager.repository.RecordCoreAudit(
		saveContext,
		actorAdminID,
		"core.rollback",
		"failure",
		"Mihomo core rollback failed: "+redact.Text(cause.Error()),
		manager.clock().UTC(),
	)
}

func (manager *Manager) Due(ctx context.Context, now time.Time) (bool, error) {
	settings, err := manager.repository.CoreSettings(ctx)
	if err != nil {
		return false, err
	}
	if !settings.Managed || !settings.AutoUpdate {
		return false, nil
	}
	degraded, err := manager.systemDegraded(ctx)
	if err != nil || degraded {
		return false, err
	}
	state, err := manager.repository.CoreState(ctx)
	if err != nil {
		return false, err
	}
	return state.NextCheckAt == nil || !state.NextCheckAt.After(now), nil
}

func (manager *Manager) BinaryPath() string {
	return manager.files.BinaryPath()
}
