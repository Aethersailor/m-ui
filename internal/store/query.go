package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Aethersailor/m-ui/internal/domain"
)

func (managed *ManagedStore) Revisions(
	ctx context.Context,
	limit, offset int,
) ([]domain.Revision, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, errors.New("revision pagination is invalid")
	}
	rows, err := managed.store.db.QueryContext(
		ctx,
		`SELECT id, revision_number, sha256, file_path, state_file_path,
		        status, reason, COALESCE(actor_admin_id, ''),
		        COALESCE(error_message_redacted, ''), created_at, activated_at
		   FROM config_revisions
		  ORDER BY revision_number DESC
		  LIMIT ? OFFSET ?`,
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("query configuration revisions: %w", err)
	}
	defer rows.Close()
	var revisions []domain.Revision
	for rows.Next() {
		revision, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate configuration revisions: %w", err)
	}
	return revisions, nil
}

func (managed *ManagedStore) LatestActiveRevision(
	ctx context.Context,
) (domain.Revision, error) {
	return scanRevision(managed.store.db.QueryRowContext(
		ctx,
		`SELECT id, revision_number, sha256, file_path, state_file_path,
		        status, reason, COALESCE(actor_admin_id, ''),
		        COALESCE(error_message_redacted, ''), created_at, activated_at
		   FROM config_revisions
		  WHERE status = ?
		  ORDER BY revision_number DESC
		  LIMIT 1`,
		domain.RevisionActive,
	))
}

func (managed *ManagedStore) AuditEntries(
	ctx context.Context,
	limit, offset int,
) ([]AuditEntry, error) {
	if limit < 1 || limit > 200 || offset < 0 {
		return nil, errors.New("audit pagination is invalid")
	}
	rows, err := managed.store.db.QueryContext(
		ctx,
		`SELECT id, COALESCE(actor_admin_id, ''), action, resource_type,
		        COALESCE(resource_id, ''), result, summary_redacted, created_at
		   FROM audit_logs
		  ORDER BY created_at DESC, id DESC
		  LIMIT ? OFFSET ?`,
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("query audit entries: %w", err)
	}
	defer rows.Close()
	var entries []AuditEntry
	for rows.Next() {
		entry, err := scanAuditEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit entries: %w", err)
	}
	return entries, nil
}

func (managed *ManagedStore) RecordAudit(
	ctx context.Context,
	entry AuditEntry,
) error {
	return managed.store.InsertAudit(ctx, entry)
}

func scanAuditEntry(row rowScanner) (AuditEntry, error) {
	var entry AuditEntry
	var createdAt string
	if err := row.Scan(
		&entry.ID,
		&entry.ActorAdminID,
		&entry.Action,
		&entry.ResourceType,
		&entry.ResourceID,
		&entry.Result,
		&entry.SummaryRedacted,
		&createdAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuditEntry{}, ErrNotFound
		}
		return AuditEntry{}, fmt.Errorf("scan audit entry: %w", err)
	}
	var err error
	entry.CreatedAt, err = parseTime(createdAt)
	return entry, err
}
