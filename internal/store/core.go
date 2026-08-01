package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Aethersailor/m-ui/internal/core"
	"github.com/Aethersailor/m-ui/internal/redact"
)

func (managed *ManagedStore) EnsureCoreSettings(
	ctx context.Context,
	settings core.Settings,
	now time.Time,
) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	_, err := managed.store.db.ExecContext(
		ctx,
		`INSERT INTO core_settings(
			id, channel, auto_update, check_interval_seconds,
			managed, external_path, updated_at
		) VALUES (1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		settings.Channel,
		boolInt(settings.AutoUpdate),
		int64(settings.CheckInterval/time.Second),
		boolInt(settings.Managed),
		settings.ExternalPath,
		formatTime(now),
	)
	if err != nil {
		return fmt.Errorf("seed core settings: %w", err)
	}
	return nil
}

func (managed *ManagedStore) CoreSettings(
	ctx context.Context,
) (core.Settings, error) {
	var channel, externalPath string
	var autoUpdate, isManaged int
	var intervalSeconds int64
	err := managed.store.db.QueryRowContext(
		ctx,
		`SELECT channel, auto_update, check_interval_seconds,
		        managed, external_path
		   FROM core_settings
		  WHERE id = 1`,
	).Scan(
		&channel,
		&autoUpdate,
		&intervalSeconds,
		&isManaged,
		&externalPath,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Settings{}, errors.New("core settings are not initialized")
	}
	if err != nil {
		return core.Settings{}, fmt.Errorf("read core settings: %w", err)
	}
	settings := core.Settings{
		Channel:       core.Channel(channel),
		AutoUpdate:    autoUpdate == 1,
		CheckInterval: time.Duration(intervalSeconds) * time.Second,
		Managed:       isManaged == 1,
		ExternalPath:  externalPath,
	}
	if err := settings.Validate(); err != nil {
		return core.Settings{}, fmt.Errorf("validate stored core settings: %w", err)
	}
	return settings, nil
}

func (managed *ManagedStore) UpdateCoreSettings(
	ctx context.Context,
	settings core.Settings,
	now time.Time,
) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	result, err := managed.store.db.ExecContext(
		ctx,
		`UPDATE core_settings
		    SET channel = ?,
		        auto_update = ?,
		        check_interval_seconds = ?,
		        managed = ?,
		        external_path = ?,
		        updated_at = ?
		  WHERE id = 1`,
		settings.Channel,
		boolInt(settings.AutoUpdate),
		int64(settings.CheckInterval/time.Second),
		boolInt(settings.Managed),
		settings.ExternalPath,
		formatTime(now),
	)
	if err != nil {
		return fmt.Errorf("update core settings: %w", err)
	}
	if count, countErr := result.RowsAffected(); countErr != nil || count != 1 {
		return errors.New("core settings are not initialized")
	}
	return nil
}

// UpdateCoreSettingsAndState commits the user-facing core settings together
// with the scheduler's next check in one SQLite transaction.  This prevents a
// settings write from advertising one interval while the scheduler still has
// a stale durable deadline (or vice versa).
func (managed *ManagedStore) UpdateCoreSettingsAndState(
	ctx context.Context,
	settings core.Settings,
	state core.State,
	now time.Time,
) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	currentJSON, err := marshalOptional(state.Current)
	if err != nil {
		return errors.New("encode current core manifest")
	}
	availableJSON, err := marshalOptional(state.Available)
	if err != nil {
		return errors.New("encode available core identity")
	}
	tx, err := managed.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin core settings transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(
		ctx,
		`UPDATE core_settings
		    SET channel = ?,
		        auto_update = ?,
		        check_interval_seconds = ?,
		        managed = ?,
		        external_path = ?,
		        updated_at = ?
		  WHERE id = 1`,
		settings.Channel,
		boolInt(settings.AutoUpdate),
		int64(settings.CheckInterval/time.Second),
		boolInt(settings.Managed),
		settings.ExternalPath,
		formatTime(now),
	)
	if err != nil {
		return fmt.Errorf("update core settings: %w", err)
	}
	if count, countErr := result.RowsAffected(); countErr != nil || count != 1 {
		return errors.New("core settings are not initialized")
	}
	result, err = tx.ExecContext(
		ctx,
		`UPDATE core_state
		    SET current_manifest_json = ?,
		        available_identity_json = ?,
		        last_check_at = ?,
		        last_check_result = ?,
		        last_update_at = ?,
		        last_update_result = ?,
		        last_error_redacted = ?,
		        next_check_at = ?,
		        update_in_progress = ?
		  WHERE id = 1`,
		currentJSON,
		availableJSON,
		nullableCoreTime(state.LastCheckAt),
		state.LastCheckResult,
		nullableCoreTime(state.LastUpdateAt),
		state.LastUpdateResult,
		redact.Text(state.LastErrorRedacted),
		nullableCoreTime(state.NextCheckAt),
		boolInt(state.UpdateInProgress),
	)
	if err != nil {
		return fmt.Errorf("save core state: %w", err)
	}
	if count, countErr := result.RowsAffected(); countErr != nil || count != 1 {
		return errors.New("core state is not initialized")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit core settings transaction: %w", err)
	}
	return nil
}

func (managed *ManagedStore) CoreState(ctx context.Context) (core.State, error) {
	var currentJSON, availableJSON sql.NullString
	var lastCheckAt, lastUpdateAt, nextCheckAt sql.NullString
	var state core.State
	var updateInProgress int
	err := managed.store.db.QueryRowContext(
		ctx,
		`SELECT current_manifest_json, available_identity_json,
		        last_check_at, last_check_result,
		        last_update_at, last_update_result,
		        last_error_redacted, next_check_at, update_in_progress
		   FROM core_state
		  WHERE id = 1`,
	).Scan(
		&currentJSON,
		&availableJSON,
		&lastCheckAt,
		&state.LastCheckResult,
		&lastUpdateAt,
		&state.LastUpdateResult,
		&state.LastErrorRedacted,
		&nextCheckAt,
		&updateInProgress,
	)
	if err != nil {
		return core.State{}, fmt.Errorf("read core state: %w", err)
	}
	if currentJSON.Valid {
		var manifest core.Manifest
		if err := json.Unmarshal([]byte(currentJSON.String), &manifest); err != nil {
			return core.State{}, errors.New("stored core manifest is invalid")
		}
		state.Current = &manifest
	}
	if availableJSON.Valid {
		var identity core.ReleaseIdentity
		if err := json.Unmarshal([]byte(availableJSON.String), &identity); err != nil {
			return core.State{}, errors.New("stored available core identity is invalid")
		}
		state.Available = &identity
	}
	if state.LastCheckAt, err = parseOptionalTime(lastCheckAt); err != nil {
		return core.State{}, err
	}
	if state.LastUpdateAt, err = parseOptionalTime(lastUpdateAt); err != nil {
		return core.State{}, err
	}
	if state.NextCheckAt, err = parseOptionalTime(nextCheckAt); err != nil {
		return core.State{}, err
	}
	state.UpdateInProgress = updateInProgress == 1
	return state, nil
}

func (managed *ManagedStore) SaveCoreState(
	ctx context.Context,
	state core.State,
) error {
	currentJSON, err := marshalOptional(state.Current)
	if err != nil {
		return errors.New("encode current core manifest")
	}
	availableJSON, err := marshalOptional(state.Available)
	if err != nil {
		return errors.New("encode available core identity")
	}
	_, err = managed.store.db.ExecContext(
		ctx,
		`UPDATE core_state
		    SET current_manifest_json = ?,
		        available_identity_json = ?,
		        last_check_at = ?,
		        last_check_result = ?,
		        last_update_at = ?,
		        last_update_result = ?,
		        last_error_redacted = ?,
		        next_check_at = ?,
		        update_in_progress = ?
		  WHERE id = 1`,
		currentJSON,
		availableJSON,
		nullableCoreTime(state.LastCheckAt),
		state.LastCheckResult,
		nullableCoreTime(state.LastUpdateAt),
		state.LastUpdateResult,
		redact.Text(state.LastErrorRedacted),
		nullableCoreTime(state.NextCheckAt),
		boolInt(state.UpdateInProgress),
	)
	if err != nil {
		return fmt.Errorf("save core state: %w", err)
	}
	return nil
}

func (managed *ManagedStore) RecordCoreAudit(
	ctx context.Context,
	actorAdminID, action, result, summary string,
	now time.Time,
) error {
	return managed.RecordAudit(ctx, AuditEntry{
		ID:              uuid.NewString(),
		ActorAdminID:    actorAdminID,
		Action:          action,
		ResourceType:    "mihomo_core",
		Result:          result,
		SummaryRedacted: redact.Text(summary),
		CreatedAt:       now.UTC(),
	})
}

func (managed *ManagedStore) CoreSystemDegraded(
	ctx context.Context,
) (bool, error) {
	state, err := managed.SystemState(ctx)
	if err != nil {
		return false, err
	}
	return state.Degraded, nil
}

func parseOptionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func nullableCoreTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func marshalOptional[T any](value *T) (any, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return string(encoded), nil
}
