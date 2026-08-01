package mihomo

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"runtime"
)

func NewProcess(
	ctx context.Context,
	mode, binaryPath, configPath, serviceName string,
	logger *slog.Logger,
) (CoreProcess, error) {
	if mode == "" {
		mode = "auto"
	}
	if mode == "auto" {
		switch {
		case runtime.GOOS != "linux":
			mode = "systemd"
		case fileExists("/sbin/rc-service"):
			mode = "openrc"
		default:
			mode = "systemd"
		}
	}
	switch mode {
	case "systemd":
		return NewSystemdProcess(serviceName)
	case "openrc":
		return NewOpenRCProcess(serviceName)
	case "managed":
		return NewManagedProcess(ctx, binaryPath, configPath, logger)
	default:
		return nil, errors.New("unsupported Mihomo process mode")
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
