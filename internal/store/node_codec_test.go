package store

import (
	"strings"
	"testing"

	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/domain"
)

func TestNodeCodecRoundTripsNewProtocolSecrets(t *testing.T) {
	t.Parallel()
	sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{9, 8, 7, 6})
	if err != nil {
		t.Fatal(err)
	}
	node := domain.Node{
		ID: "603465bc-831a-46a5-bd80-2668f8da48ed", Protocol: domain.ProtocolTrojan,
		SchemaVersion: domain.NodeSchemaVersion,
		Trojan: &domain.TrojanSpec{
			Handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
			Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityReality, Reality: &domain.RealityConfig{
				PrivateKey: "reality-private", PublicKey: "reality-public",
			}},
			Shadowsocks: domain.TrojanShadowsocksSpec{Enabled: true, Method: "aes-128-gcm", Password: "wrapper-secret"},
		},
	}
	configJSON, ciphertext, err := encodeNodeProtocol(sealer, node)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"reality-private", "wrapper-secret"} {
		if strings.Contains(configJSON, secret) {
			t.Fatalf("config JSON leaked %q: %s", secret, configJSON)
		}
	}
	decoded := domain.Node{ID: node.ID, Protocol: node.Protocol, SchemaVersion: node.SchemaVersion}
	if err := decodeNodeProtocol(sealer, &decoded, configJSON, ciphertext); err != nil {
		t.Fatal(err)
	}
	if decoded.Trojan == nil || decoded.Trojan.Security.Reality.PrivateKey != "reality-private" || decoded.Trojan.Shadowsocks.Password != "wrapper-secret" {
		t.Fatalf("decoded Trojan = %#v", decoded.Trojan)
	}

	user := domain.NodeUser{ID: "03251dfd-d03a-4be9-8721-9b971a12dbca", NodeID: node.ID, Trojan: &domain.TrojanCredential{Password: "user-secret"}}
	credentialCiphertext, err := encodeUserCredential(sealer, user, node.Protocol)
	if err != nil {
		t.Fatal(err)
	}
	decodedUser := domain.NodeUser{ID: user.ID, NodeID: user.NodeID}
	if err := decodeUserCredential(sealer, &decodedUser, node.Protocol, credentialCiphertext); err != nil {
		t.Fatal(err)
	}
	if decodedUser.Trojan == nil || decodedUser.Trojan.Password != "user-secret" {
		t.Fatalf("decoded user = %#v", decodedUser)
	}

	vmess := domain.Node{
		ID: "ab6d95bd-a92a-4839-9bc8-d1a150c68c67", Protocol: domain.ProtocolVMess,
		SchemaVersion: domain.NodeSchemaVersion,
		VMess: &domain.VMessSpec{
			Handler:  domain.VLESSHandlerSpec{Type: domain.VMessHandlerMKCP, MKCP: &domain.MKCPConfig{Seed: "mkcp-secret"}},
			Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
		},
	}
	configJSON, ciphertext, err = encodeNodeProtocol(sealer, vmess)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(configJSON, "mkcp-secret") {
		t.Fatalf("config JSON leaked mKCP seed: %s", configJSON)
	}
	decoded = domain.Node{ID: vmess.ID, Protocol: vmess.Protocol, SchemaVersion: vmess.SchemaVersion}
	if err := decodeNodeProtocol(sealer, &decoded, configJSON, ciphertext); err != nil {
		t.Fatal(err)
	}
	if decoded.VMess == nil || decoded.VMess.Handler.MKCP == nil || decoded.VMess.Handler.MKCP.Seed != "mkcp-secret" {
		t.Fatalf("decoded VMess = %#v", decoded.VMess)
	}
}
