//go:build !windows

// Not on Windows: the restarted djinn is stopped with SIGINT, and the lead terminals run /bin/sh.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	"github.com/empowill/djinn/internal/harness"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/terminal"
	"github.com/empowill/djinn/internal/ui"
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
	leadRecord := &planv1.Lead{Id: "lead", Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: session, Directory: folder}
	wish := &planv1.Wish{
		Id: store.NewID(), Title: "Polish the lamp", ProjectIds: []string{project.GetId()}, CreateTime: timestamppb.Now(),
	}
	err = db.Tx(ctx, func(tx *store.Tx) error {
		return errors.Join(tx.Journal("test", planv1connect.WishServiceMakeProcedure,
			&planv1.WishServiceMakeRequest{Title: wish.GetTitle()}), tx.Put(project), tx.Put(wish), tx.Put(leadRecord))
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

const (
	// updatePollForTests is updatePoll in the djinn the tests run (TestMain).
	updatePollForTests = 20 * time.Millisecond
	// restartDelayForTests is restartDelay there: the server's shutdown lets the response leave, on loopback.
	restartDelayForTests = 50 * time.Millisecond
)

// releaseCheckForTests is releaseCheck in a djinn of the tests that serves a fake release (TestMain).
const releaseCheckForTests = 30 * time.Millisecond

// trailerMark ends a copy of the test binary with the version it plays: two copies of one binary, two versions.
const trailerMark = "\ndjinn-test-version="

// withVersions are copies of the test binary, each playing one of versions.
func withVersions(t *testing.T, versions ...string) [][]byte {
	t.Helper()
	b, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	out := make([][]byte, len(versions))
	for i, v := range versions {
		out[i] = append(b[:len(b):len(b)], trailerMark+v+"\n"...)
	}
	return out
}

// trailerVersion is the version a copy of the test binary plays; empty for the test binary itself.
func trailerVersion(exe string) string {
	f, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil || end < 64 {
		return ""
	}
	tail := make([]byte, 64)
	if _, err := f.ReadAt(tail, end-64); err != nil {
		return ""
	}
	i := bytes.LastIndex(tail, []byte(trailerMark))
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(string(tail[i+len(trailerMark):]))
}

// TestRunUpdate covers runUpdate, djinn update, on processes of this test binary: a newer build installed beside the
// running djinn, a newer release, no djinn running, no person to say yes. The cases run in parallel; each starts one
// djinn up that restarts into another, a few seconds here.
func TestRunUpdate(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		run  func(t *testing.T)
	}{
		{"a newer build installed", updateInstalled},
		{"a newer release", updateFromRelease},
		{"no djinn runs", updateWithoutDjinn},
		{"no person", updateNeedsAPerson},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t)
		})
	}
}

// updateInstalled installs a newer djinn while one runs a lead, checks that nothing restarts until asked, then runs
// djinn update: the new djinn runs the lead again on its session, in its folder, and reports the terminal it could not.
func updateInstalled(t *testing.T) {
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
	code, out, errs := runDjinn(t, env, "lead", "resume", "--wish-id", wish.GetId())
	if code != 0 {
		t.Fatalf("resume: exit %d\n%s%s", code, out, errs)
	}
	ui, terminals := clients(t, addr)
	lead := "lead"
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
	// The note of the update is gone: in its place, the note of the lead that runs again, for a crash.
	b, err := os.ReadFile(filepath.Join(home, RestartFile))
	if err != nil {
		t.Fatalf("no note of the leads after the update: %v", err)
	}
	var note restartNote
	if err := json.Unmarshal(b, &note); err != nil || note.Version != "" || len(note.Terminals) != 1 ||
		note.Terminals[0].Name != lead {
		t.Fatalf("the note after the update: %+v, %v", note, err)
	}
}

// updateFromRelease runs a Djinn installed from a release while a fake release is served: it offers nothing while
// the release is its own version, offers a newer one with a link to its notes without downloading it, refuses it on a
// click while its sum is wrong, then on djinn update downloads it, swaps it in and restarts on it.
func updateFromRelease(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	fake := newFakeRelease(t)
	vs := withVersions(t, "v1.0.0", "v2.0.0")
	v1, v2 := vs[0], vs[1]
	dir := t.TempDir()
	djinnPath := filepath.Join(dir, "djinn")
	if err := os.WriteFile(djinnPath, v1, 0o755); err != nil {
		t.Fatal(err)
	}
	fake.publish(t, "v1.0.0", "djinn_test", v1, nil)
	env := environ(home, t.TempDir(), "DJINN_TEST_RELEASE_API="+fake.srv.URL, "DJINN_TEST_RELEASE_ASSET=djinn_test")

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
	ui, _ := clients(t, answering(t, home, oldErrs.String))
	alive := func(when string) {
		t.Helper()
		select {
		case <-exited:
			t.Fatalf("%s: djinn stopped:\n%s", when, oldErrs.String())
		default:
		}
		if b, err := os.ReadFile(djinnPath); err != nil || !bytes.Equal(b, v1) {
			t.Fatalf("%s: the installed binary changed (%v)", when, err)
		}
	}
	// waitChecks waits until djinn looked at the release n more times.
	waitChecks := func(n int) {
		t.Helper()
		from, _ := fake.counts()
		deadline := time.Now().Add(20 * time.Second)
		for checks, _ := fake.counts(); checks < from+n; checks, _ = fake.counts() {
			if time.Now().After(deadline) {
				t.Fatalf("djinn looked at the release %d times, want %d more:\n%s", checks-from, n, oldErrs.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	watch, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	updates, err := ui.WatchUpdate(watch, connect.NewRequest(&uiv1.UiServiceWatchUpdateRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	defer updates.Close()
	if !updates.Receive() || updates.Msg().GetCurrent() != "v1.0.0" {
		t.Fatalf("at start: %v, %v", updates.Msg(), updates.Err())
	}
	waitChecks(2)
	if r, n := updates.Msg().GetReady(), updates.Msg().GetNotesUrl(); r != "" || n != "" {
		t.Fatalf("the release of its own version is offered: %q, notes %q", r, n)
	}

	// A newer release, whose sum is wrong: offered, and nothing downloads without the click.
	fake.publish(t, "v2.0.0", "djinn_test", v2, func(s string) string { return strings.Repeat("0", 64) + s[64:] })
	for updates.Msg().GetReady() != "v2.0.0" {
		if !updates.Receive() {
			t.Fatalf("v2.0.0 never offered: %v\n%s", updates.Err(), oldErrs.String())
		}
	}
	// The window links its release notes.
	if n := updates.Msg().GetNotesUrl(); n != fake.srv.URL+"/notes/v2.0.0" {
		t.Fatalf("the notes of v2.0.0: %q", n)
	}
	waitChecks(3)
	if _, downloads := fake.counts(); downloads != 0 {
		t.Fatalf("downloaded %d times without a click", downloads)
	}
	alive("offered")

	// The click: the sum does not match, the update fails and nothing changes.
	_, err = ui.Update(ctx, connect.NewRequest(&uiv1.UiServiceUpdateRequest{}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("update with a wrong sum: %v", err)
	}
	if _, downloads := fake.counts(); downloads != 1 {
		t.Fatalf("downloads after the click: %d, want 1", downloads)
	}
	time.Sleep(2 * restartDelayForTests)
	alive("wrong sum")
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("a refused release left files beside djinn: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(home, RestartFile)); !os.IsNotExist(err) {
		t.Fatalf("a refused release noted a restart: %v", err)
	}

	// The right sum: djinn update downloads it, swaps it in, and Djinn restarts on it.
	fake.publish(t, "v2.0.0", "djinn_test", v2, nil)
	code, out, errs := runDjinn(t, env, "update", "--yes")
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		t.Fatalf("v1.0.0 still runs after djinn update:\n%s", oldErrs.String())
	}
	stopStarted(t, home, oldErrs.String())
	t.Logf("djinn update: exit %d\n%s%s", code, out, errs)
	if code != 0 || !strings.Contains(out, "restarting on v2.0.0") || !strings.Contains(out, "now running v2.0.0") {
		t.Fatalf("djinn update: exit %d\n%s%s\nv1.0.0:\n%s", code, out, errs, oldErrs.String())
	}
	if b, err := os.ReadFile(djinnPath); err != nil || !bytes.Equal(b, v2) {
		t.Fatalf("the installed binary is not the release (%v)", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("files beside djinn after the update: %v", entries)
	}
	ui, _ = clients(t, answering(t, home, func() string { b, _ := os.ReadFile(filepath.Join(home, LogFile)); return string(b) }))
	now, err := ui.WatchUpdate(ctx, connect.NewRequest(&uiv1.UiServiceWatchUpdateRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	defer now.Close()
	if !now.Receive() || now.Msg().GetCurrent() != "v2.0.0" || now.Msg().GetReady() != "" || now.Msg().GetNotesUrl() != "" {
		t.Fatalf("after the update: %v, %v", now.Msg(), now.Err())
	}
}

// updateWithoutDjinn says that no djinn runs, and starts none.
func updateWithoutDjinn(t *testing.T) {
	home := t.TempDir()
	code, out, errs := runDjinn(t, environ(home, t.TempDir()), "update", "--yes")
	if code != 1 || !strings.Contains(errs, "no djinn runs") {
		t.Fatalf("djinn update with no djinn: exit %d\n%s%s", code, out, errs)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("djinn update wrote in the data directory: %v", entries)
	}
}

// updateNeedsAPerson: from a shell that is not a terminal, as an agent's, djinn update refuses without --yes.
func updateNeedsAPerson(t *testing.T) {
	code, out, errs := runDjinn(t, environ(t.TempDir(), t.TempDir()), "update")
	if code != 1 || !strings.Contains(errs, "--yes") {
		t.Fatalf("djinn update without a terminal: exit %d\n%s%s", code, out, errs)
	}
}

// scriptSource is a release source whose newest release is a script that says its version, as djinn version does.
type scriptSource struct {
	version string
	mu      sync.Mutex
	fetches int
}

func (s *scriptSource) latest(context.Context) (*release, error) {
	return &release{Version: s.version}, nil
}

func (s *scriptSource) fetch(_ context.Context, dir string) (string, string, error) {
	s.mu.Lock()
	s.fetches++
	s.mu.Unlock()
	f, err := os.CreateTemp(dir, ".djinn-new-*")
	if err != nil {
		return "", "", err
	}
	_, err = f.WriteString("#!/bin/sh\necho djinn " + s.version + "\n")
	if err := errors.Join(err, f.Close(), os.Chmod(f.Name(), 0o755)); err != nil {
		return "", "", err
	}
	return f.Name(), s.version, nil
}

// TestUpdaterConsider: a Djinn built from a checkout finds a release and does what its checkout says
// (harness.Release, tested on real repositories in internal/harness): on main, the release installs by itself at the
// path of the running djinn, once, the restart left to the click; offered, it waits for the click; a release the build
// holds, or one a branch merges instead, is not offered, and nothing downloads. Not in parallel: the release it installs
// runs as soon as written (TestBinaryVersion).
func TestUpdaterConsider(t *testing.T) {
	const version = "local-1a2b3c4"
	for _, c := range []struct {
		name      string
		fit       harness.ReleaseFit
		offered   bool
		installed bool
	}{
		{"install", harness.ReleaseInstall, true, true},
		{"offer", harness.ReleaseOffer, true, false},
		{"held", harness.ReleaseHeld, false, false},
		{"merge", harness.ReleaseMerge, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			exe := filepath.Join(t.TempDir(), "djinn")
			if err := os.WriteFile(exe, []byte("#!/bin/sh\necho djinn "+version+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(exe)
			if err != nil {
				t.Fatal(err)
			}
			svc, err := ui.New(version)
			if err != nil {
				t.Fatal(err)
			}
			src := &scriptSource{version: "v0.2.0"}
			var asked []string
			u := &updater{
				exe: exe, version: version, ui: svc, releases: src, started: info, seen: info,
				fit: func(_ context.Context, tag string) (harness.ReleaseFit, error) {
					asked = append(asked, tag)
					return c.fit, nil
				},
			}
			r, _ := src.latest(t.Context())
			u.consider(t.Context(), r)
			u.consider(t.Context(), r) // Found again, six hours later.
			if len(asked) != 2 || asked[0] != "v0.2.0" {
				t.Errorf("fit %d: the checkout was asked about %v", c.fit, asked)
			}
			if got := u.offered(); got != c.offered {
				t.Errorf("fit %d: offered %v; want %v", c.fit, got, c.offered)
			}
			installed, err := binaryVersion(t.Context(), exe)
			if err != nil {
				t.Fatal(err)
			}
			if got := installed == "v0.2.0"; got != c.installed || c.installed && u.check(t.Context()) != "v0.2.0" {
				t.Errorf("fit %d: the djinn at the path is %s, the update ready %q", c.fit, installed, u.check(t.Context()))
			}
			if want := map[bool]int{true: 1, false: 0}[c.installed]; src.fetches != want {
				t.Errorf("fit %d: %d downloads; want %d", c.fit, src.fetches, want)
			}
			if u.Restarting() {
				t.Errorf("fit %d: restarting without a click", c.fit)
			}
		})
	}
}

// TestBinaryVersion: the version a djinn binary prints, as djinn version does; any other output, or a binary that does
// not run, is an error. Not in parallel: a script run as soon as written fails with "text file busy" while another
// test forks, its child holding the script open for writing until it execs.
func TestBinaryVersion(t *testing.T) {
	for _, c := range []struct {
		name, script, want, err string
	}{
		{name: "a djinn", script: "echo djinn v1.2.0", want: "v1.2.0"},
		{name: "another program", script: "echo lamp 1.0 beta", err: `unexpected version "lamp 1.0 beta"`},
		{name: "it fails", script: "exit 3", err: "exit status 3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			exe := filepath.Join(t.TempDir(), "djinn")
			if err := os.WriteFile(exe, []byte("#!/bin/sh\n"+c.script+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			got, err := binaryVersion(t.Context(), exe)
			if got != c.want || c.err == "" && err != nil || c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
				t.Errorf("binaryVersion = %q, %v; want %q, %q", got, err, c.want, c.err)
			}
		})
	}
}

// TestRestartTerminals: an update restart opens the shells and the leads again, never an agent's install or
// sign-in that the window started.
func TestRestartTerminals(t *testing.T) {
	t.Parallel()
	got := restartTerminals([]*terminal.Terminal{
		{Name: "main", Command: []string{"/bin/sh"}, Dir: "/p"},
		{Name: "setup-claude-k1", Command: []string{"/bin/sh", "-c", "curl … | bash"}},
		{Name: "lead", Command: []string{"claude"}, Dir: "/p"},
	})
	if len(got) != 2 || got[0].Name != "main" || got[0].Directory != "/p" || got[1].Name != "lead" {
		t.Fatalf("restart terminals %+v, want main and lead", got)
	}
}

// TestResumable: a lead started from a brief resumes its session after a restart; other commands stay as they were.
func TestResumable(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, want []string }{
		{[]string{"/bin/zsh", "-lc", "claude --session-id 0a56-c9be --append-system-prompt-file rules.md 'go'"},
			[]string{"/bin/zsh", "-lc", "claude --resume 0a56-c9be"}},
		{[]string{"/bin/zsh", "-lc", "claude --resume 0a56"}, []string{"/bin/zsh", "-lc", "claude --resume 0a56"}},
		{[]string{"/bin/zsh"}, []string{"/bin/zsh"}},
		{nil, nil},
	} {
		if got := resumable(c.in); !slices.Equal(got, c.want) {
			t.Errorf("resumable(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
