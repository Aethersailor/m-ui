package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/store"
)

func TestPublisherValidatesPublishesCommitsAndArchives(t *testing.T) {
	t.Parallel()
	fixture := newPublisherFixture(t)
	revision, err := fixture.publisher.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if revision.Status != domain.RevisionActive || revision.ActivatedAt == nil {
		t.Fatalf("revision = %#v", revision)
	}
	config, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "name: next") {
		t.Fatalf("active config was not replaced:\n%s", config)
	}
	if fixture.repository.currentState().Listeners[0].Name != "next" {
		t.Fatal("structured state was not committed")
	}
	for _, path := range []string{revision.FilePath, revision.StateFilePath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("revision artifact %q: %v", path, err)
		}
		if info.Size() == 0 {
			t.Fatalf("revision artifact %q is empty", path)
		}
	}
	if fixture.repository.auditCount() != 1 {
		t.Fatalf("audit count = %d", fixture.repository.auditCount())
	}
}

func TestPublisherCommitsManagedStateAndRevisionInRealSQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	directory := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(directory, "m-ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = database.Close()
	}()
	sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := store.NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	initial := publisherState("old", 443)
	transaction, err := repository.BeginImmediate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.ReplaceDesiredState(ctx, initial); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "mihomo", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		t.Fatal(err)
	}
	initialYAML, err := (YAMLCompiler{}).Compile(ctx, initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, initialYAML, 0o600); err != nil {
		t.Fatal(err)
	}
	instance, err := New(
		repository,
		YAMLCompiler{},
		&fakeCLI{},
		&fakeController{},
		&fakeProcess{active: true},
		Options{
			ConfigPath:        configPath,
			RevisionDirectory: filepath.Join(directory, "revisions"),
			HistoryLimit:      20,
			HealthTimeout:     20 * time.Millisecond,
			HealthInterval:    time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	instance.now = func() time.Time { return initial.AsOf.Add(time.Minute) }
	next := publisherState("next", 8443)
	revision, err := instance.Publish(ctx, Request{
		Reason: "SQLite integration test",
		Mutate: func(ctx context.Context, transaction store.PublicationTransaction) error {
			return transaction.ReplaceDesiredState(ctx, next)
		},
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	loadedRevision, err := repository.Revision(ctx, revision.ID)
	if err != nil || loadedRevision.Status != domain.RevisionActive {
		t.Fatalf("Revision() = %#v, %v", loadedRevision, err)
	}
	readTransaction, err := repository.BeginImmediate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	loadedState, err := readTransaction.DesiredState(ctx, next.AsOf)
	if err != nil {
		t.Fatal(err)
	}
	if err := readTransaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if loadedState.Listeners[0].Name != "next" {
		t.Fatalf("SQLite desired state = %#v", loadedState)
	}
}

func TestPublisherValidationFailureLeavesActiveConfigurationUntouched(t *testing.T) {
	t.Parallel()
	fixture := newPublisherFixture(t)
	fixture.cli.validateErr = errors.New("invalid candidate")
	_, err := fixture.publisher.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if err == nil || !strings.Contains(err.Error(), "candidate validation failed") {
		t.Fatalf("Publish() error = %v", err)
	}
	fixture.assertOldStateAndConfig(t)
	if fixture.controller.reloadCount() != 0 {
		t.Fatal("Controller was called for an invalid candidate")
	}
	if fixture.repository.failedCount() != 1 {
		t.Fatalf("failed revision count = %d", fixture.repository.failedCount())
	}
}

func TestPublisherReloadFailureRestoresPreviousConfiguration(t *testing.T) {
	t.Parallel()
	fixture := newPublisherFixture(t)
	fixture.controller.reloadErrors = []error{
		errors.New("reload new configuration"),
		nil,
	}
	fixture.process.restartErrors = []error{errors.New("restart new configuration")}
	_, err := fixture.publisher.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if err == nil || !strings.Contains(err.Error(), "runtime reload failed") {
		t.Fatalf("Publish() error = %v", err)
	}
	fixture.assertOldStateAndConfig(t)
	systemState, _ := fixture.repository.SystemState(context.Background())
	if systemState.Degraded {
		t.Fatal("successful automatic recovery incorrectly marked degraded")
	}
}

func TestPublisherAcceptsFixedSystemdRestartAfterControllerReloadFailure(t *testing.T) {
	t.Parallel()
	fixture := newPublisherFixture(t)
	fixture.controller.reloadErrors = []error{errors.New("reload failed")}
	revision, err := fixture.publisher.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if revision.Status != domain.RevisionActive ||
		fixture.process.restartCount() != 1 {
		t.Fatalf("revision = %#v, restart count = %d", revision, fixture.process.restartCount())
	}
}

func TestPublisherHealthFailureRestoresPreviousConfiguration(t *testing.T) {
	t.Parallel()
	fixture := newPublisherFixture(t)
	fixture.controller.versionByReload = true
	_, err := fixture.publisher.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if err == nil || !strings.Contains(err.Error(), "health check failed") {
		t.Fatalf("Publish() error = %v", err)
	}
	fixture.assertOldStateAndConfig(t)
	if fixture.controller.reloadCount() < 2 {
		t.Fatal("previous configuration was not reloaded")
	}
}

func TestPublisherCommitFailureRestoresPreviousConfiguration(t *testing.T) {
	t.Parallel()
	fixture := newPublisherFixture(t)
	fixture.repository.commitErr = errors.New("simulated commit failure")
	_, err := fixture.publisher.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if err == nil || !strings.Contains(err.Error(), "database commit failed") {
		t.Fatalf("Publish() error = %v", err)
	}
	fixture.assertOldStateAndConfig(t)
	if fixture.controller.reloadCount() < 2 {
		t.Fatal("previous configuration was not reloaded after commit failure")
	}
}

func TestPublisherRecoveryFailureMarksDegradedAndBlocksWrites(t *testing.T) {
	t.Parallel()
	fixture := newPublisherFixture(t)
	fixture.controller.reloadErrors = []error{
		errors.New("reload new configuration"),
		errors.New("reload restored configuration"),
	}
	fixture.process.restartErrors = []error{
		errors.New("restart new configuration"),
		errors.New("restart restored configuration"),
	}
	_, err := fixture.publisher.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if err == nil || !strings.Contains(err.Error(), "automatic recovery failed") {
		t.Fatalf("Publish() error = %v", err)
	}
	systemState, stateErr := fixture.repository.SystemState(context.Background())
	if stateErr != nil || !systemState.Degraded || systemState.DegradedRevisionID == "" {
		t.Fatalf("SystemState() = %#v, %v", systemState, stateErr)
	}
	_, err = fixture.publisher.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if !errors.Is(err, ErrDegraded) {
		t.Fatalf("second Publish() error = %v, want ErrDegraded", err)
	}
}

func TestPublisherSerializesConcurrentPublications(t *testing.T) {
	t.Parallel()
	fixture := newPublisherFixture(t)
	fixture.cli.delay = 15 * time.Millisecond
	var wait sync.WaitGroup
	errorsFound := make(chan error, 2)
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := fixture.publisher.Publish(
				context.Background(),
				fixture.request(fixture.nextState),
			)
			errorsFound <- err
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatalf("concurrent Publish() error = %v", err)
		}
	}
	if fixture.cli.maxConcurrent.Load() != 1 {
		t.Fatalf("maximum concurrent validations = %d", fixture.cli.maxConcurrent.Load())
	}
}

func TestPublisherRollbackRestoresStructuredSnapshotAndCreatesAudit(t *testing.T) {
	t.Parallel()
	fixture := newPublisherFixture(t)
	first, err := fixture.publisher.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if err != nil {
		t.Fatal(err)
	}
	thirdState := cloneState(fixture.nextState)
	thirdState.Listeners[0].Name = "third"
	thirdState.Listeners[0].ListenPort = 10443
	if _, err := fixture.publisher.Publish(
		context.Background(),
		fixture.request(thirdState),
	); err != nil {
		t.Fatal(err)
	}

	rolledBack, err := fixture.publisher.Rollback(
		context.Background(),
		first.ID,
		"administrator-id",
	)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if rolledBack.RevisionNumber != 3 ||
		fixture.repository.currentState().Listeners[0].Name != "next" {
		t.Fatalf(
			"rollback revision/state = %#v/%#v",
			rolledBack,
			fixture.repository.currentState(),
		)
	}
	audits := fixture.repository.currentAudits()
	if len(audits) != 3 || audits[2].Action != "config.rollback" ||
		audits[2].ResourceID != first.ID {
		t.Fatalf("rollback audit entries = %#v", audits)
	}
}

func TestPublisherRollbackRejectsTamperedRevisionArtifacts(t *testing.T) {
	t.Parallel()
	fixture := newPublisherFixture(t)
	revision, err := fixture.publisher.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(revision.FilePath, []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.publisher.Rollback(
		context.Background(),
		revision.ID,
		"administrator-id",
	)
	if err == nil || !strings.Contains(err.Error(), "integrity check") {
		t.Fatalf("Rollback() error = %v", err)
	}
}

type publisherFixture struct {
	publisher  *Publisher
	repository *fakePublicationRepository
	cli        *fakeCLI
	controller *fakeController
	process    *fakeProcess
	configPath string
	oldState   domain.DesiredState
	nextState  domain.DesiredState
}

func newPublisherFixture(t *testing.T) *publisherFixture {
	t.Helper()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "mihomo", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("old-config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldState := publisherState("old", 443)
	nextState := publisherState("next", 8443)
	repository := newFakePublicationRepository(oldState)
	cli := &fakeCLI{}
	controller := &fakeController{}
	process := &fakeProcess{active: true}
	instance, err := New(
		repository,
		YAMLCompiler{},
		cli,
		controller,
		process,
		Options{
			ConfigPath:        configPath,
			RevisionDirectory: filepath.Join(directory, "revisions"),
			HistoryLimit:      20,
			HealthTimeout:     15 * time.Millisecond,
			HealthInterval:    time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	instance.now = func() time.Time {
		return time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	}
	return &publisherFixture{
		publisher:  instance,
		repository: repository,
		cli:        cli,
		controller: controller,
		process:    process,
		configPath: configPath,
		oldState:   oldState,
		nextState:  nextState,
	}
}

func (fixture *publisherFixture) request(state domain.DesiredState) Request {
	return Request{
		Reason:          "test publication",
		ActorAdminID:    "administrator-id",
		AuditAction:     "config.publish",
		AuditResource:   "managed_state",
		AuditResourceID: state.Listeners[0].ID,
		AuditSummary:    "published managed state",
		Mutate: func(ctx context.Context, transaction store.PublicationTransaction) error {
			return transaction.ReplaceDesiredState(ctx, state)
		},
	}
}

func (fixture *publisherFixture) assertOldStateAndConfig(t *testing.T) {
	t.Helper()
	config, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(config) != "old-config\n" {
		t.Fatalf("active configuration = %q, want original bytes", config)
	}
	if fixture.repository.currentState().Listeners[0].Name !=
		fixture.oldState.Listeners[0].Name {
		t.Fatalf("structured state changed: %#v", fixture.repository.currentState())
	}
}

func publisherState(name string, port uint16) domain.DesiredState {
	listenerID := "773a43f6-ab75-4836-9b83-cf18a1559c97"
	return domain.DesiredState{
		AsOf:              time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
		ControllerAddress: "127.0.0.1:9090",
		ControllerSecret:  "publisher-controller-secret",
		PublicHost:        "node.example.com",
		Listeners: []domain.Listener{{
			ID:                listenerID,
			Name:              name,
			Enabled:           true,
			ListenAddress:     "0.0.0.0",
			ListenPort:        port,
			ServerName:        "www.example.com",
			RealityDest:       "www.example.com:443",
			RealityPrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			RealityPublicKey:  "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE",
			ShortID:           "0123456789abcdef",
			UDPEnabled:        true,
			Users: []domain.User{{
				ID:         "beb8ec46-f6d8-4c49-9bd4-b8628599c64f",
				ListenerID: listenerID,
				Name:       "user",
				Enabled:    true,
				UUID:       "2b26a842-8bd1-493a-978b-ee5e546cf508",
			}},
		}},
	}
}

type fakePublicationRepository struct {
	mutex       sync.Mutex
	state       domain.DesiredState
	systemState domain.SystemState
	revisions   map[string]domain.Revision
	audits      []store.AuditEntry
	commitErr   error
}

func newFakePublicationRepository(state domain.DesiredState) *fakePublicationRepository {
	return &fakePublicationRepository{
		state:     cloneState(state),
		revisions: make(map[string]domain.Revision),
	}
}

func (repository *fakePublicationRepository) BeginImmediate(
	context.Context,
) (store.PublicationTransaction, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return &fakePublicationTransaction{
		repository: repository,
		state:      cloneState(repository.state),
		revisions:  cloneRevisions(repository.revisions),
		audits:     append([]store.AuditEntry(nil), repository.audits...),
	}, nil
}

func (repository *fakePublicationRepository) SystemState(
	context.Context,
) (domain.SystemState, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return repository.systemState, nil
}

func (repository *fakePublicationRepository) MarkDegraded(
	_ context.Context,
	reason, revisionID string,
	now time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.systemState = domain.SystemState{
		Degraded:           true,
		DegradedReason:     reason,
		DegradedRevisionID: revisionID,
		UpdatedAt:          now,
	}
	return nil
}

func (repository *fakePublicationRepository) ClearDegraded(
	_ context.Context,
	now time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.systemState = domain.SystemState{UpdatedAt: now}
	return nil
}

func (repository *fakePublicationRepository) Revision(
	_ context.Context,
	id string,
) (domain.Revision, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	revision, exists := repository.revisions[id]
	if !exists {
		return domain.Revision{}, store.ErrNotFound
	}
	return revision, nil
}

func (repository *fakePublicationRepository) RecordFailedRevision(
	_ context.Context,
	revision domain.Revision,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	revision.Status = domain.RevisionFailed
	repository.revisions[revision.ID] = revision
	return nil
}

func (repository *fakePublicationRepository) InactiveRevisionsBeyond(
	context.Context,
	int,
) ([]domain.Revision, error) {
	return nil, nil
}

func (repository *fakePublicationRepository) DeleteRevision(
	_ context.Context,
	id string,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	delete(repository.revisions, id)
	return nil
}

func (repository *fakePublicationRepository) currentState() domain.DesiredState {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return cloneState(repository.state)
}

func (repository *fakePublicationRepository) failedCount() int {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	count := 0
	for _, revision := range repository.revisions {
		if revision.Status == domain.RevisionFailed {
			count++
		}
	}
	return count
}

func (repository *fakePublicationRepository) auditCount() int {
	return len(repository.currentAudits())
}

func (repository *fakePublicationRepository) currentAudits() []store.AuditEntry {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return append([]store.AuditEntry(nil), repository.audits...)
}

type fakePublicationTransaction struct {
	repository *fakePublicationRepository
	state      domain.DesiredState
	revisions  map[string]domain.Revision
	audits     []store.AuditEntry
	done       bool
}

func (transaction *fakePublicationTransaction) DesiredState(
	_ context.Context,
	asOf time.Time,
) (domain.DesiredState, error) {
	state := cloneState(transaction.state)
	state.AsOf = asOf
	return state, nil
}

func (transaction *fakePublicationTransaction) ReplaceDesiredState(
	_ context.Context,
	state domain.DesiredState,
) error {
	if err := state.Validate(); err != nil {
		return err
	}
	transaction.state = cloneState(state)
	return nil
}

func (transaction *fakePublicationTransaction) NextRevisionNumber(
	context.Context,
) (int64, error) {
	var maximum int64
	for _, revision := range transaction.revisions {
		if revision.RevisionNumber > maximum {
			maximum = revision.RevisionNumber
		}
	}
	return maximum + 1, nil
}

func (transaction *fakePublicationTransaction) InsertRevision(
	_ context.Context,
	revision domain.Revision,
) error {
	transaction.revisions[revision.ID] = revision
	return nil
}

func (transaction *fakePublicationTransaction) ActivateRevision(
	_ context.Context,
	revisionID string,
	activatedAt time.Time,
) error {
	for id, revision := range transaction.revisions {
		if revision.Status == domain.RevisionActive {
			revision.Status = domain.RevisionRolledBack
			transaction.revisions[id] = revision
		}
	}
	revision, exists := transaction.revisions[revisionID]
	if !exists || revision.Status != domain.RevisionPending {
		return errors.New("pending revision not found")
	}
	revision.Status = domain.RevisionActive
	revision.ActivatedAt = &activatedAt
	transaction.revisions[revisionID] = revision
	return nil
}

func (transaction *fakePublicationTransaction) InsertAudit(
	_ context.Context,
	entry store.AuditEntry,
) error {
	transaction.audits = append(transaction.audits, entry)
	return nil
}

func (transaction *fakePublicationTransaction) Commit(context.Context) error {
	if transaction.repository.commitErr != nil {
		transaction.done = true
		return transaction.repository.commitErr
	}
	transaction.repository.mutex.Lock()
	defer transaction.repository.mutex.Unlock()
	transaction.repository.state = cloneState(transaction.state)
	transaction.repository.revisions = cloneRevisions(transaction.revisions)
	transaction.repository.audits = append([]store.AuditEntry(nil), transaction.audits...)
	transaction.done = true
	return nil
}

func (transaction *fakePublicationTransaction) Rollback(context.Context) error {
	transaction.done = true
	return nil
}

type fakeCLI struct {
	validateErr   error
	delay         time.Duration
	concurrent    atomic.Int32
	maxConcurrent atomic.Int32
}

func (cli *fakeCLI) Validate(context.Context, string) error {
	current := cli.concurrent.Add(1)
	defer cli.concurrent.Add(-1)
	for {
		maximum := cli.maxConcurrent.Load()
		if current <= maximum || cli.maxConcurrent.CompareAndSwap(maximum, current) {
			break
		}
	}
	if cli.delay != 0 {
		time.Sleep(cli.delay)
	}
	return cli.validateErr
}

func (*fakeCLI) Version(context.Context) (string, error) {
	return "test", nil
}

func (*fakeCLI) GenerateRealityKeypair(context.Context) (domain.Keypair, error) {
	return domain.Keypair{}, nil
}

type fakeController struct {
	mutex           sync.Mutex
	reloads         int
	reloadErrors    []error
	versionByReload bool
}

func (controller *fakeController) Reload(context.Context, string) error {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	controller.reloads++
	if len(controller.reloadErrors) == 0 {
		return nil
	}
	err := controller.reloadErrors[0]
	controller.reloadErrors = controller.reloadErrors[1:]
	return err
}

func (controller *fakeController) Version(context.Context) (mihomo.Version, error) {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	if controller.versionByReload && controller.reloads < 2 {
		return mihomo.Version{}, errors.New("unhealthy")
	}
	return mihomo.Version{Version: "test"}, nil
}

func (controller *fakeController) reloadCount() int {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	return controller.reloads
}

func (*fakeController) Traffic(context.Context) (mihomo.TrafficSnapshot, error) {
	return mihomo.TrafficSnapshot{}, nil
}

func (*fakeController) Memory(context.Context) (mihomo.MemorySnapshot, error) {
	return mihomo.MemorySnapshot{}, nil
}

func (*fakeController) Connections(context.Context) (mihomo.ConnectionsSnapshot, error) {
	return mihomo.ConnectionsSnapshot{}, nil
}

func (*fakeController) Restart(context.Context, string) error {
	return nil
}

type fakeProcess struct {
	mutex         sync.Mutex
	active        bool
	restarts      int
	restartErrors []error
	stopErr       error
}

func (process *fakeProcess) IsActive(context.Context) (bool, error) {
	process.mutex.Lock()
	defer process.mutex.Unlock()
	return process.active, nil
}

func (*fakeProcess) Start(context.Context) error {
	return nil
}

func (process *fakeProcess) Stop(context.Context) error {
	process.mutex.Lock()
	defer process.mutex.Unlock()
	if process.stopErr == nil {
		process.active = false
	}
	return process.stopErr
}

func (process *fakeProcess) Restart(context.Context) error {
	process.mutex.Lock()
	defer process.mutex.Unlock()
	process.restarts++
	if len(process.restartErrors) == 0 {
		process.active = true
		return nil
	}
	err := process.restartErrors[0]
	process.restartErrors = process.restartErrors[1:]
	return err
}

func (*fakeProcess) Reload(context.Context) error {
	return nil
}

func (*fakeProcess) RecentLogs(context.Context, int) ([]mihomo.LogEntry, error) {
	return nil, nil
}

func (process *fakeProcess) restartCount() int {
	process.mutex.Lock()
	defer process.mutex.Unlock()
	return process.restarts
}

func cloneState(state domain.DesiredState) domain.DesiredState {
	encoded, _ := json.Marshal(state)
	var cloned domain.DesiredState
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func cloneRevisions(
	revisions map[string]domain.Revision,
) map[string]domain.Revision {
	cloned := make(map[string]domain.Revision, len(revisions))
	for id, revision := range revisions {
		cloned[id] = revision
	}
	return cloned
}
