//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// install puts a copy of this test binary at path, as task install does: built apart, then renamed over the
// installed one. Its version is in a file next to it, which TestMain reads.
func install(t *testing.T, path, version string) {
	t.Helper()
	b, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(filepath.Dir(path), ".djinn-new")
	if err := os.WriteFile(fresh, b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".version", []byte(version), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(fresh, path); err != nil {
		t.Fatal(err)
	}
}

// seedWish writes a wish whose lead runs session in folder, while no djinn runs.
func seedWish(t *testing.T, home, folder, session string) *planv1.Wish {
	t.Helper()
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	project := &planv1.Project{Id: store.NewID(), Name: "lamp", Directory: folder, CreateTime: timestamppb.Now()}
	wish := &planv1.Wish{
		Id: store.NewID(), Title: "Polish the lamp", ProjectIds: []string{project.GetId()}, CreateTime: timestamppb.Now(),
		Lead: &planv1.Lead{Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: session, Directory: folder},
	}
	err = db.Tx(ctx, func(tx *store.Tx) error {
		return errors.Join(tx.Journal("test", planv1connect.WishServiceMakeProcedure,
			&planv1.WishServiceMakeRequest{Title: wish.GetTitle()}), tx.Put(project), tx.Put(wish))
	})
	if err != nil {
		t.Fatal(err)
	}
	return wish
}

// clients dials the djinn at addr.
func clients(t *testing.T, addr string) (uiv1connect.UiServiceClient, terminalv1connect.TerminalServiceClient) {
	t.Helper()
	httpClient, base, err := cli.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	return uiv1connect.NewUiServiceClient(httpClient, base), terminalv1connect.NewTerminalServiceClient(httpClient, base)
}

// TestUpdate installs a newer djinn while one runs a lead, checks that nothing restarts until asked, then runs
// djinn update: the new djinn runs the lead again on its session, in its folder, and reports the terminal it could not.
func TestUpdate(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	bin, _ := fakeClaude(t)
	env := environ(home, bin, "DJINN_TEST_COUNT="+filepath.Join(t.TempDir(), "starts"))
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const session = "0b7e2a8c-5f1d-4c1e-9a3e-1f2d3c4b5a69"
	wish := seedWish(t, home, folder, session)
	djinnPath := filepath.Join(t.TempDir(), "djinn")
	install(t, djinnPath, "v1")

	// Djinn v1 runs from the installed path; the test stops it by its PID.
	old := exec.Command(djinnPath, "up")
	old.Env = env
	oldErrs := &buffer{}
	old.Stdout, old.Stderr = &buffer{}, oldErrs
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = old.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = old.Process.Signal(syscall.SIGINT)
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			_ = old.Process.Kill()
		}
	})
	addr := answering(t, home, oldErrs.String)

	// The lead runs in its terminal; a second terminal runs in a folder that will be gone at the restart.
	code, out, errs := runDjinn(t, env, "wish", "resume", wish.GetId())
	if code != 0 {
		t.Fatalf("resume: exit %d\n%s%s", code, out, errs)
	}
	ui, terminals := clients(t, addr)
	lead := "lead-" + wish.GetId()
	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := terminals.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{
		Name: "main", Command: []string{"/bin/sh", "-c", "claude --resume another"}, Directory: gone,
	})); err != nil {
		t.Fatal(err)
	}

	watch, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	updates, err := ui.WatchUpdate(watch, connect.NewRequest(&uiv1.UiServiceWatchUpdateRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !updates.Receive() || updates.Msg().GetCurrent() != "v1" || updates.Msg().GetReady() != "" {
		t.Fatalf("before any install: %v, %v", updates.Msg(), updates.Err())
	}
	// Nothing newer: an update is refused, and djinn keeps running.
	if _, err := ui.Update(ctx, connect.NewRequest(&uiv1.UiServiceUpdateRequest{})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("update with nothing newer: %v", err)
	}

	// The install renames a newer djinn over the running one's file: v1 sees it, says so, and keeps running.
	install(t, djinnPath, "v2")
	for updates.Msg().GetReady() != "v2" {
		if !updates.Receive() {
			t.Fatalf("v2 never offered: %v", updates.Err())
		}
	}
	time.Sleep(5 * updatePollForTests)
	select {
	case <-exited:
		t.Fatalf("djinn restarted without being asked:\n%s", oldErrs.String())
	default:
	}
	if opened, err := terminals.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: lead})); err != nil ||
		!opened.Msg.GetAttached() {
		t.Fatalf("the lead after the install: %v, %v; want it still running", opened, err)
	}
	updates.Close()

	// djinn update: v1 stops, v2 starts and runs the lead again; the terminal of the folder gone is reported.
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	code, out, errs = runDjinn(t, env, "update", "--yes")
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		t.Fatalf("v1 still runs after djinn update:\n%s", oldErrs.String())
	}
	stopStarted(t, home, oldErrs.String())
	t.Logf("djinn update: exit %d\n%s%s", code, out, errs)
	if code != 1 || !strings.Contains(out, "restarting on v2, with 2 terminals") || !strings.Contains(out, "now running v2") ||
		!strings.Contains(out, "- main: ") || !strings.Contains(errs, "some terminals did not start again") {
		t.Fatalf("djinn update: exit %d\n%s%s\nv1:\n%s", code, out, errs, oldErrs.String())
	}

	addr = answering(t, home, func() string { b, _ := os.ReadFile(filepath.Join(home, LogFile)); return string(b) })
	ui, terminals = clients(t, addr)
	opened, err := terminals.Open(ctx, connect.NewRequest(&terminalv1.TerminalServiceOpenRequest{Name: lead}))
	if err != nil || !opened.Msg.GetAttached() {
		t.Fatalf("the lead after the update: %v, %v; want it running again", opened, err)
	}
	read(t, terminals, opened.Msg.GetTerminal().GetId(), "fake-claude --resume "+session+" in "+folder)
	updates, err = ui.WatchUpdate(ctx, connect.NewRequest(&uiv1.UiServiceWatchUpdateRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !updates.Receive() || updates.Msg().GetCurrent() != "v2" || updates.Msg().GetReady() != "" ||
		len(updates.Msg().GetNotResumed()) != 1 || !strings.HasPrefix(updates.Msg().GetNotResumed()[0], "main: ") {
		t.Fatalf("v2: %v, %v", updates.Msg(), updates.Err())
	}
	updates.Close()
	// The new window shows the wish and its lead, as the old one did.
	shows, err := ui.WatchShow(ctx, connect.NewRequest(&uiv1.UiServiceWatchShowRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !shows.Receive() || shows.Msg().GetWishId() != wish.GetId() || shows.Msg().GetTerminal() != lead {
		t.Fatalf("shown after the update: %v, %v", shows.Msg(), shows.Err())
	}
	shows.Close()
	if _, err := os.Stat(filepath.Join(home, RestartFile)); !os.IsNotExist(err) {
		t.Fatalf("the restart note is still there: %v", err)
	}
}

// updatePollForTests is updatePoll in the djinn the tests run (TestMain).
const updatePollForTests = 100 * time.Millisecond

// TestUpdateWithoutDjinn says that no djinn runs, and starts none.
func TestUpdateWithoutDjinn(t *testing.T) {
	home := t.TempDir()
	code, out, errs := runDjinn(t, environ(home, t.TempDir()), "update", "--yes")
	if code != 1 || !strings.Contains(errs, "no djinn runs") {
		t.Fatalf("djinn update with no djinn: exit %d\n%s%s", code, out, errs)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("djinn update wrote in the data directory: %v", entries)
	}
}

// TestUpdateNeedsAPerson: from a shell that is not a terminal, as an agent's, djinn update refuses without --yes.
func TestUpdateNeedsAPerson(t *testing.T) {
	code, out, errs := runDjinn(t, environ(t.TempDir(), t.TempDir()), "update")
	if code != 1 || !strings.Contains(errs, "--yes") {
		t.Fatalf("djinn update without a terminal: exit %d\n%s%s", code, out, errs)
	}
}
