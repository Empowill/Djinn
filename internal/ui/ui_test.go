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

func TestHomeDefaultsToConfigDirectory(t *testing.T) {
	t.Setenv("DJINN_HOME", "")
	t.Setenv("HOME", "/somewhere")
	t.Setenv("USERPROFILE", "/somewhere")
	home, err := Home()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/somewhere", ".config", "djinn"); home != want {
		t.Fatalf("Home() = %q, want %q", home, want)
	}
}

func TestHomeOfADevelopmentBuild(t *testing.T) {
	t.Setenv("HOME", "/somewhere")
	t.Setenv("USERPROFILE", "/somewhere")
	t.Cleanup(func() { Develop = false })
	for _, c := range []struct {
		develop bool
		env     string
		want    string
	}{
		{true, "", filepath.Join("/somewhere", ".config", "djinn-dev")},
		{false, "", filepath.Join("/somewhere", ".config", "djinn")},
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

func TestStateRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t)

	empty, err := s.LoadState(ctx, connect.NewRequest(&uiv1.UiServiceLoadStateRequest{}))
	if err != nil || empty.Msg.GetStateJson() != "" {
		t.Fatalf("LoadState before any save = %q, %v; want empty", empty.Msg.GetStateJson(), err)
	}

	for _, state := range []string{`{"version":2,"tasks":[]}`, `{"version":2,"tasks":[{"id":"a"}]}`} {
		if _, err := s.SaveState(ctx, connect.NewRequest(&uiv1.UiServiceSaveStateRequest{StateJson: state})); err != nil {
			t.Fatal(err)
		}
		got, err := s.LoadState(ctx, connect.NewRequest(&uiv1.UiServiceLoadStateRequest{}))
		if err != nil || got.Msg.GetStateJson() != state {
			t.Fatalf("LoadState = %q, %v; want %q", got.Msg.GetStateJson(), err, state)
		}
	}

	// A reloaded service, as after a restart, reads the same state.
	again := &Service{Home: s.Home}
	got, err := again.LoadState(ctx, connect.NewRequest(&uiv1.UiServiceLoadStateRequest{}))
	if err != nil || !strings.Contains(got.Msg.GetStateJson(), `"id":"a"`) {
		t.Fatalf("LoadState after reload = %q, %v", got.Msg.GetStateJson(), err)
	}

	// Atomic: only the state file is left, readable by its owner only.
	entries, err := os.ReadDir(s.Home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != stateFile {
		t.Fatalf("data directory holds %v, want only %s", entries, stateFile)
	}
	info, err := os.Stat(filepath.Join(s.Home, stateFile))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestSaveStateRefusesNonObject(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t)
	const kept = `{"version":2}`
	if _, err := s.SaveState(ctx, connect.NewRequest(&uiv1.UiServiceSaveStateRequest{StateJson: kept})); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "null", "[]", "42", `{"version":`} {
		_, err := s.SaveState(ctx, connect.NewRequest(&uiv1.UiServiceSaveStateRequest{StateJson: bad}))
		if code(err) != connect.CodeInvalidArgument {
			t.Errorf("SaveState(%q) = %v, want invalid_argument", bad, err)
		}
	}
	got, _ := s.LoadState(ctx, connect.NewRequest(&uiv1.UiServiceLoadStateRequest{}))
	if got.Msg.GetStateJson() != kept {
		t.Fatalf("a refused save changed the state to %q", got.Msg.GetStateJson())
	}
}

func TestValidateProject(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t)
	validate := func(dir string) (*uiv1.UiServiceValidateProjectResponse, error) {
		res, err := s.ValidateProject(ctx, connect.NewRequest(&uiv1.UiServiceValidateProjectRequest{Directory: dir}))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}

	plain, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := validate(plain); err != nil || got.GetDirectory() != plain || got.GetGit() {
		t.Fatalf("plain directory = %v, %v; want %s without git", got, err, plain)
	}

	repo := filepath.Join(plain, "repo")
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := validate(repo); err != nil || !got.GetGit() {
		t.Fatalf("repository = %v, %v; want git", got, err)
	}
	if got, err := validate(sub); err != nil || !got.GetGit() {
		t.Fatalf("directory inside a repository = %v, %v; want git", got, err)
	}

	if runtime.GOOS != "windows" {
		link := filepath.Join(plain, "link")
		if err := os.Symlink(repo, link); err != nil {
			t.Fatal(err)
		}
		if got, err := validate(link); err != nil || got.GetDirectory() != repo {
			t.Fatalf("symbolic link = %v, %v; want resolved to %s", got, err, repo)
		}
	}

	file := filepath.Join(plain, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "relative/path", filepath.Join(plain, "missing"), file} {
		if _, err := validate(bad); code(err) != connect.CodeInvalidArgument {
			t.Errorf("ValidateProject(%q) = %v, want invalid_argument", bad, err)
		}
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

func TestNotifyQuestionShowsNothingWithoutError(t *testing.T) {
	s, _ := newService(t)
	res, err := s.NotifyQuestion(context.Background(), connect.NewRequest(&uiv1.UiServiceNotifyQuestionRequest{Title: "t"}))
	if err != nil || res.Msg.GetShown() {
		t.Fatalf("NotifyQuestion = %v, %v; want not shown, no error", res, err)
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

	stream, err := client.Watch(context.Background(), connect.NewRequest(&uiv1.UiServiceWatchRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	stream.Receive()
	if code(stream.Err()) != connect.CodeUnimplemented {
		t.Fatalf("Watch = %v, want unimplemented", stream.Err())
	}
}
