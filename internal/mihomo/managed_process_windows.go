//go:build windows

package mihomo

import "context"

// Managed mode is a supported Linux deployment mode. On Windows the process
// remains observable through the in-memory supervisor used by tests and local
// development; there is no /proc equivalent to safely enumerate here.
func managedProcessActive(ctx context.Context, _ string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return false, nil
}
