//go:build webembed

package ui

import (
	"embed"
	"io/fs"
)

// Include Vite chunks whose names begin with an underscore or a dot.
//go:embed all:dist
var embedded embed.FS

func assets() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
