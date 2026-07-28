package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Admin struct {
	ID                string
	Username          string
	PasswordHash      string
	PasswordChangedAt time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Session struct {
	ID               string
	AdminUserID      string
	SessionTokenHash string
	CSRFTokenHash    string
	ExpiresAt        time.Time
	LastSeenAt       time.Time
	CreatedAt        time.Time
	IPHash           string
	UserAgent        string
}

type AuthSession struct {
	Session Session
	Admin   Admin
}

func (s *Store) AdminByUsername(
	ctx context.Context,
	username string,
) (Admin, error) {
	return scanAdmin(s.db.QueryRowContext(
		ctx,
		`SELECT id, username, password_hash, password_changed_at,
		        created_at, updated_at
		   FROM admin_users
		  WHERE username = ?`,
		username,
	))
}

func (s *Store) AdminByID(ctx context.Context, id string) (Admin, error) {
	return scanAdmin(s.db.QueryRowContext(
		ctx,
		`SELECT id, username, password_hash, password_changed_at,
		        created_at, updated_at
		   FROM admin_users
		  WHERE id = ?`,
		id,
	))
}

func scanAdmin(row *sql.Row) (Admin, error) {
	var admin Admin
	var changedAt, createdAt, updatedAt string
	if err := row.Scan(
		&admin.ID,
		&admin.Username,
		&admin.PasswordHash,
		&changedAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Admin{}, ErrNotFound
		}
		return Admin{}, fmt.Errorf("scan administrator: %w", err)
	}
	var err error
	if admin.PasswordChangedAt, err = parseTime(changedAt); err != nil {
		return Admin{}, err
	}
	if admin.CreatedAt, err = parseTime(createdAt); err != nil {
		return Admin{}, err
	}
	if admin.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Admin{}, err
	}
	return admin, nil
}

// ResetAdminPassword creates the first administrator or updates the matching
// existing administrator. v0.1 rejects creation of a second administrator.
func (s *Store) ResetAdminPassword(
	ctx context.Context,
	id string,
	username string,
	passwordHash string,
	now time.Time,
) (Admin, bool, error) {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Admin{}, false, fmt.Errorf("begin administrator reset: %w", err)
	}
	defer func() {
		_ = transaction.Rollback()
	}()

	var existingID, createdAt string
	err = transaction.QueryRowContext(
		ctx,
		"SELECT id, created_at FROM admin_users WHERE username = ?",
		username,
	).Scan(&existingID, &createdAt)

	created := false
	nowText := formatTime(now)
	switch {
	case err == nil:
		if _, err := transaction.ExecContext(
			ctx,
			`UPDATE admin_users
			    SET password_hash = ?, password_changed_at = ?, updated_at = ?
			  WHERE id = ?`,
			passwordHash,
			nowText,
			nowText,
			existingID,
		); err != nil {
			return Admin{}, false, fmt.Errorf("update administrator password: %w", err)
		}
		id = existingID
	case errors.Is(err, sql.ErrNoRows):
		var count int
		if err := transaction.QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM admin_users",
		).Scan(&count); err != nil {
			return Admin{}, false, fmt.Errorf("count administrators: %w", err)
		}
		if count != 0 {
			return Admin{}, false, ErrSingleAdminConflict
		}
		if _, err := transaction.ExecContext(
			ctx,
			`INSERT INTO admin_users(
				id, username, password_hash, password_changed_at,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?)`,
			id,
			username,
			passwordHash,
			nowText,
			nowText,
			nowText,
		); err != nil {
			return Admin{}, false, fmt.Errorf("create administrator: %w", err)
		}
		created = true
	default:
		return Admin{}, false, fmt.Errorf("find administrator: %w", err)
	}

	if _, err := transaction.ExecContext(
		ctx,
		"DELETE FROM sessions WHERE admin_user_id = ?",
		id,
	); err != nil {
		return Admin{}, false, fmt.Errorf("revoke administrator sessions: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return Admin{}, false, fmt.Errorf("commit administrator reset: %w", err)
	}
	admin, err := s.AdminByID(ctx, id)
	return admin, created, err
}

func (s *Store) UpdateAdminPassword(
	ctx context.Context,
	adminID string,
	passwordHash string,
	now time.Time,
) error {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password update: %w", err)
	}
	defer func() {
		_ = transaction.Rollback()
	}()

	result, err := transaction.ExecContext(
		ctx,
		`UPDATE admin_users
		    SET password_hash = ?, password_changed_at = ?, updated_at = ?
		  WHERE id = ?`,
		passwordHash,
		formatTime(now),
		formatTime(now),
		adminID,
	)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read password update result: %w", err)
	}
	if affected != 1 {
		return ErrNotFound
	}
	if _, err := transaction.ExecContext(
		ctx,
		"DELETE FROM sessions WHERE admin_user_id = ?",
		adminID,
	); err != nil {
		return fmt.Errorf("revoke sessions after password update: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit password update: %w", err)
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, session Session) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO sessions(
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
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) AuthSessionByTokenHash(
	ctx context.Context,
	tokenHash string,
	now time.Time,
) (AuthSession, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT
			s.id, s.admin_user_id, s.session_token_hash, s.csrf_token_hash,
			s.expires_at, s.last_seen_at, s.created_at,
			COALESCE(s.ip_hash, ''), s.user_agent,
			a.id, a.username, a.password_hash, a.password_changed_at,
			a.created_at, a.updated_at
		FROM sessions s
		JOIN admin_users a ON a.id = s.admin_user_id
		WHERE s.session_token_hash = ? AND s.expires_at > ?`,
		tokenHash,
		formatTime(now),
	)

	var result AuthSession
	var sessionExpires, sessionSeen, sessionCreated string
	var passwordChanged, adminCreated, adminUpdated string
	if err := row.Scan(
		&result.Session.ID,
		&result.Session.AdminUserID,
		&result.Session.SessionTokenHash,
		&result.Session.CSRFTokenHash,
		&sessionExpires,
		&sessionSeen,
		&sessionCreated,
		&result.Session.IPHash,
		&result.Session.UserAgent,
		&result.Admin.ID,
		&result.Admin.Username,
		&result.Admin.PasswordHash,
		&passwordChanged,
		&adminCreated,
		&adminUpdated,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuthSession{}, ErrNotFound
		}
		return AuthSession{}, fmt.Errorf("scan authenticated session: %w", err)
	}
	var err error
	if result.Session.ExpiresAt, err = parseTime(sessionExpires); err != nil {
		return AuthSession{}, err
	}
	if result.Session.LastSeenAt, err = parseTime(sessionSeen); err != nil {
		return AuthSession{}, err
	}
	if result.Session.CreatedAt, err = parseTime(sessionCreated); err != nil {
		return AuthSession{}, err
	}
	if result.Admin.PasswordChangedAt, err = parseTime(passwordChanged); err != nil {
		return AuthSession{}, err
	}
	if result.Admin.CreatedAt, err = parseTime(adminCreated); err != nil {
		return AuthSession{}, err
	}
	if result.Admin.UpdatedAt, err = parseTime(adminUpdated); err != nil {
		return AuthSession{}, err
	}
	return result, nil
}

func (s *Store) TouchSession(
	ctx context.Context,
	id string,
	lastSeen time.Time,
) error {
	_, err := s.db.ExecContext(
		ctx,
		"UPDATE sessions SET last_seen_at = ? WHERE id = ?",
		formatTime(lastSeen),
		id,
	)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(
		ctx,
		"DELETE FROM sessions WHERE id = ?",
		id,
	); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Store) DeleteExpiredSessions(
	ctx context.Context,
	now time.Time,
) error {
	if _, err := s.db.ExecContext(
		ctx,
		"DELETE FROM sessions WHERE expires_at <= ?",
		formatTime(now),
	); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}
