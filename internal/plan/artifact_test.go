package plan

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/tilasm"
)

// fakeClaude is a claude that replays a recorded stream: its status command exits auth (0 signed in), its other
// calls write their folder and arguments to the log, one per line, then print stream and exit with exit. It is a copy
// of this test binary named claude, shared by every fake (FakeClaudeMain), so that it runs on Windows too; its part is
// read from a folder of its own, put first on the PATH. It returns that PATH, and the log.
func fakeClaude(t *testing.T, auth int, stream string, exit int) (path, log string) {
	t.Helper()
	bin := fakeClaudeBin(t)
	dir := t.TempDir()
	log = filepath.Join(dir, "calls.log")
	b, err := json.Marshal(fakeClaudePart{Auth: auth, Stream: stream, Exit: exit, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fakeClaudeFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return strings.Join([]string{dir, bin, os.Getenv("PATH")}, string(os.PathListSeparator)), log
}

// fakeClaudeFile, in the first folder of a fake claude's PATH, holds its part.
const fakeClaudeFile = "fake-claude.json"

type fakeClaudePart struct {
	Auth   int    `json:"auth"`
	Stream string `json:"stream"`
	Exit   int    `json:"exit"`
	Log    string `json:"log"`
}

var fakeClaudeShared struct {
	once sync.Once
	dir  string
	err  error
}

// fakeClaudeBin is the folder of the fakes' claude, made at the first fake.
func fakeClaudeBin(t *testing.T) string {
	t.Helper()
	f := &fakeClaudeShared
	f.once.Do(func() { f.dir, f.err = newFakeClaudeBin() })
	if f.err != nil {
		t.Fatal(f.err)
	}
	return f.dir
}

func newFakeClaudeBin() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "fake-claude-")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "claude")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	// A link costs no copy, but Windows refuses to remove a name of a running program: a copy there, or where the
	// folders' disks differ.
	if runtime.GOOS != "windows" && os.Link(exe, bin) == nil {
		return dir, nil
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		return "", err
	}
	return dir, os.WriteFile(bin, b, 0o755)
}

// FakeClaudeMain plays a fake claude when this test binary runs as one (fakeClaude), and exits; otherwise it returns
// what removes the fakes' claude once the tests ended.
func FakeClaudeMain() (cleanup func()) {
	if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") != "claude" {
		return func() {
			if dir := fakeClaudeShared.dir; dir != "" {
				_ = os.RemoveAll(dir)
			}
		}
	}
	first, _, _ := strings.Cut(os.Getenv("PATH"), string(os.PathListSeparator))
	b, err := os.ReadFile(filepath.Join(first, fakeClaudeFile))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var part fakeClaudePart
	if err := json.Unmarshal(b, &part); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(os.Args) > 1 && os.Args[1] == "auth" {
		os.Exit(part.Auth)
	}
	if err := fakeClaudeCall(part); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(part.Exit)
	return nil
}

// fakeClaudeCall logs a call, its folder (as a shell reads it, from PWD when it names the folder) then its
// arguments, and prints the stream.
func fakeClaudeCall(part fakeClaudePart) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(part.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(f, strings.Join(append(append([]string{dir}, os.Args[1:]...), "--"), "\n")+"\n")
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	if part.Stream == "" {
		return nil
	}
	s, err := os.Open(part.Stream)
	if err != nil {
		return err
	}
	defer s.Close()
	_, err = io.Copy(os.Stdout, s)
	return err
}

// signedIn is a user's home folder where Claude Code has its folder: its status command decides.
func signedIn(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

// initLine is the first event of a recorded publish, its tools those of claude 2.1.303 with the Artifact tool offered.
func initLine(t *testing.T, tools string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "init.jsonl")
	if err := os.WriteFile(p, []byte(`{"type":"system","subtype":"init","tools":[`+tools+`],"model":"claude-haiku-5-5"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// calls are the calls the fake claude logged, each its folder then its arguments.
func calls(t *testing.T, log string) [][]string {
	t.Helper()
	b, err := os.ReadFile(log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	var call []string
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if line == "--" {
			out, call = append(out, call), nil
			continue
		}
		call = append(call, line)
	}
	return out
}

// TestPublisherCheck: the button shows only when claude is installed, signed in, and offers the Artifact tool, looked
// for once per start.
func TestPublisherCheck(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		auth   int
		tools  string // the init event's; "-": no init event
		noBin  bool
		want   planv1.PublishBlock
		probes int
	}{
		{name: "not installed", noBin: true, want: planv1.PublishBlock_PUBLISH_BLOCK_NO_CLAUDE},
		{name: "signed out", auth: 1, tools: `"Artifact"`, want: planv1.PublishBlock_PUBLISH_BLOCK_SIGNED_OUT},
		{name: "no Artifact tool", tools: `"Read","Bash"`, want: planv1.PublishBlock_PUBLISH_BLOCK_NO_ARTIFACT_TOOL, probes: 1},
		{name: "no init event", tools: "-", want: planv1.PublishBlock_PUBLISH_BLOCK_CHECK_FAILED, probes: 1},
		{name: "offered", tools: `"Artifact","ArtifactComments","ArtifactData","Read"`, probes: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			stream := ""
			if c.tools != "-" && c.tools != "" {
				stream = initLine(t, c.tools)
			}
			path, log := fakeClaude(t, c.auth, stream, 0)
			if c.noBin {
				path = t.TempDir()
			}
			p := &Publisher{Home: t.TempDir(), UserHome: signedIn(t), Path: path}
			for range 2 {
				block, detail, _ := p.Check(t.Context())
				if block != c.want {
					t.Fatalf("Check = %v (%s), want %v", block, detail, c.want)
				}
			}
			got := calls(t, log)
			if len(got) != c.probes {
				t.Fatalf("claude was called %d times, want %d: the tool is looked for once per start", len(got), c.probes)
			}
			if c.probes > 0 && !strings.Contains(strings.Join(got[0], " "), "--tools Artifact,Read") {
				t.Errorf("the check's call = %v, want the tools of a publish", got[0])
			}
		})
	}
}

// TestReadArtifactStream: the address comes from the Artifact tool's result line, never from the model's text; a
// refused publish tells why.
func TestReadArtifactStream(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		fixture string
		url     string
		version int32
		cost    float64
		why     string
	}{
		{fixture: "publish.jsonl", url: "https://claude.ai/artifact/D1gnnFxekF5SvDnhTFSFcj", version: 2, cost: 0.01720977},
		{fixture: "publish-unread.jsonl", cost: 0.01172162, why: "left out of the recording"},
		{fixture: "publish-type-refused.jsonl", cost: 0.015656330000000003, why: `extension ".docx" is not served`},
	} {
		t.Run(c.fixture, func(t *testing.T) {
			t.Parallel()
			f, err := os.Open(filepath.Join("testdata", "artifact", c.fixture))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			lines := bufio.NewScanner(f)
			lines.Buffer(make([]byte, 64<<10), 16<<20)
			pub, last, _ := readArtifactStream(lines)
			if pub.URL != c.url || pub.Version != c.version || pub.CostUSD != c.cost {
				t.Errorf("read %+v, want %s version %d for $%v", pub, c.url, c.version, c.cost)
			}
			if c.why != "" && !strings.Contains(last, c.why) {
				t.Errorf("why = %q, want it to hold %q", last, c.why)
			}
		})
	}
}

// TestArtifactPrompt: the page read by its path, published by the same path as a JSON string, whose backslashes
// (the separator on Windows, a letter of a folder's name elsewhere) come doubled.
func TestArtifactPrompt(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{filepath.FromSlash("/data/tilasms/.publish-1"), `/data/back\slash/.publish-1`} {
		page := filepath.Join(dir, tilasm.Index)
		quoted, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		first := artifactPrompt(&tilasm.Bundle{Dir: dir, Files: []string{"img/one.png"}}, "")
		for _, want := range []string{
			"1. Read " + page + ".",
			`2. Call Artifact with {"action":"publish","file_path":` + string(quoted) + `,"files":{"img/one.png":"img/one.png"},"icon":"document"}.`,
		} {
			if !strings.Contains(first, want) {
				t.Errorf("first publish prompt lacks %q:\n%s", want, first)
			}
		}
		if strings.Contains(first, `"read"`) {
			t.Errorf("a first publish reads no live version:\n%s", first)
		}
	}
	again := artifactPrompt(&tilasm.Bundle{Dir: "/data/tilasms/.publish-2"}, "https://claude.ai/artifact/D1gnn")
	for _, want := range []string{
		`1. Call Artifact with {"action": "read", "url": "https://claude.ai/artifact/D1gnn"}`,
		`"url":"https://claude.ai/artifact/D1gnn"`,
		"call it again with the same arguments",
	} {
		if !strings.Contains(again, want) {
			t.Errorf("publish again prompt lacks %q:\n%s", want, again)
		}
	}
	if strings.Contains(again, `"icon"`) || strings.Contains(again, `"files"`) {
		t.Errorf("a page alone publishes again with no icon nor files:\n%s", again)
	}
}

// TestPublisherPublish: one call, in the bundle's folder, under the paid gate, its address read from the tool's
// result; a call that publishes nothing fails, saying why.
func TestPublisherPublish(t *testing.T) {
	t.Parallel()
	recorded := func(name string) string { return filepath.Join("testdata", "artifact", name) }
	for _, c := range []struct {
		name    string
		stream  string
		exit    int
		gateErr error
		url     string
		err     string
	}{
		{name: "published", stream: recorded("publish.jsonl"), url: "https://claude.ai/artifact/D1gnnFxekF5SvDnhTFSFcj"},
		{name: "page not read", stream: recorded("publish-unread.jsonl"), err: "claude did not publish L52"},
		{name: "a type refused", stream: recorded("publish-type-refused.jsonl"), err: `claude did not publish L52: files: "probe/probe.docx"`},
		{name: "claude fails", exit: 3, err: "exit status 3"},
		{name: "gate refused", stream: recorded("publish.jsonl"), gateErr: context.Canceled, err: "take the paid gate"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			stream, err := filepath.Abs(c.stream)
			if c.stream == "" || err != nil {
				stream = ""
			}
			// The check's call reads the same stream: its first line is the init event, the Artifact tool in it.
			path, log := fakeClaude(t, 0, stream, c.exit)
			var gated []string
			p := &Publisher{Home: t.TempDir(), UserHome: signedIn(t), Path: path,
				Gate: func(_ context.Context, taskID, what string) (func(), error) {
					gated = append(gated, taskID+": "+what)
					return func() { gated = append(gated, "given back") }, c.gateErr
				}}
			if c.stream == "" {
				p.checked = &artifactCheck{} // Checked already: the stream has no init event to find.
			}
			// The bundle's folder through a link: claude runs in it as named, not with its links resolved. Windows
			// may refuse a link to a user without the right; Djinn sets no PWD there, and the folder itself serves.
			dir := filepath.Join(t.TempDir(), "bundle")
			if err := os.Symlink(t.TempDir(), dir); err != nil {
				if runtime.GOOS != "windows" {
					t.Fatal(err)
				}
				dir = t.TempDir()
			}
			pub, err := p.Publish(t.Context(), "W7", "L52", &tilasm.Bundle{Dir: dir}, "https://claude.ai/artifact/D1gnnFxekF5SvDnhTFSFcj")
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("Publish = %+v, %v; want an error saying %q", pub, err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if pub.URL != c.url || pub.Version != 2 || pub.CostUSD == 0 {
				t.Errorf("published %+v, want %s, version 2, its cost", pub, c.url)
			}
			if strings.Join(gated, ", ") != "W7: publish tilasm L52 on claude.ai, given back" {
				t.Errorf("gate = %v, want the paid gate taken for the task, then given back", gated)
			}
			got := calls(t, log)
			last := got[len(got)-1]
			args := strings.Join(last[1:], " ")
			if last[0] != dir {
				t.Errorf("claude ran in %s, want the bundle's folder %s: the files it sends are found from it", last[0], dir)
			}
			for _, want := range []string{"--model claude-haiku-5-5", "--tools Artifact,Read", `--settings {"env":{"CLAUDE_CODE_ARTIFACT":"1"}}`,
				"--permission-prompts none", "--strict-mcp-config", "--no-session-persistence"} {
				if !strings.Contains(args, want) {
					t.Errorf("claude's arguments lack %q: %s", want, args)
				}
			}
		})
	}
}
