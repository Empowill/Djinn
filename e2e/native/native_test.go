// Package native drives the real native window of Djinn end to end, through the MCP server of Wails: a test build
// (`-tags mcp`) serves it on a loopback port of its own, behind a token. Run it with `go tool task e2e-native`, which
// builds that binary and passes it in DJINN_E2E_NATIVE. It opens a window, so `go tool task test` skips it.
//
// It touches only what it started: its own data folder, its own port, its own window ("Djinn e2e"), and it stops the
// binary by its PID.
package native

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/gen/go/ui/v1/uiv1connect"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/server"
)

// hold keeps the window open this long before it is closed, to look at it or capture it: DJINN_E2E_NATIVE_HOLD=5s.
const holdEnv = "DJINN_E2E_NATIVE_HOLD"

func TestNativeWindow(t *testing.T) {
	binary := os.Getenv("DJINN_E2E_NATIVE")
	if binary == "" {
		t.Skip("opens a native window: run it with go tool task e2e-native")
	}
	d := start(t, binary)

	t.Run("the interface shows", func(t *testing.T) {
		var page struct {
			Title    string `json:"title"`
			Terminal bool   `json:"terminal"`
			Bridge   bool   `json:"bridge"`
		}
		eventually(t, 30*time.Second, func() error {
			if err := d.eval(`return {
				title: document.title,
				terminal: !!document.querySelector(".lead-terminal .xterm"),
				bridge: !!document.querySelector(".wish-app"),
			};`, &page); err != nil {
				return err
			}
			if !page.Terminal || !page.Bridge {
				return fmt.Errorf("not ready: %+v", page)
			}
			return nil
		})
		if !strings.HasSuffix(page.Title, "Djinn") {
			t.Errorf("document title %q, want one ending in Djinn", page.Title)
		}
	})

	// The window reads the wishes itself and follows them (WishService.Watch through wails://): a wish made from the
	// command line shows without a reload.
	t.Run("a wish made by the command line shows in the window", func(t *testing.T) {
		addr, err := server.ReadAddr(d.home)
		if err != nil {
			t.Fatal(err)
		}
		client, base, err := cli.Dial(addr)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		wishes := planv1connect.NewWishServiceClient(client, base)
		title := "Native " + d.token[:8]
		if _, err := wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: title, Paused: true})); err != nil {
			t.Fatal(err)
		}
		eventually(t, 10*time.Second, func() error {
			var page struct {
				Titles []string `json:"titles"`
			}
			if err := d.eval(`return { titles: [...document.querySelectorAll(".wish-nav")].map((b) => b.textContent) };`,
				&page); err != nil {
				return err
			}
			for _, got := range page.Titles {
				if strings.Contains(got, title) {
					return nil
				}
			}
			return fmt.Errorf("no wish %q in the side panel: %q", title, page.Titles)
		})
	})

	// A lead asks with `djinn question ask`, another process: the question shows in the window that shows its wish,
	// without a reload.
	t.Run("a question asked by the command line shows in the window", func(t *testing.T) {
		addr, err := server.ReadAddr(d.home)
		if err != nil {
			t.Fatal(err)
		}
		client, base, err := cli.Dial(addr)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		title := "Asked " + d.token[:8]
		made, err := planv1connect.NewWishServiceClient(client, base).
			Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: title}))
		if err != nil {
			t.Fatal(err)
		}
		eventually(t, 10*time.Second, func() error {
			var page struct {
				Shown string `json:"shown"`
			}
			if err := d.eval(`const item = [...document.querySelectorAll(".wish-nav")]
				.find((b) => b.textContent.includes(`+jsString(title)+`));
				item?.click();
				return { shown: document.querySelector(".hero h1")?.textContent ?? "" };`, &page); err != nil {
				return err
			}
			if page.Shown != title {
				return fmt.Errorf("the window shows %q, not %q", page.Shown, title)
			}
			return nil
		})

		text := "Which lamp first " + d.token[:8] + "?"
		ask := exec.Command(d.binary, "question", "ask", text, made.Msg.GetWish().GetId(),
			"--options", "Brass", "--options", "Glass", "--recommendation", "B: lighter")
		ask.Env = append(os.Environ(), "DJINN_HOME="+d.home)
		if out, err := ask.CombinedOutput(); err != nil {
			t.Fatalf("djinn question ask: %v\n%s", err, out)
		}
		eventually(t, 10*time.Second, func() error {
			var page struct {
				Cards []string `json:"cards"`
			}
			if err := d.eval(`return { cards: [...document.querySelectorAll(".question-card")].map((c) => c.textContent) };`,
				&page); err != nil {
				return err
			}
			for _, card := range page.Cards {
				if strings.Contains(card, text) {
					return nil
				}
			}
			return fmt.Errorf("no question %q in the window: %q", text, page.Cards)
		})
	})

	t.Run("the terminal runs a command and shows its output", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("whoami prints the domain too on Windows")
		}
		me, err := user.Current()
		if err != nil {
			t.Fatal(err)
		}
		// Typed key by key into xterm, as a person would. The MCP server gives a punctuation key the key code of
		// another key ("'" reads as the right arrow) and xterm drops its space: only letters, digits and Enter go
		// through, hence a command without arguments.
		if _, err := d.call("keyboard_type", map[string]any{
			"selector": ".lead-terminal .xterm-helper-textarea",
			"text":     "whoami",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := d.call("keyboard_press", map[string]any{"key": "Enter"}); err != nil {
			t.Fatal(err)
		}
		// The command line reads "$ whoami": only the output is the user name alone.
		eventually(t, 10*time.Second, func() error {
			var rows struct {
				Lines []string `json:"lines"`
			}
			if err := d.eval(`return { lines: [...document.querySelectorAll(".lead-terminal .xterm-rows > div")]
				.map((row) => row.textContent.trim()).filter(Boolean) };`, &rows); err != nil {
				return err
			}
			for _, line := range rows.Lines {
				if line == me.Username {
					return nil
				}
			}
			return fmt.Errorf("no line %q on the terminal: %q", me.Username, rows.Lines)
		})
	})

	if hold, err := time.ParseDuration(os.Getenv(holdEnv)); err == nil && hold > 0 {
		t.Logf("holding the window %s (%s)", hold, holdEnv)
		time.Sleep(hold)
	}

	// Q36: closing the window puts it out of sight; djinn goes on and still answers.
	t.Run("closing the window keeps djinn running", func(t *testing.T) {
		if _, err := d.call("window_control", map[string]any{"action": "close"}); err != nil {
			t.Fatal(err)
		}
		eventually(t, 10*time.Second, func() error {
			var windows []struct {
				Minimised bool `json:"minimised"`
				Visible   bool `json:"visible"`
			}
			text, err := d.call("windows_list", nil)
			if err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(text), &windows); err != nil {
				return err
			}
			if len(windows) != 1 {
				return fmt.Errorf("%d windows, want 1: %s", len(windows), text)
			}
			// macOS hides the window, as its apps do; the others minimise it.
			if runtime.GOOS == "darwin" && windows[0].Visible || runtime.GOOS != "darwin" && !windows[0].Minimised {
				return fmt.Errorf("the window is still in sight: %s", text)
			}
			return nil
		})
		if d.exited() {
			t.Fatal("djinn quit when its window closed")
		}
		// The server answers the command line as before: the lead and the workers go on.
		addr, err := server.ReadAddr(d.home)
		if err != nil {
			t.Fatal(err)
		}
		client, base, err := cli.Dial(addr)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ui := uiv1connect.NewUiServiceClient(client, base)
		if _, err := ui.GetEnvironment(ctx, connect.NewRequest(&uiv1.UiServiceGetEnvironmentRequest{})); err != nil {
			t.Fatalf("djinn no longer answers once its window is closed: %v", err)
		}
	})
}

// djinn is the test build running for one test.
type djinn struct {
	cmd      *exec.Cmd
	binary   string
	home     string
	token    string
	endpoint string
	done     chan struct{}
}

// start runs binary up with a data folder, an MCP port and a token of its own, and waits for its MCP server. The
// cleanup stops it by its PID and removes the folder.
func start(t *testing.T, binary string) *djinn {
	t.Helper()
	// Short: the socket path must fit in 104 bytes, and macOS's temporary folder is long.
	base := os.TempDir()
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	home, err := os.MkdirTemp(base, "dj-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	token := randomHex(t)

	cmd := exec.Command(binary, "up")
	cmd.Dir = home
	cmd.Env = append(os.Environ(),
		"DJINN_HOME="+home,
		// The MCP server listens on the loopback only, on a free port it logs, and asks for the token.
		"WAILS_MCP_HOST=127.0.0.1",
		"WAILS_MCP_PORT=0",
		"WAILS_MCP_TOKEN="+token,
		"WAILS_MCP_HIDE_CURSOR=1",
	)
	if runtime.GOOS != "windows" {
		// The terminal of the window runs a plain POSIX shell, whatever the developer's own shell is.
		cmd.Env = append(cmd.Env, "SHELL=/bin/sh")
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	var out logBuffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	d := &djinn{cmd: cmd, binary: binary, home: home, token: token, done: make(chan struct{})}
	found := make(chan string, 1)
	go func() {
		endpoint := regexp.MustCompile(`url=(http://127\.0\.0\.1:\d+/mcp)`)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			out.WriteString(line + "\n")
			if m := endpoint.FindStringSubmatch(line); m != nil {
				select {
				case found <- m[1]:
				default:
				}
			}
		}
		_ = cmd.Wait()
		close(d.done)
	}()
	t.Cleanup(func() {
		d.stop(t)
		if t.Failed() {
			t.Logf("djinn's output:\n%s", out.String())
		}
	})

	select {
	case d.endpoint = <-found:
	case <-d.done:
		t.Fatalf("djinn exited before its MCP server started:\n%s", out.String())
	case <-time.After(30 * time.Second):
		t.Fatalf("no MCP server after 30 s: was djinn built with -tags mcp?\n%s", out.String())
	}
	t.Logf("djinn pid %d, data %s, MCP %s", cmd.Process.Pid, home, d.endpoint)
	return d
}

// stop interrupts djinn by its PID, as Ctrl+C would, and kills it if it does not stop in time.
func (d *djinn) stop(t *testing.T) {
	if d.exited() {
		return
	}
	if runtime.GOOS == "windows" {
		_ = d.cmd.Process.Kill() // Windows has no interrupt to send to another process.
	} else {
		_ = d.cmd.Process.Signal(os.Interrupt)
	}
	select {
	case <-d.done:
		if code := d.cmd.ProcessState.ExitCode(); code != 0 && runtime.GOOS != "windows" {
			t.Errorf("djinn exited with %d on interrupt", code)
		}
	case <-time.After(15 * time.Second):
		_ = d.cmd.Process.Kill()
		<-d.done
		t.Error("djinn did not stop within 15 s of an interrupt: killed")
	}
}

func (d *djinn) exited() bool {
	select {
	case <-d.done:
		return true
	default:
		return false
	}
}

// call runs one tool of the MCP server and returns its text.
func (d *djinn) call(tool string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, d.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.token)
	client := http.Client{Timeout: 60 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: HTTP %d: %s", tool, res.StatusCode, data)
	}
	var reply struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return "", fmt.Errorf("%s: %w: %s", tool, err, data)
	}
	if reply.Error != nil {
		return "", fmt.Errorf("%s: %s", tool, reply.Error.Message)
	}
	text := ""
	if len(reply.Result.Content) > 0 {
		text = reply.Result.Content[0].Text
	}
	if reply.Result.IsError {
		return "", fmt.Errorf("%s: %s", tool, text)
	}
	return text, nil
}

// eval runs js, the body of an async function returning an object, in the window and decodes that object into out.
// An exception comes back with its message: WebKit gives the MCP server only its stack.
func (d *djinn) eval(js string, out any) error {
	js = "try {\n" + js + "\n} catch (e) { return { evalError: String(e?.message ?? e) }; }"
	text, err := d.call("js_eval", map[string]any{"js": js, "timeout_ms": 5000})
	if err != nil {
		return err
	}
	var failed struct {
		EvalError string `json:"evalError"`
	}
	if json.Unmarshal([]byte(text), &failed) == nil && failed.EvalError != "" {
		return fmt.Errorf("javascript error: %s", failed.EvalError)
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		return fmt.Errorf("js_eval returned %q: %w", text, err)
	}
	return nil
}

// eventually retries check until it succeeds or timeout passes, then fails with its last error.
func eventually(t *testing.T, timeout time.Duration, check func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		err := check()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %s: %v", timeout, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// jsString is s as a JavaScript string literal.
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func randomHex(t *testing.T) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// logBuffer collects djinn's output from two goroutines.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) WriteString(s string) { _, _ = b.Write([]byte(s)) }

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
