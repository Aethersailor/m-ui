package publisher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aethersailor/m-ui/internal/domain"
)

func TestYAMLCompilerMatchesGoldenAndIsDeterministic(t *testing.T) {
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
	golden, err := os.ReadFile(filepath.Join("testdata", "vless_reality.golden.yaml"))
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}
	if string(first) != string(golden) {
		t.Fatalf("compiled YAML differs from golden\n--- got ---\n%s\n--- want ---\n%s", first, golden)
	}
}

func TestYAMLCompilerSortsAndFiltersManagedState(t *testing.T) {
	t.Parallel()
	state := compilerState()
	output, err := (YAMLCompiler{}).Compile(context.Background(), state)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	yaml := string(output)
	if strings.Index(yaml, "name: alpha") > strings.Index(yaml, "name: zeta") {
		t.Fatal("listeners are not sorted by name")
	}
	if strings.Index(yaml, "username: alice") > strings.Index(yaml, "username: zed") {
		t.Fatal("users are not sorted by name")
	}
	for _, excluded := range []string{"disabled-listener", "disabled-user", "expired-user"} {
		if strings.Contains(yaml, excluded) {
			t.Fatalf("compiled YAML contains filtered record %q", excluded)
		}
	}
}

func compilerState() domain.DesiredState {
	asOf := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	expired := asOf.Add(-time.Second)
	privateKey := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	publicKey := "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE"
	alphaID := "0e5a3b4b-a9a4-426f-ae22-3723044a67d8"
	zetaID := "cfa6f3fc-d8f4-4bd1-aae9-50ad62d3758d"
	return domain.DesiredState{
		AsOf:              asOf,
		ControllerAddress: "127.0.0.1:9090",
		ControllerSecret:  "controller-test-secret",
		PublicHost:        "node.example.com",
		Listeners: []domain.Listener{
			{
				ID:                zetaID,
				Name:              "zeta",
				Enabled:           true,
				ListenAddress:     "::",
				ListenPort:        8443,
				ServerName:        "zeta.example.com",
				RealityDest:       "zeta.example.com:443",
				RealityPrivateKey: privateKey,
				RealityPublicKey:  publicKey,
				ShortID:           "fedcba9876543210",
				Users: []domain.User{{
					ID:         "96334cb9-642a-4eb2-beab-a0ff8f277568",
					ListenerID: zetaID,
					Name:       "zed",
					Enabled:    true,
					UUID:       "aa8e5cf6-2ca0-4fd6-873c-9971b28a76b5",
				}},
			},
			{
				ID:                alphaID,
				Name:              "alpha",
				Enabled:           true,
				ListenAddress:     "0.0.0.0",
				ListenPort:        443,
				ServerName:        "www.example.com",
				RealityDest:       "www.example.com:443",
				RealityPrivateKey: privateKey,
				RealityPublicKey:  publicKey,
				ShortID:           "0123456789abcdef",
				UDPEnabled:        true,
				Users: []domain.User{
					{
						ID:         "81a91f2d-8c16-4589-9594-72331c234c0b",
						ListenerID: alphaID,
						Name:       "disabled-user",
						Enabled:    false,
						UUID:       "aef294fb-dd74-4df6-92a0-28631f546655",
					},
					{
						ID:         "547bd1df-5e89-4c01-981f-894499835e0e",
						ListenerID: alphaID,
						Name:       "zed",
						Enabled:    true,
						UUID:       "3503f9ac-2f35-44f1-83ae-fd4f27d36a70",
					},
					{
						ID:         "28688527-364a-45ce-991e-9ce153e253d0",
						ListenerID: alphaID,
						Name:       "expired-user",
						Enabled:    true,
						UUID:       "8903e93e-4284-4c70-9181-942cd1ebba18",
						ExpiresAt:  &expired,
					},
					{
						ID:         "7f989cf3-e6a7-47e6-917a-eb8344fe3578",
						ListenerID: alphaID,
						Name:       "alice",
						Enabled:    true,
						UUID:       "2c8dd707-c97c-400a-b343-86c9df5504bb",
					},
				},
			},
			{
				ID:                "5585e28a-2b50-4120-9d77-0ab44f1627e9",
				Name:              "disabled-listener",
				Enabled:           false,
				ListenAddress:     "0.0.0.0",
				ListenPort:        9443,
				ServerName:        "disabled.example.com",
				RealityDest:       "disabled.example.com:443",
				RealityPrivateKey: privateKey,
				RealityPublicKey:  publicKey,
				ShortID:           "1122334455667788",
			},
		},
	}
}
