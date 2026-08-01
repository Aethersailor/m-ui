//go:build !windows

package core

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

func currentOwnerID() *int {
	owner := os.Geteuid()
	return &owner
}

func ownedByExpectedUser(info os.FileInfo, expected int) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	if int(stat.Uid) == expected {
		return true
	}
	// The long-running service owns managed files as m-ui, while root-run
	// administrative commands must still be able to audit that state. Keep
	// the fallback narrow: only root may accept the dedicated service UID.
	if expected != 0 || os.Geteuid() != 0 {
		return false
	}
	serviceUser, err := user.Lookup("m-ui")
	if err != nil {
		return false
	}
	serviceUID, err := strconv.Atoi(serviceUser.Uid)
	return err == nil && serviceUID != 0 && int(stat.Uid) == serviceUID
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
