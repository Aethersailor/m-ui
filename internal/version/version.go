package version

import (
	"fmt"
	"strings"
)

const commitDisplayLength = 8

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
	dirty   = "true"
)

// Info contains build metadata injected by the release build.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Dirty   bool   `json:"dirty"`
}

// Current returns immutable metadata for the running binary.
func Current() Info {
	return Info{
		Version: version,
		Commit:  displayCommit(commit),
		Date:    date,
		Dirty:   dirty == "true",
	}
}

func displayCommit(value string) string {
	if len(value) <= commitDisplayLength {
		return value
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return value
		}
	}
	return value[:commitDisplayLength]
}

func (i Info) String() string {
	return fmt.Sprintf(
		"m-ui %s (commit=%s date=%s dirty=%t)",
		i.Version,
		i.Commit,
		i.Date,
		i.Dirty,
	)
}
