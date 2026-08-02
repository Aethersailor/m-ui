package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// BootstrapState is the durable state of the first-administrator capability.
// The token itself is never stored in plaintext; TokenCiphertext is sealed by
// the application before it reaches the store and TokenHash is used only for
// one-time request verification.
type BootstrapState struct {
	Required        bool
	TokenHash       string
	TokenCiphertext string
	CreatedAt       time.Time
	RotatedAt       *time.Time
	ConsumedAt      *time.Time
}

type BootstrapSeed struct {
	TokenHash       string
	TokenCiphertext string
	CreatedAt       time.Time
}

type BootstrapCompletion struct {
	Admin   Admin
	Session Session
	Audit   AuditEntry
}

func (s *Store) AdminCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM admin_users",
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count administrators: %w", err)
	}
	return count, nil
}

func (s *Store) BootstrapState(ctx context.Context) (BootstrapState, error) {
	count, err := s.AdminCount(ctx)
	if err != nil {
		return BootstrapState{}, err
	}
	if count > 1 {
		return BootstrapState{}, ErrMultipleAdministrators
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT token_hash, token_ciphertext, created_at, rotated_at, consumed_at
		  FROM bootstrap_state
		 WHERE id = 1`)
	state, err := scanBootstrapState(row)
	if err != nil {
		return BootstrapState{}, err
	}
	state.Required = count == 0 && state.ConsumedAt == nil
	if state.Required && (state.TokenHash == "" || state.TokenCiphertext == "") {
		return BootstrapState{}, ErrBootstrapUnavailable
	}
	return state, nil
}

func scanBootstrapState(row *sql.Row) (BootstrapState, error) {
	var state BootstrapState
	var createdAt string
	var rotatedAt, consumedAt sql.NullString
	if err := row.Scan(
		&state.TokenHash,
		&state.TokenCiphertext,
		&createdAt,
		&rotatedAt,
		&consumedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BootstrapState{}, ErrNotFound
		}
		return BootstrapState{}, fmt.Errorf("scan bootstrap state: %w", err)
	}
	var err error
	if state.CreatedAt, err = parseTime(createdAt); err != nil {
		return BootstrapState{}, err
	}
	if state.RotatedAt, err = parseBootstrapOptionalTime(rotatedAt); err != nil {
		return BootstrapState{}, err
	}
	if state.ConsumedAt, err = parseBootstrapOptionalTime(consumedAt); err != nil {
		return BootstrapState{}, err
	}
	return state, nil
}

func parseBootstrapOptionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

// EnsureBootstrap creates the encrypted capability exactly once for an empty
// database. If an administrator already exists, it seals the bootstrap
// state permanently without creating or retaining a token.
func (s *Store) EnsureBootstrap(
	ctx context.Context,
	seed BootstrapSeed,
	now time.Time,
) error {
	conn, err := s.beginImmediate(ctx)
	if err != nil {
		return fmt.Errorf("begin bootstrap initialization: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()
	rollback := func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }
	defer rollback()

	count, err := adminCountConn(ctx, conn)
	if err != nil {
		return err
	}
	if count > 1 {
		return ErrMultipleAdministrators
	}
	state, stateErr := scanBootstrapState(conn.QueryRowContext(ctx, `
		SELECT token_hash, token_ciphertext, created_at, rotated_at, consumed_at
		  FROM bootstrap_state
		 WHERE id = 1`))
	if stateErr != nil && !errors.Is(stateErr, ErrNotFound) {
		return stateErr
	}

	if count == 1 {
		if errors.Is(stateErr, ErrNotFound) {
			_, err = conn.ExecContext(ctx, `
				INSERT INTO bootstrap_state(
					id, token_hash, token_ciphertext, created_at, consumed_at
				) VALUES (1, '', '', ?, ?)`, formatTime(now), formatTime(now))
		} else {
			_, err = conn.ExecContext(ctx, `
				UPDATE bootstrap_state
				   SET token_hash = '', token_ciphertext = '', consumed_at = COALESCE(consumed_at, ?)
				 WHERE id = 1`, formatTime(now))
		}
		if err != nil {
			return fmt.Errorf("mark administrator bootstrap complete: %w", err)
		}
	} else {
		if errors.Is(stateErr, ErrNotFound) {
			if seed.TokenHash == "" || seed.TokenCiphertext == "" {
				return ErrBootstrapUnavailable
			}
			createdAt := seed.CreatedAt
			if createdAt.IsZero() {
				createdAt = now
			}
			if _, err := conn.ExecContext(ctx, `
				INSERT INTO bootstrap_state(
					id, token_hash, token_ciphertext, created_at
				) VALUES (1, ?, ?, ?)`,
				seed.TokenHash,
				seed.TokenCiphertext,
				formatTime(createdAt),
			); err != nil {
				return fmt.Errorf("create administrator bootstrap state: %w", err)
			}
		} else if state.ConsumedAt != nil {
			return ErrBootstrapUnavailable
		} else if state.TokenHash == "" || state.TokenCiphertext == "" {
			return ErrBootstrapUnavailable
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit bootstrap initialization: %w", err)
	}
	return nil
}

func (s *Store) RotateBootstrap(
	ctx context.Context,
	seed BootstrapSeed,
	now time.Time,
) error {
	if seed.TokenHash == "" || seed.TokenCiphertext == "" {
		return ErrBootstrapUnavailable
	}
	conn, err := s.beginImmediate(ctx)
	if err != nil {
		return fmt.Errorf("begin bootstrap rotation: %w", err)
	}
	defer func() { _ = conn.Close() }()
	defer func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }()

	count, err := adminCountConn(ctx, conn)
	if err != nil {
		return err
	}
	if count != 0 {
		return ErrBootstrapCompleted
	}
	state, err := scanBootstrapState(conn.QueryRowContext(ctx, `
		SELECT token_hash, token_ciphertext, created_at, rotated_at, consumed_at
		  FROM bootstrap_state
		 WHERE id = 1`))
	if errors.Is(err, ErrNotFound) {
		return ErrBootstrapUnavailable
	}
	if err != nil {
		return err
	}
	if state.ConsumedAt != nil {
		return ErrBootstrapCompleted
	}
	if _, err := conn.ExecContext(ctx, `
		UPDATE bootstrap_state
		   SET token_hash = ?, token_ciphertext = ?, rotated_at = ?
		 WHERE id = 1 AND consumed_at IS NULL`,
		seed.TokenHash,
		seed.TokenCiphertext,
		formatTime(now),
	); err != nil {
		return fmt.Errorf("rotate administrator bootstrap token: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit bootstrap rotation: %w", err)
	}
	return nil
}

// CompleteBootstrap is the sole first-administrator creation primitive. It
// rechecks every precondition under BEGIN IMMEDIATE and commits the account,
// first session, audit record, and capability consumption together.
func (s *Store) CompleteBootstrap(
	ctx context.Context,
	expectedTokenHash string,
	completion BootstrapCompletion,
	now time.Time,
) error {
	if expectedTokenHash == "" {
		return ErrInvalidBootstrapToken
	}
	conn, err := s.beginImmediate(ctx)
	if err != nil {
		return fmt.Errorf("begin administrator bootstrap: %w", err)
	}
	defer func() { _ = conn.Close() }()
	defer func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }()

	count, err := adminCountConn(ctx, conn)
	if err != nil {
		return err
	}
	if count != 0 {
		return ErrBootstrapCompleted
	}
	state, err := scanBootstrapState(conn.QueryRowContext(ctx, `
		SELECT token_hash, token_ciphertext, created_at, rotated_at, consumed_at
		  FROM bootstrap_state
		 WHERE id = 1`))
	if errors.Is(err, ErrNotFound) {
		return ErrBootstrapUnavailable
	}
	if err != nil {
		return err
	}
	if state.ConsumedAt != nil {
		return ErrBootstrapCompleted
	}
	if state.TokenHash != expectedTokenHash {
		return ErrInvalidBootstrapToken
	}

	admin := completion.Admin
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO admin_users(
			id, username, password_hash, password_changed_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		admin.ID,
		admin.Username,
		admin.PasswordHash,
		formatTime(admin.PasswordChangedAt),
		formatTime(admin.CreatedAt),
		formatTime(admin.UpdatedAt),
	); err != nil {
		return fmt.Errorf("create administrator during bootstrap: %w", err)
	}
	session := completion.Session
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO sessions(
			id, admin_user_id, session_token_hash, csrf_token_hash,
			expires_at, last_seen_at, created_at, ip_hash, user_agent
		) VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)`,
		session.ID,
		session.AdminUserID,
		session.SessionTokenHash,
		session.CSRFTokenHash,
		formatTime(session.ExpiresAt),
		formatTime(session.LastSeenAt),
		formatTime(session.CreatedAt),
		session.IPHash,
		session.UserAgent,
	); err != nil {
		return fmt.Errorf("create session during bootstrap: %w", err)
	}
	audit := completion.Audit
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO audit_logs(
			id, actor_admin_id, action, resource_type, resource_id,
			result, summary_redacted, created_at
		) VALUES (?, NULLIF(?, ''), ?, ?, NULLIF(?, ''), ?, ?, ?)`,
		audit.ID,
		audit.ActorAdminID,
		audit.Action,
		audit.ResourceType,
		audit.ResourceID,
		audit.Result,
		audit.SummaryRedacted,
		formatTime(audit.CreatedAt),
	); err != nil {
		return fmt.Errorf("record administrator bootstrap audit: %w", err)
	}
	result, err := conn.ExecContext(ctx, `
		UPDATE bootstrap_state
		   SET token_hash = '', token_ciphertext = '', consumed_at = ?
		 WHERE id = 1 AND consumed_at IS NULL AND token_hash = ?`,
		formatTime(now),
		expectedTokenHash,
	)
	if err != nil {
		return fmt.Errorf("consume administrator bootstrap token: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read bootstrap token consumption result: %w", err)
	}
	if affected != 1 {
		return ErrBootstrapCompleted
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit administrator bootstrap: %w", err)
	}
	return nil
}

func (s *Store) beginImmediate(ctx context.Context) (*sql.Conn, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func adminCountConn(ctx context.Context, conn *sql.Conn) (int, error) {
	var count int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin_users").Scan(&count); err != nil {
		return 0, fmt.Errorf("count administrators: %w", err)
	}
	if count > 1 {
		return 0, ErrMultipleAdministrators
	}
	return count, nil
}
