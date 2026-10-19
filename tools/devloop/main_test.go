package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/empowill/djinn/internal/server"
)

// TestRelay: the page's calls reach the djinn of the moment with its token, never from another page.
func TestRelay(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	const origin = "http://127.0.0.1:4317"
	r := httptest.NewServer(relay(home, origin))
	defer r.Close()
	call := func(from string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, r.URL+"/ui.v1.UiService/GetEnvironment", nil)
		if from != "" {
			req.Header.Set("Origin", from)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	if code, _ := call(origin); code != http.StatusServiceUnavailable {
		t.Fatalf("no djinn: %d", code)
	}

	// A djinn behind its guard, as djinn up --browser serves it.
	var seen *http.Request
	djinn := httptest.NewServer(nil)
	djinn.Config.Handler = server.Guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		_, _ = io.WriteString(w, "answered "+r.URL.Path)
	}), "s3cret", djinn.URL)
	defer djinn.Close()
	remove, err := server.WriteAddr(home, djinn.URL+"/?token=s3cret")
	if err != nil {
		t.Fatal(err)
	}
	defer remove()

	if code, body := call(origin); code != http.StatusOK || body != "answered /ui.v1.UiService/GetEnvironment" {
		t.Fatalf("from the page: %d %q", code, body)
	}
	if seen.Header.Get("Origin") != "" {
		t.Fatalf("djinn saw the origin %q", seen.Header.Get("Origin"))
	}
	if code, _ := call(""); code != http.StatusOK {
		t.Fatalf("without an origin: %d", code)
	}
	seen = nil
	if code, _ := call("http://evil.example"); code != http.StatusForbidden || seen != nil {
		t.Fatalf("from another page: %d, reached djinn %v", code, seen != nil)
	}
}

// TestDevHome: the loop never runs on the data of the Djinn in use; another folder is made, and given absolute.
func TestDevHome(t *testing.T) {
	t.Parallel()
	t.Run("the data of the Djinn in use", func(t *testing.T) {
		t.Parallel()
		config, err := os.UserConfigDir()
		if err != nil {
			t.Skip(err)
		}
		if _, err := devHome(filepath.Join(config, "djinn")); err == nil {
			t.Fatal("devHome accepted the data of the Djinn in use")
		}
	})
	t.Run("another folder", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "dev-home")
		if got, err := devHome(dir); err != nil || got != dir {
			t.Fatalf("devHome(%q) = %q, %v", dir, got, err)
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("not made: %v", err)
		}
	})
	t.Run("a folder it cannot make", func(t *testing.T) {
		t.Parallel()
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := devHome(filepath.Join(file, "dev-home")); err == nil {
			t.Fatal("devHome made a folder under a file")
		}
	})
}

// TestChanged sees a Go file change, and ignores a test file.
func TestChanged(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll(filepath.Join("internal", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join("internal", "x", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("x.go", "package x")
	seen := snapshot()
	if changed(&seen) {
		t.Fatal("changed with nothing changed")
	}
	write("x_test.go", "package x")
	if changed(&seen) {
		t.Fatal("a test file rebuilds djinn")
	}
	write("x.go", "package x // edited")
	if !changed(&seen) {
		t.Fatal("an edited Go file went unseen")
	}
	if changed(&seen) {
		t.Fatal("the same change seen twice")
	}
}

// TestPrefixedWrite marks every line of djinn, a partial one too, and says it wrote all it was given.
func TestPrefixedWrite(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, in, want string }{
		{"one line", "up\n", "[djinn] up\n"},
		{"two lines", "a\nb\n", "[djinn] a\n[djinn] b\n"},
		{"a partial line", "a\nb", "[djinn] a\n[djinn] b"},
		{"nothing", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			if n, err := (prefixed{&out}).Write([]byte(c.in)); n != len(c.in) || err != nil || out.String() != c.want {
				t.Errorf("Write(%q) = %d, %v, wrote %q; want %q", c.in, n, err, out.String(), c.want)
			}
		})
	}
	t.Run("a writer that fails", func(t *testing.T) {
		t.Parallel()
		_, w := io.Pipe()
		_ = w.Close()
		if n, err := (prefixed{w}).Write([]byte("up\n")); n != 0 || err == nil {
			t.Errorf("Write = %d, %v; want 0 and an error", n, err)
		}
	})
}

// output is what the loop prints, read while it runs.
type output struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *output) Write(b []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(b)
}

func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

// await waits until the output holds want n times.
func (o *output) await(t *testing.T, want string, n int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); strings.Count(o.String(), want) < n; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q (%d times) in:\n%s", want, n, o.String())
		}
	}
}

// fakes puts a fake node and go first on the PATH, sh scripts: node prints its arguments and waits, as Vite; go build
// writes a djinn that prints how it was started and waits, unless the file fail-build is in the current folder, or
// writes none with no-exe.
func fakes(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	for name, script := range map[string]string{
		"node": `echo "vite $*"; exec sleep 30`,
		"go": `[ -e fail-build ] && { echo "syntax error" >&2; exit 1; }
[ -e no-exe ] && exit 0
mkdir -p "$(dirname "$5")"
printf '#!/bin/sh\necho "up $* in $DJINN_HOME"\nexec sleep 30\n' > "$5"
chmod +x "$5"`,
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestRun starts Vite and a djinn built from the sources, rebuilds and restarts djinn when a Go file changes, keeps
// the last djinn when a build fails, and stops both once done. Not parallel: it changes the current folder and the
// PATH.
func TestRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake node and go are sh scripts")
	}
	src := func(t *testing.T, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join("internal", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join("internal", "x", "x.go"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("rebuilds on a change", func(t *testing.T) {
		t.Chdir(t.TempDir())
		fakes(t)
		src(t, "package x")
		home := filepath.Join(t.TempDir(), "home")
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var stdout, stderr output
		done := make(chan error, 1)
		go func() { done <- run(ctx, home, 4317, 20*time.Millisecond, &stdout, &stderr) }()
		stdout.await(t, "vite node_modules/vite/bin/vite.js --host 127.0.0.1 --port 4317 --strictPort", 1)
		stdout.await(t, "devloop: djinn runs (pid ", 1)
		stdout.await(t, "[djinn] up up --browser in "+home+"\n", 1)

		src(t, "package x // edited")
		stdout.await(t, "devloop: Go changed, rebuilding djinn\n", 1)
		stdout.await(t, "devloop: djinn runs (pid ", 2)

		if err := os.WriteFile("fail-build", nil, 0o644); err != nil {
			t.Fatal(err)
		}
		src(t, "package x // broken")
		stderr.await(t, "devloop: the build failed, djinn keeps its last version: exit status 1\nsyntax error\n", 1)
		if n := strings.Count(stdout.String(), "devloop: djinn runs"); n != 2 {
			t.Errorf("djinn restarted after a failed build: %d starts", n)
		}

		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("run = %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("run did not stop")
		}
	})
	t.Run("the data of the Djinn in use", func(t *testing.T) {
		config, err := os.UserConfigDir()
		if err != nil {
			t.Skip(err)
		}
		if err := run(t.Context(), filepath.Join(config, "djinn"), 4317, time.Second, io.Discard, io.Discard); err == nil ||
			!strings.Contains(err.Error(), "pick another -home") {
			t.Fatalf("run = %v", err)
		}
	})
	t.Run("no node", func(t *testing.T) {
		t.Chdir(t.TempDir())
		t.Setenv("PATH", t.TempDir())
		if err := run(t.Context(), t.TempDir(), 4317, time.Second, io.Discard, io.Discard); err == nil ||
			!strings.Contains(err.Error(), "start vite (npm ci first)") {
			t.Fatalf("run = %v", err)
		}
	})
	t.Run("djinn does not start", func(t *testing.T) {
		t.Chdir(t.TempDir())
		fakes(t)
		if err := os.WriteFile("no-exe", nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := run(t.Context(), t.TempDir(), 4317, time.Second, io.Discard, io.Discard); err == nil {
			t.Fatal("run went on without a djinn")
		}
	})
}

// TestHalt returns at once when no djinn runs (TestRun halts one that does).
func TestHalt(t *testing.T) {
	t.Parallel()
	for name, cmd := range map[string]*exec.Cmd{"none": nil, "never started": exec.Command("djinn")} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			halt(cmd)
		})
	}
}
