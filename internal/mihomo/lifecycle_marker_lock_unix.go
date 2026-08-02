//go:build !windows

package mihomo

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openRuntimeLockFile(path string, create bool) (*os.File, error) {
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	if create {
		flags |= unix.O_CREAT
	}
	fileDescriptor, err := unix.Open(path, flags, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fileDescriptor), path)
	if file == nil {
		_ = unix.Close(fileDescriptor)
		return nil, errors.New("create Mihomo lifecycle marker file")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fileDescriptor, &stat); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect Mihomo lifecycle marker: %w", err)
	}
	ownerOK := uint32(os.Geteuid()) == stat.Uid || stat.Uid == 0 || os.Geteuid() == 0
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || !ownerOK {
		_ = file.Close()
		return nil, errors.New("Mihomo lifecycle marker has unsafe type or owner")
	}
	if err := unix.Fchmod(fileDescriptor, 0o600); err != nil {
		_ = file.Close()
		return nil, errors.New("set Mihomo lifecycle marker permissions")
	}
	return file, nil
}

func tryLockRuntimeLockFile(file *os.File) (bool, error) {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func trySharedRuntimeLockFile(file *os.File) (bool, error) {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func unlockRuntimeLockFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
