package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

const MasterKeySize = 32

type MasterKey [MasterKeySize]byte

func LoadMasterKey(path string) (MasterKey, error) {
	var key MasterKey
	info, err := os.Lstat(path)
	if err != nil {
		return key, fmt.Errorf("stat master key: %w", err)
	}
	if !info.Mode().IsRegular() {
		return key, errors.New("master key is not a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return key, fmt.Errorf(
			"master key permissions %04o are too broad; require 0600",
			info.Mode().Perm(),
		)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return key, fmt.Errorf("read master key: %w", err)
	}
	if len(content) != MasterKeySize {
		return key, fmt.Errorf(
			"master key length is %d bytes; require %d",
			len(content),
			MasterKeySize,
		)
	}
	copy(key[:], content)
	return key, nil
}

// GenerateMasterKey creates a new key with exclusive file creation. It never
// replaces an existing key.
func GenerateMasterKey(path string) (MasterKey, error) {
	return generateMasterKey(path, rand.Reader)
}

func generateMasterKey(path string, random io.Reader) (MasterKey, error) {
	var key MasterKey
	if _, err := io.ReadFull(random, key[:]); err != nil {
		return key, fmt.Errorf("generate master key: %w", err)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return MasterKey{}, fmt.Errorf("create master key directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return MasterKey{}, fmt.Errorf("create master key: %w", err)
	}
	removeOnFailure := true
	defer func() {
		_ = file.Close()
		if removeOnFailure {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(key[:]); err != nil {
		return MasterKey{}, fmt.Errorf("write master key: %w", err)
	}
	if err := file.Sync(); err != nil {
		return MasterKey{}, fmt.Errorf("sync master key: %w", err)
	}
	if err := file.Close(); err != nil {
		return MasterKey{}, fmt.Errorf("close master key: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return MasterKey{}, fmt.Errorf("set master key permissions: %w", err)
	}
	if runtime.GOOS != "windows" {
		directoryHandle, err := os.Open(directory)
		if err != nil {
			return MasterKey{}, fmt.Errorf("open master key directory: %w", err)
		}
		if err := directoryHandle.Sync(); err != nil {
			_ = directoryHandle.Close()
			return MasterKey{}, fmt.Errorf("sync master key directory: %w", err)
		}
		if err := directoryHandle.Close(); err != nil {
			return MasterKey{}, fmt.Errorf("close master key directory: %w", err)
		}
	}
	removeOnFailure = false
	return key, nil
}
