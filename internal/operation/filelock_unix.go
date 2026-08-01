//go:build !windows

package operation

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func tryPlatformFileLock(path string) (func(), bool, error) {
	if path == "" {
		return nil, false, nil
	}
	fileDescriptor, err := unix.Open(
		path,
		unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o660,
	)
	if err != nil {
		return nil, false, fmt.Errorf("open runtime operation lock: %w", err)
	}
	closeFile := func() {
		_ = unix.Close(fileDescriptor)
	}
	if err := unix.Fchmod(fileDescriptor, 0o660); err != nil {
		closeFile()
		return nil, false, errors.New("set runtime operation lock permissions")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fileDescriptor, &stat); err != nil {
		closeFile()
		return nil, false, errors.New("inspect runtime operation lock")
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		(int(stat.Uid) != os.Geteuid() && int(stat.Uid) != 0) ||
		stat.Mode&0o007 != 0 {
		closeFile()
		return nil, false, errors.New(
			"runtime operation lock has unsafe type, owner, or permissions",
		)
	}
	if err := unix.Flock(fileDescriptor, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		closeFile()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, true, nil
		}
		return nil, false, errors.New("lock runtime operation file")
	}
	return func() {
		_ = unix.Flock(fileDescriptor, unix.LOCK_UN)
		closeFile()
	}, false, nil
}
