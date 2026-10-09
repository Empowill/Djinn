// Package djinn holds what must sit at the root of the module: the built interface, embedded at compile time.
package djinn

import (
	"embed"
	"io/fs"

	"github.com/empowill/djinn/internal/docsite"
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

// site is the documentation site, and openAPI the document its API tab reads (go tool task gen).
//
//go:embed docs/site
var site embed.FS

//go:embed docs/openapi.json
var openAPI []byte

// Docs returns the documentation site, with the OpenAPI document as its script: what djinn up serves at /docs/.
func Docs() fs.FS {
	s, err := fs.Sub(site, "docs/site")
	if err != nil {
		panic(err) // "docs/site" is a valid path: fs.Sub cannot fail on it.
	}
	return docsite.FS(s, openAPI)
}

// Icon is the icon of the application, for the window, the dock and the tray.
//
//go:embed build/icon.png
var Icon []byte

// Icon256 is the icon at 256 px, for the window on Linux: GTK 3 silently drops a window icon that does not fit one
// X11 request, 512 px or more (gdk_x11_window_set_icon_list). Made by `go run ./tools/icons gen`.
//
//go:embed build/icon-256.png
var Icon256 []byte
