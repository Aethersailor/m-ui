package publisher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"sort"

	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/protocol"
	"gopkg.in/yaml.v3"
)

type Compiler interface {
	Compile(ctx context.Context, state domain.DesiredState) ([]byte, error)
}

type YAMLCompiler struct {
	Registry protocol.Registry
}

func (compiler YAMLCompiler) registry() protocol.Registry {
	if compiler.Registry.Empty() {
		return protocol.DefaultRegistry()
	}
	return compiler.Registry
}

func (compiler YAMLCompiler) Compile(ctx context.Context, state domain.DesiredState) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized, err := state.NormalizeLegacy()
	if err != nil {
		return nil, fmt.Errorf("normalize desired state endpoints: %w", err)
	}
	state = normalized
	if err := state.Validate(); err != nil {
		return nil, fmt.Errorf("validate desired state: %w", err)
	}

	nodes := make([]domain.Node, 0, len(state.Nodes))
	for _, node := range state.Nodes {
		if node.Enabled {
			nodes = append(nodes, node)
		}
	}
	sort.SliceStable(nodes, func(left, right int) bool {
		if nodes[left].Name == nodes[right].Name {
			return nodes[left].ID < nodes[right].ID
		}
		return nodes[left].Name < nodes[right].Name
	})

	document := configuration{
		Mode:               "rule",
		LogLevel:           "info",
		IPv6:               true,
		ExternalController: state.MihomoExternalControllerBind.Address(),
		Secret:             state.ControllerSecret,
		Listeners:          make([]any, 0, len(nodes)),
		Rules:              []string{"MATCH,DIRECT"},
	}
	if len(state.ExternalControllerCORSOrigins) > 0 {
		document.ExternalControllerCORS = &externalControllerCORS{
			AllowOrigins: append([]string(nil), state.ExternalControllerCORSOrigins...),
		}
	}
	registry := compiler.registry()
	for _, node := range nodes {
		compiled, err := registry.Compile(ctx, node, state.AsOf)
		if err != nil {
			return nil, err
		}
		document.Listeners = append(document.Listeners, compiled)
	}

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("encode Mihomo YAML: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("finish Mihomo YAML: %w", err)
	}
	return output.Bytes(), nil
}

func SHA256(content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(content))
}

type configuration struct {
	Mode                   string                  `yaml:"mode"`
	LogLevel               string                  `yaml:"log-level"`
	IPv6                   bool                    `yaml:"ipv6"`
	ExternalController     string                  `yaml:"external-controller"`
	ExternalControllerCORS *externalControllerCORS `yaml:"external-controller-cors,omitempty"`
	Secret                 string                  `yaml:"secret"`
	Listeners              []any                   `yaml:"listeners"`
	Rules                  []string                `yaml:"rules"`
}

type externalControllerCORS struct {
	AllowOrigins        []string `yaml:"allow-origins,omitempty"`
	AllowPrivateNetwork bool     `yaml:"allow-private-network,omitempty"`
}
