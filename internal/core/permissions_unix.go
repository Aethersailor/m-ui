//go:build !windows

package core

import (
	"os"
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
