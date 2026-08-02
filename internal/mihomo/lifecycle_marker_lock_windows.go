//go:build windows

package mihomo

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func openRuntimeLockFile(path string, create bool) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Mihomo lifecycle marker must not be a symlink")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect Mihomo lifecycle marker: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("Mihomo lifecycle marker has unsafe type")
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, errors.New("set Mihomo lifecycle marker permissions")
	}
	return file, nil
}

func tryLockRuntimeLockFile(file *os.File) (bool, error) {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&overlapped,
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return true, nil
	}
	return false, err
}

func trySharedRuntimeLockFile(file *os.File) (bool, error) {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&overlapped,
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return true, nil
	}
	return false, err
}

func unlockRuntimeLockFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}
