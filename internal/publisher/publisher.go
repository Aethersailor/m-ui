package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/redact"
	"github.com/Aethersailor/m-ui/internal/store"
)

var (
	ErrDegraded            = errors.New("configuration publishing is blocked because the system is degraded")
	ErrCandidateValidation = errors.New("candidate configuration validation failed")
	ErrStartupDegraded     = errors.New("startup reconciliation marked the system degraded")
)

type Mutation func(ctx context.Context, transaction store.PublicationTransaction) error

type Request struct {
	Reason           string
	ActorAdminID     string
	AuditAction      string
	AuditResource    string
	AuditResourceID  string
	AuditSummary     string
	AuditSummaryFunc func() string
	EffectiveAt      *time.Time
	Mutate           Mutation
}

type Options struct {
	ConfigPath        string
	RevisionDirectory string
	HistoryLimit      int
	HealthTimeout     time.Duration
	HealthInterval    time.Duration
	Logger            *slog.Logger
}

type Publisher struct {
	repository store.PublicationRepository
	compiler   Compiler
	cli        mihomo.CoreCLI
	controller mihomo.CoreController
	process    mihomo.CoreProcess
	options    Options
	logger     *slog.Logger
	now        func() time.Time
	mutex      sync.Mutex
}

type publicationFileState struct {
	content []byte
	exists  bool
	sha256  string
}

type publicationBaseline struct {
	databaseSHA256 string
	databaseAsOf   time.Time
	activeRevision *domain.Revision
	activeYAML     publicationFileState
}

func New(
	repository store.PublicationRepository,
	compiler Compiler,
	cli mihomo.CoreCLI,
	controller mihomo.CoreController,
	process mihomo.CoreProcess,
	options Options,
) (*Publisher, error) {
	switch {
	case repository == nil:
		return nil, errors.New("publication repository is required")
	case compiler == nil:
		return nil, errors.New("configuration compiler is required")
	case cli == nil:
		return nil, errors.New("mihomo CLI is required")
	case controller == nil:
		return nil, errors.New("mihomo Controller is required")
	case process == nil:
		return nil, errors.New("mihomo process adapter is required")
	case !filepath.IsAbs(options.ConfigPath):
		return nil, errors.New("mihomo configuration path must be absolute")
	case !filepath.IsAbs(options.RevisionDirectory):
		return nil, errors.New("revision directory must be absolute")
	case options.HistoryLimit < 1:
		return nil, errors.New("revision history limit must be positive")
	case options.HealthTimeout <= 0:
		return nil, errors.New("health timeout must be positive")
	case options.HealthInterval <= 0:
		return nil, errors.New("health interval must be positive")
	}
	if err := prepareDirectories(options.ConfigPath, options.RevisionDirectory); err != nil {
		return nil, err
	}
	if err := rejectSymlink(options.ConfigPath); err != nil {
		return nil, err
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Publisher{
		repository: repository,
		compiler:   compiler,
		cli:        cli,
		controller: controller,
		process:    process,
		options:    options,
		logger:     options.Logger,
		now:        func() time.Time { return time.Now().UTC() },
	}, nil
}

func (publisher *Publisher) Publish(
	ctx context.Context,
	request Request,
) (domain.Revision, error) {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	return publisher.publishLocked(ctx, request)
}

func (publisher *Publisher) CompileCurrent(
	ctx context.Context,
	asOf time.Time,
) ([]byte, domain.DesiredState, error) {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()

	transaction, err := publisher.repository.BeginImmediate(ctx)
	if err != nil {
		return nil, domain.DesiredState{}, err
	}
	defer func() {
		_ = transaction.Rollback(context.Background())
	}()
	state, err := transaction.DesiredState(ctx, asOf.UTC())
	if err != nil {
		return nil, domain.DesiredState{}, fmt.Errorf("load desired state: %w", err)
	}
	compiled, err := publisher.compiler.Compile(ctx, state)
	if err != nil {
		return nil, domain.DesiredState{}, err
	}
	return compiled, state, nil
}

func (publisher *Publisher) ValidateCurrent(
	ctx context.Context,
	asOf time.Time,
) ([]byte, error) {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()

	transaction, err := publisher.repository.BeginImmediate(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = transaction.Rollback(context.Background())
	}()
	state, err := transaction.DesiredState(ctx, asOf.UTC())
	if err != nil {
		return nil, fmt.Errorf("load desired state: %w", err)
	}
	compiled, err := publisher.compiler.Compile(ctx, state)
	if err != nil {
		return nil, err
	}
	validationID, err := uuid.NewRandom()
	if err != nil {
		return nil, errors.New("generate validation candidate ID")
	}
	candidatePath := filepath.Join(
		filepath.Dir(publisher.options.ConfigPath),
		".m-ui-validation-"+validationID.String()+".yaml",
	)
	defer func() {
		_ = os.Remove(candidatePath)
	}()
	if err := writeExclusiveSynced(candidatePath, compiled); err != nil {
		return nil, err
	}
	if err := publisher.cli.Validate(ctx, candidatePath); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCandidateValidation, err)
	}
	return compiled, nil
}

func (publisher *Publisher) ReconcileStartup(ctx context.Context) error {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()

	reconciliationContext, cancel := context.WithTimeout(
		ctx,
		45*time.Second+publisher.options.HealthTimeout,
	)
	defer cancel()
	systemState, err := publisher.repository.SystemState(reconciliationContext)
	if err != nil {
		return fmt.Errorf("read system state before startup reconciliation: %w", err)
	}
	if systemState.Degraded {
		return fmt.Errorf(
			"%w: the system was already degraded before startup",
			ErrStartupDegraded,
		)
	}

	initial, err := publisher.repository.ReadPublicationSnapshot(
		reconciliationContext,
		publisher.now().UTC(),
	)
	if err != nil {
		if errors.Is(err, store.ErrMultipleActiveRevisions) {
			return publisher.markStartupDegraded(
				reconciliationContext,
				"",
				"multiple active revisions prevent safe startup reconciliation",
			)
		}
		return fmt.Errorf("read startup publication state: %w", err)
	}
	if initial.ActiveRevision == nil {
		return nil
	}
	activeRevision := *initial.ActiveRevision
	if !pathWithin(
		publisher.options.RevisionDirectory,
		activeRevision.FilePath,
	) || !pathWithin(
		publisher.options.RevisionDirectory,
		activeRevision.StateFilePath,
	) {
		return publisher.markStartupDegraded(
			reconciliationContext,
			activeRevision.ID,
			"active revision artifacts are outside the managed revision directory",
		)
	}

	revisionYAML, exists, err := readManagedFile(activeRevision.FilePath)
	if err != nil || !exists || SHA256(revisionYAML) != activeRevision.SHA256 {
		return publisher.markStartupDegraded(
			reconciliationContext,
			activeRevision.ID,
			"active revision YAML is missing or failed its integrity check",
		)
	}
	stateSnapshot, err := readRevisionStateSnapshot(activeRevision)
	if err != nil {
		return publisher.markStartupDegraded(
			reconciliationContext,
			activeRevision.ID,
			"active revision state snapshot is missing or invalid",
		)
	}
	snapshotYAML, err := publisher.compiler.Compile(
		reconciliationContext,
		stateSnapshot.State,
	)
	if err != nil || SHA256(snapshotYAML) != activeRevision.SHA256 {
		return publisher.markStartupDegraded(
			reconciliationContext,
			activeRevision.ID,
			"active revision state snapshot does not match its YAML revision",
		)
	}

	durable, err := publisher.repository.ReadPublicationSnapshot(
		reconciliationContext,
		stateSnapshot.State.AsOf,
	)
	if err != nil {
		if errors.Is(err, store.ErrMultipleActiveRevisions) {
			return publisher.markStartupDegraded(
				reconciliationContext,
				activeRevision.ID,
				"multiple active revisions prevent safe startup reconciliation",
			)
		}
		return fmt.Errorf("read durable startup state: %w", err)
	}
	if !revisionMatches(durable.ActiveRevision, &activeRevision) {
		return publisher.markStartupDegraded(
			reconciliationContext,
			activeRevision.ID,
			"durable active revision changed during startup reconciliation",
		)
	}
	durableYAML, err := publisher.compiler.Compile(
		reconciliationContext,
		durable.State,
	)
	if err != nil || SHA256(durableYAML) != activeRevision.SHA256 {
		return publisher.markStartupDegraded(
			reconciliationContext,
			activeRevision.ID,
			"durable desired state does not match the active revision",
		)
	}

	activeYAML, err := publisher.readActiveYAML()
	if err != nil {
		return publisher.markStartupDegraded(
			reconciliationContext,
			activeRevision.ID,
			"active Mihomo YAML could not be inspected safely",
		)
	}
	if activeYAML.exists && activeYAML.sha256 == activeRevision.SHA256 {
		return nil
	}
	if err := publisher.publishCompiled(
		reconciliationContext,
		durableYAML,
	); err != nil {
		return publisher.markStartupDegraded(
			reconciliationContext,
			activeRevision.ID,
			"active Mihomo YAML could not be repaired from durable state",
		)
	}
	publisher.logger.Warn(
		"startup reconciliation repaired the active Mihomo configuration",
		"revision",
		activeRevision.RevisionNumber,
	)
	return nil
}

func (publisher *Publisher) markStartupDegraded(
	ctx context.Context,
	revisionID string,
	reason string,
) error {
	markContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := publisher.repository.MarkDegraded(
		markContext,
		reason,
		revisionID,
		publisher.now().UTC(),
	); err != nil {
		return fmt.Errorf("persist degraded startup state: %w", err)
	}
	publisher.logger.Error(
		"startup reconciliation requires operator intervention",
		"reason",
		reason,
	)
	return fmt.Errorf("%w: %s", ErrStartupDegraded, reason)
}

func (publisher *Publisher) Rollback(
	ctx context.Context,
	revisionID, actorAdminID string,
) (domain.Revision, error) {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()

	revision, err := publisher.repository.Revision(ctx, revisionID)
	if err != nil {
		return domain.Revision{}, fmt.Errorf("load rollback revision: %w", err)
	}
	if revision.Status != domain.RevisionActive &&
		revision.Status != domain.RevisionRolledBack {
		return domain.Revision{}, errors.New("only successful revisions can be rolled back")
	}
	if !pathWithin(publisher.options.RevisionDirectory, revision.FilePath) ||
		!pathWithin(publisher.options.RevisionDirectory, revision.StateFilePath) {
		return domain.Revision{}, errors.New("rollback artifact path is outside the revision directory")
	}
	revisionYAML, exists, err := readManagedFile(revision.FilePath)
	if err != nil {
		return domain.Revision{}, fmt.Errorf("read rollback YAML: %w", err)
	}
	if !exists {
		return domain.Revision{}, errors.New("rollback YAML is missing")
	}
	if SHA256(revisionYAML) != revision.SHA256 {
		return domain.Revision{}, errors.New("rollback YAML failed its SHA-256 integrity check")
	}
	snapshot, err := readRevisionStateSnapshot(revision)
	if err != nil {
		return domain.Revision{}, fmt.Errorf("read rollback state snapshot: %w", err)
	}
	snapshotYAML, err := publisher.compiler.Compile(ctx, snapshot.State)
	if err != nil || SHA256(snapshotYAML) != revision.SHA256 {
		return domain.Revision{}, errors.New("rollback state snapshot does not match its YAML revision")
	}
	return publisher.publishLocked(ctx, Request{
		Reason:          fmt.Sprintf("rollback to revision %d", revision.RevisionNumber),
		ActorAdminID:    actorAdminID,
		AuditAction:     "config.rollback",
		AuditResource:   "config_revision",
		AuditResourceID: revision.ID,
		AuditSummary:    fmt.Sprintf("rolled back to revision %d", revision.RevisionNumber),
		Mutate: func(ctx context.Context, transaction store.PublicationTransaction) error {
			return transaction.ReplaceDesiredState(ctx, snapshot.State)
		},
	})
}

func (publisher *Publisher) publishLocked(
	ctx context.Context,
	request Request,
) (domain.Revision, error) {
	if strings.TrimSpace(request.Reason) == "" {
		return domain.Revision{}, errors.New("publication reason is required")
	}
	if request.Mutate == nil {
		return domain.Revision{}, errors.New("publication mutation is required")
	}
	systemState, err := publisher.repository.SystemState(ctx)
	if err != nil {
		return domain.Revision{}, fmt.Errorf("check degraded state: %w", err)
	}
	if systemState.Degraded {
		return domain.Revision{}, fmt.Errorf("%w: %s", ErrDegraded, systemState.DegradedReason)
	}

	now := publisher.now().UTC()
	effectiveAt := now
	if request.EffectiveAt != nil {
		effectiveAt = request.EffectiveAt.UTC()
	}
	transaction, err := publisher.repository.BeginImmediate(ctx)
	if err != nil {
		return domain.Revision{}, err
	}
	transactionOpen := true
	defer func() {
		if transactionOpen {
			_ = transaction.Rollback(context.Background())
		}
	}()
	oldActiveRevision, err := transaction.ActiveRevision(ctx)
	if err != nil {
		return domain.Revision{}, fmt.Errorf("load previous active revision: %w", err)
	}
	oldEffectiveAt := effectiveAt
	if oldActiveRevision != nil {
		if !pathWithin(
			publisher.options.RevisionDirectory,
			oldActiveRevision.StateFilePath,
		) {
			return domain.Revision{}, errors.New(
				"previous active revision state is outside the revision directory",
			)
		}
		oldSnapshot, err := readRevisionStateSnapshot(*oldActiveRevision)
		if err != nil {
			return domain.Revision{}, fmt.Errorf(
				"load previous active revision state: %w",
				err,
			)
		}
		oldEffectiveAt = oldSnapshot.State.AsOf
	}
	oldState, err := transaction.DesiredState(ctx, oldEffectiveAt)
	if err != nil {
		return domain.Revision{}, fmt.Errorf("load previous desired state: %w", err)
	}
	oldCompiled, err := publisher.compiler.Compile(ctx, oldState)
	if err != nil {
		return domain.Revision{}, fmt.Errorf("compile previous desired state: %w", err)
	}
	if oldActiveRevision != nil && SHA256(oldCompiled) != oldActiveRevision.SHA256 {
		return domain.Revision{}, errors.New(
			"previous durable state does not match the active revision",
		)
	}
	if err := request.Mutate(ctx, transaction); err != nil {
		return domain.Revision{}, fmt.Errorf("apply managed-state mutation: %w", err)
	}
	state, err := transaction.DesiredState(ctx, effectiveAt)
	if err != nil {
		return domain.Revision{}, fmt.Errorf("load desired state: %w", err)
	}
	compiled, err := publisher.compiler.Compile(ctx, state)
	if err != nil {
		return domain.Revision{}, err
	}
	revisionNumber, err := transaction.NextRevisionNumber(ctx)
	if err != nil {
		return domain.Revision{}, err
	}
	revisionID, err := uuid.NewRandom()
	if err != nil {
		return domain.Revision{}, errors.New("generate revision ID")
	}
	revision := domain.Revision{
		ID:             revisionID.String(),
		RevisionNumber: revisionNumber,
		SHA256:         SHA256(compiled),
		Status:         domain.RevisionPending,
		Reason:         redact.Text(request.Reason),
		ActorAdminID:   request.ActorAdminID,
		CreatedAt:      now,
	}
	revision.FilePath = filepath.Join(
		publisher.options.RevisionDirectory,
		revision.ID+".yaml",
	)
	revision.StateFilePath = filepath.Join(
		publisher.options.RevisionDirectory,
		revision.ID+".json",
	)
	configDirectory := filepath.Dir(publisher.options.ConfigPath)
	candidatePath := filepath.Join(
		configDirectory,
		".m-ui-candidate-"+revision.ID+".yaml",
	)
	defer func() {
		_ = os.Remove(candidatePath)
	}()
	if err := writeSharedConfigSynced(candidatePath, compiled); err != nil {
		return domain.Revision{}, err
	}
	if err := publisher.cli.Validate(ctx, candidatePath); err != nil {
		transactionOpen = false
		_ = transaction.Rollback(context.Background())
		return domain.Revision{}, publisher.recordFailure(
			ctx,
			revision,
			state,
			compiled,
			"candidate validation failed",
			fmt.Errorf("%w: %v", ErrCandidateValidation, err),
		)
	}

	previousConfig, previousExists, err := readManagedFile(publisher.options.ConfigPath)
	if err != nil {
		return domain.Revision{}, err
	}
	baseline := publicationBaseline{
		databaseSHA256: SHA256(oldCompiled),
		databaseAsOf:   oldEffectiveAt,
		activeRevision: oldActiveRevision,
		activeYAML: publicationFileState{
			content: previousConfig,
			exists:  previousExists,
			sha256:  SHA256(previousConfig),
		},
	}
	backupPath := filepath.Join(
		configDirectory,
		".m-ui-rollback-"+revision.ID+".yaml",
	)
	defer func() {
		_ = os.Remove(backupPath)
	}()
	if previousExists {
		if err := writeExclusiveSynced(backupPath, previousConfig); err != nil {
			return domain.Revision{}, err
		}
	}
	if err := archiveRevision(revision, state, compiled); err != nil {
		_ = os.Remove(revision.FilePath)
		_ = os.Remove(revision.StateFilePath)
		return domain.Revision{}, err
	}
	if err := transaction.InsertRevision(ctx, revision); err != nil {
		transactionOpen = false
		_ = transaction.Rollback(context.Background())
		return domain.Revision{}, publisher.recordFailure(
			ctx,
			revision,
			domain.DesiredState{},
			nil,
			"revision metadata insertion failed",
			err,
		)
	}
	if err := replaceAndSync(candidatePath, publisher.options.ConfigPath); err != nil {
		return domain.Revision{}, publisher.failAfterReplacement(
			ctx,
			transaction,
			&transactionOpen,
			revision,
			previousConfig,
			previousExists,
			"atomic configuration replacement failed",
			err,
		)
	}

	if err := publisher.reloadWithFallback(ctx); err != nil {
		return domain.Revision{}, publisher.failAfterReplacement(
			ctx,
			transaction,
			&transactionOpen,
			revision,
			previousConfig,
			previousExists,
			"runtime reload failed",
			err,
		)
	}
	if err := publisher.waitHealthy(ctx); err != nil {
		return domain.Revision{}, publisher.failAfterReplacement(
			ctx,
			transaction,
			&transactionOpen,
			revision,
			previousConfig,
			previousExists,
			"post-publication health check failed",
			err,
		)
	}
	activatedAt := publisher.now().UTC()
	if err := transaction.ActivateRevision(ctx, revision.ID, activatedAt); err != nil {
		return domain.Revision{}, publisher.failAfterReplacement(
			ctx,
			transaction,
			&transactionOpen,
			revision,
			previousConfig,
			previousExists,
			"revision activation failed",
			err,
		)
	}
	if err := publisher.insertSuccessAudit(ctx, transaction, request, activatedAt); err != nil {
		return domain.Revision{}, publisher.failAfterReplacement(
			ctx,
			transaction,
			&transactionOpen,
			revision,
			previousConfig,
			previousExists,
			"audit recording failed",
			err,
		)
	}
	if err := transaction.Commit(ctx); err != nil {
		transactionOpen = false
		return publisher.reconcileCommit(
			ctx,
			revision,
			activatedAt,
			effectiveAt,
			baseline,
			err,
		)
	}
	transactionOpen = false
	return publisher.finishSuccessfulPublication(ctx, revision, activatedAt), nil
}

func (publisher *Publisher) failAfterReplacement(
	ctx context.Context,
	transaction store.PublicationTransaction,
	transactionOpen *bool,
	revision domain.Revision,
	previousConfig []byte,
	previousExists bool,
	stage string,
	cause error,
) error {
	recoveryContext, cancel := publisher.recoveryContext(ctx)
	defer cancel()
	recoveryErr := publisher.restorePrevious(
		recoveryContext,
		revision.ID,
		previousConfig,
		previousExists,
	)
	_ = transaction.Rollback(context.Background())
	*transactionOpen = false
	failureErr := publisher.recordFailure(
		ctx,
		revision,
		domain.DesiredState{},
		nil,
		stage,
		cause,
	)
	if recoveryErr != nil {
		degradedErr := publisher.repository.MarkDegraded(
			context.Background(),
			"automatic configuration recovery failed; restore the indicated revision manually",
			revision.ID,
			publisher.now().UTC(),
		)
		return errors.Join(
			fmt.Errorf("automatic recovery failed: %w", recoveryErr),
			degradedErr,
			failureErr,
		)
	}
	return failureErr
}

func (publisher *Publisher) reconcileCommit(
	ctx context.Context,
	revision domain.Revision,
	activatedAt time.Time,
	effectiveAt time.Time,
	baseline publicationBaseline,
	commitErr error,
) (domain.Revision, error) {
	recoveryContext, cancel := publisher.recoveryContext(ctx)
	defer cancel()

	durable, err := publisher.repository.ReadPublicationSnapshot(
		recoveryContext,
		effectiveAt,
	)
	if err != nil {
		return domain.Revision{}, publisher.markCommitReconciliationDegraded(
			recoveryContext,
			revision.ID,
			"database commit result could not be reconciled from durable state",
			fmt.Errorf("reconcile uncertain database commit: read durable state"),
			err,
		)
	}
	activeYAML, err := publisher.readActiveYAML()
	if err != nil {
		return domain.Revision{}, publisher.markCommitReconciliationDegraded(
			recoveryContext,
			revision.ID,
			"database commit result could not be reconciled with the active configuration",
			fmt.Errorf("reconcile uncertain database commit: read active YAML"),
			err,
		)
	}

	var durableCompiled []byte
	databaseIsNew := false
	databaseIsOld := false
	switch {
	case revisionMatches(durable.ActiveRevision, &revision):
		durableCompiled, err = publisher.compiler.Compile(
			recoveryContext,
			durable.State,
		)
		if err == nil {
			databaseIsNew = SHA256(durableCompiled) == revision.SHA256
		}
	case revisionMatches(durable.ActiveRevision, baseline.activeRevision):
		if !baseline.databaseAsOf.Equal(effectiveAt) {
			durable, err = publisher.repository.ReadPublicationSnapshot(
				recoveryContext,
				baseline.databaseAsOf,
			)
			if err != nil {
				return domain.Revision{}, publisher.markCommitReconciliationDegraded(
					recoveryContext,
					revision.ID,
					"database commit result could not be reconciled from durable state",
					fmt.Errorf("reconcile uncertain database commit: read previous durable state"),
					err,
				)
			}
		}
		if revisionMatches(durable.ActiveRevision, baseline.activeRevision) {
			durableCompiled, err = publisher.compiler.Compile(
				recoveryContext,
				durable.State,
			)
			if err == nil {
				databaseIsOld = SHA256(durableCompiled) ==
					baseline.databaseSHA256
			}
		}
	}
	if err != nil {
		return domain.Revision{}, publisher.markCommitReconciliationDegraded(
			recoveryContext,
			revision.ID,
			"database commit result could not be reconciled from durable state",
			fmt.Errorf("reconcile uncertain database commit: compile durable state"),
			err,
		)
	}
	yamlIsNew := activeYAML.exists && activeYAML.sha256 == revision.SHA256
	yamlIsOld := fileStateMatches(activeYAML, baseline.activeYAML)

	switch {
	case databaseIsNew && yamlIsNew:
		publisher.logger.Warn(
			"database commit returned an error after the publication became durable",
			"revision",
			revision.RevisionNumber,
		)
		return publisher.finishDurablePublication(
			recoveryContext,
			durable.ActiveRevision,
			activatedAt,
		), nil
	case databaseIsOld && yamlIsOld:
		return domain.Revision{}, publisher.recordFailure(
			ctx,
			revision,
			domain.DesiredState{},
			nil,
			"database commit failed",
			commitErr,
		)
	case databaseIsOld && yamlIsNew:
		recoveryErr := publisher.restorePrevious(
			recoveryContext,
			revision.ID,
			baseline.activeYAML.content,
			baseline.activeYAML.exists,
		)
		failureErr := publisher.recordFailure(
			ctx,
			revision,
			domain.DesiredState{},
			nil,
			"database commit failed",
			commitErr,
		)
		if recoveryErr != nil {
			return domain.Revision{}, errors.Join(
				publisher.markCommitReconciliationDegraded(
					recoveryContext,
					revision.ID,
					"database commit failed and the previous configuration could not be restored",
					errors.New("automatic recovery failed after database commit"),
					recoveryErr,
				),
				failureErr,
			)
		}
		return domain.Revision{}, failureErr
	case databaseIsNew && (!activeYAML.exists || yamlIsOld):
		if err := publisher.publishCompiled(
			recoveryContext,
			durableCompiled,
		); err != nil {
			return domain.Revision{}, publisher.markCommitReconciliationDegraded(
				recoveryContext,
				revision.ID,
				"database commit succeeded but the active configuration could not be reconciled",
				errors.New("reconcile uncertain database commit: republish durable state"),
				err,
			)
		}
		publisher.logger.Warn(
			"database commit returned an error and durable state was republished",
			"revision",
			revision.RevisionNumber,
		)
		return publisher.finishDurablePublication(
			recoveryContext,
			durable.ActiveRevision,
			activatedAt,
		), nil
	default:
		return domain.Revision{}, publisher.markCommitReconciliationDegraded(
			recoveryContext,
			revision.ID,
			"database commit result is inconsistent across durable state, active revision, and active configuration",
			errors.New("cannot classify uncertain database commit result"),
			nil,
		)
	}
}

func (publisher *Publisher) markCommitReconciliationDegraded(
	ctx context.Context,
	revisionID string,
	reason string,
	resultErr error,
	cause error,
) error {
	markContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	markErr := publisher.repository.MarkDegraded(
		markContext,
		reason,
		revisionID,
		publisher.now().UTC(),
	)
	return errors.Join(resultErr, cause, markErr)
}

func (publisher *Publisher) finishDurablePublication(
	ctx context.Context,
	durable *domain.Revision,
	activatedAt time.Time,
) domain.Revision {
	if durable == nil {
		return domain.Revision{}
	}
	revision := *durable
	if revision.ActivatedAt == nil {
		revision.ActivatedAt = &activatedAt
	}
	return publisher.finishSuccessfulPublication(ctx, revision, activatedAt)
}

func (publisher *Publisher) finishSuccessfulPublication(
	ctx context.Context,
	revision domain.Revision,
	activatedAt time.Time,
) domain.Revision {
	revision.Status = domain.RevisionActive
	if revision.ActivatedAt == nil {
		revision.ActivatedAt = &activatedAt
	}
	if err := publisher.prune(ctx); err != nil {
		publisher.logger.Warn(
			"configuration publication succeeded but revision maintenance failed",
			"revision",
			revision.RevisionNumber,
			"error",
			redact.Text(err.Error()),
		)
	}
	return revision
}

func (publisher *Publisher) recordFailure(
	ctx context.Context,
	revision domain.Revision,
	state domain.DesiredState,
	compiled []byte,
	stage string,
	cause error,
) error {
	var archiveErr error
	if len(compiled) != 0 {
		archiveErr = archiveRevision(revision, state, compiled)
	}
	revision.Status = domain.RevisionFailed
	revision.ErrorMessageRedacted = stage
	recordContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		5*time.Second,
	)
	defer cancel()
	recordErr := publisher.repository.RecordFailedRevision(
		recordContext,
		revision,
	)
	return errors.Join(
		fmt.Errorf("%s: %w", stage, cause),
		archiveErr,
		recordErr,
	)
}

func archiveRevision(
	revision domain.Revision,
	state domain.DesiredState,
	compiled []byte,
) error {
	snapshot, err := json.MarshalIndent(domain.StateSnapshot{
		Version: 1,
		State:   state,
	}, "", "  ")
	if err != nil {
		return errors.New("encode desired-state revision snapshot")
	}
	snapshot = append(snapshot, '\n')
	if _, exists, err := readManagedFile(revision.FilePath); err != nil {
		return err
	} else if !exists {
		if err := writeExclusiveSynced(revision.FilePath, compiled); err != nil {
			return err
		}
	}
	if _, exists, err := readManagedFile(revision.StateFilePath); err != nil {
		return err
	} else if !exists {
		if err := writeExclusiveSynced(revision.StateFilePath, snapshot); err != nil {
			return err
		}
	}
	if err := syncDirectory(filepath.Dir(revision.FilePath)); err != nil {
		return errors.New("synchronize revision directory")
	}
	return nil
}

func readRevisionStateSnapshot(
	revision domain.Revision,
) (domain.StateSnapshot, error) {
	content, exists, err := readManagedFile(revision.StateFilePath)
	if err != nil {
		return domain.StateSnapshot{}, err
	}
	if !exists {
		return domain.StateSnapshot{}, errors.New("revision state snapshot is missing")
	}
	var snapshot domain.StateSnapshot
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil || snapshot.Version != 1 {
		return domain.StateSnapshot{}, errors.New("revision state snapshot is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return domain.StateSnapshot{}, errors.New("revision state snapshot is invalid")
	}
	return snapshot, nil
}

func (publisher *Publisher) publishCompiled(
	ctx context.Context,
	compiled []byte,
) error {
	candidateID, err := uuid.NewRandom()
	if err != nil {
		return errors.New("generate reconciliation candidate ID")
	}
	candidatePath := filepath.Join(
		filepath.Dir(publisher.options.ConfigPath),
		".m-ui-reconciliation-"+candidateID.String()+".yaml",
	)
	defer func() {
		_ = os.Remove(candidatePath)
	}()
	if err := writeSharedConfigSynced(candidatePath, compiled); err != nil {
		return err
	}
	if err := publisher.cli.Validate(ctx, candidatePath); err != nil {
		return fmt.Errorf("%w during reconciliation", ErrCandidateValidation)
	}
	if err := replaceAndSync(candidatePath, publisher.options.ConfigPath); err != nil {
		return err
	}
	if err := publisher.reloadWithFallback(ctx); err != nil {
		return err
	}
	if err := publisher.waitHealthy(ctx); err != nil {
		return err
	}
	return nil
}

func (publisher *Publisher) readActiveYAML() (publicationFileState, error) {
	content, exists, err := readManagedFile(publisher.options.ConfigPath)
	if err != nil {
		return publicationFileState{}, err
	}
	state := publicationFileState{
		content: content,
		exists:  exists,
	}
	if exists {
		state.sha256 = SHA256(content)
	}
	return state, nil
}

func revisionMatches(actual, expected *domain.Revision) bool {
	if actual == nil || expected == nil {
		return actual == nil && expected == nil
	}
	return actual.ID == expected.ID && actual.SHA256 == expected.SHA256
}

func fileStateMatches(actual, expected publicationFileState) bool {
	if actual.exists != expected.exists {
		return false
	}
	return !actual.exists || actual.sha256 == expected.sha256
}

func (publisher *Publisher) reloadWithFallback(ctx context.Context) error {
	if err := publisher.controller.Reload(ctx, publisher.options.ConfigPath); err == nil {
		return nil
	}
	if err := publisher.process.Restart(ctx); err != nil {
		return errors.New("controller reload and systemd restart both failed")
	}
	return nil
}

func (publisher *Publisher) waitHealthy(ctx context.Context) error {
	healthContext, cancel := context.WithTimeout(ctx, publisher.options.HealthTimeout)
	defer cancel()
	for {
		active, processErr := publisher.process.IsActive(healthContext)
		_, controllerErr := publisher.controller.Version(healthContext)
		if processErr == nil && active && controllerErr == nil {
			return nil
		}
		timer := time.NewTimer(publisher.options.HealthInterval)
		select {
		case <-healthContext.Done():
			timer.Stop()
			return errors.New("mihomo did not become healthy before the deadline")
		case <-timer.C:
		}
	}
}

func (publisher *Publisher) restorePrevious(
	ctx context.Context,
	revisionID string,
	previousConfig []byte,
	previousExists bool,
) error {
	if !previousExists {
		if err := removeAndSync(publisher.options.ConfigPath); err != nil {
			return err
		}
		if err := publisher.process.Stop(ctx); err != nil {
			return errors.New("stop Mihomo after removing initial failed configuration")
		}
		return nil
	}
	recoveryPath := filepath.Join(
		filepath.Dir(publisher.options.ConfigPath),
		".m-ui-recovery-"+revisionID+".yaml",
	)
	defer func() {
		_ = os.Remove(recoveryPath)
	}()
	if err := writeSharedConfigSynced(recoveryPath, previousConfig); err != nil {
		return err
	}
	if err := replaceAndSync(recoveryPath, publisher.options.ConfigPath); err != nil {
		return err
	}
	if err := publisher.reloadWithFallback(ctx); err != nil {
		return err
	}
	return publisher.waitHealthy(ctx)
}

func (publisher *Publisher) recoveryContext(
	ctx context.Context,
) (context.Context, context.CancelFunc) {
	timeout := 45*time.Second + publisher.options.HealthTimeout
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

func (publisher *Publisher) insertSuccessAudit(
	ctx context.Context,
	transaction store.PublicationTransaction,
	request Request,
	now time.Time,
) error {
	if request.AuditAction == "" {
		return nil
	}
	summary := request.AuditSummary
	if request.AuditSummaryFunc != nil {
		summary = request.AuditSummaryFunc()
	}
	auditID, err := uuid.NewRandom()
	if err != nil {
		return errors.New("generate audit ID")
	}
	return transaction.InsertAudit(ctx, store.AuditEntry{
		ID:              auditID.String(),
		ActorAdminID:    request.ActorAdminID,
		Action:          request.AuditAction,
		ResourceType:    request.AuditResource,
		ResourceID:      request.AuditResourceID,
		Result:          "success",
		SummaryRedacted: redact.Text(summary),
		CreatedAt:       now,
	})
}

func (publisher *Publisher) prune(ctx context.Context) error {
	revisions, err := publisher.repository.InactiveRevisionsBeyond(
		ctx,
		publisher.options.HistoryLimit,
	)
	if err != nil {
		return err
	}
	for _, revision := range revisions {
		if revision.Status == domain.RevisionActive {
			return errors.New("revision maintenance selected the active revision")
		}
		paths := []string{revision.FilePath, revision.StateFilePath}
		for _, path := range paths {
			if !pathWithin(publisher.options.RevisionDirectory, path) {
				return errors.New("revision maintenance path is outside the revision directory")
			}
		}
		for _, path := range paths {
			if err := removeAndSync(path); err != nil {
				return errors.New("remove expired revision file")
			}
		}
		if err := publisher.repository.DeleteRevision(ctx, revision.ID); err != nil {
			return err
		}
	}
	return nil
}
