package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/publisher"
	"github.com/Aethersailor/m-ui/internal/service"
	"github.com/Aethersailor/m-ui/internal/store"
)

func TestGeneratedServerAndClientConfigurationsWithRealMihomo(t *testing.T) {
	binary := os.Getenv("M_UI_TEST_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("M_UI_TEST_MIHOMO_BINARY is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	t.Setenv("USERPROFILE", t.TempDir())
	cli, err := mihomo.NewCLI(binary)
	if err != nil {
		t.Fatalf("NewCLI() error = %v", err)
	}
	keypair, err := cli.GenerateRealityKeypair(ctx)
	if err != nil {
		t.Fatalf("GenerateRealityKeypair() error = %v", err)
	}
	version, err := cli.Version(ctx)
	if err != nil || version == "" {
		t.Fatalf("Version() = %q, %v", version, err)
	}
	scenarios := []struct {
		name        string
		bindHost    string
		connectHost string
	}{
		{name: "loopback-ipv4", bindHost: "127.0.0.1", connectHost: "127.0.0.1"},
		{name: "wildcard-ipv4", bindHost: "0.0.0.0", connectHost: "127.0.0.1"},
		{name: "loopback-ipv6", bindHost: "::1", connectHost: "::1"},
		{name: "wildcard-ipv6", bindHost: "::", connectHost: "::1"},
	}
	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			scenarioContext, scenarioCancel := context.WithTimeout(ctx, 30*time.Second)
			defer scenarioCancel()
			controllerPort := availableTCPPortForHost(t, scenario.bindHost)
			listenerPort := availableTCPPortForHost(t, scenario.bindHost)
			for listenerPort == controllerPort {
				listenerPort = availableTCPPortForHost(t, scenario.bindHost)
			}
			nodeID := "f8bb1f0d-a396-42fd-a221-c382c8ef9526"
			userID := "eeed3560-deb6-453c-8d56-9a2f5b66defc"
			state := domain.DesiredState{
				AsOf: time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
				MihomoExternalControllerBind: domain.Endpoint{
					Host: scenario.bindHost,
					Port: uint16(controllerPort),
				},
				MihomoControllerConnect: domain.Endpoint{
					Host: scenario.connectHost,
					Port: uint16(controllerPort),
				},
				ExternalControllerCORSOrigins: []string{"https://dashboard.example.com"},
				ControllerSecret:              "integration-test-controller-secret",
				PublicHost:                    "node.example.com",
				Nodes: []domain.Node{{
					ID: nodeID, Name: "integration-node", Enabled: true, ListenAddress: "127.0.0.1",
					Port: fmt.Sprint(listenerPort), Protocol: domain.ProtocolVLESS, SchemaVersion: domain.NodeSchemaVersion,
					VLESS: &domain.VLESSSpec{Decryption: "none", Handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw}, Security: domain.VLESSSecuritySpec{
						Type: domain.VLESSSecurityReality, Reality: &domain.RealityConfig{
							Destination: "www.example.com:443", PrivateKey: keypair.PrivateKey, PublicKey: keypair.PublicKey,
							ShortIDs: []string{"0123456789abcdef"}, ServerNames: []string{"www.example.com"},
						},
					}},
					Users: []domain.NodeUser{{
						ID:      userID,
						NodeID:  nodeID,
						Name:    "integration-user",
						Enabled: true,
						VLESS:   &domain.VLESSCredential{UUID: "2bf189fe-ec56-497d-9069-68bf32c4425b"},
					}},
					AccessProfiles: []domain.AccessProfile{{
						ID: "41cc5551-01b2-45d6-9974-a848be4a6a5a", NodeID: nodeID,
						Name: "default", Default: true, PublicPort: uint16(listenerPort), ServerName: "www.example.com",
						Fingerprint: domain.ClientFingerprint, PacketEncoding: domain.PacketEncodingXUDP,
					}},
					Generation: 1,
				}},
			}

			serverYAML, err := (publisher.YAMLCompiler{}).Compile(scenarioContext, state)
			if err != nil {
				t.Fatalf("compile server YAML: %v", err)
			}
			share, err := service.BuildShare(state, nodeID, userID)
			if err != nil {
				t.Fatalf("build client YAML: %v", err)
			}

			directory := t.TempDir()
			sensitiveValues := []string{
				keypair.PrivateKey,
				keypair.PublicKey,
				state.ControllerSecret,
				state.Nodes[0].Users[0].VLESS.UUID,
			}
			validateWithMihomo(t, scenarioContext, cli, directory, "server.yaml", serverYAML, sensitiveValues)
			clientYAML := []byte(share.ClientYAML + "rules:\n  - MATCH,DIRECT\n")
			validateWithMihomo(t, scenarioContext, cli, directory, "client.yaml", clientYAML, sensitiveValues)
			startAndReloadMihomo(
				t,
				scenarioContext,
				binary,
				directory,
				filepath.Join(directory, "server.yaml"),
				state.MihomoControllerConnect.Address(),
				state.ControllerSecret,
				sensitiveValues,
			)
		})
	}
}

func TestGeneratedHysteria2ConfigurationsWithRealMihomo(t *testing.T) {
	binary := os.Getenv("M_UI_TEST_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("M_UI_TEST_MIHOMO_BINARY is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	directory := t.TempDir()
	certificatePath, privateKeyPath := writeTestCertificate(t, directory)
	nodeID := "f17c5039-0c0a-418f-865a-10c2806f6810"
	userID := "7f9954a2-f324-4938-b224-89903a13d569"
	state := domain.DesiredState{
		AsOf:                         time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC),
		MihomoExternalControllerBind: domain.Endpoint{Host: "127.0.0.1", Port: 19090},
		MihomoControllerConnect:      domain.Endpoint{Host: "127.0.0.1", Port: 19090},
		ControllerSecret:             "hysteria2-integration-controller-secret",
		PublicHost:                   "hy2.example.com",
		Nodes: []domain.Node{{
			ID: nodeID, Name: "integration-hysteria2", Enabled: true,
			ListenAddress: "127.0.0.1", Port: "24443", Protocol: domain.ProtocolHysteria2,
			SchemaVersion: domain.NodeSchemaVersion, Generation: 1,
			Hysteria2: &domain.Hysteria2Spec{
				Certificate: certificatePath, PrivateKey: privateKeyPath,
				Obfs: "salamander", ObfsPassword: "hysteria2-obfs-secret",
				ALPN: []string{"h3"}, Up: "100 Mbps", Down: "100 Mbps",
				UDPMTU: 1200, InitialStreamReceiveWindow: 8388608,
				MaxStreamReceiveWindow: 8388608,
				Realm: &domain.Hysteria2RealmConfig{
					Enabled: true, ServerURL: "https://realm.example.com", Token: "hysteria2-realm-token",
					RealmID: "edge-a", STUNServers: []string{"stun.example.com:3478"},
					ServerName: "realm.example.com", SkipCertVerify: true, ALPN: []string{"h3"},
				},
			},
			Users: []domain.NodeUser{{
				ID: userID, NodeID: nodeID, Name: "alice", Enabled: true,
				Hysteria2: &domain.Hysteria2Credential{Password: "hysteria2-user-secret"},
			}},
			AccessProfiles: []domain.AccessProfile{{
				ID: "593c4d1f-11aa-47ac-9e82-88f15f696cc6", NodeID: nodeID,
				Name: "default", Default: true, PublicPort: 24443, ServerName: "hy2.example.com",
			}},
		}},
	}
	serverYAML, err := (publisher.YAMLCompiler{}).Compile(ctx, state)
	if err != nil {
		t.Fatalf("compile Hysteria2 server YAML: %v", err)
	}
	share, err := service.BuildShare(state, nodeID, userID)
	if err != nil {
		t.Fatalf("build Hysteria2 client YAML: %v", err)
	}
	cli, err := mihomo.NewCLI(binary)
	if err != nil {
		t.Fatalf("NewCLI() error = %v", err)
	}
	sensitive := []string{state.ControllerSecret, "hysteria2-obfs-secret", "hysteria2-user-secret", "hysteria2-realm-token"}
	validateWithMihomo(t, ctx, cli, directory, "hysteria2-server.yaml", serverYAML, sensitive)
	clientYAML := []byte(share.ClientYAML + "rules:\n  - MATCH,DIRECT\n")
	validateWithMihomo(t, ctx, cli, directory, "hysteria2-client.yaml", clientYAML, sensitive)
}

func TestGeneratedR3ProtocolConfigurationsWithRealMihomo(t *testing.T) {
	binary := os.Getenv("M_UI_TEST_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("M_UI_TEST_MIHOMO_BINARY is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	directory := t.TempDir()
	certificatePath, privateKeyPath := writeTestCertificate(t, directory)
	cli, err := mihomo.NewCLI(binary)
	if err != nil {
		t.Fatalf("NewCLI() error = %v", err)
	}

	nodeIDs := []string{
		"a3138154-3286-47f3-93d0-23e24e16ab05", "386d819e-6dd7-463e-8646-31d0c4420f51",
		"6b39f0e6-cbc2-47d7-be4f-1025cf3a7ae8", "ec66c25f-acfb-416a-88c4-486837116e84",
		"977bfbba-1bd2-43e7-b132-0263f15798c5",
	}
	userIDs := []string{
		"c78cb0ce-009f-408c-96ac-a95561089437", "05215167-2e30-49d0-b778-3304c789a008",
		"d07a7bcb-e069-40c7-8990-af4a6c13e065", "d111c9e8-3262-43b4-87ee-520316586db2",
		"c779c307-dbaa-4eb3-a3d7-a38b1870af16",
	}
	profileIDs := []string{
		"e8b1ced2-731f-426c-b416-851800cdf742", "ea3b1d08-f790-4b04-875f-732008c630df",
		"ac78cbc8-5ef8-493f-b31b-1802da245063", "51f8aa13-bd85-4158-be3e-21cd065a932f",
		"651ce043-5596-4f18-a493-fc175795d310",
	}
	profile := func(index int, port uint16) []domain.AccessProfile {
		return []domain.AccessProfile{{
			ID: profileIDs[index], NodeID: nodeIDs[index], Name: "default", Default: true,
			PublicHost: "127.0.0.1", PublicPort: port, ServerName: "hy2.example.com",
			Fingerprint: domain.ClientFingerprint, AllowInsecure: true,
		}}
	}
	state := domain.DesiredState{
		AsOf:                         time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC),
		MihomoExternalControllerBind: domain.Endpoint{Host: "127.0.0.1", Port: 39090},
		MihomoControllerConnect:      domain.Endpoint{Host: "127.0.0.1", Port: 39090},
		ControllerSecret:             "r3-integration-controller-secret", PublicHost: "127.0.0.1",
		Nodes: []domain.Node{
			{
				ID: nodeIDs[0], Name: "vmess-raw", Enabled: true, ListenAddress: "127.0.0.1", Port: "31001",
				Protocol: domain.ProtocolVMess, SchemaVersion: domain.NodeSchemaVersion, Generation: 1,
				VMess:          &domain.VMessSpec{Handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw}, Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone}},
				Users:          []domain.NodeUser{{ID: userIDs[0], NodeID: nodeIDs[0], Name: "alice", Enabled: true, VMess: &domain.VMessCredential{UUID: "96dbaa82-0975-4420-aef7-ec115eaed40a", Cipher: "auto"}}},
				AccessProfiles: profile(0, 31001),
			},
			{
				ID: nodeIDs[1], Name: "vmess-mkcp", Enabled: true, ListenAddress: "127.0.0.1", Port: "31002",
				Protocol: domain.ProtocolVMess, SchemaVersion: domain.NodeSchemaVersion, Generation: 1,
				VMess: &domain.VMessSpec{Handler: domain.VLESSHandlerSpec{Type: domain.VMessHandlerMKCP, MKCP: &domain.MKCPConfig{
					MTU: 1350, TTI: 50, UplinkCapacity: 5, DownlinkCapacity: 20,
					WriteBuffer: 2097152, ReadBuffer: 2097152, Seed: "r3-mkcp-seed", Header: "wireguard",
				}}, Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone}},
				Users:          []domain.NodeUser{{ID: userIDs[1], NodeID: nodeIDs[1], Name: "bob", Enabled: true, VMess: &domain.VMessCredential{UUID: "ac6bc3ba-9a96-40dc-adc7-a2c7e7606171", Cipher: "auto"}}},
				AccessProfiles: profile(1, 31002),
			},
			{
				ID: nodeIDs[2], Name: "trojan-tls", Enabled: true, ListenAddress: "127.0.0.1", Port: "31003",
				Protocol: domain.ProtocolTrojan, SchemaVersion: domain.NodeSchemaVersion, Generation: 1,
				Trojan:         &domain.TrojanSpec{Handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw}, Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityTLS, TLS: &domain.TLSConfig{Certificate: certificatePath, PrivateKey: privateKeyPath}}},
				Users:          []domain.NodeUser{{ID: userIDs[2], NodeID: nodeIDs[2], Name: "carol", Enabled: true, Trojan: &domain.TrojanCredential{Password: "r3-trojan-password"}}},
				AccessProfiles: profile(2, 31003),
			},
			{
				ID: nodeIDs[3], Name: "ss-2022", Enabled: true, ListenAddress: "127.0.0.1", Port: "31004",
				Protocol: domain.ProtocolShadowsocks, SchemaVersion: domain.NodeSchemaVersion, Generation: 1,
				Shadowsocks:    &domain.ShadowsocksSpec{Cipher: "2022-blake3-aes-256-gcm", UDP: true, Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone}},
				Users:          []domain.NodeUser{{ID: userIDs[3], NodeID: nodeIDs[3], Name: "dave", Enabled: true, Shadowsocks: &domain.ShadowsocksCredential{Password: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}}},
				AccessProfiles: profile(3, 31004),
			},
			{
				ID: nodeIDs[4], Name: "ss-simple-obfs", Enabled: true, ListenAddress: "127.0.0.1", Port: "31005",
				Protocol: domain.ProtocolShadowsocks, SchemaVersion: domain.NodeSchemaVersion, Generation: 1,
				Shadowsocks:    &domain.ShadowsocksSpec{Cipher: "aes-128-gcm", UDP: false, Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone}, SimpleObfs: domain.SimpleObfsSpec{Enabled: true, Mode: "http"}},
				Users:          []domain.NodeUser{{ID: userIDs[4], NodeID: nodeIDs[4], Name: "erin", Enabled: true, Shadowsocks: &domain.ShadowsocksCredential{Password: "r3-legacy-ss-password"}}},
				AccessProfiles: profile(4, 31005),
			},
		},
	}
	serverYAML, err := (publisher.YAMLCompiler{}).Compile(ctx, state)
	if err != nil {
		t.Fatalf("compile R3 server YAML: %v", err)
	}
	secrets := []string{"r3-integration-controller-secret", "r3-mkcp-seed", "r3-trojan-password", "r3-legacy-ss-password"}
	validateWithMihomo(t, ctx, cli, directory, "r3-server.yaml", serverYAML, secrets)
	for index := range state.Nodes {
		index := index
		t.Run(state.Nodes[index].Name, func(t *testing.T) {
			share, err := service.BuildShare(state, nodeIDs[index], userIDs[index])
			if err != nil {
				t.Fatalf("build client YAML: %v", err)
			}
			clientYAML := []byte(share.ClientYAML + "rules:\n  - MATCH,DIRECT\n")
			validateWithMihomo(t, ctx, cli, directory, state.Nodes[index].Name+"-client.yaml", clientYAML, secrets)
		})
	}
}

func TestGeneratedShadowsocksSecurityPluginsWithRealMihomo(t *testing.T) {
	binary := os.Getenv("M_UI_TEST_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("M_UI_TEST_MIHOMO_BINARY is not set")
	}
	cli, err := mihomo.NewCLI(binary)
	if err != nil {
		t.Fatalf("NewCLI() error = %v", err)
	}
	type variant struct {
		name     string
		security domain.VLESSSecuritySpec
		secrets  []string
	}
	variants := []variant{
		{
			name: "shadow-tls-v3",
			security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityShadowTLS, ShadowTLS: &domain.ShadowTLSConfig{
				Version:   3,
				Users:     []domain.ShadowTLSUser{{Name: "alice", Password: "plugin-shadow-tls-password"}},
				Handshake: domain.ShadowTLSHandshake{Destination: "www.example.com:443"},
			}},
			secrets: []string{"plugin-shadow-tls-password"},
		},
		{
			name: "restls",
			security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityResTLS, ResTLS: &domain.ResTLSConfig{
				Destination: "www.example.com:443", Password: "plugin-restls-password", VersionHint: "tls13",
			}},
			secrets: []string{"plugin-restls-password"},
		},
		{
			name: "jls",
			security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityJLS, JLS: &domain.JLSConfig{
				Destination: "www.example.com:443", ServerName: "www.example.com", ALPN: []string{"h2"},
				Users: []domain.JLSUser{{Username: "alice", Password: "plugin-jls-password"}},
			}},
			secrets: []string{"plugin-jls-password"},
		},
	}
	for index, current := range variants {
		current := current
		t.Run(current.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			directory := t.TempDir()
			nodeID := "b658d301-81c8-48f0-b681-b3ff0d96d99f"
			userID := "846726df-c9d3-4076-be2f-ef31989aa2ba"
			state := domain.DesiredState{
				AsOf:                         time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC),
				MihomoExternalControllerBind: domain.Endpoint{Host: "127.0.0.1", Port: uint16(39200 + index)},
				MihomoControllerConnect:      domain.Endpoint{Host: "127.0.0.1", Port: uint16(39200 + index)},
				ControllerSecret:             "plugin-controller-secret", PublicHost: "127.0.0.1",
				Nodes: []domain.Node{{
					ID: nodeID, Name: "ss-" + current.name, Enabled: true, ListenAddress: "127.0.0.1", Port: strconv.Itoa(32200 + index),
					Protocol: domain.ProtocolShadowsocks, SchemaVersion: domain.NodeSchemaVersion, Generation: 1,
					Shadowsocks: &domain.ShadowsocksSpec{Cipher: "aes-128-gcm", UDP: true, Security: current.security},
					Users:       []domain.NodeUser{{ID: userID, NodeID: nodeID, Name: "alice", Enabled: true, Shadowsocks: &domain.ShadowsocksCredential{Password: "plugin-ss-password"}}},
					AccessProfiles: []domain.AccessProfile{{
						ID: "2b6c8248-cefb-45ad-8767-4f28df86c03a", NodeID: nodeID, Name: "default", Default: true,
						PublicHost: "127.0.0.1", PublicPort: uint16(32200 + index), ServerName: "www.example.com",
						Fingerprint: domain.ClientFingerprint, AllowInsecure: true,
					}},
				}},
			}
			serverYAML, err := (publisher.YAMLCompiler{}).Compile(ctx, state)
			if err != nil {
				t.Fatalf("compile %s server YAML: %v", current.name, err)
			}
			share, err := service.BuildShare(state, nodeID, userID)
			if err != nil {
				t.Fatalf("compile %s client YAML: %v", current.name, err)
			}
			secrets := append([]string{"plugin-controller-secret", "plugin-ss-password"}, current.secrets...)
			validateWithMihomo(t, ctx, cli, directory, current.name+"-server.yaml", serverYAML, secrets)
			clientYAML := []byte(share.ClientYAML + "rules:\n  - MATCH,DIRECT\n")
			validateWithMihomo(t, ctx, cli, directory, current.name+"-client.yaml", clientYAML, secrets)
		})
	}
}

func TestGeneratedCoreCipherOptionsWithRealMihomo(t *testing.T) {
	binary := os.Getenv("M_UI_TEST_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("M_UI_TEST_MIHOMO_BINARY is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cli, err := mihomo.NewCLI(binary)
	if err != nil {
		t.Fatalf("NewCLI() error = %v", err)
	}
	certificateRoot := t.TempDir()
	certificatePath, privateKeyPath := writeTestCertificate(t, certificateRoot)

	type cipherScenario struct {
		protocol domain.ProtocolKind
		option   string
	}
	var scenarios []cipherScenario
	for _, option := range []string{"auto", "aes-128-gcm", "chacha20-poly1305", "none", "zero"} {
		scenarios = append(scenarios, cipherScenario{protocol: domain.ProtocolVMess, option: option})
	}
	for _, option := range []string{
		"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305",
		"aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "none", "xchacha20-ietf-poly1305",
	} {
		scenarios = append(scenarios, cipherScenario{protocol: domain.ProtocolShadowsocks, option: option})
	}
	for _, option := range []string{"aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305"} {
		scenarios = append(scenarios, cipherScenario{protocol: domain.ProtocolTrojan, option: option})
	}

	for _, current := range scenarios {
		current := current
		t.Run(string(current.protocol)+"/"+current.option, func(t *testing.T) {
			directory := t.TempDir()
			nodeID := "a893a54c-c1e8-4a2b-8c70-e7432b6ed82e"
			userID := "aee77e49-91ca-4f09-9233-fce79165c580"
			node := domain.Node{
				ID: nodeID, Name: "cipher-option", Enabled: true, ListenAddress: "127.0.0.1", Port: "32443",
				Protocol: current.protocol, SchemaVersion: domain.NodeSchemaVersion, Generation: 1,
				AccessProfiles: []domain.AccessProfile{{
					ID: "b41d0c45-e1f9-4254-8c15-17656a453d58", NodeID: nodeID, Name: "default", Default: true,
					PublicHost: "127.0.0.1", PublicPort: 32443, ServerName: "hy2.example.com", AllowInsecure: true,
				}},
			}
			user := domain.NodeUser{ID: userID, NodeID: nodeID, Name: "cipher-user", Enabled: true}
			sensitive := []string{"cipher-controller-secret"}
			switch current.protocol {
			case domain.ProtocolVMess:
				node.VMess = &domain.VMessSpec{
					Handler:  domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
					Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
				}
				user.VMess = &domain.VMessCredential{UUID: "4f49ba3b-0bca-4117-9bd1-5a9c1ec6b31b", Cipher: current.option}
				sensitive = append(sensitive, user.VMess.UUID)
			case domain.ProtocolShadowsocks:
				node.Shadowsocks = &domain.ShadowsocksSpec{
					Cipher: current.option, UDP: true,
					Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
				}
				password := "cipher-legacy-password"
				if strings.HasPrefix(current.option, "2022-blake3-aes-128") {
					password = "MDEyMzQ1Njc4OWFiY2RlZg=="
				} else if strings.HasPrefix(current.option, "2022-blake3-") {
					password = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
				}
				user.Shadowsocks = &domain.ShadowsocksCredential{Password: password}
				sensitive = append(sensitive, password)
			case domain.ProtocolTrojan:
				node.Trojan = &domain.TrojanSpec{
					Handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
					Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityTLS, TLS: &domain.TLSConfig{
						Certificate: certificatePath, PrivateKey: privateKeyPath,
					}},
					Shadowsocks: domain.TrojanShadowsocksSpec{
						Enabled: true, Method: current.option, Password: "cipher-wrapper-password",
					},
				}
				user.Trojan = &domain.TrojanCredential{Password: "cipher-trojan-password"}
				sensitive = append(sensitive, node.Trojan.Shadowsocks.Password, user.Trojan.Password)
			default:
				t.Fatalf("unsupported cipher scenario protocol %q", current.protocol)
			}
			node.Users = []domain.NodeUser{user}
			state := domain.DesiredState{
				AsOf:                         time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC),
				MihomoExternalControllerBind: domain.Endpoint{Host: "127.0.0.1", Port: 39400},
				MihomoControllerConnect:      domain.Endpoint{Host: "127.0.0.1", Port: 39400},
				ControllerSecret:             "cipher-controller-secret", PublicHost: "127.0.0.1",
				Nodes: []domain.Node{node},
			}
			serverYAML, err := (publisher.YAMLCompiler{}).Compile(ctx, state)
			if err != nil {
				t.Fatalf("compile server YAML: %v", err)
			}
			share, err := service.BuildShare(state, nodeID, userID)
			if err != nil {
				t.Fatalf("compile client YAML: %v", err)
			}
			name := string(current.protocol) + "-" + current.option
			validateWithMihomo(t, ctx, cli, directory, name+"-server.yaml", serverYAML, sensitive)
			clientYAML := []byte(share.ClientYAML + "rules:\n  - MATCH,DIRECT\n")
			validateWithMihomo(t, ctx, cli, directory, name+"-client.yaml", clientYAML, sensitive)
		})
	}
}

func TestR3ProtocolsTransferDataWithRealMihomo(t *testing.T) {
	binary := os.Getenv("M_UI_TEST_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("M_UI_TEST_MIHOMO_BINARY is not set")
	}
	echo := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(response, "m-ui-r3-real-transfer:"+request.URL.Path)
	}))
	defer echo.Close()

	type scenario struct {
		name        string
		protocol    domain.ProtocolKind
		udpListener bool
		build       func(string, string) (*domain.Node, string)
	}
	scenarios := []scenario{
		{
			name: "vmess-raw", protocol: domain.ProtocolVMess,
			build: func(_, _ string) (*domain.Node, string) {
				return &domain.Node{VMess: &domain.VMessSpec{
					Handler:  domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
					Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
				}}, ""
			},
		},
		{
			name: "vmess-mkcp", protocol: domain.ProtocolVMess, udpListener: true,
			build: func(_, _ string) (*domain.Node, string) {
				return &domain.Node{VMess: &domain.VMessSpec{
					Handler: domain.VLESSHandlerSpec{Type: domain.VMessHandlerMKCP, MKCP: &domain.MKCPConfig{
						MTU: 1350, TTI: 50, UplinkCapacity: 5, DownlinkCapacity: 20,
						WriteBuffer: 2097152, ReadBuffer: 2097152, Seed: "transfer-mkcp-seed", Header: "wireguard",
					}}, Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
				}}, ""
			},
		},
		{
			name: "vmess-mkcp-tls", protocol: domain.ProtocolVMess, udpListener: true,
			build: func(certificate, privateKey string) (*domain.Node, string) {
				return &domain.Node{VMess: &domain.VMessSpec{
					Handler: domain.VLESSHandlerSpec{Type: domain.VMessHandlerMKCP, MKCP: &domain.MKCPConfig{
						MTU: 1350, TTI: 50, UplinkCapacity: 5, DownlinkCapacity: 20,
						WriteBuffer: 2097152, ReadBuffer: 2097152, Seed: "transfer-mkcp-tls-seed", Header: "srtp",
					}},
					Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityTLS, TLS: &domain.TLSConfig{
						Certificate: certificate, PrivateKey: privateKey,
					}},
				}}, ""
			},
		},
		{
			name: "vmess-websocket-tls", protocol: domain.ProtocolVMess,
			build: func(certificate, privateKey string) (*domain.Node, string) {
				return &domain.Node{VMess: &domain.VMessSpec{
					Handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerWebSocket, WebSocket: &domain.WebSocketSpec{Path: "/vmess-websocket"}},
					Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityTLS, TLS: &domain.TLSConfig{
						Certificate: certificate, PrivateKey: privateKey,
					}},
				}}, ""
			},
		},
		{
			name: "vmess-grpc-tls", protocol: domain.ProtocolVMess,
			build: func(certificate, privateKey string) (*domain.Node, string) {
				return &domain.Node{VMess: &domain.VMessSpec{
					Handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerGRPC, GRPC: &domain.GRPCSpec{ServiceName: "vmess-grpc"}},
					Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityTLS, TLS: &domain.TLSConfig{
						Certificate: certificate, PrivateKey: privateKey,
					}},
				}}, ""
			},
		},
		{
			name: "trojan-tls", protocol: domain.ProtocolTrojan,
			build: func(certificate, privateKey string) (*domain.Node, string) {
				return &domain.Node{Trojan: &domain.TrojanSpec{
					Handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
					Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityTLS, TLS: &domain.TLSConfig{
						Certificate: certificate, PrivateKey: privateKey,
					}},
				}}, "transfer-trojan-password"
			},
		},
		{
			name: "trojan-websocket-tls", protocol: domain.ProtocolTrojan,
			build: func(certificate, privateKey string) (*domain.Node, string) {
				return &domain.Node{Trojan: &domain.TrojanSpec{
					Handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerWebSocket, WebSocket: &domain.WebSocketSpec{Path: "/trojan-websocket"}},
					Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityTLS, TLS: &domain.TLSConfig{
						Certificate: certificate, PrivateKey: privateKey,
					}},
				}}, "transfer-trojan-password"
			},
		},
		{
			name: "trojan-grpc-tls", protocol: domain.ProtocolTrojan,
			build: func(certificate, privateKey string) (*domain.Node, string) {
				return &domain.Node{Trojan: &domain.TrojanSpec{
					Handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerGRPC, GRPC: &domain.GRPCSpec{ServiceName: "trojan-grpc"}},
					Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityTLS, TLS: &domain.TLSConfig{
						Certificate: certificate, PrivateKey: privateKey,
					}},
				}}, "transfer-trojan-password"
			},
		},
		{
			name: "shadowsocks-2022", protocol: domain.ProtocolShadowsocks,
			build: func(_, _ string) (*domain.Node, string) {
				return &domain.Node{Shadowsocks: &domain.ShadowsocksSpec{
					Cipher: "2022-blake3-aes-256-gcm", UDP: true,
					Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
				}}, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
			},
		},
		{
			name: "shadowsocks-simple-obfs", protocol: domain.ProtocolShadowsocks,
			build: func(_, _ string) (*domain.Node, string) {
				return &domain.Node{Shadowsocks: &domain.ShadowsocksSpec{
					Cipher: "aes-128-gcm", UDP: false,
					Security:   domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
					SimpleObfs: domain.SimpleObfsSpec{Enabled: true, Mode: "http"},
				}}, "transfer-simple-obfs-password"
			},
		},
	}
	for _, current := range scenarios {
		current := current
		t.Run(current.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			serverDirectory := t.TempDir()
			clientDirectory := t.TempDir()
			certificatePath, privateKeyPath := writeTestCertificate(t, serverDirectory)
			nodePort := availableTCPPortForHost(t, "127.0.0.1")
			if current.udpListener {
				nodePort = availableUDPPortForHost(t, "127.0.0.1")
			}
			controllerPort := availableTCPPortForHost(t, "127.0.0.1")
			clientPort := availableTCPPortForHost(t, "127.0.0.1")
			node, credential := current.build(certificatePath, privateKeyPath)
			nodeID := "0cf1da53-6d60-476e-ab1c-643aebfbc94b"
			userID := "c7485528-c75a-471f-9e82-52485535885e"
			profileID := "450a6f6e-a05a-417e-99c3-412b88098c46"
			node.ID, node.Name, node.Enabled = nodeID, current.name, true
			node.ListenAddress, node.Port = "127.0.0.1", fmt.Sprint(nodePort)
			node.Protocol, node.SchemaVersion, node.Generation = current.protocol, domain.NodeSchemaVersion, 1
			user := domain.NodeUser{ID: userID, NodeID: nodeID, Name: "transfer-user", Enabled: true}
			switch current.protocol {
			case domain.ProtocolVMess:
				user.VMess = &domain.VMessCredential{UUID: "7b791378-08c3-4fdb-b731-5b9a0d1d1aec", Cipher: "auto"}
			case domain.ProtocolTrojan:
				user.Trojan = &domain.TrojanCredential{Password: credential}
			case domain.ProtocolShadowsocks:
				user.Shadowsocks = &domain.ShadowsocksCredential{Password: credential}
			}
			node.Users = []domain.NodeUser{user}
			node.AccessProfiles = []domain.AccessProfile{{
				ID: profileID, NodeID: nodeID, Name: "default", Default: true,
				PublicHost: "127.0.0.1", PublicPort: uint16(nodePort), ServerName: "hy2.example.com",
				Fingerprint: domain.ClientFingerprint, AllowInsecure: true,
			}}
			state := domain.DesiredState{
				AsOf:                         time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC),
				MihomoExternalControllerBind: domain.Endpoint{Host: "127.0.0.1", Port: uint16(controllerPort)},
				MihomoControllerConnect:      domain.Endpoint{Host: "127.0.0.1", Port: uint16(controllerPort)},
				ControllerSecret:             "transfer-controller-secret", PublicHost: "127.0.0.1", Nodes: []domain.Node{*node},
			}
			serverYAML, err := (publisher.YAMLCompiler{}).Compile(ctx, state)
			if err != nil {
				t.Fatalf("compile server YAML: %v", err)
			}
			share, err := service.BuildShare(state, nodeID, userID)
			if err != nil {
				t.Fatalf("compile client YAML: %v", err)
			}
			proxyName := node.Name + " - " + user.Name
			clientYAML := []byte(share.ClientYAML + fmt.Sprintf(
				"mixed-port: %d\nmode: rule\nrules:\n  - MATCH,%s\n", clientPort, proxyName,
			))
			secrets := []string{"transfer-controller-secret", credential, "transfer-mkcp-seed", "transfer-mkcp-tls-seed"}
			cli, err := mihomo.NewCLI(binary)
			if err != nil {
				t.Fatal(err)
			}
			validateWithMihomo(t, ctx, cli, serverDirectory, "server.yaml", serverYAML, secrets)
			validateWithMihomo(t, ctx, cli, clientDirectory, "client.yaml", clientYAML, secrets)
			serverConfig := filepath.Join(serverDirectory, "server.yaml")
			server := startMihomoForTransfer(t, ctx, binary, serverDirectory, serverConfig)
			defer server.stop()
			waitForController(t, ctx, state.MihomoControllerConnect.Address(), state.ControllerSecret, server, secrets)
			clientConfig := filepath.Join(clientDirectory, "client.yaml")
			clientProcess := startMihomoForTransfer(t, ctx, binary, clientDirectory, clientConfig)
			defer clientProcess.stop()
			waitForTCPPort(t, ctx, net.JoinHostPort("127.0.0.1", strconv.Itoa(clientPort)), clientProcess, secrets)

			proxyURL, err := url.Parse("http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(clientPort)))
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 10 * time.Second}
			response, err := client.Get(echo.URL + "/" + current.name)
			if err != nil {
				t.Fatalf(
					"transfer through %s: %v\nserver: %s\nclient: %s",
					current.name,
					err,
					server.diagnostic(secrets),
					clientProcess.diagnostic(secrets),
				)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
			if err != nil {
				t.Fatal(err)
			}
			expected := "m-ui-r3-real-transfer:/" + current.name
			if response.StatusCode != http.StatusOK || string(body) != expected {
				t.Fatalf("transfer response = %d %q, want 200 %q", response.StatusCode, body, expected)
			}
		})
	}
}

func TestR3CutoverReloadsEmptyBaselineAndClosesLegacyListenerWithRealMihomo(t *testing.T) {
	binary := os.Getenv("M_UI_TEST_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("M_UI_TEST_MIHOMO_BINARY is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	directory := t.TempDir()
	controllerPort := availableTCPPortForHost(t, "127.0.0.1")
	legacyPort := availableTCPPortForHost(t, "127.0.0.1")
	controllerSecret := "r3-cutover-controller-secret"
	nodeID := "1d66931d-c7ee-4028-bdbd-e9aa97a48013"
	state := domain.DesiredState{
		AsOf:                         time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC),
		MihomoExternalControllerBind: domain.Endpoint{Host: "127.0.0.1", Port: uint16(controllerPort)},
		MihomoControllerConnect:      domain.Endpoint{Host: "127.0.0.1", Port: uint16(controllerPort)},
		ControllerSecret:             controllerSecret,
		PublicHost:                   "127.0.0.1",
		Nodes: []domain.Node{{
			ID: nodeID, Name: "legacy-runtime-listener", Enabled: true,
			ListenAddress: "127.0.0.1", Port: strconv.Itoa(legacyPort),
			Protocol: domain.ProtocolVMess, SchemaVersion: domain.NodeSchemaVersion, Generation: 1,
			VMess: &domain.VMessSpec{
				Handler:  domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
				Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
			},
			Users: []domain.NodeUser{{
				ID: "3f67885d-3d5c-4789-b1f6-027087f1bf32", NodeID: nodeID,
				Name: "legacy", Enabled: true,
				VMess: &domain.VMessCredential{UUID: "707469b6-5f7f-4c28-a708-47f7517c3e11", Cipher: "auto"},
			}},
			AccessProfiles: []domain.AccessProfile{{
				ID: "de8864e0-6302-414c-83a1-c489f58c1272", NodeID: nodeID,
				Name: "default", Default: true, PublicHost: "127.0.0.1", PublicPort: uint16(legacyPort),
			}},
		}},
	}
	staleYAML, err := (publisher.YAMLCompiler{}).Compile(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	emptyState := state
	emptyState.Nodes = nil
	emptyYAML, err := (publisher.YAMLCompiler{}).Compile(ctx, emptyState)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "mihomo", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, staleYAML, 0o640); err != nil {
		t.Fatal(err)
	}
	running := startMihomoForTransfer(t, ctx, binary, directory, configPath)
	defer running.stop()
	waitForController(t, ctx, emptyState.MihomoControllerConnect.Address(), controllerSecret, running, []string{controllerSecret})
	legacyAddress := net.JoinHostPort("127.0.0.1", strconv.Itoa(legacyPort))
	waitForTCPPort(t, ctx, legacyAddress, running, []string{controllerSecret})

	database, err := store.Open(ctx, filepath.Join(directory, "m-ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{41, 42, 43})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := store.NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := repository.BeginImmediate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.ReplaceDesiredState(ctx, emptyState); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	cli, err := mihomo.NewCLI(binary)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := mihomo.NewController(emptyState.MihomoControllerConnect.Address(), controllerSecret)
	if err != nil {
		t.Fatal(err)
	}
	configurationPublisher, err := publisher.New(
		repository,
		publisher.YAMLCompiler{},
		cli,
		controller,
		&observedIntegrationProcess{running: running},
		publisher.Options{
			ConfigPath:        configPath,
			RevisionDirectory: filepath.Join(directory, "revisions"),
			HistoryLimit:      20,
			HealthTimeout:     5 * time.Second,
			HealthInterval:    50 * time.Millisecond,
			Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := configurationPublisher.ReconcileStartupBeforeRuntime(ctx); err != nil {
		t.Fatalf("pre-runtime convergence: %v", err)
	}
	activeYAML, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(activeYAML) != string(emptyYAML) || !strings.Contains(string(activeYAML), "listeners: []") {
		t.Fatalf("active YAML is not the empty cutover baseline:\n%s", activeYAML)
	}
	// Pre-runtime publication replaces the file but deliberately cannot claim
	// that the already-running legacy listener has applied it.
	waitForTCPPort(t, ctx, legacyAddress, running, []string{controllerSecret})
	convergence, err := repository.RuntimeConvergenceState(ctx)
	if err != nil || !convergence.R3CutoverPending {
		t.Fatalf("pre-runtime convergence state = %#v, %v", convergence, err)
	}

	if err := configurationPublisher.ReconcileStartup(ctx); err != nil {
		t.Fatalf("post-runtime convergence: %v", err)
	}
	waitForTCPPortClosed(t, ctx, legacyAddress, running, []string{controllerSecret})
	convergence, err = repository.RuntimeConvergenceState(ctx)
	if err != nil || convergence.R3CutoverPending {
		t.Fatalf("post-runtime convergence state = %#v, %v", convergence, err)
	}
	activeRevision, err := repository.LatestActiveRevision(ctx)
	if err != nil || activeRevision.Reason != "r3_protocol_cutover_baseline" {
		t.Fatalf("active cutover revision = %#v, %v", activeRevision, err)
	}
}

func TestGeneratedAdvancedVLESSVariantsWithRealMihomo(t *testing.T) {
	binary := os.Getenv("M_UI_TEST_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("M_UI_TEST_MIHOMO_BINARY is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	directory := t.TempDir()
	certificatePath, privateKeyPath := writeTestCertificate(t, directory)
	cli, err := mihomo.NewCLI(binary)
	if err != nil {
		t.Fatalf("NewCLI() error = %v", err)
	}

	type variant struct {
		name     string
		handler  domain.VLESSHandlerSpec
		security domain.VLESSSecuritySpec
		flow     string
	}
	variants := []variant{
		{
			name: "xhttp-none",
			handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerXHTTP, XHTTP: &domain.XHTTPConfig{
				Path: "/xhttp", Host: "cdn.example.com", Mode: "auto", XPaddingBytes: "100-1000",
			}},
			security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
		},
		{
			name:     "grpc-tls",
			handler:  domain.VLESSHandlerSpec{Type: domain.VLESSHandlerGRPC, GRPC: &domain.GRPCSpec{ServiceName: "edge"}},
			security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityTLS, TLS: &domain.TLSConfig{Certificate: certificatePath, PrivateKey: privateKeyPath, AllowInsecure: true}},
		},
		{
			name:     "raw-tls-vision",
			handler:  domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
			security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityTLS, TLS: &domain.TLSConfig{Certificate: certificatePath, PrivateKey: privateKeyPath, AllowInsecure: true}},
			flow:     domain.VLESSFlowVision,
		},
		{
			name:    "raw-shadow-tls-v3",
			handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
			security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityShadowTLS, ShadowTLS: &domain.ShadowTLSConfig{
				Version: 3, Users: []domain.ShadowTLSUser{{Name: "alice", Password: "shadow-secret"}},
				Handshake: domain.ShadowTLSHandshake{Destination: "www.example.com:443"}, StrictMode: true,
			}},
		},
		{
			name:     "websocket-res-tls",
			handler:  domain.VLESSHandlerSpec{Type: domain.VLESSHandlerWebSocket, WebSocket: &domain.WebSocketSpec{Path: "/edge"}},
			security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityResTLS, ResTLS: &domain.ResTLSConfig{Destination: "www.example.com:443", Password: "restls-secret"}},
		},
		{
			name:    "raw-jls",
			handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
			security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityJLS, JLS: &domain.JLSConfig{
				Users:       []domain.JLSUser{{Username: "alice", Password: "jls-secret"}},
				Destination: "www.example.com:443", ServerName: "www.example.com", ALPN: []string{"h2"},
			}},
		},
	}
	nodeIDs := []string{
		"41464388-95d8-42f6-b393-f4181019ac4a", "84a8593f-e62b-43f3-9c50-f6a45b190021",
		"5de289cb-bd06-4229-a4f4-fe73f2402c66", "65072cc7-0fd9-4d48-9679-c23a2905f0bb",
		"f0745dc9-f927-4b69-888e-fd02930ee159", "01fc101e-2135-4b69-b788-251db66a5dc8",
	}
	userIDs := []string{
		"85816360-b0e9-4813-999c-b1f3a8cc6cf9", "ff2fc38c-1c9b-49bd-9162-8d9187977cd5",
		"9cbb2ca3-623e-45e4-a249-3b1393291761", "74555d11-11a8-456d-96d2-b0686cde8269",
		"16267745-223c-4dcc-88d9-1076530ab982", "613d0c65-50bf-445a-a045-f7198ded90db",
	}
	profileIDs := []string{
		"99d40070-7f4f-47df-b319-90dcb2cf5cdd", "5dc9a870-26c5-4aa0-9375-a36ac30b4ec5",
		"7d7a66cd-6106-4c9e-8749-cd8c5d27446e", "a536af18-39dc-4c23-a663-7f5f183a0eae",
		"5c061389-555a-4a09-a77b-ec167e614243", "8b8fb915-18ed-41b3-a219-b609832239da",
	}
	state := domain.DesiredState{
		AsOf:                         time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC),
		MihomoExternalControllerBind: domain.Endpoint{Host: "127.0.0.1", Port: 29090},
		MihomoControllerConnect:      domain.Endpoint{Host: "127.0.0.1", Port: 29090},
		ControllerSecret:             "advanced-vless-controller-secret",
		PublicHost:                   "node.example.com",
	}
	for index, current := range variants {
		port := uint16(26443 + index)
		state.Nodes = append(state.Nodes, domain.Node{
			ID: nodeIDs[index], Name: current.name, Enabled: true, ListenAddress: "127.0.0.1",
			Port: fmt.Sprint(port), Protocol: domain.ProtocolVLESS, SchemaVersion: domain.NodeSchemaVersion,
			VLESS: &domain.VLESSSpec{Decryption: "none", Handler: current.handler, Security: current.security},
			Users: []domain.NodeUser{{
				ID: userIDs[index], NodeID: nodeIDs[index], Name: "alice", Enabled: true,
				VLESS: &domain.VLESSCredential{UUID: "226c63f7-e6f3-48c9-9ce2-02d5838743ac", Flow: current.flow},
			}},
			AccessProfiles: []domain.AccessProfile{{
				ID: profileIDs[index], NodeID: nodeIDs[index], Name: "default", Default: true,
				PublicPort: port, ServerName: "www.example.com", AllowInsecure: true,
				Fingerprint: domain.ClientFingerprint, PacketEncoding: domain.PacketEncodingXUDP,
			}},
			Generation: 1,
		})
	}

	serverYAML, err := (publisher.YAMLCompiler{}).Compile(ctx, state)
	if err != nil {
		t.Fatalf("compile advanced VLESS server YAML: %v", err)
	}
	secrets := []string{"advanced-vless-controller-secret", "shadow-secret", "restls-secret", "jls-secret"}
	validateWithMihomo(t, ctx, cli, directory, "advanced-vless-server.yaml", serverYAML, secrets)
	for index, current := range variants {
		share, err := service.BuildShare(state, nodeIDs[index], userIDs[index])
		if err != nil {
			t.Fatalf("build %s client YAML: %v", current.name, err)
		}
		clientYAML := []byte(share.ClientYAML + "rules:\n  - MATCH,DIRECT\n")
		validateWithMihomo(t, ctx, cli, directory, current.name+"-client.yaml", clientYAML, secrets)
	}
}

func writeTestCertificate(t *testing.T, directory string) (string, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "hy2.example.com"},
		DNSNames: []string{"hy2.example.com"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePath := filepath.Join(directory, "hysteria2-cert.pem")
	privateKeyPath := filepath.Join(directory, "hysteria2-key.pem")
	if err := os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	keyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	if err := os.WriteFile(privateKeyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyBytes}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certificatePath, privateKeyPath
}

func validateWithMihomo(
	t *testing.T,
	ctx context.Context,
	cli *mihomo.CLI,
	directory string,
	name string,
	content []byte,
	sensitiveValues []string,
) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := cli.Validate(ctx, path); err != nil {
		diagnostic := []byte(err.Error())
		if binary := os.Getenv("M_UI_TEST_MIHOMO_BINARY"); binary != "" {
			command := exec.CommandContext(ctx, binary, "-t", "-f", path)
			if output, commandErr := command.CombinedOutput(); len(output) > 0 {
				diagnostic = append(diagnostic, append([]byte("\n"), output...)...)
			} else if commandErr != nil {
				diagnostic = append(diagnostic, []byte("\n"+commandErr.Error())...)
			}
		}
		t.Fatalf(
			"Mihomo rejected %s: %s",
			name,
			redactedOutput(diagnostic, sensitiveValues),
		)
	}
}

func redactedOutput(output []byte, sensitiveValues []string) string {
	if len(output) > 4096 {
		output = []byte(fmt.Sprintf("%s\n[output truncated]", output[:4096]))
	}
	result := string(output)
	for _, value := range sensitiveValues {
		if value != "" {
			result = strings.ReplaceAll(result, value, "[redacted]")
		}
	}
	return result
}

func availableTCPPortForHost(t *testing.T, host string) int {
	t.Helper()
	network := "tcp4"
	if strings.Contains(host, ":") {
		network = "tcp6"
	}
	listener, err := net.Listen(network, net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatalf("reserve TCP port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release TCP port: %v", err)
	}
	return port
}

func availableUDPPortForHost(t *testing.T, host string) int {
	t.Helper()
	network := "udp4"
	if strings.Contains(host, ":") {
		network = "udp6"
	}
	address, err := net.ResolveUDPAddr(network, net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatalf("resolve UDP address: %v", err)
	}
	listener, err := net.ListenUDP(network, address)
	if err != nil {
		t.Fatalf("reserve UDP port: %v", err)
	}
	port := listener.LocalAddr().(*net.UDPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release UDP port: %v", err)
	}
	return port
}

type synchronizedBuffer struct {
	mutex sync.Mutex
	data  bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(content []byte) (int, error) {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return buffer.data.Write(content)
}

func (buffer *synchronizedBuffer) snapshot() []byte {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return bytes.Clone(buffer.data.Bytes())
}

type runningMihomo struct {
	command  *exec.Cmd
	output   synchronizedBuffer
	done     chan error
	stopOnce sync.Once
	active   atomic.Bool
}

func startMihomoForTransfer(
	t *testing.T,
	ctx context.Context,
	binary, directory, configPath string,
) *runningMihomo {
	t.Helper()
	process := &runningMihomo{
		command: exec.CommandContext(ctx, binary, "-d", directory, "-f", configPath),
		done:    make(chan error, 1),
	}
	process.command.Env = append(os.Environ(), "HOME="+directory, "SAFE_PATHS="+directory)
	process.command.Stdout = &process.output
	process.command.Stderr = &process.output
	if err := process.command.Start(); err != nil {
		t.Fatalf("start Mihomo with %s: %v", filepath.Base(configPath), err)
	}
	process.active.Store(true)
	go func() {
		process.done <- process.command.Wait()
		process.active.Store(false)
		close(process.done)
	}()
	return process
}

type observedIntegrationProcess struct {
	running *runningMihomo
}

func (process *observedIntegrationProcess) IsActive(context.Context) (bool, error) {
	return process.running != nil && process.running.active.Load(), nil
}

func (*observedIntegrationProcess) Start(context.Context) error {
	return errors.New("integration process is already externally started")
}

func (process *observedIntegrationProcess) Stop(context.Context) error {
	process.running.stop()
	return nil
}

func (*observedIntegrationProcess) Restart(context.Context) error {
	return errors.New("integration process restart is unavailable")
}

func (*observedIntegrationProcess) Reload(context.Context) error {
	return errors.New("integration process reload is unavailable")
}

func (process *observedIntegrationProcess) RecentLogs(
	context.Context,
	int,
) ([]mihomo.LogEntry, error) {
	return nil, nil
}

func (process *runningMihomo) stop() {
	process.stopOnce.Do(func() {
		if process.command.Process != nil {
			_ = process.command.Process.Kill()
		}
		select {
		case <-process.done:
		case <-time.After(5 * time.Second):
		}
	})
}

func (process *runningMihomo) diagnostic(sensitiveValues []string) string {
	return redactedOutput(process.output.snapshot(), sensitiveValues)
}

func waitForController(
	t *testing.T,
	ctx context.Context,
	address, secret string,
	process *runningMihomo,
	sensitiveValues []string,
) {
	t.Helper()
	controller, err := mihomo.NewController(address, secret)
	if err != nil {
		t.Fatalf("create Mihomo Controller client: %v", err)
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err = controller.Version(ctx); err == nil {
			return
		}
		select {
		case processErr := <-process.done:
			t.Fatalf(
				"Mihomo exited before Controller became ready: %v: %s",
				processErr,
				process.diagnostic(sensitiveValues),
			)
		case <-deadline.C:
			t.Fatalf("Mihomo Controller did not become ready: %s", process.diagnostic(sensitiveValues))
		case <-ctx.Done():
			t.Fatalf("wait for Mihomo Controller: %v: %s", ctx.Err(), process.diagnostic(sensitiveValues))
		case <-ticker.C:
		}
	}
}

func waitForTCPPort(
	t *testing.T,
	ctx context.Context,
	address string,
	process *runningMihomo,
	sensitiveValues []string,
) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		select {
		case processErr := <-process.done:
			t.Fatalf(
				"Mihomo exited before %s became ready: %v: %s",
				address,
				processErr,
				process.diagnostic(sensitiveValues),
			)
		case <-deadline.C:
			t.Fatalf("Mihomo port %s did not become ready: %s", address, process.diagnostic(sensitiveValues))
		case <-ctx.Done():
			t.Fatalf("wait for Mihomo port %s: %v: %s", address, ctx.Err(), process.diagnostic(sensitiveValues))
		case <-ticker.C:
		}
	}
}

func waitForTCPPortClosed(
	t *testing.T,
	ctx context.Context,
	address string,
	process *runningMihomo,
	sensitiveValues []string,
) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
		if err != nil {
			return
		}
		_ = connection.Close()
		select {
		case processErr := <-process.done:
			t.Fatalf(
				"Mihomo exited while waiting for %s to close: %v: %s",
				address,
				processErr,
				process.diagnostic(sensitiveValues),
			)
		case <-deadline.C:
			t.Fatalf("Mihomo port %s remained open after empty-baseline reload: %s", address, process.diagnostic(sensitiveValues))
		case <-ctx.Done():
			t.Fatalf("wait for Mihomo port %s to close: %v: %s", address, ctx.Err(), process.diagnostic(sensitiveValues))
		case <-ticker.C:
		}
	}
}

func startAndReloadMihomo(
	t *testing.T,
	ctx context.Context,
	binary, directory, configPath, controllerAddress, controllerSecret string,
	sensitiveValues []string,
) {
	t.Helper()
	command := exec.CommandContext(
		ctx,
		binary,
		"-d",
		directory,
		"-f",
		configPath,
	)
	command.Env = append(
		os.Environ(),
		"HOME="+directory,
		"SAFE_PATHS="+directory,
	)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start Mihomo: %v", err)
	}
	stopped := false
	defer func() {
		if !stopped {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	controller, err := mihomo.NewController(controllerAddress, controllerSecret)
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err = controller.Version(ctx); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"Mihomo Controller did not become ready: %s",
				redactedOutput(output.Bytes(), sensitiveValues),
			)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for Mihomo Controller: %v", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := controller.Reload(ctx, configPath); err != nil {
		t.Fatalf("reload Mihomo configuration: %v", err)
	}
	if _, err := controller.Version(ctx); err != nil {
		t.Fatalf("Mihomo Controller unhealthy after reload: %v", err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatalf("stop Mihomo: %v", err)
	}
	_ = command.Wait()
	stopped = true
}
