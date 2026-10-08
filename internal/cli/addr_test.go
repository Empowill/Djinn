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
)

// handler serves the fake services the way djinn up does, through server.Handler.
func handler(f *fake) http.Handler {
	qp, qh := planv1connect.NewQuestionServiceHandler(questions{fake: f})
	pp, ph := planv1connect.NewProjectServiceHandler(projects{fake: f})
	return server.Handler(fstest.MapFS{"index.html": {}}, map[string]http.Handler{qp: qh, pp: ph})
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

// resumes answers WishService.Resume, which is marked autostart.
type resumes struct {
	planv1connect.UnimplementedWishServiceHandler
}

func (resumes) Resume(
	_ context.Context, req *connect.Request[planv1.WishServiceResumeRequest],
) (*connect.Response[planv1.WishServiceResumeResponse], error) {
	return connect.NewResponse(&planv1.WishServiceResumeResponse{Terminal: "lead-" + req.Msg.GetWishId()}), nil
}

func TestAutostart(t *testing.T) {
	wp, wh := planv1connect.NewWishServiceHandler(resumes{})
	f := &fake{}
	qp, qh := planv1connect.NewQuestionServiceHandler(questions{fake: f})
	srv := httptest.NewServer(server.Handler(fstest.MapFS{"index.html": {}}, map[string]http.Handler{wp: wh, qp: qh}))
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
	if code, out, errs := run("wish", "resume", wishID); code != 0 || started != 1 || !strings.Contains(out, "lead-"+wishID) {
		t.Fatalf("resume without djinn: code %d, started %d\n%s%s", code, started, out, errs)
	}
	// Once one answers, nothing is started.
	if code, _, errs := run("wish", "resume", wishID); code != 0 || started != 1 {
		t.Fatalf("resume with djinn: code %d, started %d, stderr %q", code, started, errs)
	}
	if !Alive(srv.URL) || Alive("http://127.0.0.1:1") || Alive("unix://"+filepath.Join(home, "none.sock")) {
		t.Error("Alive is wrong about a server")
	}
}
