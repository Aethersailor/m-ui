package core

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxBinarySize = 256 << 20
)

type FileStore struct {
	root          string
	current       string
	staging       string
	backups       string
	expectedOwner *int
}

type Activation struct {
	backupPath string
}

func NewFileStore(root string) (*FileStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("managed core root must be absolute")
	}
	clean := filepath.Clean(root)
	return &FileStore{
		root:          clean,
		current:       filepath.Join(clean, "current"),
		staging:       filepath.Join(clean, "staging"),
		backups:       filepath.Join(clean, "backups"),
		expectedOwner: currentOwnerID(),
	}, nil
}

func (store *FileStore) Prepare() error {
	for _, directory := range []string{
		store.root,
		store.staging,
		store.backups,
	} {
		if err := ensureSecureDirectory(directory, 0o750); err != nil {
			return err
		}
		if err := store.validateOwner(directory); err != nil {
			return err
		}
	}
	return syncDirectory(store.root)
}

func (store *FileStore) Recover() error {
	if err := store.Prepare(); err != nil {
		return err
	}
	entries, err := os.ReadDir(store.staging)
	if err != nil {
		return fmt.Errorf("read core staging directory: %w", err)
	}
	for _, entry := range entries {
		path := filepath.Join(store.staging, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("core staging contains a symbolic link")
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("clean interrupted core staging: %w", err)
		}
	}
	if err := syncDirectory(store.staging); err != nil {
		return err
	}
	_, err = store.Current()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (store *FileStore) Current() (Manifest, error) {
	binaryPath := filepath.Join(store.current, "mihomo")
	manifestPath := filepath.Join(store.current, "manifest.json")
	if err := store.validateOwner(store.current); err != nil {
		return Manifest{}, err
	}
	if err := validateRegularFile(binaryPath, true, maxBinarySize); err != nil {
		return Manifest{}, err
	}
	if err := store.validateOwner(binaryPath); err != nil {
		return Manifest{}, err
	}
	if err := validateRegularFile(manifestPath, false, 1<<20); err != nil {
		return Manifest{}, err
	}
	if err := store.validateOwner(manifestPath); err != nil {
		return Manifest{}, err
	}
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("read managed core manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return Manifest{}, errors.New("decode managed core manifest")
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("validate managed core manifest: %w", err)
	}
	digest, size, err := fileSHA256(binaryPath, maxBinarySize)
	if err != nil {
		return Manifest{}, err
	}
	if digest != manifest.BinarySHA256 || size != manifest.BinarySize {
		return Manifest{}, errors.New("managed core manifest does not match the binary")
	}
	return manifest, nil
}

func (store *FileStore) CreateDownloadStage() (string, string, error) {
	if err := store.Prepare(); err != nil {
		return "", "", err
	}
	directory := filepath.Join(store.staging, "update-"+uuid.NewString())
	if err := os.Mkdir(directory, 0o750); err != nil {
		return "", "", fmt.Errorf("create core update staging directory: %w", err)
	}
	if err := store.validateOwner(directory); err != nil {
		_ = os.RemoveAll(directory)
		return "", "", err
	}
	if err := syncDirectory(store.staging); err != nil {
		_ = os.RemoveAll(directory)
		return "", "", err
	}
	return directory, filepath.Join(directory, "asset.gz"), nil
}

func (store *FileStore) FinalizeDownloadedStage(
	directory string,
	identity ReleaseIdentity,
	compressedSHA, reportedVersion string,
	installedAt time.Time,
) (Manifest, error) {
	if !pathWithin(store.staging, directory) {
		return Manifest{}, errors.New("core staging directory escaped its managed root")
	}
	archivePath := filepath.Join(directory, "asset.gz")
	if err := validateRegularFile(archivePath, false, maxAssetSize); err != nil {
		return Manifest{}, err
	}
	if err := store.validateOwner(archivePath); err != nil {
		return Manifest{}, err
	}
	if compressedSHA != identity.AssetDigestSHA256 {
		return Manifest{}, errors.New("downloaded core digest does not match release identity")
	}
	archive, err := os.Open(archivePath)
	if err != nil {
		return Manifest{}, errors.New("open downloaded core archive")
	}
	reader, err := gzip.NewReader(archive)
	if err != nil {
		_ = archive.Close()
		return Manifest{}, errors.New("open downloaded core gzip payload")
	}
	binaryPath := filepath.Join(directory, "mihomo")
	binary, err := os.OpenFile(
		binaryPath,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o750,
	)
	if err != nil {
		_ = reader.Close()
		_ = archive.Close()
		return Manifest{}, errors.New("create staged core binary")
	}
	hasher := sha256.New()
	limited := &io.LimitedReader{R: reader, N: maxBinarySize + 1}
	size, copyErr := io.Copy(io.MultiWriter(binary, hasher), limited)
	closeErr := binary.Close()
	gzipErr := reader.Close()
	archiveErr := archive.Close()
	if copyErr != nil || closeErr != nil || gzipErr != nil || archiveErr != nil {
		return Manifest{}, errors.New("extract downloaded core gzip payload")
	}
	if size <= 0 || size > maxBinarySize || limited.N <= 0 {
		return Manifest{}, errors.New("extracted core binary size is invalid")
	}
	if err := os.Chmod(binaryPath, 0o750); err != nil {
		return Manifest{}, errors.New("set staged core binary permissions")
	}
	if err := syncFile(binaryPath); err != nil {
		return Manifest{}, err
	}
	binarySHA := hex.EncodeToString(hasher.Sum(nil))
	identity.BinaryReportedVersion = reportedVersion
	manifest := Manifest{
		SchemaVersion:         1,
		Source:                "downloaded",
		VerifiedSource:        true,
		Identity:              identity,
		CompressedSHA256:      compressedSHA,
		BinarySHA256:          binarySHA,
		BinarySize:            size,
		BinaryReportedVersion: reportedVersion,
		InstalledAt:           installedAt.UTC(),
	}
	if err := writeManifest(filepath.Join(directory, "manifest.json"), manifest); err != nil {
		return Manifest{}, err
	}
	if err := os.Remove(archivePath); err != nil {
		return Manifest{}, errors.New("remove verified core archive")
	}
	if err := syncDirectory(directory); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (store *FileStore) SetStagedVersion(
	directory string,
	manifest Manifest,
	reportedVersion string,
) (Manifest, error) {
	if !pathWithin(store.staging, directory) {
		return Manifest{}, errors.New("core staging directory escaped its managed root")
	}
	if strings.TrimSpace(reportedVersion) == "" {
		return Manifest{}, errors.New("staged core version is empty")
	}
	manifest.BinaryReportedVersion = reportedVersion
	manifest.Identity.BinaryReportedVersion = reportedVersion
	path := filepath.Join(directory, "manifest.json")
	temporary := path + ".tmp-" + uuid.NewString()
	if err := writeManifest(temporary, manifest); err != nil {
		return Manifest{}, err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return Manifest{}, errors.New("replace staged core manifest")
	}
	if err := syncDirectory(directory); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (store *FileStore) StageAdopted(
	sourcePath, reportedVersion string,
	now time.Time,
) (string, Manifest, error) {
	if err := validateRegularFile(sourcePath, true, maxBinarySize); err != nil {
		return "", Manifest{}, err
	}
	directory, _, err := store.CreateDownloadStage()
	if err != nil {
		return "", Manifest{}, err
	}
	destination := filepath.Join(directory, "mihomo")
	if err := copySecureFile(sourcePath, destination, 0o750, maxBinarySize); err != nil {
		_ = os.RemoveAll(directory)
		return "", Manifest{}, err
	}
	digest, size, err := fileSHA256(destination, maxBinarySize)
	if err != nil {
		_ = os.RemoveAll(directory)
		return "", Manifest{}, err
	}
	manifest := Manifest{
		SchemaVersion:         1,
		Source:                "adopted",
		VerifiedSource:        false,
		BinarySHA256:          digest,
		BinarySize:            size,
		BinaryReportedVersion: reportedVersion,
		InstalledAt:           now.UTC(),
	}
	if err := writeManifest(filepath.Join(directory, "manifest.json"), manifest); err != nil {
		_ = os.RemoveAll(directory)
		return "", Manifest{}, err
	}
	if err := syncDirectory(directory); err != nil {
		_ = os.RemoveAll(directory)
		return "", Manifest{}, err
	}
	return directory, manifest, nil
}

func (store *FileStore) StageBootstrap(
	binaryPath, manifestPath string,
) (string, Manifest, error) {
	if err := validateRegularFile(binaryPath, true, maxBinarySize); err != nil {
		return "", Manifest{}, err
	}
	if err := validateRegularFile(manifestPath, false, 1<<20); err != nil {
		return "", Manifest{}, err
	}
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", Manifest{}, errors.New("read bootstrap core manifest")
	}
	var manifest Manifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return "", Manifest{}, errors.New("decode bootstrap core manifest")
	}
	manifest.Source = "bootstrap"
	if err := manifest.Validate(); err != nil {
		return "", Manifest{}, err
	}
	digest, size, err := fileSHA256(binaryPath, maxBinarySize)
	if err != nil {
		return "", Manifest{}, err
	}
	if digest != manifest.BinarySHA256 || size != manifest.BinarySize {
		return "", Manifest{}, errors.New("bootstrap manifest does not match core binary")
	}
	directory, _, err := store.CreateDownloadStage()
	if err != nil {
		return "", Manifest{}, err
	}
	if err := copySecureFile(
		binaryPath,
		filepath.Join(directory, "mihomo"),
		0o750,
		maxBinarySize,
	); err != nil {
		_ = os.RemoveAll(directory)
		return "", Manifest{}, err
	}
	if err := writeManifest(filepath.Join(directory, "manifest.json"), manifest); err != nil {
		_ = os.RemoveAll(directory)
		return "", Manifest{}, err
	}
	return directory, manifest, nil
}

func (store *FileStore) Activate(stagedDirectory string) (Activation, error) {
	if !pathWithin(store.staging, stagedDirectory) {
		return Activation{}, errors.New("core activation source escaped staging")
	}
	if _, err := store.validateStagedDirectory(stagedDirectory); err != nil {
		return Activation{}, err
	}
	backupPath := ""
	currentInfo, err := os.Lstat(store.current)
	if err == nil {
		if !currentInfo.IsDir() || currentInfo.Mode()&os.ModeSymlink != 0 {
			return Activation{}, errors.New(
				"managed current core path has an unsafe type",
			)
		}
		if _, err := store.Current(); err != nil {
			return Activation{}, fmt.Errorf(
				"validate current managed core before activation: %w",
				err,
			)
		}
		backupPath = filepath.Join(
			store.backups,
			time.Now().UTC().Format("20060102T150405.000000000Z")+"-"+uuid.NewString(),
		)
		if err := os.Rename(store.current, backupPath); err != nil {
			return Activation{}, fmt.Errorf("move current core to backup: %w", err)
		}
		if err := syncDirectory(store.root); err != nil {
			_ = os.Rename(backupPath, store.current)
			return Activation{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Activation{}, fmt.Errorf("inspect current core directory: %w", err)
	}
	if err := os.Rename(stagedDirectory, store.current); err != nil {
		if backupPath != "" {
			_ = os.Rename(backupPath, store.current)
		}
		return Activation{}, fmt.Errorf("activate staged core: %w", err)
	}
	if err := syncDirectory(store.root); err != nil {
		return Activation{backupPath: backupPath}, err
	}
	if err := store.pruneBackups(2); err != nil {
		return Activation{backupPath: backupPath}, err
	}
	return Activation{backupPath: backupPath}, nil
}

func (store *FileStore) Restore(activation Activation) error {
	if activation.backupPath == "" ||
		!pathWithin(store.backups, activation.backupPath) {
		return errors.New("no verified previous core backup is available")
	}
	if _, err := store.validateStagedDirectory(activation.backupPath); err != nil {
		return fmt.Errorf("validate previous core backup: %w", err)
	}
	failedPath := filepath.Join(store.staging, "failed-"+uuid.NewString())
	if err := os.Rename(store.current, failedPath); err != nil {
		return fmt.Errorf("move failed core out of current path: %w", err)
	}
	if err := os.Rename(activation.backupPath, store.current); err != nil {
		_ = os.Rename(failedPath, store.current)
		return fmt.Errorf("restore previous core backup: %w", err)
	}
	if err := syncDirectory(store.root); err != nil {
		return err
	}
	if err := os.RemoveAll(failedPath); err != nil {
		return fmt.Errorf("remove failed core staging: %w", err)
	}
	return syncDirectory(store.staging)
}

func (store *FileStore) RevertActivation(activation Activation) error {
	if activation.backupPath != "" {
		return store.Restore(activation)
	}
	if err := os.RemoveAll(store.current); err != nil {
		return fmt.Errorf("remove uncommitted managed core activation: %w", err)
	}
	return syncDirectory(store.root)
}

func (store *FileStore) LatestBackup() (string, Manifest, error) {
	entries, err := os.ReadDir(store.backups)
	if err != nil {
		return "", Manifest{}, fmt.Errorf("read core backups: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		path := filepath.Join(store.backups, name)
		manifest, validateErr := store.validateStagedDirectory(path)
		if validateErr == nil {
			return path, manifest, nil
		}
	}
	return "", Manifest{}, errors.New("no valid managed core backup is available")
}

func (store *FileStore) StageBackup(backupPath string) (string, error) {
	if !pathWithin(store.backups, backupPath) {
		return "", errors.New("core backup path escaped managed backups")
	}
	if _, err := store.validateStagedDirectory(backupPath); err != nil {
		return "", fmt.Errorf("validate core backup: %w", err)
	}
	destination := filepath.Join(store.staging, "rollback-"+uuid.NewString())
	if err := copyDirectory(backupPath, destination); err != nil {
		return "", err
	}
	if _, err := store.validateStagedDirectory(destination); err != nil {
		_ = os.RemoveAll(destination)
		return "", err
	}
	return destination, nil
}

func (store *FileStore) RemoveStage(directory string) {
	if pathWithin(store.staging, directory) {
		_ = os.RemoveAll(directory)
		_ = syncDirectory(store.staging)
	}
}

func (store *FileStore) BinaryPath() string {
	return filepath.Join(store.current, "mihomo")
}

func (store *FileStore) ManifestPath() string {
	return filepath.Join(store.current, "manifest.json")
}

func (store *FileStore) pruneBackups(keep int) error {
	entries, err := os.ReadDir(store.backups)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) <= keep {
		return syncDirectory(store.backups)
	}
	for _, name := range names[keep:] {
		if err := os.RemoveAll(filepath.Join(store.backups, name)); err != nil {
			return fmt.Errorf("prune old core backup: %w", err)
		}
	}
	return syncDirectory(store.backups)
}

func (store *FileStore) validateStagedDirectory(directory string) (Manifest, error) {
	binaryPath := filepath.Join(directory, "mihomo")
	manifestPath := filepath.Join(directory, "manifest.json")
	if err := store.validateOwner(directory); err != nil {
		return Manifest{}, err
	}
	if err := validateRegularFile(binaryPath, true, maxBinarySize); err != nil {
		return Manifest{}, err
	}
	if err := store.validateOwner(binaryPath); err != nil {
		return Manifest{}, err
	}
	if err := validateRegularFile(manifestPath, false, 1<<20); err != nil {
		return Manifest{}, err
	}
	if err := store.validateOwner(manifestPath); err != nil {
		return Manifest{}, err
	}
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return Manifest{}, errors.New("decode staged core manifest")
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	digest, size, err := fileSHA256(binaryPath, maxBinarySize)
	if err != nil {
		return Manifest{}, err
	}
	if digest != manifest.BinarySHA256 || size != manifest.BinarySize {
		return Manifest{}, errors.New("staged core manifest does not match binary")
	}
	return manifest, nil
}

func (store *FileStore) validateOwner(path string) error {
	if store.expectedOwner == nil {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !ownedByExpectedUser(info, *store.expectedOwner) {
		return errors.New("managed core path has an unexpected owner")
	}
	return nil
}

func writeManifest(path string, manifest Manifest) error {
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return errors.New("encode managed core manifest")
	}
	content = append(content, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return errors.New("create managed core manifest")
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return errors.New("write managed core manifest")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return errors.New("sync managed core manifest")
	}
	if err := file.Close(); err != nil {
		return errors.New("close managed core manifest")
	}
	return nil
}

func ensureSecureDirectory(path string, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, mode); err != nil {
			return fmt.Errorf("create managed core directory: %w", err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect managed core directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		unsafeCorePermissions(info.Mode()) {
		return errors.New("managed core directory has unsafe type or permissions")
	}
	return nil
}

func validateRegularFile(path string, executable bool, limit int64) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed core file is not a regular file")
	}
	if info.Size() <= 0 || info.Size() > limit {
		return errors.New("managed core file size is invalid")
	}
	if unsafeCorePermissions(info.Mode()) {
		return errors.New("managed core file is writable by group or others")
	}
	if executable && !executableCoreMode(info.Mode()) {
		return errors.New("managed core binary is not executable")
	}
	return nil
}

func fileSHA256(path string, limit int64) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	reader := &io.LimitedReader{R: file, N: limit + 1}
	size, err := io.Copy(hasher, reader)
	if err != nil || size <= 0 || size > limit || reader.N <= 0 {
		return "", 0, errors.New("hash managed core file")
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

func copySecureFile(source, destination string, mode os.FileMode, limit int64) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	reader := &io.LimitedReader{R: input, N: limit + 1}
	written, copyErr := io.Copy(output, reader)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil ||
		written <= 0 || written > limit || reader.N <= 0 {
		return errors.New("copy managed core file")
	}
	return nil
}

func copyDirectory(source, destination string) error {
	if err := os.Mkdir(destination, 0o750); err != nil {
		return err
	}
	if err := copySecureFile(
		filepath.Join(source, "mihomo"),
		filepath.Join(destination, "mihomo"),
		0o750,
		maxBinarySize,
	); err != nil {
		return err
	}
	content, err := os.ReadFile(filepath.Join(source, "manifest.json"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(destination, "manifest.json"), content, 0o640); err != nil {
		return err
	}
	if err := syncFile(filepath.Join(destination, "manifest.json")); err != nil {
		return err
	}
	return syncDirectory(destination)
}

func syncFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return file.Sync()
}

func syncDirectory(path string) error {
	return syncCoreDirectoryPlatform(path)
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil &&
		relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
