package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	coremanagement "github.com/Aethersailor/m-ui/internal/core"
	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/operation"
	"github.com/Aethersailor/m-ui/internal/publisher"
	"github.com/Aethersailor/m-ui/internal/redact"
	"github.com/Aethersailor/m-ui/internal/store"
)

var (
	ErrNotFound   = errors.New("managed resource not found")
	ErrValidation = errors.New("managed state validation failed")
	ErrConflict   = errors.New("managed state conflict")
)

type ListenerSpec struct {
	Name               string
	ListenAddress      string
	ListenPort         uint16
	PublicHostOverride string
	PublicPortOverride *uint16
	ServerName         string
	RealityDest        string
	RealityPrivateKey  string
	RealityPublicKey   string
	ShortID            string
	UDPEnabled         bool
}

type UserSpec struct {
	Name      string
	UUID      string
	ExpiresAt *time.Time
}

type OnboardingSpec struct {
	PublicHost string
	Listener   ListenerSpec
	User       UserSpec
}

type OnboardingResult struct {
	Listener domain.Listener
	User     domain.User
	Revision domain.Revision
	Share    Share
}

type EditableSettings struct {
	PanelTitle   string
	UILanguage   string
	PublicHost   string
	CookieSecure bool
}

type EndpointSettings = store.EndpointSettings
type PendingEndpointSettings = store.PendingEndpointSettings
type EndpointSettingsState = store.EndpointSettingsState

// RuntimeReadyGuard acquires a continuous m-ui startup-readiness guard. The
// returned release function must stay held until the runtime coordinator has
// been released, so a new m-ui startup cannot invalidate the observation while
// a lifecycle action is in progress.
type RuntimeReadyGuard func(context.Context) (func() error, error)

type RuntimeStatus struct {
	Active          bool
	Degraded        bool
	DegradedReason  string
	Version         mihomo.Version
	Traffic         mihomo.TrafficSnapshot
	Memory          mihomo.MemorySnapshot
	ConnectionCount int
	DownloadTotal   int64
	UploadTotal     int64
	ObservedAt      time.Time
}

type ManagerOptions struct {
	Store       *store.ManagedStore
	Publisher   *publisher.Publisher
	CLI         mihomo.CoreCLI
	Controller  mihomo.CoreController
	Process     mihomo.CoreProcess
	Runtime     *RuntimeMonitor
	Core        *coremanagement.Manager
	Coordinator *operation.Coordinator
	ReadyGuard  RuntimeReadyGuard
	Clock       func() time.Time
}

type Manager struct {
	store       *store.ManagedStore
	publisher   *publisher.Publisher
	cli         mihomo.CoreCLI
	controller  mihomo.CoreController
	process     mihomo.CoreProcess
	boundary    *RuntimeBoundary
	runtime     *RuntimeMonitor
	core        *coremanagement.Manager
	coordinator *operation.Coordinator
	readyGuard  RuntimeReadyGuard
	clock       func() time.Time
}

func NewManager(options ManagerOptions) (*Manager, error) {
	switch {
	case options.Store == nil:
		return nil, errors.New("managed store is required")
	case options.Publisher == nil:
		return nil, errors.New("publisher is required")
	case options.CLI == nil:
		return nil, errors.New("mihomo CLI is required")
	case options.Controller == nil:
		return nil, errors.New("mihomo Controller is required")
	case options.Process == nil:
		return nil, errors.New("mihomo process adapter is required")
	case options.Runtime == nil:
		return nil, errors.New("runtime monitor is required")
	case options.ReadyGuard == nil:
		return nil, errors.New("runtime readiness guard is required")
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Coordinator == nil {
		options.Coordinator = operation.NewCoordinator()
	}
	boundary, err := NewRuntimeBoundary(RuntimeBoundaryOptions{
		Store:          options.Store,
		Controller:     options.Controller,
		Process:        options.Process,
		Coordinator:    options.Coordinator,
		HealthTimeout:  10 * time.Second,
		HealthInterval: 100 * time.Millisecond,
	})
	if err != nil {
		return nil, err
	}
	return &Manager{
		store:       options.Store,
		publisher:   options.Publisher,
		cli:         options.CLI,
		controller:  options.Controller,
		process:     options.Process,
		boundary:    boundary,
		runtime:     options.Runtime,
		core:        options.Core,
		coordinator: options.Coordinator,
		readyGuard:  options.ReadyGuard,
		clock:       options.Clock,
	}, nil
}

func (manager *Manager) Listeners(ctx context.Context) ([]domain.Listener, error) {
	state, err := manager.currentState(ctx)
	if err != nil {
		return nil, err
	}
	return state.Listeners, nil
}

func (manager *Manager) Listener(
	ctx context.Context,
	listenerID string,
) (domain.Listener, error) {
	state, err := manager.currentState(ctx)
	if err != nil {
		return domain.Listener{}, err
	}
	index := listenerIndex(state, listenerID)
	if index < 0 {
		return domain.Listener{}, ErrNotFound
	}
	return state.Listeners[index], nil
}

func (manager *Manager) CompleteOnboarding(
	ctx context.Context,
	actorAdminID string,
	spec OnboardingSpec,
) (OnboardingResult, error) {
	listenerID, err := domain.GenerateUUID()
	if err != nil {
		return OnboardingResult{}, err
	}
	userID, err := domain.GenerateUUID()
	if err != nil {
		return OnboardingResult{}, err
	}
	if spec.User.UUID == "" {
		spec.User.UUID, err = domain.GenerateUUID()
		if err != nil {
			return OnboardingResult{}, err
		}
	}
	keypair, err := manager.cli.GenerateRealityKeypair(ctx)
	if err != nil {
		return OnboardingResult{}, err
	}
	spec.Listener.RealityPrivateKey = keypair.PrivateKey
	spec.Listener.RealityPublicKey = keypair.PublicKey
	spec.Listener.ShortID, err = domain.GenerateShortID()
	if err != nil {
		return OnboardingResult{}, err
	}

	var result OnboardingResult
	result.Revision, err = manager.mutate(
		ctx,
		actorAdminID,
		"complete first-use onboarding",
		"onboarding.complete",
		"system",
		"onboarding",
		"Created and enabled the first listener and user.",
		func(state *domain.DesiredState, now time.Time) error {
			if len(state.Listeners) != 0 {
				return fmt.Errorf("%w: onboarding is already complete", ErrConflict)
			}
			state.PublicHost = strings.TrimSpace(spec.PublicHost)
			listener := listenerFromSpec(listenerID, spec.Listener)
			listener.Enabled = true
			listener.CreatedAt = now
			listener.UpdatedAt = now
			user := domain.User{
				ID:         userID,
				ListenerID: listenerID,
				Name:       spec.User.Name,
				Enabled:    true,
				UUID:       spec.User.UUID,
				ExpiresAt:  normalizeExpiry(spec.User.ExpiresAt),
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			listener.Users = []domain.User{user}
			state.Listeners = append(state.Listeners, listener)
			share, shareErr := BuildShare(*state, listenerID, userID)
			if shareErr != nil {
				return fmt.Errorf("%w: %v", ErrValidation, shareErr)
			}
			result.Listener = listener
			result.User = user
			result.Share = share
			return nil
		},
	)
	if err != nil {
		return OnboardingResult{}, err
	}
	return result, nil
}

func (manager *Manager) CreateListener(
	ctx context.Context,
	actorAdminID string,
	spec ListenerSpec,
) (domain.Listener, domain.Revision, error) {
	id, err := domain.GenerateUUID()
	if err != nil {
		return domain.Listener{}, domain.Revision{}, err
	}
	if spec.RealityPrivateKey == "" && spec.RealityPublicKey == "" {
		keypair, err := manager.cli.GenerateRealityKeypair(ctx)
		if err != nil {
			return domain.Listener{}, domain.Revision{}, err
		}
		spec.RealityPrivateKey = keypair.PrivateKey
		spec.RealityPublicKey = keypair.PublicKey
	} else if spec.RealityPrivateKey == "" || spec.RealityPublicKey == "" {
		return domain.Listener{}, domain.Revision{}, fmt.Errorf(
			"%w: both REALITY keys are required",
			ErrValidation,
		)
	}
	if spec.ShortID == "" {
		spec.ShortID, err = domain.GenerateShortID()
		if err != nil {
			return domain.Listener{}, domain.Revision{}, err
		}
	}
	var created domain.Listener
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		"create listener",
		"listener.create",
		"listener",
		id,
		"Created a disabled VLESS REALITY listener.",
		func(state *domain.DesiredState, now time.Time) error {
			created = listenerFromSpec(id, spec)
			created.CreatedAt = now
			created.UpdatedAt = now
			state.Listeners = append(state.Listeners, created)
			return nil
		},
	)
	return created, revision, err
}

func (manager *Manager) UpdateListener(
	ctx context.Context,
	actorAdminID, listenerID string,
	spec ListenerSpec,
) (domain.Listener, domain.Revision, error) {
	var updated domain.Listener
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		"update listener",
		"listener.update",
		"listener",
		listenerID,
		"Updated a VLESS REALITY listener.",
		func(state *domain.DesiredState, now time.Time) error {
			index := listenerIndex(*state, listenerID)
			if index < 0 {
				return ErrNotFound
			}
			current := state.Listeners[index]
			updated = listenerFromSpec(listenerID, spec)
			updated.Enabled = current.Enabled
			updated.Users = current.Users
			updated.CreatedAt = current.CreatedAt
			updated.UpdatedAt = now
			if updated.RealityPrivateKey == "" {
				updated.RealityPrivateKey = current.RealityPrivateKey
			}
			if updated.RealityPublicKey == "" {
				updated.RealityPublicKey = current.RealityPublicKey
			}
			if updated.ShortID == "" {
				updated.ShortID = current.ShortID
			}
			state.Listeners[index] = updated
			return nil
		},
	)
	return updated, revision, err
}

func (manager *Manager) DeleteListener(
	ctx context.Context,
	actorAdminID, listenerID string,
) (domain.Revision, error) {
	return manager.mutate(
		ctx,
		actorAdminID,
		"delete listener",
		"listener.delete",
		"listener",
		listenerID,
		"Deleted a VLESS REALITY listener.",
		func(state *domain.DesiredState, _ time.Time) error {
			index := listenerIndex(*state, listenerID)
			if index < 0 {
				return ErrNotFound
			}
			state.Listeners = append(
				state.Listeners[:index],
				state.Listeners[index+1:]...,
			)
			return nil
		},
	)
}

func (manager *Manager) SetListenerEnabled(
	ctx context.Context,
	actorAdminID, listenerID string,
	enabled bool,
) (domain.Listener, domain.Revision, error) {
	action := "listener.disable"
	summary := "Disabled a VLESS REALITY listener."
	if enabled {
		action = "listener.enable"
		summary = "Enabled a VLESS REALITY listener."
	}
	var updated domain.Listener
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		action,
		action,
		"listener",
		listenerID,
		summary,
		func(state *domain.DesiredState, now time.Time) error {
			index := listenerIndex(*state, listenerID)
			if index < 0 {
				return ErrNotFound
			}
			state.Listeners[index].Enabled = enabled
			state.Listeners[index].UpdatedAt = now
			updated = state.Listeners[index]
			return nil
		},
	)
	return updated, revision, err
}

func (manager *Manager) Users(
	ctx context.Context,
	listenerID string,
) ([]domain.User, error) {
	listener, err := manager.Listener(ctx, listenerID)
	if err != nil {
		return nil, err
	}
	return listener.Users, nil
}

func (manager *Manager) CreateUser(
	ctx context.Context,
	actorAdminID, listenerID string,
	spec UserSpec,
) (domain.User, domain.Revision, error) {
	id, err := domain.GenerateUUID()
	if err != nil {
		return domain.User{}, domain.Revision{}, err
	}
	if spec.UUID == "" {
		spec.UUID, err = domain.GenerateUUID()
		if err != nil {
			return domain.User{}, domain.Revision{}, err
		}
	}
	var created domain.User
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		"create listener user",
		"user.create",
		"listener_user",
		id,
		"Created an enabled listener user.",
		func(state *domain.DesiredState, now time.Time) error {
			index := listenerIndex(*state, listenerID)
			if index < 0 {
				return ErrNotFound
			}
			created = domain.User{
				ID:         id,
				ListenerID: listenerID,
				Name:       spec.Name,
				Enabled:    true,
				UUID:       spec.UUID,
				ExpiresAt:  normalizeExpiry(spec.ExpiresAt),
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			state.Listeners[index].Users = append(
				state.Listeners[index].Users,
				created,
			)
			return nil
		},
	)
	return created, revision, err
}

func (manager *Manager) UpdateUser(
	ctx context.Context,
	actorAdminID, listenerID, userID string,
	spec UserSpec,
) (domain.User, domain.Revision, error) {
	var updated domain.User
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		"update listener user",
		"user.update",
		"listener_user",
		userID,
		"Updated a listener user.",
		func(state *domain.DesiredState, now time.Time) error {
			listenerPosition := listenerIndex(*state, listenerID)
			if listenerPosition < 0 {
				return ErrNotFound
			}
			userPosition := userIndex(
				state.Listeners[listenerPosition],
				userID,
			)
			if userPosition < 0 {
				return ErrNotFound
			}
			current := state.Listeners[listenerPosition].Users[userPosition]
			updated = current
			updated.Name = spec.Name
			updated.ExpiresAt = normalizeExpiry(spec.ExpiresAt)
			updated.UpdatedAt = now
			if spec.UUID != "" {
				updated.UUID = spec.UUID
			}
			state.Listeners[listenerPosition].Users[userPosition] = updated
			return nil
		},
	)
	return updated, revision, err
}

func (manager *Manager) DeleteUser(
	ctx context.Context,
	actorAdminID, listenerID, userID string,
) (domain.Revision, error) {
	return manager.mutate(
		ctx,
		actorAdminID,
		"delete listener user",
		"user.delete",
		"listener_user",
		userID,
		"Deleted a listener user.",
		func(state *domain.DesiredState, _ time.Time) error {
			listenerPosition := listenerIndex(*state, listenerID)
			if listenerPosition < 0 {
				return ErrNotFound
			}
			userPosition := userIndex(
				state.Listeners[listenerPosition],
				userID,
			)
			if userPosition < 0 {
				return ErrNotFound
			}
			users := state.Listeners[listenerPosition].Users
			state.Listeners[listenerPosition].Users = append(
				users[:userPosition],
				users[userPosition+1:]...,
			)
			return nil
		},
	)
}

func (manager *Manager) SetUserEnabled(
	ctx context.Context,
	actorAdminID, listenerID, userID string,
	enabled bool,
) (domain.User, domain.Revision, error) {
	action := "user.disable"
	summary := "Disabled a listener user."
	if enabled {
		action = "user.enable"
		summary = "Enabled a listener user."
	}
	var updated domain.User
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		action,
		action,
		"listener_user",
		userID,
		summary,
		func(state *domain.DesiredState, now time.Time) error {
			listenerPosition := listenerIndex(*state, listenerID)
			if listenerPosition < 0 {
				return ErrNotFound
			}
			userPosition := userIndex(
				state.Listeners[listenerPosition],
				userID,
			)
			if userPosition < 0 {
				return ErrNotFound
			}
			state.Listeners[listenerPosition].Users[userPosition].Enabled = enabled
			state.Listeners[listenerPosition].Users[userPosition].UpdatedAt = now
			updated = state.Listeners[listenerPosition].Users[userPosition]
			return nil
		},
	)
	return updated, revision, err
}

func (manager *Manager) GenerateRealityKeypair(
	ctx context.Context,
) (domain.Keypair, string, error) {
	keypair, err := manager.cli.GenerateRealityKeypair(ctx)
	if err != nil {
		return domain.Keypair{}, "", err
	}
	shortID, err := domain.GenerateShortID()
	if err != nil {
		return domain.Keypair{}, "", err
	}
	return keypair, shortID, nil
}

func (manager *Manager) GenerateUUID() (string, error) {
	return domain.GenerateUUID()
}

func (manager *Manager) Share(
	ctx context.Context,
	listenerID, userID string,
) (Share, error) {
	state, err := manager.currentState(ctx)
	if err != nil {
		return Share{}, err
	}
	share, err := BuildShare(state, listenerID, userID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return Share{}, ErrNotFound
		}
		return Share{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return share, nil
}

func (manager *Manager) EditableSettings(
	ctx context.Context,
) (EditableSettings, error) {
	settings, err := manager.store.Settings(ctx)
	if err != nil {
		return EditableSettings{}, err
	}
	return EditableSettings{
		PanelTitle:   settings.PanelTitle,
		UILanguage:   settings.UILanguage,
		PublicHost:   settings.PublicHost,
		CookieSecure: settings.CookieSecure,
	}, nil
}

// UILanguage is the narrow public preference read used before authentication
// so the first page can select the configured default without loading secrets.
func (manager *Manager) UILanguage(ctx context.Context) (string, error) {
	return manager.store.UILanguage(ctx)
}

func (manager *Manager) UpdateSettings(
	ctx context.Context,
	actorAdminID string,
	settings EditableSettings,
) (domain.Revision, error) {
	if strings.TrimSpace(settings.PanelTitle) == "" ||
		len(settings.PanelTitle) > 80 {
		return domain.Revision{}, fmt.Errorf(
			"%w: panel title must contain between 1 and 80 bytes",
			ErrValidation,
		)
	}
	switch settings.UILanguage {
	case "auto", "en-US", "zh-CN":
	default:
		return domain.Revision{}, fmt.Errorf(
			"%w: UI language must be auto, en-US, or zh-CN",
			ErrValidation,
		)
	}
	return manager.mutate(
		ctx,
		actorAdminID,
		"update managed settings",
		"settings.update",
		"settings",
		"managed",
		"Updated managed panel and public endpoint settings.",
		func(state *domain.DesiredState, _ time.Time) error {
			state.PanelTitle = settings.PanelTitle
			state.UILanguage = settings.UILanguage
			state.PublicHost = settings.PublicHost
			state.CookieSecure = settings.CookieSecure
			return nil
		},
	)
}

func (manager *Manager) EndpointSettings(
	ctx context.Context,
) (EndpointSettingsState, error) {
	return manager.store.EndpointSettings(ctx)
}

func (manager *Manager) UpdateEndpointSettings(
	ctx context.Context,
	actorAdminID string,
	settings EndpointSettings,
	expectedGeneration int64,
) (EndpointSettingsState, error) {
	if expectedGeneration < 1 {
		return EndpointSettingsState{}, fmt.Errorf(
			"%w: endpoint settings generation is required",
			ErrValidation,
		)
	}
	if err := domain.ValidateBindEndpoint(settings.PanelUIBind, "panel UI bind endpoint"); err != nil {
		return EndpointSettingsState{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if err := domain.ValidateBindEndpoint(
		settings.MihomoExternalControllerBind,
		"Mihomo external-controller bind endpoint",
	); err != nil {
		return EndpointSettingsState{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if err := domain.ValidateConnectEndpoint(
		settings.MihomoControllerConnect,
		"m-ui Mihomo controller connect endpoint",
	); err != nil {
		return EndpointSettingsState{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if err := domain.ValidateControllerEndpointPair(
		settings.MihomoExternalControllerBind,
		settings.MihomoControllerConnect,
	); err != nil {
		return EndpointSettingsState{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if err := domain.ValidateCORSOrigins(settings.ExternalControllerCORSOrigins); err != nil {
		return EndpointSettingsState{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	current, err := manager.store.EndpointSettings(ctx)
	if err != nil {
		return EndpointSettingsState{}, err
	}
	if current.Active.Generation != expectedGeneration {
		return EndpointSettingsState{}, fmt.Errorf(
			"%w: endpoint settings changed; reload and try again",
			ErrConflict,
		)
	}
	if endpointSettingsEqual(current.Active, settings) {
		return current, nil
	}
	effectiveAt := manager.clock().UTC()
	_, err = manager.publisher.Publish(ctx, publisher.Request{
		Reason:          "update endpoint settings",
		ActorAdminID:    actorAdminID,
		AuditAction:     "settings.endpoints.update",
		AuditResource:   "settings",
		AuditResourceID: "endpoints",
		AuditSummary:    "Updated m-ui and Mihomo endpoint settings; restart requirements were recorded.",
		EffectiveAt:     &effectiveAt,
		RestartRequired: true,
		Mutate: func(ctx context.Context, transaction store.PublicationTransaction) error {
			state, err := transaction.DesiredState(ctx, effectiveAt)
			if err != nil {
				return err
			}
			if state.EndpointGeneration != expectedGeneration {
				return fmt.Errorf(
					"%w: endpoint settings changed; reload and try again",
					ErrConflict,
				)
			}
			state.AsOf = effectiveAt
			state.PanelUIBind = settings.PanelUIBind
			state.MihomoExternalControllerBind = settings.MihomoExternalControllerBind
			state.MihomoControllerConnect = settings.MihomoControllerConnect
			state.ExternalControllerCORSOrigins = append(
				[]string(nil),
				settings.ExternalControllerCORSOrigins...,
			)
			if err := state.Validate(); err != nil {
				return fmt.Errorf("%w: %v", ErrValidation, err)
			}
			return transaction.ReplaceDesiredState(ctx, state)
		},
	})
	if err != nil {
		return EndpointSettingsState{}, err
	}
	return manager.store.EndpointSettings(ctx)
}

func endpointSettingsEqual(left, right EndpointSettings) bool {
	if !left.PanelUIBind.Equal(right.PanelUIBind) ||
		!left.MihomoExternalControllerBind.Equal(right.MihomoExternalControllerBind) ||
		!left.MihomoControllerConnect.Equal(right.MihomoControllerConnect) ||
		len(left.ExternalControllerCORSOrigins) != len(right.ExternalControllerCORSOrigins) {
		return false
	}
	for index := range left.ExternalControllerCORSOrigins {
		if left.ExternalControllerCORSOrigins[index] != right.ExternalControllerCORSOrigins[index] {
			return false
		}
	}
	return true
}

func (manager *Manager) PreviewConfig(
	ctx context.Context,
	reveal bool,
) ([]byte, string, error) {
	compiled, _, err := manager.publisher.CompileCurrent(
		ctx,
		manager.clock().UTC(),
	)
	if err != nil {
		return nil, "", err
	}
	hash := publisher.SHA256(compiled)
	if reveal {
		return compiled, hash, nil
	}
	return []byte(redact.Text(string(compiled))), hash, nil
}

func (manager *Manager) ValidateConfig(
	ctx context.Context,
) (string, error) {
	compiled, err := manager.publisher.ValidateCurrent(
		ctx,
		manager.clock().UTC(),
	)
	if err != nil {
		return "", err
	}
	return publisher.SHA256(compiled), nil
}

func (manager *Manager) Revisions(
	ctx context.Context,
	limit, offset int,
) ([]domain.Revision, error) {
	return manager.store.Revisions(ctx, limit, offset)
}

func (manager *Manager) Revision(
	ctx context.Context,
	revisionID string,
) (domain.Revision, error) {
	revision, err := manager.store.Revision(ctx, revisionID)
	if errors.Is(err, store.ErrNotFound) {
		return domain.Revision{}, ErrNotFound
	}
	return revision, err
}

func (manager *Manager) Rollback(
	ctx context.Context,
	actorAdminID, revisionID string,
) (domain.Revision, error) {
	revision, err := manager.publisher.Rollback(ctx, revisionID, actorAdminID)
	if errors.Is(err, store.ErrNotFound) {
		return domain.Revision{}, ErrNotFound
	}
	return revision, err
}

func (manager *Manager) AuditEntries(
	ctx context.Context,
	limit, offset int,
) ([]store.AuditEntry, error) {
	return manager.store.AuditEntries(ctx, limit, offset)
}

func (manager *Manager) RuntimeStatus(
	ctx context.Context,
) (RuntimeStatus, error) {
	systemState, err := manager.store.SystemState(ctx)
	if err != nil {
		return RuntimeStatus{}, err
	}
	status := manager.runtime.Snapshot()
	status.Degraded = systemState.Degraded
	status.DegradedReason = systemState.DegradedReason
	if status.ObservedAt.IsZero() {
		status.ObservedAt = manager.clock().UTC()
	}
	return status, nil
}

func (manager *Manager) RuntimeLogs(
	ctx context.Context,
	limit int,
) ([]mihomo.LogEntry, error) {
	return manager.process.RecentLogs(ctx, limit)
}

func (manager *Manager) RuntimeAction(
	ctx context.Context,
	actorAdminID, action string,
) error {
	if manager.readyGuard == nil {
		return errors.New("runtime readiness guard is required")
	}
	readyRelease, err := manager.readyGuard(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if readyRelease != nil {
			_ = readyRelease()
		}
	}()
	release, lockErr := manager.coordinator.TryAcquire()
	if lockErr != nil {
		return lockErr
	}
	defer release()
	err = manager.runtimeBoundary().runLocked(ctx, action)
	result := "success"
	if err != nil {
		result = "failure"
	}
	auditID, idErr := domain.GenerateUUID()
	if idErr == nil {
		idErr = manager.store.RecordAudit(ctx, store.AuditEntry{
			ID:              auditID,
			ActorAdminID:    actorAdminID,
			Action:          "runtime." + action,
			ResourceType:    "mihomo",
			ResourceID:      "",
			Result:          result,
			SummaryRedacted: "Requested Mihomo runtime " + action + ".",
			CreatedAt:       manager.clock().UTC(),
		})
	}
	return errors.Join(err, idErr)
}

// StartManagedProcess applies the managed-mode startup boundary. The process
// is started by the application before the HTTP listener is bound; if the
// active YAML contains a pending Mihomo endpoint candidate, startup must
// health-check it and clear only the Mihomo side with the same CAS used by
// explicit runtime actions.
func (manager *Manager) StartManagedProcess(ctx context.Context) error {
	return manager.runtimeBoundary().Start(ctx)
}

func (manager *Manager) runtimeBoundary() *RuntimeBoundary {
	if manager.boundary != nil {
		return manager.boundary
	}
	return &RuntimeBoundary{
		store:       manager.store,
		controller:  manager.controller,
		process:     manager.process,
		coordinator: manager.coordinator,
		healthLimit: 10 * time.Second,
		healthStep:  100 * time.Millisecond,
	}
}

func (manager *Manager) CoreStatus(
	ctx context.Context,
) (coremanagement.Status, error) {
	if manager.core == nil {
		return coremanagement.Status{}, errors.New("core management is unavailable")
	}
	return manager.core.Status(ctx)
}

func (manager *Manager) CoreSettings(
	ctx context.Context,
) (coremanagement.Settings, error) {
	if manager.core == nil {
		return coremanagement.Settings{}, errors.New("core management is unavailable")
	}
	return manager.core.Settings(ctx)
}

func (manager *Manager) UpdateCoreSettings(
	ctx context.Context,
	actorAdminID string,
	settings coremanagement.Settings,
) error {
	if manager.core == nil {
		return errors.New("core management is unavailable")
	}
	return manager.core.UpdateSettings(ctx, actorAdminID, settings)
}

func (manager *Manager) CheckCore(
	ctx context.Context,
	actorAdminID string,
) (coremanagement.ReleaseIdentity, error) {
	if manager.core == nil {
		return coremanagement.ReleaseIdentity{}, errors.New("core management is unavailable")
	}
	return manager.core.Check(ctx, actorAdminID)
}

func (manager *Manager) UpdateCore(
	ctx context.Context,
	actorAdminID string,
) (coremanagement.Manifest, bool, error) {
	if manager.core == nil {
		return coremanagement.Manifest{}, false, errors.New("core management is unavailable")
	}
	if err := manager.rejectPendingMihomoRestart(ctx); err != nil {
		return coremanagement.Manifest{}, false, err
	}
	return manager.core.Update(ctx, actorAdminID)
}

func (manager *Manager) RollbackCore(
	ctx context.Context,
	actorAdminID string,
) (coremanagement.Manifest, error) {
	if manager.core == nil {
		return coremanagement.Manifest{}, errors.New("core management is unavailable")
	}
	if err := manager.rejectPendingMihomoRestart(ctx); err != nil {
		return coremanagement.Manifest{}, err
	}
	return manager.core.Rollback(ctx, actorAdminID)
}

func (manager *Manager) rejectPendingMihomoRestart(ctx context.Context) error {
	required, err := manager.store.MihomoRestartRequired(ctx)
	if err != nil {
		return err
	}
	if required {
		return publisher.ErrMihomoRestartRequired
	}
	return nil
}

func (manager *Manager) TestCore(
	ctx context.Context,
) (string, error) {
	return manager.cli.Version(ctx)
}

func (manager *Manager) TestController(
	ctx context.Context,
) (mihomo.Version, error) {
	return manager.controller.Version(ctx)
}

func (manager *Manager) currentState(
	ctx context.Context,
) (domain.DesiredState, error) {
	return manager.store.ReadDesiredState(ctx, manager.clock().UTC())
}

func (manager *Manager) mutate(
	ctx context.Context,
	actorAdminID, reason, auditAction, auditResource, auditResourceID, auditSummary string,
	mutation func(*domain.DesiredState, time.Time) error,
) (domain.Revision, error) {
	effectiveAt := manager.clock().UTC()
	revision, err := manager.publisher.Publish(ctx, publisher.Request{
		Reason:          reason,
		ActorAdminID:    actorAdminID,
		AuditAction:     auditAction,
		AuditResource:   auditResource,
		AuditResourceID: auditResourceID,
		AuditSummary:    auditSummary,
		EffectiveAt:     &effectiveAt,
		Mutate: func(
			ctx context.Context,
			transaction store.PublicationTransaction,
		) error {
			state, err := transaction.DesiredState(ctx, effectiveAt)
			if err != nil {
				return err
			}
			state.AsOf = effectiveAt
			if err := mutation(&state, effectiveAt); err != nil {
				return err
			}
			if err := state.Validate(); err != nil {
				return fmt.Errorf("%w: %v", ErrValidation, err)
			}
			return transaction.ReplaceDesiredState(ctx, state)
		},
	})
	return revision, err
}

func listenerFromSpec(id string, spec ListenerSpec) domain.Listener {
	return domain.Listener{
		ID:                 id,
		Name:               spec.Name,
		ListenAddress:      spec.ListenAddress,
		ListenPort:         spec.ListenPort,
		PublicHostOverride: spec.PublicHostOverride,
		PublicPortOverride: spec.PublicPortOverride,
		ServerName:         spec.ServerName,
		RealityDest:        spec.RealityDest,
		RealityPrivateKey:  spec.RealityPrivateKey,
		RealityPublicKey:   spec.RealityPublicKey,
		ShortID:            spec.ShortID,
		UDPEnabled:         spec.UDPEnabled,
	}
}

func listenerIndex(state domain.DesiredState, listenerID string) int {
	for index := range state.Listeners {
		if state.Listeners[index].ID == listenerID {
			return index
		}
	}
	return -1
}

func userIndex(listener domain.Listener, userID string) int {
	for index := range listener.Users {
		if listener.Users[index].ID == userID {
			return index
		}
	}
	return -1
}

func normalizeExpiry(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}
