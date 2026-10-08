// Package djinn holds what must sit at the root of the module: the built interface, embedded at compile time.
package djinn

import (
	"embed"
	"io/fs"
)

// dist is the Vite build of the interface. Run `npm run build` before compiling: embedding fails without it.
//
//go:embed all:dist
var dist embed.FS

// UI returns the built interface, rooted at the content of dist/.
func UI() fs.FS {
	ui, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // "dist" is a valid path: fs.Sub cannot fail on it.
	}
	return ui
}

// Icon is the icon of the application, for the window, the dock and the tray.
//
//go:embed build/icon.png
var Icon []byte
