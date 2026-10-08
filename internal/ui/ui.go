// Package ui implements UiService, the API the window calls. It writes nothing outside the data directory.
package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"connectrpc.com/connect"

	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/gen/go/ui/v1/uiv1connect"
)

const stateFile = "state.json"

// Service implements uiv1connect.UiServiceHandler. Watch is not implemented here.
type Service struct {
	uiv1connect.UnimplementedUiServiceHandler

	// Home is the data directory: $DJINN_HOME, by default ~/.config/djinn.
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
	// AgentPath is the PATH where GetEnvironment looks for the agents, and nowhere else. Nil: searchPath, which adds
	// the login shell's PATH and the installers' folders to Djinn's own, then the applications that ship an agent.
	// Tests replace it.
	AgentPath func() string
	// ChooseFolder opens the system's folder dialog over the window, titled title and open in directory, and returns
	// the folder chosen, or empty when the user cancelled. Nil: the page has no folder dialog (the browser).
	ChooseFolder func(title, directory string) (string, error)
	// Notices shows the system notifications, and knows whether the system lets it. Nil: none (a test).
	Notices *Notices

	mu      sync.Mutex // Serializes the writes of the state.
	dialogs sync.Mutex // One folder dialog at a time.

	shows    sync.Mutex
	lastShow *uiv1.UiServiceWatchShowResponse
	lastAt   time.Time
	watchers map[chan *uiv1.UiServiceWatchShowResponse]struct{}

	updates    sync.Mutex
	ready      string        // version of the newer Djinn waiting; empty for none
	notResumed []string      // terminals the last restart could not run again
	updated    chan struct{} // closed and replaced at each change
}

// replay is how long a request to show something waits for a window that opens after it: djinn wish resume may
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

// Develop is set by a development build of djinn, one built from a checkout rather than installed at a version: its
// data directory is then djinn-dev, so that the djinn being built never reaches the user's own djinn, its window,
// its socket or its wishes. DJINN_HOME still decides when it is set.
var Develop bool

// Home is the data directory: $DJINN_HOME, by default ~/.config/djinn (djinn-dev for a development build).
func Home() (string, error) {
	if home := os.Getenv("DJINN_HOME"); home != "" {
		return home, nil
	}
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
	return filepath.Join(config, name), nil
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
	if req.Msg.GetAgents() {
		if s.AgentPath != nil {
			res.Providers = checkAgents(ctx, s.AgentPath(), false)
		} else {
			res.Providers = checkAgents(ctx, searchPath(), true)
		}
	}
	return connect.NewResponse(res), nil
}

func (s *Service) LoadState(
	context.Context, *connect.Request[uiv1.UiServiceLoadStateRequest],
) (*connect.Response[uiv1.UiServiceLoadStateResponse], error) {
	data, err := os.ReadFile(filepath.Join(s.Home, stateFile))
	if errors.Is(err, fs.ErrNotExist) {
		return connect.NewResponse(&uiv1.UiServiceLoadStateResponse{}), nil
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read the state: %w", err))
	}
	return connect.NewResponse(&uiv1.UiServiceLoadStateResponse{StateJson: string(data)}), nil
}

func (s *Service) SaveState(
	_ context.Context, req *connect.Request[uiv1.UiServiceSaveStateRequest],
) (*connect.Response[uiv1.UiServiceSaveStateResponse], error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(req.Msg.GetStateJson()), &object); err != nil || object == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("the state must be a JSON object"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := WriteAtomic(filepath.Join(s.Home, stateFile), []byte(req.Msg.GetStateJson())); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("save the state: %w", err))
	}
	return connect.NewResponse(&uiv1.UiServiceSaveStateResponse{}), nil
}

// WriteAtomic writes a temporary file next to path, readable by the owner only, then renames it over path: a
// reader sees the old content or the new one, never a part of it.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // Fails harmlessly once renamed.
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (s *Service) ValidateProject(
	_ context.Context, req *connect.Request[uiv1.UiServiceValidateProjectRequest],
) (*connect.Response[uiv1.UiServiceValidateProjectResponse], error) {
	invalid := func(err error) error {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("project directory: %w", err))
	}
	dir := req.Msg.GetDirectory()
	if dir == "" {
		return nil, invalid(errors.New("associate this project with a local directory first"))
	}
	if !filepath.IsAbs(dir) {
		return nil, invalid(errors.New("the path must be absolute"))
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, invalid(err)
	}
	f, err := os.Open(root)
	if err != nil {
		return nil, invalid(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, invalid(err)
	}
	if !info.IsDir() {
		return nil, invalid(errors.New("not a directory"))
	}
	if _, err := f.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return nil, invalid(err)
	}
	return connect.NewResponse(&uiv1.UiServiceValidateProjectResponse{Directory: root, Git: inGit(root)}), nil
}

// inGit tells whether dir is inside a Git repository: dir or a parent holds .git, a directory or, in a worktree
// or a submodule, a file.
func inGit(dir string) bool {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
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

// Present asks the windows to show a wish and a terminal: the ones watching now, and one that starts watching
// within a minute.
func (s *Service) Present(wishID, terminal string) { s.PresentAt(wishID, terminal, "") }

// PresentAt is Present with an element of the wish in view (UiServiceWatchShowResponse.target).
func (s *Service) PresentAt(wishID, terminal, target string) {
	msg := &uiv1.UiServiceWatchShowResponse{WishId: wishID, Terminal: terminal, Target: target}
	s.shows.Lock()
	defer s.shows.Unlock()
	s.lastShow, s.lastAt = msg, time.Now()
	for ch := range s.watchers {
		select {
		case ch <- msg:
		default: // A window that does not read keeps what it has: the next request matters more than this one.
		}
	}
}

// SetReady says that a newer Djinn of version waits at the path of the running one; empty says none does.
func (s *Service) SetReady(version string) {
	s.updates.Lock()
	defer s.updates.Unlock()
	if s.ready == version {
		return
	}
	s.ready = version
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
		msg := &uiv1.UiServiceWatchUpdateResponse{
			Current: s.Version, Ready: s.ready, NotResumed: slices.Clone(s.notResumed),
		}
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
	context.Context, *connect.Request[uiv1.UiServiceUpdateRequest],
) (*connect.Response[uiv1.UiServiceUpdateResponse], error) {
	if s.Restart == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("this djinn cannot update itself"))
	}
	version, terminals, err := s.Restart()
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&uiv1.UiServiceUpdateResponse{Version: version, Terminals: int32(terminals)}), nil
}

func (s *Service) WatchShow(
	ctx context.Context, _ *connect.Request[uiv1.UiServiceWatchShowRequest],
	stream *connect.ServerStream[uiv1.UiServiceWatchShowResponse],
) error {
	ch := make(chan *uiv1.UiServiceWatchShowResponse, 8)
	s.shows.Lock()
	if s.watchers == nil {
		s.watchers = map[chan *uiv1.UiServiceWatchShowResponse]struct{}{}
	}
	s.watchers[ch] = struct{}{}
	if s.lastShow != nil && time.Since(s.lastAt) < replay {
		ch <- s.lastShow
	}
	s.shows.Unlock()
	defer func() {
		s.shows.Lock()
		delete(s.watchers, ch)
		s.shows.Unlock()
	}()
	// The headers go out at once: the window knows it is watching before anything is asked.
	if err := stream.Send(nil); err != nil {
		return err
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
	return connect.NewResponse(&uiv1.UiServiceViewResponse{}), nil
}
