//go:build !windows

package core

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
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
	if _, err := files.Activate(stage); err != nil {
		t.Fatal(err)
	}
	currentInfo, err := os.Stat(files.current)
	if err != nil {
		t.Fatal(err)
	}
	if got := currentInfo.Mode().Perm(); got != 0o750 {
		t.Fatalf("active core directory mode = %o, want 750", got)
	}
}

func TestFileStoreDownloadedCoreKeepsServiceGroupUnderRestrictiveUmask(t *testing.T) {
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
	stage, archivePath, err := files.CreateDownloadStage()
	if err != nil {
		t.Fatal(err)
	}
	defer files.RemoveStage(stage)

	archiveFile, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(archiveFile)
	if _, err := io.WriteString(gzipWriter, "synthetic-downloaded-core"); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	archiveSum := sha256.Sum256(archiveBytes)
	identity := ReleaseIdentity{
		Channel:           ChannelRelease,
		Repository:        UpstreamRepository,
		ReleaseID:         1,
		TagName:           "v1.0.0",
		PublishedAt:       time.Unix(1, 0).UTC(),
		AssetID:           1,
		AssetName:         "mihomo-linux-amd64-compatible-v1.0.0.gz",
		AssetSize:         int64(len(archiveBytes)),
		AssetDigestSHA256: hex.EncodeToString(archiveSum[:]),
	}
	if _, err := files.FinalizeDownloadedStage(
		stage,
		identity,
		identity.AssetDigestSHA256,
		"synthetic-downloaded-core",
		time.Unix(2, 0),
	); err != nil {
		t.Fatal(err)
	}
	binaryInfo, err := os.Stat(filepath.Join(stage, "mihomo"))
	if err != nil {
		t.Fatal(err)
	}
	stageInfo, err := os.Stat(stage)
	if err != nil {
		t.Fatal(err)
	}
	binaryStat, ok := binaryInfo.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("downloaded core did not expose unix ownership")
	}
	stageStat, ok := stageInfo.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("staging directory did not expose unix ownership")
	}
	if binaryStat.Gid != stageStat.Gid {
		t.Fatalf("downloaded core gid = %d, staging gid = %d", binaryStat.Gid, stageStat.Gid)
	}
	if got := binaryInfo.Mode().Perm(); got != 0o750 {
		t.Fatalf("downloaded core mode = %o, want 750", got)
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
