package protocol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Aethersailor/m-ui/internal/domain"
	"gopkg.in/yaml.v3"
)

func TestVMessMKCPCompilesMetaListenerClientAndShare(t *testing.T) {
	t.Parallel()
	node, state := protocolTestNode(domain.ProtocolVMess)
	node.VMess = &domain.VMessSpec{
		Handler: domain.VLESSHandlerSpec{Type: domain.VMessHandlerMKCP, MKCP: &domain.MKCPConfig{
			MTU: 1350, TTI: 50, UplinkCapacity: 5, DownlinkCapacity: 20,
			Congestion: true, WriteBuffer: 2097152, ReadBuffer: 4194304,
			Seed: "mkcp-secret", Header: "wireguard",
		}},
		Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
	}
	node.Users[0].VMess = &domain.VMessCredential{UUID: "7b40acf8-1a83-4216-ac73-88161f70e181", AlterID: 0, Cipher: "auto"}
	state.Nodes = []domain.Node{node}
	compiled, err := (VMessModule{}).Compile(context.Background(), node, state.AsOf)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := yaml.Marshal(compiled)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, expected := range []string{
		"type: vmess", "mkcp-config:", "enable: true", "mtu: 1350", "tti: 50",
		"uplink-capacity: 5", "downlink-capacity: 20", "write-buffer: 2097152",
		"read-buffer: 4194304", "seed: mkcp-secret", "header: wireguard",
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("listener misses %q:\n%s", expected, text)
		}
	}
	share, err := (VMessModule{}).BuildShare(state, node, node.Users[0], node.AccessProfiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(share.URI, "vmess://") {
		t.Fatalf("share URI = %s", share.URI)
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(share.URI, "vmess://"))
	if err != nil {
		t.Fatal(err)
	}
	var vmess map[string]any
	if err := json.Unmarshal(payload, &vmess); err != nil {
		t.Fatal(err)
	}
	if vmess["net"] != "mkcp" || vmess["type"] != "wireguard" || vmess["path"] != "mkcp-secret" {
		t.Fatalf("VMess payload = %#v", vmess)
	}
	for _, expected := range []string{"type: vmess", "alterId: 0", "network: mkcp", "mkcp-opts:", "header: wireguard"} {
		if !strings.Contains(string(share.ClientYAML), expected) {
			t.Errorf("client YAML misses %q:\n%s", expected, share.ClientYAML)
		}
	}
}

func TestTrojanAndShadowsocksCompileAndBuildShare(t *testing.T) {
	t.Parallel()
	trojan, state := protocolTestNode(domain.ProtocolTrojan)
	trojan.Trojan = &domain.TrojanSpec{
		Handler:     domain.VLESSHandlerSpec{Type: domain.VLESSHandlerWebSocket, WebSocket: &domain.WebSocketSpec{Path: "/trojan"}},
		Security:    domain.VLESSSecuritySpec{Type: domain.VLESSSecurityTLS, TLS: &domain.TLSConfig{Certificate: "/cert.pem", PrivateKey: "/key.pem"}},
		Shadowsocks: domain.TrojanShadowsocksSpec{Enabled: true, Method: "aes-128-gcm", Password: "wrapper-secret"},
	}
	trojan.Users[0].Trojan = &domain.TrojanCredential{Password: "trojan-password"}
	state.Nodes = []domain.Node{trojan}
	compiled, err := (TrojanModule{}).Compile(context.Background(), trojan, state.AsOf)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := yaml.Marshal(compiled)
	for _, expected := range []string{"type: trojan", "ws-path: /trojan", "certificate: /cert.pem", "ss-option:", "password: wrapper-secret"} {
		if !strings.Contains(string(encoded), expected) {
			t.Errorf("Trojan listener misses %q:\n%s", expected, encoded)
		}
	}
	share, err := (TrojanModule{}).BuildShare(state, trojan, trojan.Users[0], trojan.AccessProfiles[0])
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(share.URI)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "trojan" || parsed.Query().Get("type") != "ws" || parsed.Query().Get("security") != "tls" {
		t.Fatalf("Trojan URI = %s", share.URI)
	}
	if parsed.Query().Get("encryption") != "ss;aes-128-gcm:wrapper-secret" || !strings.Contains(string(share.ClientYAML), "ss-opts:") {
		t.Fatalf("Trojan Shadowsocks share mismatch: %s\n%s", share.URI, share.ClientYAML)
	}
	if !strings.Contains(string(share.ClientYAML), "sni: www.example.com") || strings.Contains(string(share.ClientYAML), "\n    tls:") {
		t.Fatalf("Trojan client used wrong Mihomo TLS fields:\n%s", share.ClientYAML)
	}

	ss, ssState := protocolTestNode(domain.ProtocolShadowsocks)
	ss.Shadowsocks = &domain.ShadowsocksSpec{Cipher: "2022-blake3-aes-256-gcm", UDP: true, Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone}, SimpleObfs: domain.SimpleObfsSpec{Enabled: true, Mode: "http"}}
	ss.Users[0].Shadowsocks = &domain.ShadowsocksCredential{Password: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}
	ssState.Nodes = []domain.Node{ss}
	compiled, err = (ShadowsocksModule{}).Compile(context.Background(), ss, ssState.AsOf)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = yaml.Marshal(compiled)
	for _, expected := range []string{"type: shadowsocks", "cipher: 2022-blake3-aes-256-gcm", "udp: true", "simple-obfs:", "mode: http"} {
		if !strings.Contains(string(encoded), expected) {
			t.Errorf("SS listener misses %q:\n%s", expected, encoded)
		}
	}
	ssShare, err := (ShadowsocksModule{}).BuildShare(ssState, ss, ss.Users[0], ss.AccessProfiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ssShare.URI, "ss://") || !strings.Contains(ssShare.URI, "plugin=") {
		t.Fatalf("SS URI = %s", ssShare.URI)
	}
	for _, expected := range []string{"type: ss", "plugin: obfs", "mode: http"} {
		if !strings.Contains(string(ssShare.ClientYAML), expected) {
			t.Errorf("SS client YAML misses %q:\n%s", expected, ssShare.ClientYAML)
		}
	}
}

func TestNewProtocolCapabilitiesAreSelfMounted(t *testing.T) {
	t.Parallel()
	manifest := DefaultRegistry().Capabilities()
	for _, kind := range []domain.ProtocolKind{domain.ProtocolVMess, domain.ProtocolTrojan, domain.ProtocolShadowsocks} {
		capability := protocolByKind(t, manifest, kind)
		if len(capability.DefaultUser) == 0 || !json.Valid(capability.DefaultUser) {
			t.Fatalf("%s default user is missing", kind)
		}
		for _, component := range capability.Components {
			if component.ConfigPath == "" {
				t.Fatalf("%s %s:%s has no config_path", kind, component.Group, component.Kind)
			}
		}
	}
	mkcp := componentByKind(t, protocolByKind(t, manifest, domain.ProtocolVMess), ComponentTransport, "mkcp")
	if mkcp.ConfigPath != "vmess.handler" || mkcp.SelectionPath != "vmess.handler.type" {
		t.Fatalf("mKCP mount = %#v", mkcp)
	}
	for _, path := range []string{"mkcp.mtu", "mkcp.tti", "mkcp.uplink_capacity", "mkcp.downlink_capacity", "mkcp.congestion", "mkcp.write_buffer", "mkcp.read_buffer", "mkcp.seed", "mkcp.header"} {
		fieldByPath(t, mkcp.Fields, path)
	}
	vmessTLS := componentByKind(t, protocolByKind(t, manifest, domain.ProtocolVMess), ComponentSecurity, "tls")
	for _, field := range vmessTLS.Fields {
		if field.Path == "tls.allow_insecure" {
			t.Fatal("VMess exposed unsupported listener allow-insecure")
		}
	}
}

func protocolTestNode(kind domain.ProtocolKind) (domain.Node, domain.DesiredState) {
	nodeID := "6dc869be-6085-45b5-937a-c79ad479093a"
	user := domain.NodeUser{ID: "2df49737-ddb4-4e32-b884-e831e7248a64", NodeID: nodeID, Name: "alice", Enabled: true}
	node := domain.Node{
		ID: nodeID, Name: "edge", Enabled: true, ListenAddress: "0.0.0.0", Port: "443",
		Protocol: kind, SchemaVersion: domain.NodeSchemaVersion, Users: []domain.NodeUser{user},
		AccessProfiles: []domain.AccessProfile{{ID: "c4555000-77d9-48fc-a743-063986bc7b87", NodeID: nodeID, Name: "default", Default: true, PublicHost: "edge.example.com", PublicPort: 443, ServerName: "www.example.com", Fingerprint: "chrome"}},
		Generation:     1,
	}
	state := domain.DesiredState{AsOf: time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC), PublicHost: "global.example.com"}
	return node, state
}
