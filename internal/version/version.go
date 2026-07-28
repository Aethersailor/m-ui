package version

import "fmt"

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
		Commit:  commit,
		Date:    date,
		Dirty:   dirty == "true",
	}
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
