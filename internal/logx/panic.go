package logx

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
)

const (
	// CrashFile receives what the runtime writes when djinn up dies of a panic or a fatal error, in Dir: the runtime
	// writes it on the standard error too, which only a background djinn up keeps (Console). It is empty while djinn
	// up runs, and removed when it stops.
	CrashFile = "crash.txt"
	// CrashPrefix starts the name a start gives the CrashFile the last djinn up left: crash-<time>.txt, its time the
	// crash's.
	CrashPrefix = "crash-"
	// CrashesKept is how many crash files the log folder keeps: the oldest go first.
	CrashesKept = 5
)

// Crash, deferred first in a goroutine of djinn up that cannot go on after a panic (a loop of the harness, a reader),
// logs the panic at error, with where it happened (the loop, then key-value pairs: task, wish), its stack, and that
// djinn up stops; then it panics again: the runtime ends the process, and writes the panic to CrashFile and the
// standard error. Without a panic it does nothing.
func Crash(where string, args ...any) {
	p := recover()
	if p == nil {
		return
	}
	L().Error("panic: djinn up stops", panicArgs(where, p, args)...)
	panic(p)
}

// Panicked logs at error p, a panic recovered in where where djinn up goes on (a call to the server, a callback of
// the window), with its stack.
func Panicked(where string, p any, args ...any) {
	L().Error("panic recovered", panicArgs(where, p, args)...)
}

func panicArgs(where string, p any, args []any) []any {
	return append(append([]any{"in", where}, args...), "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
}

// crashOutput makes dir/CrashFile the runtime's crash output, after naming the one the last djinn up left by its time
// and logging it, and returns what removes it, empty, at the stop.
func crashOutput(dir string) (stop func(), err error) {
	path := filepath.Join(dir, CrashFile)
	keepCrash(dir, path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	defer f.Close() // The runtime holds its own copy.
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		return nil, err
	}
	return func() {
		debug.SetCrashOutput(nil, debug.CrashOptions{}) //nolint:errcheck // Nil never fails.
		if info, err := os.Stat(path); err == nil && info.Size() == 0 {
			os.Remove(path)
		}
	}, nil
}

// keepCrash names the crash file at path the last djinn up left by its time, and logs at error what it died of; an
// empty one, from a djinn up killed or ended without a stop, goes. It keeps the latest CrashesKept.
func keepCrash(dir, path string) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return
	case err != nil:
		L().Warn("read the last crash", "file", path, "err", err)
		return
	case info.Size() == 0:
		os.Remove(path)
		return
	}
	kept := filepath.Join(dir, CrashPrefix+info.ModTime().UTC().Format("20060102T150405.000000000Z")+".txt")
	if err := os.Rename(path, kept); err != nil {
		L().Warn("keep the last crash", "file", path, "err", err)
		return
	}
	L().Error("the last djinn up crashed", "file", kept, "panic", crashLine(kept))
	crashes := Crashes(dir)
	for len(crashes) > CrashesKept {
		os.Remove(crashes[0])
		crashes = crashes[1:]
	}
}

// crashLine is the first line of the crash file at path that says what the process died of.
func crashLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	first := ""
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "panic: ") || strings.HasPrefix(line, "fatal error: ") {
			return Cut(line, 300)
		}
		if first == "" {
			first = line
		}
	}
	return Cut(first, 300)
}

// Crashes lists the crash files of dir, oldest first.
func Crashes(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), CrashPrefix) && strings.HasSuffix(e.Name(), ".txt") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	slices.Sort(out) // Their names sort by time.
	return out
}
