//go:build windows

package operation

func tryPlatformFileLock(string) (func(), bool, error) {
	return nil, false, nil
}
