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
)

// managedProcessActive checks the kernel's process table so a short-lived CLI
// control plane can report the Mihomo process supervised by the long-running
// m-ui instance. Core activation moves current/ to backups/ before the process
// is restarted, so the current executable path alone is not a sufficient
// identity. The scan also accepts the managed core root and verifies the exact
// config argument used by m-ui; this catches a process whose executable was
// renamed or unlinked after an Activate-before-Restart crash.
func managedProcessActive(
	ctx context.Context,
	binaryPath, configPath string,
) (bool, error) {
	return managedProcessActiveAt(ctx, "/proc", binaryPath, configPath)
}

func managedProcessActiveAt(
	ctx context.Context,
	procRoot, binaryPath, configPath string,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	wanted := ""
	resolvedWanted, err := filepath.EvalSymlinks(binaryPath)
	if err == nil {
		wanted, err = filepath.Abs(resolvedWanted)
		if err != nil {
			return false, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	root, err := managedCoreRoot(binaryPath)
	if err != nil {
		return false, err
	}
	config, err := filepath.Abs(configPath)
	if err != nil {
		return false, err
	}
	entries, err := os.ReadDir(procRoot)
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
		processRoot := filepath.Join(procRoot, entry.Name())
		executable, err := os.Readlink(filepath.Join(processRoot, "exe"))
		if err != nil {
			continue
		}
		executable = strings.TrimSuffix(executable, " (deleted)")
		resolved, err := filepath.Abs(executable)
		if err != nil {
			continue
		}
		if wanted != "" && resolved == wanted {
			return true, nil
		}
		if !managedCoreExecutablePath(root, resolved) {
			continue
		}
		commandLine, readErr := os.ReadFile(filepath.Join(processRoot, "cmdline"))
		if readErr != nil {
			// A process whose executable is a m-ui-owned managed core is a
			// positive match even when /proc hides its command line. Refuse a
			// duplicate rather than treating an inspection failure as absence.
			return true, nil
		}
		if managedProcessCommandLine(bytes.Split(commandLine, []byte{0}), config) {
			return true, nil
		}
	}
	return false, nil
}

func managedCoreRoot(binaryPath string) (string, error) {
	clean, err := filepath.Abs(binaryPath)
	if err != nil {
		return "", err
	}
	if filepath.Base(filepath.Dir(clean)) == "current" {
		return filepath.Dir(filepath.Dir(clean)), nil
	}
	return filepath.Dir(clean), nil
}

func managedCoreExecutablePath(root, executable string) bool {
	relative, err := filepath.Rel(root, executable)
	if err != nil || relative == "." || filepath.IsAbs(relative) {
		return false
	}
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	if len(parts) == 2 && parts[0] == "current" && parts[1] == "mihomo" {
		return true
	}
	return len(parts) == 3 && parts[0] == "backups" &&
		parts[1] != "" && parts[2] == "mihomo"
}

func managedProcessCommandLine(args [][]byte, configPath string) bool {
	if len(args) == 0 {
		return false
	}
	for index := 1; index+1 < len(args); index++ {
		if string(args[index]) == "-f" && string(args[index+1]) == configPath {
			return true
		}
	}
	return false
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
