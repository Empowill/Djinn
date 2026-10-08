package main

// The update of the Djinn in use. `go tool task install` puts a newer djinn at the path of the running one without
// disturbing it; the running one notices, says so, and waits. Only the window's update button or `djinn update`
// restarts it: it notes its open terminals in the data directory, stops as when it quits (workers interrupted,
// nothing lost), and starts the new binary, which runs those terminals again on the same sessions.

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
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/term"

	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/gen/go/ui/v1/uiv1connect"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/terminal"
	"github.com/empowill/djinn/internal/ui"
)

// RestartFile holds, in the data directory, the terminals a restart runs again. The new djinn removes it once it
// has run them; one that could not start finds it at the next djinn up.
const RestartFile = "restart.json"

var (
	// updatePoll is how often djinn up looks at its own path for a newer binary. Tests shorten it.
	updatePoll = 3 * time.Second
	// restartDelay leaves the response to Update the time to leave before the server stops.
	restartDelay = 200 * time.Millisecond
)

// restartNote is what a restart remembers, in RestartFile.
type restartNote struct {
	// Version the restart goes to.
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

// updater watches the path of the running djinn for a newer binary, and restarts on it when asked.
type updater struct {
	exe     string // path of the running binary, as it started
	version string // version of the running binary
	home    string
	ui      *ui.Service
	terms   *terminal.Manager
	stop    func() // stops djinn up

	mu         sync.Mutex
	started    os.FileInfo // the file this djinn started from
	seen       os.FileInfo // the file last looked at
	restarting bool
}

// newUpdater returns the updater of a djinn of version. A development build never updates: workers rebuild it all the
// time, and it is not the Djinn in use.
func newUpdater(version, home string, svc *ui.Service, terms *terminal.Manager, stop func()) (*updater, error) {
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
	return &updater{
		exe: exe, version: version, home: home, ui: svc, terms: terms, stop: stop, started: info, seen: info,
	}, nil
}

// run looks for a newer binary at once, then every updatePoll, until ctx is done.
func (u *updater) run(ctx context.Context) {
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
		u.ui.SetReady("")
		return ""
	}
	if u.seen != nil && os.SameFile(info, u.seen) && info.ModTime().Equal(u.seen.ModTime()) {
		return u.ui.Ready()
	}
	u.seen = info
	ready := ""
	if !os.SameFile(info, u.started) || !info.ModTime().Equal(u.started.ModTime()) {
		if v, err := binaryVersion(ctx, u.exe); err != nil {
			fmt.Fprintln(os.Stderr, "djinn: the binary at", u.exe, "does not run:", err)
		} else if v != u.version {
			ready = v
		}
	}
	u.ui.SetReady(ready)
	return ready
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

// restart notes the running terminals, then stops djinn up, which starts the newer binary once everything is closed.
// It is ui.Service.Restart.
func (u *updater) restart() (string, int, error) {
	version := u.check(context.Background())
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
	for _, t := range u.terms.Running() {
		note.Terminals = append(note.Terminals, restartTerminal{Name: t.Name, Command: resumable(t.Command), Directory: t.Dir})
	}
	data, err := json.MarshalIndent(note, "", "  ")
	if err != nil {
		return "", 0, err
	}
	if err := ui.WriteAtomic(filepath.Join(u.home, RestartFile), data); err != nil {
		return "", 0, fmt.Errorf("note the terminals: %w", err)
	}
	u.restarting = true
	go func() {
		time.Sleep(restartDelay)
		u.stop()
	}()
	return version, len(note.Terminals), nil
}

// sessionID finds the session a lead was started with from a brief: claude --session-id <id> ….
var sessionID = regexp.MustCompile(`(?:^|\s)claude\s.*--session-id[ =]([A-Za-z0-9][A-Za-z0-9._-]*)`)

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

// resumeTerminals runs again the terminals a restart noted in home, shows the window what it showed, and returns the
// terminals that did not start, one line each. The note is removed once read: a lead that did not start keeps its
// session in its wish, for djinn wish resume.
func resumeTerminals(home string, terms *terminal.Manager, svc *ui.Service, say io.Writer) []string {
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
		return []string{fmt.Sprintf("%s: %v", path, err)}
	}
	var failed []string
	shown := ""
	for _, t := range note.Terminals {
		_, err := os.Stat(t.Directory)
		if err == nil {
			_, _, err = terms.Open(t.Name, t.Command, t.Directory, 0, 0)
		}
		if err != nil {
			line := fmt.Sprintf("%s: %s in %s: %v", t.Name, strings.Join(t.Command, " "), t.Directory, err)
			fmt.Fprintln(say, "djinn: not resumed:", line)
			failed = append(failed, line)
			continue
		}
		if t.Name == note.ShowTerminal || shown == "" && strings.HasPrefix(t.Name, "lead-") {
			shown = t.Name
		}
	}
	switch {
	case shown != "" && shown == note.ShowTerminal:
		svc.Present(note.ShowWish, shown)
	case shown != "":
		svc.Present(strings.TrimPrefix(shown, "lead-"), shown)
	}
	if err := os.Remove(path); err != nil {
		fmt.Fprintln(say, "djinn:", err)
	}
	fmt.Fprintf(say, "djinn: restarted on %s, %d of %d terminals resumed\n",
		note.Version, len(note.Terminals)-len(failed), len(note.Terminals))
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
		if a != "--yes" {
			return fmt.Errorf("unexpected argument %q", a)
		}
		yes = true
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
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
		fmt.Println("djinn: not resumed (each wish keeps its lead: djinn wish resume <wish-id>):")
		for _, line := range lines {
			fmt.Println("  -", line)
		}
		return errors.New("some terminals did not start again")
	}
	return nil
}
