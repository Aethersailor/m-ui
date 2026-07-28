//go:build webembed

package ui

import (
	"embed"
	"io/fs"
)

//go:embed dist/*
var embedded embed.FS

func assets() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
