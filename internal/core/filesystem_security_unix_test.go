//go:build !windows

package core

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestFileStoreRejectsUnexpectedOwner(t *testing.T) {
	t.Parallel()
	files, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	unexpected := os.Geteuid() + 1
	files.expectedOwner = &unexpected
	if err := files.Prepare(); err == nil {
		t.Fatal("managed core root with an unexpected owner was accepted")
	}
}

func TestFileStoreKeepsCoreExecutableForServiceGroupUnderRestrictiveUmask(t *testing.T) {
	previous := syscall.Umask(0o077)
	defer syscall.Umask(previous)
	root := filepath.Join(t.TempDir(), "core")
	files, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := files.Prepare(); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(filepath.Dir(root), "source-mihomo")
	if err := os.WriteFile(source, []byte("synthetic-core"), 0o750); err != nil {
		t.Fatal(err)
	}
	stage, _, err := files.StageAdopted(source, "synthetic", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer files.RemoveStage(stage)
	info, err := os.Stat(filepath.Join(stage, "mihomo"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Fatalf("staged core mode = %o, want 750", got)
	}
}

func TestFileStoreRejectsSymlinkedManagedDirectory(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "core")
	if err := os.Symlink(target, root); err != nil {
		t.Fatal(err)
	}
	files, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := files.Prepare(); err == nil {
		t.Fatal("symbolic-link managed core root was accepted")
	}
}

func TestFileStoreRejectsSymlinkedCurrentDuringActivation(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	root := filepath.Join(parent, "core")
	files, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := files.Prepare(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "outside")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, files.current); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(parent, "source-mihomo")
	if err := os.WriteFile(source, []byte("synthetic-core"), 0o750); err != nil {
		t.Fatal(err)
	}
	stage, _, err := files.StageAdopted(source, "synthetic", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer files.RemoveStage(stage)
	if _, err := files.Activate(stage); err == nil {
		t.Fatal("symbolic-link current core path was accepted")
	}
}

func TestFileStoreAllowsActivationIntoEmptyCurrentDirectory(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "core")
	files, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := files.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(files.current, 0o750); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(filepath.Dir(root), "source-mihomo")
	if err := os.WriteFile(source, []byte("synthetic-core"), 0o750); err != nil {
		t.Fatal(err)
	}
	stage, _, err := files.StageAdopted(source, "synthetic", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer files.RemoveStage(stage)
	if _, err := files.Activate(stage); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if _, err := files.Current(); err != nil {
		t.Fatalf("Current() error = %v", err)
	}
}
