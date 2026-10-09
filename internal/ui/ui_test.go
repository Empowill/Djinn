package ui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/gen/go/ui/v1/uiv1connect"
)

func newService(t *testing.T) (*Service, *[]string) {
	t.Helper()
	t.Setenv("DJINN_HOME", t.TempDir())
	s, err := New("test")
	if err != nil {
		t.Fatal(err)
	}
	var opened []string
	s.Open = func(link string) error { opened = append(opened, link); return nil }
	return s, &opened
}

func code(err error) connect.Code {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Code()
	}
	return 0
}

// configHome points the system's place for user configuration at /somewhere and returns it: ~/.config on Linux,
// ~/Library/Application Support on macOS, %AppData% on Windows.
func configHome(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", "/somewhere")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AppData", `C:\somewhere\AppData\Roaming`)
	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/somewhere", ".config"); runtime.GOOS == "linux" && config != want {
		t.Fatalf("os.UserConfigDir() = %q, want %q", config, want)
	}
	return config
}

func TestHomeDefaultsToConfigDirectory(t *testing.T) {
	t.Setenv("DJINN_HOME", "")
	config := configHome(t)
	home, err := Home()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(config, "djinn"); home != want {
		t.Fatalf("Home() = %q, want %q", home, want)
	}
}

func TestHomeOfADevelopmentBuild(t *testing.T) {
	config := configHome(t)
	t.Cleanup(func() { Develop = false })
	for _, c := range []struct {
		develop bool
		env     string
		want    string
	}{
		{true, "", filepath.Join(config, "djinn-dev")},
		{false, "", filepath.Join(config, "djinn")},
		// DJINN_HOME decides, development build or not.
		{true, "/data/mine", "/data/mine"},
	} {
		Develop = c.develop
		t.Setenv("DJINN_HOME", c.env)
		if home, err := Home(); err != nil || home != c.want {
			t.Errorf("Develop %v, DJINN_HOME %q: Home() = %q, %v; want %q", c.develop, c.env, home, err, c.want)
		}
	}
}

func TestShow(t *testing.T) {
	s, _ := newService(t)
	raised := 0
	s.Raise = func() { raised++ }
	s.Window = true
	mux := http.NewServeMux()
	mux.Handle(uiv1connect.NewUiServiceHandler(s))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := uiv1connect.NewUiServiceClient(srv.Client(), srv.URL)
	ctx := t.Context()

	// Showing only the window raises it, and asks the page for nothing.
	res, err := client.Show(ctx, connect.NewRequest(&uiv1.UiServiceShowRequest{}))
	if err != nil || !res.Msg.GetWindow() || raised != 1 {
		t.Fatalf("show: %v, %v, raised %d", res, err, raised)
	}
	// A request made before the window watches reaches it when it does.
	if _, err := client.Show(ctx, connect.NewRequest(&uiv1.UiServiceShowRequest{WishId: "w1", Terminal: "lead-w1"})); err != nil {
		t.Fatal(err)
	}
	stream, err := client.WatchShow(ctx, connect.NewRequest(&uiv1.UiServiceWatchShowRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stream.Close() })
	next := func() *uiv1.UiServiceWatchShowResponse {
		t.Helper()
		if !stream.Receive() {
			t.Fatalf("the stream ended: %v", stream.Err())
		}
		return stream.Msg()
	}
	if got := next(); got.GetWishId() != "w1" || got.GetTerminal() != "lead-w1" {
		t.Fatalf("first message = %v, want the earlier request", got)
	}
	// Then each request as it comes.
	s.Present("w2", "lead-w2")
	if got := next(); got.GetWishId() != "w2" || got.GetTerminal() != "lead-w2" {
		t.Fatalf("next message = %v", got)
	}
	// An old request is not replayed.
	s.shows.Lock()
	s.lastAt = s.lastAt.Add(-2 * replay)
	s.shows.Unlock()
	late, err := client.WatchShow(ctx, connect.NewRequest(&uiv1.UiServiceWatchShowRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { late.Close() })
	got := make(chan string, 1)
	go func() {
		if late.Receive() {
			got <- late.Msg().GetWishId()
		}
	}()
	select {
	case id := <-got:
		t.Fatalf("an old request was replayed: %s", id)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestOpenExternal(t *testing.T) {
	ctx := context.Background()
	s, opened := newService(t)
	for _, good := range []string{"https://example.com/a?b=c", "http://127.0.0.1:8080/"} {
		res, err := s.OpenExternal(ctx, connect.NewRequest(&uiv1.UiServiceOpenExternalRequest{Url: good}))
		if err != nil || res.Msg.GetUrl() != good {
			t.Errorf("OpenExternal(%q) = %v, %v", good, res, err)
		}
	}
	bad := []string{
		"", "file:///etc/passwd", "javascript:alert(1)", "ftp://example.com", "https://user:pw@example.com",
		"https:///no-host", "example.com", "https://example.com/" + strings.Repeat("a", 2048),
	}
	for _, link := range bad {
		_, err := s.OpenExternal(ctx, connect.NewRequest(&uiv1.UiServiceOpenExternalRequest{Url: link}))
		if code(err) != connect.CodeInvalidArgument {
			t.Errorf("OpenExternal(%q) = %v, want invalid_argument", link, err)
		}
	}
	if len(*opened) != 2 {
		t.Fatalf("opened %v, want only the two http(s) links", *opened)
	}
}

// The browser has no folder dialog: the page hides its button, and the method says so.
func TestChooseDirectoryWithoutADialog(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t)
	env, err := s.GetEnvironment(ctx, connect.NewRequest(&uiv1.UiServiceGetEnvironmentRequest{}))
	if err != nil || env.Msg.GetFolderDialog() {
		t.Fatalf("GetEnvironment = %v, %v; want no folder dialog", env, err)
	}
	_, err = s.ChooseDirectory(ctx, connect.NewRequest(&uiv1.UiServiceChooseDirectoryRequest{}))
	if code(err) != connect.CodeUnimplemented {
		t.Fatalf("ChooseDirectory = %v, want unimplemented", err)
	}
}

// The window's dialog, faked: it opens where the field points, or at home, and its answer comes back as it is.
func TestChooseDirectory(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := t.TempDir()
	var title, start string
	answer, fail := "/chosen/folder", error(nil)
	s.ChooseFolder = func(named, dir string) (string, error) {
		title, start = named, dir
		return answer, fail
	}
	env, err := s.GetEnvironment(ctx, connect.NewRequest(&uiv1.UiServiceGetEnvironmentRequest{}))
	if err != nil || !env.Msg.GetFolderDialog() {
		t.Fatalf("GetEnvironment = %v, %v; want a folder dialog", env, err)
	}
	choose := func(dir string) (string, error) {
		res, err := s.ChooseDirectory(ctx, connect.NewRequest(&uiv1.UiServiceChooseDirectoryRequest{
			Title: "Choose a folder", Directory: dir,
		}))
		if err != nil {
			return "", err
		}
		return res.Msg.GetDirectory(), nil
	}

	if got, err := choose(project); err != nil || got != answer || title != "Choose a folder" || start != project {
		t.Fatalf("ChooseDirectory(%q) = %q, %v; dialog %q in %q", project, got, err, title, start)
	}
	// Empty, relative, missing or a file: the dialog opens at home.
	file := filepath.Join(project, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"", "code/app", filepath.Join(project, "missing"), file} {
		if _, err := choose(dir); err != nil || start != home {
			t.Errorf("ChooseDirectory(%q): dialog in %q, %v; want home %q", dir, start, err, home)
		}
	}
	// Cancelled: an empty folder, no error.
	answer = ""
	if got, err := choose(project); err != nil || got != "" {
		t.Errorf("cancelled: ChooseDirectory = %q, %v; want empty", got, err)
	}
	fail = errors.New("no display")
	if _, err := choose(project); code(err) != connect.CodeInternal {
		t.Errorf("failed dialog: ChooseDirectory = %v, want internal", err)
	}
}

// A second dialog waits for none: while one is open, another request is refused at once.
func TestChooseDirectoryOneAtATime(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t)
	opened, release := make(chan struct{}), make(chan struct{})
	s.ChooseFolder = func(string, string) (string, error) {
		close(opened)
		<-release
		return "/first", nil
	}
	first := make(chan error, 1)
	go func() {
		_, err := s.ChooseDirectory(ctx, connect.NewRequest(&uiv1.UiServiceChooseDirectoryRequest{}))
		first <- err
	}()
	<-opened
	_, err := s.ChooseDirectory(ctx, connect.NewRequest(&uiv1.UiServiceChooseDirectoryRequest{}))
	if code(err) != connect.CodeAborted {
		t.Errorf("second ChooseDirectory = %v, want aborted", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first ChooseDirectory = %v", err)
	}
}

// The handler the server mounts answers over HTTP, and Watch is left to the server.
func TestHandler(t *testing.T) {
	s, _ := newService(t)
	path, handler := uiv1connect.NewUiServiceHandler(s)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := uiv1connect.NewUiServiceClient(server.Client(), server.URL)

	env, err := client.GetEnvironment(context.Background(), connect.NewRequest(&uiv1.UiServiceGetEnvironmentRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if env.Msg.GetVersion() != "test" || env.Msg.GetPlatform() == "" || len(env.Msg.GetProviders()) != 2 {
		t.Fatalf("GetEnvironment = %v", env.Msg)
	}
}

// TestUpdateInstallsABuild: a build committed is proposed to the window; installing it runs its install command, then
// restarts on the newer Djinn it installed, or restarts nothing when it installed none. Only the build proposed
// installs.
func TestUpdateInstallsABuild(t *testing.T) {
	s, _ := newService(t)
	var installed []string
	newer, restarts := false, 0
	s.Install = func(_ context.Context, b *uiv1.Build) (bool, error) {
		installed = append(installed, b.GetSha())
		return newer, nil
	}
	s.Restart = func() (string, int, error) { restarts++; return "v2", 1, nil }
	s.SetBuild(&uiv1.Build{WishId: "w", ProjectId: "p", Branch: "feat/x", Sha: "abc", Changes: []string{"Work of W1"}})

	path, handler := uiv1connect.NewUiServiceHandler(s)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := uiv1connect.NewUiServiceClient(server.Client(), server.URL)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	watch, err := client.WatchUpdate(ctx, connect.NewRequest(&uiv1.UiServiceWatchUpdateRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !watch.Receive() || watch.Msg().GetBuild().GetSha() != "abc" || watch.Msg().GetBuild().GetChanges()[0] != "Work of W1" {
		t.Fatalf("the window is not proposed the build: %v, %v", watch.Msg(), watch.Err())
	}

	update := func(sha string) (*uiv1.UiServiceUpdateResponse, error) {
		res, err := s.Update(t.Context(), connect.NewRequest(&uiv1.UiServiceUpdateRequest{Build: sha}))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}
	if _, err := update("other"); code(err) != connect.CodeFailedPrecondition || len(installed) != 0 {
		t.Errorf("a build not proposed: %v, installed %v", err, installed)
	}
	res, err := update("abc")
	if err != nil || res.GetInstalled() != "abc" || res.GetVersion() != "" || restarts != 0 {
		t.Errorf("a build that installed no newer Djinn: %v, %v, %d restarts", res, err, restarts)
	}
	if !watch.Receive() || watch.Msg().GetBuild() != nil {
		t.Errorf("still proposed once installed: %v, %v", watch.Msg(), watch.Err())
	}
	newer = true
	s.SetBuild(&uiv1.Build{Sha: "def"})
	if res, err := update("def"); err != nil || res.GetInstalled() != "def" || res.GetVersion() != "v2" || restarts != 1 {
		t.Errorf("a build that installed a newer Djinn: %v, %v, %d restarts", res, err, restarts)
	}
}
