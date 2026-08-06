package domain

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDesiredStateValidationAndEffectiveUsers(t *testing.T) {
	t.Parallel()
	state := validState()
	expiredAt := state.AsOf
	state.Nodes[0].Users = append(state.Nodes[0].Users,
		NodeUser{
			ID:        "73de80e2-1f03-403f-bf58-dd96f82f0979",
			NodeID:    state.Nodes[0].ID,
			Name:      "expired",
			Enabled:   true,
			VLESS:     &VLESSCredential{UUID: "3494b4ca-6e48-48a1-8b69-b5d0f148f01b"},
			ExpiresAt: &expiredAt,
		},
		NodeUser{
			ID:      "4049e001-3abe-4609-96a1-4012473ef2df",
			NodeID:  state.Nodes[0].ID,
			Name:    "disabled",
			Enabled: false,
			VLESS:   &VLESSCredential{UUID: "72844a9a-6b70-4adf-a74e-2a8e5190d525"},
		},
	)

	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	effective := state.Nodes[0].EffectiveUsers(state.AsOf)
	if len(effective) != 1 || effective[0].Name != "active" {
		t.Fatalf("EffectiveUsers() = %#v, want only active user", effective)
	}
}

func TestDesiredStateRejectsPortConflictAndListenerWithoutEffectiveUser(t *testing.T) {
	t.Parallel()
	state := validState()
	second := state.Nodes[0]
	second.ID = "f73e3a5a-f2dd-4c04-bfa3-b99e01c34464"
	second.Name = "second"
	second.Users = []NodeUser{{
		ID:      "0ab9ea19-11c0-4ef0-90e0-480371515892",
		NodeID:  second.ID,
		Name:    "disabled",
		Enabled: false,
		VLESS:   &VLESSCredential{UUID: "abdefc62-7632-4e4f-873b-896c6276f2d6"},
	}}
	for index := range second.AccessProfiles {
		second.AccessProfiles[index].ID = "52b3bc0e-3ab2-4ef4-99cb-ec15f5447001"
		second.AccessProfiles[index].NodeID = second.ID
	}
	state.Nodes = append(state.Nodes, second)

	err := state.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want validation failures")
	}
	message := err.Error()
	for _, expected := range []string{
		"port range conflicts",
		"enabled node must have at least one enabled, unexpired user",
	} {
		if !strings.Contains(message, expected) {
			t.Errorf("Validate() error %q does not contain %q", message, expected)
		}
	}
}

func TestDesiredStateRejectsNonVersion4UserUUID(t *testing.T) {
	t.Parallel()
	state := validState()
	state.Nodes[0].Users[0].VLESS.UUID = "00000000-0000-1000-8000-000000000000"

	err := state.Validate()
	if err == nil || !strings.Contains(err.Error(), "RFC 4122 version 4 UUID") {
		t.Fatalf("Validate() error = %v, want UUID version failure", err)
	}
}

func TestValidateShadowTLSAndJLSProtocolCredentials(t *testing.T) {
	t.Parallel()

	shadowTLS := VLESSSpec{
		Handler: VLESSHandlerSpec{Type: VLESSHandlerRaw},
		Security: VLESSSecuritySpec{Type: VLESSSecurityShadowTLS, ShadowTLS: &ShadowTLSConfig{
			Version: 3,
			Users: []ShadowTLSUser{
				{Name: "alice", Password: "secret-a"},
				{Name: "alice", Password: "secret-b"},
			},
			Handshake: ShadowTLSHandshake{Destination: "www.example.com:443"},
		}},
	}
	if err := validateVLESS(shadowTLS); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("validateVLESS(ShadowTLS) error = %v, want duplicate-user failure", err)
	}

	jls := VLESSSpec{
		Handler: VLESSHandlerSpec{Type: VLESSHandlerRaw},
		Security: VLESSSecuritySpec{Type: VLESSSecurityJLS, JLS: &JLSConfig{
			Destination: "www.example.com:443",
			Users:       []JLSUser{{Username: "alice"}},
			ALPN:        []string{"h2", "h2"},
		}},
	}
	if err := validateVLESS(jls); err == nil ||
		!strings.Contains(err.Error(), "password is required") ||
		!strings.Contains(err.Error(), "ALPN value") {
		t.Fatalf("validateVLESS(JLS) error = %v, want credential and ALPN failures", err)
	}
}

func TestValidateHysteria2RealmMatchesRuntimeRequirements(t *testing.T) {
	t.Parallel()

	spec := Hysteria2Spec{
		Certificate: "/data/cert.pem",
		PrivateKey:  "/data/key.pem",
		Realm: &Hysteria2RealmConfig{
			Enabled:     true,
			ServerURL:   "https://realm.example.com",
			RealmID:     "edge-a",
			STUNServers: []string{"stun.example.com", "[2001:db8::1]:3478"},
		},
	}
	if err := validateHysteria2(spec); err != nil {
		t.Fatalf("validateHysteria2() error = %v", err)
	}

	spec.Realm.ServerURL = "realm.example.com"
	spec.Realm.RealmID = ""
	spec.Realm.STUNServers = nil
	if err := validateHysteria2(spec); err == nil ||
		!strings.Contains(err.Error(), "absolute HTTP or HTTPS URL") ||
		!strings.Contains(err.Error(), "Realm ID is required") ||
		!strings.Contains(err.Error(), "at least one STUN server") {
		t.Fatalf("validateHysteria2() error = %v, want Realm requirement failures", err)
	}
}

func TestGenerateUUIDIsRFC4122Version4(t *testing.T) {
	t.Parallel()
	value, err := GenerateUUID()
	if err != nil {
		t.Fatalf("GenerateUUID() error = %v", err)
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		t.Fatalf("parse generated UUID: %v", err)
	}
	if parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 {
		t.Fatalf("generated UUID version/variant = %v/%v", parsed.Version(), parsed.Variant())
	}
}

func TestGenerateShortIDUsesEightRandomBytes(t *testing.T) {
	t.Parallel()
	value, err := generateShortID(bytes.NewReader([]byte{
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
	}))
	if err != nil {
		t.Fatalf("generateShortID() error = %v", err)
	}
	if value != "0011223344556677" {
		t.Fatalf("generateShortID() = %q", value)
	}
	if err := ValidateShortID(value); err != nil {
		t.Fatalf("ValidateShortID() error = %v", err)
	}
}

func TestEndpointValidationSupportsWildcardIPv4AndIPv6ButKeepsConnectLoopbackOnly(t *testing.T) {
	t.Parallel()
	if err := ValidateBindEndpoint(
		Endpoint{Host: "0.0.0.0", Port: 2095},
		"panel",
	); err != nil {
		t.Fatalf("IPv4 wildcard bind rejected: %v", err)
	}
	if err := ValidateBindEndpoint(
		Endpoint{Host: "::", Port: 9090},
		"controller",
	); err != nil {
		t.Fatalf("IPv6 wildcard bind rejected: %v", err)
	}
	if err := ValidateConnectEndpoint(
		Endpoint{Host: "::1", Port: 9090},
		"controller",
	); err != nil {
		t.Fatalf("IPv6 loopback connect rejected: %v", err)
	}
	if err := ValidateConnectEndpoint(
		Endpoint{Host: "0.0.0.0", Port: 9090},
		"controller",
	); err == nil {
		t.Fatal("wildcard controller connect accepted")
	}
}

func TestNormalizeLegacyDefaultsPanelToAllIPv4Interfaces(t *testing.T) {
	t.Parallel()

	state, err := (DesiredState{}).NormalizeLegacy()
	if err != nil {
		t.Fatalf("NormalizeLegacy() error = %v", err)
	}
	if got, want := state.PanelUIBind, (Endpoint{Host: "0.0.0.0", Port: 2095}); got != want {
		t.Fatalf("panel UI bind = %#v, want %#v", got, want)
	}
}

func TestSplitLegacyControllerEndpointPreservesAddressFamily(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		input      Endpoint
		wantBind   Endpoint
		wantClient Endpoint
	}{
		{
			name:       "ipv4 loopback",
			input:      Endpoint{Host: "127.0.0.1", Port: 9090},
			wantBind:   Endpoint{Host: "127.0.0.1", Port: 9090},
			wantClient: Endpoint{Host: "127.0.0.1", Port: 9090},
		},
		{
			name:       "ipv4 wildcard",
			input:      Endpoint{Host: "0.0.0.0", Port: 9090},
			wantBind:   Endpoint{Host: "0.0.0.0", Port: 9090},
			wantClient: Endpoint{Host: "127.0.0.1", Port: 9090},
		},
		{
			name:       "ipv6 loopback",
			input:      Endpoint{Host: "::1", Port: 9090},
			wantBind:   Endpoint{Host: "::1", Port: 9090},
			wantClient: Endpoint{Host: "::1", Port: 9090},
		},
		{
			name:       "ipv6 wildcard",
			input:      Endpoint{Host: "::", Port: 9090},
			wantBind:   Endpoint{Host: "::", Port: 9090},
			wantClient: Endpoint{Host: "::1", Port: 9090},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bind, client, err := SplitLegacyControllerEndpoint(test.input)
			if err != nil {
				t.Fatalf("SplitLegacyControllerEndpoint() error = %v", err)
			}
			if !bind.Equal(test.wantBind) || !client.Equal(test.wantClient) {
				t.Fatalf("split = %#v, %#v; want %#v, %#v", bind, client, test.wantBind, test.wantClient)
			}
		})
	}
	if _, _, err := SplitLegacyControllerEndpoint(Endpoint{Host: "192.0.2.1", Port: 9090}); err == nil {
		t.Fatal("specific non-loopback legacy endpoint was silently migrated")
	}
}

func TestValidateControllerEndpointPairRequiresMatchingFamilyAndPort(t *testing.T) {
	t.Parallel()
	valid := []struct {
		bind    Endpoint
		connect Endpoint
	}{
		{Endpoint{Host: "127.0.0.1", Port: 9090}, Endpoint{Host: "127.0.0.1", Port: 9090}},
		{Endpoint{Host: "0.0.0.0", Port: 9090}, Endpoint{Host: "127.0.0.1", Port: 9090}},
		{Endpoint{Host: "::1", Port: 9090}, Endpoint{Host: "::1", Port: 9090}},
		{Endpoint{Host: "::", Port: 9090}, Endpoint{Host: "::1", Port: 9090}},
	}
	for _, test := range valid {
		if err := ValidateControllerEndpointPair(test.bind, test.connect); err != nil {
			t.Fatalf("valid pair %#v/%#v rejected: %v", test.bind, test.connect, err)
		}
	}
	for _, test := range []struct {
		bind    Endpoint
		connect Endpoint
	}{
		{Endpoint{Host: "0.0.0.0", Port: 9090}, Endpoint{Host: "::1", Port: 9090}},
		{Endpoint{Host: "::", Port: 9090}, Endpoint{Host: "127.0.0.1", Port: 9090}},
		{Endpoint{Host: "127.0.0.1", Port: 9090}, Endpoint{Host: "127.0.0.1", Port: 9091}},
	} {
		if err := ValidateControllerEndpointPair(test.bind, test.connect); err == nil {
			t.Fatalf("invalid pair %#v/%#v accepted", test.bind, test.connect)
		}
	}
}

func TestValidateCORSOriginsRejectsWildcardAndPaths(t *testing.T) {
	t.Parallel()
	if err := ValidateCORSOrigins([]string{"https://dashboard.example.com"}); err != nil {
		t.Fatalf("exact CORS origin rejected: %v", err)
	}
	for _, origin := range []string{
		"*",
		"https://dashboard.example.com/path",
		"https://dashboard.example.com:",
		"https://dashboard.example.com:65536",
	} {
		if err := ValidateCORSOrigins([]string{origin}); err == nil {
			t.Fatalf("CORS origin %q accepted", origin)
		}
	}
}

func validState() DesiredState {
	return DesiredState{
		AsOf:              time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
		ControllerAddress: "127.0.0.1:9090",
		ControllerSecret:  "controller-test-secret",
		PublicHost:        "node.example.com",
		Nodes: []Node{{
			ID:            "bf1f792d-7646-45b1-94b4-9845742d5ad1",
			Name:          "primary",
			Enabled:       true,
			ListenAddress: "0.0.0.0",
			Port:          "443",
			Protocol:      ProtocolVLESS,
			SchemaVersion: NodeSchemaVersion,
			VLESS: &VLESSSpec{
				Decryption: "none",
				Handler:    VLESSHandlerSpec{Type: VLESSHandlerRaw},
				Security: VLESSSecuritySpec{Type: VLESSSecurityReality, Reality: &RealityConfig{
					Destination: "www.example.com:443",
					PrivateKey:  "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
					PublicKey:   "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE",
					ShortIDs:    []string{"0123456789abcdef"}, ServerNames: []string{"www.example.com"},
				}},
			},
			Users: []NodeUser{{
				ID:      "e8e65007-0350-48a8-a992-33bc795b8ba3",
				NodeID:  "bf1f792d-7646-45b1-94b4-9845742d5ad1",
				Name:    "active",
				Enabled: true,
				VLESS:   &VLESSCredential{UUID: "1bd44e59-d67d-4af1-9a6d-b9488c8d8a9f", Flow: VLESSFlowVision},
			}},
			AccessProfiles: []AccessProfile{{
				ID: "a3a568b4-46fb-44d9-884e-dcfb72b9f7aa", NodeID: "bf1f792d-7646-45b1-94b4-9845742d5ad1",
				Name: "default", Default: true, PublicPort: 443, ServerName: "www.example.com",
				Fingerprint: ClientFingerprint, PacketEncoding: PacketEncodingXUDP,
			}},
			Generation: 1,
		}},
	}
}
