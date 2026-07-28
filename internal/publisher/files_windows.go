//go:build windows

package publisher

import (
	"errors"
	"os"
)

// Windows is a development and test platform for m-ui, not a supported
// deployment target. Production atomic replacement is implemented by the Unix
// variant used on supported Linux systems.
func replaceFile(source, destination string) error {
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(source, destination)
}

func syncDirectory(string) error {
	return nil
}
