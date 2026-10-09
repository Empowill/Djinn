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
		updatePoll = updatePollForTests
		readMachine = calmMachine
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
	os.Exit(m.Run())
}

// calmMachine is this machine, its cores and memory, never under pressure. A djinn up of a test starts its workers
// whatever else the machine runs: a macOS runner of the CI, with no pressure stall information, reads a load of 21
// on 3 cores while the packages test in parallel, and its scheduler held a resumed worker past the test's wait.
// The pressure has its own tests (internal/machine, internal/gate).
func calmMachine() (machine.Snapshot, error) {
	s, err := machine.Read()
	return machine.Snapshot{Cores: s.Cores, MemoryTotal: s.MemoryTotal, MemoryAvailable: s.MemoryTotal}, err
}

// environ is the environment of a djinn of the test: its data directory (none when home is empty), a plain POSIX
// shell, and the fake agents of bin first on the PATH.
func environ(home, bin string, extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "DJINN_") && !strings.HasPrefix(kv, "PATH=") && !strings.HasPrefix(kv, "SHELL=") {
			env = append(env, kv)
		}
	}
	env = append(env, "DJINN_TEST_MAIN=1", "SHELL=/bin/sh", "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
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

func TestUpTwiceShowsTheFirst(t *testing.T) {
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

func TestUpReplacesTheAddressOfACrash(t *testing.T) {
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

func TestDevelopmentBuildKeepsItsOwnData(t *testing.T) {
	for _, c := range []struct{ version, dir string }{{"", "djinn-dev"}, {"local-1234abcd", "djinn"}} {
		t.Run(c.dir, func(t *testing.T) {
			user := t.TempDir()
			t.Setenv("HOME", user)
			t.Setenv("XDG_CONFIG_HOME", "")
			config, err := os.UserConfigDir()
			if err != nil {
				t.Fatal(err)
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

// fakeClaude writes a claude command in a new folder that says how it was called and where, counts its starts in
// the file count, then waits for its terminal to hang up.
func fakeClaude(t *testing.T) (bin, count string) {
	t.Helper()
	bin = t.TempDir()
	count = filepath.Join(t.TempDir(), "starts")
	script := "#!/bin/sh\necho \"fake-claude $* in $(pwd)\"\necho start >> \"$DJINN_TEST_COUNT\"\nexec cat\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, count
}

func TestWishResume(t *testing.T) {
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
	wish := &planv1.Wish{
		Id: store.NewID(), Title: "Polish the lamp", ProjectIds: []string{project.GetId()}, CreateTime: timestamppb.Now(),
		Lead: &planv1.Lead{Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: session, Directory: folder},
	}
	err = db.Tx(ctx, func(tx *store.Tx) error {
		return errors.Join(tx.Journal("test", planv1connect.WishServiceMakeProcedure, &planv1.WishServiceMakeRequest{Title: wish.GetTitle()}),
			tx.Put(project), tx.Put(wish))
	})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	// No djinn runs: resume starts one in the background, then resumes the lead in its terminal. The wish being the
	// first active one, djinn up may have resumed its lead at its start already: resume attaches to it then.
	code, out, errs := runDjinn(t, env, "wish", "resume", wish.GetId())
	if strings.Contains(errs, "(pid ") {
		stopStarted(t, home, errs)
	}
	name := "lead-" + wish.GetId()
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
	code, out, errs = runDjinn(t, env, "wish", "resume", wish.GetId())
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
	code, out, errs = runDjinn(t, env, "wish", "resume", wish.GetId())
	if code != 1 || !strings.Contains(errs, "already running in another terminal") || !strings.Contains(errs, `"main"`) {
		t.Fatalf("resume while main runs the session: exit %d\n%s%s", code, out, errs)
	}
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

// TestModuleVersion: a stamped version wins; without one, a test binary (built from the checkout) stays "dev".
func TestModuleVersion(t *testing.T) {
	if got := moduleVersion("local-abc"); got != "local-abc" {
		t.Errorf("moduleVersion(local-abc) = %q", got)
	}
	if got := moduleVersion("dev"); got != "dev" {
		t.Errorf("moduleVersion(dev) from a checkout = %q, want dev", got)
	}
}

// TestReadOnlyMethodsAnswerAGet calls a method that only reads as a GET on a real djinn up, the request in the
// query; a method that writes refuses the GET.
func TestReadOnlyMethodsAnswerAGet(t *testing.T) {
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

// TestWindowTerminalOpensInAProject: started without --terminal-dir, as from a menu, the window's terminal opens in
// the first project's folder, never in the home folder; with no project, it does not start, and says to create one.
func TestWindowTerminalOpensInAProject(t *testing.T) {
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

// TestWorkerScopesFallback: without systemd-run, djinn up says once that workers run in their process group,
// uncapped when caps were asked, and starts no scope.
func TestWorkerScopesFallback(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var out bytes.Buffer
	if s := workerScopes(t.Context(), &out, 150, 1<<30); s != nil {
		t.Errorf("scopes %+v without systemd-run", s)
	}
	if got := out.String(); strings.Count(got, "\n") != 1 || !strings.Contains(got, "no systemd scope per worker") ||
		!strings.HasSuffix(got, "workers run in their process group, uncapped\n") {
		t.Errorf("djinn up said %q", got)
	}
	out.Reset()
	workerScopes(t.Context(), &out, 0, 0)
	if got := out.String(); strings.Contains(got, "uncapped") || !strings.HasSuffix(got, "workers run in their process group\n") {
		t.Errorf("without caps, djinn up said %q", got)
	}
}
