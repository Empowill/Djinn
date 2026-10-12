//go:build !windows

// Not on Windows: the fake claude is a shell script, and the lead terminals run /bin/sh.

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	terminalv1 "github.com/empowill/djinn/gen/go/terminal/v1"
	"github.com/empowill/djinn/gen/go/terminal/v1/terminalv1connect"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// seedOldWishes writes, while no djinn runs, a project in folder and active wishes as a Djinn of one lead per wish
// (before a7d1f90) stored them: each with the claude session of its lead in the field 6 of Wish, reserved since,
// an empty session for none. No lead of Djinn is recorded.
func seedOldWishes(t *testing.T, home, folder string, sessions ...string) []*planv1.Wish {
	t.Helper()
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	project := &planv1.Project{Id: store.NewID(), Name: "lamp", Directory: folder, CreateTime: timestamppb.Now()}
	var wishes []*planv1.Wish
	for i, session := range sessions {
		w := &planv1.Wish{
			Id: store.NewID(), Title: "Polish the lamp", ProjectIds: []string{project.GetId()},
			CreateTime: timestamppb.Now(), State: planv1.WishState_WISH_STATE_ACTIVE, Rank: int32(i + 1),
		}
		if session != "" {
			b, err := proto.Marshal(&planv1.Lead{Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: session, Directory: folder})
			if err != nil {
				t.Fatal(err)
			}
			w.ProtoReflect().SetUnknown(protowire.AppendBytes(protowire.AppendTag(nil, 6, protowire.BytesType), b))
		}
		wishes = append(wishes, w)
	}
	err = db.Tx(ctx, func(tx *store.Tx) error {
		errs := []error{tx.Journal("test", planv1connect.WishServiceMakeProcedure,
			&planv1.WishServiceMakeRequest{Title: "Polish the lamp"}), tx.Put(project)}
		for _, w := range wishes {
			errs = append(errs, tx.Put(w))
		}
		return errors.Join(errs...)
	})
	if err != nil {
		t.Fatal(err)
	}
	return wishes
}

// writeNote writes the restart note an update leaves in home.
func writeNote(t *testing.T, home string, note restartNote) {
	t.Helper()
	b, err := json.Marshal(note)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, RestartFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// runningTerminals lists the terminals that run, by name, once the lead's terminal runs.
func runningTerminals(t *testing.T, c terminalv1connect.TerminalServiceClient) map[string]*terminalv1.Terminal {
	t.Helper()
	res, err := c.List(t.Context(), connect.NewRequest(&terminalv1.TerminalServiceListRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*terminalv1.Terminal{}
	for _, term := range res.Msg.GetTerminals() {
		if !term.GetExited() {
			out[term.GetName()] = term
		}
	}
	return out
}

// leadGet is what djinn lead get prints.
func leadGet(t *testing.T, env []string) string {
	t.Helper()
	code, out, errs := runDjinn(t, env, "lead", "get")
	if code != 0 {
		t.Fatalf("djinn lead get: exit %d\n%s%s", code, out, errs)
	}
	return out
}

// TestUpdateFromLeadsPerWish replays the incident of 11/10/2026: the first start after an update from a Djinn of one
// lead per wish. Its restart note names its leads' terminals lead-<wish id>, the store keeps each wish's lead, and no
// lead of Djinn is recorded. The first active wish's lead becomes the lead: one "lead" terminal resumes its session,
// with DJINN_LEAD, shown in the window; no terminal runs a wish's lead under another name, nor that session twice.
func TestUpdateFromLeadsPerWish(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	bin, count := fakeClaude(t)
	env := environ(home, bin, "DJINN_TEST_COUNT="+count)
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const first, second = "0b7e2a8c-5f1d-4c1e-9a3e-1f2d3c4b5a69", "7d2f4b1a-3c5e-4f60-8a9b-0c1d2e3f4a5b"
	wishes := seedOldWishes(t, home, folder, first, second)
	firstLead, secondLead := "lead-"+wishes[0].GetId(), "lead-"+wishes[1].GetId()
	writeNote(t, home, restartNote{
		Version: "local-b6016c5",
		Terminals: []restartTerminal{
			{Name: secondLead, Command: []string{"/bin/sh", "-c", "claude --resume " + second}, Directory: folder},
			{Name: firstLead, Command: []string{"/bin/sh", "-c", `echo "lead=$DJINN_LEAD"; claude --resume ` + first}, Directory: folder},
		},
		ShowWish: wishes[0].GetId(), ShowTerminal: firstLead,
	})

	r, addr := startUp(t, home, env)
	ui, terminals := clients(t, addr)
	opened, err := terminals.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: plan.LeadTerminal}))
	if err != nil || !opened.Msg.GetAttached() {
		t.Fatalf("the lead: %v, %v; want it running\n%s", opened, err, r.errs.String())
	}
	read(t, terminals, opened.Msg.GetTerminal().GetId(), "lead=1")
	read(t, terminals, opened.Msg.GetTerminal().GetId(), "fake-claude --resume "+first+" in "+folder)
	time.Sleep(300 * time.Millisecond)
	if n := starts(t, count); n != 1 {
		t.Fatalf("claude started %d times, want once\n%s", n, r.errs.String())
	}
	running := runningTerminals(t, terminals)
	if len(running) != 1 || running[plan.LeadTerminal] == nil {
		t.Fatalf("running terminals %v, want the lead's alone", running)
	}
	if got := leadGet(t, env); !strings.Contains(got, first) || !strings.Contains(got, folder) {
		t.Fatalf("djinn lead get:\n%s\nwant the first wish's lead", got)
	}
	errs := r.errs.String()
	if !strings.Contains(errs, secondLead+", a wish's lead, does not run again") ||
		!strings.Contains(errs, "restarted on local-b6016c5, 1 of 1 terminals resumed") ||
		strings.Contains(errs, "no lead session") {
		t.Fatalf("djinn up says:\n%s", errs)
	}
	shows, err := ui.WatchShow(ctx, connect.NewRequest(&uiv1.UiServiceWatchShowRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !shows.Receive() || shows.Msg().GetWishId() != wishes[0].GetId() || shows.Msg().GetTerminal() != plan.LeadTerminal {
		t.Fatalf("shown: %v, %v", shows.Msg(), shows.Err())
	}
	shows.Close()
}

// TestUpdateFromLeadsPerWishOtherSession: the note's terminal ran another session than the wish's lead recorded.
// It does not run again: the lead's terminal resumes the lead's session, and runs nothing else.
func TestUpdateFromLeadsPerWishOtherSession(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	bin, count := fakeClaude(t)
	env := environ(home, bin, "DJINN_TEST_COUNT="+count)
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const session = "0b7e2a8c-5f1d-4c1e-9a3e-1f2d3c4b5a69"
	wishes := seedOldWishes(t, home, folder, "", session)
	writeNote(t, home, restartNote{
		Version: "local-b6016c5",
		Terminals: []restartTerminal{{
			Name: "lead-" + wishes[0].GetId(), Command: []string{"/bin/sh", "-c", "claude --resume stray"}, Directory: folder,
		}},
		ShowWish: wishes[0].GetId(), ShowTerminal: "lead-" + wishes[0].GetId(),
	})

	r, addr := startUp(t, home, env)
	_, terminals := clients(t, addr)
	waitUntil(t, "the lead", func() bool { return starts(t, count) == 1 })
	opened, err := terminals.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: plan.LeadTerminal}))
	if err != nil || !opened.Msg.GetAttached() {
		t.Fatalf("the lead: %v, %v; want it running\n%s", opened, err, r.errs.String())
	}
	read(t, terminals, opened.Msg.GetTerminal().GetId(), "fake-claude --resume "+session+" in "+folder)
	time.Sleep(300 * time.Millisecond)
	if n := starts(t, count); n != 1 {
		t.Fatalf("claude started %d times, want once\n%s", n, r.errs.String())
	}
	if running := runningTerminals(t, terminals); len(running) != 1 || running[plan.LeadTerminal] == nil {
		t.Fatalf("running terminals %v, want the lead's alone", running)
	}
	if got := leadGet(t, env); !strings.Contains(got, session) {
		t.Fatalf("djinn lead get:\n%s\nwant the second wish's lead", got)
	}
}

// TestStartWithoutLead: a start with a project and no lead session at all starts a new claude lead from the brief
// in the lead's terminal, and records its session: never a shell in the lead's terminal.
func TestStartWithoutLead(t *testing.T) {
	home := t.TempDir()
	bin, count := fakeClaude(t)
	env := environ(home, bin, "DJINN_TEST_COUNT="+count)
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seedOldWishes(t, home, folder, "")

	r, addr := startUp(t, home, env)
	_, terminals := clients(t, addr)
	waitUntil(t, "the new lead", func() bool { return starts(t, count) == 1 })
	lead := runningTerminals(t, terminals)[plan.LeadTerminal]
	got := leadGet(t, env)
	line := strings.Join(lead.GetCommand(), " ")
	if lead == nil || lead.GetDirectory() != folder || !strings.Contains(line, "claude --session-id ") ||
		!strings.Contains(line, "djinn brief") {
		t.Fatalf("the lead's terminal: %v\n%s", lead, r.errs.String())
	}
	session := strings.Fields(line[strings.Index(line, "--session-id ")+len("--session-id "):])[0]
	if !strings.Contains(got, session) {
		t.Fatalf("djinn lead get:\n%s\nwant the new lead's session %s", got, session)
	}
	if errs := r.errs.String(); strings.Contains(errs, "no lead session") || !strings.Contains(errs, "A new lead started") {
		t.Fatalf("djinn up says:\n%s", errs)
	}
}
