//go:build !windows

package mihomo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// managedProcessActive checks the kernel's process table so a short-lived CLI
// control plane can report the Mihomo process supervised by the long-running
// m-ui instance. The binary path is owned by m-ui, so matching its resolved
// executable path avoids relying on a mutable PID file or a shell command.
func managedProcessActive(ctx context.Context, binaryPath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	wanted, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	wanted, err = filepath.Abs(wanted)
	if err != nil {
		return false, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if !isPIDDirectory(entry.Name(), entry.IsDir()) {
			continue
		}
		executable, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
		if err != nil {
			continue
		}
		resolved, err := filepath.Abs(executable)
		if err == nil && resolved == wanted {
			return true, nil
		}
	}
	return false, nil
}

func isPIDDirectory(name string, directory bool) bool {
	if !directory || name == "" {
		return false
	}
	for _, value := range name {
		if value < '0' || value > '9' {
			return false
		}
	}
	_, err := strconv.Atoi(name)
	return err == nil
}
