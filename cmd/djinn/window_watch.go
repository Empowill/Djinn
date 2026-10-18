package main

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/ui"
	"github.com/empowill/djinn/internal/window"
	"github.com/empowill/djinn/locales"
)

// windowConfig is what openWindow shows, and what it tells djinn up.
type windowConfig struct {
	// url is the page the window loads, the loopback server; with assets, the window loads them from the Wails asset
	// server instead (wails://), with no port.
	url    string
	assets http.Handler
	// Each value received on raise brings the window to the front.
	raise     <-chan struct{}
	notices   *ui.Notices
	shortcuts *ui.Shortcuts
	// links receives the djinn:// links the system gives the app.
	links func(string)
	// quit names what quits Djinn from the window (Ctrl+Q, the tray) before it closes: stopContext's stop.
	quit func(source string)
	// watch follows the window's page, and rescues it when it is silent or dead: a reload, then a new view (Linux);
	// nil watches nothing.
	watch *window.Watch
}

// windowTold says outside the window what its Watch found: a line on djinn up's output, the terminal or the console
// log of who started it, and a desktop notification, in the system's language.
func windowTold(w io.Writer, notices *ui.Notices, language string) func(window.Event) {
	return func(e window.Event) {
		title, body := windowNote(language, e)
		fmt.Fprintf(w, "djinn: %s. %s\n", title, body)
		if notices == nil {
			return
		}
		if err := notices.Window(title, body); err != nil {
			logx.L().Warn("notify the window's rescue", "err", err)
		}
	}
}

// windowNote is the title and body that tell e, in language.
func windowNote(language string, e window.Event) (title, body string) {
	reasons := map[string]string{
		"crashed": "window.reason_crashed", "memory": "window.reason_memory", "terminated": "window.reason_terminated",
	}
	vars := map[string]string{"seconds": strconv.Itoa(int(e.After.Seconds())), "reason": e.Ended}
	if key, ok := reasons[e.Ended]; ok {
		vars["reason"] = locales.T(language, key, nil)
	}
	var lines []string
	if e.Ended != "" {
		title = locales.T(language, "window.ended_title", vars)
		lines = append(lines, locales.T(language, "window.ended_body", vars))
	} else {
		title = locales.T(language, "window.silent_title", vars)
		lines = append(lines, locales.T(language, "window.silent_body", vars))
	}
	if n := len(e.Hangs); n > 0 && !e.Hangs[n-1].Time.IsZero() {
		vars["time"] = e.Hangs[n-1].Time.Local().Format("15:04:05")
		lines = append(lines, locales.T(language, "window.gpu_hang", vars))
	}
	switch {
	case e.Err != nil:
		vars["err"] = e.Err.Error()
		lines = append(lines, locales.T(language, "window.rescue_failed", vars))
	case e.Did == window.Reloaded:
		lines = append(lines, locales.T(language, "window.reloaded", vars))
	case e.Did == window.Recreated && e.Software:
		lines = append(lines, locales.T(language, "window.relaunched", vars))
	case e.Did == window.Recreated:
		lines = append(lines, locales.T(language, "window.recreated", vars))
	default:
		lines = append(lines, locales.T(language, "window.restart", vars))
	}
	return title, strings.Join(lines, " ")
}
