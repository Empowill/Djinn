//go:build !mcp

package main

import "net/http"

// windowTitle is the title of the native window.
const windowTitle = "Djinn"

// windowAssets serves the interface to the window as it is. The test build changes it (window_mcp.go).
func windowAssets(h http.Handler) http.Handler { return h }
