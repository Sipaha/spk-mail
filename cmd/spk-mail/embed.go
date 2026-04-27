package main

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// frontendFS returns dist/ as an io/fs.FS rooted at dist/.
func frontendFS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
