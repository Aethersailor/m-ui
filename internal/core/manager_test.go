package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/operation"
)

func TestManagerUpdatesNoOpsAndReadsActualVersion(t *testing.T) {
	t.Parallel()
	fixture := newManagerFixture(t)
	old := fixture.adopt(t, "old-runtime-version")
	manifest, changed, err := fixture.manager.Update(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !changed || manifest.BinaryReportedVersion != "new-runtime-version" {
		t.Fatalf("Update() = %#v, %v", manifest, changed)
	}
	if manifest.BinarySHA256 == old.BinarySHA256 {
		t.Fatal("managed core binary did not change")
	}
	status, err := fixture.manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.ActualVersion != "new-runtime-version" ||
		!status.RuntimeVersionMatches ||
		status.UpdateAvailable {
		t.Fatalf("Status() = %#v", status)
	}
	second, changed, err := fixture.manager.Update(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if changed || second.BinarySHA256 != manifest.BinarySHA256 {
		t.Fatalf("no-op Update() = %#v, %v", second, changed)
	}
}

func TestManagerRejectsDamagedCandidateBeforeActivation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		downloadErr error
		content     string
	}{
		{name: "download digest failure", downloadErr: errors.New("digest mismatch")},
		{name: "candidate version failure", content: "version-error"},
		{name: "candidate config failure", content: "config-error"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newManagerFixture(t)
			old := fixture.adopt(t, "old-runtime-version")
			if test.content != "" {
				fixture.setUpstream(t, test.content)
			}
			fixture.upstream.downloadErr = test.downloadErr
			if _, _, err := fixture.manager.Update(
				context.Background(),
				"admin",
			); err == nil {
				t.Fatal("Update() error = nil")
			}
			current, err := fixture.files.Current()
			if err != nil {
				t.Fatal(err)
			}
			if current.BinarySHA256 != old.BinarySHA256 {
				t.Fatal("pre-activation failure changed current core")
			}
			if fixture.repository.degraded {
				t.Fatal("pre-activation failure marked system degraded")
			}
		})
	}
}

func TestManagerRestartFailureRollsBackWithoutDegraded(t *testing.T) {
	t.Parallel()
	fixture := newManagerFixture(t)
	old := fixture.adopt(t, "old-runtime-version")
	fixture.process.restartFailures = []bool{true, false}
	if _, _, err := fixture.manager.Update(
		context.Background(),
		"admin",
	); err == nil {
		t.Fatal("Update() error = nil")
	}
	current, err := fixture.files.Current()
	if err != nil {
		t.Fatal(err)
	}
	if current.BinarySHA256 != old.BinarySHA256 {
		t.Fatalf("rollback current = %#v, want %#v", current, old)
	}
	if fixture.repository.degraded {
		t.Fatal("successful automatic rollback marked degraded")
	}
}

func TestManagerStateCommitFailureRollsBackWithoutDegraded(t *testing.T) {
	t.Parallel()
	fixture := newManagerFixture(t)
	old := fixture.adopt(t, "old-runtime-version")
	fixture.repository.mutex.Lock()
	fixture.repository.saveCalls = 0
	fixture.repository.failSaveAt = 3
	fixture.repository.mutex.Unlock()
	if _, _, err := fixture.manager.Update(
		context.Background(),
		"admin",
	); err == nil {
		t.Fatal("Update() error = nil")
	}
	current, err := fixture.files.Current()
	if err != nil {
		t.Fatal(err)
	}
	if current.BinarySHA256 != old.BinarySHA256 {
		t.Fatalf("state failure current = %#v, want %#v", current, old)
	}
	if fixture.repository.degraded {
		t.Fatal("successful state-failure rollback marked degraded")
	}
	if fixture.process.restarts != 2 {
		t.Fatalf("restart count = %d, want 2", fixture.process.restarts)
	}
}

func TestManagerRollbackFailureMarksDegraded(t *testing.T) {
	t.Parallel()
	fixture := newManagerFixture(t)
	fixture.adopt(t, "old-runtime-version")
	fixture.process.restartFailures = []bool{true, true}
	_, _, err := fixture.manager.Update(context.Background(), "admin")
	if err == nil {
		t.Fatal("Update() error = nil")
	}
	if !fixture.repository.degraded {
		t.Fatal("failed automatic rollback did not mark degraded")
	}
	if _, _, err := fixture.manager.Update(
		context.Background(),
		"admin",
	); !errors.Is(err, ErrDegraded) {
		t.Fatalf("Update() after degraded = %v", err)
	}
}

func TestManagerExternalCoreAndCoordinatorConflict(t *testing.T) {
	t.Parallel()
	fixture := newManagerFixture(t)
	fixture.repository.settings.Managed = false
	fixture.repository.settings.ExternalPath = filepath.Join(
		fixture.root,
		"external",
	)
	if _, _, err := fixture.manager.Update(
		context.Background(),
		"admin",
	); !errors.Is(err, ErrExternal) {
		t.Fatalf("external Update() error = %v", err)
	}
	if _, err := fixture.manager.Check(
		context.Background(),
		"admin",
	); !errors.Is(err, ErrExternal) {
		t.Fatalf("external Check() error = %v", err)
	}
	fixture.repository.settings.Managed = true
	fixture.repository.settings.ExternalPath = ""
	fixture.repository.degraded = true
	if _, err := fixture.manager.Check(
		context.Background(),
		"admin",
	); !errors.Is(err, ErrDegraded) {
		t.Fatalf("degraded Check() error = %v", err)
	}
	fixture.repository.degraded = false
	release, err := fixture.coordinator.TryAcquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := fixture.manager.Check(
		context.Background(),
		"admin",
	); !errors.Is(err, operation.ErrBusy) {
		t.Fatalf("concurrent Check() error = %v", err)
	}
}

func TestFileStoreRecoversStagingAndRetainsTwoBackups(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := files.Prepare(); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(root, "staging", "interrupted")
	if err := os.Mkdir(orphan, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "partial"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := files.Recover(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted staging remains: %v", err)
	}
	for index, version := range []string{"one", "two", "three", "four"} {
		source := filepath.Join(root, "source-"+version)
		if err := os.WriteFile(source, []byte(version), 0o750); err != nil {
			t.Fatal(err)
		}
		stage, _, err := files.StageAdopted(
			source,
			version,
			time.Unix(int64(index+1), 0),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := files.Activate(stage); err != nil {
			t.Fatal(err)
		}
	}
	backups, err := os.ReadDir(filepath.Join(root, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 2 {
		t.Fatalf("backup count = %d, want 2", len(backups))
	}
	if err := os.WriteFile(
		filepath.Join(root, "current", "mihomo"),
		[]byte("tampered"),
		0o750,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Current(); err == nil {
		t.Fatal("manifest/binary mismatch was accepted")
	}
}

func TestFileStorePostRenameDurabilityFailureIsRevertible(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := files.Prepare(); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("post-rename"), 0o750); err != nil {
		t.Fatal(err)
	}
	stage, _, err := files.StageAdopted(source, "post-rename", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	files.syncDirectoryHook = func(string) error {
		return errors.New("synthetic root fsync failure")
	}
	activation, err := files.Activate(stage)
	if err == nil {
		t.Fatal("Activate() error = nil")
	}
	if !activation.activated {
		t.Fatal("post-rename failure did not return an activated transaction")
	}
	if err := files.RevertActivation(activation); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Current(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Current() after revert = %v, want not-exist", err)
	}
}

func TestManagerFailClosedLatchSurvivesDatabaseMarkFailure(t *testing.T) {
	t.Parallel()
	fixture := newManagerFixture(t)
	fixture.repository.failMarkDegraded = true
	if err := fixture.manager.failAfterActivation(
		context.Background(),
		Activation{},
		Manifest{},
		false,
		errors.New("synthetic activation failure"),
	); err == nil {
		t.Fatal("failAfterActivation() error = nil")
	}
	if !fixture.manager.FailClosed() {
		t.Fatal("fail-closed latch was not set")
	}
	marked, err := fixture.files.FailClosed()
	if err != nil {
		t.Fatal(err)
	}
	if !marked {
		t.Fatal("durable fail-closed marker was not written")
	}
	degraded, err := fixture.manager.systemDegraded(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !degraded {
		t.Fatal("systemDegraded() ignored fail-closed latch")
	}
	blocked, err := fixture.manager.SafetyBlocked(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !blocked {
		t.Fatal("SafetyBlocked() ignored fail-closed latch")
	}
}

func TestManagerSettingsUpdateSchedulesAndWakesCoreChecks(t *testing.T) {
	t.Parallel()
	fixture := newManagerFixture(t)
	lastCheck := time.Unix(1000, 0).UTC()
	fixture.repository.state.LastCheckAt = &lastCheck
	var woke bool
	fixture.manager.SetWake(func() { woke = true })
	settings := fixture.repository.settings
	settings.AutoUpdate = true
	settings.CheckInterval = 6 * time.Hour
	if err := fixture.manager.UpdateSettings(context.Background(), "admin", settings); err != nil {
		t.Fatal(err)
	}
	state := fixture.repository.state
	want := lastCheck.Add(6 * time.Hour)
	if state.NextCheckAt == nil || !state.NextCheckAt.Equal(want) {
		t.Fatalf("next check = %v, want %v", state.NextCheckAt, want)
	}
	if !woke {
		t.Fatal("settings update did not wake the scheduler")
	}
}

type managerFixture struct {
	root        string
	files       *FileStore
	repository  *fakeCoreRepository
	upstream    *fakeUpstream
	process     *fakeCoreProcess
	controller  fakeCoreController
	coordinator *operation.Coordinator
	manager     *Manager
}

func newManagerFixture(t *testing.T) *managerFixture {
	t.Helper()
	root := t.TempDir()
	files, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeCoreRepository{
		settings: Settings{
			Channel:       ChannelRelease,
			CheckInterval: DefaultCheckInterval,
			Managed:       true,
		},
	}
	process := &fakeCoreProcess{}
	coordinator := operation.NewCoordinator()
	fixture := &managerFixture{
		root:       root,
		files:      files,
		repository: repository,
		process:    process,
		controller: fakeCoreController{
			path: filepath.Join(root, "current", "mihomo"),
		},
		coordinator: coordinator,
	}
	fixture.setUpstream(t, "new-runtime-version")
	manager, err := NewManager(ManagerOptions{
		Repository:  repository,
		Upstream:    fixture.upstream,
		Files:       files,
		Process:     process,
		Controller:  fixture.controller,
		Coordinator: coordinator,
		ConfigPath:  filepath.Join(root, "config.yaml"),
		NewCLI:      fakeCLIFactory,
		HealthCheck: func(context.Context) error {
			return nil
		},
		Clock: func() time.Time {
			return time.Unix(1000, 0).UTC()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.manager = manager
	if err := os.WriteFile(
		filepath.Join(root, "config.yaml"),
		[]byte("synthetic"),
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture *managerFixture) setUpstream(t *testing.T, content string) {
	t.Helper()
	payload := gzipPayload(t, []byte(content))
	fixture.upstream = &fakeUpstream{
		identity: ReleaseIdentity{
			Channel:           ChannelRelease,
			Repository:        UpstreamRepository,
			ReleaseID:         99,
			TagName:           "v9.9.9",
			PublishedAt:       time.Unix(900, 0).UTC(),
			AssetID:           100,
			AssetName:         "mihomo-linux-amd64-compatible-v9.9.9.gz",
			AssetSize:         int64(len(payload)),
			AssetDigestSHA256: sha256Hex(payload),
		},
		payload: payload,
	}
	if fixture.manager != nil {
		fixture.manager.upstream = fixture.upstream
	}
}

func (fixture *managerFixture) adopt(t *testing.T, version string) Manifest {
	t.Helper()
	source := filepath.Join(fixture.root, "legacy-mihomo")
	if err := os.WriteFile(source, []byte(version), 0o750); err != nil {
		t.Fatal(err)
	}
	manifest, changed, err := fixture.manager.AdoptExternal(
		context.Background(),
		source,
	)
	if err != nil || !changed {
		t.Fatalf("AdoptExternal() = %#v, %v, %v", manifest, changed, err)
	}
	return manifest
}

type fakeCoreRepository struct {
	mutex            sync.Mutex
	settings         Settings
	state            State
	degraded         bool
	audit            []string
	saveCalls        int
	failSaveAt       int
	failMarkDegraded bool
}

func (repository *fakeCoreRepository) CoreSettings(context.Context) (Settings, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return repository.settings, nil
}

func (repository *fakeCoreRepository) UpdateCoreSettings(
	_ context.Context,
	settings Settings,
	_ time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.settings = settings
	return nil
}

func (repository *fakeCoreRepository) CoreState(context.Context) (State, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return repository.state, nil
}

func (repository *fakeCoreRepository) SaveCoreState(
	_ context.Context,
	state State,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.saveCalls++
	if repository.failSaveAt > 0 &&
		repository.saveCalls == repository.failSaveAt {
		return errors.New("synthetic state commit failure")
	}
	repository.state = state
	return nil
}

func (repository *fakeCoreRepository) CoreSystemDegraded(
	context.Context,
) (bool, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return repository.degraded, nil
}

func (repository *fakeCoreRepository) MarkDegraded(
	_ context.Context,
	_, _ string,
	_ time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.failMarkDegraded {
		return errors.New("synthetic degraded-state write failure")
	}
	repository.degraded = true
	return nil
}

func (repository *fakeCoreRepository) RecordCoreAudit(
	_ context.Context,
	_, action, _, summary string,
	_ time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.audit = append(
		repository.audit,
		action+":"+strings.ReplaceAll(summary, "secret=value", "[redacted]"),
	)
	return nil
}

type fakeUpstream struct {
	identity    ReleaseIdentity
	payload     []byte
	downloadErr error
}

func (upstream *fakeUpstream) Resolve(
	context.Context,
	Channel,
	string,
) (ReleaseIdentity, error) {
	return upstream.identity, nil
}

func (upstream *fakeUpstream) Download(
	_ context.Context,
	_ ReleaseIdentity,
	destination io.Writer,
) (string, int64, error) {
	if upstream.downloadErr != nil {
		return "", 0, upstream.downloadErr
	}
	written, err := io.Copy(destination, bytes.NewReader(upstream.payload))
	return sha256Hex(upstream.payload), written, err
}

type fakeCLI struct {
	version string
}

func fakeCLIFactory(path string) (mihomo.CoreCLI, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return fakeCLI{version: string(content)}, nil
}

func (cli fakeCLI) Validate(context.Context, string) error {
	if cli.version == "config-error" {
		return errors.New("candidate config rejected")
	}
	return nil
}

func (cli fakeCLI) Version(context.Context) (string, error) {
	if cli.version == "version-error" {
		return "", errors.New("candidate version rejected")
	}
	return cli.version, nil
}

func (fakeCLI) GenerateRealityKeypair(context.Context) (domain.Keypair, error) {
	return domain.Keypair{}, nil
}

type fakeCoreProcess struct {
	mutex           sync.Mutex
	restartFailures []bool
	restarts        int
}

func (*fakeCoreProcess) IsActive(context.Context) (bool, error) { return true, nil }
func (*fakeCoreProcess) Start(context.Context) error            { return nil }
func (*fakeCoreProcess) Stop(context.Context) error             { return nil }
func (process *fakeCoreProcess) Restart(context.Context) error {
	process.mutex.Lock()
	defer process.mutex.Unlock()
	index := process.restarts
	process.restarts++
	if index < len(process.restartFailures) && process.restartFailures[index] {
		return errors.New("synthetic restart failure")
	}
	return nil
}
func (*fakeCoreProcess) Reload(context.Context) error { return nil }
func (*fakeCoreProcess) RecentLogs(context.Context, int) ([]mihomo.LogEntry, error) {
	return nil, nil
}

type fakeCoreController struct {
	path string
}

func (controller fakeCoreController) Version(context.Context) (mihomo.Version, error) {
	if controller.path != "" {
		content, err := os.ReadFile(controller.path)
		if err != nil {
			return mihomo.Version{}, err
		}
		return mihomo.Version{Version: strings.TrimSpace(string(content))}, nil
	}
	return mihomo.Version{Version: "controller-runtime-version"}, nil
}
func (fakeCoreController) Traffic(context.Context) (mihomo.TrafficSnapshot, error) {
	return mihomo.TrafficSnapshot{}, nil
}
func (fakeCoreController) Memory(context.Context) (mihomo.MemorySnapshot, error) {
	return mihomo.MemorySnapshot{}, nil
}
func (fakeCoreController) Connections(context.Context) (mihomo.ConnectionsSnapshot, error) {
	return mihomo.ConnectionsSnapshot{}, nil
}
func (fakeCoreController) Reload(context.Context, string) error  { return nil }
func (fakeCoreController) Restart(context.Context, string) error { return nil }
