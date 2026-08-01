package publisher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/store"
)

type commitFaultMode int

const (
	commitFailsBeforeDurableWrite commitFaultMode = iota + 1
	commitSucceedsThenReturnsError
)

func TestCommitFailureReconcilesDBOldAndYAMLNew(t *testing.T) {
	t.Parallel()
	fixture := newSQLiteReconciliationFixture(t)
	repository := &commitFaultRepository{
		PublicationRepository: fixture.repository,
		mode:                  commitFailsBeforeDurableWrite,
	}
	instance := fixture.newPublisher(t, repository)

	_, err := instance.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if err == nil || !strings.Contains(err.Error(), "database commit failed") {
		t.Fatalf("Publish() error = %v", err)
	}
	fixture.assertStateAndConfig(t, fixture.initialState, fixture.initialYAML)
	revisions, err := fixture.repository.Revisions(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 1 || revisions[0].Status != domain.RevisionFailed {
		t.Fatalf("revisions = %#v", revisions)
	}
}

func TestCommitDurableWriteSuccessReturningErrorIsRecognizedAsSuccess(t *testing.T) {
	t.Parallel()
	fixture := newSQLiteReconciliationFixture(t)
	repository := &commitFaultRepository{
		PublicationRepository: fixture.repository,
		mode:                  commitSucceedsThenReturnsError,
	}
	instance := fixture.newPublisher(t, repository)

	revision, err := instance.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if revision.Status != domain.RevisionActive || revision.ActivatedAt == nil {
		t.Fatalf("revision = %#v", revision)
	}
	fixture.assertStateAndConfig(t, fixture.nextState, fixture.nextYAML)
	revisions, err := fixture.repository.Revisions(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 1 || revisions[0].Status != domain.RevisionActive {
		t.Fatalf("revisions = %#v", revisions)
	}
}

func TestCommitDurableWriteSuccessRepublishesOldOrMissingYAML(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		tamper func(sqliteReconciliationFixture) error
	}{
		{
			name: "old",
			tamper: func(fixture sqliteReconciliationFixture) error {
				return os.WriteFile(fixture.configPath, fixture.initialYAML, 0o640)
			},
		},
		{
			name: "missing",
			tamper: func(fixture sqliteReconciliationFixture) error {
				return os.Remove(fixture.configPath)
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newSQLiteReconciliationFixture(t)
			repository := &commitFaultRepository{
				PublicationRepository: fixture.repository,
				mode:                  commitSucceedsThenReturnsError,
				afterCommit: func() error {
					return test.tamper(fixture)
				},
			}
			instance := fixture.newPublisher(t, repository)

			revision, err := instance.Publish(
				context.Background(),
				fixture.request(fixture.nextState),
			)
			if err != nil {
				t.Fatalf("Publish() error = %v", err)
			}
			if revision.Status != domain.RevisionActive {
				t.Fatalf("revision = %#v", revision)
			}
			fixture.assertStateAndConfig(t, fixture.nextState, fixture.nextYAML)
			if fixture.cli.calls.Load() != 2 {
				t.Fatalf("Mihomo validation calls = %d, want 2", fixture.cli.calls.Load())
			}
		})
	}
}

func TestCommitFailureRecognizesDBOldAndYAMLOld(t *testing.T) {
	t.Parallel()
	fixture := newSQLiteReconciliationFixture(t)
	repository := &commitFaultRepository{
		PublicationRepository: fixture.repository,
		mode:                  commitFailsBeforeDurableWrite,
		afterCommit: func() error {
			return os.WriteFile(fixture.configPath, fixture.initialYAML, 0o640)
		},
	}
	instance := fixture.newPublisher(t, repository)

	_, err := instance.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if err == nil || !strings.Contains(err.Error(), "database commit failed") {
		t.Fatalf("Publish() error = %v", err)
	}
	fixture.assertStateAndConfig(t, fixture.initialState, fixture.initialYAML)
	if fixture.controller.reloadCount() != 1 {
		t.Fatal("clean commit failure unnecessarily reloaded the old YAML")
	}
}

func TestCommitFailureUsesPreviousRevisionTimeForExpiredOldState(t *testing.T) {
	t.Parallel()
	fixture := newSQLiteReconciliationFixture(t)
	publicationTime := fixture.effectiveTime
	expiresAt := publicationTime.Add(time.Minute)
	activeState := cloneState(fixture.initialState)
	activeState.AsOf = publicationTime
	activeState.Listeners[0].Users[0].ExpiresAt = &expiresAt
	transaction, err := fixture.repository.BeginImmediate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.ReplaceDesiredState(
		context.Background(),
		activeState,
	); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	activeYAML, err := (YAMLCompiler{}).Compile(context.Background(), activeState)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.configPath, activeYAML, 0o640); err != nil {
		t.Fatal(err)
	}
	fixture.initialState = activeState
	fixture.initialYAML = activeYAML
	seedPublisher := fixture.newPublisher(t, fixture.repository)
	if _, err := seedPublisher.Publish(
		context.Background(),
		fixture.request(activeState),
	); err != nil {
		t.Fatalf("seed active revision: %v", err)
	}

	expiryTime := expiresAt.Add(time.Minute)
	expiredState := cloneState(activeState)
	expiredState.AsOf = expiryTime
	expiredState.Listeners[0].Enabled = false
	expiredState.Listeners[0].Users[0].Enabled = false
	repository := &commitFaultRepository{
		PublicationRepository: fixture.repository,
		mode:                  commitFailsBeforeDurableWrite,
	}
	instance := fixture.newPublisher(t, repository)
	instance.now = func() time.Time { return expiryTime }

	_, err = instance.Publish(
		context.Background(),
		fixture.request(expiredState),
	)
	if err == nil || !strings.Contains(err.Error(), "database commit failed") {
		t.Fatalf("Publish() error = %v", err)
	}
	fixture.assertStateAndConfig(t, activeState, activeYAML)
	systemState, err := fixture.repository.SystemState(context.Background())
	if err != nil || systemState.Degraded {
		t.Fatalf("SystemState() = %#v, %v", systemState, err)
	}
}

func TestCommitReconciliationFreshReadFailureMarksDegraded(t *testing.T) {
	t.Parallel()
	fixture := newSQLiteReconciliationFixture(t)
	repository := &commitFaultRepository{
		PublicationRepository: fixture.repository,
		mode:                  commitFailsBeforeDurableWrite,
		readErr:               errors.New("synthetic durable read failure"),
	}
	instance := fixture.newPublisher(t, repository)

	_, err := instance.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if err == nil || !strings.Contains(err.Error(), "reconcile uncertain database commit") {
		t.Fatalf("Publish() error = %v", err)
	}
	systemState, stateErr := fixture.repository.SystemState(context.Background())
	if stateErr != nil || !systemState.Degraded {
		t.Fatalf("SystemState() = %#v, %v", systemState, stateErr)
	}
	active, readErr := os.ReadFile(fixture.configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(active) != string(fixture.nextYAML) {
		t.Fatal("unclassified active YAML was overwritten")
	}
}

func TestCommitReconciliationUnclassifiableStateMarksDegraded(t *testing.T) {
	t.Parallel()
	fixture := newSQLiteReconciliationFixture(t)
	repository := &commitFaultRepository{
		PublicationRepository: fixture.repository,
		mode:                  commitSucceedsThenReturnsError,
		afterCommit: func() error {
			_, err := fixture.database.DB().ExecContext(
				context.Background(),
				`UPDATE config_revisions
				    SET sha256 = ?
				  WHERE status = ?`,
				strings.Repeat("0", 64),
				domain.RevisionActive,
			)
			return err
		},
	}
	instance := fixture.newPublisher(t, repository)

	_, err := instance.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if err == nil || !strings.Contains(err.Error(), "cannot classify") {
		t.Fatalf("Publish() error = %v", err)
	}
	systemState, stateErr := fixture.repository.SystemState(context.Background())
	if stateErr != nil || !systemState.Degraded {
		t.Fatalf("SystemState() = %#v, %v", systemState, stateErr)
	}
	revisions, listErr := fixture.repository.Revisions(context.Background(), 10, 0)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(revisions) != 1 || revisions[0].Status == domain.RevisionFailed {
		t.Fatalf("revisions = %#v", revisions)
	}
}

func TestPublishRejectsTamperedOrMissingActiveYAMLBeforeMutation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		tamper     func(*testing.T, activeSQLiteFixture)
		assertKept func(*testing.T, activeSQLiteFixture)
	}{
		{
			name: "tampered",
			tamper: func(t *testing.T, fixture activeSQLiteFixture) {
				t.Helper()
				if err := os.WriteFile(
					fixture.configPath,
					[]byte("externally-modified-active-yaml\n"),
					0o640,
				); err != nil {
					t.Fatal(err)
				}
			},
			assertKept: func(t *testing.T, fixture activeSQLiteFixture) {
				t.Helper()
				content, err := os.ReadFile(fixture.configPath)
				if err != nil {
					t.Fatal(err)
				}
				if string(content) != "externally-modified-active-yaml\n" {
					t.Fatalf("active YAML was overwritten: %q", content)
				}
			},
		},
		{
			name: "missing",
			tamper: func(t *testing.T, fixture activeSQLiteFixture) {
				t.Helper()
				if err := os.Remove(fixture.configPath); err != nil {
					t.Fatal(err)
				}
			},
			assertKept: func(t *testing.T, fixture activeSQLiteFixture) {
				t.Helper()
				if _, err := os.Stat(fixture.configPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("missing active YAML was recreated: %v", err)
				}
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := newActiveSQLiteFixture(t)
			test.tamper(t, fixture)
			revisionsBefore, err := fixture.repository.Revisions(ctx, 10, 0)
			if err != nil {
				t.Fatal(err)
			}
			validationsBefore := fixture.cli.calls.Load()
			reloadsBefore := fixture.controller.reloadCount()
			restartsBefore := fixture.process.restartCount()
			stopsBefore := fixture.process.stopCount()
			mutated := false

			_, err = fixture.publisher.Publish(ctx, Request{
				Reason: "must not mutate inconsistent active state",
				Mutate: func(ctx context.Context, transaction store.PublicationTransaction) error {
					mutated = true
					return transaction.ReplaceDesiredState(ctx, fixture.initialState)
				},
			})
			if err == nil || !strings.Contains(
				err.Error(),
				"active configuration integrity check failed",
			) {
				t.Fatalf("Publish() error = %v", err)
			}
			if mutated {
				t.Fatal("managed-state mutation was called")
			}
			state, stateErr := fixture.repository.ReadDesiredState(
				ctx,
				fixture.effectiveTime,
			)
			if stateErr != nil || state.Listeners[0].Name != fixture.nextState.Listeners[0].Name {
				t.Fatalf("DesiredState() = %#v, %v", state, stateErr)
			}
			revisionsAfter, listErr := fixture.repository.Revisions(ctx, 10, 0)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if len(revisionsAfter) != len(revisionsBefore) {
				t.Fatalf("revisions before/after = %d/%d", len(revisionsBefore), len(revisionsAfter))
			}
			if fixture.cli.calls.Load() != validationsBefore {
				t.Fatal("candidate validation was called")
			}
			if fixture.controller.reloadCount() != reloadsBefore ||
				fixture.process.restartCount() != restartsBefore ||
				fixture.process.stopCount() != stopsBefore {
				t.Fatal("runtime lifecycle operation was called")
			}
			test.assertKept(t, fixture)
			fixture.assertDegraded(t)

			if _, err := fixture.publisher.Publish(
				ctx,
				fixture.request(fixture.initialState),
			); !errors.Is(err, ErrDegraded) {
				t.Fatalf("second Publish() error = %v, want ErrDegraded", err)
			}
		})
	}
}

func TestPublishAcceptsMatchingActiveYAML(t *testing.T) {
	t.Parallel()
	fixture := newActiveSQLiteFixture(t)
	thirdState := cloneState(fixture.nextState)
	thirdState.Listeners[0].Name = "third"
	thirdState.Listeners[0].ListenPort = 9443

	revision, err := fixture.publisher.Publish(
		context.Background(),
		fixture.request(thirdState),
	)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if revision.Status != domain.RevisionActive ||
		fixture.cli.calls.Load() != 2 ||
		fixture.controller.reloadCount() != 2 {
		t.Fatalf(
			"revision/validation/reload = %#v/%d/%d",
			revision,
			fixture.cli.calls.Load(),
			fixture.controller.reloadCount(),
		)
	}
}

func TestPublishRejectsActiveYAMLDriftAfterCandidateValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newActiveSQLiteFixture(t)
	externalYAML := []byte("externally-modified-after-validation\n")
	revisionsBefore, err := fixture.repository.Revisions(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	revisionFilesBefore, err := os.ReadDir(fixture.revisionDir)
	if err != nil {
		t.Fatal(err)
	}
	reloadsBefore := fixture.controller.reloadCount()
	restartsBefore := fixture.process.restartCount()
	stopsBefore := fixture.process.stopCount()
	mutated := false
	fixture.cli.afterValidate = func() error {
		fixture.cli.afterValidate = nil
		return os.WriteFile(fixture.configPath, externalYAML, 0o640)
	}

	_, err = fixture.publisher.Publish(ctx, Request{
		Reason: "detect drift after candidate validation",
		Mutate: func(ctx context.Context, transaction store.PublicationTransaction) error {
			mutated = true
			return transaction.ReplaceDesiredState(ctx, fixture.initialState)
		},
	})
	if err == nil || !strings.Contains(
		err.Error(),
		"active configuration changed after its initial integrity check",
	) {
		t.Fatalf("Publish() error = %v", err)
	}
	if !mutated {
		t.Fatal("managed-state mutation was not exercised inside the transaction")
	}
	state, stateErr := fixture.repository.ReadDesiredState(ctx, fixture.effectiveTime)
	if stateErr != nil ||
		state.Listeners[0].Name != fixture.nextState.Listeners[0].Name {
		t.Fatalf("DesiredState() = %#v, %v", state, stateErr)
	}
	active, readErr := os.ReadFile(fixture.configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(active) != string(externalYAML) {
		t.Fatalf("external YAML was overwritten: %q", active)
	}
	revisionsAfter, listErr := fixture.repository.Revisions(ctx, 10, 0)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(revisionsAfter) != len(revisionsBefore) {
		t.Fatalf(
			"revisions before/after = %d/%d",
			len(revisionsBefore),
			len(revisionsAfter),
		)
	}
	revisionFilesAfter, dirErr := os.ReadDir(fixture.revisionDir)
	if dirErr != nil {
		t.Fatal(dirErr)
	}
	if len(revisionFilesAfter) != len(revisionFilesBefore) {
		t.Fatalf(
			"revision artifacts before/after = %d/%d",
			len(revisionFilesBefore),
			len(revisionFilesAfter),
		)
	}
	if fixture.controller.reloadCount() != reloadsBefore ||
		fixture.process.restartCount() != restartsBefore ||
		fixture.process.stopCount() != stopsBefore {
		t.Fatal("runtime lifecycle operation was called after detecting drift")
	}
	fixture.assertDegraded(t)
	if _, err := fixture.publisher.Publish(
		ctx,
		fixture.request(fixture.initialState),
	); !errors.Is(err, ErrDegraded) {
		t.Fatalf("second Publish() error = %v, want ErrDegraded", err)
	}
}

func TestPublishActiveIntegrityUsesSnapshotAsOfForExpiredUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newSQLiteReconciliationFixture(t)
	publicationTime := fixture.effectiveTime
	expiresAt := publicationTime.Add(time.Minute)
	activeState := cloneState(fixture.nextState)
	activeState.AsOf = publicationTime
	activeState.Listeners[0].Users[0].ExpiresAt = &expiresAt
	transaction, err := fixture.repository.BeginImmediate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.ReplaceDesiredState(ctx, activeState); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	activeYAML, err := (YAMLCompiler{}).Compile(ctx, activeState)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.configPath, activeYAML, 0o640); err != nil {
		t.Fatal(err)
	}
	instance := fixture.newPublisher(t, fixture.repository)
	if _, err := instance.Publish(
		ctx,
		fixture.request(activeState),
	); err != nil {
		t.Fatalf("seed active revision: %v", err)
	}

	later := expiresAt.Add(time.Hour)
	instance.now = func() time.Time { return later }
	laterState := cloneState(activeState)
	laterState.AsOf = later
	laterState.Listeners[0].Name = "after-expiry"
	laterState.Listeners[0].Enabled = false
	laterState.Listeners[0].Users[0].Enabled = false
	revision, err := instance.Publish(ctx, fixture.request(laterState))
	if err != nil {
		t.Fatalf("Publish() after wall time advance error = %v", err)
	}
	if revision.Status != domain.RevisionActive {
		t.Fatalf("revision = %#v", revision)
	}
	systemState, err := fixture.repository.SystemState(ctx)
	if err != nil || systemState.Degraded {
		t.Fatalf("SystemState() = %#v, %v", systemState, err)
	}
}

func TestStartupRepairsMissingOrTamperedActiveYAML(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		tamper func(*testing.T, activeSQLiteFixture)
	}{
		{
			name: "missing",
			tamper: func(t *testing.T, fixture activeSQLiteFixture) {
				t.Helper()
				if err := os.Remove(fixture.configPath); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "tampered",
			tamper: func(t *testing.T, fixture activeSQLiteFixture) {
				t.Helper()
				if err := os.WriteFile(
					fixture.configPath,
					[]byte("tampered-active-yaml\n"),
					0o640,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newActiveSQLiteFixture(t)
			test.tamper(t, fixture)
			validationsBefore := fixture.cli.calls.Load()
			reloadsBefore := fixture.controller.reloadCount()

			if err := fixture.publisher.ReconcileStartup(context.Background()); err != nil {
				t.Fatalf("ReconcileStartup() error = %v", err)
			}
			active, err := os.ReadFile(fixture.configPath)
			if err != nil {
				t.Fatal(err)
			}
			revisionYAML, err := os.ReadFile(fixture.revision.FilePath)
			if err != nil {
				t.Fatal(err)
			}
			if string(active) != string(revisionYAML) {
				t.Fatal("active YAML was not rebuilt from durable state")
			}
			if fixture.cli.calls.Load() != validationsBefore+1 {
				t.Fatal("rebuilt startup candidate was not validated")
			}
			if fixture.controller.reloadCount() != reloadsBefore+1 {
				t.Fatal("rebuilt startup candidate was not reloaded")
			}
			systemState, err := fixture.repository.SystemState(context.Background())
			if err != nil || systemState.Degraded {
				t.Fatalf("SystemState() = %#v, %v", systemState, err)
			}
		})
	}
}

func TestStartupRecoversDegradedMissingOrTamperedActiveYAML(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		tamper func(*testing.T, activeSQLiteFixture)
	}{
		{
			name: "missing",
			tamper: func(t *testing.T, fixture activeSQLiteFixture) {
				t.Helper()
				if err := os.Remove(fixture.configPath); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "tampered",
			tamper: func(t *testing.T, fixture activeSQLiteFixture) {
				t.Helper()
				if err := os.WriteFile(
					fixture.configPath,
					[]byte("tampered-before-degraded-recovery\n"),
					0o640,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := newActiveSQLiteFixture(t)
			test.tamper(t, fixture)

			if _, err := fixture.publisher.Publish(
				ctx,
				fixture.request(fixture.initialState),
			); err == nil || !strings.Contains(
				err.Error(),
				"active configuration integrity check failed",
			) {
				t.Fatalf("Publish() integrity error = %v", err)
			}
			fixture.assertDegraded(t)

			if err := fixture.publisher.ReconcileStartup(ctx); err != nil {
				t.Fatalf("ReconcileStartup() error = %v", err)
			}
			active, err := os.ReadFile(fixture.configPath)
			if err != nil {
				t.Fatal(err)
			}
			revisionYAML, err := os.ReadFile(fixture.revision.FilePath)
			if err != nil {
				t.Fatal(err)
			}
			if string(active) != string(revisionYAML) {
				t.Fatal("active YAML was not restored from the active revision")
			}
			systemState, err := fixture.repository.SystemState(ctx)
			if err != nil || systemState.Degraded {
				t.Fatalf("SystemState() = %#v, %v", systemState, err)
			}
			revision, err := fixture.publisher.Publish(
				ctx,
				fixture.request(fixture.initialState),
			)
			if err != nil || revision.Status != domain.RevisionActive {
				t.Fatalf("Publish() after recovery = %#v, %v", revision, err)
			}
		})
	}
}

func TestStartupKeepsDegradedWhenEvidenceRemainsInconsistent(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		tamper func(*testing.T, activeSQLiteFixture)
	}{
		{
			name: "revision archive damaged",
			tamper: func(t *testing.T, fixture activeSQLiteFixture) {
				t.Helper()
				if err := os.WriteFile(
					fixture.revision.FilePath,
					[]byte("damaged-revision-archive\n"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "database differs from active revision",
			tamper: func(t *testing.T, fixture activeSQLiteFixture) {
				t.Helper()
				if _, err := fixture.database.DB().ExecContext(
					context.Background(),
					"UPDATE listeners SET listen_port = 10443",
				); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := newActiveSQLiteFixture(t)
			if err := os.WriteFile(
				fixture.configPath,
				[]byte("tampered-to-enter-degraded\n"),
				0o640,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.publisher.Publish(
				ctx,
				fixture.request(fixture.initialState),
			); err == nil {
				t.Fatal("Publish() integrity error = nil")
			}
			fixture.assertDegraded(t)
			test.tamper(t, fixture)

			err := fixture.publisher.ReconcileStartup(ctx)
			if !errors.Is(err, ErrStartupDegraded) {
				t.Fatalf("ReconcileStartup() error = %v, want ErrStartupDegraded", err)
			}
			fixture.assertDegraded(t)
		})
	}
}

func TestStartupDoesNotClearDegradedWithoutActiveRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newSQLiteReconciliationFixture(t)
	if err := fixture.repository.MarkDegraded(
		ctx,
		"synthetic bootstrap inconsistency",
		"",
		fixture.effectiveTime,
	); err != nil {
		t.Fatal(err)
	}
	instance := fixture.newPublisher(t, fixture.repository)

	err := instance.ReconcileStartup(ctx)
	if !errors.Is(err, ErrStartupDegraded) {
		t.Fatalf("ReconcileStartup() error = %v, want ErrStartupDegraded", err)
	}
	systemState, stateErr := fixture.repository.SystemState(ctx)
	if stateErr != nil || !systemState.Degraded {
		t.Fatalf("SystemState() = %#v, %v", systemState, stateErr)
	}
}

func TestStartupClearDegradedFailureDoesNotReportRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newActiveSQLiteFixture(t)
	if err := os.WriteFile(
		fixture.configPath,
		[]byte("tampered-before-clear-failure\n"),
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.publisher.Publish(
		ctx,
		fixture.request(fixture.initialState),
	); err == nil {
		t.Fatal("Publish() integrity error = nil")
	}
	repository := &clearFaultRepository{
		PublicationRepository: fixture.repository,
		clearErr:              errors.New("synthetic clear degraded failure"),
	}
	instance := fixture.newPublisher(t, repository)

	err := instance.ReconcileStartup(ctx)
	if !errors.Is(err, ErrStartupDegraded) ||
		!strings.Contains(err.Error(), "clear degraded state") {
		t.Fatalf("ReconcileStartup() error = %v, want clear failure", err)
	}
	if repository.clearCalls.Load() != 1 {
		t.Fatalf("ClearDegraded() calls = %d, want 1", repository.clearCalls.Load())
	}
	fixture.assertDegraded(t)
	if _, err := instance.Publish(
		ctx,
		fixture.request(fixture.initialState),
	); !errors.Is(err, ErrDegraded) {
		t.Fatalf("Publish() after clear failure = %v, want ErrDegraded", err)
	}
}

func TestStartupClearAndCompensationFailureIsFatal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newActiveSQLiteFixture(t)
	if err := fixture.repository.MarkDegraded(
		ctx,
		"synthetic degraded state",
		fixture.revision.ID,
		fixture.effectiveTime,
	); err != nil {
		t.Fatal(err)
	}
	repository := &clearAndMarkFaultRepository{
		PublicationRepository: fixture.repository,
		clearErr: errors.New(
			"synthetic clear failure secret=clear-sensitive-value",
		),
		markErr: errors.New(
			"synthetic mark failure private_key=mark-sensitive-value",
		),
	}
	instance := fixture.newPublisher(t, repository)

	err := instance.ReconcileStartup(ctx)
	if err == nil {
		t.Fatal("ReconcileStartup() error = nil")
	}
	if errors.Is(err, ErrStartupDegraded) {
		t.Fatalf(
			"ReconcileStartup() error = %v, must be fatal and not wrap ErrStartupDegraded",
			err,
		)
	}
	if repository.clearCalls.Load() != 1 || repository.markCalls.Load() != 1 {
		t.Fatalf(
			"ClearDegraded/MarkDegraded calls = %d/%d, want 1/1",
			repository.clearCalls.Load(),
			repository.markCalls.Load(),
		)
	}
	if !repository.clearReleased.Load() {
		t.Fatal("ClearDegraded context was not released before MarkDegraded")
	}
	if !repository.independentMarkContext.Load() {
		t.Fatal("MarkDegraded did not receive a fresh independent context")
	}
	if strings.Contains(err.Error(), "clear-sensitive-value") ||
		strings.Contains(err.Error(), "mark-sensitive-value") {
		t.Fatalf("ReconcileStartup() leaked sensitive error text: %v", err)
	}
}

func TestStartupClearsDegradedAfterRevalidatingMatchingActiveYAML(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newActiveSQLiteFixture(t)
	if err := fixture.repository.MarkDegraded(
		ctx,
		"synthetic degraded state with intact publication evidence",
		fixture.revision.ID,
		fixture.effectiveTime,
	); err != nil {
		t.Fatal(err)
	}
	validationsBefore := fixture.cli.calls.Load()
	reloadsBefore := fixture.controller.reloadCount()

	if err := fixture.publisher.ReconcileStartup(ctx); err != nil {
		t.Fatalf("ReconcileStartup() error = %v", err)
	}
	systemState, err := fixture.repository.SystemState(ctx)
	if err != nil || systemState.Degraded {
		t.Fatalf("SystemState() = %#v, %v", systemState, err)
	}
	if fixture.cli.calls.Load() != validationsBefore+1 {
		t.Fatal("matching active YAML was not revalidated before clearing degraded")
	}
	if fixture.controller.reloadCount() != reloadsBefore+1 {
		t.Fatal("matching active YAML was not reloaded before clearing degraded")
	}
	active, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	revisionYAML, err := os.ReadFile(fixture.revision.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(active) != string(revisionYAML) {
		t.Fatal("matching active YAML changed during degraded recovery")
	}
}

func TestStartupDBAndActiveRevisionMismatchMarksDegraded(t *testing.T) {
	t.Parallel()
	fixture := newActiveSQLiteFixture(t)
	if _, err := fixture.database.DB().ExecContext(
		context.Background(),
		"UPDATE listeners SET listen_port = 10443",
	); err != nil {
		t.Fatal(err)
	}

	err := fixture.publisher.ReconcileStartup(context.Background())
	if !errors.Is(err, ErrStartupDegraded) {
		t.Fatalf("ReconcileStartup() error = %v, want ErrStartupDegraded", err)
	}
	fixture.assertDegraded(t)
}

func TestStartupRepairFailureMarksDegraded(t *testing.T) {
	t.Parallel()
	fixture := newActiveSQLiteFixture(t)
	tampered := []byte("tampered-active-yaml\n")
	if err := os.WriteFile(fixture.configPath, tampered, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repository.MarkDegraded(
		context.Background(),
		"synthetic degraded state before failed recovery",
		fixture.revision.ID,
		fixture.effectiveTime,
	); err != nil {
		t.Fatal(err)
	}
	fixture.cli.validateErr = errors.New("synthetic startup validation failure")

	err := fixture.publisher.ReconcileStartup(context.Background())
	if !errors.Is(err, ErrStartupDegraded) {
		t.Fatalf("ReconcileStartup() error = %v, want ErrStartupDegraded", err)
	}
	fixture.assertDegraded(t)
	active, readErr := os.ReadFile(fixture.configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(active) != string(tampered) {
		t.Fatal("failed startup repair overwrote the active YAML")
	}
}

func TestStartupRevisionYAMLIntegrityFailureMarksDegraded(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		tamper func(*testing.T, activeSQLiteFixture)
	}{
		{
			name: "missing",
			tamper: func(t *testing.T, fixture activeSQLiteFixture) {
				t.Helper()
				if err := os.Remove(fixture.revision.FilePath); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "tampered",
			tamper: func(t *testing.T, fixture activeSQLiteFixture) {
				t.Helper()
				if err := os.WriteFile(
					fixture.revision.FilePath,
					[]byte("tampered-revision-yaml\n"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newActiveSQLiteFixture(t)
			test.tamper(t, fixture)
			err := fixture.publisher.ReconcileStartup(context.Background())
			if !errors.Is(err, ErrStartupDegraded) {
				t.Fatalf("ReconcileStartup() error = %v, want ErrStartupDegraded", err)
			}
			fixture.assertDegraded(t)
		})
	}
}

func TestStartupRevisionJSONIntegrityFailureMarksDegraded(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		tamper func(*testing.T, activeSQLiteFixture)
	}{
		{
			name: "missing",
			tamper: func(t *testing.T, fixture activeSQLiteFixture) {
				t.Helper()
				if err := os.Remove(fixture.revision.StateFilePath); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "tampered",
			tamper: func(t *testing.T, fixture activeSQLiteFixture) {
				t.Helper()
				if err := os.WriteFile(
					fixture.revision.StateFilePath,
					[]byte(`{"version":1,"state":{},"unexpected":true}`),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newActiveSQLiteFixture(t)
			test.tamper(t, fixture)
			err := fixture.publisher.ReconcileStartup(context.Background())
			if !errors.Is(err, ErrStartupDegraded) {
				t.Fatalf("ReconcileStartup() error = %v, want ErrStartupDegraded", err)
			}
			fixture.assertDegraded(t)
		})
	}
}

func TestStartupWithoutActiveRevisionLeavesBootstrapYAMLUntouched(t *testing.T) {
	t.Parallel()
	fixture := newSQLiteReconciliationFixture(t)
	bootstrap := []byte("bootstrap-owned-yaml\n")
	if err := os.WriteFile(fixture.configPath, bootstrap, 0o640); err != nil {
		t.Fatal(err)
	}
	instance := fixture.newPublisher(t, fixture.repository)

	if err := instance.ReconcileStartup(context.Background()); err != nil {
		t.Fatalf("ReconcileStartup() error = %v", err)
	}
	active, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(active) != string(bootstrap) {
		t.Fatal("bootstrap YAML changed without an active revision")
	}
	if fixture.cli.calls.Load() != 0 {
		t.Fatal("bootstrap YAML was unexpectedly validated or republished")
	}
	systemState, err := fixture.repository.SystemState(context.Background())
	if err != nil || systemState.Degraded {
		t.Fatalf("SystemState() = %#v, %v", systemState, err)
	}
}

func TestStartupIntegrityUsesTheActiveRevisionEffectiveTime(t *testing.T) {
	t.Parallel()
	fixture := newSQLiteReconciliationFixture(t)
	expiresAfterPublication := fixture.effectiveTime.Add(time.Hour)
	fixture.nextState.Listeners[0].Users[0].ExpiresAt = &expiresAfterPublication
	nextYAML, err := (YAMLCompiler{}).Compile(
		context.Background(),
		fixture.nextState,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.nextYAML = nextYAML
	instance := fixture.newPublisher(t, fixture.repository)
	if _, err := instance.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	); err != nil {
		t.Fatal(err)
	}
	instance.now = func() time.Time {
		return expiresAfterPublication.Add(time.Hour)
	}

	if err := instance.ReconcileStartup(context.Background()); err != nil {
		t.Fatalf("ReconcileStartup() error = %v", err)
	}
	systemState, err := fixture.repository.SystemState(context.Background())
	if err != nil || systemState.Degraded {
		t.Fatalf("SystemState() = %#v, %v", systemState, err)
	}
	active, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(active) != string(nextYAML) {
		t.Fatal("startup integrity rewrote a valid revision because wall time advanced")
	}
}

type sqliteReconciliationFixture struct {
	database      *store.Store
	repository    *store.ManagedStore
	cli           *fakeCLI
	controller    *fakeController
	process       *fakeProcess
	configPath    string
	revisionDir   string
	initialState  domain.DesiredState
	nextState     domain.DesiredState
	initialYAML   []byte
	nextYAML      []byte
	effectiveTime time.Time
}

func newSQLiteReconciliationFixture(t *testing.T) sqliteReconciliationFixture {
	t.Helper()
	ctx := context.Background()
	directory := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(directory, "m-ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := store.NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	initialState := publisherState("old", 443)
	nextState := publisherState("next", 8443)
	effectiveTime := initialState.AsOf.Add(time.Minute)
	initialState.AsOf = effectiveTime
	nextState.AsOf = effectiveTime
	transaction, err := repository.BeginImmediate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.ReplaceDesiredState(ctx, initialState); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	initialYAML, err := (YAMLCompiler{}).Compile(ctx, initialState)
	if err != nil {
		t.Fatal(err)
	}
	nextYAML, err := (YAMLCompiler{}).Compile(ctx, nextState)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "mihomo", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, initialYAML, 0o640); err != nil {
		t.Fatal(err)
	}
	return sqliteReconciliationFixture{
		database:      database,
		repository:    repository,
		cli:           &fakeCLI{},
		controller:    &fakeController{},
		process:       &fakeProcess{active: true},
		configPath:    configPath,
		revisionDir:   filepath.Join(directory, "revisions"),
		initialState:  initialState,
		nextState:     nextState,
		initialYAML:   initialYAML,
		nextYAML:      nextYAML,
		effectiveTime: effectiveTime,
	}
}

func (fixture sqliteReconciliationFixture) newPublisher(
	t *testing.T,
	repository store.PublicationRepository,
) *Publisher {
	t.Helper()
	instance, err := New(
		repository,
		YAMLCompiler{},
		fixture.cli,
		fixture.controller,
		fixture.process,
		Options{
			ConfigPath:        fixture.configPath,
			RevisionDirectory: fixture.revisionDir,
			HistoryLimit:      20,
			HealthTimeout:     50 * time.Millisecond,
			HealthInterval:    time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	instance.now = func() time.Time { return fixture.effectiveTime }
	return instance
}

func (fixture sqliteReconciliationFixture) request(state domain.DesiredState) Request {
	return Request{
		Reason: "SQLite reconciliation test",
		Mutate: func(ctx context.Context, transaction store.PublicationTransaction) error {
			return transaction.ReplaceDesiredState(ctx, state)
		},
	}
}

func (fixture sqliteReconciliationFixture) assertStateAndConfig(
	t *testing.T,
	wantState domain.DesiredState,
	wantConfig []byte,
) {
	t.Helper()
	state, err := fixture.repository.ReadDesiredState(
		context.Background(),
		fixture.effectiveTime,
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.Listeners[0].Name != wantState.Listeners[0].Name {
		t.Fatalf("durable listener name = %q, want %q", state.Listeners[0].Name, wantState.Listeners[0].Name)
	}
	active, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(active) != string(wantConfig) {
		t.Fatalf("active YAML differs\n--- got ---\n%s\n--- want ---\n%s", active, wantConfig)
	}
}

type activeSQLiteFixture struct {
	sqliteReconciliationFixture
	publisher *Publisher
	revision  domain.Revision
}

func newActiveSQLiteFixture(t *testing.T) activeSQLiteFixture {
	t.Helper()
	fixture := newSQLiteReconciliationFixture(t)
	instance := fixture.newPublisher(t, fixture.repository)
	revision, err := instance.Publish(
		context.Background(),
		fixture.request(fixture.nextState),
	)
	if err != nil {
		t.Fatal(err)
	}
	return activeSQLiteFixture{
		sqliteReconciliationFixture: fixture,
		publisher:                   instance,
		revision:                    revision,
	}
}

func (fixture activeSQLiteFixture) assertDegraded(t *testing.T) {
	t.Helper()
	systemState, err := fixture.repository.SystemState(context.Background())
	if err != nil || !systemState.Degraded ||
		systemState.DegradedRevisionID != fixture.revision.ID {
		t.Fatalf("SystemState() = %#v, %v", systemState, err)
	}
}

type commitFaultRepository struct {
	store.PublicationRepository
	mode        commitFaultMode
	afterCommit func() error
	readErr     error
}

type clearFaultRepository struct {
	store.PublicationRepository
	clearErr   error
	clearCalls atomic.Int32
}

func (repository *clearFaultRepository) ClearDegraded(
	context.Context,
	time.Time,
) error {
	repository.clearCalls.Add(1)
	return repository.clearErr
}

type clearAndMarkFaultRepository struct {
	store.PublicationRepository
	clearErr               error
	markErr                error
	clearContext           context.Context
	clearCalls             atomic.Int32
	markCalls              atomic.Int32
	clearReleased          atomic.Bool
	independentMarkContext atomic.Bool
}

func (repository *clearAndMarkFaultRepository) ClearDegraded(
	ctx context.Context,
	_ time.Time,
) error {
	repository.clearCalls.Add(1)
	repository.clearContext = ctx
	return repository.clearErr
}

func (repository *clearAndMarkFaultRepository) MarkDegraded(
	ctx context.Context,
	_, _ string,
	_ time.Time,
) error {
	repository.markCalls.Add(1)
	select {
	case <-repository.clearContext.Done():
		repository.clearReleased.Store(true)
	default:
	}
	repository.independentMarkContext.Store(ctx != repository.clearContext)
	return repository.markErr
}

func (repository *commitFaultRepository) BeginImmediate(
	ctx context.Context,
) (store.PublicationTransaction, error) {
	transaction, err := repository.PublicationRepository.BeginImmediate(ctx)
	if err != nil {
		return nil, err
	}
	return &commitFaultTransaction{
		PublicationTransaction: transaction,
		mode:                   repository.mode,
		afterCommit:            repository.afterCommit,
	}, nil
}

func (repository *commitFaultRepository) ReadPublicationSnapshot(
	ctx context.Context,
	asOf time.Time,
) (store.PublicationSnapshot, error) {
	if repository.readErr != nil {
		return store.PublicationSnapshot{}, repository.readErr
	}
	return repository.PublicationRepository.ReadPublicationSnapshot(ctx, asOf)
}

type commitFaultTransaction struct {
	store.PublicationTransaction
	mode        commitFaultMode
	afterCommit func() error
}

func (transaction *commitFaultTransaction) Commit(ctx context.Context) error {
	synthetic := errors.New("synthetic uncertain commit result")
	switch transaction.mode {
	case commitFailsBeforeDurableWrite:
		if err := transaction.Rollback(ctx); err != nil {
			return errors.Join(synthetic, err)
		}
		if transaction.afterCommit != nil {
			return errors.Join(synthetic, transaction.afterCommit())
		}
		return synthetic
	case commitSucceedsThenReturnsError:
		if err := transaction.PublicationTransaction.Commit(ctx); err != nil {
			return err
		}
		if transaction.afterCommit != nil {
			return errors.Join(synthetic, transaction.afterCommit())
		}
		return synthetic
	default:
		return transaction.PublicationTransaction.Commit(ctx)
	}
}
