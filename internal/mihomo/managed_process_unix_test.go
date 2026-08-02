//go:build !windows

package mihomo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedProcessActiveFindsCurrentExecutable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	active, err := managedProcessActive(
		context.Background(),
		executable,
		filepath.Join(t.TempDir(), "config.yaml"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatalf("managedProcessActive(%q) = false, want true", executable)
	}
}

func TestManagedProcessActiveMissingAndCanceled(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-mihomo")
	active, err := managedProcessActive(
		context.Background(),
		missing,
		filepath.Join(t.TempDir(), "config.yaml"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("managedProcessActive() reported a missing binary as active")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := managedProcessActive(
		canceled,
		missing,
		filepath.Join(t.TempDir(), "config.yaml"),
	); err == nil {
		t.Fatal("managedProcessActive() with canceled context returned nil error")
	}
}

func TestManagedCoreIdentitySurvivesCurrentRename(t *testing.T) {
	root := filepath.Join(t.TempDir(), "core")
	current := filepath.Join(root, "current")
	backup := filepath.Join(root, "backups", "old")
	if err := os.MkdirAll(filepath.Dir(backup), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(current, 0o750); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(current, "mihomo")
	if err := os.WriteFile(old, []byte("synthetic"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(current, backup); err != nil {
		t.Fatal(err)
	}
	if !managedCoreExecutablePath(root, filepath.Join(backup, "mihomo")) {
		t.Fatal("renamed managed core was not recognized as a managed executable")
	}
	if !managedProcessCommandLine(
		[][]byte{[]byte(filepath.Join(backup, "mihomo")), []byte("-d"), []byte("/var/lib/mihomo"), []byte("-f"), []byte("/etc/m-ui/mihomo.yaml")},
		"/etc/m-ui/mihomo.yaml",
	) {
		t.Fatal("managed core command line identity was not recognized")
	}
}

func TestManagedProcessActiveFindsCoreAfterActivateBeforeRestart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "core")
	current := filepath.Join(root, "current")
	backup := filepath.Join(root, "backups", "old")
	if err := os.MkdirAll(current, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(backup), 0o750); err != nil {
		t.Fatal(err)
	}
	oldBinary := filepath.Join(current, "mihomo")
	if err := os.WriteFile(oldBinary, []byte("old"), 0o750); err != nil {
		t.Fatal(err)
	}
	// This models the exact filesystem transition performed by Activate. The
	// process is still executing the inode now reachable through backups/old;
	// m-ui has not reached its Restart boundary yet.
	if err := os.Rename(current, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(current, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, "mihomo"), []byte("new"), 0o750); err != nil {
		t.Fatal(err)
	}

	procRoot := filepath.Join(t.TempDir(), "proc")
	processRoot := filepath.Join(procRoot, "4242")
	if err := os.MkdirAll(processRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(backup, "mihomo"), filepath.Join(processRoot, "exe")); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "mihomo.yaml")
	commandLine := strings.Join([]string{
		filepath.Join(backup, "mihomo"),
		"-d",
		"/var/lib/mihomo",
		"-f",
		configPath,
		"",
	}, "\x00")
	if err := os.WriteFile(filepath.Join(processRoot, "cmdline"), []byte(commandLine), 0o600); err != nil {
		t.Fatal(err)
	}

	active, err := managedProcessActiveAt(
		context.Background(),
		procRoot,
		filepath.Join(current, "mihomo"),
		configPath,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("managedProcessActiveAt missed a process left in the activated core backup")
	}
}

func TestManagedProcessStartRejectsExistingMihomo(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process, err := NewManagedProcess(
		ctx,
		executable,
		filepath.Join(t.TempDir(), "config.yaml"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(ctx); err == nil {
		t.Fatal("Start() accepted an already active executable")
	}
}
