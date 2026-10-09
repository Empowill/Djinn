//go:build !windows

// The pseudo-terminal tests drive /bin/sh, so they run on macOS and Linux; on Windows the terminal is checked by hand
// in the window (see the T21 task file).
package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	rawterm "golang.org/x/term"

	terminalv1 "github.com/empowill/djinn/gen/go/terminal/v1"
	"github.com/empowill/djinn/gen/go/terminal/v1/terminalv1connect"
)

// sh opens a terminal running /bin/sh in a temporary directory, closed at the end of the test.
func sh(t *testing.T, m *Manager, name string, args ...string) *Terminal {
	t.Helper()
	term, _, err := m.Open(name, append([]string{"/bin/sh"}, args...), t.TempDir(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return term
}

// output follows the output of term from offset from, and returns all of it once match finds what it looks for.
func output(t *testing.T, term *Terminal, from uint64, match func(string) bool) string {
	t.Helper()
	var out bytes.Buffer
	stop := make(chan struct{})
	read := make(chan bool)
	go func() {
		found := false
		_ = term.Read(from, stop, func(o Output) error {
			out.Write(o.Data)
			if found = match(out.String()); found {
				return errors.New("found")
			}
			return nil
		})
		read <- found
	}()
	timeout := time.AfterFunc(5*time.Second, func() { close(stop) })
	found := <-read
	timeout.Stop()
	if !found {
		t.Fatalf("not found in the output of the terminal:\n%q", out.String())
	}
	return out.String()
}

func contains(s string) func(string) bool {
	return func(out string) bool { return strings.Contains(out, s) }
}

// end waits for the program of term to end and returns its exit code.
func end(t *testing.T, term *Terminal) int {
	t.Helper()
	select {
	case <-term.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the program did not end")
	}
	_, _, exited, code := term.State()
	if !exited {
		t.Fatal("ended but not exited")
	}
	return code
}

func TestWriteThenRead(t *testing.T) {
	m := NewManager(Config{})
	term := sh(t, m, "main")
	// The quotes keep the typed line, which the terminal echoes, from matching.
	if err := term.Write([]byte("echo hel''lo; exit 3\n")); err != nil {
		t.Fatal(err)
	}
	output(t, term, 0, contains("hello"))
	if code := end(t, term); code != 3 {
		t.Fatalf("exit code %d, want 3", code)
	}
	// The whole output is still there, and the end comes last.
	var last Output
	if err := term.Read(0, nil, func(o Output) error { last = o; return nil }); err != nil {
		t.Fatal(err)
	}
	if !last.Exited || last.Code != 3 {
		t.Fatalf("last piece %+v, want the end with code 3", last)
	}
	if err := term.Write([]byte("x")); err != ErrExited {
		t.Fatalf("write after the end: %v, want ErrExited", err)
	}
}

func TestOpenAttachesToTheRunningTerminal(t *testing.T) {
	m := NewManager(Config{Command: []string{"/bin/sh"}, Dir: t.TempDir()})
	t.Cleanup(m.Close)
	first, attached, err := m.Open("main", nil, "", 0, 0)
	if err != nil || attached {
		t.Fatalf("first open: attached %v, %v", attached, err)
	}
	again, attached, err := m.Open("main", nil, "", 0, 0)
	if err != nil || !attached || again != first {
		t.Fatalf("second open: attached %v, same %v, %v", attached, again == first, err)
	}
	other, attached, err := m.Open("other", nil, "", 0, 0)
	if err != nil || attached || other == first {
		t.Fatalf("other name: attached %v, %v", attached, err)
	}
	// The defaults of the manager apply: the shell runs in its directory.
	if err := first.Write([]byte("pwd\n")); err != nil {
		t.Fatal(err)
	}
	output(t, first, 0, contains(m.cfg.Dir))
	// Once ended, the name starts a new program, and the old one is forgotten.
	if err := first.Write([]byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	end(t, first)
	next, attached, err := m.Open("main", nil, "", 0, 0)
	if err != nil || attached || next.ID == first.ID {
		t.Fatalf("open after the end: attached %v, new %v, %v", attached, next.ID != first.ID, err)
	}
	if _, err := m.Get(first.ID); err != ErrNotFound {
		t.Fatalf("get the ended terminal: %v, want ErrNotFound", err)
	}
}

func TestOpenExclusiveRefusesASessionRunningElsewhere(t *testing.T) {
	m := NewManager(Config{})
	t.Cleanup(m.Close)
	dir := t.TempDir()
	const session = "0b7e2a8c-5f1d-4c1e-9a3e-1f2d3c4b5a69"
	// The terminal of the window already resumes the session, as djinn up --terminal "claude --resume …" does.
	main, _, err := m.Open("main", []string{"/bin/sh", "-c", "exec cat # claude --resume " + session}, dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.OpenExclusive("lead-1", []string{"/bin/sh"}, dir, 0, 0, session); !errors.Is(err, ErrBusy) ||
		!strings.Contains(err.Error(), `"main"`) {
		t.Fatalf("open a second program on the session: %v, want ErrBusy naming main", err)
	}
	// Another session, or none, starts.
	lead, attached, err := m.OpenExclusive("lead-1", []string{"/bin/sh"}, dir, 0, 0, "another-session")
	if err != nil || attached {
		t.Fatalf("another session: attached %v, %v", attached, err)
	}
	// Its own name attaches, whatever runs elsewhere.
	if again, attached, err := m.OpenExclusive("lead-1", nil, "", 0, 0, session); err != nil || !attached || again != lead {
		t.Fatalf("attach to the lead: attached %v, %v", attached, err)
	}
	// Once the other program ended, the session is free.
	main.Hangup()
	end(t, main)
	if _, _, err := m.OpenExclusive("lead-2", []string{"/bin/sh"}, dir, 0, 0, session); err != nil {
		t.Fatalf("open once the session is free: %v", err)
	}
}

// TestChangedFollowsWhatRuns: Changed runs once a program starts and once it ends, when Running already says so; an
// Open that attaches changes nothing.
func TestChangedFollowsWhatRuns(t *testing.T) {
	calls := make(chan []string, 10)
	var m *Manager
	m = NewManager(Config{Changed: func() {
		var names []string
		for _, r := range m.Running() {
			names = append(names, r.Name)
		}
		calls <- names
	}})
	next := func() string {
		t.Helper()
		select {
		case names := <-calls:
			return strings.Join(names, ",")
		case <-time.After(5 * time.Second):
			t.Fatal("Changed did not run")
			return ""
		}
	}
	term := sh(t, m, "lead-a")
	if got := next(); got != "lead-a" {
		t.Fatalf("after the start: %q", got)
	}
	if _, attached, err := m.Open("lead-a", nil, "", 0, 0); err != nil || !attached {
		t.Fatalf("attach: %v, %v", attached, err)
	}
	if err := term.Write([]byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	if got := next(); got != "" {
		t.Fatalf("after the end: %q", got)
	}
	select {
	case names := <-calls:
		t.Fatalf("one more call: %q", names)
	default:
	}
}

func TestResize(t *testing.T) {
	m := NewManager(Config{})
	term, _, err := m.Open("main", []string{"/bin/sh"}, t.TempDir(), 120, 30)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	// stty prints rows then columns; the offsets keep each answer apart.
	if err := term.Write([]byte("stty size\n")); err != nil {
		t.Fatal(err)
	}
	out := output(t, term, 0, regexp.MustCompile(`(^|\s)30 120\r?\n`).MatchString)
	if err := term.Resize(100, 40); err != nil {
		t.Fatal(err)
	}
	if cols, rows, _, _ := term.State(); cols != 100 || rows != 40 {
		t.Fatalf("state %d×%d, want 100×40", cols, rows)
	}
	if err := term.Write([]byte("stty size\n")); err != nil {
		t.Fatal(err)
	}
	output(t, term, uint64(len(out)), regexp.MustCompile(`(^|\s)40 100\r?\n`).MatchString)
}

// TestReadResumes checks the scrollback alone: a read from an offset gets what follows it, and a read from an
// offset no longer kept starts at the oldest byte kept.
func TestReadResumes(t *testing.T) {
	term := &Terminal{changed: make(chan struct{}), screen: newScreen(80, 24)}
	var all []byte
	for i := range 40 {
		piece := bytes.Repeat([]byte{byte('a' + i%26)}, 50_000)
		all = append(all, piece...)
		term.append(piece)
	}
	read := func(from uint64) (first uint64, data []byte) {
		stop := make(chan struct{})
		close(stop) // Stop once all is sent: the program has not ended.
		first = ^uint64(0)
		if err := term.Read(from, stop, func(o Output) error {
			first = min(first, o.Offset)
			data = append(data, o.Data...)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return first, data
	}
	total := uint64(len(all))
	first, data := read(0)
	if kept := total - first; kept < Scrollback || kept > Scrollback+Scrollback/4 || first == 0 {
		t.Fatalf("kept %d bytes from %d, want the last mebibyte or a quarter more", kept, first)
	}
	if !bytes.Equal(data, all[first:]) {
		t.Fatal("the bytes kept are not the end of the output")
	}
	from := total - 1234
	if first, data := read(from); first != from || !bytes.Equal(data, all[from:]) {
		t.Fatalf("resume from %d: got %d bytes from %d", from, len(data), first)
	}
	if first, data := read(total + 10); len(data) != 0 {
		t.Fatalf("read past the end: %d bytes from %d", len(data), first)
	}
}

func TestCloseHangsUp(t *testing.T) {
	defer func(g time.Duration) { grace = g }(grace)
	grace = 300 * time.Millisecond
	m := NewManager(Config{})
	polite := sh(t, m, "polite", "-c", `trap 'exit 7' HUP; echo ready; while :; do sleep 0.05; done`)
	stubborn := sh(t, m, "stubborn", "-c", `trap '' HUP; echo ready; while :; do sleep 0.05; done`)
	output(t, polite, 0, contains("ready"))
	output(t, stubborn, 0, contains("ready"))
	start := time.Now()
	m.Close() // djinn up stopping.
	if code := end(t, polite); code != 7 {
		t.Fatalf("the program that handles SIGHUP ended with %d, want 7", code)
	}
	if code := end(t, stubborn); code != -1 {
		t.Fatalf("the program that ignores SIGHUP ended with %d, want -1 (killed)", code)
	}
	if took := time.Since(start); took < grace || took > grace+2*time.Second {
		t.Fatalf("closing took %v, want the grace delay %v and a little", took, grace)
	}
	if _, _, err := m.Open("again", nil, "", 0, 0); err != ErrClosed {
		t.Fatalf("open after close: %v, want ErrClosed", err)
	}
}

// TestHeldSpaceArrivesAsRepeats is what Claude Code's dictation needs in hold mode: a terminal sends no key release,
// so the program tells a held Space from the key repeat, a quick run of spaces. Each space written to the terminal,
// at a key repeat rate, must reach a program in raw mode on its own and at once, not batched.
func TestHeldSpaceArrivesAsRepeats(t *testing.T) {
	const repeats, every = 20, 30 * time.Millisecond // A usual key repeat: 30 a second.
	t.Setenv(helperEnv, "spaces")
	m := NewManager(Config{})
	t.Cleanup(m.Close)
	term, _, err := m.Open("main", []string{os.Args[0], "-test.run=^TestHelperProcess$"}, t.TempDir(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	out := output(t, term, 0, contains("ready"))
	for range repeats {
		if err := term.Write([]byte(" ")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(every)
	}
	out = output(t, term, uint64(len(out)), contains("done"))
	// One line a read: the byte count of the read and the milliseconds since the first.
	reads := regexp.MustCompile(`read (\d+) at (\d+)`).FindAllStringSubmatch(out, -1)
	if len(reads) != repeats {
		t.Fatalf("the program read %d times, want %d single spaces:\n%s", len(reads), repeats, out)
	}
	prev := -1
	for i, r := range reads {
		n, _ := strconv.Atoi(r[1])
		at, _ := strconv.Atoi(r[2])
		if n != 1 {
			t.Fatalf("read %d got %d bytes, want one space at a time", i, n)
		}
		if prev >= 0 && at-prev < int(every/time.Millisecond)/2 {
			t.Fatalf("read %d came %d ms after the previous one: batched, not as typed", i, at-prev)
		}
		prev = at
	}
	t.Logf("%d spaces in %d ms, one read each", repeats, prev)
}

const helperEnv = "DJINN_TERMINAL_HELPER"

// TestHelperProcess is the program the tests run in a terminal, not a test: with spaces, it puts its terminal in raw
// mode, as Claude Code does, and reports each read until it has read 20 spaces.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "spaces" {
		t.Skip("run by TestHeldSpaceArrivesAsRepeats")
	}
	state, err := rawterm.MakeRaw(0)
	if err != nil {
		fmt.Println("raw:", err)
		os.Exit(1)
	}
	defer rawterm.Restore(0, state) //nolint:errcheck
	fmt.Print("ready\r\n")
	var first time.Time
	b := make([]byte, 64)
	for count := 0; count < 20; {
		n, err := os.Stdin.Read(b)
		if err != nil {
			os.Exit(1)
		}
		if first.IsZero() {
			first = time.Now()
		}
		count += bytes.Count(b[:n], []byte(" "))
		fmt.Printf("read %d at %d\r\n", n, time.Since(first).Milliseconds())
	}
	fmt.Print("done\r\n")
	rawterm.Restore(0, state) //nolint:errcheck
	os.Exit(0)
}

// TestService drives the terminal through Connect, as the window does: open, write, read the stream, close.
func TestService(t *testing.T) {
	m := NewManager(Config{Command: []string{"/bin/sh"}, Dir: t.TempDir()})
	t.Cleanup(m.Close)
	srv := httptest.NewServer(func() *http.ServeMux {
		mux := http.NewServeMux()
		mux.Handle(Handler(m))
		return mux
	}())
	t.Cleanup(srv.Close)
	c := terminalv1connect.NewTerminalServiceClient(srv.Client(), srv.URL)
	ctx := t.Context()
	open, err := c.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: "main", Cols: 90, Rows: 20}))
	if err != nil {
		t.Fatal(err)
	}
	got := open.Msg.GetTerminal()
	if got.GetCols() != 90 || got.GetRows() != 20 || got.GetCommand()[0] != "/bin/sh" || open.Msg.GetAttached() {
		t.Fatalf("opened %v", open.Msg)
	}
	id := got.GetId()
	if _, err := c.Write(ctx, connect.NewRequest(&terminalv1.TerminalServiceWriteRequest{
		Id: id, Data: []byte("echo hel''lo\n"),
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Resize(ctx, connect.NewRequest(&terminalv1.TerminalServiceResizeRequest{Id: id})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("resize to 0×0: %v, want invalid argument", err)
	}
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stream, err := c.Read(readCtx, connect.NewRequest(&terminalv1.TerminalServiceReadRequest{Id: id}))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for !strings.Contains(out.String(), "hello") && stream.Receive() {
		out.Write(stream.Msg().GetData())
	}
	if !strings.Contains(out.String(), "hello") {
		t.Fatalf("no hello in %q: %v", out.String(), stream.Err())
	}
	if _, err := c.Close(ctx, connect.NewRequest(&terminalv1.TerminalServiceCloseRequest{Id: id})); err != nil {
		t.Fatal(err)
	}
	// The stream ends with the program: an interactive shell exits on SIGHUP.
	exited := false
	for stream.Receive() {
		exited = stream.Msg().GetExited()
	}
	if !exited {
		t.Fatalf("the stream ended without the end of the program: %v", stream.Err())
	}
	if _, err := c.Write(ctx, connect.NewRequest(&terminalv1.TerminalServiceWriteRequest{Id: "nope"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("write to an unknown terminal: %v, want not found", err)
	}
}

// TestServiceRunsALine runs a command line through the user's shell, pipes and all, as the agents' setup does; a
// line and a command together are refused.
func TestServiceRunsALine(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	m := NewManager(Config{Dir: t.TempDir()})
	t.Cleanup(m.Close)
	srv := httptest.NewServer(func() *http.ServeMux {
		mux := http.NewServeMux()
		mux.Handle(Handler(m))
		return mux
	}())
	t.Cleanup(srv.Close)
	c := terminalv1connect.NewTerminalServiceClient(srv.Client(), srv.URL)
	ctx := t.Context()
	if _, err := c.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{
		Name: "both", Command: []string{"/bin/sh"}, Line: "true",
	})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a command and a line: %v, want invalid argument", err)
	}
	open, err := c.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{
		Name: "setup", Line: "echo set''up | tr s S; exit 3",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := open.Msg.GetTerminal().GetCommand(); got[0] != "/bin/sh" || got[len(got)-1] != "echo set''up | tr s S; exit 3" {
		t.Fatalf("command %q", got)
	}
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stream, err := c.Read(readCtx, connect.NewRequest(&terminalv1.TerminalServiceReadRequest{Id: open.Msg.GetTerminal().GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := int32(0)
	for stream.Receive() {
		out.Write(stream.Msg().GetData())
		if stream.Msg().GetExited() {
			code = stream.Msg().GetExitCode()
		}
	}
	if !strings.Contains(out.String(), "Setup") || code != 3 {
		t.Fatalf("output %q, exit %d: %v", out.String(), code, stream.Err())
	}
}
