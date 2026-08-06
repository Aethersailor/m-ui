package publisher

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Aethersailor/m-ui/internal/domain"
)

func TestYAMLCompilerIsDeterministicAndCompilesProtocolModules(t *testing.T) {
	t.Parallel()
	state := compilerState()
	compiler := YAMLCompiler{}

	first, err := compiler.Compile(context.Background(), state)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	second, err := compiler.Compile(context.Background(), state)
	if err != nil {
		t.Fatalf("second Compile() error = %v", err)
	}
	if string(first) != string(second) || SHA256(first) != SHA256(second) {
		t.Fatal("same desired state did not produce identical YAML and SHA-256")
	}
	text := string(first)
	for _, expected := range []string{
		"external-controller: 127.0.0.1:9090",
		"name: alpha-vless", "type: vless", "ws-path: /edge",
		"reality-config:", "private-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"name: beta-hysteria2", "type: hysteria2", "obfs: salamander",
		"obfs-password: obfs-secret", "alice: password-alice",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("compiled YAML does not contain %q:\n%s", expected, text)
		}
	}
}

func TestYAMLCompilerSortsVLESSUsersIndependentOfInputOrder(t *testing.T) {
	t.Parallel()
	state := compilerState()
	node := &state.Nodes[0]
	node.Users = append(node.Users, domain.NodeUser{
		ID:      "bb8ff4d5-f539-4e6d-8538-cbe11c16470c",
		NodeID:  node.ID,
		Name:    "alice",
		Enabled: true,
		VLESS:   &domain.VLESSCredential{UUID: "139a100e-799d-42f7-951d-6914f2d9e16d"},
	})

	first, err := (YAMLCompiler{}).Compile(context.Background(), state)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	node.Users[0], node.Users[1] = node.Users[1], node.Users[0]
	second, err := (YAMLCompiler{}).Compile(context.Background(), state)
	if err != nil {
		t.Fatalf("Compile() with reversed users error = %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("user input order changed compiled YAML:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestYAMLCompilerFiltersDisabledAndExpiredUsersAndNodes(t *testing.T) {
	t.Parallel()
	state := compilerState()
	expired := state.AsOf.Add(-time.Second)
	state.Nodes[0].Users = append(state.Nodes[0].Users,
		domain.NodeUser{ID: "28977cba-6d76-45d6-b009-7c7d11ac0dbd", NodeID: state.Nodes[0].ID, Name: "disabled-user", Enabled: false, VLESS: &domain.VLESSCredential{UUID: "88937cda-a178-4497-9942-b374df94503d"}},
		domain.NodeUser{ID: "080e7b00-4f82-4574-b074-376b56543f3c", NodeID: state.Nodes[0].ID, Name: "expired-user", Enabled: true, VLESS: &domain.VLESSCredential{UUID: "c8d86fa9-5d75-40cb-943b-885602291fbe"}, ExpiresAt: &expired},
	)
	disabled := state.Nodes[0]
	disabled.Users = append([]domain.NodeUser(nil), disabled.Users...)
	disabled.AccessProfiles = append([]domain.AccessProfile(nil), disabled.AccessProfiles...)
	disabled.ID = "7fc89e8a-da47-42b2-908e-2d639ad3ff0f"
	disabled.Name = "disabled-node"
	disabled.Enabled = false
	disabled.Port = "9443"
	for index := range disabled.Users {
		disabled.Users[index].NodeID = disabled.ID
	}
	for index := range disabled.AccessProfiles {
		disabled.AccessProfiles[index].ID = "0d387f71-d01e-4e5c-9f83-8d1ef10cd0df"
		disabled.AccessProfiles[index].NodeID = disabled.ID
	}
	state.Nodes = append(state.Nodes, disabled)

	output, err := (YAMLCompiler{}).Compile(context.Background(), state)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	for _, excluded := range []string{"disabled-user", "expired-user", "disabled-node"} {
		if strings.Contains(string(output), excluded) {
			t.Fatalf("compiled YAML contains filtered record %q", excluded)
		}
	}
}

func TestYAMLCompilerUsesIPv6ControllerAndExactCORSOrigins(t *testing.T) {
	t.Parallel()
	state := compilerState()
	state.MihomoExternalControllerBind = domain.Endpoint{Host: "::", Port: 9090}
	state.MihomoControllerConnect = domain.Endpoint{Host: "::1", Port: 9090}
	state.ExternalControllerCORSOrigins = []string{"https://dashboard.example.com"}
	output, err := (YAMLCompiler{}).Compile(context.Background(), state)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	for _, expected := range []string{"external-controller: '[::]:9090'", "external-controller-cors:", "https://dashboard.example.com"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("compiled YAML does not contain %q:\n%s", expected, output)
		}
	}
}

func compilerState() domain.DesiredState {
	asOf := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	privateKey := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	publicKey := "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE"
	vlessID := "0e5a3b4b-a9a4-426f-ae22-3723044a67d8"
	hysteriaID := "cfa6f3fc-d8f4-4bd1-aae9-50ad62d3758d"
	return domain.DesiredState{
		AsOf: asOf, ControllerAddress: "127.0.0.1:9090",
		ControllerSecret: "controller-test-secret", PublicHost: "node.example.com",
		Nodes: []domain.Node{
			{
				ID: vlessID, Name: "alpha-vless", Enabled: true, ListenAddress: "0.0.0.0", Port: "443",
				Protocol: domain.ProtocolVLESS, SchemaVersion: domain.NodeSchemaVersion,
				VLESS: &domain.VLESSSpec{
					Decryption: "none",
					Handler:    domain.VLESSHandlerSpec{Type: domain.VLESSHandlerWebSocket, WebSocket: &domain.WebSocketSpec{Path: "/edge"}},
					Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityReality, Reality: &domain.RealityConfig{
						Destination: "www.example.com:443", PrivateKey: privateKey, PublicKey: publicKey,
						ShortIDs: []string{"0123456789abcdef"}, ServerNames: []string{"www.example.com"},
					}},
				},
				Users:          []domain.NodeUser{{ID: "81a91f2d-8c16-4589-9594-72331c234c0b", NodeID: vlessID, Name: "zed", Enabled: true, VLESS: &domain.VLESSCredential{UUID: "3503f9ac-2f35-44f1-83ae-fd4f27d36a70"}}},
				AccessProfiles: []domain.AccessProfile{{ID: "67c4043b-d7a4-40fd-8b42-18103c189ca9", NodeID: vlessID, Name: "default", Default: true, PublicPort: 443, ServerName: "www.example.com"}},
				Generation:     1,
			},
			{
				ID: hysteriaID, Name: "beta-hysteria2", Enabled: true, ListenAddress: "0.0.0.0", Port: "8443",
				Protocol: domain.ProtocolHysteria2, SchemaVersion: domain.NodeSchemaVersion,
				Hysteria2:      &domain.Hysteria2Spec{Certificate: "/data/cert.pem", PrivateKey: "/data/key.pem", Obfs: "salamander", ObfsPassword: "obfs-secret", ALPN: []string{"h3"}},
				Users:          []domain.NodeUser{{ID: "96334cb9-642a-4eb2-beab-a0ff8f277568", NodeID: hysteriaID, Name: "alice", Enabled: true, Hysteria2: &domain.Hysteria2Credential{Password: "password-alice"}}},
				AccessProfiles: []domain.AccessProfile{{ID: "5eb744c8-a50a-4ea6-952c-202541b6f75b", NodeID: hysteriaID, Name: "default", Default: true, PublicPort: 8443, ServerName: "hy2.example.com"}},
				Generation:     1,
			},
		},
	}
}
