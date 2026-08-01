//go:build !windows

package core

import (
	"os"
	"path/filepath"
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
