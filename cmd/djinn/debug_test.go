package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/server"
)

// TestRunDebugDump covers runDebugDump, djinn debug dump: its arguments, and the dump the server answers with, whose
// path it prints, naming the log to send with it (W511).
func TestRunDebugDump(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DJINN_HOME", home)
	srv := httptest.NewServer(server.Handler(nil, nil, map[string]http.Handler{server.DumpPath: server.Dump(logx.Folder(home))}))
	defer srv.Close()
	for _, c := range []struct {
		name string
		args []string
		code int
		errs string
	}{
		{name: "help", args: []string{"--help"}, errs: "Usage: djinn debug dump"},
		{name: "an unknown flag", args: []string{"--bogus"}, code: 2, errs: "flag provided but not defined: -bogus"},
		{name: "an argument", args: []string{"now"}, code: 2, errs: `unexpected argument "now"`},
		{name: "the server answers", args: []string{"--addr", srv.URL}, errs: "send it with the log, " + filepath.Join(logx.Folder(home), logx.File)},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out, errs bytes.Buffer
			code := runDebugDump(c.args, &out, &errs)
			if code != c.code || !strings.Contains(errs.String(), c.errs) {
				t.Fatalf("djinn debug dump %q = %d\n%s%s", c.args, code, out.String(), errs.String())
			}
			if code == 0 && c.args[0] != "--help" {
				if dumps := logx.Dumps(logx.Folder(home)); len(dumps) != 1 || strings.TrimSpace(out.String()) != dumps[0] {
					t.Errorf("printed %q, dumps %v", out.String(), dumps)
				}
			}
		})
	}
}

// TestDebugDump covers debugDump: a server that does not answer gets the request file, which its djinn up watches;
// when no djinn up dumps within dumpWait, it says so and takes the request back.
func TestDebugDump(t *testing.T) {
	defer func(d, w time.Duration) { dumpPoll, dumpWait = d, w }(dumpPoll, dumpWait)
	dumpPoll = 10 * time.Millisecond
	for _, c := range []struct {
		name    string
		wait    time.Duration
		watched bool // a djinn up watches the log folder
		err     string
	}{
		{name: "the request file", wait: 5 * time.Second, watched: true},
		{name: "no djinn up dumps", wait: 200 * time.Millisecond, err: "no djinn up dumped its goroutines within 200ms"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dumpWait = c.wait
			home := t.TempDir()
			if c.watched {
				dumping(t, home, nil, io.Discard)
			}
			var say strings.Builder
			path, err := debugDump(t.Context(), home, "http://127.0.0.1:1", &say)
			if !strings.Contains(say.String(), logx.DumpRequest) {
				t.Errorf("said %q", say.String())
			}
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) || logx.Dumps(logx.Folder(home)) != nil {
					t.Errorf("debugDump = %q, %v; want %q", path, err, c.err)
				}
				if _, err := os.Stat(filepath.Join(logx.Folder(home), logx.DumpRequest)); err == nil {
					t.Error("the request is still there: a djinn up started later would dump")
				}
				return
			}
			if err != nil || filepath.Dir(path) != logx.Folder(home) {
				t.Errorf("debugDump = %q, %v", path, err)
			}
		})
	}
}
