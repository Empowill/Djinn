// Command devloop is `go tool task dev`: Djinn while you edit it. Vite serves the interface and reloads it as the
// React code changes; djinn serves the API in browser mode, rebuilt and restarted when a Go file changes. A small
// relay between them adds djinn's token, so the page works without it. Everything runs on a data directory of its own
// (bin/dev-home by default), never the Djinn you use.
//
//	go run ./tools/devloop [-home bin/dev-home] [-port 4317]
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/empowill/djinn/internal/server"
)

func main() {
	home := flag.String("home", filepath.Join("bin", "dev-home"), "data directory of the djinn in development")
	port := flag.Int("port", 4317, "port of the Vite server, the page to open")
	poll := flag.Duration("poll", 500*time.Millisecond, "how often to look for a changed Go file")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *home, *port, *poll); err != nil {
		fmt.Fprintln(os.Stderr, "devloop:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, home string, port int, poll time.Duration) error {
	home, err := devHome(home)
	if err != nil {
		return err
	}
	origin := fmt.Sprintf("http://127.0.0.1:%d", port)

	// The relay: the page's calls, through Vite, reach the djinn of the moment.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	go func() { _ = http.Serve(ln, relay(home, origin)) }()
	defer ln.Close()

	vite := exec.CommandContext(ctx, "node", filepath.Join("node_modules", "vite", "bin", "vite.js"),
		"--host", "127.0.0.1", "--port", fmt.Sprint(port), "--strictPort", "--clearScreen", "false")
	vite.Env = append(os.Environ(), "DJINN_DEV_API=http://"+ln.Addr().String())
	vite.Stdout, vite.Stderr = os.Stdout, os.Stderr
	vite.Cancel = func() error { return interrupt(vite.Process) }
	if err := vite.Start(); err != nil {
		return fmt.Errorf("start vite (npm ci first): %w", err)
	}
	defer func() { _ = vite.Wait() }()

	exe := filepath.Join("bin", "djinn-dev"+exeExt())
	var djinn *exec.Cmd
	defer func() { halt(djinn) }()
	seen := snapshot()
	for first := true; ; first = false {
		if first || changed(&seen) {
			if !first {
				fmt.Println("devloop: Go changed, rebuilding djinn")
			}
			if err := build(ctx, exe); err != nil {
				fmt.Fprintln(os.Stderr, "devloop: the build failed, djinn keeps its last version:", err)
			} else {
				halt(djinn)
				if djinn, err = start(exe, home); err != nil {
					return err
				}
				fmt.Printf("devloop: djinn runs (pid %d, data %s); open %s/\n", djinn.Process.Pid, home, origin)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(poll):
		}
	}
}

// devHome is the data directory of the djinn in development, absolute. It is never the one of the Djinn in use.
func devHome(home string) (string, error) {
	home, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	if config, err := os.UserConfigDir(); err == nil && filepath.Clean(home) == filepath.Join(config, "djinn") {
		return "", fmt.Errorf("%s is the data of the Djinn you use: pick another -home", home)
	}
	return home, os.MkdirAll(home, 0o700)
}

// relay forwards the Connect calls of the page to the djinn that runs in home, with its token. Like djinn itself, it
// refuses a call that a browser says comes from another page than origin, the Vite server.
func relay(home, origin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && o != origin {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		addr, err := server.ReadAddr(home)
		target, perr := url.Parse(addr)
		if err != nil || perr != nil || target.Scheme != "http" {
			http.Error(w, "djinn is not running yet: the page retries", http.StatusServiceUnavailable)
			return
		}
		token := target.Query().Get("token")
		target.RawQuery = ""
		proxy := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				pr.Out.Header.Del("Origin") // Checked above; djinn knows its own origin only.
				pr.Out.Header.Set("Authorization", "Bearer "+token)
			},
			FlushInterval: -1, // Streams, value by value.
		}
		proxy.ServeHTTP(w, r)
	})
}

// watched are the Go sources of the djinn binary, and what it embeds besides the interface.
var watched = []string{"cmd/djinn", "internal", "gen/go", "locales", "go.mod", "go.sum", "assets.go"}

// snapshot is the modification time and size of every watched file.
func snapshot() map[string]string {
	files := map[string]string{}
	for _, root := range watched {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			if ext := filepath.Ext(p); ext != ".go" && ext != ".json" && ext != ".mod" && ext != ".sum" {
				return nil
			}
			if info, err := d.Info(); err == nil {
				files[p] = fmt.Sprint(info.ModTime().UnixNano(), info.Size())
			}
			return nil
		})
	}
	return files
}

// changed tells whether a watched file changed since seen, and updates it. A burst of saves settles first.
func changed(seen *map[string]string) bool {
	now := snapshot()
	if equal(now, *seen) {
		return false
	}
	for {
		time.Sleep(200 * time.Millisecond)
		next := snapshot()
		if equal(next, now) {
			*seen = next
			return true
		}
		now = next
	}
}

func equal(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// build compiles the djinn in development, headless: no CGO, no window, a quick build.
func build(ctx context.Context, exe string) error {
	cmd := exec.CommandContext(ctx, "go", "build", "-tags", "headless", "-o", exe, "./cmd/djinn")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// start runs djinn up in browser mode on home, its output prefixed.
func start(exe, home string) (*exec.Cmd, error) {
	cmd := exec.Command(exe, "up", "--browser")
	cmd.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "DJINN_HOME=") || strings.HasPrefix(kv, "DJINN_ADDR=")
	}), "DJINN_HOME="+home)
	cmd.Stdout = prefixed{os.Stdout}
	cmd.Stderr = prefixed{os.Stderr}
	return cmd, cmd.Start()
}

// halt stops djinn by its PID, as when it quits, and waits for it.
func halt(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = interrupt(cmd.Process)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

// interrupt asks p to stop cleanly; Windows has no interrupt to send, so it is killed.
func interrupt(p *os.Process) error {
	if runtime.GOOS == "windows" {
		return p.Kill()
	}
	return p.Signal(os.Interrupt)
}

func exeExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// prefixed marks djinn's lines among Vite's.
type prefixed struct{ w io.Writer }

func (p prefixed) Write(b []byte) (int, error) {
	lines := strings.SplitAfter(string(b), "\n")
	var out strings.Builder
	for _, l := range lines {
		if l != "" {
			out.WriteString("[djinn] " + l)
		}
	}
	if _, err := io.WriteString(p.w, out.String()); err != nil {
		return 0, err
	}
	return len(b), nil
}
