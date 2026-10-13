package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/empowill/djinn/internal/logx"
)

// TestConsoleKeepsThePreviousRun: what a background djinn up wrote on its output before it died, a panic, is still
// there after the start that follows: appended to below logx.MaxSize, in console.log.1 past it (W525).
func TestConsoleKeepsThePreviousRun(t *testing.T) {
	home := t.TempDir()
	path, err := openConsole(home)
	if err != nil {
		t.Fatal(err)
	}
	died := "panic: no worker for W7\n\ngoroutine 12 [running]:\nmain.loop()\n"
	if err := os.WriteFile(path, []byte(died), 0o600); err != nil {
		t.Fatal(err)
	}
	if again, err := openConsole(home); err != nil || again != path {
		t.Fatalf("openConsole = %q, %v", again, err)
	}
	if b, _ := os.ReadFile(path); string(b) != died {
		t.Errorf("after a restart, %s:\n%s", LogFile, b)
	}

	// Past MaxSize, the start rolls it over: the panic, written last, is in the older file.
	big := append(bytes.Repeat([]byte("gtk: a line\n"), logx.MaxSize/12+1), died...)
	if err := os.WriteFile(path, big, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openConsole(home); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path + ".1"); !strings.HasSuffix(string(b), died) {
		t.Errorf("%s.1 lacks the panic: %d bytes", filepath.Base(path), len(b))
	}
	if info, err := os.Stat(path); err == nil && info.Size() != 0 {
		t.Errorf("%s holds %d bytes after the roll over", path, info.Size())
	}
}
