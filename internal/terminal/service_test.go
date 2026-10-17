//go:build !windows

package terminal

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	terminalv1 "github.com/empowill/djinn/gen/go/terminal/v1"
	"github.com/empowill/djinn/gen/go/terminal/v1/terminalv1connect"
)

// serve serves m through Connect, as djinn up does, and returns a client of it and the test's context.
func serve(t *testing.T, m *Manager) (terminalv1connect.TerminalServiceClient, context.Context) {
	t.Helper()
	srv := httptest.NewServer(func() *http.ServeMux {
		mux := http.NewServeMux()
		mux.Handle(Handler(m))
		return mux
	}())
	t.Cleanup(srv.Close)
	return terminalv1connect.NewTerminalServiceClient(srv.Client(), srv.URL), t.Context()
}

// TestService drives the terminals through Connect, as the window does.
func TestService(t *testing.T) {
	t.Parallel()
	t.Run("open write read close", func(t *testing.T) {
		t.Parallel()
		m := NewManager(Config{Command: []string{"/bin/sh"}, Dir: t.TempDir()})
		t.Cleanup(m.Close)
		c, ctx := serve(t, m)
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
		// Attached again, it says how far its output has come: the window answers no query below.
		again, err := c.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: "main"}))
		if err != nil {
			t.Fatal(err)
		}
		if !again.Msg.GetAttached() || again.Msg.GetEndOffset() < uint64(out.Len()) {
			t.Fatalf("open again: %v, read %d bytes", again.Msg, out.Len())
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
	})
	// List gives the terminals whose program runs, by name, without starting any: the window shows a wish's lead
	// only while it runs.
	t.Run("list", func(t *testing.T) {
		t.Parallel()
		m := NewManager(Config{Command: []string{"/bin/sh"}, Dir: t.TempDir()})
		t.Cleanup(m.Close)
		c, ctx := serve(t, m)
		names := func() []string {
			t.Helper()
			res, err := c.List(ctx, connect.NewRequest(&terminalv1.TerminalServiceListRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, term := range res.Msg.GetTerminals() {
				out = append(out, term.GetName())
			}
			return out
		}
		if got := names(); len(got) != 0 || len(m.Running()) != 0 {
			t.Fatalf("listed %v before any terminal, or started one", got)
		}
		sh(t, m, "main")
		lead := sh(t, m, "lead-w1", "-c", "exit 3")
		end(t, lead)
		if got := names(); len(got) != 1 || got[0] != "main" {
			t.Fatalf("listed %v, want only main: an ended lead is not running", got)
		}
		sh(t, m, "lead-w2")
		if got := strings.Join(names(), " "); got != "lead-w2 main" {
			t.Fatalf("listed %q, want lead-w2 main", got)
		}
	})
	// A command line runs through the user's shell, pipes and all, as the agents' setup does; a line and a command
	// together are refused.
	t.Run("runs a line", func(t *testing.T) {
		t.Parallel()
		m := NewManager(Config{Dir: t.TempDir()})
		t.Cleanup(m.Close)
		c, ctx := serve(t, m)
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
	})
	// The window attaches to the lead's terminal while the lead runs, and is refused the default shell under that
	// name, with DJINN_LEAD=1: a line Djinn types to the lead would run there as a command. A command the window names
	// (a restart) still runs there, and a terminal of another name gets the shell.
	t.Run("never opens a shell for a lead", func(t *testing.T) {
		t.Parallel()
		m := NewManager(Config{Command: []string{"/bin/sh"}, Dir: t.TempDir()})
		t.Cleanup(m.Close)
		c, ctx := serve(t, m)
		refused := func(when string) {
			t.Helper()
			_, err := c.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: "lead", Cols: 80, Rows: 24}))
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("open the lead's terminal %s: %v, want failed precondition", when, err)
			}
			if m.Lookup("lead") != nil {
				t.Fatalf("a shell started under the lead's name %s", when)
			}
		}
		refused("that runs nothing")

		lead := sh(t, m, "lead", "-c", "echo lead here; exec cat")
		output(t, lead, 0, contains("lead here"))
		open, err := c.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: "lead", Cols: 90, Rows: 20}))
		if err != nil {
			t.Fatal(err)
		}
		if got := open.Msg.GetTerminal(); got.GetId() != lead.ID || !open.Msg.GetAttached() || got.GetCols() != 90 ||
			open.Msg.GetEndOffset() == 0 {
			t.Fatalf("opened %v, want attached to %s at 90 columns, with the end of what it wrote", open.Msg, lead.ID)
		}

		// The lead ended: still no shell under its name, the window attaches to the ended lead.
		lead.Hangup()
		select {
		case <-lead.Done():
		case <-time.After(10 * time.Second):
			t.Fatal("the lead did not end")
		}
		ended, err := c.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: "lead", Cols: 80, Rows: 24}))
		if err != nil || ended.Msg.GetTerminal().GetId() != lead.ID || !ended.Msg.GetTerminal().GetExited() {
			t.Fatalf("open the lead's terminal whose lead ended: %v, %v, want the ended lead", ended.Msg, err)
		}
		if m.Lookup("lead") != nil {
			t.Fatal("a shell started under the lead's name whose lead ended")
		}

		// A command the window names runs there; another terminal gets the shell.
		again, err := c.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{
			Name: "lead", Command: []string{"/bin/sh", "-c", "exec cat"}, Cols: 80, Rows: 24,
		}))
		if err != nil || again.Msg.GetAttached() || again.Msg.GetTerminal().GetCommand()[0] != "/bin/sh" {
			t.Fatalf("restart the lead's command: %v, %v", again.Msg, err)
		}
		shell, err := c.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: "main", Cols: 80, Rows: 24}))
		if err != nil || shell.Msg.GetAttached() {
			t.Fatalf("open the window's shell: %v, %v", shell.Msg, err)
		}
	})
	// The lead's last terminal is kept for the window after a fast exit: its output can still be read, and opening it
	// starts nothing in its place.
	t.Run("attaches to an exited lead", func(t *testing.T) {
		t.Parallel()
		m := NewManager(Config{Command: []string{"/bin/sh"}, Dir: t.TempDir()})
		t.Cleanup(m.Close)
		c, ctx := serve(t, m)
		lead, attached, err := m.Open("lead", []string{"/bin/sh", "-c", "printf 'retained\\n'; exit 7"}, t.TempDir(), 80, 24)
		if err != nil || attached {
			t.Fatalf("start lead: attached %v, %v", attached, err)
		}
		if code := end(t, lead); code != 7 {
			t.Fatalf("lead exit code %d, want 7", code)
		}
		if m.Latest("lead") != lead {
			t.Fatal("Latest lost the ended lead")
		}

		open, err := c.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{
			Name: "lead", Cols: 90, Rows: 20,
		}))
		if err != nil {
			t.Fatal(err)
		}
		got := open.Msg.GetTerminal()
		if got.GetId() != lead.ID || !open.Msg.GetAttached() || !got.GetExited() || got.GetExitCode() != 7 {
			t.Fatalf("opened %v, want attached to exited terminal %s", open.Msg, lead.ID)
		}
		if m.Lookup("lead") != nil {
			t.Fatal("opening an exited lead restarted it")
		}

		stream, err := c.Read(ctx, connect.NewRequest(&terminalv1.TerminalServiceReadRequest{Id: got.GetId()}))
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		var code int32
		var exited bool
		for stream.Receive() {
			out.Write(stream.Msg().GetData())
			if stream.Msg().GetExited() {
				exited, code = true, stream.Msg().GetExitCode()
			}
		}
		if stream.Err() != nil || !strings.Contains(out.String(), "retained") || !exited || code != 7 {
			t.Fatalf("read output %q, exited %v, code %d: %v", out.String(), exited, code, stream.Err())
		}
	})
}
