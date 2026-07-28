package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
}

type Publisher struct {
	repository store.PublicationRepository
	compiler   Compiler
	cli        mihomo.CoreCLI
	controller mihomo.CoreController
	process    mihomo.CoreProcess
	options    Options
	now        func() time.Time
	mutex      sync.Mutex
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
		return nil, errors.New("Mihomo CLI is required")
	case controller == nil:
		return nil, errors.New("Mihomo Controller is required")
	case process == nil:
		return nil, errors.New("Mihomo process adapter is required")
	case !filepath.IsAbs(options.ConfigPath):
		return nil, errors.New("Mihomo configuration path must be absolute")
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
	return &Publisher{
		repository: repository,
		compiler:   compiler,
		cli:        cli,
		controller: controller,
		process:    process,
		options:    options,
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
	defer transaction.Rollback(context.Background())
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
	defer transaction.Rollback(context.Background())
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
	defer os.Remove(candidatePath)
	if err := writeExclusiveSynced(candidatePath, compiled); err != nil {
		return nil, err
	}
	if err := publisher.cli.Validate(ctx, candidatePath); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCandidateValidation, err)
	}
	return compiled, nil
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
	content, exists, err := readManagedFile(revision.StateFilePath)
	if err != nil {
		return domain.Revision{}, fmt.Errorf("read rollback state: %w", err)
	}
	if !exists {
		return domain.Revision{}, errors.New("rollback state snapshot is missing")
	}
	var snapshot domain.StateSnapshot
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil || snapshot.Version != 1 {
		return domain.Revision{}, errors.New("rollback state snapshot is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return domain.Revision{}, errors.New("rollback state snapshot is invalid")
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
	if err := request.Mutate(ctx, transaction); err != nil {
		return domain.Revision{}, fmt.Errorf("apply managed-state mutation: %w", err)
	}
	now := publisher.now().UTC()
	effectiveAt := now
	if request.EffectiveAt != nil {
		effectiveAt = request.EffectiveAt.UTC()
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
	defer os.Remove(candidatePath)
	if err := writeExclusiveSynced(candidatePath, compiled); err != nil {
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
	backupPath := filepath.Join(
		configDirectory,
		".m-ui-rollback-"+revision.ID+".yaml",
	)
	defer os.Remove(backupPath)
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
		return domain.Revision{}, publisher.failAfterCommit(
			ctx,
			revision,
			previousConfig,
			previousExists,
			err,
		)
	}
	transactionOpen = false
	revision.Status = domain.RevisionActive
	revision.ActivatedAt = &activatedAt
	if err := publisher.prune(ctx); err != nil {
		return revision, fmt.Errorf("publication succeeded but revision cleanup failed: %w", err)
	}
	return revision, nil
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
	recoveryContext, cancel := publisher.recoveryContext()
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

func (publisher *Publisher) failAfterCommit(
	ctx context.Context,
	revision domain.Revision,
	previousConfig []byte,
	previousExists bool,
	cause error,
) error {
	recoveryContext, cancel := publisher.recoveryContext()
	defer cancel()
	recoveryErr := publisher.restorePrevious(
		recoveryContext,
		revision.ID,
		previousConfig,
		previousExists,
	)
	failureErr := publisher.recordFailure(
		ctx,
		revision,
		domain.DesiredState{},
		nil,
		"database commit failed",
		cause,
	)
	if recoveryErr != nil {
		degradedErr := publisher.repository.MarkDegraded(
			context.Background(),
			"database commit and automatic configuration recovery failed; restore the indicated revision manually",
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
	recordErr := publisher.repository.RecordFailedRevision(
		context.WithoutCancel(ctx),
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

func (publisher *Publisher) reloadWithFallback(ctx context.Context) error {
	if err := publisher.controller.Reload(ctx, publisher.options.ConfigPath); err == nil {
		return nil
	}
	if err := publisher.process.Restart(ctx); err != nil {
		return errors.New("Controller reload and systemd restart both failed")
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
			return errors.New("Mihomo did not become healthy before the deadline")
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
	defer os.Remove(recoveryPath)
	if err := writeExclusiveSynced(recoveryPath, previousConfig); err != nil {
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

func (publisher *Publisher) recoveryContext() (context.Context, context.CancelFunc) {
	timeout := 45*time.Second + publisher.options.HealthTimeout
	return context.WithTimeout(context.Background(), timeout)
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
		for _, path := range []string{revision.FilePath, revision.StateFilePath} {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return errors.New("remove expired revision file")
			}
		}
		if err := publisher.repository.DeleteRevision(ctx, revision.ID); err != nil {
			return err
		}
	}
	return nil
}
