package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/empowill/djinn/internal/logx"
)

// TestOpenConsole: what a background djinn up wrote on its output before it died, a panic, is still there after the
// start that follows: appended to below logx.MaxSize, in console.log.1 past it (W525).
func TestOpenConsole(t *testing.T) {
	t.Parallel()
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

// TestTail: what djinn up wrote on its console since a start, its last 2000 bytes, says why it stopped at once.
func TestTail(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "console.log")
	long := strings.Repeat("x", 2500) + "panic: the end"
	if err := os.WriteFile(path, []byte("an older run\n"+long+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	from := int64(len("an older run\n"))
	for _, c := range []struct {
		name, path string
		from       int64
		want       string
	}{
		{"since the start, cut to its end", path, from, long[len(long)-2000:]},
		{"nothing since the start", path, from + int64(len(long)) + 1, "no output"},
		{"a shorter file", path, 1 << 20, "see " + path},
		{"no file", path + ".gone", 0, "see " + path + ".gone"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := tail(c.path, c.from); got != c.want {
				t.Errorf("tail(%s, %d) = %.80q, want %.80q", c.path, c.from, got, c.want)
			}
		})
	}
}
