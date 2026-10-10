//go:build mcp

// The test build of the window: `go tool task e2e-native` builds it with the mcp tag, which also compiles in the
// MCP server of Wails. App.Run starts that server on its own; it lets a local process read the DOM, run JavaScript
// and click in the window. This build is for tests and is never shipped.

package main

import (
	"bytes"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// windowTitle is never the title of the Djinn you use: a test, a capture or a window tool finds this window, and
// only this one.
const windowTitle = "Djinn e2e"

// readyPath serves readyScript, which every page of the test build loads.
const readyPath = "/djinn-e2e/ready.js"

// readyScript tells Wails the page is up. Wails runs a window's scripts, those of the MCP server included, only once
// its runtime says so, and the interface does not load that runtime: it has no bindings.
const readyScript = `(window.chrome?.webview ?? window.webkit?.messageHandlers?.external)?.postMessage("wails:runtime:ready");
`

// windowAssets makes the pages drivable by the MCP server, and changes nothing else: each page loads readyScript, and
// may post to http://127.0.0.1:<port>, where each MCP call sends its result back, which the interface's connect-src
// 'self' forbids.
func windowAssets(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == readyPath {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = io.WriteString(w, readyScript)
			return
		}
		// A page is a GET with no extension, or .html. Everything else, the Connect streams first, passes untouched.
		if ext := path.Ext(r.URL.Path); r.Method != http.MethodGet || (ext != "" && ext != ".html") {
			h.ServeHTTP(w, r)
			return
		}
		page := &pageWriter{header: http.Header{}, code: http.StatusOK}
		h.ServeHTTP(page, r)
		body := page.body.Bytes()
		if strings.HasPrefix(page.header.Get("Content-Type"), "text/html") {
			body = bytes.Replace(body, []byte("connect-src "), []byte("connect-src http://127.0.0.1:* "), 1)
			body = bytes.Replace(body, []byte("</head>"), []byte(`<script src="`+readyPath+`"></script></head>`), 1)
			page.header.Set("Content-Length", strconv.Itoa(len(body)))
		}
		for k, v := range page.header {
			w.Header()[k] = v
		}
		w.WriteHeader(page.code)
		_, _ = w.Write(body)
	})
}

// pageWriter holds a page until it is complete.
type pageWriter struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (w *pageWriter) Header() http.Header         { return w.header }
func (w *pageWriter) WriteHeader(code int)        { w.code = code }
func (w *pageWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
