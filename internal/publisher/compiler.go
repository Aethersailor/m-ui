package publisher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"sort"

	"github.com/Aethersailor/m-ui/internal/domain"
	"gopkg.in/yaml.v3"
)

type Compiler interface {
	Compile(ctx context.Context, state domain.DesiredState) ([]byte, error)
}

type YAMLCompiler struct{}

func (YAMLCompiler) Compile(ctx context.Context, state domain.DesiredState) ([]byte, error) {
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

	listeners := make([]domain.Listener, 0, len(state.Listeners))
	for _, listener := range state.Listeners {
		if listener.Enabled {
			listeners = append(listeners, listener)
		}
	}
	sort.SliceStable(listeners, func(left, right int) bool {
		if listeners[left].Name == listeners[right].Name {
			return listeners[left].ID < listeners[right].ID
		}
		return listeners[left].Name < listeners[right].Name
	})

	document := configuration{
		Mode:               "rule",
		LogLevel:           "info",
		IPv6:               true,
		ExternalController: state.MihomoExternalControllerBind.Address(),
		Secret:             state.ControllerSecret,
		Listeners:          make([]vlessListener, 0, len(listeners)),
		Rules:              []string{"MATCH,DIRECT"},
	}
	if len(state.ExternalControllerCORSOrigins) > 0 {
		document.ExternalControllerCORS = &externalControllerCORS{
			AllowOrigins: append([]string(nil), state.ExternalControllerCORSOrigins...),
		}
	}
	for _, listener := range listeners {
		users := listener.EffectiveUsers(state.AsOf)
		sort.SliceStable(users, func(left, right int) bool {
			if users[left].Name == users[right].Name {
				return users[left].ID < users[right].ID
			}
			return users[left].Name < users[right].Name
		})
		compiledUsers := make([]vlessUser, 0, len(users))
		for _, user := range users {
			compiledUsers = append(compiledUsers, vlessUser{
				Username: user.Name,
				UUID:     user.UUID,
				Flow:     domain.VLESSFlow,
			})
		}
		document.Listeners = append(document.Listeners, vlessListener{
			Name:           listener.Name,
			Type:           "vless",
			Listen:         listener.ListenAddress,
			Port:           listener.ListenPort,
			Users:          compiledUsers,
			UDP:            listener.UDPEnabled,
			PacketEncoding: domain.PacketEncoding,
			Reality: realityConfiguration{
				Destination: listener.RealityDest,
				PrivateKey:  listener.RealityPrivateKey,
				ShortIDs:    []string{listener.ShortID},
				ServerNames: []string{listener.ServerName},
			},
		})
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
	Listeners              []vlessListener         `yaml:"listeners"`
	Rules                  []string                `yaml:"rules"`
}

type externalControllerCORS struct {
	AllowOrigins        []string `yaml:"allow-origins,omitempty"`
	AllowPrivateNetwork bool     `yaml:"allow-private-network,omitempty"`
}

type vlessListener struct {
	Name           string               `yaml:"name"`
	Type           string               `yaml:"type"`
	Listen         string               `yaml:"listen"`
	Port           uint16               `yaml:"port"`
	Users          []vlessUser          `yaml:"users"`
	UDP            bool                 `yaml:"udp"`
	PacketEncoding string               `yaml:"packet-encoding"`
	Reality        realityConfiguration `yaml:"reality-config"`
}

type vlessUser struct {
	Username string `yaml:"username"`
	UUID     string `yaml:"uuid"`
	Flow     string `yaml:"flow"`
}

type realityConfiguration struct {
	Destination string   `yaml:"dest"`
	PrivateKey  string   `yaml:"private-key"`
	ShortIDs    []string `yaml:"short-id"`
	ServerNames []string `yaml:"server-names"`
}
