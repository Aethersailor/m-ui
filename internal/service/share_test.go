package service

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Aethersailor/m-ui/internal/domain"
)

func TestBuildShareProducesVLESSURIQRContentAndMihomoYAML(t *testing.T) {
	t.Parallel()
	state := shareState()
	share, err := BuildShare(
		state,
		state.Listeners[0].ID,
		state.Listeners[0].Users[0].ID,
	)
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
	if parsed.Scheme != "vless" ||
		parsed.User.Username() != state.Listeners[0].Users[0].UUID ||
		parsed.Host != "edge.example.net:8443" ||
		parsed.Fragment != "node / east - Alice & Bob" {
		t.Fatalf("unexpected share URI: %s", share.URI)
	}
	expectedParameters := map[string]string{
		"encryption":     "none",
		"flow":           domain.VLESSFlow,
		"fp":             domain.ClientFingerprint,
		"packetEncoding": domain.PacketEncoding,
		"pbk":            state.Listeners[0].RealityPublicKey,
		"security":       "reality",
		"sid":            state.Listeners[0].ShortID,
		"sni":            state.Listeners[0].ServerName,
		"type":           "tcp",
	}
	for key, expected := range expectedParameters {
		if actual := parsed.Query().Get(key); actual != expected {
			t.Errorf("URI query %q = %q, want %q", key, actual, expected)
		}
	}
	for _, expected := range []string{
		"proxies:\n",
		"  - name: node / east - Alice & Bob\n",
		"    type: vless\n",
		"    server: edge.example.net\n",
		"    port: 8443\n",
		"    flow: xtls-rprx-vision\n",
		"    packet-encoding: xudp\n",
		"    tls: true\n",
		"    client-fingerprint: chrome\n",
		"    public-key: " + state.Listeners[0].RealityPublicKey + "\n",
		"    short-id: " + state.Listeners[0].ShortID + "\n",
		"    encryption: \"\"\n",
		"    network: tcp\n",
	} {
		if !strings.Contains(share.ClientYAML, expected) {
			t.Errorf("client YAML does not contain %q\n%s", expected, share.ClientYAML)
		}
	}
}

func TestBuildShareRejectsExpiredUser(t *testing.T) {
	t.Parallel()
	state := shareState()
	expiredAt := state.AsOf
	state.Listeners[0].Users[0].ExpiresAt = &expiredAt
	_, err := BuildShare(state, state.Listeners[0].ID, state.Listeners[0].Users[0].ID)
	if err == nil {
		t.Fatal("BuildShare() error = nil")
	}
}

func shareState() domain.DesiredState {
	publicPort := uint16(8443)
	listenerID := "1cf5d79b-7998-4efc-8e31-edf9d1319c16"
	return domain.DesiredState{
		AsOf:              time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
		ControllerAddress: "127.0.0.1:9090",
		ControllerSecret:  "controller-test-secret",
		PublicHost:        "global.example.net",
		Listeners: []domain.Listener{{
			ID:                 listenerID,
			Name:               "node / east",
			Enabled:            true,
			ListenAddress:      "0.0.0.0",
			ListenPort:         443,
			PublicHostOverride: "edge.example.net",
			PublicPortOverride: &publicPort,
			ServerName:         "www.example.com",
			RealityDest:        "www.example.com:443",
			RealityPrivateKey:  "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			RealityPublicKey:   "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE",
			ShortID:            "0123456789abcdef",
			UDPEnabled:         true,
			Users: []domain.User{{
				ID:         "390615fb-5b13-4f96-837d-5fe7eaf8ec39",
				ListenerID: listenerID,
				Name:       "Alice & Bob",
				Enabled:    true,
				UUID:       "261e47e2-8a59-4c53-876c-b8d41bd51c1e",
			}},
		}},
	}
}
