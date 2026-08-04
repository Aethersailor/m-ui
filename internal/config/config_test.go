package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesFileOverDefaults(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	content := []byte(`
[server]
listen_address = "127.0.0.2"
port = 3095

[logging]
format = "json"
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Address(), "127.0.0.2:3095"; got != want {
		t.Fatalf("Address() = %q, want %q", got, want)
	}
	if got, want := cfg.Logging.Level, "info"; got != want {
		t.Fatalf("Logging.Level = %q, want default %q", got, want)
	}
	if got, want := cfg.Logging.Format, "json"; got != want {
		t.Fatalf("Logging.Format = %q, want %q", got, want)
	}
}

func TestValidateAcceptsAutomaticPanelLanguage(t *testing.T) {
	t.Parallel()

	cfg := Default()
	if cfg.Panel.UILanguage != "auto" {
		t.Fatalf("default panel language = %q, want auto", cfg.Panel.UILanguage)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("automatic panel language rejected: %v", err)
	}
}

func TestDefaultPanelListensOnAllIPv4Interfaces(t *testing.T) {
	t.Parallel()

	cfg := Default()
	if got, want := cfg.Server.ListenAddress, "0.0.0.0"; got != want {
		t.Fatalf("default server listen address = %q, want %q", got, want)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default configuration rejected: %v", err)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(
		path,
		[]byte("[server]\nlisten_address = \"public.example\"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want invalid address error")
	}
}

func TestLoadAcceptsControllerBootstrapSecret(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(
		path,
		[]byte("[mihomo]\ncontroller_secret = \"synthetic-controller-secret-0001\"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Mihomo.ControllerSecret != "synthetic-controller-secret-0001" {
		t.Fatal("controller bootstrap secret was not loaded")
	}
}

func TestLoadRequiresExplicitFile(t *testing.T) {
	t.Parallel()

	if _, err := Load(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Fatal("Load() error = nil, want missing file error")
	}
}

func TestConfiguredPathWithinSupportsLinuxConfigurationOnAnyHost(t *testing.T) {
	t.Parallel()

	if !configuredPathWithin(
		"/etc/mihomo",
		"/etc/mihomo/config.yaml",
	) {
		t.Fatal("Linux child path was rejected")
	}
	if configuredPathWithin(
		"/etc/mihomo",
		"/etc/mihomo-other/config.yaml",
	) {
		t.Fatal("Linux sibling path was accepted")
	}
}

func TestLoadMapsLegacyControllerAddressToSplitEndpoints(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
[mihomo]
controller_address = "[::1]:9090"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Mihomo.ExternalControllerAddress != "[::1]:9090" ||
		cfg.Mihomo.ControllerConnectAddress != "[::1]:9090" {
		t.Fatalf("split endpoint compatibility = %#v", cfg.Mihomo)
	}
}

func TestLoadMapsLegacyWildcardControllerAddressToLoopbackClient(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		address    string
		wantBind   string
		wantClient string
	}{
		{
			name:       "ipv4 wildcard",
			address:    "0.0.0.0:9090",
			wantBind:   "0.0.0.0:9090",
			wantClient: "127.0.0.1:9090",
		},
		{
			name:       "ipv6 wildcard",
			address:    "[::]:9090",
			wantBind:   "[::]:9090",
			wantClient: "[::1]:9090",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			content := []byte("[mihomo]\ncontroller_address = \"" + test.address + "\"\n")
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Mihomo.ExternalControllerAddress != test.wantBind ||
				cfg.Mihomo.ControllerConnectAddress != test.wantClient {
				t.Fatalf("split endpoint compatibility = %#v", cfg.Mihomo)
			}
		})
	}
}

func TestLoadAcceptsWildcardIPv4IPv6BindAndExactCORS(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
[server]
listen_address = "::"

[mihomo]
external_controller_address = "[::]:9090"
controller_connect_address = "[::1]:9090"
external_controller_cors_origins = ["https://dashboard.example.com"]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Address(), "[::]:2095"; got != want {
		t.Fatalf("panel IPv6 Address() = %q, want %q", got, want)
	}
}

func TestLoadRejectsNonLoopbackControllerConnectAddress(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
[mihomo]
controller_connect_address = "192.0.2.1:9090"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want remote controller connect rejection")
	}
}
