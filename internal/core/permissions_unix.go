//go:build !windows

package core

import (
	"os"
	"path/filepath"
	"syscall"
)

func currentOwnerID() *int {
	owner := os.Geteuid()
	return &owner
}

func ownedByExpectedUser(info os.FileInfo, expected int) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == expected
}

func unsafeCorePermissions(mode os.FileMode) bool {
	return mode.Perm()&0o022 != 0
}

func executableCoreMode(mode os.FileMode) bool {
	return mode.Perm()&0o111 != 0
}

// setCoreGroupFromParent keeps files created by the non-root m-ui service in
// the setgid directory's service group. This is required when the native
// Mihomo service runs under its separate account.
func setCoreGroupFromParent(path string) error {
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return err
	}
	stat, ok := parent.Sys().(*syscall.Stat_t)
	if !ok {
		return syscall.ENOTSUP
	}
	return os.Chown(path, -1, int(stat.Gid))
}

func syncCoreDirectoryPlatform(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
