//go:build windows

package mihomo

import "os"

func signalTerminate(process *os.Process) error {
	return process.Signal(os.Interrupt)
}
