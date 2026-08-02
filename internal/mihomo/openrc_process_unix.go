//go:build !windows

package mihomo

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const openRCPidPath = "/run/mihomo.pid"

// openRCProcessActive accounts for OpenRC's start_post window. During that
// hook rc-service still reports the service as inactive, and m-ui cannot read
// another user's /proc/<pid>/exe on hosts with ptrace restrictions. The
// service-owned pidfile is the second, narrowly scoped identity source.
func openRCProcessActive(
	ctx context.Context,
	binaryPath string,
	configPath string,
) (bool, error) {
	active, err := managedProcessActive(ctx, binaryPath, configPath)
	if err == nil && active {
		return true, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	content, err := os.ReadFile(openRCPidPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil || pid < 1 {
		return false, nil
	}

	processRoot := filepath.Join("/proc", strconv.Itoa(pid))
	commandLine, commandErr := os.ReadFile(filepath.Join(processRoot, "cmdline"))
	if commandErr == nil {
		if !managedProcessCommandLine(
			bytes.Split(commandLine, []byte{0}),
			configPath,
		) {
			return false, nil
		}
	} else if errors.Is(commandErr, os.ErrNotExist) {
		return false, nil
	}

	switch err := unix.Kill(pid, 0); {
	case err == nil, errors.Is(err, unix.EPERM):
		return true, nil
	case errors.Is(err, unix.ESRCH):
		return false, nil
	default:
		return false, err
	}
}
