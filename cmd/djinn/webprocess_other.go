//go:build !headless && (cgo || windows) && !(linux && cgo && gtk3)

package main

import "unsafe"

// watchView hears nothing outside WebKitGTK 4.1 (the gtk3 tag): the GTK 4 build of Wails, macOS and Windows. On
// Linux a silent page is still caught by its silence, and its load events by Wails.
func watchView(unsafe.Pointer, viewHooks) {}
