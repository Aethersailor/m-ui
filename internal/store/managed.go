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
	settingPublicHost         = "public_host"
	settingPanelTitle         = "panel_title"
	settingUILanguage         = "ui_language"
	settingCookieSecure       = "cookie_secure"
	settingPanelAddress       = "panel_listen_address"
	settingPanelPort          = "panel_listen_port"
	settingExternalBindHost   = "mihomo_external_controller_bind_host"
	settingExternalBindPort   = "mihomo_external_controller_bind_port"
	settingConnectHost        = "mihomo_controller_connect_host"
	settingConnectPort        = "mihomo_controller_connect_port"
	settingCORSOrigins        = "mihomo_external_controller_cors_origins"
	settingEndpointGeneration = "endpoint_settings_generation"
	settingTrustedProxies     = "trusted_proxy_cidrs"
	settingMihomoBinary       = "mihomo_binary_path"
	settingMihomoConfigDir    = "mihomo_config_dir"
	settingMihomoConfigPath   = "mihomo_config_path"
	settingControllerAddress  = "mihomo_controller_address"
	settingControllerSecret   = "mihomo_controller_secret_ciphertext"
	settingMihomoService      = "mihomo_service_name"
	settingHistoryLimit       = "config_history_limit"

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
	PanelTitle                       string
	UILanguage                       string
	PublicHost                       string
	CookieSecure                     bool
	PanelListenAddress               string
	PanelListenPort                  uint16
	MihomoExternalControllerBindHost string
	MihomoExternalControllerBindPort uint16
	MihomoControllerConnectHost      string
	MihomoControllerConnectPort      uint16
	ExternalControllerCORSOrigins    []string
	TrustedProxyCIDRs                []string
	MihomoBinaryPath                 string
	MihomoConfigDir                  string
	MihomoConfigPath                 string
	ControllerAddress                string // Deprecated legacy bootstrap address.
	BootstrapSecret                  string
	MihomoServiceName                string
	HistoryLimit                     int
}

type RuntimeSettings struct {
	InitialSettings
	ControllerSecret string
}

type EndpointSettings struct {
	PanelUIBind                   domain.Endpoint
	MihomoExternalControllerBind  domain.Endpoint
	MihomoControllerConnect       domain.Endpoint
	ExternalControllerCORSOrigins []string
	Generation                    int64
}

type PendingEndpointSettings struct {
	EndpointSettings
	RequiresMUIRestart    bool
	RequiresMihomoRestart bool
	UpdatedAt             time.Time
}

type EndpointSettingsState struct {
	Active      EndpointSettings
	LastApplied EndpointSettings
	Pending     *PendingEndpointSettings
}

var ErrEndpointStateChanged = errors.New("endpoint restart state changed while applying")

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

func initialEndpointSettings(settings InitialSettings) (EndpointSettings, error) {
	endpoints := EndpointSettings{
		PanelUIBind: domain.Endpoint{
			Host: settings.PanelListenAddress,
			Port: settings.PanelListenPort,
		},
		MihomoExternalControllerBind: domain.Endpoint{
			Host: settings.MihomoExternalControllerBindHost,
			Port: settings.MihomoExternalControllerBindPort,
		},
		MihomoControllerConnect: domain.Endpoint{
			Host: settings.MihomoControllerConnectHost,
			Port: settings.MihomoControllerConnectPort,
		},
		ExternalControllerCORSOrigins: append([]string(nil), settings.ExternalControllerCORSOrigins...),
		Generation:                    1,
	}
	var err error
	if settings.ControllerAddress != "" &&
		(endpoints.MihomoExternalControllerBind.Host == "" ||
			endpoints.MihomoExternalControllerBind.Port == 0 ||
			endpoints.MihomoControllerConnect.Host == "" ||
			endpoints.MihomoControllerConnect.Port == 0) {
		legacy, parseErr := domain.ParseEndpoint(settings.ControllerAddress)
		if parseErr != nil {
			return EndpointSettings{}, fmt.Errorf("parse legacy Mihomo controller endpoint: %w", parseErr)
		}
		legacyBind, legacyConnect, err := domain.SplitLegacyControllerEndpoint(legacy)
		if err != nil {
			return EndpointSettings{}, err
		}
		if endpoints.MihomoExternalControllerBind.Host == "" &&
			endpoints.MihomoExternalControllerBind.Port == 0 {
			endpoints.MihomoExternalControllerBind = legacyBind
		}
		if endpoints.MihomoControllerConnect.Host == "" &&
			endpoints.MihomoControllerConnect.Port == 0 {
			endpoints.MihomoControllerConnect = legacyConnect
		}
	}
	if endpoints.MihomoControllerConnect.Host == "" &&
		endpoints.MihomoControllerConnect.Port == 0 &&
		(endpoints.MihomoExternalControllerBind.Host != "" ||
			endpoints.MihomoExternalControllerBind.Port != 0) {
		_, endpoints.MihomoControllerConnect, err = domain.SplitLegacyControllerEndpoint(
			endpoints.MihomoExternalControllerBind,
		)
		if err != nil {
			return EndpointSettings{}, err
		}
	}
	if err := domain.ValidateBindEndpoint(endpoints.PanelUIBind, "panel UI bind endpoint"); err != nil {
		return EndpointSettings{}, err
	}
	if err := domain.ValidateBindEndpoint(
		endpoints.MihomoExternalControllerBind,
		"Mihomo external-controller bind endpoint",
	); err != nil {
		return EndpointSettings{}, err
	}
	if err := domain.ValidateConnectEndpoint(
		endpoints.MihomoControllerConnect,
		"m-ui Mihomo controller connect endpoint",
	); err != nil {
		return EndpointSettings{}, err
	}
	if err := domain.ValidateControllerEndpointPair(
		endpoints.MihomoExternalControllerBind,
		endpoints.MihomoControllerConnect,
	); err != nil {
		return EndpointSettings{}, err
	}
	if err := domain.ValidateCORSOrigins(endpoints.ExternalControllerCORSOrigins); err != nil {
		return EndpointSettings{}, err
	}
	return endpoints, nil
}

func (managed *ManagedStore) EnsureInitialSettings(
	ctx context.Context,
	settings InitialSettings,
	now time.Time,
) error {
	endpoints, err := initialEndpointSettings(settings)
	if err != nil {
		return err
	}
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
		{settingCookieSecure, strconv.FormatBool(settings.CookieSecure)},
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
	legacyAddress := ""
	_ = transaction.QueryRowContext(
		ctx,
		"SELECT value FROM settings WHERE key = ?",
		settingControllerAddress,
	).Scan(&legacyAddress)
	if legacyAddress != "" {
		var existingExternalHost string
		externalExists := transaction.QueryRowContext(
			ctx,
			"SELECT value FROM settings WHERE key = ?",
			settingExternalBindHost,
		).Scan(&existingExternalHost) == nil
		var existingConnectHost string
		connectExists := transaction.QueryRowContext(
			ctx,
			"SELECT value FROM settings WHERE key = ?",
			settingConnectHost,
		).Scan(&existingConnectHost) == nil
		if !externalExists || !connectExists {
			legacyEndpoint, parseErr := domain.ParseEndpoint(legacyAddress)
			if parseErr != nil {
				return fmt.Errorf("parse stored legacy Mihomo controller endpoint: %w", parseErr)
			}
			legacyBind, legacyConnect, splitErr := domain.SplitLegacyControllerEndpoint(legacyEndpoint)
			if splitErr != nil {
				return fmt.Errorf("migrate stored legacy Mihomo controller endpoint: %w", splitErr)
			}
			if !externalExists {
				endpoints.MihomoExternalControllerBind = legacyBind
			}
			if !connectExists {
				endpoints.MihomoControllerConnect = legacyConnect
			}
		}
	}
	corsOrigins := settings.ExternalControllerCORSOrigins
	if corsOrigins == nil {
		corsOrigins = []string{}
	}
	corsJSON, err := json.Marshal(corsOrigins)
	if err != nil {
		return errors.New("encode Mihomo external-controller CORS settings")
	}
	advanced := []struct {
		key   string
		value string
	}{
		{settingPanelAddress, endpoints.PanelUIBind.Host},
		{settingPanelPort, strconv.Itoa(int(endpoints.PanelUIBind.Port))},
		{settingExternalBindHost, endpoints.MihomoExternalControllerBind.Host},
		{settingExternalBindPort, strconv.Itoa(int(endpoints.MihomoExternalControllerBind.Port))},
		{settingConnectHost, endpoints.MihomoControllerConnect.Host},
		{settingConnectPort, strconv.Itoa(int(endpoints.MihomoControllerConnect.Port))},
		{settingCORSOrigins, string(corsJSON)},
		{settingEndpointGeneration, "1"},
		{settingTrustedProxies, string(trustedProxies)},
		{settingMihomoConfigDir, settings.MihomoConfigDir},
		{settingMihomoConfigPath, settings.MihomoConfigPath},
		{settingControllerAddress, endpoints.MihomoControllerConnect.Address()},
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
		conflictClause := "ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at"
		switch item.key {
		case settingPanelAddress,
			settingPanelPort,
			settingExternalBindHost,
			settingExternalBindPort,
			settingConnectHost,
			settingConnectPort,
			settingCORSOrigins,
			settingEndpointGeneration,
			settingControllerAddress:
			conflictClause = "ON CONFLICT(key) DO NOTHING"
		}
		if _, err := transaction.ExecContext(
			ctx,
			`INSERT INTO settings(key, value, updated_at) VALUES (?, ?, ?) `+conflictClause,
			item.key,
			item.value,
			formatTime(now),
		); err != nil {
			return fmt.Errorf("store local setting %q: %w", item.key, err)
		}
	}
	activeValues := make(map[string]string)
	activeRows, err := transaction.QueryContext(ctx, "SELECT key, value FROM settings")
	if err != nil {
		return fmt.Errorf("read seeded endpoint settings: %w", err)
	}
	for activeRows.Next() {
		var key, value string
		if err := activeRows.Scan(&key, &value); err != nil {
			_ = activeRows.Close()
			return fmt.Errorf("scan seeded endpoint setting: %w", err)
		}
		activeValues[key] = value
	}
	if err := activeRows.Err(); err != nil {
		_ = activeRows.Close()
		return fmt.Errorf("iterate seeded endpoint settings: %w", err)
	}
	_ = activeRows.Close()
	activeEndpoints, err := endpointSettingsFromValues(activeValues)
	if err != nil {
		return fmt.Errorf("validate seeded endpoint settings: %w", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO endpoint_settings_last_applied(
			id, panel_ui_bind_host, panel_ui_bind_port,
			mihomo_external_controller_bind_host,
			mihomo_external_controller_bind_port,
			mihomo_controller_connect_host, mihomo_controller_connect_port,
			external_controller_cors_origins_json, generation, updated_at
		) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		activeEndpoints.PanelUIBind.Host,
		activeEndpoints.PanelUIBind.Port,
		activeEndpoints.MihomoExternalControllerBind.Host,
		activeEndpoints.MihomoExternalControllerBind.Port,
		activeEndpoints.MihomoControllerConnect.Host,
		activeEndpoints.MihomoControllerConnect.Port,
		encodeOrigins(activeEndpoints.ExternalControllerCORSOrigins),
		activeEndpoints.Generation,
		formatTime(now),
	); err != nil {
		return fmt.Errorf("seed applied endpoint settings: %w", err)
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
	endpoints, err := endpointSettingsFromValues(values)
	if err != nil {
		return RuntimeSettings{}, err
	}
	historyLimit, err := strconv.Atoi(values[settingHistoryLimit])
	if err != nil || historyLimit < 1 {
		return RuntimeSettings{}, errors.New("stored revision history limit is invalid")
	}
	cookieSecure, err := strconv.ParseBool(values[settingCookieSecure])
	if err != nil {
		return RuntimeSettings{}, errors.New("stored cookie security setting is invalid")
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
			PanelTitle:                       values[settingPanelTitle],
			UILanguage:                       values[settingUILanguage],
			PublicHost:                       values[settingPublicHost],
			CookieSecure:                     cookieSecure,
			PanelListenAddress:               endpoints.PanelUIBind.Host,
			PanelListenPort:                  endpoints.PanelUIBind.Port,
			MihomoExternalControllerBindHost: endpoints.MihomoExternalControllerBind.Host,
			MihomoExternalControllerBindPort: endpoints.MihomoExternalControllerBind.Port,
			MihomoControllerConnectHost:      endpoints.MihomoControllerConnect.Host,
			MihomoControllerConnectPort:      endpoints.MihomoControllerConnect.Port,
			ExternalControllerCORSOrigins:    append([]string(nil), endpoints.ExternalControllerCORSOrigins...),
			TrustedProxyCIDRs:                trustedProxies,
			MihomoBinaryPath:                 values[settingMihomoBinary],
			MihomoConfigDir:                  values[settingMihomoConfigDir],
			MihomoConfigPath:                 values[settingMihomoConfigPath],
			ControllerAddress:                endpoints.MihomoControllerConnect.Address(),
			BootstrapSecret:                  "",
			MihomoServiceName:                values[settingMihomoService],
			HistoryLimit:                     historyLimit,
		},
		ControllerSecret: string(secret),
	}, nil
}

// UILanguage returns only the public panel language default. Keep this query
// separate from Settings so the unauthenticated setup status endpoint never
// needs to decrypt or expose unrelated managed settings.
func (managed *ManagedStore) UILanguage(ctx context.Context) (string, error) {
	var language string
	if err := managed.store.db.QueryRowContext(
		ctx,
		"SELECT value FROM settings WHERE key = ?",
		settingUILanguage,
	).Scan(&language); err != nil {
		return "", fmt.Errorf("read UI language: %w", err)
	}
	switch language {
	case "auto", "en-US", "zh-CN":
		return language, nil
	default:
		return "", errors.New("stored UI language is invalid")
	}
}

func endpointSettingsFromValues(values map[string]string) (EndpointSettings, error) {
	panelPort, err := strconv.ParseUint(values[settingPanelPort], 10, 16)
	if err != nil || panelPort == 0 {
		return EndpointSettings{}, errors.New("stored panel port is invalid")
	}
	externalHost := values[settingExternalBindHost]
	externalPortText := values[settingExternalBindPort]
	connectHost := values[settingConnectHost]
	connectPortText := values[settingConnectPort]
	var legacyBind, legacyConnect domain.Endpoint
	var hasLegacy bool
	needsExternal := externalHost == "" || externalPortText == ""
	needsConnect := connectHost == "" || connectPortText == ""
	if (needsExternal || needsConnect) && values[settingControllerAddress] != "" {
		legacy, legacyErr := domain.ParseEndpoint(values[settingControllerAddress])
		if legacyErr != nil {
			return EndpointSettings{}, errors.New("stored legacy Mihomo controller endpoint is invalid")
		}
		legacyBind, legacyConnect, legacyErr = domain.SplitLegacyControllerEndpoint(legacy)
		if legacyErr != nil {
			return EndpointSettings{}, legacyErr
		}
		hasLegacy = true
	}
	if needsExternal {
		if !hasLegacy {
			return EndpointSettings{}, errors.New("stored Mihomo external-controller endpoint is invalid")
		}
		externalHost = legacyBind.Host
		externalPortText = strconv.Itoa(int(legacyBind.Port))
	}
	if needsConnect {
		if hasLegacy {
			connectHost = legacyConnect.Host
			connectPortText = strconv.Itoa(int(legacyConnect.Port))
		} else {
			externalPort, externalErr := strconv.ParseUint(externalPortText, 10, 16)
			if externalErr != nil || externalPort == 0 {
				return EndpointSettings{}, errors.New("stored Mihomo controller connect endpoint is invalid")
			}
			_, connect, splitErr := domain.SplitLegacyControllerEndpoint(domain.Endpoint{
				Host: externalHost,
				Port: uint16(externalPort),
			})
			if splitErr != nil {
				return EndpointSettings{}, splitErr
			}
			connectHost = connect.Host
			connectPortText = strconv.Itoa(int(connect.Port))
		}
	}
	externalPort, err := strconv.ParseUint(externalPortText, 10, 16)
	if err != nil || externalPort == 0 {
		return EndpointSettings{}, errors.New("stored Mihomo external-controller port is invalid")
	}
	connectPort, err := strconv.ParseUint(connectPortText, 10, 16)
	if err != nil || connectPort == 0 {
		return EndpointSettings{}, errors.New("stored Mihomo controller connect port is invalid")
	}
	var origins []string
	if values[settingCORSOrigins] == "" {
		origins = []string{}
	} else if err := json.Unmarshal([]byte(values[settingCORSOrigins]), &origins); err != nil {
		return EndpointSettings{}, errors.New("stored Mihomo external-controller CORS settings are invalid")
	}
	endpointSettings := EndpointSettings{
		PanelUIBind: domain.Endpoint{
			Host: values[settingPanelAddress],
			Port: uint16(panelPort),
		},
		MihomoExternalControllerBind: domain.Endpoint{
			Host: externalHost,
			Port: uint16(externalPort),
		},
		MihomoControllerConnect: domain.Endpoint{
			Host: connectHost,
			Port: uint16(connectPort),
		},
		ExternalControllerCORSOrigins: origins,
		Generation:                    1,
	}
	if generation, parseErr := strconv.ParseInt(values[settingEndpointGeneration], 10, 64); parseErr == nil && generation > 0 {
		endpointSettings.Generation = generation
	}
	if err := domain.ValidateBindEndpoint(endpointSettings.PanelUIBind, "stored panel UI bind endpoint"); err != nil {
		return EndpointSettings{}, err
	}
	if err := domain.ValidateBindEndpoint(
		endpointSettings.MihomoExternalControllerBind,
		"stored Mihomo external-controller bind endpoint",
	); err != nil {
		return EndpointSettings{}, err
	}
	if err := domain.ValidateConnectEndpoint(
		endpointSettings.MihomoControllerConnect,
		"stored Mihomo controller connect endpoint",
	); err != nil {
		return EndpointSettings{}, err
	}
	if err := domain.ValidateControllerEndpointPair(
		endpointSettings.MihomoExternalControllerBind,
		endpointSettings.MihomoControllerConnect,
	); err != nil {
		return EndpointSettings{}, err
	}
	if err := domain.ValidateCORSOrigins(endpointSettings.ExternalControllerCORSOrigins); err != nil {
		return EndpointSettings{}, err
	}
	return endpointSettings, nil
}

func (managed *ManagedStore) EndpointSettings(
	ctx context.Context,
) (EndpointSettingsState, error) {
	rows, err := managed.store.db.QueryContext(
		ctx,
		`SELECT key, value FROM settings
		  WHERE key IN (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		settingPanelAddress,
		settingPanelPort,
		settingExternalBindHost,
		settingExternalBindPort,
		settingConnectHost,
		settingConnectPort,
		settingCORSOrigins,
		settingEndpointGeneration,
		settingControllerAddress,
	)
	if err != nil {
		return EndpointSettingsState{}, fmt.Errorf("query endpoint settings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	values := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return EndpointSettingsState{}, fmt.Errorf("scan endpoint setting: %w", err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return EndpointSettingsState{}, fmt.Errorf("iterate endpoint settings: %w", err)
	}
	active, err := endpointSettingsFromValues(values)
	if err != nil {
		return EndpointSettingsState{}, err
	}
	pending, err := managed.readPendingEndpointSettings(ctx)
	if err != nil {
		return EndpointSettingsState{}, err
	}
	lastApplied, err := managed.readLastAppliedEndpointSettings(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Test and pre-bootstrap databases may not have a snapshot row yet;
			// the current endpoint is the only safe baseline in that state.
			lastApplied = active
		} else {
			return EndpointSettingsState{}, err
		}
	}
	return EndpointSettingsState{
		Active:      active,
		LastApplied: lastApplied,
		Pending:     pending,
	}, nil
}

// MihomoRestartRequired is the small gate consumed by lifecycle managers
// which can restart Mihomo without going through Publisher. Keeping the
// decision in the durable settings store prevents those paths from applying
// a pending external-controller candidate behind the UI's back.
func (managed *ManagedStore) MihomoRestartRequired(ctx context.Context) (bool, error) {
	state, err := managed.EndpointSettings(ctx)
	if err != nil {
		return false, err
	}
	return state.Pending != nil && state.Pending.RequiresMihomoRestart, nil
}

func (managed *ManagedStore) readLastAppliedEndpointSettings(
	ctx context.Context,
) (EndpointSettings, error) {
	var applied EndpointSettings
	var originsJSON string
	err := managed.store.db.QueryRowContext(
		ctx,
		`SELECT panel_ui_bind_host, panel_ui_bind_port,
		        mihomo_external_controller_bind_host,
		        mihomo_external_controller_bind_port,
		        mihomo_controller_connect_host, mihomo_controller_connect_port,
		        external_controller_cors_origins_json, generation
		   FROM endpoint_settings_last_applied
		  WHERE id = 1`,
	).Scan(
		&applied.PanelUIBind.Host,
		&applied.PanelUIBind.Port,
		&applied.MihomoExternalControllerBind.Host,
		&applied.MihomoExternalControllerBind.Port,
		&applied.MihomoControllerConnect.Host,
		&applied.MihomoControllerConnect.Port,
		&originsJSON,
		&applied.Generation,
	)
	if err != nil {
		return EndpointSettings{}, fmt.Errorf("read last-applied endpoint settings: %w", err)
	}
	if err := json.Unmarshal([]byte(originsJSON), &applied.ExternalControllerCORSOrigins); err != nil {
		return EndpointSettings{}, errors.New("decode last-applied endpoint CORS settings")
	}
	return applied, nil
}

func (managed *ManagedStore) readPendingEndpointSettings(
	ctx context.Context,
) (*PendingEndpointSettings, error) {
	var pending PendingEndpointSettings
	var originsJSON, updatedAt string
	var requiresMUI, requiresMihomo int
	err := managed.store.db.QueryRowContext(
		ctx,
		`SELECT panel_ui_bind_host, panel_ui_bind_port,
		        mihomo_external_controller_bind_host,
		        mihomo_external_controller_bind_port,
		        mihomo_controller_connect_host, mihomo_controller_connect_port,
		        external_controller_cors_origins_json, generation,
		        requires_mui_restart, requires_mihomo_restart, updated_at
		   FROM endpoint_settings_pending
		  WHERE id = 1`,
	).Scan(
		&pending.PanelUIBind.Host,
		&pending.PanelUIBind.Port,
		&pending.MihomoExternalControllerBind.Host,
		&pending.MihomoExternalControllerBind.Port,
		&pending.MihomoControllerConnect.Host,
		&pending.MihomoControllerConnect.Port,
		&originsJSON,
		&pending.Generation,
		&requiresMUI,
		&requiresMihomo,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pending endpoint settings: %w", err)
	}
	if err := json.Unmarshal([]byte(originsJSON), &pending.ExternalControllerCORSOrigins); err != nil {
		return nil, errors.New("decode pending endpoint CORS settings")
	}
	pending.RequiresMUIRestart = requiresMUI == 1
	pending.RequiresMihomoRestart = requiresMihomo == 1
	pending.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return nil, err
	}
	return &pending, nil
}

func (managed *ManagedStore) ClearEndpointRestartRequirements(
	ctx context.Context,
	clearMUI, clearMihomo bool,
	expectedGeneration int64,
	expected EndpointSettings,
) error {
	if !clearMUI && !clearMihomo {
		return nil
	}
	transaction, err := managed.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin endpoint restart state update: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	var pending PendingEndpointSettings
	var originsJSON, updatedAt string
	var requiresMUI, requiresMihomo int
	if err := transaction.QueryRowContext(
		ctx,
		`SELECT panel_ui_bind_host, panel_ui_bind_port,
		        mihomo_external_controller_bind_host,
		        mihomo_external_controller_bind_port,
		        mihomo_controller_connect_host, mihomo_controller_connect_port,
		        external_controller_cors_origins_json, generation,
		        requires_mui_restart, requires_mihomo_restart, updated_at
		   FROM endpoint_settings_pending
		  WHERE id = 1`,
	).Scan(
		&pending.PanelUIBind.Host,
		&pending.PanelUIBind.Port,
		&pending.MihomoExternalControllerBind.Host,
		&pending.MihomoExternalControllerBind.Port,
		&pending.MihomoControllerConnect.Host,
		&pending.MihomoControllerConnect.Port,
		&originsJSON,
		&pending.Generation,
		&requiresMUI,
		&requiresMihomo,
		&updatedAt,
	); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return fmt.Errorf("read endpoint restart state: %w", err)
	}
	if err := json.Unmarshal([]byte(originsJSON), &pending.ExternalControllerCORSOrigins); err != nil {
		return errors.New("decode pending endpoint CORS settings")
	}
	if expectedGeneration < 1 || pending.Generation != expectedGeneration {
		return ErrEndpointStateChanged
	}
	if clearMUI && (!pending.PanelUIBind.Equal(expected.PanelUIBind) ||
		!pending.MihomoControllerConnect.Equal(expected.MihomoControllerConnect)) {
		return ErrEndpointStateChanged
	}
	if clearMihomo && (!pending.MihomoExternalControllerBind.Equal(expected.MihomoExternalControllerBind) ||
		!equalStrings(pending.ExternalControllerCORSOrigins, expected.ExternalControllerCORSOrigins)) {
		return ErrEndpointStateChanged
	}
	var applied EndpointSettings
	var appliedOrigins string
	if err := transaction.QueryRowContext(
		ctx,
		`SELECT panel_ui_bind_host, panel_ui_bind_port,
		        mihomo_external_controller_bind_host,
		        mihomo_external_controller_bind_port,
		        mihomo_controller_connect_host, mihomo_controller_connect_port,
		        external_controller_cors_origins_json, generation
		   FROM endpoint_settings_last_applied
		  WHERE id = 1`,
	).Scan(
		&applied.PanelUIBind.Host,
		&applied.PanelUIBind.Port,
		&applied.MihomoExternalControllerBind.Host,
		&applied.MihomoExternalControllerBind.Port,
		&applied.MihomoControllerConnect.Host,
		&applied.MihomoControllerConnect.Port,
		&appliedOrigins,
		&applied.Generation,
	); errors.Is(err, sql.ErrNoRows) {
		return errors.New("applied endpoint settings snapshot is missing")
	} else if err != nil {
		return fmt.Errorf("read applied endpoint settings snapshot: %w", err)
	}
	if err := json.Unmarshal([]byte(appliedOrigins), &applied.ExternalControllerCORSOrigins); err != nil {
		return errors.New("decode applied endpoint CORS settings")
	}
	if clearMUI {
		applied.PanelUIBind = pending.PanelUIBind
		applied.MihomoControllerConnect = pending.MihomoControllerConnect
		requiresMUI = 0
	}
	if clearMihomo {
		applied.MihomoExternalControllerBind = pending.MihomoExternalControllerBind
		applied.ExternalControllerCORSOrigins = append(
			[]string(nil),
			pending.ExternalControllerCORSOrigins...,
		)
		requiresMihomo = 0
	}
	result, err := transaction.ExecContext(
		ctx,
		`UPDATE endpoint_settings_last_applied
		    SET panel_ui_bind_host = ?, panel_ui_bind_port = ?,
		        mihomo_external_controller_bind_host = ?,
		        mihomo_external_controller_bind_port = ?,
		        mihomo_controller_connect_host = ?, mihomo_controller_connect_port = ?,
		        external_controller_cors_origins_json = ?, generation = ?, updated_at = ?
		  WHERE id = 1 AND generation = ?`,
		applied.PanelUIBind.Host,
		applied.PanelUIBind.Port,
		applied.MihomoExternalControllerBind.Host,
		applied.MihomoExternalControllerBind.Port,
		applied.MihomoControllerConnect.Host,
		applied.MihomoControllerConnect.Port,
		encodeOrigins(applied.ExternalControllerCORSOrigins),
		pending.Generation,
		formatTime(time.Now().UTC()),
		applied.Generation,
	)
	if err != nil {
		return fmt.Errorf("update applied endpoint settings snapshot: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return ErrEndpointStateChanged
	}
	if requiresMUI == 0 && requiresMihomo == 0 {
		result, err := transaction.ExecContext(
			ctx,
			"DELETE FROM endpoint_settings_pending WHERE id = 1 AND generation = ?",
			pending.Generation,
		)
		if err != nil {
			return fmt.Errorf("delete endpoint restart state: %w", err)
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return ErrEndpointStateChanged
		}
	} else if _, err := transaction.ExecContext(
		ctx,
		`UPDATE endpoint_settings_pending
		    SET requires_mui_restart = ?, requires_mihomo_restart = ?
		  WHERE id = 1 AND generation = ?`,
		requiresMUI,
		requiresMihomo,
		pending.Generation,
	); err != nil {
		return fmt.Errorf("update endpoint restart state: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit endpoint restart state: %w", err)
	}
	return nil
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
		AsOf:                          asOf.UTC(),
		PanelTitle:                    settings[settingPanelTitle],
		UILanguage:                    settings[settingUILanguage],
		CookieSecure:                  settings[settingCookieSecure] == "true",
		PanelUIBind:                   domain.Endpoint{Host: settings[settingPanelAddress], Port: parseStoredPort(settings[settingPanelPort])},
		MihomoExternalControllerBind:  domain.Endpoint{Host: settings[settingExternalBindHost], Port: parseStoredPort(settings[settingExternalBindPort])},
		MihomoControllerConnect:       domain.Endpoint{Host: settings[settingConnectHost], Port: parseStoredPort(settings[settingConnectPort])},
		ExternalControllerCORSOrigins: parseStoredOrigins(settings[settingCORSOrigins]),
		EndpointGeneration:            parseStoredGeneration(settings[settingEndpointGeneration]),
		ControllerAddress:             settings[settingControllerAddress],
		ControllerSecret:              string(secret),
		PublicHost:                    settings[settingPublicHost],
	}
	state, err = state.NormalizeLegacy()
	if err != nil {
		return domain.DesiredState{}, fmt.Errorf("normalize desired-state endpoints: %w", err)
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
	normalized, err := state.NormalizeLegacy()
	if err != nil {
		return fmt.Errorf("normalize replacement desired-state endpoints: %w", err)
	}
	state = normalized
	if err := state.Validate(); err != nil {
		return fmt.Errorf("validate replacement desired state: %w", err)
	}
	previousEndpoints, previousExists, err := transaction.endpointSettings(ctx)
	if err != nil {
		return err
	}
	lastApplied, lastAppliedExists, err := transaction.lastAppliedEndpointSettings(ctx)
	if err != nil {
		return err
	}
	baselineEndpoints := previousEndpoints
	baselineExists := previousExists
	if lastAppliedExists {
		baselineEndpoints = lastApplied
		baselineExists = true
	}
	if state.EndpointGeneration < 1 {
		state.EndpointGeneration = 1
	}
	if previousExists {
		if endpointSettingsChanged(previousEndpoints, state) {
			state.EndpointGeneration = previousEndpoints.Generation + 1
		} else {
			state.EndpointGeneration = previousEndpoints.Generation
		}
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
		{settingCookieSecure, strconv.FormatBool(state.CookieSecure)},
		{settingPanelAddress, state.PanelUIBind.Host},
		{settingPanelPort, strconv.Itoa(int(state.PanelUIBind.Port))},
		{settingExternalBindHost, state.MihomoExternalControllerBind.Host},
		{settingExternalBindPort, strconv.Itoa(int(state.MihomoExternalControllerBind.Port))},
		{settingConnectHost, state.MihomoControllerConnect.Host},
		{settingConnectPort, strconv.Itoa(int(state.MihomoControllerConnect.Port))},
		{settingCORSOrigins, encodeOrigins(state.ExternalControllerCORSOrigins)},
		{settingEndpointGeneration, strconv.FormatInt(state.EndpointGeneration, 10)},
		{settingControllerAddress, state.MihomoControllerConnect.Address()},
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
	if baselineExists && endpointSettingsChanged(baselineEndpoints, state) {
		if err := transaction.upsertPendingEndpointSettings(
			ctx,
			state,
			baselineEndpoints,
			formatTime(state.AsOf),
		); err != nil {
			return err
		}
	} else if baselineExists {
		if _, err := transaction.conn.ExecContext(
			ctx,
			"DELETE FROM endpoint_settings_pending WHERE id = 1",
		); err != nil {
			return fmt.Errorf("clear resolved endpoint settings: %w", err)
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
		  WHERE key IN (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		settingPanelTitle,
		settingUILanguage,
		settingPublicHost,
		settingCookieSecure,
		settingPanelAddress,
		settingPanelPort,
		settingExternalBindHost,
		settingExternalBindPort,
		settingConnectHost,
		settingConnectPort,
		settingCORSOrigins,
		settingEndpointGeneration,
		settingControllerAddress,
		settingControllerSecret,
	)
	if err != nil {
		return nil, fmt.Errorf("query managed settings: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	settings := make(map[string]string, 14)
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
		settingCookieSecure,
		settingPanelAddress,
		settingPanelPort,
		settingControllerSecret,
	} {
		if settings[required] == "" {
			return nil, fmt.Errorf("required managed setting %q is not configured", required)
		}
	}
	return settings, nil
}

func (transaction *ManagedTx) endpointSettings(
	ctx context.Context,
) (EndpointSettings, bool, error) {
	settings, err := transaction.settings(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "required managed setting") {
			return EndpointSettings{}, false, nil
		}
		return EndpointSettings{}, false, err
	}
	endpoints, err := endpointSettingsFromValues(settings)
	if err != nil {
		return EndpointSettings{}, false, err
	}
	return endpoints, true, nil
}

func (transaction *ManagedTx) lastAppliedEndpointSettings(
	ctx context.Context,
) (EndpointSettings, bool, error) {
	var endpoints EndpointSettings
	var originsJSON string
	err := transaction.conn.QueryRowContext(
		ctx,
		`SELECT panel_ui_bind_host, panel_ui_bind_port,
		        mihomo_external_controller_bind_host,
		        mihomo_external_controller_bind_port,
		        mihomo_controller_connect_host, mihomo_controller_connect_port,
		        external_controller_cors_origins_json, generation
		   FROM endpoint_settings_last_applied
		  WHERE id = 1`,
	).Scan(
		&endpoints.PanelUIBind.Host,
		&endpoints.PanelUIBind.Port,
		&endpoints.MihomoExternalControllerBind.Host,
		&endpoints.MihomoExternalControllerBind.Port,
		&endpoints.MihomoControllerConnect.Host,
		&endpoints.MihomoControllerConnect.Port,
		&originsJSON,
		&endpoints.Generation,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return EndpointSettings{}, false, nil
	}
	if err != nil {
		return EndpointSettings{}, false, fmt.Errorf("read applied endpoint settings: %w", err)
	}
	if err := json.Unmarshal([]byte(originsJSON), &endpoints.ExternalControllerCORSOrigins); err != nil {
		return EndpointSettings{}, false, errors.New("decode applied endpoint CORS settings")
	}
	return endpoints, true, nil
}

func endpointSettingsChanged(previous EndpointSettings, state domain.DesiredState) bool {
	return !previous.PanelUIBind.Equal(state.PanelUIBind) ||
		!previous.MihomoExternalControllerBind.Equal(state.MihomoExternalControllerBind) ||
		!previous.MihomoControllerConnect.Equal(state.MihomoControllerConnect) ||
		!equalStrings(previous.ExternalControllerCORSOrigins, state.ExternalControllerCORSOrigins)
}

func (transaction *ManagedTx) upsertPendingEndpointSettings(
	ctx context.Context,
	state domain.DesiredState,
	lastApplied EndpointSettings,
	updatedAt string,
) error {
	requiresMUI := !lastApplied.PanelUIBind.Equal(state.PanelUIBind) ||
		!lastApplied.MihomoControllerConnect.Equal(state.MihomoControllerConnect)
	requiresMihomo := !lastApplied.MihomoExternalControllerBind.Equal(state.MihomoExternalControllerBind) ||
		!equalStrings(lastApplied.ExternalControllerCORSOrigins, state.ExternalControllerCORSOrigins)
	if _, err := transaction.conn.ExecContext(
		ctx,
		`INSERT INTO endpoint_settings_pending(
			id, panel_ui_bind_host, panel_ui_bind_port,
			mihomo_external_controller_bind_host,
			mihomo_external_controller_bind_port,
			mihomo_controller_connect_host, mihomo_controller_connect_port,
			external_controller_cors_origins_json, generation,
			requires_mui_restart, requires_mihomo_restart, updated_at
		) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
			panel_ui_bind_host = excluded.panel_ui_bind_host,
			panel_ui_bind_port = excluded.panel_ui_bind_port,
			mihomo_external_controller_bind_host = excluded.mihomo_external_controller_bind_host,
			mihomo_external_controller_bind_port = excluded.mihomo_external_controller_bind_port,
			mihomo_controller_connect_host = excluded.mihomo_controller_connect_host,
			mihomo_controller_connect_port = excluded.mihomo_controller_connect_port,
			external_controller_cors_origins_json = excluded.external_controller_cors_origins_json,
			generation = excluded.generation,
			requires_mui_restart = excluded.requires_mui_restart,
			requires_mihomo_restart = excluded.requires_mihomo_restart,
			updated_at = excluded.updated_at`,
		state.PanelUIBind.Host,
		state.PanelUIBind.Port,
		state.MihomoExternalControllerBind.Host,
		state.MihomoExternalControllerBind.Port,
		state.MihomoControllerConnect.Host,
		state.MihomoControllerConnect.Port,
		encodeOrigins(state.ExternalControllerCORSOrigins),
		state.EndpointGeneration,
		boolInt(requiresMUI),
		boolInt(requiresMihomo),
		updatedAt,
	); err != nil {
		return fmt.Errorf("store pending endpoint settings: %w", err)
	}
	return nil
}

func parseStoredPort(value string) uint16 {
	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return 0
	}
	return uint16(parsed)
}

func parseStoredGeneration(value string) int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 1 {
		return 1
	}
	return parsed
}

func parseStoredOrigins(value string) []string {
	if value == "" {
		return []string{}
	}
	var origins []string
	if err := json.Unmarshal([]byte(value), &origins); err != nil {
		return []string{}
	}
	return origins
}

func encodeOrigins(origins []string) string {
	if origins == nil {
		origins = []string{}
	}
	encoded, err := json.Marshal(origins)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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
