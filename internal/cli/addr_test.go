package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
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

func TestUnixSocket(t *testing.T) {
	// A Unix socket path is limited to about 100 bytes; t.TempDir is longer than that on macOS.
	home, err := os.MkdirTemp("", "dj")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	socket := filepath.Join(home, server.SocketFile)
	ln, err := server.ListenUnix(socket)
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{}
	go server.Serve(t.Context(), ln, handler(f)) //nolint:errcheck // Stops with the test.

	if code, _, errs := runIn(home, "project", "list"); code != 1 || !strings.Contains(errs, "djinn up") {
		t.Fatalf("no address file: code %d, stderr %q, want a hint to run djinn up", code, errs)
	}
	remove, err := server.WriteAddr(home, "unix://"+socket)
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	if code, out, errs := runIn(home, "project", "list"); code != 0 || !strings.Contains(out, "name: api") {
		t.Fatalf("over the socket: code %d\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}
	if len(f.calls) != 1 {
		t.Fatalf("server calls: %d, want 1", len(f.calls))
	}
}

func TestHTTPBearer(t *testing.T) {
	const token = "s3cret"
	srv := httptest.NewUnstartedServer(nil)
	origin := "http://" + srv.Listener.Addr().String()
	f := &fake{}
	srv.Config.Handler = server.Guard(handler(f), token, origin)
	srv.Start()
	defer srv.Close()
	home := t.TempDir()

	// The address djinn up writes carries the token, which the command line sends as a bearer token.
	if _, err := server.WriteAddr(home, origin+"/?token="+token); err != nil {
		t.Fatal(err)
	}
	if code, out, errs := runIn(home, "project", "list"); code != 0 || !strings.Contains(out, "name: api") {
		t.Fatalf("with the token: code %d\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}
	// --addr wins over the file, and without a token the guard refuses the call.
	if code, _, errs := runIn(home, "--addr", origin, "project", "list"); code != 1 || !strings.Contains(errs, "unauthenticated") {
		t.Fatalf("without the token: code %d, stderr %q, want unauthenticated", code, errs)
	}
	if code, _, errs := runIn(home, "--addr", origin+"/?token=wrong", "project", "list"); code != 1 || !strings.Contains(errs, "unauthenticated") {
		t.Fatalf("wrong token: code %d, stderr %q, want unauthenticated", code, errs)
	}
	if len(f.calls) != 1 {
		t.Fatalf("server calls: %d, want 1", len(f.calls))
	}
}

func TestDialAddress(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:4000", "ftp://host", "unix:/x", "http://"} {
		if _, _, err := dial(addr); err == nil {
			t.Errorf("dial(%q): no error", addr)
		}
	}
	if _, base, err := dial("http://127.0.0.1:4000/?token=x"); err != nil || base != "http://127.0.0.1:4000" {
		t.Errorf("dial with token: base %q, %v", base, err)
	}
	// A Unix socket keeps no host: the dialer ignores it.
	if _, base, err := dial("unix:///tmp/djinn.sock"); err != nil || base != "http://djinn" {
		t.Errorf("dial unix: base %q, %v", base, err)
	}
}

// resumes answers LeadService.Resume, which is marked autostart.
type resumes struct {
	planv1connect.UnimplementedLeadServiceHandler
}

func (resumes) Resume(
	_ context.Context, req *connect.Request[planv1.LeadServiceResumeRequest],
) (*connect.Response[planv1.LeadServiceResumeResponse], error) {
	return connect.NewResponse(&planv1.LeadServiceResumeResponse{Terminal: "lead"}), nil
}

func TestAutostart(t *testing.T) {
	lp, lh := planv1connect.NewLeadServiceHandler(resumes{})
	f := &fake{}
	qp, qh := planv1connect.NewQuestionServiceHandler(questions{fake: f})
	srv := httptest.NewServer(server.Handler(fstest.MapFS{"index.html": {}}, nil, map[string]http.Handler{lp: lh, qp: qh}))
	defer srv.Close()
	home := t.TempDir()
	started := 0
	run := func(args ...string) (int, string, string) {
		var out, errs bytes.Buffer
		code := Run(t.Context(), args, Config{Home: home, Stdout: &out, Stderr: &errs, Start: func(context.Context) (string, error) {
			started++
			if _, err := server.WriteAddr(home, srv.URL); err != nil {
				return "", err
			}
			return srv.URL, nil
		}})
		return code, out.String(), errs.String()
	}

	// A method without autostart fails as before when no djinn answers.
	if code, _, errs := run("question", "list"); code != 1 || started != 0 || !strings.Contains(errs, "djinn up") {
		t.Fatalf("question list without djinn: code %d, started %d, stderr %q", code, started, errs)
	}
	// An address left by a crash is not a djinn: resume starts one, then calls it.
	if _, err := server.WriteAddr(home, "http://127.0.0.1:1/?token=old"); err != nil {
		t.Fatal(err)
	}
	if code, out, errs := run("lead", "resume"); code != 0 || started != 1 || !strings.Contains(out, "lead") {
		t.Fatalf("resume without djinn: code %d, started %d\n%s%s", code, started, out, errs)
	}
	// Once one answers, nothing is started.
	if code, _, errs := run("lead", "resume"); code != 0 || started != 1 {
		t.Fatalf("resume with djinn: code %d, started %d, stderr %q", code, started, errs)
	}
	if !Alive(srv.URL) || Alive("http://127.0.0.1:1") || Alive("unix://"+filepath.Join(home, "none.sock")) {
		t.Error("Alive is wrong about a server")
	}
}

// TestRefusesRealHomeSocket: Alive, Dial, and Run refuse to touch the real data directory or socket under test.
func TestRefusesRealHomeSocket(t *testing.T) {
	realHome := ui.RealHome()
	if realHome == "" {
		t.Skip("no real home directory found on this system")
	}

	realSock := "unix://" + filepath.Join(realHome, "djinn.sock")
	if Alive(realSock) {
		t.Fatal("Alive must return false for real home socket under test")
	}
	if _, _, err := Dial(realSock); err == nil || !strings.Contains(err.Error(), "refusing to dial real data directory") {
		t.Fatalf("expected dial refusal for real home socket under test, got err: %v", err)
	}

	var out, errs bytes.Buffer
	code := Run(t.Context(), []string{"wish", "list"}, Config{Home: realHome, Stdout: &out, Stderr: &errs})
	if code == 0 {
		t.Fatalf("Run unexpectedly succeeded on real home under test: %s", out.String())
	}
	if !strings.Contains(errs.String(), "refusing to use real data directory") {
		t.Fatalf("expected Run refusal message, got: %s", errs.String())
	}
}

// TestCallerHeader: each request says who runs the command (harness.CallerHeader), so that only the developer's own
// commands keep their ifrits lit: the lead's terminal, a worker, a watcher and Djinn's checks are not the developer.
func TestCallerHeader(t *testing.T) {
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
	for _, c := range []struct {
		env  map[string]string
		want string
	}{
		{nil, "you"},
		{map[string]string{"DJINN_LEAD": "1"}, "lead"},
		{map[string]string{"DJINN_WORKER_TOKEN": "w0rker"}, "djinn"},
		{map[string]string{"DJINN_TASK_ID": "01a144b9-e0d2-71af-8c46-469eb0ca4513"}, "djinn"},
		{map[string]string{"DJINN_CALLER": "djinn"}, "djinn"},
	} {
		for _, name := range []string{"DJINN_LEAD", "DJINN_WORKER_TOKEN", "DJINN_TASK_ID", "DJINN_CALLER"} {
			t.Setenv(name, c.env[name])
		}
		seen = nil
		if code, _, errs := runIn(home, "project", "list"); code != 0 || len(seen) != 1 || seen[0] != c.want {
			t.Errorf("with %v: code %d (%s), caller %q, want %q", c.env, code, errs, seen, c.want)
		}
	}
}
