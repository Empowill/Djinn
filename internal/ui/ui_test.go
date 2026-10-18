package ui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/gen/go/ui/v1/uiv1connect"
	"github.com/empowill/djinn/internal/link"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
)

// newService is a service on a data directory of its own, built without New: no environment variable is set, so
// that the test may run in parallel. TestNew checks New.
func newService(t *testing.T) (*Service, *[]string) {
	t.Helper()
	s := &Service{Home: t.TempDir(), Version: "test"}
	// No test runs the real agents: none is found.
	empty := t.TempDir()
	s.AgentPath = func() string { return empty }
	var opened []string
	s.Open = func(link string) error { opened = append(opened, link); return nil }
	return s, &opened
}

// serve serves s over HTTP for the test and returns a client of it.
func serve(t *testing.T, s *Service) uiv1connect.UiServiceClient {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(uiv1connect.NewUiServiceHandler(s))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return uiv1connect.NewUiServiceClient(srv.Client(), srv.URL)
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

// TestNew: the service is on the data directory Home gives, and refuses the real one under test.
func TestNew(t *testing.T) {
	t.Run("on the data directory", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("DJINN_HOME", home)
		s, err := New("v1")
		if err != nil || s.Home != home || s.Version != "v1" || s.Open == nil {
			t.Fatalf("New = %+v, %v; want the service on %s", s, err, home)
		}
	})
	t.Run("refuses the real data directory under test", func(t *testing.T) {
		home := RealHome()
		if home == "" {
			t.Skip("no real config directory on this system")
		}
		t.Setenv("DJINN_HOME", home)
		if s, err := New("v1"); err == nil {
			t.Fatalf("New = %+v, want refused", s)
		}
	})
}

// TestHome: the data directory is DJINN_HOME, else djinn in the system's configuration folder (djinn-dev for a
// development build); under test, never the real one.
func TestHome(t *testing.T) {
	t.Cleanup(func() { Develop = false })
	for _, c := range []struct {
		name    string
		develop bool
		env     string
		want    string // under the configuration folder when DJINN_HOME is empty
	}{
		{"defaults to the configuration folder", false, "", "djinn"},
		{"of a development build", true, "", "djinn-dev"},
		{"DJINN_HOME decides, development build or not", true, "/data/mine", "/data/mine"},
	} {
		t.Run(c.name, func(t *testing.T) {
			config := configHome(t)
			Develop = c.develop
			t.Setenv("DJINN_HOME", c.env)
			want := c.want
			if c.env == "" {
				want = filepath.Join(config, c.want)
			}
			if home, err := Home(); err != nil || home != want {
				t.Errorf("Develop %v, DJINN_HOME %q: Home() = %q, %v; want %q", c.develop, c.env, home, err, want)
			}
		})
	}
	Develop = false
	t.Run("refuses the real data directory under test", func(t *testing.T) {
		if !InTest() {
			t.Fatal("InTest() should be true when running go test")
		}
		realHome := RealHome()
		if realHome == "" {
			t.Skip("no real config directory on this system")
		}
		for _, cand := range []string{realHome, "~/.config/djinn", "~/.config/djinn-dev"} {
			t.Setenv("DJINN_HOME", cand)
			home, err := Home()
			if err == nil {
				t.Errorf("DJINN_HOME=%q: Home() = %q, want refusal error", cand, home)
			} else if !strings.Contains(err.Error(), "refusing to use real data directory") {
				t.Errorf("DJINN_HOME=%q: error = %v, want refusal error", cand, err)
			}
		}
		tmp := t.TempDir()
		t.Setenv("DJINN_HOME", tmp)
		if got, err := Home(); err != nil || got != tmp {
			t.Fatalf("DJINN_HOME=%q: Home() = %q, %v", tmp, got, err)
		}
	})
}

// TestRealHome: the user's data directory is djinn in the configuration folder, djinn-dev for a development build. It
// sets Develop, a package variable: not parallel.
func TestRealHome(t *testing.T) {
	t.Cleanup(func() { Develop = false })
	for _, c := range []struct {
		name    string
		develop bool
		want    string
	}{
		{"an installed build", false, "djinn"},
		{"a development build", true, "djinn-dev"},
	} {
		t.Run(c.name, func(t *testing.T) {
			Develop = c.develop
			got := RealHome()
			if got == "" {
				t.Skip("no real config directory on this system")
			}
			if filepath.Base(got) != c.want || !IsRealHome(got) {
				t.Errorf("RealHome() = %q, want %s, a real home", got, c.want)
			}
		})
	}
}

// TestIsTestBuild: a go test binary is a test build; one stamped by task e2e is too. It sets isTestBuild, a package
// variable: not parallel.
func TestIsTestBuild(t *testing.T) {
	t.Cleanup(func() { isTestBuild = "" })
	for _, c := range []struct{ name, stamp string }{
		{"a go test binary", ""},
		{"a binary stamped as a test build", "true"},
	} {
		t.Run(c.name, func(t *testing.T) {
			isTestBuild = c.stamp
			if !IsTestBuild() {
				t.Error("IsTestBuild() = false")
			}
		})
	}
}

// TestStampedTestBuild: only a binary stamped when linked is, not a go test binary. It sets isTestBuild, a package
// variable: not parallel.
func TestStampedTestBuild(t *testing.T) {
	t.Cleanup(func() { isTestBuild = "" })
	for _, c := range []struct {
		name, stamp string
		want        bool
	}{
		{"a go test binary", "", false},
		{"a binary stamped as a test build", "true", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			isTestBuild = c.stamp
			if got := StampedTestBuild(); got != c.want {
				t.Errorf("StampedTestBuild() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestShow: showing the window raises it; a request to show something reaches the windows watching, and one that
// starts watching within the minute, unless its wish is gone.
func TestShow(t *testing.T) {
	t.Parallel()
	t.Run("raises the window and shows what it is asked", func(t *testing.T) {
		t.Parallel()
		s, _ := newService(t)
		raised := 0
		s.Raise = func() { raised++ }
		s.Window = true
		client := serve(t, s)
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
		// A click on a notification: the wish, there.
		s.PresentAt("w3", "", "question-q1")
		if got := next(); got.GetWishId() != "w3" || got.GetTarget() != "question-q1" {
			t.Fatalf("message with a target = %v", got)
		}
		// A tilasm, in its wish's Tilasms tab.
		s.PresentTilasm("w4", "t1")
		if got := next(); got.GetWishId() != "w4" || got.GetTilasmId() != "t1" {
			t.Fatalf("message with a tilasm = %v", got)
		}
		if wish, terminal := s.LastShow(); wish != "w4" || terminal != "" {
			t.Errorf("LastShow() = %q, %q; want the tilasm's wish", wish, terminal)
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
		if wish, _ := s.LastShow(); wish != "w4" {
			t.Errorf("LastShow() = %q; want the last show however old", wish)
		}
	})
	// W501: a spec opened a wish, then deleted it; a reload within the minute got that show again, and the window left
	// the wish it showed for the flying carpet. A show whose wish is gone is not replayed; one of a wish still here is.
	t.Run("forgets the show of a deleted wish", func(t *testing.T) {
		t.Parallel()
		s, _ := newService(t)
		deleted := map[string]bool{"gone": true}
		s.Known = func(_ context.Context, id string) bool { return !deleted[id] }
		client := serve(t, s)
		ctx := t.Context()

		// replayed is the show a window that starts watching now gets first, from Watch and from WatchShow.
		replayed := func() (fromWatch, fromWatchShow string) {
			t.Helper()
			watch, err := client.Watch(ctx, connect.NewRequest(&uiv1.UiServiceWatchRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			if !watch.Receive() {
				t.Fatalf("Watch ended: %v", watch.Err())
			}
			fromWatch = watch.Msg().GetShow().GetWishId()
			show, err := client.WatchShow(ctx, connect.NewRequest(&uiv1.UiServiceWatchShowRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			defer show.Close()
			got := make(chan string, 1)
			go func() {
				if show.Receive() {
					got <- show.Msg().GetWishId()
				}
			}()
			select {
			case fromWatchShow = <-got:
			case <-time.After(200 * time.Millisecond):
			}
			return fromWatch, fromWatchShow
		}

		s.Present("gone", "")
		if w, ws := replayed(); w != "" || ws != "" {
			t.Fatalf("a deleted wish's show was replayed: Watch %q, WatchShow %q", w, ws)
		}
		s.Present("here", "lead")
		if w, ws := replayed(); w != "here" || ws != "here" {
			t.Fatalf("replayed Watch %q, WatchShow %q, want the wish still here", w, ws)
		}
		// A show of no wish (a terminal alone) is replayed whatever Known says.
		s.Present("", "lead")
		watch, err := client.Watch(ctx, connect.NewRequest(&uiv1.UiServiceWatchRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		defer watch.Close()
		if !watch.Receive() || watch.Msg().GetShow().GetTerminal() != "lead" {
			t.Fatalf("a terminal's show was not replayed: %v, %v", watch.Msg(), watch.Err())
		}
	})
}

// TestOpenExternal: only an http or https link with a host and no password, of a sane length, opens.
func TestOpenExternal(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, url string
		opens     bool
	}{
		{"an https link", "https://example.com/a?b=c", true},
		{"a local http link", "http://127.0.0.1:8080/", true},
		{"no link", "", false},
		{"a file", "file:///etc/passwd", false},
		{"a script", "javascript:alert(1)", false},
		{"ftp", "ftp://example.com", false},
		{"a password", "https://user:pw@example.com", false},
		{"no host", "https:///no-host", false},
		{"no scheme", "example.com", false},
		{"too long", "https://example.com/" + strings.Repeat("a", 2048), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s, opened := newService(t)
			res, err := s.OpenExternal(context.Background(), connect.NewRequest(&uiv1.UiServiceOpenExternalRequest{Url: c.url}))
			switch {
			case c.opens && (err != nil || res.Msg.GetUrl() != c.url || !slices.Equal(*opened, []string{c.url})):
				t.Errorf("OpenExternal(%q) = %v, %v; opened %v", c.url, res, err, *opened)
			case !c.opens && (code(err) != connect.CodeInvalidArgument || len(*opened) != 0):
				t.Errorf("OpenExternal(%q) = %v, want invalid_argument; opened %v", c.url, err, *opened)
			}
		})
	}
}

// TestChooseDirectory: the window's folder dialog, one at a time; the browser has none.
func TestChooseDirectory(t *testing.T) {
	// The browser has no folder dialog: the page hides its button, and the method says so.
	t.Run("the browser has no folder dialog", func(t *testing.T) {
		t.Parallel()
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
	})
	// The window's dialog, faked: it opens where the field points, or at home, and its answer comes back as it is.
	t.Run("the dialog opens where the field points, else at home", func(t *testing.T) {
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
	})
	// A second dialog waits for none: while one is open, another request is refused at once.
	t.Run("one dialog at a time", func(t *testing.T) {
		t.Parallel()
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
	})
}

// TestOpenLink: a Djinn link shows what it names, the window in front; one Djinn does not know is refused, and the
// window says so.
func TestOpenLink(t *testing.T) {
	t.Parallel()
	s, _ := newService(t)
	raised := 0
	s.Raise = func() { raised++ }
	s.Window = true
	const tilasm, wish = "01a1223a-ae45-728f-8c37-c005eee91edb", "01a11833-a440-7479-a067-52615c91da70"
	s.Linked = func(_ context.Context, l link.Link) (string, error) {
		if l.ID == tilasm || l.ID == wish {
			return wish, nil
		}
		return "", connect.NewError(connect.CodeNotFound, errors.New("no tilasm "+l.ID))
	}
	client := serve(t, s)
	ctx := t.Context()
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
	open := func(url string) (*uiv1.UiServiceOpenLinkResponse, error) {
		res, err := client.OpenLink(ctx, connect.NewRequest(&uiv1.UiServiceOpenLinkRequest{Url: url}))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}

	// A tilasm's link shows its wish's Tilasms tab on it, the window in front.
	res, err := open("djinn://tilasm/" + strings.ToUpper(tilasm))
	if err != nil || res.GetWishId() != wish || res.GetTilasmId() != tilasm || !res.GetWindow() || raised != 1 {
		t.Fatalf("a tilasm's link: %v, %v, raised %d", res, err, raised)
	}
	if got := next(); got.GetWishId() != wish || got.GetTilasmId() != tilasm || got.GetUnknownLink() != "" {
		t.Fatalf("shown: %v", got)
	}
	// A wish's link, the wish.
	if res, err = open("djinn://wish/" + wish); err != nil || res.GetWishId() != wish || res.GetTilasmId() != "" {
		t.Fatalf("a wish's link: %v, %v", res, err)
	}
	if got := next(); got.GetWishId() != wish || got.GetTilasmId() != "" {
		t.Fatalf("shown: %v", got)
	}
	// A link Djinn does not know, or whose tilasm is not here: refused, and the window says so.
	for _, c := range []struct {
		url  string
		code connect.Code
	}{
		{"djinn://moon/" + wish, connect.CodeInvalidArgument},
		{"djinn://tilasm/01a1223a-ae45-728f-8c37-000000000000", connect.CodeNotFound},
	} {
		if _, err := open(c.url); code(err) != c.code {
			t.Errorf("%s: %v, want %v", c.url, err, c.code)
		}
		if got := next(); got.GetUnknownLink() != c.url || got.GetWishId() != "" {
			t.Errorf("%s: shown %v", c.url, got)
		}
	}
	if raised != 4 {
		t.Errorf("raised %d times, want 4: the window comes forward to say so", raised)
	}
}

// TestUpdate: a build committed is proposed to the window; installing it runs its install command, then restarts on
// the newer Djinn it installed, or restarts nothing when it installed none. Only the build proposed installs. While it
// installs, the window is told where it stands: what it waits for, that it builds, that Djinn restarts.
func TestUpdate(t *testing.T) {
	t.Parallel()
	s, _ := newService(t)
	// The install runs in a goroutine of its own: what it shares with the test is under mu.
	var mu sync.Mutex
	var installed []string
	newer, restarts, restartBy := false, 0, ""
	proceed := make(chan struct{}) // the first install waits until the window has seen it wait
	wait := proceed
	s.Install = func(_ context.Context, b *uiv1.Build, run InstallRun) (bool, string, error) {
		mu.Lock()
		first := wait
		wait = nil
		mu.Unlock()
		if first != nil {
			run.Progress(uiv1.InstallStep_INSTALL_STEP_WAITING, "gate install: held by W1")
			<-first
		}
		run.Progress(uiv1.InstallStep_INSTALL_STEP_BUILDING, "")
		mu.Lock()
		defer mu.Unlock()
		installed = append(installed, b.GetSha())
		return newer, "", nil
	}
	s.Restart = func(by string) (string, int, error) {
		mu.Lock()
		defer mu.Unlock()
		restarts++
		restartBy = by
		return "v2", 1, nil
	}
	counts := func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		return len(installed), restarts
	}
	s.SetBuild(&uiv1.Build{WishId: "w", ProjectId: "p", Branch: "feat/x", Sha: "abc", Changes: []string{"Work of W1"}})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	watch, err := serve(t, s).WatchUpdate(ctx, connect.NewRequest(&uiv1.UiServiceWatchUpdateRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !watch.Receive() || len(watch.Msg().GetBuilds()) != 1 || watch.Msg().GetBuilds()[0].GetSha() != "abc" ||
		watch.Msg().GetBuilds()[0].GetChanges()[0] != "Work of W1" {
		t.Fatalf("the window is not proposed the build: %v, %v", watch.Msg(), watch.Err())
	}

	update := func(sha string) (*uiv1.UiServiceUpdateResponse, error) {
		res, err := s.Update(t.Context(), connect.NewRequest(&uiv1.UiServiceUpdateRequest{Build: sha}))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}
	if _, err := update("other"); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("a build not proposed: %v", err)
	}
	if n, _ := counts(); n != 0 {
		t.Errorf("a build not proposed: installed %d", n)
	}
	type result struct {
		res *uiv1.UiServiceUpdateResponse
		err error
	}
	done := make(chan result, 1)
	go func() { res, err := update("abc"); done <- result{res, err} }()
	// It prepares, maybe, then waits.
	for watch.Receive() && watch.Msg().GetInstalling().GetStep() == uiv1.InstallStep_INSTALL_STEP_PREPARING {
	}
	if watch.Msg().GetInstalling().GetStep() != uiv1.InstallStep_INSTALL_STEP_WAITING ||
		watch.Msg().GetInstalling().GetWaiting() != "gate install: held by W1" || watch.Msg().GetInstalling().GetSha() != "abc" {
		t.Errorf("the window is not told what the install waits for: %v, %v", watch.Msg(), watch.Err())
	}
	close(proceed)
	r := <-done
	if _, n := counts(); r.err != nil || r.res.GetInstalled() != "abc" || r.res.GetVersion() != "" || n != 0 {
		t.Errorf("a build that installed no newer Djinn: %v, %v, %d restarts", r.res, r.err, n)
	}
	// The window sees it build, maybe, then the install over: nothing proposed, nothing installing.
	for watch.Receive() && (len(watch.Msg().GetBuilds()) > 0 || watch.Msg().GetInstalling() != nil) {
		if step := watch.Msg().GetInstalling().GetStep(); step != uiv1.InstallStep_INSTALL_STEP_BUILDING &&
			step != uiv1.InstallStep_INSTALL_STEP_WAITING {
			t.Errorf("installing: %v", watch.Msg())
		}
	}
	if watch.Err() != nil {
		t.Errorf("still proposed or installing once installed: %v", watch.Err())
	}
	mu.Lock()
	newer = true
	mu.Unlock()
	s.SetBuild(&uiv1.Build{ProjectId: "p", Branch: "feat/x", Sha: "def"})
	res, err := update("def")
	if _, n := counts(); err != nil || res.GetInstalled() != "def" || res.GetVersion() != "v2" || n != 1 {
		t.Errorf("a build that installed a newer Djinn: %v, %v, %d restarts", res, err, n)
	}
	mu.Lock()
	by := restartBy
	mu.Unlock()
	if by != "an install from the command line (djinn update)" {
		t.Errorf("restart by %q", by)
	}
	restarting := false
	for !restarting && watch.Receive() {
		restarting = watch.Msg().GetInstalling().GetStep() == uiv1.InstallStep_INSTALL_STEP_RESTARTING
	}
	if !restarting {
		t.Errorf("the window is not told Djinn restarts: %v", watch.Err())
	}
}

// TestSetBuild: each integration branch keeps its own proposal, a newer push of a branch replaces only that branch's;
// the branch the running Djinn was built from comes first; installing one leaves the others.
func TestSetBuild(t *testing.T) {
	t.Parallel()
	s, _ := newService(t)
	s.Install = func(context.Context, *uiv1.Build, InstallRun) (bool, string, error) { return false, "", nil }
	shas := func() []string {
		var out []string
		for _, b := range state(s).GetBuilds() {
			out = append(out, b.GetBranch()+"@"+b.GetSha())
		}
		return out
	}
	s.SetBuild(nil) // Nothing to propose.
	s.SetBuild(&uiv1.Build{ProjectId: "p", Branch: "feat/clement-port", WishTitle: "Clement's branches", Sha: "e60f8fc"})
	s.SetBuild(&uiv1.Build{ProjectId: "p", Branch: "feat/wails-go", Sha: "aaa", Running: true})
	s.SetBuild(&uiv1.Build{ProjectId: "q", Branch: "feat/wails-go", Sha: "ccc"})
	if got := shas(); !slices.Equal(got, []string{"feat/wails-go@aaa", "feat/clement-port@e60f8fc", "feat/wails-go@ccc"}) {
		t.Errorf("builds = %v, want the running branch first, one per branch and project", got)
	}
	// W527: a branch that carries the running line on comes after it, before the parallel ones.
	s.SetBuild(&uiv1.Build{ProjectId: "p", Branch: "feat/robustness", Sha: "bbb", Continues: "feat/wails-go"})
	if got := shas(); !slices.Equal(got, []string{"feat/wails-go@aaa", "feat/robustness@bbb", "feat/clement-port@e60f8fc", "feat/wails-go@ccc"}) {
		t.Errorf("builds = %v, want the line carried on second", got)
	}
	s.SetBuild(&uiv1.Build{ProjectId: "p", Branch: "feat/clement-port", Sha: "e70"})
	if got := shas(); !slices.Equal(got, []string{"feat/wails-go@aaa", "feat/robustness@bbb", "feat/clement-port@e70", "feat/wails-go@ccc"}) {
		t.Errorf("builds = %v, want the newer push of feat/clement-port in place of its last", got)
	}
	if _, err := s.Update(t.Context(), connect.NewRequest(&uiv1.UiServiceUpdateRequest{Build: "e70"})); err != nil {
		t.Fatal(err)
	}
	if got := shas(); !slices.Equal(got, []string{"feat/wails-go@aaa", "feat/robustness@bbb", "feat/wails-go@ccc"}) {
		t.Errorf("builds = %v, want the others kept once one is installed", got)
	}
	if _, err := s.Update(t.Context(), connect.NewRequest(&uiv1.UiServiceUpdateRequest{Build: "e60f8fc"})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("a build replaced since installs: %v", err)
	}
}

// TestSetReadyBuild: the newer Djinn ready is named once described, and forgotten when another version waits.
func TestSetReadyBuild(t *testing.T) {
	t.Parallel()
	s, _ := newService(t)
	ready := func() *uiv1.Build { return state(s).GetReadyBuild() }
	s.SetReady("local-g1", "", true)
	s.SetReadyBuild("local-g0", &uiv1.Build{Sha: "0"})
	if ready() != nil {
		t.Error("a version no longer waiting is named")
	}
	s.SetReadyBuild("local-g1", &uiv1.Build{Sha: "1", Branch: "feat/clement-port"})
	if ready().GetBranch() != "feat/clement-port" {
		t.Errorf("ready build = %v", ready())
	}
	s.SetReady("local-g2", "", true)
	if ready() != nil {
		t.Error("the name of the version before stays")
	}
	if got := s.Ready(); got != "local-g2" {
		t.Errorf("Ready() = %q, want the version waiting now", got)
	}
}

// TestSetNotResumed: the terminals a restart could not run again reach the window with the update, as they were said.
func TestSetNotResumed(t *testing.T) {
	t.Parallel()
	s, _ := newService(t)
	lines := []string{"lead of W1: its session is gone"}
	s.SetNotResumed(lines)
	lines[0] = "changed after"
	if got := state(s).GetNotResumed(); !slices.Equal(got, []string{"lead of W1: its session is gone"}) {
		t.Errorf("not resumed = %v", got)
	}
}

// TestWatch: the window's one stream says the update first, then each show, update and load as they come.
func TestWatch(t *testing.T) {
	t.Parallel()
	t.Run("the update, then each show and update", func(t *testing.T) {
		t.Parallel()
		s, _ := newService(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		stream, err := serve(t, s).Watch(ctx, connect.NewRequest(&uiv1.UiServiceWatchRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { stream.Close() })

		// First message contains initial update.
		if !stream.Receive() {
			t.Fatalf("first message failed: %v", stream.Err())
		}
		if got := stream.Msg(); got.GetUpdate() == nil || got.GetUpdate().GetCurrent() != "test" {
			t.Fatalf("initial message update = %v, want current 'test'", got.GetUpdate())
		}
		s.Present("w-1", "t-1")
		if !stream.Receive() {
			t.Fatalf("show message failed: %v", stream.Err())
		}
		if got := stream.Msg(); got.GetShow() == nil || got.GetShow().GetWishId() != "w-1" || got.GetShow().GetTerminal() != "t-1" {
			t.Fatalf("show msg = %v, want wish w-1 and terminal t-1", got)
		}
		s.SetReady("2.0.0", "https://notes", false)
		if !stream.Receive() {
			t.Fatalf("update message failed: %v", stream.Err())
		}
		if got := stream.Msg(); got.GetUpdate() == nil || got.GetUpdate().GetReady() != "2.0.0" {
			t.Fatalf("update msg = %v, want ready 2.0.0", got)
		}
	})
	t.Run("the load WatchLoad sends", func(t *testing.T) {
		t.Parallel()
		s, _ := newService(t)
		s.WatchLoad = func(ctx context.Context, send func(*djinnv1.LoadServiceWatchResponse) error) error {
			_ = send(&djinnv1.LoadServiceWatchResponse{Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH})
			<-ctx.Done()
			return ctx.Err()
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		stream, err := serve(t, s).Watch(ctx, connect.NewRequest(&uiv1.UiServiceWatchRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { stream.Close() })
		// First message contains initial update.
		if !stream.Receive() {
			t.Fatalf("first message failed: %v", stream.Err())
		}
		// Second message contains the load message from WatchLoad.
		if !stream.Receive() {
			t.Fatalf("load message failed: %v", stream.Err())
		}
		if got := stream.Msg(); got.GetLoad() == nil || got.GetLoad().GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_HIGH {
			t.Fatalf("load msg = %v, want notch HIGH", got)
		}
	})
}

// TestGetNotifications: the page reads whether the system shows notifications, asks for them (RequestNotifications),
// and opens the system's settings on macOS (OpenNotificationSettings).
func TestGetNotifications(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, opened := newService(t)
	get := func() *uiv1.UiServiceGetNotificationsResponse {
		t.Helper()
		res, err := s.GetNotifications(ctx, connect.NewRequest(&uiv1.UiServiceGetNotificationsRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}
	// No window: none, and no settings to open.
	if got := get(); got.GetAccess() != uiv1.NotificationAccess_NOTIFICATION_ACCESS_UNAVAILABLE || got.GetSettings() {
		t.Errorf("without notices: %v", got)
	}
	if _, err := s.OpenNotificationSettings(ctx, connect.NewRequest(&uiv1.UiServiceOpenNotificationSettingsRequest{})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("open the settings without notices: %v", err)
	}

	p := &permitted{}
	s.Notices = &Notices{}
	s.Notices.Use(p)
	if got := get().GetAccess(); got != uiv1.NotificationAccess_NOTIFICATION_ACCESS_DENIED {
		t.Errorf("not allowed yet: %v", got)
	}
	res, err := s.RequestNotifications(ctx, connect.NewRequest(&uiv1.UiServiceRequestNotificationsRequest{}))
	if err != nil || res.Msg.GetAccess() != uiv1.NotificationAccess_NOTIFICATION_ACCESS_ALLOWED || !p.asked {
		t.Errorf("request: %v, %v", res, err)
	}
	mac := runtime.GOOS == "darwin"
	if got := get().GetSettings(); got != mac {
		t.Errorf("settings = %v on %s", got, runtime.GOOS)
	}
	_, err = s.OpenNotificationSettings(ctx, connect.NewRequest(&uiv1.UiServiceOpenNotificationSettingsRequest{}))
	if mac && (err != nil || len(*opened) != 1 || (*opened)[0] != notificationSettings) {
		t.Errorf("open the settings: %v, opened %v", err, *opened)
	}
	if !mac && connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("open the settings on %s: %v", runtime.GOOS, err)
	}
}

// TestView: the page says which wish it shows, for the notices, and the theme it shows, the system's resolved.
func TestView(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("tells the notices the wish in view", func(t *testing.T) {
		t.Parallel()
		s, _ := newService(t)
		s.Notices = &Notices{}
		if _, err := s.View(ctx, connect.NewRequest(&uiv1.UiServiceViewRequest{WishId: "w1"})); err != nil {
			t.Fatal(err)
		}
		s.Notices.mu.Lock()
		viewing := s.Notices.viewing
		s.Notices.mu.Unlock()
		if viewing != "w1" {
			t.Errorf("viewing = %q, want w1", viewing)
		}
	})
	// Djinn keeps the last theme said for the driven lead's tone, beside the other settings, and a View without a
	// theme keeps it.
	t.Run("keeps the theme shown", func(t *testing.T) {
		t.Parallel()
		s, _ := newService(t)
		if err := os.WriteFile(filepath.Join(s.Home, "settings.json"), []byte(`{"leadDriven": true}`), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, step := range []struct{ sent, kept uiv1.ShownTheme }{
			{uiv1.ShownTheme_SHOWN_THEME_LIGHT, uiv1.ShownTheme_SHOWN_THEME_LIGHT},
			{uiv1.ShownTheme_SHOWN_THEME_UNSPECIFIED, uiv1.ShownTheme_SHOWN_THEME_LIGHT},
			{uiv1.ShownTheme_SHOWN_THEME_DARK, uiv1.ShownTheme_SHOWN_THEME_DARK},
		} {
			if _, err := s.View(ctx, connect.NewRequest(&uiv1.UiServiceViewRequest{WishId: "w1", Theme: step.sent})); err != nil {
				t.Fatal(err)
			}
			if got := plan.ReadShownTheme(s.Home); got != step.kept {
				t.Errorf("after %v, kept %v, want %v", step.sent, got, step.kept)
			}
		}
		if !machine.ReadLeadDriven(s.Home) {
			t.Errorf("the other settings are lost")
		}
	})
}

// TestTouch: the page says the person is there; Djinn without a listener answers as well.
func TestTouch(t *testing.T) {
	t.Parallel()
	s, _ := newService(t)
	if _, err := s.Touch(context.Background(), connect.NewRequest(&uiv1.UiServiceTouchRequest{})); err != nil {
		t.Fatalf("Touch without a listener: %v", err)
	}
	touched := 0
	s.Touched = func() { touched++ }
	if _, err := s.Touch(context.Background(), connect.NewRequest(&uiv1.UiServiceTouchRequest{})); err != nil || touched != 1 {
		t.Errorf("Touch: %v, touched %d", err, touched)
	}
}

// TestSetShortcut: the service says what the shortcut is, and sets it; without a window it has none.
func TestSetShortcut(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := newService(t)
	if _, err := s.SetShortcut(ctx, connect.NewRequest(&uiv1.UiServiceSetShortcutRequest{Chord: "Ctrl+Alt+J"})); code(err) != connect.CodeUnimplemented {
		t.Fatalf("SetShortcut without a window = %v", err)
	}
	env, err := s.GetEnvironment(ctx, connect.NewRequest(&uiv1.UiServiceGetEnvironmentRequest{}))
	if err != nil || env.Msg.GetShortcut() != nil {
		t.Fatalf("GetEnvironment without a window = %v, %v", env, err)
	}

	s.Shortcuts = &Shortcuts{Home: s.Home, GOOS: "linux"}
	s.Shortcuts.Use(&fakeRegistrar{})
	res, err := s.SetShortcut(ctx, connect.NewRequest(&uiv1.UiServiceSetShortcutRequest{Chord: "alt+ctrl+j"}))
	if err != nil || res.Msg.GetShortcut().GetChord() != "Ctrl+Alt+J" {
		t.Fatalf("SetShortcut = %v, %v", res, err)
	}
	if _, err := s.SetShortcut(ctx, connect.NewRequest(&uiv1.UiServiceSetShortcutRequest{Chord: "J"})); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("SetShortcut(J) = %v", err)
	}
	env, err = s.GetEnvironment(ctx, connect.NewRequest(&uiv1.UiServiceGetEnvironmentRequest{}))
	if sc := env.Msg.GetShortcut(); err != nil || sc.GetChord() != "Ctrl+Alt+J" || !sc.GetAvailable() {
		t.Fatalf("GetEnvironment = %v, %v", env, err)
	}
}
