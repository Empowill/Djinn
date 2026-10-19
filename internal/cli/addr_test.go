package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/ui"
)

// handler serves the fake services the way djinn up does, through server.Handler.
func handler(f *fake) http.Handler {
	qp, qh := planv1connect.NewQuestionServiceHandler(questions{fake: f})
	pp, ph := planv1connect.NewProjectServiceHandler(projects{fake: f})
	return server.Handler(fstest.MapFS{"index.html": {}}, nil, map[string]http.Handler{qp: qh, pp: ph})
}

// runIn runs a command line with no address given, as an agent does: the command line reads the address file.
func runIn(home string, args ...string) (int, string, string) {
	var out, errs bytes.Buffer
	code := Run(context.Background(), args, Config{Version: "test", Home: home, Stdout: &out, Stderr: &errs})
	return code, out.String(), errs.String()
}

// realSocket is the socket of the real data directory, "" when the system has no home directory.
func realSocket() string {
	if home := ui.RealHome(); home != "" {
		return "unix://" + filepath.Join(home, "djinn.sock")
	}
	return ""
}

// TestDial: an address is a Unix socket or an http URL with a host, its token kept out of the base URL; the real data
// directory's socket is refused under test.
func TestDial(t *testing.T) {
	for _, tt := range []struct {
		name, addr, wantBase, wantErr string
	}{
		{name: "no scheme", addr: "127.0.0.1:4000", wantErr: "expected unix:///path/to/djinn.sock or http://host:port"},
		{name: "another scheme", addr: "ftp://host", wantErr: "expected unix:///path/to/djinn.sock or http://host:port"},
		{name: "a socket without its slashes", addr: "unix:/x", wantErr: "expected unix:///path/to/djinn.sock or http://host:port"},
		{name: "no host", addr: "http://", wantErr: "expected unix:///path/to/djinn.sock or http://host:port"},
		{name: "the token leaves the URL", addr: "http://127.0.0.1:4000/?token=x", wantBase: "http://127.0.0.1:4000"},
		// A Unix socket keeps no host: the dialer ignores it.
		{name: "a socket", addr: "unix:///tmp/djinn.sock", wantBase: "http://djinn"},
		{name: "the real home socket", addr: realSocket(), wantErr: "refusing to dial real data directory"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.addr == "" {
				t.Skip("no real home directory found on this system")
			}
			_, base, err := Dial(tt.addr)
			if base != tt.wantBase || (err == nil) != (tt.wantErr == "") || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("Dial(%q) = %q, %v; want %q, %q", tt.addr, base, err, tt.wantBase, tt.wantErr)
			}
		})
	}
}

// TestAlive: a server that takes a connection is alive; an address left by a crash, or the real data directory's
// socket under test, is not.
func TestAlive(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close) // After the cases, which run side by side.
	for _, tt := range []struct {
		name, addr string
		want       bool
	}{
		{"a server that answers", srv.URL, true},
		{"a port nobody listens on", "http://127.0.0.1:1", false},
		{"a socket that is gone", "unix://" + filepath.Join(t.TempDir(), "none.sock"), false},
		{"no host", "http://", false},
		{"the real home socket", realSocket(), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.addr == "" {
				t.Skip("no real home directory found on this system")
			}
			if got := Alive(tt.addr); got != tt.want {
				t.Errorf("Alive(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

// TestCaller: each request says who runs the command (harness.CallerHeader), so that only the developer's own
// commands keep their ifrits lit: the lead's terminal, a worker, a watcher and Djinn's checks are not the developer.
func TestCaller(t *testing.T) {
	f := &fake{}
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("X-Djinn-Caller"))
		handler(f).ServeHTTP(w, r)
	}))
	defer srv.Close()
	home := t.TempDir()
	if _, err := server.WriteAddr(home, srv.URL); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"the developer", nil, "you"},
		{"the lead", map[string]string{"DJINN_LEAD": "1"}, "lead"},
		{"a worker", map[string]string{"DJINN_WORKER_TOKEN": "w0rker"}, "djinn"},
		{"a watcher", map[string]string{"DJINN_TASK_ID": "01a144b9-e0d2-71af-8c46-469eb0ca4513"}, "djinn"},
		{"a check", map[string]string{"DJINN_CALLER": "djinn"}, "djinn"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, name := range []string{"DJINN_LEAD", "DJINN_WORKER_TOKEN", "DJINN_TASK_ID", "DJINN_CALLER"} {
				t.Setenv(name, tt.env[name])
			}
			seen = nil
			if code, _, errs := runIn(home, "project", "list"); code != 0 || len(seen) != 1 || seen[0] != tt.want {
				t.Errorf("code %d (%s), caller %q, want %q", code, errs, seen, tt.want)
			}
		})
	}
}
