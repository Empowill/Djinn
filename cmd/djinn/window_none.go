//go:build headless || (!cgo && !windows)

package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/empowill/djinn/internal/ui"
)

// hasWindow is false in a build without a native window: a headless build (-tags headless), made for tests and
// CI, or a build without CGO on macOS and Linux, which installs with no system library and no sudo. djinn up
// then serves the browser.
const hasWindow = false

// openWindow is unavailable in a build without a native window.
func openWindow(context.Context, string, http.Handler, <-chan struct{}, *ui.Notices, *ui.Shortcuts) error {
	return errors.New("this build has no native window: use --browser, or rebuild with CGO and the system libraries of the window (see the README)")
}

// chooseFolder is unavailable in a build without a native window: the browser has no folder dialog.
func chooseFolder(string, string) (string, error) {
	return "", errors.New("this build has no native window, so no folder dialog")
}
