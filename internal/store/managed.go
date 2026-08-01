package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/domain"
)

const (
	settingPublicHost        = "public_host"
	settingPanelTitle        = "panel_title"
	settingUILanguage        = "ui_language"
	settingPanelAddress      = "panel_listen_address"
	settingPanelPort         = "panel_listen_port"
	settingTrustedProxies    = "trusted_proxy_cidrs"
	settingMihomoBinary      = "mihomo_binary_path"
	settingMihomoConfigDir   = "mihomo_config_dir"
	settingMihomoConfigPath  = "mihomo_config_path"
	settingControllerAddress = "mihomo_controller_address"
	settingControllerSecret  = "mihomo_controller_secret_ciphertext"
	settingMihomoService     = "mihomo_service_name"
	settingHistoryLimit      = "config_history_limit"

	controllerSecretPurpose = "settings:mihomo_controller_secret"
)

type ManagedStore struct {
	store  *Store
	sealer *muicrypto.Sealer
}

type ManagedTx struct {
	conn   *sql.Conn
	sealer *muicrypto.Sealer
	done   bool
}

type InitialSettings struct {
	PanelTitle         string
	UILanguage         string
	PublicHost         string
	PanelListenAddress string
	PanelListenPort    uint16
	TrustedProxyCIDRs  []string
	MihomoBinaryPath   string
	MihomoConfigDir    string
	MihomoConfigPath   string
	ControllerAddress  string
	BootstrapSecret    string
	MihomoServiceName  string
	HistoryLimit       int
}

type RuntimeSettings struct {
	InitialSettings
	ControllerSecret string
}

type PublicationSnapshot struct {
	State          domain.DesiredState
	ActiveRevision *domain.Revision
}

type PublicationRepository interface {
	BeginImmediate(ctx context.Context) (PublicationTransaction, error)
	ReadPublicationSnapshot(
		ctx context.Context,
		asOf time.Time,
	) (PublicationSnapshot, error)
	SystemState(ctx context.Context) (domain.SystemState, error)
	MarkDegraded(ctx context.Context, reason, revisionID string, now time.Time) error
	ClearDegraded(ctx context.Context, now time.Time) error
	Revision(ctx context.Context, id string) (domain.Revision, error)
	RecordFailedRevision(ctx context.Context, revision domain.Revision) error
	InactiveRevisionsBeyond(ctx context.Context, keep int) ([]domain.Revision, error)
	DeleteRevision(ctx context.Context, id string) error
}

type PublicationTransaction interface {
	DesiredState(ctx context.Context, asOf time.Time) (domain.DesiredState, error)
	ReplaceDesiredState(ctx context.Context, state domain.DesiredState) error
	ActiveRevision(ctx context.Context) (*domain.Revision, error)
	NextRevisionNumber(ctx context.Context) (int64, error)
	InsertRevision(ctx context.Context, revision domain.Revision) error
	ActivateRevision(ctx context.Context, revisionID string, activatedAt time.Time) error
	InsertAudit(ctx context.Context, entry AuditEntry) error
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

func NewManagedStore(store *Store, sealer *muicrypto.Sealer) (*ManagedStore, error) {
	if store == nil {
		return nil, errors.New("store is required")
	}
	if sealer == nil {
		return nil, errors.New("field sealer is required")
	}
	return &ManagedStore{store: store, sealer: sealer}, nil
}

func (managed *ManagedStore) EnsureInitialSettings(
	ctx context.Context,
	settings InitialSettings,
	now time.Time,
) error {
	trustedProxies, err := json.Marshal(settings.TrustedProxyCIDRs)
	if err != nil {
		return errors.New("encode trusted proxy settings")
	}
	transaction, err := managed.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin initial settings transaction: %w", err)
	}
	defer func() {
		_ = transaction.Rollback()
	}()
	displayDefaults := []struct {
		key   string
		value string
	}{
		{settingPanelTitle, settings.PanelTitle},
		{settingUILanguage, settings.UILanguage},
		{settingPublicHost, settings.PublicHost},
	}
	for _, item := range displayDefaults {
		if _, err := transaction.ExecContext(
			ctx,
			`INSERT INTO settings(key, value, updated_at)
			 VALUES (?, ?, ?)
			 ON CONFLICT(key) DO NOTHING`,
			item.key,
			item.value,
			formatTime(now),
		); err != nil {
			return fmt.Errorf("seed managed setting %q: %w", item.key, err)
		}
	}
	advanced := []struct {
		key   string
		value string
	}{
		{settingPanelAddress, settings.PanelListenAddress},
		{settingPanelPort, strconv.Itoa(int(settings.PanelListenPort))},
		{settingTrustedProxies, string(trustedProxies)},
		{settingMihomoConfigDir, settings.MihomoConfigDir},
		{settingMihomoConfigPath, settings.MihomoConfigPath},
		{settingControllerAddress, settings.ControllerAddress},
		{settingMihomoService, settings.MihomoServiceName},
		{settingHistoryLimit, strconv.Itoa(settings.HistoryLimit)},
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO settings(key, value, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(key) DO NOTHING`,
		settingMihomoBinary,
		settings.MihomoBinaryPath,
		formatTime(now),
	); err != nil {
		return fmt.Errorf("seed managed setting %q: %w", settingMihomoBinary, err)
	}
	for _, item := range advanced {
		if _, err := transaction.ExecContext(
			ctx,
			`INSERT INTO settings(key, value, updated_at)
			 VALUES (?, ?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value,
			                                updated_at = excluded.updated_at`,
			item.key,
			item.value,
			formatTime(now),
		); err != nil {
			return fmt.Errorf("store local setting %q: %w", item.key, err)
		}
	}
	var secretCount int
	if err := transaction.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM settings WHERE key = ?",
		settingControllerSecret,
	).Scan(&secretCount); err != nil {
		return fmt.Errorf("check Mihomo Controller secret: %w", err)
	}
	if secretCount == 0 {
		secret := settings.BootstrapSecret
		if secret == "" {
			rawSecret := make([]byte, 32)
			if _, err := io.ReadFull(rand.Reader, rawSecret); err != nil {
				return errors.New("generate Mihomo Controller secret")
			}
			secret = base64.RawURLEncoding.EncodeToString(rawSecret)
		}
		ciphertext, err := managed.sealer.Encrypt(
			[]byte(secret),
			controllerSecretPurpose,
		)
		if err != nil {
			return errors.New("encrypt Mihomo Controller secret")
		}
		if _, err := transaction.ExecContext(
			ctx,
			"INSERT INTO settings(key, value, updated_at) VALUES (?, ?, ?)",
			settingControllerSecret,
			ciphertext,
			formatTime(now),
		); err != nil {
			return fmt.Errorf("store Mihomo Controller secret: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit initial settings: %w", err)
	}
	return nil
}

func (managed *ManagedStore) Settings(ctx context.Context) (RuntimeSettings, error) {
	rows, err := managed.store.db.QueryContext(ctx, "SELECT key, value FROM settings")
	if err != nil {
		return RuntimeSettings{}, fmt.Errorf("query settings: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	values := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return RuntimeSettings{}, fmt.Errorf("scan setting: %w", err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return RuntimeSettings{}, fmt.Errorf("iterate settings: %w", err)
	}
	panelPort, err := strconv.ParseUint(values[settingPanelPort], 10, 16)
	if err != nil || panelPort == 0 {
		return RuntimeSettings{}, errors.New("stored panel port is invalid")
	}
	historyLimit, err := strconv.Atoi(values[settingHistoryLimit])
	if err != nil || historyLimit < 1 {
		return RuntimeSettings{}, errors.New("stored revision history limit is invalid")
	}
	var trustedProxies []string
	if err := json.Unmarshal([]byte(values[settingTrustedProxies]), &trustedProxies); err != nil {
		return RuntimeSettings{}, errors.New("stored trusted proxy list is invalid")
	}
	secret, err := managed.sealer.Decrypt(
		values[settingControllerSecret],
		controllerSecretPurpose,
	)
	if err != nil {
		return RuntimeSettings{}, errors.New("decrypt Mihomo Controller secret")
	}
	return RuntimeSettings{
		InitialSettings: InitialSettings{
			PanelTitle:         values[settingPanelTitle],
			UILanguage:         values[settingUILanguage],
			PublicHost:         values[settingPublicHost],
			PanelListenAddress: values[settingPanelAddress],
			PanelListenPort:    uint16(panelPort),
			TrustedProxyCIDRs:  trustedProxies,
			MihomoBinaryPath:   values[settingMihomoBinary],
			MihomoConfigDir:    values[settingMihomoConfigDir],
			MihomoConfigPath:   values[settingMihomoConfigPath],
			ControllerAddress:  values[settingControllerAddress],
			BootstrapSecret:    "",
			MihomoServiceName:  values[settingMihomoService],
			HistoryLimit:       historyLimit,
		},
		ControllerSecret: string(secret),
	}, nil
}

func (managed *ManagedStore) SetMihomoBinaryPath(
	ctx context.Context,
	binaryPath string,
	now time.Time,
) error {
	if strings.TrimSpace(binaryPath) == "" {
		return errors.New("mihomo binary path is required")
	}
	_, err := managed.store.db.ExecContext(
		ctx,
		`UPDATE settings SET value = ?, updated_at = ?
		  WHERE key = ?`,
		binaryPath,
		formatTime(now),
		settingMihomoBinary,
	)
	if err != nil {
		return fmt.Errorf("update Mihomo binary path: %w", err)
	}
	return nil
}

func (managed *ManagedStore) ReadDesiredState(
	ctx context.Context,
	asOf time.Time,
) (domain.DesiredState, error) {
	snapshot, err := managed.ReadPublicationSnapshot(ctx, asOf)
	if err != nil {
		return domain.DesiredState{}, err
	}
	return snapshot.State, nil
}

func (managed *ManagedStore) ReadPublicationSnapshot(
	ctx context.Context,
	asOf time.Time,
) (PublicationSnapshot, error) {
	conn, err := managed.store.db.Conn(ctx)
	if err != nil {
		return PublicationSnapshot{}, fmt.Errorf(
			"acquire SQLite publication snapshot connection: %w",
			err,
		)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		_ = conn.Close()
		return PublicationSnapshot{}, fmt.Errorf(
			"begin publication snapshot transaction: %w",
			err,
		)
	}
	transaction := &ManagedTx{conn: conn, sealer: managed.sealer}
	defer func() {
		_ = transaction.Rollback(context.Background())
	}()
	state, err := transaction.DesiredState(ctx, asOf.UTC())
	if err != nil {
		return PublicationSnapshot{}, fmt.Errorf(
			"read durable desired state: %w",
			err,
		)
	}
	activeRevision, err := transaction.ActiveRevision(ctx)
	if err != nil {
		return PublicationSnapshot{}, fmt.Errorf(
			"read durable active revision: %w",
			err,
		)
	}
	return PublicationSnapshot{
		State:          state,
		ActiveRevision: activeRevision,
	}, nil
}

func (managed *ManagedStore) BeginImmediate(ctx context.Context) (PublicationTransaction, error) {
	conn, err := managed.store.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire SQLite publication connection: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("begin immediate publication transaction: %w", err)
	}
	return &ManagedTx{conn: conn, sealer: managed.sealer}, nil
}

func (transaction *ManagedTx) DesiredState(
	ctx context.Context,
	asOf time.Time,
) (domain.DesiredState, error) {
	settings, err := transaction.settings(ctx)
	if err != nil {
		return domain.DesiredState{}, err
	}
	secret, err := transaction.sealer.Decrypt(
		settings[settingControllerSecret],
		controllerSecretPurpose,
	)
	if err != nil {
		return domain.DesiredState{}, errors.New("decrypt Mihomo Controller secret")
	}
	state := domain.DesiredState{
		AsOf:              asOf.UTC(),
		PanelTitle:        settings[settingPanelTitle],
		UILanguage:        settings[settingUILanguage],
		ControllerAddress: settings[settingControllerAddress],
		ControllerSecret:  string(secret),
		PublicHost:        settings[settingPublicHost],
	}

	rows, err := transaction.conn.QueryContext(
		ctx,
		`SELECT id, name, enabled, listen_address, listen_port,
		        COALESCE(public_host_override, ''), public_port_override,
		        server_name, reality_dest, reality_private_key_ciphertext,
		        reality_public_key, short_id, udp_enabled, created_at, updated_at
		   FROM listeners
		  ORDER BY name, id`,
	)
	if err != nil {
		return domain.DesiredState{}, fmt.Errorf("query listeners: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var listener domain.Listener
		var enabled, udpEnabled int
		var listenPort int64
		var publicPort sql.NullInt64
		var privateCiphertext, createdAt, updatedAt string
		if err := rows.Scan(
			&listener.ID,
			&listener.Name,
			&enabled,
			&listener.ListenAddress,
			&listenPort,
			&listener.PublicHostOverride,
			&publicPort,
			&listener.ServerName,
			&listener.RealityDest,
			&privateCiphertext,
			&listener.RealityPublicKey,
			&listener.ShortID,
			&udpEnabled,
			&createdAt,
			&updatedAt,
		); err != nil {
			return domain.DesiredState{}, fmt.Errorf("scan listener: %w", err)
		}
		listener.ListenPort = uint16(listenPort)
		listener.Enabled = enabled == 1
		listener.UDPEnabled = udpEnabled == 1
		if publicPort.Valid {
			value := uint16(publicPort.Int64)
			listener.PublicPortOverride = &value
		}
		if listener.CreatedAt, err = parseTime(createdAt); err != nil {
			return domain.DesiredState{}, err
		}
		if listener.UpdatedAt, err = parseTime(updatedAt); err != nil {
			return domain.DesiredState{}, err
		}
		privateKey, err := transaction.sealer.Decrypt(
			privateCiphertext,
			listenerPrivateKeyPurpose(listener.ID),
		)
		if err != nil {
			return domain.DesiredState{}, errors.New("decrypt listener REALITY private key")
		}
		listener.RealityPrivateKey = string(privateKey)
		state.Listeners = append(state.Listeners, listener)
	}
	if err := rows.Err(); err != nil {
		return domain.DesiredState{}, fmt.Errorf("iterate listeners: %w", err)
	}
	if err := transaction.loadUsers(ctx, &state); err != nil {
		return domain.DesiredState{}, err
	}
	return state, nil
}

func (transaction *ManagedTx) ReplaceDesiredState(
	ctx context.Context,
	state domain.DesiredState,
) error {
	if err := state.Validate(); err != nil {
		return fmt.Errorf("validate replacement desired state: %w", err)
	}
	controllerCiphertext, err := transaction.sealer.Encrypt(
		[]byte(state.ControllerSecret),
		controllerSecretPurpose,
	)
	if err != nil {
		return errors.New("encrypt Mihomo Controller secret")
	}
	now := formatTime(state.AsOf)
	panelTitle := state.PanelTitle
	if panelTitle == "" {
		panelTitle = "m-ui"
	}
	uiLanguage := state.UILanguage
	if uiLanguage == "" {
		uiLanguage = "en-US"
	}
	for _, item := range []struct {
		key   string
		value string
	}{
		{settingPanelTitle, panelTitle},
		{settingUILanguage, uiLanguage},
		{settingPublicHost, state.PublicHost},
		{settingControllerAddress, state.ControllerAddress},
		{settingControllerSecret, controllerCiphertext},
	} {
		if _, err := transaction.conn.ExecContext(
			ctx,
			`INSERT INTO settings(key, value, updated_at) VALUES (?, ?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value,
			                                updated_at = excluded.updated_at`,
			item.key,
			item.value,
			now,
		); err != nil {
			return fmt.Errorf("store managed setting %q: %w", item.key, err)
		}
	}
	if _, err := transaction.conn.ExecContext(ctx, "DELETE FROM listeners"); err != nil {
		return fmt.Errorf("replace listeners: %w", err)
	}
	for _, listener := range state.Listeners {
		privateCiphertext, err := transaction.sealer.Encrypt(
			[]byte(listener.RealityPrivateKey),
			listenerPrivateKeyPurpose(listener.ID),
		)
		if err != nil {
			return errors.New("encrypt listener REALITY private key")
		}
		createdAt := listener.CreatedAt
		if createdAt.IsZero() {
			createdAt = state.AsOf
		}
		updatedAt := listener.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = state.AsOf
		}
		if _, err := transaction.conn.ExecContext(
			ctx,
			`INSERT INTO listeners(
				id, name, enabled, listen_address, listen_port,
				public_host_override, public_port_override, server_name,
				reality_dest, reality_private_key_ciphertext, reality_public_key,
				short_id, udp_enabled, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			listener.ID,
			listener.Name,
			boolInt(listener.Enabled),
			listener.ListenAddress,
			listener.ListenPort,
			listener.PublicHostOverride,
			nullablePort(listener.PublicPortOverride),
			listener.ServerName,
			listener.RealityDest,
			privateCiphertext,
			listener.RealityPublicKey,
			listener.ShortID,
			boolInt(listener.UDPEnabled),
			formatTime(createdAt),
			formatTime(updatedAt),
		); err != nil {
			return fmt.Errorf("insert listener: %w", err)
		}
		for _, user := range listener.Users {
			userCreatedAt := user.CreatedAt
			if userCreatedAt.IsZero() {
				userCreatedAt = state.AsOf
			}
			userUpdatedAt := user.UpdatedAt
			if userUpdatedAt.IsZero() {
				userUpdatedAt = state.AsOf
			}
			if _, err := transaction.conn.ExecContext(
				ctx,
				`INSERT INTO listener_users(
					id, listener_id, name, enabled, uuid, expires_at,
					created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				user.ID,
				listener.ID,
				user.Name,
				boolInt(user.Enabled),
				user.UUID,
				nullableTime(user.ExpiresAt),
				formatTime(userCreatedAt),
				formatTime(userUpdatedAt),
			); err != nil {
				return fmt.Errorf("insert listener user: %w", err)
			}
		}
	}
	return nil
}

func (transaction *ManagedTx) NextRevisionNumber(ctx context.Context) (int64, error) {
	var number int64
	if err := transaction.conn.QueryRowContext(
		ctx,
		"SELECT COALESCE(MAX(revision_number), 0) + 1 FROM config_revisions",
	).Scan(&number); err != nil {
		return 0, fmt.Errorf("select next revision number: %w", err)
	}
	return number, nil
}

func (transaction *ManagedTx) ActiveRevision(
	ctx context.Context,
) (*domain.Revision, error) {
	rows, err := transaction.conn.QueryContext(
		ctx,
		`SELECT id, revision_number, sha256, file_path, state_file_path,
		        status, reason, COALESCE(actor_admin_id, ''),
		        COALESCE(error_message_redacted, ''), created_at, activated_at
		   FROM config_revisions
		  WHERE status = ?
		  ORDER BY revision_number DESC
		  LIMIT 2`,
		domain.RevisionActive,
	)
	if err != nil {
		return nil, fmt.Errorf("query active configuration revision: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate active configuration revision: %w", err)
		}
		return nil, nil
	}
	revision, err := scanRevision(rows)
	if err != nil {
		return nil, err
	}
	if rows.Next() {
		return nil, ErrMultipleActiveRevisions
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active configuration revision: %w", err)
	}
	return &revision, nil
}

func (transaction *ManagedTx) InsertRevision(
	ctx context.Context,
	revision domain.Revision,
) error {
	_, err := transaction.conn.ExecContext(
		ctx,
		`INSERT INTO config_revisions(
			id, revision_number, sha256, file_path, state_file_path, status,
			reason, actor_admin_id, error_message_redacted, created_at, activated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?)`,
		revision.ID,
		revision.RevisionNumber,
		revision.SHA256,
		revision.FilePath,
		revision.StateFilePath,
		revision.Status,
		revision.Reason,
		revision.ActorAdminID,
		revision.ErrorMessageRedacted,
		formatTime(revision.CreatedAt),
		nullableTime(revision.ActivatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert configuration revision: %w", err)
	}
	return nil
}

func (transaction *ManagedTx) ActivateRevision(
	ctx context.Context,
	revisionID string,
	activatedAt time.Time,
) error {
	if _, err := transaction.conn.ExecContext(
		ctx,
		`UPDATE config_revisions
		    SET status = ?
		  WHERE status = ?`,
		domain.RevisionRolledBack,
		domain.RevisionActive,
	); err != nil {
		return fmt.Errorf("retire active revision: %w", err)
	}
	result, err := transaction.conn.ExecContext(
		ctx,
		`UPDATE config_revisions
		    SET status = ?, activated_at = ?, error_message_redacted = NULL
		  WHERE id = ? AND status = ?`,
		domain.RevisionActive,
		formatTime(activatedAt),
		revisionID,
		domain.RevisionPending,
	)
	if err != nil {
		return fmt.Errorf("activate configuration revision: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read revision activation result: %w", err)
	}
	if affected != 1 {
		return errors.New("pending configuration revision not found")
	}
	return nil
}

func (transaction *ManagedTx) InsertAudit(ctx context.Context, entry AuditEntry) error {
	_, err := transaction.conn.ExecContext(
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
		return fmt.Errorf("insert publication audit entry: %w", err)
	}
	return nil
}

func (transaction *ManagedTx) Commit(ctx context.Context) error {
	if transaction.done {
		return errors.New("publication transaction is already closed")
	}
	_, err := transaction.conn.ExecContext(ctx, "COMMIT")
	transaction.done = true
	_ = transaction.conn.Close()
	if err != nil {
		return fmt.Errorf("commit publication transaction: %w", err)
	}
	return nil
}

func (transaction *ManagedTx) Rollback(ctx context.Context) error {
	if transaction.done {
		return nil
	}
	_, err := transaction.conn.ExecContext(ctx, "ROLLBACK")
	transaction.done = true
	closeErr := transaction.conn.Close()
	if err != nil {
		return fmt.Errorf("roll back publication transaction: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("release publication connection: %w", closeErr)
	}
	return nil
}

func (managed *ManagedStore) SystemState(ctx context.Context) (domain.SystemState, error) {
	var state domain.SystemState
	var degraded int
	var updatedAt string
	if err := managed.store.db.QueryRowContext(
		ctx,
		`SELECT degraded, degraded_reason, COALESCE(degraded_revision_id, ''), updated_at
		   FROM system_state
		  WHERE id = 1`,
	).Scan(
		&degraded,
		&state.DegradedReason,
		&state.DegradedRevisionID,
		&updatedAt,
	); err != nil {
		return domain.SystemState{}, fmt.Errorf("read system state: %w", err)
	}
	state.Degraded = degraded == 1
	var err error
	state.UpdatedAt, err = parseTime(updatedAt)
	return state, err
}

func (managed *ManagedStore) MarkDegraded(
	ctx context.Context,
	reason, revisionID string,
	now time.Time,
) error {
	if stringsTrimmedEmpty(reason) {
		return errors.New("degraded reason is required")
	}
	_, err := managed.store.db.ExecContext(
		ctx,
		`UPDATE system_state
		    SET degraded = 1, degraded_reason = ?,
		        degraded_revision_id = NULLIF(?, ''), updated_at = ?
		  WHERE id = 1`,
		reason,
		revisionID,
		formatTime(now),
	)
	if err != nil {
		return fmt.Errorf("mark system degraded: %w", err)
	}
	return nil
}

func (managed *ManagedStore) ClearDegraded(ctx context.Context, now time.Time) error {
	_, err := managed.store.db.ExecContext(
		ctx,
		`UPDATE system_state
		    SET degraded = 0, degraded_reason = '',
		        degraded_revision_id = NULL, updated_at = ?
		  WHERE id = 1`,
		formatTime(now),
	)
	if err != nil {
		return fmt.Errorf("clear degraded state: %w", err)
	}
	return nil
}

func (managed *ManagedStore) Revision(
	ctx context.Context,
	id string,
) (domain.Revision, error) {
	return scanRevision(managed.store.db.QueryRowContext(
		ctx,
		`SELECT id, revision_number, sha256, file_path, state_file_path,
		        status, reason, COALESCE(actor_admin_id, ''),
		        COALESCE(error_message_redacted, ''), created_at, activated_at
		   FROM config_revisions
		  WHERE id = ?`,
		id,
	))
}

func (managed *ManagedStore) RecordFailedRevision(
	ctx context.Context,
	revision domain.Revision,
) error {
	revision.Status = domain.RevisionFailed
	_, err := managed.store.db.ExecContext(
		ctx,
		`INSERT INTO config_revisions(
			id, revision_number, sha256, file_path, state_file_path, status,
			reason, actor_admin_id, error_message_redacted, created_at, activated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, NULL)`,
		revision.ID,
		revision.RevisionNumber,
		revision.SHA256,
		revision.FilePath,
		revision.StateFilePath,
		revision.Status,
		revision.Reason,
		revision.ActorAdminID,
		revision.ErrorMessageRedacted,
		formatTime(revision.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("record failed configuration revision: %w", err)
	}
	return nil
}

func (managed *ManagedStore) InactiveRevisionsBeyond(
	ctx context.Context,
	keep int,
) ([]domain.Revision, error) {
	if keep < 1 {
		return nil, errors.New("revision retention must be positive")
	}
	rows, err := managed.store.db.QueryContext(
		ctx,
		`SELECT id, revision_number, sha256, file_path, state_file_path,
		        status, reason, COALESCE(actor_admin_id, ''),
		        COALESCE(error_message_redacted, ''), created_at, activated_at
		   FROM config_revisions
		  WHERE status IN (?, ?)
		  ORDER BY revision_number DESC
		  LIMIT -1 OFFSET ?`,
		domain.RevisionRolledBack,
		domain.RevisionFailed,
		keep,
	)
	if err != nil {
		return nil, fmt.Errorf("query expired revisions: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	var revisions []domain.Revision
	for rows.Next() {
		revision, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

func (managed *ManagedStore) DeleteRevision(ctx context.Context, id string) error {
	result, err := managed.store.db.ExecContext(
		ctx,
		"DELETE FROM config_revisions WHERE id = ? AND status <> ?",
		id,
		domain.RevisionActive,
	)
	if err != nil {
		return fmt.Errorf("delete configuration revision: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read revision deletion result: %w", err)
	}
	if affected != 1 {
		return errors.New("inactive configuration revision not found")
	}
	return nil
}

func (transaction *ManagedTx) settings(ctx context.Context) (map[string]string, error) {
	rows, err := transaction.conn.QueryContext(
		ctx,
		`SELECT key, value FROM settings
		  WHERE key IN (?, ?, ?, ?, ?)`,
		settingPanelTitle,
		settingUILanguage,
		settingPublicHost,
		settingControllerAddress,
		settingControllerSecret,
	)
	if err != nil {
		return nil, fmt.Errorf("query managed settings: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	settings := make(map[string]string, 3)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan managed setting: %w", err)
		}
		settings[key] = value
	}
	for _, required := range []string{
		settingPanelTitle,
		settingUILanguage,
		settingPublicHost,
		settingControllerAddress,
		settingControllerSecret,
	} {
		if settings[required] == "" {
			return nil, fmt.Errorf("required managed setting %q is not configured", required)
		}
	}
	return settings, nil
}

func (transaction *ManagedTx) loadUsers(
	ctx context.Context,
	state *domain.DesiredState,
) error {
	listenerIndex := make(map[string]int, len(state.Listeners))
	for index := range state.Listeners {
		listenerIndex[state.Listeners[index].ID] = index
	}
	rows, err := transaction.conn.QueryContext(
		ctx,
		`SELECT id, listener_id, name, enabled, uuid, expires_at,
		        created_at, updated_at
		   FROM listener_users
		  ORDER BY name, id`,
	)
	if err != nil {
		return fmt.Errorf("query listener users: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var user domain.User
		var enabled int
		var expiresAt sql.NullString
		var createdAt, updatedAt string
		if err := rows.Scan(
			&user.ID,
			&user.ListenerID,
			&user.Name,
			&enabled,
			&user.UUID,
			&expiresAt,
			&createdAt,
			&updatedAt,
		); err != nil {
			return fmt.Errorf("scan listener user: %w", err)
		}
		user.Enabled = enabled == 1
		if expiresAt.Valid {
			value, err := parseTime(expiresAt.String)
			if err != nil {
				return err
			}
			user.ExpiresAt = &value
		}
		if user.CreatedAt, err = parseTime(createdAt); err != nil {
			return err
		}
		if user.UpdatedAt, err = parseTime(updatedAt); err != nil {
			return err
		}
		index, exists := listenerIndex[user.ListenerID]
		if !exists {
			return errors.New("listener user references an unknown listener")
		}
		state.Listeners[index].Users = append(state.Listeners[index].Users, user)
	}
	return rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRevision(row rowScanner) (domain.Revision, error) {
	var revision domain.Revision
	var status string
	var createdAt string
	var activatedAt sql.NullString
	if err := row.Scan(
		&revision.ID,
		&revision.RevisionNumber,
		&revision.SHA256,
		&revision.FilePath,
		&revision.StateFilePath,
		&status,
		&revision.Reason,
		&revision.ActorAdminID,
		&revision.ErrorMessageRedacted,
		&createdAt,
		&activatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Revision{}, ErrNotFound
		}
		return domain.Revision{}, fmt.Errorf("scan configuration revision: %w", err)
	}
	revision.Status = domain.RevisionStatus(status)
	var err error
	revision.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return domain.Revision{}, err
	}
	if activatedAt.Valid {
		value, err := parseTime(activatedAt.String)
		if err != nil {
			return domain.Revision{}, err
		}
		revision.ActivatedAt = &value
	}
	return revision, nil
}

func listenerPrivateKeyPurpose(id string) string {
	return "listener:" + id + ":reality_private_key"
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullablePort(value *uint16) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func stringsTrimmedEmpty(value string) bool {
	return strings.TrimSpace(value) == ""
}

var _ PublicationRepository = (*ManagedStore)(nil)
var _ PublicationTransaction = (*ManagedTx)(nil)
