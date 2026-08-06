package service

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Aethersailor/m-ui/internal/domain"
)

func TestBuildShareProducesVLESSURIAndMihomoClientYAML(t *testing.T) {
	t.Parallel()
	state := shareState()
	node, user := state.Nodes[0], state.Nodes[0].Users[0]
	share, err := BuildShare(state, node.ID, user.ID)
	if err != nil {
		t.Fatalf("BuildShare() error = %v", err)
	}
	if share.QRContent != share.URI {
		t.Fatal("QR content is not the VLESS URI")
	}
	parsed, err := url.Parse(share.URI)
	if err != nil {
		t.Fatalf("parse share URI: %v", err)
	}
	if parsed.Scheme != "vless" || parsed.User.Username() != user.VLESS.UUID ||
		parsed.Host != "edge.example.net:8443" || parsed.Fragment != "node / east - Alice & Bob" {
		t.Fatalf("unexpected share URI: %s", share.URI)
	}
	expectedParameters := map[string]string{
		"encryption": "none", "flow": domain.VLESSFlowVision,
		"fp": domain.ClientFingerprint, "packetEncoding": domain.PacketEncodingXUDP,
		"pbk": node.VLESS.Security.Reality.PublicKey, "security": "reality",
		"sid": node.VLESS.Security.Reality.ShortIDs[0], "sni": "www.example.com", "type": "tcp",
	}
	for key, expected := range expectedParameters {
		if actual := parsed.Query().Get(key); actual != expected {
			t.Errorf("URI query %q = %q, want %q", key, actual, expected)
		}
	}
	for _, expected := range []string{
		"name: node / east - Alice & Bob", "type: vless", "server: edge.example.net",
		"port: 8443", "flow: xtls-rprx-vision", "packet-encoding: xudp",
		"tls: true", "client-fingerprint: chrome",
		"public-key: " + node.VLESS.Security.Reality.PublicKey,
		"short-id: " + node.VLESS.Security.Reality.ShortIDs[0], "encryption: none", "network: tcp",
	} {
		if !strings.Contains(share.ClientYAML, expected) {
			t.Errorf("client YAML does not contain %q\n%s", expected, share.ClientYAML)
		}
	}
}

func TestPreserveNodeSecretsRestoresNamedProtocolCredentials(t *testing.T) {
	t.Parallel()
	current := domain.Node{Protocol: domain.ProtocolVLESS, VLESS: &domain.VLESSSpec{
		Security: domain.VLESSSecuritySpec{ShadowTLS: &domain.ShadowTLSConfig{
			Password: "v2-secret",
			Users:    []domain.ShadowTLSUser{{Name: "alice", Password: "shadow-secret"}},
		}, JLS: &domain.JLSConfig{
			Users: []domain.JLSUser{{Username: "bob", Password: "jls-secret"}},
		}},
	}}
	updated := domain.Node{Protocol: domain.ProtocolVLESS, VLESS: &domain.VLESSSpec{
		Security: domain.VLESSSecuritySpec{ShadowTLS: &domain.ShadowTLSConfig{
			Users: []domain.ShadowTLSUser{{Name: "alice"}},
		}, JLS: &domain.JLSConfig{
			Users: []domain.JLSUser{{Username: "bob"}},
		}},
	}}

	preserveNodeSecrets(&updated, current)
	if updated.VLESS.Security.ShadowTLS.Password != "v2-secret" ||
		updated.VLESS.Security.ShadowTLS.Users[0].Password != "shadow-secret" ||
		updated.VLESS.Security.JLS.Users[0].Password != "jls-secret" {
		t.Fatalf("preserved secrets = %#v", updated.VLESS.Security)
	}
}

func TestPreserveNodeSecretsRestoresVMessMKCPSeed(t *testing.T) {
	t.Parallel()
	current := domain.Node{Protocol: domain.ProtocolVMess, VMess: &domain.VMessSpec{
		Handler: domain.VLESSHandlerSpec{Type: domain.VMessHandlerMKCP, MKCP: &domain.MKCPConfig{Seed: "mkcp-secret"}},
	}}
	updated := domain.Node{Protocol: domain.ProtocolVMess, VMess: &domain.VMessSpec{
		Handler: domain.VLESSHandlerSpec{Type: domain.VMessHandlerMKCP, MKCP: &domain.MKCPConfig{}},
	}}
	preserveNodeSecrets(&updated, current)
	if updated.VMess.Handler.MKCP.Seed != "mkcp-secret" {
		t.Fatalf("seed = %q", updated.VMess.Handler.MKCP.Seed)
	}
}

func TestBuildShareRejectsExpiredUser(t *testing.T) {
	t.Parallel()
	state := shareState()
	expiredAt := state.AsOf
	state.Nodes[0].Users[0].ExpiresAt = &expiredAt
	if _, err := BuildShare(state, state.Nodes[0].ID, state.Nodes[0].Users[0].ID); err == nil {
		t.Fatal("BuildShare() error = nil")
	}
}

func shareState() domain.DesiredState {
	nodeID := "1cf5d79b-7998-4efc-8e31-edf9d1319c16"
	return domain.DesiredState{
		AsOf:              time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC),
		ControllerAddress: "127.0.0.1:9090", ControllerSecret: "controller-test-secret", PublicHost: "global.example.net",
		Nodes: []domain.Node{{
			ID: nodeID, Name: "node / east", Enabled: true, ListenAddress: "0.0.0.0", Port: "443",
			Protocol: domain.ProtocolVLESS, SchemaVersion: domain.NodeSchemaVersion,
			VLESS: &domain.VLESSSpec{Decryption: "none", Handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw}, Security: domain.VLESSSecuritySpec{
				Type: domain.VLESSSecurityReality, Reality: &domain.RealityConfig{
					Destination: "www.example.com:443", PrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
					PublicKey: "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE",
					ShortIDs:  []string{"0123456789abcdef"}, ServerNames: []string{"www.example.com"},
				},
			}},
			Users:          []domain.NodeUser{{ID: "390615fb-5b13-4f96-837d-5fe7eaf8ec39", NodeID: nodeID, Name: "Alice & Bob", Enabled: true, VLESS: &domain.VLESSCredential{UUID: "261e47e2-8a59-4c53-876c-b8d41bd51c1e", Flow: domain.VLESSFlowVision}}},
			AccessProfiles: []domain.AccessProfile{{ID: "999fa0c0-9b7c-4405-848a-6a92bf23ad97", NodeID: nodeID, Name: "default", Default: true, PublicHost: "edge.example.net", PublicPort: 8443, ServerName: "www.example.com", Fingerprint: domain.ClientFingerprint, PacketEncoding: domain.PacketEncodingXUDP}},
			Generation:     1,
		}},
	}
}
