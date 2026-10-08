//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"

	terminalv1 "github.com/empowill/djinn/gen/go/terminal/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
)

// running is a djinn up a test started, which it stops by its PID.
type running struct {
	cmd    *exec.Cmd
	errs   *buffer
	exited chan struct{}
}

// startUp starts djinn up in env, waits until it answers in home, and returns it with its address. The end of the
// test stops it, if it still runs.
func startUp(t *testing.T, home string, env []string) (*running, string) {
	t.Helper()
	r := &running{cmd: exec.Command(os.Args[0], "up"), errs: &buffer{}, exited: make(chan struct{})}
	r.cmd.Env = env
	r.cmd.Stdout, r.cmd.Stderr = &buffer{}, r.errs
	if err := r.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = r.cmd.Wait(); close(r.exited) }()
	t.Cleanup(func() { r.stop(t, syscall.SIGINT) })
	return r, answering(t, home, r.errs.String)
}

// stop sends sig to the djinn, and waits until it exits; SIGKILL after 15 s.
func (r *running) stop(t *testing.T, sig syscall.Signal) {
	t.Helper()
	select {
	case <-r.exited:
		return
	default:
	}
	_ = r.cmd.Process.Signal(sig)
	select {
	case <-r.exited:
	case <-time.After(15 * time.Second):
		_ = r.cmd.Process.Kill()
		<-r.exited
		t.Errorf("djinn did not stop on %v:\n%s", sig, r.errs.String())
	}
}

// starts counts the starts of the fake claude.
func starts(t *testing.T, count string) int {
	t.Helper()
	b, _ := os.ReadFile(count)
	return strings.Count(string(b), "start")
}

// TestCrashReopensTheLeads: a djinn killed while its lead runs leaves the note of its leads, and the next djinn up
// reopens the lead on its session, in its folder, and shows it. A djinn stopped as asked removes the note: the next
// one reopens nothing.
func TestCrashReopensTheLeads(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	bin, count := fakeClaude(t)
	env := environ(home, bin, "DJINN_TEST_COUNT="+count)
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const session = "0b7e2a8c-5f1d-4c1e-9a3e-1f2d3c4b5a69"
	wish := seedWish(t, home, folder, session)
	lead := "lead-" + wish.GetId()
	notePath := filepath.Join(home, RestartFile)

	// A first djinn runs the lead: the note says so, and only the lead, not the shell of the window.
	first, addr := startUp(t, home, env)
	_, terminals := clients(t, addr)
	if _, err := terminals.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: "main"})); err != nil {
		t.Fatal(err)
	}
	if code, out, errs := runDjinn(t, env, "wish", "resume", wish.GetId()); code != 0 {
		t.Fatalf("resume: exit %d\n%s%s", code, out, errs)
	}
	opened, err := terminals.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: lead}))
	if err != nil || !opened.Msg.GetAttached() {
		t.Fatalf("the lead: %v, %v; want it running", opened, err)
	}
	read(t, terminals, opened.Msg.GetTerminal().GetId(), "fake-claude --resume "+session+" in "+folder)
	var note restartNote
	if b, err := os.ReadFile(notePath); err != nil {
		t.Fatalf("no note of the leads while the lead runs: %v", err)
	} else if err := json.Unmarshal(b, &note); err != nil {
		t.Fatal(err)
	}
	if note.Version != "" || len(note.Terminals) != 1 || note.Terminals[0].Name != lead ||
		note.Terminals[0].Directory != folder || !strings.HasSuffix(strings.Join(note.Terminals[0].Command, " "), "claude --resume "+session) {
		t.Fatalf("the note of the leads: %+v", note)
	}

	// It crashes: nothing of it runs, and the note stays.
	first.stop(t, syscall.SIGKILL)
	if _, err := os.Stat(notePath); err != nil {
		t.Fatalf("the note after a crash: %v", err)
	}

	// The next djinn reopens the lead, on its session and in its folder, and shows it.
	second, addr := startUp(t, home, env)
	ui, terminals := clients(t, addr)
	opened, err = terminals.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: lead}))
	if err != nil || !opened.Msg.GetAttached() {
		t.Fatalf("the lead after the crash: %v, %v; want it reopened\n%s", opened, err, second.errs.String())
	}
	read(t, terminals, opened.Msg.GetTerminal().GetId(), "fake-claude --resume "+session+" in "+folder)
	if n := starts(t, count); n != 2 {
		t.Fatalf("claude started %d times, want twice", n)
	}
	if !strings.Contains(second.errs.String(), "the last djinn up crashed, 1 of 1 leads reopened") {
		t.Fatalf("djinn up after the crash says:\n%s", second.errs.String())
	}
	shows, err := ui.WatchShow(ctx, connect.NewRequest(&uiv1.UiServiceWatchShowRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !shows.Receive() || shows.Msg().GetWishId() != wish.GetId() || shows.Msg().GetTerminal() != lead {
		t.Fatalf("shown after the crash: %v, %v", shows.Msg(), shows.Err())
	}
	shows.Close()
	if _, err := os.Stat(notePath); err != nil {
		t.Fatalf("no note of the reopened lead: %v", err)
	}

	// Stopped as asked: the note goes, and the next djinn reopens nothing.
	second.stop(t, syscall.SIGTERM)
	if _, err := os.Stat(notePath); !os.IsNotExist(err) {
		t.Fatalf("the note after a quit: %v", err)
	}
	third, _ := startUp(t, home, env)
	time.Sleep(500 * time.Millisecond)
	if n := starts(t, count); n != 2 {
		t.Fatalf("claude started %d times after a quit, want twice", n)
	}
	if strings.Contains(third.errs.String(), "reopened") {
		t.Fatalf("djinn up after a quit reopened leads:\n%s", third.errs.String())
	}
}
