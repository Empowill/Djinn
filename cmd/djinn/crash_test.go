//go:build !windows

// Not on Windows: these tests stop djinn up with SIGTERM and SIGKILL to play a crash, and Windows sends a process
// no signal but a kill.

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

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	terminalv1 "github.com/empowill/djinn/gen/go/terminal/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/cli"
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

// TestLeadNote covers leadNote, the note of the leads a running djinn up keeps for a crash: a djinn killed while its lead runs leaves the note of its leads, and the next djinn up
// reopens the lead on its session, in its folder, and shows it. A djinn stopped as asked removes the note: the next
// one reopens none of its terminals, only the lead of the first active wish, as at every start.
func TestLeadNote(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	terminalLead(t, home)
	bin, count := fakeClaude(t)
	env := environ(home, bin, "DJINN_TEST_COUNT="+count)
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const session = "0b7e2a8c-5f1d-4c1e-9a3e-1f2d3c4b5a69"
	wish := seedWish(t, home, folder, session)
	lead := "lead"
	notePath := filepath.Join(home, RestartFile)

	// A first djinn runs the lead: the note says so, and only the lead, not the shell of the window.
	first, addr := startUp(t, home, env)
	_, terminals := clients(t, addr)
	if _, err := terminals.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: "main"})); err != nil {
		t.Fatal(err)
	}
	if code, out, errs := runDjinn(t, env, "lead", "resume", "--wish-id", wish.GetId()); code != 0 {
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
		note.Terminals[0].Directory != folder || !strings.Contains(strings.Join(note.Terminals[0].Command, " "), "claude --resume "+session) {
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

	// Stopped as asked: the note goes. The next djinn reopens no terminal of the note, but the lead of the first
	// active wish comes back at every start, on its session, in its folder.
	second.stop(t, syscall.SIGTERM)
	if _, err := os.Stat(notePath); !os.IsNotExist(err) {
		t.Fatalf("the note after a quit: %v", err)
	}
	third, addr := startUp(t, home, env)
	_, terminals = clients(t, addr)
	waitUntil(t, "the first wish's lead after a quit", func() bool { return starts(t, count) == 3 })
	opened, err = terminals.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: lead}))
	if err != nil || !opened.Msg.GetAttached() {
		t.Fatalf("the lead after a quit: %v, %v\n%s", opened, err, third.errs.String())
	}
	read(t, terminals, opened.Msg.GetTerminal().GetId(), "fake-claude --resume "+session+" in "+folder)
	if errs := third.errs.String(); strings.Contains(errs, "reopened") ||
		!strings.Contains(errs, "the lead resumed in "+folder) {
		t.Fatalf("djinn up after a quit says:\n%s", errs)
	}
}

func taskClient(t *testing.T, addr string) planv1connect.TaskServiceClient {
	t.Helper()
	httpClient, base, err := cli.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	return planv1connect.NewTaskServiceClient(httpClient, base)
}

// waitTask waits until the task is as ok says, and returns it.
func waitTask(t *testing.T, c planv1connect.TaskServiceClient, id string, ok func(*planv1.Task) bool) *planv1.Task {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		res, err := c.Get(t.Context(), connect.NewRequest(&planv1.TaskServiceGetRequest{TaskId: id}))
		if err == nil && ok(res.Msg.GetTask()) {
			return res.Msg.GetTask()
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s: %v, %v", id, res, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitUntil waits up to 15 s for ok, and fails the test saying what it waited for.
func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); !ok(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}
