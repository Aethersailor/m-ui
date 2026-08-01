//go:build !windows

package mihomo

import (
	"os"
	"syscall"
)

func signalTerminate(process *os.Process) error {
	return process.Signal(syscall.SIGTERM)
}
