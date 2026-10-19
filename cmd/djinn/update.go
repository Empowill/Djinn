package main

// The update of the Djinn in use. `go tool task install` puts a newer djinn at the path of the running one without
// disturbing it; the running one notices, says so, and waits. A Djinn installed from a release, or with go install,
// also looks for a newer release (release.go) and offers it the same way, downloading nothing yet. So does a Djinn
// built from a checkout, as its checkout says (harness.Release): on main, a release that holds the build is offered,
// and installs by itself when the project's settings say so, the restart still waiting for the click; on a branch,
// none installs over the branch's build: Djinn merges main into the wishes' branches instead. Only the window's
// update button or `djinn update` restarts it: a release is first downloaded, verified and renamed over the running
// binary; then Djinn notes its open terminals in the data directory, stops as when it quits (workers interrupted,
// nothing lost), and starts the new binary, which runs those terminals again on the same sessions. The same note,
// with the leads only, is kept while Djinn runs, for a crash (crash.go).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/term"

	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/gen/go/ui/v1/uiv1connect"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/harness"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/swapexe"
	"github.com/empowill/djinn/internal/terminal"
	"github.com/empowill/djinn/internal/ui"
)

// RestartFile holds, in the data directory, the terminals a restart runs again: those of an update, or the leads
// that ran when djinn up crashed (crash.go). The next djinn up removes it as it reads it, then runs them.
const RestartFile = "restart.json"

var (
	// updatePoll is how often djinn up looks at its own path for a newer binary. Tests shorten it.
	updatePoll = 3 * time.Second
	// restartDelay leaves the response to Update the time to leave before the server stops.
	restartDelay = 200 * time.Millisecond
)

// restartNote is what a restart remembers, in RestartFile.
type restartNote struct {
	// Version the restart goes to; empty in the note of the leads, kept for a crash.
	Version string `json:"version"`
	// Terminals are the terminals that ran, in order.
	Terminals []restartTerminal `json:"terminals"`
	// ShowWish and ShowTerminal are what the window showed last: the new window shows them again.
	ShowWish     string `json:"show_wish,omitempty"`
	ShowTerminal string `json:"show_terminal,omitempty"`
}

type restartTerminal struct {
	Name      string   `json:"name"`
	Command   []string `json:"command"`
	Directory string   `json:"directory"`
}

// updater watches the path of the running djinn for a newer binary, and its releases for a newer one, and restarts on
// it when asked.
type updater struct {
	exe      string // path of the running binary, as it started
	version  string // version of the running binary
	ui       *ui.Service
	terms    *terminal.Manager
	leads    *leadNote    // where the note of the restart goes
	stop     func(string) // stops djinn up, for a source (stop.go)
	releases source       // where a newer release comes from; nil for a development build
	// fit says what a release found means for a Djinn built from a checkout (harness.Release); nil for another one.
	fit func(ctx context.Context, tag string) (harness.ReleaseFit, error)
	// describe names the newer Djinn of a version as a build (harness.DescribeVersion): nil when no project holds its
	// commit. Nil: it is not named.
	describe func(ctx context.Context, version string) *uiv1.Build

	mu         sync.Mutex
	started    os.FileInfo // the file this djinn started from
	seen       os.FileInfo // the file last looked at
	local      string      // version of the newer binary at the path; empty for none
	offer      *release    // newer release offered, not downloaded yet
	installed  string      // the release installed by itself, not to install again
	installing bool
	restarting bool
	named      string // the version ready last named, or being named
}

// newUpdater returns the updater of a djinn of version. A development build never updates: workers rebuild it all the
// time, and it is not the Djinn in use.
func newUpdater(version string, svc *ui.Service, terms *terminal.Manager, leads *leadNote, stop func(string)) (*updater, error) {
	if version == "dev" {
		return nil, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(exe)
	if err != nil {
		return nil, err
	}
	releases, err := releaseSource(version)
	if err != nil {
		return nil, err
	}
	return &updater{
		exe: exe, version: version, ui: svc, terms: terms, leads: leads, stop: stop, releases: releases,
		started: info, seen: info,
	}, nil
}

// run looks for a newer binary at once, then every updatePoll, and for a newer release at once, then every
// releaseCheck, until ctx is done.
func (u *updater) run(ctx context.Context) {
	defer logx.Crash("updates")
	if u.releases != nil {
		go u.watchReleases(ctx)
	}
	tick := time.NewTicker(updatePoll)
	defer tick.Stop()
	for {
		u.check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// check tells the window whether a newer binary waits at the path, and returns its version; empty for none. It only
// runs the binary when the file changed since the last look.
func (u *updater) check(ctx context.Context) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	info, err := os.Stat(u.exe)
	if err != nil { // Between the two renames of an install on Windows, or removed: nothing to offer now.
		u.seen = nil
		u.local = ""
		u.publish()
		return ""
	}
	if u.seen != nil && os.SameFile(info, u.seen) && info.ModTime().Equal(u.seen.ModTime()) {
		return u.local
	}
	u.seen = info
	u.local = ""
	if !os.SameFile(info, u.started) || !info.ModTime().Equal(u.started.ModTime()) {
		if v, err := binaryVersion(ctx, u.exe); err != nil {
			fmt.Fprintln(os.Stderr, "djinn: the binary at", u.exe, "does not run:", err)
			logx.L().Warn("the newer binary does not run", "loop", "updates", "path", u.exe, "err", err)
		} else if v != u.version {
			u.local = v
		}
	}
	u.publish()
	return u.local
}

// publish tells the window what it may offer: the newer binary at the path, else the newer release. u.mu is held.
func (u *updater) publish() {
	ready, notes := u.local, ""
	if ready == "" && u.offer != nil {
		ready, notes = u.offer.Version, u.offer.Notes
	}
	u.ui.SetReady(ready, notes, ready == u.local)
	if ready != u.named {
		u.named = ready
		if ready != "" && u.describe != nil {
			go func() {
				defer logx.Crash("updates: describe a build", "version", ready)
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				u.ui.SetReadyBuild(ready, u.describe(ctx, ready))
			}()
		}
	}
}

// watchReleases asks the source for a newer release at once, then every releaseCheck, until ctx is done. It only
// reads a version: nothing downloads before the click.
func (u *updater) watchReleases(ctx context.Context) {
	defer logx.Crash("releases")
	tick := time.NewTicker(releaseCheck)
	defer tick.Stop()
	for {
		check, cancel := context.WithTimeout(ctx, time.Minute)
		r, err := u.releases.latest(check)
		cancel()
		if err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "djinn: looking for a newer release:", err)
			logx.L().Warn("look for a newer release", "loop", "releases", "err", err)
		}
		if err == nil {
			u.consider(ctx, r)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// consider offers r, the newest release (nil for none), unless the checkout a Djinn built from one comes from says
// otherwise; and installs it by itself when that checkout says so, once: the restart waits for the click.
func (u *updater) consider(ctx context.Context, r *release) {
	install := false
	if r != nil && u.fit != nil {
		look, cancel := context.WithTimeout(ctx, 3*time.Minute)
		fit, err := u.fit(look, r.Version)
		cancel()
		switch {
		case err != nil:
			fmt.Fprintln(os.Stderr, "djinn: where the checkout stands against", r.Version+":", err)
			logx.L().Warn("compare the checkout with a release", "loop", "releases", "release", r.Version, "err", err)
		case fit == harness.ReleaseHeld:
			r = nil
		case fit == harness.ReleaseMerge:
			fmt.Fprintln(os.Stderr, "djinn:", r.Version, "is not installed over this branch's build: Djinn merges main into the wishes' branches")
			r = nil
		case fit == harness.ReleaseInstall:
			install = true
		}
	}
	u.mu.Lock()
	u.offer = r
	install = install && u.local == "" && u.installed != r.Version
	if install {
		u.installed = r.Version
	}
	u.publish()
	u.mu.Unlock()
	if !install {
		return
	}
	if err := u.install(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "djinn: installing", r.Version+":", err)
		logx.L().Error("install a release", "loop", "releases", "release", r.Version, "err", err)
		return
	}
	u.check(ctx) // The window offers to restart on it.
}

// install downloads the newer release, checks that it runs and says the version it promises, and renames it over the
// running binary, which keeps going on its old file: check then finds it at the path.
func (u *updater) install(ctx context.Context) error {
	u.mu.Lock()
	if u.installing || u.restarting {
		u.mu.Unlock()
		return errors.New("djinn is already updating")
	}
	u.installing = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.installing = false
		u.mu.Unlock()
	}()
	path, version, err := u.releases.fetch(ctx, filepath.Dir(u.exe))
	if err != nil {
		return err
	}
	defer os.Remove(path) // Gone once renamed; left only when something failed.
	got, err := binaryVersion(ctx, path)
	if err != nil {
		return fmt.Errorf("the downloaded djinn does not run: %w", err)
	}
	if got != version {
		return fmt.Errorf("the downloaded djinn says it is %s, not %s", got, version)
	}
	return swapexe.Install(path, u.exe)
}

// binaryVersion runs exe version, and returns the version it prints.
func binaryVersion(ctx context.Context, exe string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "version")
	detach(cmd) // No console flashes on Windows.
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 || fields[0] != "djinn" {
		return "", fmt.Errorf("unexpected version %q", strings.TrimSpace(string(out)))
	}
	return fields[1], nil
}

// restart notes the running terminals, then stops djinn up, which starts the newer binary once everything is closed;
// by names who asked (ui.StopBy). It is ui.Service.Restart.
func (u *updater) restart(by string) (string, int, error) {
	ctx := context.Background()
	version := u.check(ctx)
	if version == "" && u.offered() {
		if err := u.install(ctx); err != nil {
			return "", 0, fmt.Errorf("install the new release: %w", err)
		}
		version = u.check(ctx)
	}
	if version == "" {
		return "", 0, fmt.Errorf("no newer djinn waits at %s: install one first (go tool task install)", u.exe)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.restarting {
		return "", 0, errors.New("djinn is already restarting")
	}
	note := restartNote{Version: version}
	note.ShowWish, note.ShowTerminal = u.ui.LastShow()
	note.Terminals = restartTerminals(u.terms.Running())
	if err := u.leads.restart(note); err != nil {
		return "", 0, fmt.Errorf("note the terminals: %w", err)
	}
	u.restarting = true
	logx.L().Info("restart asked", "from", u.version, "to", version, "terminals", len(note.Terminals), "by", by)
	go func() {
		defer logx.Crash("restart")
		time.Sleep(restartDelay)
		u.stop(restartStop + by)
	}()
	return version, len(note.Terminals), nil
}

// offered tells whether a newer release is offered.
func (u *updater) offered() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.offer != nil
}

// sessionID finds the session a lead was started with from a brief: claude --session-id <id> ….
var sessionID = regexp.MustCompile(`(?:^|\s)claude\s.*--session-id[ =]([A-Za-z0-9][A-Za-z0-9._-]*)`)

// restartTerminals are the terminals a restart opens again: those that run, but an agent's install or sign-in, which
// the window started (setup-…), never run again by itself.
func restartTerminals(running []*terminal.Terminal) []restartTerminal {
	var out []restartTerminal
	for _, t := range running {
		if strings.HasPrefix(t.Name, "setup-") {
			continue
		}
		out = append(out, restartTerminal{Name: t.Name, Command: resumable(t.Command), Directory: t.Dir})
	}
	return out
}

// resumable is the command that takes a terminal back after a restart. A lead started from a brief named its new
// session with --session-id, which claude refuses a second time: it resumes that session instead.
func resumable(cmd []string) []string {
	if len(cmd) == 0 {
		return cmd
	}
	last := cmd[len(cmd)-1]
	m := sessionID.FindStringSubmatch(last)
	if m == nil {
		return cmd
	}
	out := append([]string(nil), cmd...)
	out[len(out)-1] = "claude --resume " + m[1]
	return out
}

// Restarting tells whether djinn up stops to restart on a newer binary.
func (u *updater) Restarting() bool {
	if u == nil {
		return false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.restarting
}

// resumeTerminals runs again the terminals a restart noted in home, or the leads a crash left, shows the window what
// it showed, and returns the terminals that did not start, one line each. The note is removed once read, before the
// terminals start and the note of the leads follows them: a lead that did not start keeps its session recorded,
// for djinn lead resume.
//
// A note from a Djinn of one lead per wish names its leads' terminals lead-<wish id>: the first that runs session,
// the lead's recorded session (any when none is), comes back as the lead's terminal; the others do not, and
// resumeLead resumes the lead's session after: never a lead under another name, nor two terminals on a session.
// While Djinn drives the lead (driven), the lead's terminal does not run again: no lead lives in a terminal.
func resumeTerminals(home string, terms *terminal.Manager, svc *ui.Service, session string, driven bool, say io.Writer) []string {
	path := filepath.Join(home, RestartFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var note restartNote
	if err == nil {
		err = json.Unmarshal(data, &note)
	}
	if err != nil {
		fmt.Fprintln(say, "djinn: cannot read", path+":", err)
		logx.L().Error("read the restart note", "path", path, "err", err)
		return []string{fmt.Sprintf("%s: %v", path, err)}
	}
	if err := os.Remove(path); err != nil {
		fmt.Fprintln(say, "djinn:", err)
		logx.L().Warn("remove the restart note", "err", err)
	}
	var failed []string
	shown, dropped := "", 0
	lead := slices.ContainsFunc(note.Terminals, func(t restartTerminal) bool { return t.Name == plan.LeadTerminal })
	for _, t := range note.Terminals {
		if driven && (t.Name == plan.LeadTerminal || strings.HasPrefix(t.Name, "lead-")) {
			fmt.Fprintf(say, "djinn: %s does not run again: Djinn drives the lead, a session per batch of its events\n", t.Name)
			dropped++
			continue
		}
		if strings.HasPrefix(t.Name, "lead-") {
			if lead || session != "" && !strings.Contains(strings.Join(t.Command, " "), session) {
				fmt.Fprintf(say, "djinn: %s, a wish's lead, does not run again: Djinn has one lead now, in the %q terminal\n",
					t.Name, plan.LeadTerminal)
				dropped++
				continue
			}
			if note.ShowTerminal == t.Name {
				note.ShowTerminal = plan.LeadTerminal
			}
			t.Name, lead = plan.LeadTerminal, true
		}
		_, err := os.Stat(t.Directory)
		if err == nil && t.Name == plan.LeadTerminal && plan.HoldsHome(t.Directory) {
			err = errors.New("a lead never runs in the home folder: djinn lead resume starts it in a project")
		}
		if err == nil {
			_, _, err = terms.Open(t.Name, t.Command, t.Directory, 0, 0)
		}
		if err != nil {
			line := fmt.Sprintf("%s: %s in %s: %v", t.Name, strings.Join(t.Command, " "), t.Directory, err)
			fmt.Fprintln(say, "djinn: not resumed:", line)
			logx.L().Warn("terminal not resumed", "terminal", t.Name, "dir", t.Directory, "err", err)
			failed = append(failed, line)
			continue
		}
		if t.Name == note.ShowTerminal || shown == "" && t.Name == plan.LeadTerminal {
			shown = t.Name
		}
	}
	if shown != "" || note.ShowWish != "" {
		svc.Present(note.ShowWish, shown)
	}
	if note.Version == "" {
		fmt.Fprintf(say, "djinn: the last djinn up crashed, %d of %d leads reopened\n",
			len(note.Terminals)-dropped-len(failed), len(note.Terminals)-dropped)
		return failed
	}
	fmt.Fprintf(say, "djinn: restarted on %s, %d of %d terminals resumed\n",
		note.Version, len(note.Terminals)-dropped-len(failed), len(note.Terminals)-dropped)
	return failed
}

// runUpdate is djinn update: it asks the running djinn to restart on the newer binary installed at its path, waits for
// the new one, and says which terminals it could not run again.
//
// It restarts the Djinn its user works in, so it runs only from a person's terminal, or with --yes: an agent with a
// shell must not restart it by accident.
func runUpdate(args []string) error {
	yes := false
	for _, a := range args {
		switch a {
		case "-h", "--help":
			cli.Update.WriteHelp(os.Stdout)
			return nil
		case "--yes":
			yes = true
		default:
			return fmt.Errorf("unexpected argument %q", a)
		}
	}
	if !yes && !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("djinn update restarts the Djinn in use: run it from your terminal, or pass --yes")
	}
	home, err := ui.Home()
	if err != nil {
		return err
	}
	addr := os.Getenv("DJINN_ADDR")
	if addr == "" {
		if addr, err = server.ReadAddr(home); err != nil {
			return errors.New("no djinn runs: djinn up starts the one installed")
		}
	}
	if !cli.Alive(addr) {
		return errors.New("no djinn runs: djinn up starts the one installed")
	}
	client, base, err := cli.Dial(addr)
	if err != nil {
		return err
	}
	uiClient := uiv1connect.NewUiServiceClient(client, base)
	// The time to download a release, then for the new djinn to answer.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := uiClient.Update(ctx, connect.NewRequest(&uiv1.UiServiceUpdateRequest{}))
	if err != nil {
		return err
	}
	want := res.Msg.GetVersion()
	fmt.Printf("djinn: restarting on %s, with %d terminals\n", want, res.Msg.GetTerminals())
	// The new djinn answers at the same address on macOS and Linux: wait for its version.
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("djinn %s does not answer: see %s", want, filepath.Join(home, LogFile))
		case <-time.After(100 * time.Millisecond):
		}
		if os.Getenv("DJINN_ADDR") == "" {
			if addr, err = server.ReadAddr(home); err != nil || !cli.Alive(addr) {
				continue
			}
		}
		if client, base, err = cli.Dial(addr); err != nil {
			continue
		}
		uiClient = uiv1connect.NewUiServiceClient(client, base)
		env, err := uiClient.GetEnvironment(ctx, connect.NewRequest(&uiv1.UiServiceGetEnvironmentRequest{}))
		if err != nil || env.Msg.GetVersion() != want {
			continue
		}
		break
	}
	fmt.Println("djinn: now running", want)
	stream, err := uiClient.WatchUpdate(ctx, connect.NewRequest(&uiv1.UiServiceWatchUpdateRequest{}))
	if err != nil {
		return err
	}
	defer stream.Close()
	if !stream.Receive() {
		return stream.Err()
	}
	if lines := stream.Msg().GetNotResumed(); len(lines) > 0 {
		fmt.Println("djinn: not resumed (the lead keeps its session: djinn lead resume):")
		for _, line := range lines {
			fmt.Println("  -", line)
		}
		return errors.New("some terminals did not start again")
	}
	return nil
}
