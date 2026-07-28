//go:build !webembed

package ui

import (
	"embed"
	"io/fs"
)

//go:embed fallback/*
var embedded embed.FS

func assets() fs.FS {
	sub, err := fs.Sub(embedded, "fallback")
	if err != nil {
		panic(err)
	}
	return sub
}
