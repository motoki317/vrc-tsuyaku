// Package web embeds the built user interface.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Files serves the frontend with index.html at its root.
var Files, _ = fs.Sub(dist, "dist")
