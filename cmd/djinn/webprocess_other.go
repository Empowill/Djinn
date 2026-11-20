//go:build !headless && (cgo || windows) && !(linux && cgo && gtk3)

package main

import (
	"context"
	"errors"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// watchView hears nothing outside WebKitGTK 4.1 (the gtk3 tag): the GTK 4 build of Wails, macOS and Windows. On
// Linux a silent page is still caught by its silence, and its load events by Wails.
func watchView(unsafe.Pointer, viewHooks) {}

// evaluateJS gets no value back outside WebKitGTK 4.1: the probe of a silent page says so.
func evaluateJS(context.Context, *application.WebviewWindow, string) (string, error) {
	return "", errors.New("no probe outside WebKitGTK 4.1")
}
