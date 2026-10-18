//go:build linux && cgo && gtk3 && !headless

package main

// A file with an //export holds no C definition: its preamble goes into two C files (cgo's rule).

/*
#include <stdint.h>
*/
import "C"

import (
	"runtime/debug"

	"github.com/empowill/djinn/internal/logx"
)

// The functions below run on GTK's main thread, called from C: a panic cannot unwind through C and would end Djinn,
// so it is logged and dropped.
func fromC(what string) {
	if p := recover(); p != nil {
		logx.L().Error("panic recovered", "in", what, "panic", p, "stack", string(debug.Stack()))
	}
}

//export djinnWebProcessEnded
func djinnWebProcessEnded(id C.uintptr_t, reason C.int) {
	defer fromC("window: web process ended")
	webProcessEnded(uintptr(id), int(reason))
}

//export djinnLoadFailed
func djinnLoadFailed(id C.uintptr_t, uri, message *C.char) {
	defer fromC("window: load failed")
	text := "unknown error"
	if message != nil {
		text = C.GoString(message)
	}
	loadFailed(uintptr(id), C.GoString(uri), text)
}
