//go:build !windows

package publisher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSharedConfigurationModeIgnoresRestrictiveUmask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	if err := writeSharedConfigSynced(path, []byte("mode: rule\n")); err != nil {
		t.Fatalf("writeSharedConfigSynced() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o640); got != want {
		t.Fatalf("configuration mode = %04o, want %04o", got, want)
	}
}
