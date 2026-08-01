//go:build !windows

package mihomo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedProcessActiveFindsCurrentExecutable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	active, err := managedProcessActive(context.Background(), executable)
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatalf("managedProcessActive(%q) = false, want true", executable)
	}
}

func TestManagedProcessActiveMissingAndCanceled(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-mihomo")
	active, err := managedProcessActive(context.Background(), missing)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("managedProcessActive() reported a missing binary as active")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := managedProcessActive(canceled, missing); err == nil {
		t.Fatal("managedProcessActive() with canceled context returned nil error")
	}
}
