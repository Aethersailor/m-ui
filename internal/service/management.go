package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	coremanagement "github.com/Aethersailor/m-ui/internal/core"
	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/operation"
	"github.com/Aethersailor/m-ui/internal/protocol"
	"github.com/Aethersailor/m-ui/internal/publisher"
	"github.com/Aethersailor/m-ui/internal/redact"
	"github.com/Aethersailor/m-ui/internal/store"
)

var (
	ErrNotFound   = errors.New("managed resource not found")
	ErrValidation = errors.New("managed state validation failed")
	ErrConflict   = errors.New("managed state conflict")
)

type NodeSpec struct {
	Name           string
	Enabled        bool
	ListenAddress  string
	Port           string
	Protocol       domain.ProtocolKind
	VLESS          *domain.VLESSSpec
	Hysteria2      *domain.Hysteria2Spec
	VMess          *domain.VMessSpec
	Trojan         *domain.TrojanSpec
	Shadowsocks    *domain.ShadowsocksSpec
	Users          []UserSpec
	AccessProfiles []AccessProfileSpec
	Generation     int64
}

type UserSpec struct {
	Name        string
	Enabled     bool
	VLESS       *domain.VLESSCredential
	Hysteria2   *domain.Hysteria2Credential
	VMess       *domain.VMessCredential
	Trojan      *domain.TrojanCredential
	Shadowsocks *domain.ShadowsocksCredential
	ExpiresAt   *time.Time
}

type AccessProfileSpec struct {
	ID             string
	Name           string
	Default        bool
	PublicHost     string
	PublicPort     uint16
	ServerName     string
	Fingerprint    string
	PacketEncoding string
	AllowInsecure  bool
}

type OnboardingSpec struct {
	PublicHost string
	Node       NodeSpec
}

type OnboardingResult struct {
	Node     domain.Node
	User     domain.NodeUser
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
	protocols   protocol.Registry
}

// ReserveApplicationRestart prevents a Web-requested m-ui restart from
// interrupting a configuration publication, Mihomo lifecycle action, or core
// update. The caller intentionally keeps the returned lease until the process
// exits; the cross-process lock is then released by the operating system.
func (manager *Manager) ReserveApplicationRestart() (func(), error) {
	return manager.coordinator.TryAcquire()
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
		protocols:   protocol.DefaultRegistry(),
	}, nil
}

func (manager *Manager) Nodes(ctx context.Context) ([]domain.Node, error) {
	state, err := manager.currentState(ctx)
	if err != nil {
		return nil, err
	}
	return state.Nodes, nil
}

func (manager *Manager) Node(
	ctx context.Context,
	nodeID string,
) (domain.Node, error) {
	state, err := manager.currentState(ctx)
	if err != nil {
		return domain.Node{}, err
	}
	index := nodeIndex(state, nodeID)
	if index < 0 {
		return domain.Node{}, ErrNotFound
	}
	return state.Nodes[index], nil
}

func (manager *Manager) CompleteOnboarding(
	ctx context.Context,
	actorAdminID string,
	spec OnboardingSpec,
) (OnboardingResult, error) {
	nodeID, err := domain.GenerateUUID()
	if err != nil {
		return OnboardingResult{}, err
	}
	spec.Node.Enabled = true
	node, err := manager.nodeFromSpec(ctx, nodeID, spec.Node, time.Time{})
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
		"Created and enabled the first protocol-aware node and user.",
		func(state *domain.DesiredState, now time.Time) error {
			if len(state.Nodes) != 0 {
				return fmt.Errorf("%w: onboarding is already complete", ErrConflict)
			}
			state.PublicHost = strings.TrimSpace(spec.PublicHost)
			node.CreatedAt = now
			node.UpdatedAt = now
			for index := range node.Users {
				node.Users[index].CreatedAt = now
				node.Users[index].UpdatedAt = now
			}
			for index := range node.AccessProfiles {
				node.AccessProfiles[index].CreatedAt = now
				node.AccessProfiles[index].UpdatedAt = now
			}
			state.Nodes = append(state.Nodes, node)
			if len(node.Users) == 0 {
				return fmt.Errorf("%w: onboarding requires one user", ErrValidation)
			}
			share, shareErr := manager.buildShare(*state, nodeID, node.Users[0].ID, "")
			if shareErr != nil {
				return fmt.Errorf("%w: %v", ErrValidation, shareErr)
			}
			result.Node = node
			result.User = node.Users[0]
			result.Share = share
			return nil
		},
	)
	if err != nil {
		return OnboardingResult{}, err
	}
	return result, nil
}

func (manager *Manager) CreateNode(
	ctx context.Context,
	actorAdminID string,
	spec NodeSpec,
) (domain.Node, domain.Revision, error) {
	id, err := domain.GenerateUUID()
	if err != nil {
		return domain.Node{}, domain.Revision{}, err
	}
	created, err := manager.nodeFromSpec(ctx, id, spec, time.Time{})
	if err != nil {
		return domain.Node{}, domain.Revision{}, err
	}
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		"create node",
		"node.create",
		"node",
		id,
		"Created a protocol-aware node.",
		func(state *domain.DesiredState, now time.Time) error {
			created.CreatedAt = now
			created.UpdatedAt = now
			for index := range created.Users {
				created.Users[index].CreatedAt = now
				created.Users[index].UpdatedAt = now
			}
			for index := range created.AccessProfiles {
				created.AccessProfiles[index].CreatedAt = now
				created.AccessProfiles[index].UpdatedAt = now
			}
			state.Nodes = append(state.Nodes, created)
			return nil
		},
	)
	return created, revision, err
}

func (manager *Manager) UpdateNode(
	ctx context.Context,
	actorAdminID, nodeID string,
	spec NodeSpec,
) (domain.Node, domain.Revision, error) {
	var updated domain.Node
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		"update node",
		"node.update",
		"node",
		nodeID,
		"Updated a protocol-aware node.",
		func(state *domain.DesiredState, now time.Time) error {
			index := nodeIndex(*state, nodeID)
			if index < 0 {
				return ErrNotFound
			}
			current := state.Nodes[index]
			if spec.Generation != 0 && spec.Generation != current.Generation {
				return fmt.Errorf("%w: node generation changed", ErrConflict)
			}
			var buildErr error
			updated, buildErr = manager.nodeFromSpec(ctx, nodeID, spec, now)
			if buildErr != nil {
				return buildErr
			}
			if spec.Users == nil {
				updated.Users = current.Users
			}
			if spec.AccessProfiles == nil {
				updated.AccessProfiles = current.AccessProfiles
			}
			updated.CreatedAt = current.CreatedAt
			updated.UpdatedAt = now
			updated.Generation = current.Generation + 1
			preserveNodeSecrets(&updated, current)
			state.Nodes[index] = updated
			return nil
		},
	)
	return updated, revision, err
}

func (manager *Manager) DeleteNode(
	ctx context.Context,
	actorAdminID, nodeID string,
) (domain.Revision, error) {
	return manager.mutate(
		ctx,
		actorAdminID,
		"delete node",
		"node.delete",
		"node",
		nodeID,
		"Deleted a protocol-aware node.",
		func(state *domain.DesiredState, _ time.Time) error {
			index := nodeIndex(*state, nodeID)
			if index < 0 {
				return ErrNotFound
			}
			state.Nodes = append(
				state.Nodes[:index],
				state.Nodes[index+1:]...,
			)
			return nil
		},
	)
}

func (manager *Manager) SetNodeEnabled(
	ctx context.Context,
	actorAdminID, nodeID string,
	enabled bool,
) (domain.Node, domain.Revision, error) {
	action := "node.disable"
	summary := "Disabled a node."
	if enabled {
		action = "node.enable"
		summary = "Enabled a node."
	}
	var updated domain.Node
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		action,
		action,
		"node",
		nodeID,
		summary,
		func(state *domain.DesiredState, now time.Time) error {
			index := nodeIndex(*state, nodeID)
			if index < 0 {
				return ErrNotFound
			}
			state.Nodes[index].Enabled = enabled
			state.Nodes[index].Generation++
			state.Nodes[index].UpdatedAt = now
			updated = state.Nodes[index]
			return nil
		},
	)
	return updated, revision, err
}

func (manager *Manager) Users(
	ctx context.Context,
	nodeID string,
) ([]domain.NodeUser, error) {
	node, err := manager.Node(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return node.Users, nil
}

func (manager *Manager) CreateUser(
	ctx context.Context,
	actorAdminID, nodeID string,
	spec UserSpec,
) (domain.NodeUser, domain.Revision, error) {
	id, err := domain.GenerateUUID()
	if err != nil {
		return domain.NodeUser{}, domain.Revision{}, err
	}
	var created domain.NodeUser
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		"create node user",
		"user.create",
		"node_user",
		id,
		"Created an enabled node user.",
		func(state *domain.DesiredState, now time.Time) error {
			index := nodeIndex(*state, nodeID)
			if index < 0 {
				return ErrNotFound
			}
			var buildErr error
			created, buildErr = userFromSpec(
				id, nodeID, state.Nodes[index].Protocol, spec, now,
				shadowsocksCipher(state.Nodes[index]),
			)
			if buildErr != nil {
				return buildErr
			}
			state.Nodes[index].Users = append(state.Nodes[index].Users, created)
			state.Nodes[index].Generation++
			state.Nodes[index].UpdatedAt = now
			return nil
		},
	)
	return created, revision, err
}

func (manager *Manager) UpdateUser(
	ctx context.Context,
	actorAdminID, nodeID, userID string,
	spec UserSpec,
) (domain.NodeUser, domain.Revision, error) {
	var updated domain.NodeUser
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		"update node user",
		"user.update",
		"node_user",
		userID,
		"Updated a node user.",
		func(state *domain.DesiredState, now time.Time) error {
			nodePosition := nodeIndex(*state, nodeID)
			if nodePosition < 0 {
				return ErrNotFound
			}
			userPosition := userIndex(
				state.Nodes[nodePosition],
				userID,
			)
			if userPosition < 0 {
				return ErrNotFound
			}
			current := state.Nodes[nodePosition].Users[userPosition]
			updated = current
			updated.Name = spec.Name
			updated.Enabled = spec.Enabled
			updated.ExpiresAt = normalizeExpiry(spec.ExpiresAt)
			updated.UpdatedAt = now
			if spec.VLESS != nil {
				updated.VLESS = spec.VLESS
			}
			if spec.Hysteria2 != nil {
				updated.Hysteria2 = spec.Hysteria2
			}
			if spec.VMess != nil {
				updated.VMess = spec.VMess
			}
			if spec.Trojan != nil {
				updated.Trojan = spec.Trojan
			}
			if spec.Shadowsocks != nil {
				updated.Shadowsocks = spec.Shadowsocks
			}
			state.Nodes[nodePosition].Users[userPosition] = updated
			state.Nodes[nodePosition].Generation++
			state.Nodes[nodePosition].UpdatedAt = now
			return nil
		},
	)
	return updated, revision, err
}

func (manager *Manager) DeleteUser(
	ctx context.Context,
	actorAdminID, nodeID, userID string,
) (domain.Revision, error) {
	return manager.mutate(
		ctx,
		actorAdminID,
		"delete node user",
		"user.delete",
		"node_user",
		userID,
		"Deleted a node user.",
		func(state *domain.DesiredState, now time.Time) error {
			nodePosition := nodeIndex(*state, nodeID)
			if nodePosition < 0 {
				return ErrNotFound
			}
			userPosition := userIndex(
				state.Nodes[nodePosition],
				userID,
			)
			if userPosition < 0 {
				return ErrNotFound
			}
			users := state.Nodes[nodePosition].Users
			state.Nodes[nodePosition].Users = append(
				users[:userPosition],
				users[userPosition+1:]...,
			)
			state.Nodes[nodePosition].Generation++
			state.Nodes[nodePosition].UpdatedAt = now
			return nil
		},
	)
}

func (manager *Manager) SetUserEnabled(
	ctx context.Context,
	actorAdminID, nodeID, userID string,
	enabled bool,
) (domain.NodeUser, domain.Revision, error) {
	action := "user.disable"
	summary := "Disabled a listener user."
	if enabled {
		action = "user.enable"
		summary = "Enabled a listener user."
	}
	var updated domain.NodeUser
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		action,
		action,
		"node_user",
		userID,
		summary,
		func(state *domain.DesiredState, now time.Time) error {
			nodePosition := nodeIndex(*state, nodeID)
			if nodePosition < 0 {
				return ErrNotFound
			}
			userPosition := userIndex(
				state.Nodes[nodePosition],
				userID,
			)
			if userPosition < 0 {
				return ErrNotFound
			}
			state.Nodes[nodePosition].Users[userPosition].Enabled = enabled
			state.Nodes[nodePosition].Users[userPosition].UpdatedAt = now
			state.Nodes[nodePosition].Generation++
			state.Nodes[nodePosition].UpdatedAt = now
			updated = state.Nodes[nodePosition].Users[userPosition]
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

func (manager *Manager) Capabilities() protocol.CapabilityManifest {
	return manager.protocols.Capabilities()
}

func (manager *Manager) Share(
	ctx context.Context,
	nodeID, userID string,
) (Share, error) {
	state, err := manager.currentState(ctx)
	if err != nil {
		return Share{}, err
	}
	share, err := manager.buildShare(state, nodeID, userID, "")
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return Share{}, ErrNotFound
		}
		return Share{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return share, nil
}

func (manager *Manager) ShareWithProfile(
	ctx context.Context,
	nodeID, userID, profileID string,
) (Share, error) {
	state, err := manager.currentState(ctx)
	if err != nil {
		return Share{}, err
	}
	share, err := manager.buildShare(state, nodeID, userID, profileID)
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

func (manager *Manager) nodeFromSpec(
	ctx context.Context,
	id string,
	spec NodeSpec,
	now time.Time,
) (domain.Node, error) {
	if spec.Protocol == "" {
		spec.Protocol = domain.ProtocolVLESS
	}
	node := domain.Node{
		ID: id, Name: spec.Name, Enabled: spec.Enabled,
		ListenAddress: spec.ListenAddress, Port: spec.Port,
		Protocol: spec.Protocol, SchemaVersion: domain.NodeSchemaVersion,
		VLESS: spec.VLESS, Hysteria2: spec.Hysteria2, VMess: spec.VMess,
		Trojan: spec.Trojan, Shadowsocks: spec.Shadowsocks,
		Generation: 1,
	}
	if node.ListenAddress == "" {
		node.ListenAddress = "0.0.0.0"
	}
	if node.Port == "" {
		node.Port = "443"
	}
	if security := nodeStreamSecurity(&node); security != nil &&
		security.Type == domain.VLESSSecurityReality && security.Reality != nil {
		reality := security.Reality
		if reality.PrivateKey == "" && reality.PublicKey == "" {
			if now.IsZero() {
				keypair, err := manager.cli.GenerateRealityKeypair(ctx)
				if err != nil {
					return domain.Node{}, err
				}
				reality.PrivateKey = keypair.PrivateKey
				reality.PublicKey = keypair.PublicKey
			}
		} else if now.IsZero() && (reality.PrivateKey == "" || reality.PublicKey == "") {
			return domain.Node{}, fmt.Errorf("%w: both REALITY keys are required", ErrValidation)
		}
		if len(reality.ShortIDs) == 0 {
			shortID, err := domain.GenerateShortID()
			if err != nil {
				return domain.Node{}, err
			}
			reality.ShortIDs = []string{shortID}
		}
	}
	for _, userSpec := range spec.Users {
		userID, err := domain.GenerateUUID()
		if err != nil {
			return domain.Node{}, err
		}
		user, err := userFromSpec(userID, id, node.Protocol, userSpec, now, shadowsocksCipher(node))
		if err != nil {
			return domain.Node{}, err
		}
		node.Users = append(node.Users, user)
	}
	for _, profileSpec := range spec.AccessProfiles {
		profile, err := accessProfileFromSpec(id, profileSpec, now)
		if err != nil {
			return domain.Node{}, err
		}
		node.AccessProfiles = append(node.AccessProfiles, profile)
	}
	if spec.AccessProfiles == nil || len(node.AccessProfiles) == 0 {
		port, ok := domain.SinglePort(node.Port)
		if !ok {
			return domain.Node{}, fmt.Errorf("%w: an explicit access profile is required for a port range", ErrValidation)
		}
		profileID, err := domain.GenerateUUID()
		if err != nil {
			return domain.Node{}, err
		}
		serverName := ""
		if security := nodeStreamSecurity(&node); security != nil && security.Reality != nil && len(security.Reality.ServerNames) > 0 {
			serverName = security.Reality.ServerNames[0]
		}
		node.AccessProfiles = []domain.AccessProfile{{
			ID: profileID, NodeID: id, Name: "default", Default: true,
			PublicPort: port, ServerName: serverName,
			Fingerprint: domain.ClientFingerprint, PacketEncoding: domain.PacketEncodingXUDP,
			CreatedAt: now, UpdatedAt: now,
		}}
	}
	return node, nil
}

func userFromSpec(id, nodeID string, kind domain.ProtocolKind, spec UserSpec, now time.Time, ssCipher ...string) (domain.NodeUser, error) {
	user := domain.NodeUser{
		ID: id, NodeID: nodeID, Name: spec.Name, Enabled: spec.Enabled,
		VLESS: spec.VLESS, Hysteria2: spec.Hysteria2, VMess: spec.VMess,
		Trojan: spec.Trojan, Shadowsocks: spec.Shadowsocks,
		ExpiresAt: normalizeExpiry(spec.ExpiresAt), CreatedAt: now, UpdatedAt: now,
	}
	switch kind {
	case domain.ProtocolVLESS:
		if user.VLESS == nil {
			user.VLESS = &domain.VLESSCredential{Flow: domain.VLESSFlowVision}
		}
		if user.VLESS.UUID == "" {
			value, err := domain.GenerateUUID()
			if err != nil {
				return domain.NodeUser{}, err
			}
			user.VLESS.UUID = value
		}
		user.Hysteria2 = nil
		user.VMess, user.Trojan, user.Shadowsocks = nil, nil, nil
	case domain.ProtocolHysteria2:
		if user.Hysteria2 == nil {
			user.Hysteria2 = &domain.Hysteria2Credential{}
		}
		if user.Hysteria2.Password == "" {
			value, err := domain.GenerateUUID()
			if err != nil {
				return domain.NodeUser{}, err
			}
			user.Hysteria2.Password = strings.ReplaceAll(value, "-", "")
		}
		user.VLESS = nil
		user.VMess, user.Trojan, user.Shadowsocks = nil, nil, nil
	case domain.ProtocolVMess:
		if user.VMess == nil {
			user.VMess = &domain.VMessCredential{Cipher: "auto"}
		}
		if user.VMess.UUID == "" {
			value, err := domain.GenerateUUID()
			if err != nil {
				return domain.NodeUser{}, err
			}
			user.VMess.UUID = value
		}
		if user.VMess.Cipher == "" {
			user.VMess.Cipher = "auto"
		}
		user.VLESS, user.Hysteria2, user.Trojan, user.Shadowsocks = nil, nil, nil, nil
	case domain.ProtocolTrojan:
		if user.Trojan == nil {
			user.Trojan = &domain.TrojanCredential{}
		}
		if user.Trojan.Password == "" {
			value, err := domain.GenerateUUID()
			if err != nil {
				return domain.NodeUser{}, err
			}
			user.Trojan.Password = strings.ReplaceAll(value, "-", "")
		}
		user.VLESS, user.Hysteria2, user.VMess, user.Shadowsocks = nil, nil, nil, nil
	case domain.ProtocolShadowsocks:
		if user.Shadowsocks == nil {
			user.Shadowsocks = &domain.ShadowsocksCredential{}
		}
		if user.Shadowsocks.Password == "" {
			keyBytes := 32
			if len(ssCipher) > 0 && ssCipher[0] == "2022-blake3-aes-128-gcm" {
				keyBytes = 16
			}
			value := make([]byte, keyBytes)
			if _, err := rand.Read(value); err != nil {
				return domain.NodeUser{}, fmt.Errorf("generate Shadowsocks password: %w", err)
			}
			user.Shadowsocks.Password = base64.StdEncoding.EncodeToString(value)
		}
		user.VLESS, user.Hysteria2, user.VMess, user.Trojan = nil, nil, nil, nil
	default:
		return domain.NodeUser{}, fmt.Errorf("%w: unsupported protocol %q", ErrValidation, kind)
	}
	return user, nil
}

func accessProfileFromSpec(nodeID string, spec AccessProfileSpec, now time.Time) (domain.AccessProfile, error) {
	id := spec.ID
	if id == "" {
		var err error
		id, err = domain.GenerateUUID()
		if err != nil {
			return domain.AccessProfile{}, err
		}
	}
	name := spec.Name
	if name == "" {
		name = "default"
	}
	return domain.AccessProfile{
		ID: id, NodeID: nodeID, Name: name, Default: spec.Default,
		PublicHost: spec.PublicHost, PublicPort: spec.PublicPort, ServerName: spec.ServerName,
		Fingerprint: spec.Fingerprint, PacketEncoding: spec.PacketEncoding,
		AllowInsecure: spec.AllowInsecure, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func preserveNodeSecrets(updated *domain.Node, current domain.Node) {
	if updated.Protocol != current.Protocol {
		return
	}
	u, c := nodeStreamSecurity(updated), nodeStreamSecurity(&current)
	if u != nil && c != nil {
		if u.TLS != nil && c.TLS != nil && u.TLS.PrivateKey == "" {
			u.TLS.PrivateKey = c.TLS.PrivateKey
		}
		if u.TLS != nil && c.TLS != nil && u.TLS.ECHKey == "" {
			u.TLS.ECHKey = c.TLS.ECHKey
		}
		if u.Reality != nil && c.Reality != nil {
			if u.Reality.PrivateKey == "" {
				u.Reality.PrivateKey = c.Reality.PrivateKey
			}
			if u.Reality.PublicKey == "" {
				u.Reality.PublicKey = c.Reality.PublicKey
			}
		}
		if u.ShadowTLS != nil && c.ShadowTLS != nil && u.ShadowTLS.Password == "" {
			u.ShadowTLS.Password = c.ShadowTLS.Password
		}
		if u.ShadowTLS != nil && c.ShadowTLS != nil {
			passwords := make(map[string]string, len(c.ShadowTLS.Users))
			for _, item := range c.ShadowTLS.Users {
				passwords[item.Name] = item.Password
			}
			for index := range u.ShadowTLS.Users {
				if u.ShadowTLS.Users[index].Password == "" {
					u.ShadowTLS.Users[index].Password = passwords[u.ShadowTLS.Users[index].Name]
				}
			}
		}
		if u.ResTLS != nil && c.ResTLS != nil && u.ResTLS.Password == "" {
			u.ResTLS.Password = c.ResTLS.Password
		}
		if u.JLS != nil && c.JLS != nil {
			passwords := make(map[string]string, len(c.JLS.Users))
			for _, item := range c.JLS.Users {
				passwords[item.Username] = item.Password
			}
			for index := range u.JLS.Users {
				if u.JLS.Users[index].Password == "" {
					u.JLS.Users[index].Password = passwords[u.JLS.Users[index].Username]
				}
			}
		}
	}
	if updated.Trojan != nil && current.Trojan != nil &&
		updated.Trojan.Shadowsocks.Enabled && current.Trojan.Shadowsocks.Enabled &&
		updated.Trojan.Shadowsocks.Password == "" {
		updated.Trojan.Shadowsocks.Password = current.Trojan.Shadowsocks.Password
	}
	if updated.VMess != nil && current.VMess != nil &&
		updated.VMess.Handler.MKCP != nil && current.VMess.Handler.MKCP != nil &&
		updated.VMess.Handler.MKCP.Seed == "" {
		updated.VMess.Handler.MKCP.Seed = current.VMess.Handler.MKCP.Seed
	}
	if updated.Hysteria2 != nil && current.Hysteria2 != nil {
		if updated.Hysteria2.PrivateKey == "" {
			updated.Hysteria2.PrivateKey = current.Hysteria2.PrivateKey
		}
		if updated.Hysteria2.ECHKey == "" {
			updated.Hysteria2.ECHKey = current.Hysteria2.ECHKey
		}
		if updated.Hysteria2.ObfsPassword == "" {
			updated.Hysteria2.ObfsPassword = current.Hysteria2.ObfsPassword
		}
		if updated.Hysteria2.Realm != nil && current.Hysteria2.Realm != nil {
			if updated.Hysteria2.Realm.Token == "" {
				updated.Hysteria2.Realm.Token = current.Hysteria2.Realm.Token
			}
			if updated.Hysteria2.Realm.PrivateKey == "" {
				updated.Hysteria2.Realm.PrivateKey = current.Hysteria2.Realm.PrivateKey
			}
		}
	}
}

func nodeStreamSecurity(node *domain.Node) *domain.VLESSSecuritySpec {
	switch {
	case node.VLESS != nil:
		return &node.VLESS.Security
	case node.VMess != nil:
		return &node.VMess.Security
	case node.Trojan != nil:
		return &node.Trojan.Security
	case node.Shadowsocks != nil:
		return &node.Shadowsocks.Security
	default:
		return nil
	}
}

func shadowsocksCipher(node domain.Node) string {
	if node.Shadowsocks == nil {
		return ""
	}
	return node.Shadowsocks.Cipher
}

func (manager *Manager) buildShare(state domain.DesiredState, nodeID, userID, profileID string) (Share, error) {
	nodePosition := nodeIndex(state, nodeID)
	if nodePosition < 0 {
		return Share{}, errors.New("node not found")
	}
	node := state.Nodes[nodePosition]
	userPosition := userIndex(node, userID)
	if userPosition < 0 {
		return Share{}, errors.New("user not found")
	}
	var profile domain.AccessProfile
	var exists bool
	if profileID == "" {
		profile, exists = node.DefaultAccessProfile()
	} else {
		for _, candidate := range node.AccessProfiles {
			if candidate.ID == profileID {
				profile = candidate
				exists = true
				break
			}
		}
	}
	if !exists {
		return Share{}, errors.New("access profile not found")
	}
	compiled, err := manager.protocols.BuildShare(state, node, node.Users[userPosition], profile)
	if err != nil {
		return Share{}, err
	}
	return Share{URI: compiled.URI, QRContent: compiled.QRContent, ClientYAML: string(compiled.ClientYAML)}, nil
}

func nodeIndex(state domain.DesiredState, nodeID string) int {
	for index := range state.Nodes {
		if state.Nodes[index].ID == nodeID {
			return index
		}
	}
	return -1
}

func userIndex(node domain.Node, userID string) int {
	for index := range node.Users {
		if node.Users[index].ID == userID {
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
