package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/server"
)

// TestDebugDumpAsksTheServer: djinn debug dump gets its dump from the server's answer (W511).
func TestDebugDumpAsksTheServer(t *testing.T) {
	home := t.TempDir()
	srv := httptest.NewServer(server.Handler(nil, nil, map[string]http.Handler{server.DumpPath: server.Dump(logx.Folder(home))}))
	defer srv.Close()
	path, err := debugDump(t.Context(), home, srv.URL, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if dumps := logx.Dumps(logx.Folder(home)); len(dumps) != 1 || dumps[0] != path {
		t.Errorf("path %s, dumps %v", path, dumps)
	}
}

// TestDebugDumpLeavesARequest: a server that does not answer gets the request file, which its djinn up watches.
func TestDebugDumpLeavesARequest(t *testing.T) {
	defer func(d, w time.Duration) { dumpPoll, dumpWait = d, w }(dumpPoll, dumpWait)
	dumpPoll, dumpWait = 10*time.Millisecond, 5*time.Second
	home := t.TempDir()
	go dumpGoroutines(t.Context(), home, nil, io.Discard)
	var say strings.Builder
	path, err := debugDump(t.Context(), home, "http://127.0.0.1:1", &say)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != logx.Folder(home) || !strings.Contains(say.String(), logx.DumpRequest) {
		t.Errorf("path %s, said %q", path, say.String())
	}
}
