//go:build windows

package core

import "os"

func currentOwnerID() *int {
	return nil
}

func ownedByExpectedUser(os.FileInfo, int) bool {
	return true
}

func unsafeCorePermissions(os.FileMode) bool {
	return false
}

func executableCoreMode(os.FileMode) bool {
	return true
}

func syncCoreDirectoryPlatform(string) error {
	return nil
}
