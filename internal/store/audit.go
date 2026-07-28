package store

import (
	"context"
	"fmt"
	"time"
)

type AuditEntry struct {
	ID              string
	ActorAdminID    string
	Action          string
	ResourceType    string
	ResourceID      string
	Result          string
	SummaryRedacted string
	CreatedAt       time.Time
}

func (s *Store) InsertAudit(ctx context.Context, entry AuditEntry) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO audit_logs(
			id, actor_admin_id, action, resource_type, resource_id,
			result, summary_redacted, created_at
		) VALUES (?, NULLIF(?, ''), ?, ?, NULLIF(?, ''), ?, ?, ?)`,
		entry.ID,
		entry.ActorAdminID,
		entry.Action,
		entry.ResourceType,
		entry.ResourceID,
		entry.Result,
		entry.SummaryRedacted,
		formatTime(entry.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert audit entry: %w", err)
	}
	return nil
}
