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

func TestLoadRequiresExplicitFile(t *testing.T) {
	t.Parallel()

	if _, err := Load(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Fatal("Load() error = nil, want missing file error")
	}
}
