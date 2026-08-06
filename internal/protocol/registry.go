package protocol

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Aethersailor/m-ui/internal/domain"
)

type Share struct {
	URI        string
	QRContent  string
	ClientYAML []byte
}

type Module interface {
	Kind() domain.ProtocolKind
	Compile(context.Context, domain.Node, time.Time) (any, error)
	BuildShare(domain.DesiredState, domain.Node, domain.NodeUser, domain.AccessProfile) (Share, error)
	Capability() ProtocolCapability
}

type Registry struct {
	modules map[domain.ProtocolKind]Module
}

func (registry Registry) Empty() bool { return len(registry.modules) == 0 }

func DefaultRegistry() Registry {
	return NewRegistry(
		VLESSModule{}, Hysteria2Module{}, VMessModule{}, TrojanModule{}, ShadowsocksModule{},
	)
}

func NewRegistry(modules ...Module) Registry {
	registry := Registry{modules: make(map[domain.ProtocolKind]Module, len(modules))}
	for _, module := range modules {
		if module == nil {
			continue
		}
		registry.modules[module.Kind()] = module
	}
	return registry
}

func (registry Registry) Compile(
	ctx context.Context,
	node domain.Node,
	asOf time.Time,
) (any, error) {
	module, exists := registry.modules[node.Protocol]
	if !exists {
		return nil, fmt.Errorf("unsupported node protocol %q", node.Protocol)
	}
	compiled, err := module.Compile(ctx, node, asOf)
	if err != nil {
		return nil, fmt.Errorf("compile %s node %q: %w", node.Protocol, node.Name, err)
	}
	return compiled, nil
}

func (registry Registry) BuildShare(
	state domain.DesiredState,
	node domain.Node,
	user domain.NodeUser,
	profile domain.AccessProfile,
) (Share, error) {
	module, exists := registry.modules[node.Protocol]
	if !exists {
		return Share{}, fmt.Errorf("unsupported node protocol %q", node.Protocol)
	}
	if user.NodeID != node.ID || profile.NodeID != node.ID {
		return Share{}, errors.New("share input does not belong to the requested node")
	}
	if !node.Enabled {
		return Share{}, errors.New("disabled node cannot be shared")
	}
	if !user.Enabled || (user.ExpiresAt != nil && !user.ExpiresAt.After(state.AsOf)) {
		return Share{}, errors.New("disabled or expired user cannot be shared")
	}
	return module.BuildShare(state, node, user, profile)
}

func (registry Registry) Capabilities() CapabilityManifest {
	protocols := make([]ProtocolCapability, 0, len(registry.modules))
	for _, module := range registry.modules {
		protocols = append(protocols, module.Capability())
	}
	sort.Slice(protocols, func(i, j int) bool { return protocols[i].Kind < protocols[j].Kind })
	manifest := CapabilityManifest{
		SchemaVersion:     CapabilitySchemaVersion,
		NodeSchemaVersion: domain.NodeSchemaVersion,
		Source: SourceContract{
			Repository: domain.MihomoRepository,
			Branch:     domain.MihomoSourceBranch,
			Commit:     domain.MihomoSourceCommit,
		},
		NodeFields:          commonNodeFields(),
		AccessProfileFields: accessProfileFields(),
		Protocols:           protocols,
	}
	if err := ValidateCapabilityManifest(manifest); err != nil {
		panic(fmt.Sprintf("invalid protocol capability manifest: %v", err))
	}
	return manifest
}

// mountClassicCapability is a protocol-owned builder for Meta listeners that
// expose the shared handler/security composition. Registry itself remains
// protocol-agnostic: registering a module is enough for capability/UI pickup.
func mountClassicCapability(capability ProtocolCapability, root string, defaultUser any) ProtocolCapability {
	capability.DefaultUser = rawDefault(defaultUser)
	for index := range capability.Components {
		component := &capability.Components[index]
		switch component.Group {
		case ComponentTransport:
			component.ConfigPath = root + ".handler"
			component.SelectionPath = root + ".handler.type"
		case ComponentSecurity:
			component.ConfigPath = root + ".security"
			component.SelectionPath = root + ".security.type"
		case ComponentExtension:
			component.ConfigPath = root
		}
		component.Fields = relativeComponentFields(component.ConfigPath, component.Fields)
	}
	return capability
}

func mountLockedCapability(capability ProtocolCapability, root string, defaultUser any, extensions map[string]string) ProtocolCapability {
	capability.DefaultUser = rawDefault(defaultUser)
	for index := range capability.Components {
		component := &capability.Components[index]
		component.ConfigPath = root
		if path, ok := extensions[component.Kind]; ok && component.Group == ComponentExtension {
			component.ConfigPath = path
			component.EnabledPath = path + ".enabled"
		}
		component.Fields = relativeComponentFields(component.ConfigPath, component.Fields)
	}
	return capability
}

func mountShadowsocksCapability(capability ProtocolCapability, root string, defaultUser any) ProtocolCapability {
	capability.DefaultUser = rawDefault(defaultUser)
	for index := range capability.Components {
		component := &capability.Components[index]
		switch component.Group {
		case ComponentSecurity:
			component.ConfigPath = root + ".security"
			component.SelectionPath = root + ".security.type"
		case ComponentExtension:
			component.ConfigPath = root + ".simple_obfs"
			component.EnabledPath = component.ConfigPath + ".enabled"
		default:
			component.ConfigPath = root
		}
		component.Fields = relativeComponentFields(component.ConfigPath, component.Fields)
	}
	return capability
}

// Component field paths are always relative to ConfigPath. Protocol fields and
// user fields remain rooted at the node/user payload respectively.
func relativeComponentFields(configPath string, fields []FieldCapability) []FieldCapability {
	prefix := configPath + "."
	normalized := append([]FieldCapability(nil), fields...)
	for index := range normalized {
		field := &normalized[index]
		field.Path = strings.TrimPrefix(field.Path, prefix)
		if field.VisibleWhen != nil {
			condition := *field.VisibleWhen
			condition.Path = strings.TrimPrefix(condition.Path, prefix)
			field.VisibleWhen = &condition
		}
		field.ItemFields = relativeComponentFields("", field.ItemFields)
	}
	return normalized
}

func effectiveUsers(node domain.Node, asOf time.Time) []domain.NodeUser {
	users := node.EffectiveUsers(asOf)
	sort.SliceStable(users, func(i, j int) bool {
		if users[i].Name != users[j].Name {
			return users[i].Name < users[j].Name
		}
		return users[i].ID < users[j].ID
	})
	return users
}
