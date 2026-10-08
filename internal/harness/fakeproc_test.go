package harness

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test binary plays a provider when DJINN_FAKE_PROVIDER names one, so that the real process path (arguments,
// input, output, signals) is tested without a model, and on every system. It replays a fixture of the catalog
// (testdata/<provider>/<case>.jsonl):
//
//	DJINN_FAKE_PROVIDER  claude, antigravity or codex: how it reads its input
//	DJINN_FAKE_FIXTURE   the fixture: its lines go to the output, but those starting with # (comments)
//	DJINN_FAKE_STDERR    a file whose lines go to the error output first, when set
//	DJINN_FAKE_ARGS      a file the arguments are written to, one per line, when set
//	DJINN_FAKE_INPUT     a file each input line is written to, when set
//	DJINN_FAKE_END       what it does once the fixture is played: eof (wait until its input is closed, exit 0),
//	                     eof:N (the same, exit N), exit:N (exit N at once), hang (ignore SIGTERM, never end),
//	                     wait (wait until it is stopped)
//
// claude and antigravity read one message before the first line and after each result line. codex answers
// JSON-RPC: an answer line of the fixture (id and result or error) gets the id of the client's next request, and
// after a request line (id and method) the fake waits for the client's reply.
func TestMain(m *testing.M) {
	if provider := os.Getenv("DJINN_FAKE_PROVIDER"); provider != "" {
		os.Exit(fakeProvider(provider))
	}
	os.Exit(m.Run())
}

func fakeProvider(provider string) int {
	if f := os.Getenv("DJINN_FAKE_ARGS"); f != "" {
		_ = os.WriteFile(f, []byte(strings.Join(os.Args[1:], "\n")), 0o600)
	}
	var record = io.Discard
	if f := os.Getenv("DJINN_FAKE_INPUT"); f != "" {
		file, err := os.Create(f)
		if err != nil {
			return 10
		}
		defer file.Close()
		record = file
	}
	in := bufio.NewReader(os.Stdin)
	// next reads the next input line, or says the input is closed.
	next := func() (map[string]any, bool) {
		for {
			s, err := in.ReadString('\n')
			if s = strings.TrimSpace(s); s != "" {
				fmt.Fprintln(record, s)
				var msg map[string]any
				if json.Unmarshal([]byte(s), &msg) != nil {
					os.Exit(11)
				}
				return msg, true
			}
			if err != nil {
				return nil, false
			}
		}
	}
	if f := os.Getenv("DJINN_FAKE_STDERR"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return 12
		}
		os.Stderr.Write(b)
	}
	b, err := os.ReadFile(os.Getenv("DJINN_FAKE_FIXTURE"))
	if err != nil {
		return 13
	}
	var lines []string
	for l := range strings.Lines(string(b)) {
		if l = strings.TrimRight(l, "\r\n"); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	var code int
	switch provider {
	case "claude", "antigravity":
		code = fakeStream(provider, lines, next)
	case "codex":
		code = fakeCodex(lines, next)
	default:
		code = 14
	}
	if code != 0 {
		return code
	}
	end := os.Getenv("DJINN_FAKE_END")
	verb, arg, _ := strings.Cut(end, ":")
	n, _ := strconv.Atoi(arg)
	switch verb {
	case "", "eof":
		for {
			if _, ok := next(); !ok {
				return n
			}
		}
	case "exit":
		return n
	case "hang":
		signal.Ignore(syscall.SIGTERM)
		time.Sleep(time.Minute)
	case "wait":
		time.Sleep(time.Minute)
	}
	return 0
}

// fakeStream plays a stream-json agent: a message in, the lines of its turn out.
func fakeStream(provider string, lines []string, next func() (map[string]any, bool)) int {
	read := func() bool {
		msg, ok := next()
		if !ok {
			return false
		}
		if provider == "claude" {
			return msg["type"] == "user"
		}
		m, _ := msg["message"].(map[string]any)
		return msg["event"] == "user" && m["content"] != nil
	}
	if !read() {
		return 3
	}
	for i, l := range lines {
		fmt.Println(l)
		var m map[string]any
		_ = json.Unmarshal([]byte(l), &m)
		if (m["type"] == "result" || m["event"] == "result") && i < len(lines)-1 && !read() {
			return 3
		}
	}
	return 0
}

// fakeCodex plays a codex app-server.
func fakeCodex(lines []string, next func() (map[string]any, bool)) int {
	for _, l := range lines {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) != nil {
			fmt.Println(l)
			continue
		}
		_, hasID := m["id"]
		_, hasMethod := m["method"]
		switch {
		case hasID && !hasMethod: // An answer: to the client's next request.
			for {
				req, ok := next()
				if !ok {
					return 3
				}
				if _, isReq := req["method"]; isReq && req["id"] != nil {
					m["id"] = req["id"]
					break
				}
			}
			out, _ := json.Marshal(m)
			fmt.Println(string(out))
		case hasID: // A request: wait for the client's reply.
			fmt.Println(l)
			for {
				reply, ok := next()
				if !ok {
					return 3
				}
				if _, isReq := reply["method"]; !isReq && fmt.Sprint(reply["id"]) == fmt.Sprint(m["id"]) {
					break
				}
			}
		default:
			fmt.Println(l)
		}
	}
	return 0
}

// fake describes a run of the fake provider.
type fake struct {
	provider string // claude, antigravity or codex
	fixture  string // the case, a file of testdata/<provider>
	end      string // DJINN_FAKE_END
}

// env is the environment that makes the test binary play f, and the files where it writes its arguments and
// its input.
func (f fake) env(t *testing.T) (env []string, args, input string) {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("testdata", f.provider, f.fixture+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	args, input = filepath.Join(dir, "args"), filepath.Join(dir, "input")
	env = []string{"DJINN_FAKE_PROVIDER=" + f.provider, "DJINN_FAKE_FIXTURE=" + fixture, "DJINN_FAKE_END=" + f.end,
		"DJINN_FAKE_ARGS=" + args, "DJINN_FAKE_INPUT=" + input}
	if stderr := strings.TrimSuffix(fixture, ".jsonl") + ".stderr"; exists(stderr) {
		env = append(env, "DJINN_FAKE_STDERR="+stderr)
	}
	return env, args, input
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// fixtureLines are the lines of a fixture file, comments aside.
func fixtureLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for l := range strings.Lines(string(b)) {
		if l = strings.TrimRight(l, "\r\n"); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}
