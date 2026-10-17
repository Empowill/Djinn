// Package ui implements UiService, the API the window calls. It writes nothing outside the data directory.
package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/gen/go/ui/v1/uiv1connect"
	"github.com/empowill/djinn/internal/link"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
)

// Service implements uiv1connect.UiServiceHandler.
type Service struct {
	uiv1connect.UnimplementedUiServiceHandler

	// Home is the data directory: $DJINN_HOME, by default djinn in the user's configuration folder.
	Home string
	// Version of djinn, as GetEnvironment reports it.
	Version string
	// Open opens a link in the default browser. Tests replace it.
	Open func(link string) error
	// Raise brings the window to the front, or in browser mode prints the address of the page again. Nil does
	// nothing.
	Raise func()
	// Window tells that Raise brings a native window to the front.
	Window bool
	// Restart restarts Djinn on the newer one that waits at its path, once the response to Update is sent, and
	// returns the version it restarts on and how many terminals it will run again. Nil: Update is unavailable.
	Restart func() (version string, terminals int, err error)
	// AgentPath is the PATH where GetEnvironment looks for the agents, and nowhere else. Nil: Djinn's own, brought
	// up to date by machine.ExtendPath, which the workers and the leads get too. Tests replace it.
	AgentPath func() string
	// Install installs a build that SetBuild proposed: its project's install command, run on its commit, as run says.
	// It tells whether a newer Djinn waits at the path of the running one since, to restart on, and returns the end of
	// what its command wrote. Nil: no build installs.
	Install func(ctx context.Context, build *uiv1.Build, run InstallRun) (newer bool, out string, err error)
	// Life is djinn up's: an install runs within it, whatever becomes of the call that asked for it. Nil: it never ends.
	Life context.Context
	// Busy says why the machine cannot take an install Djinn starts by itself now: a gate held, the machine under
	// pressure; "" when it can. Nil: it always can.
	Busy func() string
	// InstallLimit is how long an install may work, its waits aside, before Djinn stops it. Zero: DefaultInstallLimit.
	InstallLimit time.Duration
	// AutoRetry is how long an install Djinn starts by itself waits before it looks at a busy machine again. Zero:
	// DefaultAutoRetry.
	AutoRetry time.Duration
	// ChooseFolder opens the system's folder dialog over the window, titled title and open in directory, and returns
	// the folder chosen, or empty when the user cancelled. Nil: the page has no folder dialog (the browser).
	ChooseFolder func(title, directory string) (string, error)
	// Shortcuts is the global shortcut that brings the window forward. Nil: SetShortcut is unavailable.
	Shortcuts *Shortcuts
	// Linked is the wish a link shows: the tilasm's, or the wish itself; an error of code NotFound when it is not on
	// this machine. Nil: OpenLink is unavailable.
	Linked func(ctx context.Context, l link.Link) (wishID string, err error)
	// WatchLoad streams operating load and memory stats. Nil: no load stats streamed in Watch.
	WatchLoad func(ctx context.Context, send func(*djinnv1.LoadServiceWatchResponse) error) error

	// Shown is called when the window is asked to show a wish or a terminal.
	Shown func(wishID, terminal string)
	// Known tells whether a wish is still on this machine: a show of the last minute whose wish was deleted since is
	// not replayed to a window that opens, which would leave the wish it shows for the flying carpet. Nil: every wish is.
	Known func(ctx context.Context, wishID string) bool
	// Notices shows the system notifications, and knows whether the system lets it. Nil: none (a test).
	Notices *Notices
	// Touched says that the developer is there (harness.Presence): it keeps the ifrits lit. Nil: nothing.
	Touched func()

	dialogs sync.Mutex // One folder dialog at a time.

	shows    sync.Mutex
	lastShow *uiv1.UiServiceWatchShowResponse
	lastAt   time.Time
	watchers map[chan *uiv1.UiServiceWatchShowResponse]struct{}

	updates    sync.Mutex
	ready      string           // version of the newer Djinn waiting; empty for none
	installed  bool             // it is at the path of the running one already: Update only restarts
	notesURL   string           // where its release notes are; empty for none
	notResumed []string         // terminals the last restart could not run again
	builds     []*uiv1.Build    // the last build pushed of each integration branch, not installed; the running one's first
	readyBuild *uiv1.Build      // the newer Djinn waiting, named as a build; nil when not known
	installing *uiv1.Installing // the install that runs now, or the last one when it failed; nil for none
	updated    chan struct{}    // closed and replaced at each change
	job        *job             // the install that runs now; nil for none
	autoNext   *uiv1.Build      // the build of the running line to install by itself next; nil for none
	autoRuns   bool             // autoLoop runs
	autoWake   chan struct{}    // wakes autoLoop when a build of the running line comes
}

// replay is how long a request to show something waits for a window that opens after it: djinn lead resume may
// start djinn, whose page loads after the request.
const replay = time.Minute

var _ uiv1connect.UiServiceHandler = (*Service)(nil)

// New returns the service on the data directory of this machine.
func New(version string) (*Service, error) {
	home, err := Home()
	if err != nil {
		return nil, err
	}
	return &Service{Home: home, Version: version, Open: openBrowser}, nil
}

func isTemp(p string) bool {
	if p == "" {
		return false
	}
	clean := filepath.Clean(p)
	tmp := filepath.Clean(os.TempDir())
	if strings.HasPrefix(clean, tmp+string(filepath.Separator)) || strings.HasPrefix(clean, "/tmp/") || strings.HasPrefix(clean, "/var/tmp/") {
		return true
	}
	if eval, err := filepath.EvalSymlinks(clean); err == nil {
		if strings.HasPrefix(eval, tmp+string(filepath.Separator)) || strings.HasPrefix(eval, "/tmp/") || strings.HasPrefix(eval, "/var/tmp/") {
			return true
		}
	}
	return false
}

var (
	realConfigDir string
	realHomeDir   string
)

func init() {
	if u, err := user.Current(); err == nil && u.HomeDir != "" && !isTemp(u.HomeDir) {
		realHomeDir = u.HomeDir
	} else if h, err := os.UserHomeDir(); err == nil && !isTemp(h) {
		realHomeDir = h
	}
	if c, err := os.UserConfigDir(); err == nil && !isTemp(c) {
		realConfigDir = c
	} else if realHomeDir != "" {
		realConfigDir = filepath.Join(realHomeDir, ".config")
	}
}

// isTestBuild is set to "true" via ldflags when building test binaries (e.g. bin/djinn-e2e, bin/djinn-e2e-native).
// W341 assumed that stray wishes were made by "a person with the window open" because go test was guarded,
// but the stray wishes were created when W328 executed bin/djinn-e2e by hand outside tests and cleared DJINN_TASK_ID.
// Marking test builds ensures that running test binaries by hand without harness environment refuses the real home.
var isTestBuild string

// IsTestBuild reports whether the current binary was built for testing or is an e2e binary.
func IsTestBuild() bool {
	if isTestBuild == "true" {
		return true
	}
	if exe, err := os.Executable(); err == nil {
		base := strings.ToLower(filepath.Base(exe))
		base = strings.TrimSuffix(base, ".exe")
		if strings.HasPrefix(base, "djinn-e2e") || strings.Contains(base, "-e2e") || strings.HasSuffix(base, ".test") {
			return true
		}
	}
	return false
}

// StampedTestBuild reports whether the binary was stamped a test build when linked, as task e2e and e2e-native stamp
// theirs: unlike IsTestBuild, a go test binary is not.
func StampedTestBuild() bool {
	return isTestBuild == "true"
}

// InTest reports whether the current process is running under `go test`, with DJINN_TEST=1, or is a test build.
func InTest() bool {
	return testing.Testing() || os.Getenv("DJINN_TEST") == "1" || IsTestBuild()
}

// RealHome returns the default data directory of the user: ~/.config/djinn (or djinn-dev for a development build).
func RealHome() string {
	config := realConfigDir
	if config == "" {
		if c, err := os.UserConfigDir(); err == nil && !isTemp(c) {
			config = c
		} else if realHomeDir != "" {
			config = filepath.Join(realHomeDir, ".config")
		}
	}
	if config == "" {
		return ""
	}
	name := "djinn"
	if Develop {
		name = "djinn-dev"
	}
	return filepath.Join(config, name)
}

// IsRealHome reports whether path points to the user's real Djinn data directory.
func IsRealHome(path string) bool {
	if path == "" || isTemp(path) {
		return false
	}
	clean := filepath.Clean(path)
	// Clean turns ~/ into ~\ on Windows: test the separator the system uses.
	if len(clean) > 2 && clean[0] == '~' && os.IsPathSeparator(clean[1]) && realHomeDir != "" {
		clean = filepath.Clean(filepath.Join(realHomeDir, clean[2:]))
	}
	cleanEval, errEval := filepath.EvalSymlinks(clean)

	var candidates []string
	add := func(base string) {
		if base == "" || isTemp(base) {
			return
		}
		candidates = append(candidates, filepath.Join(base, "djinn"), filepath.Join(base, "djinn-dev"))
	}
	add(realConfigDir)
	if realHomeDir != "" {
		add(filepath.Join(realHomeDir, ".config"))
		if runtime.GOOS == "darwin" {
			add(filepath.Join(realHomeDir, "Library", "Application Support"))
		}
		if runtime.GOOS == "windows" {
			add(filepath.Join(realHomeDir, "AppData", "Roaming"))
		}
	}

	for _, cand := range candidates {
		candClean := filepath.Clean(cand)
		if clean == candClean {
			return true
		}
		if errEval == nil {
			if candEval, err := filepath.EvalSymlinks(candClean); err == nil && cleanEval == candEval {
				return true
			}
		}
	}
	return false
}

// Develop is set by a development build of djinn, one built from a checkout rather than installed at a version: its
// data directory is then djinn-dev, so that the djinn being built never reaches the user's own djinn, its window,
// its socket or its wishes. DJINN_HOME still decides when it is set.
var Develop bool

// Home is the data directory: $DJINN_HOME, by default djinn in the user's configuration folder (djinn-dev for a
// development build).
func Home() (string, error) {
	home := os.Getenv("DJINN_HOME")
	if home == "" {
		// The system's place for user configuration: ~/.config on Linux, ~/Library/Application Support on macOS,
		// %AppData% on Windows.
		config, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("data directory: %w", err)
		}
		name := "djinn"
		if Develop {
			name = "djinn-dev"
		}
		home = filepath.Join(config, name)
	}
	if InTest() && IsRealHome(home) {
		return "", fmt.Errorf("refusing to use real data directory (%s) under test: without an address and a data folder given by the test harness, start a Djinn of your own (a DJINN_HOME of its own, djinn up on its own socket)", home)
	}
	return home, nil
}

// providers are the agent command lines Djinn knows: the agent, its name, and the program that runs it.
var providers = []struct{ id, name, program string }{
	{"codex", "Codex", "codex"}, {"claude", "Claude", "claude"}, {"antigravity", "Antigravity", "agy"},
}

func (s *Service) GetEnvironment(
	ctx context.Context, req *connect.Request[uiv1.UiServiceGetEnvironmentRequest],
) (*connect.Response[uiv1.UiServiceGetEnvironmentResponse], error) {
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32" // The name Node.js uses, which the window expects.
	}
	res := &uiv1.UiServiceGetEnvironmentResponse{
		Version: s.Version, Platform: platform, FolderDialog: s.ChooseFolder != nil,
	}
	if s.Shortcuts != nil {
		res.Shortcut = s.Shortcuts.State()
	}
	if req.Msg.GetAgents() {
		accounts := plan.ReadAccounts(s.Home)
		if s.AgentPath != nil {
			res.Providers = checkAgents(ctx, s.AgentPath(), accounts)
		} else {
			res.Providers = checkAgents(ctx, launchPath(), accounts)
		}
	} else {
		for _, p := range providers {
			command, err := exec.LookPath(p.program)
			if err != nil {
				command = p.program
			}
			res.Providers = append(res.Providers, &uiv1.Provider{
				Id: p.id, Name: p.name, Available: err == nil, Command: command,
			})
		}
	}
	return connect.NewResponse(res), nil
}

func (s *Service) SetShortcut(
	_ context.Context, req *connect.Request[uiv1.UiServiceSetShortcutRequest],
) (*connect.Response[uiv1.UiServiceSetShortcutResponse], error) {
	if s.Shortcuts == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("this djinn has no global shortcut"))
	}
	shortcut, err := s.Shortcuts.Set(req.Msg.GetChord())
	if errors.Is(err, errChord) {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&uiv1.UiServiceSetShortcutResponse{Shortcut: shortcut}), nil
}

func (s *Service) ChooseDirectory(
	_ context.Context, req *connect.Request[uiv1.UiServiceChooseDirectoryRequest],
) (*connect.Response[uiv1.UiServiceChooseDirectoryResponse], error) {
	if s.ChooseFolder == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("only the native window has a folder dialog"))
	}
	if !s.dialogs.TryLock() {
		return nil, connect.NewError(connect.CodeAborted, errors.New("a folder dialog is already open"))
	}
	defer s.dialogs.Unlock()
	chosen, err := s.ChooseFolder(req.Msg.GetTitle(), startFolder(req.Msg.GetDirectory()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("folder dialog: %w", err))
	}
	return connect.NewResponse(&uiv1.UiServiceChooseDirectoryResponse{Directory: chosen}), nil
}

// startFolder is where the folder dialog opens: dir when it is an absolute path to a folder, otherwise the home
// folder, or empty for the dialog's own choice.
func startFolder(dir string) string {
	if filepath.IsAbs(dir) {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func (s *Service) OpenExternal(
	_ context.Context, req *connect.Request[uiv1.UiServiceOpenExternalRequest],
) (*connect.Response[uiv1.UiServiceOpenExternalResponse], error) {
	raw := req.Msg.GetUrl()
	u, err := url.Parse(raw)
	if len(raw) > 2048 || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("only a short http or https link without credentials may be opened"))
	}
	link := u.String()
	if err := s.Open(link); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("open the link: %w", err))
	}
	return connect.NewResponse(&uiv1.UiServiceOpenExternalResponse{Url: link}), nil
}

// openBrowser opens link with the command of the operating system. link has been checked: http or https only.
func openBrowser(link string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", link)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		cmd = exec.Command("xdg-open", link)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() //nolint:errcheck // Only reaps the process; the browser reports its own errors.
	return nil
}

func (s *Service) Show(
	_ context.Context, req *connect.Request[uiv1.UiServiceShowRequest],
) (*connect.Response[uiv1.UiServiceShowResponse], error) {
	if s.Raise != nil {
		s.Raise()
	}
	if req.Msg.GetWishId() != "" || req.Msg.GetTerminal() != "" {
		s.Present(req.Msg.GetWishId(), req.Msg.GetTerminal())
	}
	return connect.NewResponse(&uiv1.UiServiceShowResponse{Window: s.Window}), nil
}

func (s *Service) OpenLink(
	ctx context.Context, req *connect.Request[uiv1.UiServiceOpenLinkRequest],
) (*connect.Response[uiv1.UiServiceOpenLinkResponse], error) {
	if s.Linked == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("this djinn opens no link"))
	}
	wishID, l, err := s.linked(ctx, req.Msg.GetUrl())
	if s.Raise != nil {
		s.Raise()
	}
	if err != nil {
		// The window says so too: the link came from a click, where nobody reads what djinn open prints.
		s.present(&uiv1.UiServiceWatchShowResponse{UnknownLink: req.Msg.GetUrl()})
		return nil, err
	}
	res := &uiv1.UiServiceOpenLinkResponse{WishId: wishID, Window: s.Window}
	if l.Kind == link.Tilasm {
		res.TilasmId = l.ID
	}
	s.present(&uiv1.UiServiceWatchShowResponse{WishId: wishID, TilasmId: res.TilasmId})
	return connect.NewResponse(res), nil
}

// linked reads a link and finds the wish it shows.
func (s *Service) linked(ctx context.Context, raw string) (string, link.Link, error) {
	l, err := link.Parse(raw)
	if err != nil {
		return "", l, connect.NewError(connect.CodeInvalidArgument, err)
	}
	wishID, err := s.Linked(ctx, l)
	if err != nil {
		return "", l, err
	}
	return wishID, l, nil
}

// PresentTilasm asks the windows to show a tilasm, in its wish's Tilasms tab, as Present does a wish.
func (s *Service) PresentTilasm(wishID, tilasmID string) {
	s.present(&uiv1.UiServiceWatchShowResponse{WishId: wishID, TilasmId: tilasmID})
}

// Present asks the windows to show a wish and a terminal: the ones watching now, and one that starts watching
// within a minute.
func (s *Service) Present(wishID, terminal string) { s.PresentAt(wishID, terminal, "") }

// PresentAt is Present with an element of the wish in view (UiServiceWatchShowResponse.target).
func (s *Service) PresentAt(wishID, terminal, target string) {
	s.present(&uiv1.UiServiceWatchShowResponse{WishId: wishID, Terminal: terminal, Target: target})
}

func (s *Service) present(msg *uiv1.UiServiceWatchShowResponse) {
	s.shows.Lock()
	s.lastShow, s.lastAt = msg, time.Now()
	for ch := range s.watchers {
		select {
		case ch <- msg:
		default: // A window that does not read keeps what it has: the next request matters more than this one.
		}
	}
	shown := s.Shown
	s.shows.Unlock()
	if shown != nil {
		shown(msg.GetWishId(), msg.GetTerminal())
	}
}

// SetReady says that a newer Djinn of version waits, its release notes at notesURL (empty for none): installed at the
// path of the running one already, or a release to download first. An empty version says none does.
func (s *Service) SetReady(version, notesURL string, installed bool) {
	s.updates.Lock()
	defer s.updates.Unlock()
	if s.ready == version && s.notesURL == notesURL && s.installed == installed {
		return
	}
	if s.ready != version {
		s.readyBuild = nil
	}
	s.ready, s.notesURL, s.installed = version, notesURL, installed && version != ""
	s.notifyUpdate()
}

// SetReadyBuild names the newer Djinn of version that waits, as a build: its commit, the branch and wish it comes
// from, how it compares with the running one. Nothing when another version waits now.
func (s *Service) SetReadyBuild(version string, build *uiv1.Build) {
	s.updates.Lock()
	defer s.updates.Unlock()
	if s.ready != version || proto.Equal(s.readyBuild, build) {
		return
	}
	s.readyBuild = build
	s.notifyUpdate()
}

// SetBuild proposes to install build, the last push of an integration branch, in place of the one proposed before
// for the same branch of the same project: each branch keeps its own, the branch the running Djinn was built from
// first, then those that carry its line on (W527), then the parallel ones. A build to install by itself (Build.auto)
// installs, once the machine is free; the others wait for the person's click.
func (s *Service) SetBuild(build *uiv1.Build) {
	if build == nil {
		return
	}
	s.updates.Lock()
	if i := slices.IndexFunc(s.builds, func(b *uiv1.Build) bool { return sameBranch(b, build) }); i >= 0 {
		s.builds[i] = build
	} else {
		s.builds = append(s.builds, build)
	}
	slices.SortStableFunc(s.builds, func(a, b *uiv1.Build) int { return cmp.Compare(lineRank(a), lineRank(b)) })
	// A failed install whose build is proposed no more is forgotten: its branch's newer build replaced it.
	if in := s.installing; in.GetStep() == uiv1.InstallStep_INSTALL_STEP_FAILED &&
		!slices.ContainsFunc(s.builds, func(b *uiv1.Build) bool { return b.GetSha() == in.GetSha() }) {
		s.installing = nil
	}
	s.notifyUpdate()
	s.updates.Unlock()
	if build.GetAuto() && s.Install != nil {
		s.autoInstall(build)
	}
}

// lineRank orders a build against the running Djinn: of its branch, then carrying its line on, then parallel.
func lineRank(b *uiv1.Build) int {
	switch {
	case b.GetRunning():
		return 0
	case b.GetContinues() != "":
		return 1
	}
	return 2
}

// sameBranch tells whether a and b are builds of the same integration branch of the same project.
func sameBranch(a, b *uiv1.Build) bool {
	return strings.EqualFold(a.GetProjectId(), b.GetProjectId()) && a.GetBranch() == b.GetBranch()
}

// updateState is what the window is told of the update now; s.updates is held.
func (s *Service) updateState() *uiv1.UiServiceWatchUpdateResponse {
	builds := make([]*uiv1.Build, len(s.builds))
	for i, b := range s.builds {
		builds[i] = proto.CloneOf(b)
	}
	return &uiv1.UiServiceWatchUpdateResponse{
		Current: s.Version, Ready: s.ready, NotResumed: slices.Clone(s.notResumed), NotesUrl: s.notesURL,
		Builds: builds, Installing: proto.CloneOf(s.installing), ReadyBuild: proto.CloneOf(s.readyBuild), Installed: s.installed,
	}
}

// setInstalling says where the install that runs now stands, or how the last one failed; nil for neither.
func (s *Service) setInstalling(in *uiv1.Installing) {
	s.updates.Lock()
	defer s.updates.Unlock()
	if proto.Equal(s.installing, in) {
		return
	}
	s.installing = in
	s.notifyUpdate()
}

// Ready is the version of the newer Djinn waiting; empty for none.
func (s *Service) Ready() string {
	s.updates.Lock()
	defer s.updates.Unlock()
	return s.ready
}

// SetNotResumed reports the terminals a restart could not run again.
func (s *Service) SetNotResumed(lines []string) {
	s.updates.Lock()
	defer s.updates.Unlock()
	s.notResumed = slices.Clone(lines)
	s.notifyUpdate()
}

// LastShow is the last wish and terminal the window was asked to show, however old; empty for none.
func (s *Service) LastShow() (wishID, terminal string) {
	s.shows.Lock()
	defer s.shows.Unlock()
	return s.lastShow.GetWishId(), s.lastShow.GetTerminal()
}

// notifyUpdate wakes the watchers of the update; s.updates is held.
func (s *Service) notifyUpdate() {
	if s.updated != nil {
		close(s.updated)
	}
	s.updated = make(chan struct{})
}

func (s *Service) WatchUpdate(
	ctx context.Context, _ *connect.Request[uiv1.UiServiceWatchUpdateRequest],
	stream *connect.ServerStream[uiv1.UiServiceWatchUpdateResponse],
) error {
	for {
		s.updates.Lock()
		if s.updated == nil {
			s.updated = make(chan struct{})
		}
		changed := s.updated
		msg := s.updateState()
		s.updates.Unlock()
		if err := stream.Send(msg); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-changed:
		}
	}
}

func (s *Service) Update(
	ctx context.Context, req *connect.Request[uiv1.UiServiceUpdateRequest],
) (*connect.Response[uiv1.UiServiceUpdateResponse], error) {
	installed := ""
	if sha := req.Msg.GetBuild(); sha != "" {
		s.updates.Lock()
		var build *uiv1.Build
		if i := slices.IndexFunc(s.builds, func(b *uiv1.Build) bool { return b.GetSha() == sha }); i >= 0 {
			build = s.builds[i]
		}
		s.updates.Unlock()
		switch {
		case build == nil:
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the build %s is not one proposed", sha))
		case s.Install == nil:
			return nil, connect.NewError(connect.CodeUnimplemented, errors.New("this djinn installs no build"))
		}
		// Logged as it comes: a call that never ends leaves no line of its own (calls log at their end).
		logx.L().Info("install asked", "sha", short(sha), "branch", build.GetBranch())
		newer, err := s.install(ctx, build, false)
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("install %s: %w", build.GetBranch(), err))
		}
		installed = sha
		if !newer || s.Restart == nil {
			// The build installed no newer Djinn at this one's path: the project is not Djinn, nothing restarts.
			return connect.NewResponse(&uiv1.UiServiceUpdateResponse{Installed: installed}), nil
		}
		s.setInstalling(&uiv1.Installing{Sha: sha, Step: uiv1.InstallStep_INSTALL_STEP_RESTARTING})
	}
	if s.Restart == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("this djinn cannot update itself"))
	}
	version, terminals, err := s.Restart()
	if err != nil {
		s.setInstalling(nil)
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&uiv1.UiServiceUpdateResponse{Version: version, Terminals: int32(terminals), Installed: installed}), nil
}

// watchShows registers a window that starts watching: ch gets each request to show something from now on, show is
// the one of the last minute to replay first (nil for none, or when its wish is gone), stop ends the watch.
func (s *Service) watchShows(ctx context.Context) (
	ch chan *uiv1.UiServiceWatchShowResponse, show *uiv1.UiServiceWatchShowResponse, stop func(),
) {
	recent := func() *uiv1.UiServiceWatchShowResponse {
		if s.lastShow != nil && time.Since(s.lastAt) < replay {
			return s.lastShow
		}
		return nil
	}
	// The store is read out of the lock; a request made meanwhile is newer, and replayed whatever it names.
	s.shows.Lock()
	last := recent()
	s.shows.Unlock()
	gone := last != nil && last.GetWishId() != "" && s.Known != nil && !s.Known(ctx, last.GetWishId())
	ch = make(chan *uiv1.UiServiceWatchShowResponse, 8)
	s.shows.Lock()
	defer s.shows.Unlock()
	if s.watchers == nil {
		s.watchers = map[chan *uiv1.UiServiceWatchShowResponse]struct{}{}
	}
	s.watchers[ch] = struct{}{}
	if show = recent(); gone && show == last {
		show = nil
	}
	return ch, show, func() {
		s.shows.Lock()
		delete(s.watchers, ch)
		s.shows.Unlock()
	}
}

func (s *Service) WatchShow(
	ctx context.Context, _ *connect.Request[uiv1.UiServiceWatchShowRequest],
	stream *connect.ServerStream[uiv1.UiServiceWatchShowResponse],
) error {
	ch, show, stop := s.watchShows(ctx)
	defer stop()
	// The headers go out at once: the window knows it is watching before anything is asked.
	if err := stream.Send(nil); err != nil {
		return err
	}
	if show != nil {
		if err := stream.Send(show); err != nil {
			return err
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-ch:
			if err := stream.Send(msg); err != nil {
				return err
			}
		}
	}
}

func (s *Service) Watch(
	ctx context.Context, _ *connect.Request[uiv1.UiServiceWatchRequest],
	stream *connect.ServerStream[uiv1.UiServiceWatchResponse],
) error {
	ch, replayShow, stop := s.watchShows(ctx)
	defer stop()

	s.updates.Lock()
	if s.updated == nil {
		s.updated = make(chan struct{})
	}
	updateChanged := s.updated
	initialUpdate := s.updateState()
	s.updates.Unlock()

	initMsg := &uiv1.UiServiceWatchResponse{
		Show:   replayShow,
		Update: initialUpdate,
	}
	if err := stream.Send(initMsg); err != nil {
		return err
	}

	loadCh := make(chan *djinnv1.LoadServiceWatchResponse, 8)
	if s.WatchLoad != nil {
		loadCtx, cancelLoad := context.WithCancel(ctx)
		defer cancelLoad()
		go func() {
			defer logx.Crash("ui watch load")
			_ = s.WatchLoad(loadCtx, func(resp *djinnv1.LoadServiceWatchResponse) error {
				select {
				case <-loadCtx.Done():
					return loadCtx.Err()
				case loadCh <- resp:
					return nil
				}
			})
		}()
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case showMsg := <-ch:
			if err := stream.Send(&uiv1.UiServiceWatchResponse{Show: showMsg}); err != nil {
				return err
			}
		case <-updateChanged:
			s.updates.Lock()
			if s.updated == nil {
				s.updated = make(chan struct{})
			}
			updateChanged = s.updated
			updateMsg := s.updateState()
			s.updates.Unlock()
			if err := stream.Send(&uiv1.UiServiceWatchResponse{Update: updateMsg}); err != nil {
				return err
			}
		case loadMsg := <-loadCh:
			if err := stream.Send(&uiv1.UiServiceWatchResponse{Load: loadMsg}); err != nil {
				return err
			}
		}
	}
}

// notificationSettings is the pane of the system's settings for notifications, on macOS.
const notificationSettings = "x-apple.systempreferences:com.apple.Notifications-Settings.extension"

func (s *Service) GetNotifications(
	context.Context, *connect.Request[uiv1.UiServiceGetNotificationsRequest],
) (*connect.Response[uiv1.UiServiceGetNotificationsResponse], error) {
	access := s.Notices.Access()
	return connect.NewResponse(&uiv1.UiServiceGetNotificationsResponse{Access: access, Settings: s.hasSettings(access)}), nil
}

func (s *Service) RequestNotifications(
	context.Context, *connect.Request[uiv1.UiServiceRequestNotificationsRequest],
) (*connect.Response[uiv1.UiServiceRequestNotificationsResponse], error) {
	access := s.Notices.Request()
	return connect.NewResponse(&uiv1.UiServiceRequestNotificationsResponse{Access: access, Settings: s.hasSettings(access)}), nil
}

// hasSettings tells that OpenNotificationSettings opens the system's settings: on macOS, where it decides.
func (s *Service) hasSettings(access uiv1.NotificationAccess) bool {
	return runtime.GOOS == "darwin" && access != uiv1.NotificationAccess_NOTIFICATION_ACCESS_UNAVAILABLE
}

func (s *Service) OpenNotificationSettings(
	context.Context, *connect.Request[uiv1.UiServiceOpenNotificationSettingsRequest],
) (*connect.Response[uiv1.UiServiceOpenNotificationSettingsResponse], error) {
	if !s.hasSettings(s.Notices.Access()) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("no notification settings to open here"))
	}
	if err := s.Open(notificationSettings); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("open the notification settings: %w", err))
	}
	return connect.NewResponse(&uiv1.UiServiceOpenNotificationSettingsResponse{}), nil
}

func (s *Service) View(
	_ context.Context, req *connect.Request[uiv1.UiServiceViewRequest],
) (*connect.Response[uiv1.UiServiceViewResponse], error) {
	if s.Notices != nil {
		s.Notices.View(req.Msg.GetWishId())
	}
	// The theme the page shows, for the driven lead's tone: the last page to say it wins.
	if err := plan.SetShownTheme(s.Home, req.Msg.GetTheme()); err != nil {
		logx.L().Warn("keep the theme shown", "err", err)
	}
	return connect.NewResponse(&uiv1.UiServiceViewResponse{}), nil
}

func (s *Service) Touch(
	context.Context, *connect.Request[uiv1.UiServiceTouchRequest],
) (*connect.Response[uiv1.UiServiceTouchResponse], error) {
	if s.Touched != nil {
		s.Touched()
	}
	return connect.NewResponse(&uiv1.UiServiceTouchResponse{}), nil
}
