//go:build windows

package mihomo

import "context"

func openRCProcessActive(
	ctx context.Context,
	binaryPath string,
	configPath string,
) (bool, error) {
	return managedProcessActive(ctx, binaryPath, configPath)
}
