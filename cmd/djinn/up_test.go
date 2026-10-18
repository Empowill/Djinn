//go:build !windows

// These tests run djinn itself: the test binary turns into the djinn command when DJINN_TEST_MAIN is set, so a
// process it starts, and the djinn up that one starts in the background, are this same build. Each test has a data
// directory of its own, and stops the processes it started by their PID, never by their name: the developer's own
// djinn keeps running. The build is headless, so djinn up serves the browser: no window opens. Not on Windows: they
// stop djinn up with SIGINT, which Windows cannot send another process, and the fake claude is a shell script.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	terminalv1 "github.com/empowill/djinn/gen/go/terminal/v1"
	"github.com/empowill/djinn/gen/go/terminal/v1/terminalv1connect"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/gen/go/ui/v1/uiv1connect"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

func TestMain(m *testing.M) {
	if os.Getenv("DJINN_TEST_MAIN") == "1" {
		if v := os.Getenv("DJINN_TEST_VERSION"); v != "" {
			version = v
		}
		// A copy of this binary "installed" by a test carries its version in a file next to it, or, when it comes from
		// a fake release, at its end.
		if exe, err := os.Executable(); err == nil {
			if v, err := os.ReadFile(exe + ".version"); err == nil {
				version = strings.TrimSpace(string(v))
			}
			if v := trailerVersion(exe); v != "" {
				version = v
			}
		}
		updatePoll, restartDelay = updatePollForTests, restartDelayForTests
		extendPath = false
		// A djinn up of a test starts its workers whatever else the machine runs (machine.Calm).
		readMachine = machine.Calm
		// No test reaches the network: only a fake release, served by the test, is looked for.
		checkReleases = os.Getenv("DJINN_TEST_RELEASE_API") != ""
		if checkReleases {
			releaseAPI = os.Getenv("DJINN_TEST_RELEASE_API")
			releaseAsset = os.Getenv("DJINN_TEST_RELEASE_ASSET")
			releaseCheck = releaseCheckForTests
		}
		main()
		os.Exit(0)
	}
	cleanup := testx.Guard()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// environ is the environment of a djinn of the test: its data directory (none when home is empty), a plain POSIX
// shell, the fake agents of bin first on the PATH, and no question worker: an answer would start a real agent.
func environ(home, bin string, extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "DJINN_") && !strings.HasPrefix(kv, "PATH=") && !strings.HasPrefix(kv, "SHELL=") {
			env = append(env, kv)
		}
	}
	env = append(env, "DJINN_TEST_MAIN=1", "DJINN_QUESTION_WORKERS=off", "SHELL=/bin/sh", "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if home != "" {
		env = append(env, "DJINN_HOME="+home)
	}
	return append(env, extra...)
}

// buffer is an output read while the process writes it.
type buffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// up starts djinn up in env, waits until it answers, and stops it at the end of the test.
func up(t *testing.T, home string, env []string) (out *buffer, addr string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "up")
	cmd.Env = env
	out, errs := &buffer{}, &buffer{}
	cmd.Stdout, cmd.Stderr = out, errs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGINT)
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
	})
	addr = answering(t, home, func() string { return errs.String() })
	return out, addr
}

// answering waits until a djinn answers in home, and returns its address.
func answering(t *testing.T, home string, log func() string) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if addr, err := server.ReadAddr(home); err == nil && cli.Alive(addr) {
			return addr
		}
		if time.Now().After(deadline) {
			t.Fatalf("djinn does not answer in %s:\n%s", home, log())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// runDjinn runs one command of djinn in env, and returns its exit code and its outputs.
func runDjinn(t *testing.T, env []string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = env
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	return code, out.String(), errs.String()
}

// stopStarted stops the djinn a command started in the background, by the PID it printed, and waits until it has
// removed its address.
func stopStarted(t *testing.T, home, stderr string) {
	t.Helper()
	m := regexp.MustCompile(`\(pid (\d+),`).FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("no PID of the djinn started in the background:\n%s", stderr)
	}
	pid, _ := strconv.Atoi(m[1])
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGINT)
		deadline := time.Now().Add(15 * time.Second)
		for {
			_, err := server.ReadAddr(home)
			if err != nil {
				return
			}
			if time.Now().After(deadline) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
}

// fakeClaude writes a claude command in a new folder that says how it was called and where, counts its starts in
// the file count, then waits for its terminal to hang up.
func fakeClaude(t *testing.T) (bin, count string) {
	t.Helper()
	bin = t.TempDir()
	count = filepath.Join(t.TempDir(), "starts")
	script := "#!/bin/sh\nargs=\"\"\nwhile [ $# -gt 0 ]; do\n  case \"$1\" in\n    --allowedTools|--project) shift 2 ;;\n    *) args=\"${args:+$args }$1\"; shift ;;\n  esac\ndone\necho \"fake-claude $args in $(pwd)\"\necho start >> \"$DJINN_TEST_COUNT\"\nexec cat\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, count
}

// read follows the output of a terminal until it holds want, or with an empty want until the program ends.
func read(t *testing.T, c terminalv1connect.TerminalServiceClient, id, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stream, err := c.Read(ctx, connect.NewRequest(&terminalv1.TerminalServiceReadRequest{Id: id}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var got bytes.Buffer
	for stream.Receive() {
		got.Write(stream.Msg().GetData())
		if want != "" && lineOf(got.String(), want) || want == "" && stream.Msg().GetExited() {
			return
		}
	}
	t.Fatalf("terminal %s: %v; want %q in:\n%q", id, stream.Err(), want, got.String())
}

// lineOf tells whether a line of out, terminal line ends aside, is want.
func lineOf(out, want string) bool {
	s := bufio.NewScanner(strings.NewReader(out))
	for s.Scan() {
		if strings.TrimRight(s.Text(), "\r") == want {
			return true
		}
	}
	return false
}

// TestRunUp covers runUp, djinn up, on processes of this test binary, each with a data folder of its own: the flags
// and variables it refuses, and what a djinn up does once it runs, case by case. The cases run in parallel; a case
// starts one or two djinn up and runs djinn commands against them, about a second each here, more on a loaded machine.
func TestRunUp(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		args []string
		env  []string
		errs string // what djinn up says before it exits
	}{
		{name: "help", args: []string{"--help"}, errs: "Usage: djinn up"},
		{name: "an argument", args: []string{"now"}, errs: `djinn up: unexpected argument "now"`},
		{name: "--workers 17", args: []string{"--workers", "17"}, errs: "--workers 17: expected 1 to 16"},
		{name: "DJINN_WORKERS", env: []string{"DJINN_WORKERS=many"}, errs: "DJINN_WORKERS: "},
		{name: "--worker-cpu -1", args: []string{"--worker-cpu", "-1"}, errs: "--worker-cpu -1: expected 1 to "},
		{name: "DJINN_WORKER_CPU", env: []string{"DJINN_WORKER_CPU=half"}, errs: "DJINN_WORKER_CPU: "},
		{name: "--worker-memory -1", args: []string{"--worker-memory", "-1"}, errs: "--worker-memory -1: expected MiB"},
		{name: "DJINN_WORKER_MEMORY", env: []string{"DJINN_WORKER_MEMORY=lots"}, errs: "DJINN_WORKER_MEMORY: "},
		{name: "--worker-memory-guard -1", args: []string{"--worker-memory-guard", "-1"}, errs: "--worker-memory-guard -1"},
		{name: "DJINN_WORKER_MEMORY_GUARD", env: []string{"DJINN_WORKER_MEMORY_GUARD=invalid"}, errs: "DJINN_WORKER_MEMORY_GUARD: "},
		{name: "DJINN_ENLIGHTEN_WORKERS", env: []string{"DJINN_ENLIGHTEN_WORKERS=maybe"}, errs: `DJINN_ENLIGHTEN_WORKERS: "maybe": expected on or off`},
		{name: "DJINN_PPROF", env: []string{"DJINN_PPROF=maybe"}, errs: `DJINN_PPROF: "maybe": expected on or off`},
		{name: "--disk-trim-gb -1", args: []string{"--disk-trim-gb", "-1"}, errs: "--disk-trim-gb -1: expected GB, or 0 for never"},
		{name: "DJINN_DISK_FULL_GB", env: []string{"DJINN_DISK_FULL_GB=ten"}, errs: "DJINN_DISK_FULL_GB: "},
		{name: "--ifrit-idle 4m", args: []string{"--ifrit-idle", "4m"}, errs: "expected whole minutes from 5m to 4h"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			want := 1
			if c.args != nil && c.args[0] == "--help" {
				want = 0
			}
			code, out, errs := runDjinn(t, environ(t.TempDir(), t.TempDir(), c.env...), append([]string{"up"}, c.args...)...)
			if code != want || !strings.Contains(out+errs, c.errs) {
				t.Errorf("djinn up %q with %q: exit %d, want %d saying %q\n%s%s", c.args, c.env, code, want, c.errs, out, errs)
			}
		})
	}
	for _, c := range []struct {
		name string
		run  func(t *testing.T)
	}{
		{"twice shows the first", upTwiceShowsTheFirst},
		{"replaces the address of a crash", upReplacesTheAddressOfACrash},
		{"a development build keeps its own data", upDevelopmentBuildKeepsItsOwnData},
		{"lead resume", upLeadResume},
		{"an exited codex lead stays until resumed", upExitedCodexLeadIsRetainedUntilExplicitResume},
		{"a lead that cannot start at start shows", upLeadStartFailureAtStartShows},
		{"read-only methods answer a GET", upReadOnlyMethodsAnswerAGet},
		{"the window's terminal opens in a project", upWindowTerminalOpensInAProject},
		{"DJINN_PPROF serves the profiles", upProfiles},
		{"an inbox source plugged runs in djinn up", upInboxPlug},
		{"a crash resumes the workers", upCrashResumesTheWorkers},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t)
		})
	}
}

// upTwiceShowsTheFirst: a second djinn up on the same data folder shows the first one, which says its address again.
func upTwiceShowsTheFirst(t *testing.T) {
	home := t.TempDir()
	env := environ(home, t.TempDir())
	first, addr := up(t, home, env)
	code, out, errs := runDjinn(t, env, "up", "--terminal", "ignored")
	if code != 0 || !strings.Contains(out, "djinn is already running: open "+addr) || !strings.Contains(out, "ignored: --terminal") {
		t.Fatalf("second djinn up: exit %d\n%s%s", code, out, errs)
	}
	// The first one printed its address again, and still answers.
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(first.String(), "djinn: open "+addr) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the first djinn did not show its address again:\n%s", first.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if again, err := server.ReadAddr(home); err != nil || again != addr || !cli.Alive(addr) {
		t.Fatalf("after the second djinn up: %q, %v; want the first one, answering", again, err)
	}
}

// upReplacesTheAddressOfACrash: the address a crashed djinn left is replaced, and djinn up says the last one did not stop cleanly.
func upReplacesTheAddressOfACrash(t *testing.T) {
	home := t.TempDir()
	// A port that was open, and no longer is: the address of a djinn that crashed.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stale := "http://" + ln.Addr().String() + "/?token=old"
	ln.Close()
	if _, err := server.WriteAddr(home, stale); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "up")
	cmd.Env = environ(home, t.TempDir())
	errs := &buffer{}
	cmd.Stderr = errs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGINT); _ = cmd.Wait() })
	addr := answering(t, home, errs.String)
	if addr == stale || !strings.Contains(errs.String(), "did not stop cleanly") {
		t.Fatalf("address %q after a crash, stderr:\n%s", addr, errs.String())
	}
}

// upDevelopmentBuildKeepsItsOwnData: without a data folder given, a development build opens djinn-dev in the user's
// config folder, a build with a version djinn, never the other one.
func upDevelopmentBuildKeepsItsOwnData(t *testing.T) {
	for _, c := range []struct{ version, dir string }{{"", "djinn-dev"}, {"local-1234abcd", "djinn"}} {
		t.Run(c.dir, func(t *testing.T) {
			t.Parallel()
			user := t.TempDir()
			config := filepath.Join(user, ".config") // os.UserConfigDir without XDG_CONFIG_HOME
			if runtime.GOOS == "darwin" {
				config = filepath.Join(user, "Library", "Application Support")
			}
			env := environ("", t.TempDir(), "HOME="+user, "XDG_CONFIG_HOME=")
			if c.version != "" {
				env = append(env, "DJINN_TEST_VERSION="+c.version)
			}
			home := filepath.Join(config, c.dir)
			up(t, home, env)
			other := map[string]string{"djinn": "djinn-dev", "djinn-dev": "djinn"}[c.dir]
			if _, err := os.Stat(filepath.Join(config, other)); err == nil {
				t.Errorf("version %q opened %s too", c.version, other)
			}
		})
	}
}

// upLeadResume: djinn lead resume starts djinn up in the background when none runs, resumes the wish's lead in its
// terminal and shows it; again, it attaches to the same terminal; a session another terminal runs is not resumed twice.
func upLeadResume(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	bin, count := fakeClaude(t)
	env := environ(home, bin, "DJINN_TEST_COUNT="+count)
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const session = "0b7e2a8c-5f1d-4c1e-9a3e-1f2d3c4b5a69"
	// A wish with its lead, written while no djinn runs.
	db, err := store.Open(ctx, filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	project := &planv1.Project{Id: store.NewID(), Name: "lamp", Directory: folder, CreateTime: timestamppb.Now()}
	leadRecord := &planv1.Lead{Id: "lead", Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: session, Directory: folder}
	wish := &planv1.Wish{
		Id: store.NewID(), Title: "Polish the lamp", ProjectIds: []string{project.GetId()}, CreateTime: timestamppb.Now(),
	}
	err = db.Tx(ctx, func(tx *store.Tx) error {
		return errors.Join(tx.Journal("test", planv1connect.WishServiceMakeProcedure, &planv1.WishServiceMakeRequest{Title: wish.GetTitle()}),
			tx.Put(project), tx.Put(wish), tx.Put(leadRecord))
	})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	// No djinn runs: resume starts one in the background, then resumes the lead in its terminal. The wish being the
	// first active one, djinn up may have resumed its lead at its start already: resume attaches to it then.
	code, out, errs := runDjinn(t, env, "lead", "resume", "--wish-id", wish.GetId())
	if strings.Contains(errs, "(pid ") {
		stopStarted(t, home, errs)
	}
	name := "lead"
	if code != 0 || !strings.Contains(errs, "started djinn up in the background") || !strings.Contains(out, "terminal: "+name) {
		t.Fatalf("resume: exit %d\n%s%s", code, out, errs)
	}
	addr := answering(t, home, func() string { return errs })
	httpClient, base, err := cli.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	terminals := terminalv1connect.NewTerminalServiceClient(httpClient, base)
	opened, err := terminals.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: name}))
	if err != nil || !opened.Msg.GetAttached() {
		t.Fatalf("the lead's terminal: %v, %v; want it running", opened, err)
	}
	lead := opened.Msg.GetTerminal()
	read(t, terminals, lead.GetId(), fmt.Sprintf("fake-claude --resume %s in %s", session, folder))

	// The window is asked to show the wish and its lead, even one that starts watching after.
	ui := uiv1connect.NewUiServiceClient(httpClient, base)
	shows, err := ui.WatchShow(ctx, connect.NewRequest(&uiv1.UiServiceWatchShowRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !shows.Receive() || shows.Msg().GetWishId() != wish.GetId() || shows.Msg().GetTerminal() != name {
		t.Fatalf("shown: %v, %v", shows.Msg(), shows.Err())
	}
	shows.Close()

	// Again: the same terminal, attached, and no second program.
	code, out, errs = runDjinn(t, env, "lead", "resume", "--wish-id", wish.GetId())
	if code != 0 || !strings.Contains(out, "attached: true") || strings.Contains(errs, "started djinn up") {
		t.Fatalf("second resume: exit %d\n%s%s", code, out, errs)
	}
	time.Sleep(300 * time.Millisecond)
	if b, _ := os.ReadFile(count); strings.Count(string(b), "start") != 1 {
		t.Fatalf("claude started %d times, want once", strings.Count(string(b), "start"))
	}

	// The guard: once the lead's terminal ended, a session another terminal of djinn runs is not resumed twice.
	if _, err := terminals.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{
		Name: "main", Command: []string{"/bin/sh", "-c", "claude --resume " + session}, Directory: folder,
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := terminals.Close(ctx, connect.NewRequest(&terminalv1.TerminalServiceCloseRequest{Id: lead.GetId()})); err != nil {
		t.Fatal(err)
	}
	read(t, terminals, lead.GetId(), "") // Until it ends.
	code, out, errs = runDjinn(t, env, "lead", "resume", "--wish-id", wish.GetId())
	if code != 1 || !strings.Contains(errs, "already running in another terminal") || !strings.Contains(errs, `"main"`) {
		t.Fatalf("resume while main runs the session: exit %d\n%s%s", code, out, errs)
	}
}

// upExitedCodexLeadIsRetainedUntilExplicitResume: a codex lead that ends at once keeps its terminal, its output and
// its exit code, which LeadService.Get gives; opening its terminal attaches to it and starts nothing. Only Lead
// (LeadService.Resume) starts it again.
func upExitedCodexLeadIsRetainedUntilExplicitResume(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	bin := t.TempDir()
	count := filepath.Join(t.TempDir(), "starts")
	fails := filepath.Join(t.TempDir(), "fails")
	// A fake codex that counts its starts and ends at once with code 2 while the file fails exists.
	script := `#!/bin/sh
echo start >> "$DJINN_TEST_COUNT"
if [ -f "$DJINN_TEST_FAILS" ]; then
  echo fake-codex-failed
  exit 2
fi
echo fake-codex-ready
exec cat
`
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fails, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	env := environ(home, bin, "DJINN_TEST_COUNT="+count, "DJINN_TEST_FAILS="+fails)
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, addr := up(t, home, env)
	httpClient, base, err := cli.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	projects := planv1connect.NewProjectServiceClient(httpClient, base)
	leads := planv1connect.NewLeadServiceClient(httpClient, base)
	terminals := terminalv1connect.NewTerminalServiceClient(httpClient, base)
	if _, err := projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: folder})); err != nil {
		t.Fatal(err)
	}
	resume := func() {
		t.Helper()
		res, err := leads.Resume(ctx, connect.NewRequest(&planv1.LeadServiceResumeRequest{Provider: planv1.Provider_PROVIDER_CODEX}))
		if err != nil || res.Msg.GetAttached() {
			t.Fatalf("resume: %v, %v; want a new lead", res, err)
		}
	}
	open := func() *terminalv1.TerminalServiceOpenResponse {
		t.Helper()
		res, err := terminals.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: plan.LeadTerminal}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}
	exit := func() *planv1.LeadExit {
		t.Helper()
		res, err := leads.Get(ctx, connect.NewRequest(&planv1.LeadServiceGetRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetExit()
	}

	resume()
	first := open().GetTerminal().GetId()
	read(t, terminals, first, "") // Until it ends.
	ended := open()
	if ended.GetTerminal().GetId() != first || !ended.GetAttached() || !ended.GetTerminal().GetExited() ||
		ended.GetTerminal().GetExitCode() != 2 {
		t.Fatalf("open the ended lead: %v, want the same terminal, ended with code 2", ended)
	}
	read(t, terminals, first, "fake-codex-failed")
	if got := exit(); got == nil || got.GetCode() != 2 {
		t.Fatalf("lead exit %v, want code 2", got)
	}
	time.Sleep(300 * time.Millisecond)
	if got := starts(t, count); got != 1 {
		t.Fatalf("codex started %d times before the retry, want once", got)
	}

	// Lead again: a new program, which runs; the exit goes with the terminal it replaced.
	if err := os.Remove(fails); err != nil {
		t.Fatal(err)
	}
	resume()
	retry := open().GetTerminal()
	if retry.GetId() == first {
		t.Fatal("the retry kept the ended terminal")
	}
	read(t, terminals, retry.GetId(), "fake-codex-ready")
	if got := exit(); got != nil {
		t.Fatalf("lead exit %v after the retry, want none", got)
	}
	if got := starts(t, count); got != 2 {
		t.Fatalf("codex started %d times after the retry, want twice", got)
	}

	// Closed on request, its tab's × or djinn lead resume of another agent: no news to show.
	if _, err := terminals.Close(ctx, connect.NewRequest(&terminalv1.TerminalServiceCloseRequest{Id: retry.GetId()})); err != nil {
		t.Fatal(err)
	}
	read(t, terminals, retry.GetId(), "") // Until it ends.
	if got := exit(); got != nil {
		t.Fatalf("lead exit %v after a close, want none", got)
	}
}

// upLeadStartFailureAtStartShows: the lead djinn up could not resume at its start shows beside Lead, as a
// failed Lead would, where a Djinn started from a menu has no terminal to print it in.
func upLeadStartFailureAtStartShows(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	bin, count := fakeClaude(t)
	env := environ(home, bin, "DJINN_TEST_COUNT="+count)
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(folder, "gone")
	db, err := store.Open(ctx, filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Tx(ctx, func(tx *store.Tx) error {
		return errors.Join(
			tx.Journal("test", planv1connect.LeadServiceSetProcedure, &planv1.LeadServiceSetRequest{SessionId: "s"}),
			tx.Put(&planv1.Project{Id: store.NewID(), Name: "lamp", Directory: folder, CreateTime: timestamppb.Now()}),
			tx.Put(&planv1.Lead{Id: "lead", Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: "0b7e2a8c-5f1d-4c1e-9a3e-1f2d3c4b5a69", Directory: gone}))
	})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	_, addr := up(t, home, env)
	httpClient, base, err := cli.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	got, err := planv1connect.NewLeadServiceClient(httpClient, base).Get(ctx, connect.NewRequest(&planv1.LeadServiceGetRequest{}))
	if err != nil || !strings.Contains(got.Msg.GetStartError(), "is not on this machine") {
		t.Fatalf("lead at start: %v, %v; want why it did not start", got, err)
	}
	if starts(t, count) != 0 {
		t.Fatal("claude started")
	}
}

// upReadOnlyMethodsAnswerAGet calls a method that only reads as a GET on a real djinn up, the request in the
// query; a method that writes refuses the GET.
func upReadOnlyMethodsAnswerAGet(t *testing.T) {
	home := t.TempDir()
	env := environ(home, t.TempDir())
	_, addr := up(t, home, env)
	if code, out, errs := runDjinn(t, env, "project", "add", t.TempDir(), "--name", "lamp"); code != 0 {
		t.Fatalf("djinn project add: exit %d\n%s%s", code, out, errs)
	}
	httpClient, base, err := cli.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	get := func(method, message string) (int, string) {
		t.Helper()
		u := base + "/plan.v1.ProjectService/" + method + "?encoding=json&message=" + url.QueryEscape(message)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, u, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, string(body)
	}
	if code, body := get("List", "{}"); code != http.StatusOK || !strings.Contains(body, `"name":"lamp"`) {
		t.Errorf("GET List: %d %s, want 200 with the project", code, body)
	}
	if code, body := get("Add", `{"directory":"/tmp"}`); code != http.StatusMethodNotAllowed {
		t.Errorf("GET Add: %d %s, want 405", code, body)
	}
}

// upWindowTerminalOpensInAProject: started without --terminal-dir, as from a menu, the window's terminal opens in
// the first project's folder, never in the home folder; with no project, it does not start, and says to create one.
func upWindowTerminalOpensInAProject(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	user, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := environ(home, t.TempDir(), "HOME="+user)
	_, addr := startUp(t, home, env)
	_, terminals := clients(t, addr)

	_, err = terminals.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: "main"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "create a project first") {
		t.Fatalf("the terminal without a project: %v, want failed_precondition asking for a project", err)
	}
	folder := filepath.Join(user, "code", "lamp")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if code, out, errs := runDjinn(t, env, "project", "add", folder); code != 0 {
		t.Fatalf("project add: exit %d\n%s%s", code, out, errs)
	}
	opened, err := terminals.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: "main"}))
	if err != nil {
		t.Fatal(err)
	}
	if dir := opened.Msg.GetTerminal().GetDirectory(); dir != folder {
		t.Errorf("the terminal opened in %q, want the project's folder %q", dir, folder)
	}
}

// upProfiles: with $DJINN_PPROF on, djinn up serves Go's profiles on its own address, as its services; off, the path
// is the interface's, as any other.
func upProfiles(t *testing.T) {
	home := t.TempDir()
	_, addr := up(t, home, environ(home, t.TempDir(), "DJINN_PPROF=on"))
	httpClient, base, err := cli.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+server.ProfilePath+"cmdline", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != http.StatusOK || !strings.Contains(string(body), "up") {
		t.Errorf("GET %scmdline: %d %q, %v; want djinn's command line", server.ProfilePath, res.StatusCode, body, err)
	}
}

// upInboxPlug: a project's skill declares an inbox source; djinn inbox sources lists it, not plugged in; djinn inbox
// plug runs it in djinn up, and its item comes; djinn inbox unplug stops it, having run it once.
func upInboxPlug(t *testing.T) {
	home := t.TempDir()
	env := environ(home, t.TempDir())
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runs := filepath.Join(t.TempDir(), "runs")
	skill := filepath.Join(folder, ".agents", "skills", "mentions")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	front := "---\nname: mentions\ndescription: Where I am mentioned.\nmetadata:\n  djinn:\n    source:\n" +
		"      watch: \"sh source.sh\"\n---\n"
	script := "echo run >> '" + runs + "'\necho 'carol mentioned you'\necho https://forge.example.com/acme/bell/issues/7\n"
	if err := errors.Join(
		os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(front), 0o644),
		os.WriteFile(filepath.Join(folder, "source.sh"), []byte(script), 0o644),
	); err != nil {
		t.Fatal(err)
	}
	up(t, home, env)
	djinn := func(args ...string) string {
		t.Helper()
		code, out, errs := runDjinn(t, env, args...)
		if code != 0 {
			t.Fatalf("djinn %s: exit %d\n%s%s", strings.Join(args, " "), code, out, errs)
		}
		return out
	}
	djinn("project", "add", folder, "--name", "bell")

	// Declared, not plugged in: listed as such. That its command never starts is TestSourceUnplugged's.
	out := djinn("inbox", "sources")
	if !strings.Contains(out, "name: bell/mentions") || !strings.Contains(out, "watch: sh source.sh") ||
		strings.Contains(out, "plugged: true") {
		t.Errorf("sources:\n%s", out)
	}

	if out := djinn("inbox", "plug", "mentions"); !strings.Contains(out, "plugged: true") {
		t.Errorf("plug:\n%s", out)
	}
	// The plug wakes the sources at once: its item comes as soon as the command has printed it.
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(djinn("inbox", "list"), "carol mentioned you") {
		if time.Now().After(deadline) {
			t.Fatal("the plugged source's item never came")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if out := djinn("inbox", "unplug", "bell/mentions"); !strings.Contains(out, "name: bell/mentions") ||
		strings.Contains(out, "plugged: true") {
		t.Errorf("unplug:\n%s", out)
	}
	if out := djinn("inbox", "sources"); strings.Contains(out, "plugged: true") {
		t.Errorf("sources after unplugging:\n%s", out)
	}
	if b, err := os.ReadFile(runs); err != nil || string(b) != "run\n" {
		t.Errorf("runs %q, %v; want one", b, err)
	}
	if code, _, errs := runDjinn(t, env, "inbox", "plug", "bell/bells"); code != 1 || !strings.Contains(errs, "no inbox source bell/bells") {
		t.Errorf("plug an unknown source: exit %d, %s", code, errs)
	}
}

// upCrashResumesTheWorkers: a djinn killed while a worker runs leaves its task running in the store; the next
// djinn up resumes it by itself, in the same task, and it finishes. The wish keeps one task.
func upCrashResumesTheWorkers(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	bin, _ := fakeClaude(t)
	env := environ(home, bin)
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// An agent configuration: the worker edits as the project says, without asking first.
	if err := os.WriteFile(filepath.Join(folder, "AGENTS.md"), []byte("# Rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wish := seedWish(t, home, folder, "0b7e2a8c-5f1d-4c1e-9a3e-1f2d3c4b5a69")

	first, addr := startUp(t, home, env)
	res, err := taskClient(t, addr).Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wish.GetId(), Title: "Long", Prompt: "text halfway\nsleep 1h", Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()
	waitTask(t, taskClient(t, addr), id, func(task *planv1.Task) bool {
		return task.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING && task.GetSessionId() != ""
	})
	first.stop(t, syscall.SIGKILL)

	second, addr := startUp(t, home, env)
	tasks := taskClient(t, addr)
	got := waitTask(t, tasks, id, func(task *planv1.Task) bool { return task.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE })
	if got.GetResumes() != 0 {
		t.Errorf("the task after the crash: %v\n%s", got, second.errs.String())
	}
	list, err := tasks.List(ctx, connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wish.GetId()}))
	if err != nil || len(list.Msg.GetTasks()) != 1 {
		t.Errorf("the wish's tasks after the crash: %v, %v; want the one resumed", list, err)
	}
}

// TestAnswer: a button of a notification answers its question through the server, guarded by
// its token like any client.
func TestAnswer(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	_, addr := up(t, home, environ(home, t.TempDir()))
	httpClient, base, err := cli.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	wish, err := planv1connect.NewWishServiceClient(httpClient, base).Make(ctx,
		connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Ship it"}))
	if err != nil {
		t.Fatal(err)
	}
	questions := planv1connect.NewQuestionServiceClient(httpClient, base)
	asked, err := questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		WishId: wish.Msg.GetWish().GetId(), Text: "Which one?", Options: []string{"this", "that"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := asked.Msg.GetQuestion().GetId()
	if err := answer(ctx, addr, id, planv1.Choice_CHOICE_B); err != nil {
		t.Fatal(err)
	}
	list, err := questions.List(ctx, connect.NewRequest(&planv1.QuestionServiceListRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := list.Msg.GetQuestions()[0].GetAnswer().GetChoice(); got != planv1.Choice_CHOICE_B {
		t.Errorf("answer = %v, want B", got)
	}
	// A choice the question does not offer is refused: the window shows the question instead.
	if err := answer(ctx, addr, id, planv1.Choice_CHOICE_D); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("answer D of two options: %v", err)
	}
}

// TestWorkerScopes: without systemd-run, djinn up says once that workers run in their process group, uncapped when
// caps or the memory guard were asked, and starts no scope.
func TestWorkerScopes(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, c := range []struct {
		name   string
		cpu    int
		memory uint64
		guard  float64
		want   string
	}{
		{"caps asked", 150, 1 << 30, 0, "workers run in their process group, uncapped\n"},
		{"no caps", 0, 0, 0, "workers run in their process group\n"},
		{"the memory guard", 0, 0, 2.5, "workers run in their process group, uncapped\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			if s := workerScopes(t.Context(), &out, c.cpu, c.memory, c.guard); s != nil {
				t.Errorf("scopes %+v without systemd-run", s)
			}
			if got := out.String(); strings.Count(got, "\n") != 1 || !strings.Contains(got, "no systemd scope per worker") ||
				!strings.HasSuffix(got, c.want) {
				t.Errorf("djinn up said %q, want it to end with %q", got, c.want)
			}
		})
	}
}

// TestOnOff: $DJINN_ANSWER_WORKERS, $DJINN_ENLIGHTEN_WORKERS, $DJINN_QUESTION_WORKERS read on or off, in any case,
// and what strconv.ParseBool reads; anything else is an error that says what it expects.
func TestOnOff(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		in   string
		want bool
		err  string
	}{
		{in: "on", want: true},
		{in: "OFF"},
		{in: " off "}, // trimmed
		{in: "true", want: true},
		{in: "0"},
		{in: "maybe", err: "expected on or off"},
	} {
		t.Run(c.in, func(t *testing.T) {
			got, err := onOff(c.in)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Errorf("onOff(%q): %v, want %q", c.in, err, c.err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Errorf("onOff(%q) = %v, %v; want %v", c.in, got, err, c.want)
			}
		})
	}
}

// TestIfritIdleFlag: djinn up --ifrit-idle takes whole minutes from 5m to 4h; none given leaves it to the settings.
func TestIfritIdleFlag(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		in   string
		want time.Duration // 0 with refused: the value is refused
		ok   bool
	}{
		{"", 0, true}, {"30m", 30 * time.Minute, true}, {"1h30m", 90 * time.Minute, true}, {"5m", 5 * time.Minute, true},
		{"4h", 4 * time.Hour, true},
		{"4m", 0, false}, {"4h1m", 0, false}, {"30", 0, false}, {"30m30s", 0, false}, {"half an hour", 0, false}, {"-30m", 0, false},
	} {
		t.Run(c.in, func(t *testing.T) {
			got, err := ifritIdleFlag(c.in)
			if !c.ok {
				if err == nil || !strings.Contains(err.Error(), "expected whole minutes from 5m to 4h") {
					t.Errorf("--ifrit-idle %q: %v, want refused", c.in, err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Errorf("--ifrit-idle %q = %v, %v; want %v", c.in, got, err, c.want)
			}
		})
	}
}
