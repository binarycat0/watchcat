// Package web holds the UI. go:embed bakes the files into the binary at build
// time, so the daemon serves them without anything on disk.
//
// The React app in ui/ is built by `make ui` (Node) into dist/app. A binary
// built without it serves fallback/index.html, which explains how to build
// the UI, so plain `go build` and `go test` never need Node.
package web

import (
	"embed"
	"io/fs"
)

// "all:" includes dist/.keep, which keeps the pattern valid before the UI
// has been built.
//
//go:embed all:dist fallback
var files embed.FS

// FS returns the built UI if present, otherwise the fallback page.
func FS() fs.FS {
	if _, err := fs.Stat(files, "dist/app/index.html"); err == nil {
		return mustSub("dist/app")
	}
	return mustSub("fallback")
}

// Built reports whether the React UI was embedded.
func Built() bool {
	_, err := fs.Stat(files, "dist/app/index.html")
	return err == nil
}

func mustSub(dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic(err) // only fails if the embed directive is wrong
	}
	return sub
}
