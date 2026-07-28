package publisher

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxManagedFileSize = 16 * 1024 * 1024

func prepareDirectories(configPath, revisionDirectory string) error {
	configDirectory := filepath.Dir(configPath)
	if err := os.MkdirAll(configDirectory, 0o750); err != nil {
		return errors.New("create Mihomo configuration directory")
	}
	if err := os.MkdirAll(revisionDirectory, 0o700); err != nil {
		return errors.New("create revision directory")
	}
	for _, directory := range []string{configDirectory, revisionDirectory} {
		info, err := os.Lstat(directory)
		if err != nil {
			return errors.New("inspect publication directory")
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("publication directory must not be a symbolic link")
		}
	}
	return nil
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return errors.New("inspect managed configuration path")
	case info.Mode()&os.ModeSymlink != 0:
		return errors.New("managed configuration path must not be a symbolic link")
	default:
		return nil
	}
}

func writeExclusiveSynced(path string, content []byte) error {
	if len(content) > maxManagedFileSize {
		return errors.New("managed file exceeds the size limit")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("create managed publication file")
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(content); err != nil {
		return errors.New("write managed publication file")
	}
	if err := file.Sync(); err != nil {
		return errors.New("synchronize managed publication file")
	}
	if err := file.Close(); err != nil {
		return errors.New("close managed publication file")
	}
	remove = false
	return nil
}

func readManagedFile(path string) ([]byte, bool, error) {
	if err := rejectSymlink(path); err != nil {
		return nil, false, err
	}
	file, err := os.Open(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, false, errors.New("open managed file")
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxManagedFileSize+1))
	if err != nil {
		return nil, false, errors.New("read managed file")
	}
	if len(content) > maxManagedFileSize {
		return nil, false, errors.New("managed file exceeds the size limit")
	}
	return content, true, nil
}

func replaceAndSync(candidatePath, configPath string) error {
	if err := replaceFile(candidatePath, configPath); err != nil {
		return errors.New("atomically replace Mihomo configuration")
	}
	if err := syncDirectory(filepath.Dir(configPath)); err != nil {
		return errors.New("synchronize Mihomo configuration directory")
	}
	return nil
}

func removeAndSync(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("remove managed configuration")
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("synchronize directory after removal: %w", err)
	}
	return nil
}

func pathWithin(directory, path string) bool {
	relative, err := filepath.Rel(directory, path)
	if err != nil {
		return false
	}
	return relative != ".." &&
		relative != "." &&
		!filepath.IsAbs(relative) &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
