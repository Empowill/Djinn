package ui

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
)

// state is what the window is told of the update now.
func state(s *Service) *uiv1.UiServiceWatchUpdateResponse {
	s.updates.Lock()
	defer s.updates.Unlock()
	return s.updateState()
}

// until waits for the update state to meet ok, and fails the test after a few seconds.
func until(t *testing.T, s *Service, what string, ok func(*uiv1.UiServiceWatchUpdateResponse) bool) *uiv1.UiServiceWatchUpdateResponse {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := state(s)
		if ok(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: never; the state is %v", what, st)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// installs is a fake Install that records the builds it installs and how, and tells the steps of go tool task install.
type installs struct {
	mu   sync.Mutex
	shas []string
	low  []bool
	hang bool // it hangs until its context ends, as a build that never ends
}

func (f *installs) install(ctx context.Context, b *uiv1.Build, run InstallRun) (bool, string, error) {
	f.mu.Lock()
	f.shas, f.low = append(f.shas, b.GetSha()), append(f.low, run.Low)
	hang := f.hang
	f.mu.Unlock()
	run.Progress(uiv1.InstallStep_INSTALL_STEP_BUILDING, "")
	run.Output("task: [ui] npm run build")
	run.Output("vite v6 building for production...")
	if hang {
		run.Output("task: [install] go build -o .djinn-new ./cmd/djinn")
		<-ctx.Done()
		return false, "task: [ui] npm run build\nbuilt in 3s\n\ntask: [install] go build -o .djinn-new ./cmd/djinn\n", ctx.Err()
	}
	return true, "Installed djinn", nil
}

func (f *installs) installed() ([]string, []bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.shas), slices.Clone(f.low)
}

// TestInstall: one install runs at a time, within djinn up's life and not the call's; the person's click comes
// first.
func TestInstall(t *testing.T) {
	t.Parallel()
	// An install whose build never ends is stopped at its deadline; the window is told the step it was at, that it
	// failed and why, with the last lines its command wrote, and the build stays proposed. A newer build of its branch
	// makes the failure forgotten.
	t.Run("past its deadline", func(t *testing.T) {
		t.Parallel()
		s, _ := newService(t)
		f := &installs{hang: true}
		s.Install, s.InstallLimit = f.install, 100*time.Millisecond
		s.Restart = func(string) (string, int, error) { t.Error("restarted on a build that failed"); return "", 0, nil }
		s.SetBuild(&uiv1.Build{ProjectId: "p", Branch: "feat/x", Sha: "abc12345678"})

		_, err := s.Update(t.Context(), connect.NewRequest(&uiv1.UiServiceUpdateRequest{Build: "abc12345678"}))
		if code(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "did not end within 100ms") {
			t.Fatalf("an install past its deadline: %v", err)
		}
		in := until(t, s, "the failure told", func(st *uiv1.UiServiceWatchUpdateResponse) bool {
			return st.GetInstalling().GetStep() == uiv1.InstallStep_INSTALL_STEP_FAILED
		}).GetInstalling()
		want := &uiv1.Installing{
			Sha: "abc12345678", Step: uiv1.InstallStep_INSTALL_STEP_FAILED, Doing: "install: go build -o .djinn-new ./cmd/djinn",
			Error: "it did not end within 100ms, at install: go build -o .djinn-new ./cmd/djinn: Djinn stopped it",
			Log:   []string{"task: [ui] npm run build", "built in 3s", "task: [install] go build -o .djinn-new ./cmd/djinn"},
		}
		if !proto.Equal(in, want) {
			t.Errorf("installing = %v, want %v", in, want)
		}
		if b := state(s).GetBuilds(); len(b) != 1 || b[0].GetSha() != "abc12345678" {
			t.Errorf("builds = %v, want the build still proposed", b)
		}
		s.SetBuild(&uiv1.Build{ProjectId: "p", Branch: "feat/x", Sha: "def"})
		if in := state(s).GetInstalling(); in != nil {
			t.Errorf("installing = %v once a newer build of the branch came", in)
		}
	})
	// The window's call that ends first (a reload, an answer lost) leaves the install running; the window learns from
	// the update state that it ended, and the newer Djinn is ready to restart on.
	t.Run("outlives its call", func(t *testing.T) {
		t.Parallel()
		s, _ := newService(t)
		release := make(chan struct{})
		s.Install = func(ctx context.Context, b *uiv1.Build, run InstallRun) (bool, string, error) {
			run.Progress(uiv1.InstallStep_INSTALL_STEP_BUILDING, "")
			<-release
			s.SetReady("local-gabc", "", true) // As djinn up's check finds the newer Djinn at the path.
			return true, "", ctx.Err()
		}
		s.Restart = func(string) (string, int, error) { t.Error("restarted without a call to answer"); return "", 0, nil }
		s.SetBuild(&uiv1.Build{ProjectId: "p", Branch: "feat/x", Sha: "abc"})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			_, err := s.Update(ctx, connect.NewRequest(&uiv1.UiServiceUpdateRequest{Build: "abc"}))
			done <- err
		}()
		until(t, s, "building", func(st *uiv1.UiServiceWatchUpdateResponse) bool {
			return st.GetInstalling().GetStep() == uiv1.InstallStep_INSTALL_STEP_BUILDING
		})
		cancel()
		if err := <-done; err == nil {
			t.Error("the call that ended answered as installed")
		}
		close(release)
		st := until(t, s, "installed", func(st *uiv1.UiServiceWatchUpdateResponse) bool {
			return st.GetInstalling() == nil && len(st.GetBuilds()) == 0
		})
		if st.GetReady() != "local-gabc" || !st.GetInstalled() {
			t.Errorf("ready %q installed %v, want the newer Djinn at the path, to restart on", st.GetReady(), st.GetInstalled())
		}
	})
	// djinn up's end ends the install it runs.
	t.Run("ends with djinn up", func(t *testing.T) {
		t.Parallel()
		s, _ := newService(t)
		life, end := context.WithCancel(context.Background())
		f := &installs{hang: true}
		s.Install, s.Life = f.install, life
		s.SetBuild(&uiv1.Build{ProjectId: "p", Branch: "feat/x", Sha: "abc"})
		done := make(chan error, 1)
		go func() {
			_, err := s.Update(t.Context(), connect.NewRequest(&uiv1.UiServiceUpdateRequest{Build: "abc"}))
			done <- err
		}()
		until(t, s, "building", func(st *uiv1.UiServiceWatchUpdateResponse) bool {
			return st.GetInstalling().GetStep() == uiv1.InstallStep_INSTALL_STEP_BUILDING
		})
		end()
		select {
		case err := <-done:
			if err == nil {
				t.Error("an install cut by djinn up's end answered as installed")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the install outlived djinn up")
		}
	})
	// The person's click on another build stops an install Djinn started by itself.
	t.Run("the click stops an install Djinn started", func(t *testing.T) {
		t.Parallel()
		s, _ := newService(t)
		f := &installs{hang: true}
		s.Install = f.install
		s.SetBuild(&uiv1.Build{ProjectId: "p", Branch: "feat/side", Sha: "side"})
		s.SetBuild(&uiv1.Build{ProjectId: "p", Branch: "feat/robustness", Sha: "line", Running: true, Auto: true})
		until(t, s, "auto install building", func(st *uiv1.UiServiceWatchUpdateResponse) bool {
			return st.GetInstalling().GetSha() == "line" && st.GetInstalling().GetStep() == uiv1.InstallStep_INSTALL_STEP_BUILDING
		})
		f.mu.Lock()
		f.hang = false
		f.mu.Unlock()
		if res, err := s.Update(t.Context(), connect.NewRequest(&uiv1.UiServiceUpdateRequest{Build: "side"})); err != nil ||
			res.Msg.GetInstalled() != "side" {
			t.Fatalf("the click on another build: %v, %v", res, err)
		}
		if shas, _ := f.installed(); !slices.Equal(shas, []string{"line", "side"}) {
			t.Errorf("installed %v, want the auto install stopped for the click", shas)
		}
		st := until(t, s, "settled", func(st *uiv1.UiServiceWatchUpdateResponse) bool { return st.GetInstalling() == nil })
		if len(st.GetBuilds()) != 1 || st.GetBuilds()[0].GetSha() != "line" {
			t.Errorf("builds = %v, want the line's build, stopped, still offered", st.GetBuilds())
		}
	})
	// A click on the build Djinn installs by itself joins it: one install for the click and Djinn's own.
	t.Run("the click joins the install of its build", func(t *testing.T) {
		t.Parallel()
		s, _ := newService(t)
		release := make(chan struct{})
		var mu sync.Mutex
		runs := 0
		s.Install = func(_ context.Context, _ *uiv1.Build, run InstallRun) (bool, string, error) {
			mu.Lock()
			runs++
			mu.Unlock()
			run.Progress(uiv1.InstallStep_INSTALL_STEP_BUILDING, "")
			<-release
			return false, "", nil
		}
		s.SetBuild(&uiv1.Build{ProjectId: "p", Branch: "feat/robustness", Sha: "line", Running: true, Auto: true})
		until(t, s, "auto install building", func(st *uiv1.UiServiceWatchUpdateResponse) bool {
			return st.GetInstalling().GetStep() == uiv1.InstallStep_INSTALL_STEP_BUILDING
		})
		go func() { time.Sleep(20 * time.Millisecond); close(release) }()
		res, err := s.Update(t.Context(), connect.NewRequest(&uiv1.UiServiceUpdateRequest{Build: "line"}))
		mu.Lock()
		defer mu.Unlock()
		if err != nil || res.Msg.GetInstalled() != "line" || runs != 1 {
			t.Errorf("the click on the build installing: %v, %v, %d runs", res, err, runs)
		}
	})
}

// TestAutoInstall: a build to install by itself (Build.auto, a green push of the running line) installs at low
// priority with no click, once the machine is free, telling what it waits for meanwhile; then the newer Djinn is ready
// and nothing restarts. A build of another branch is only offered.
func TestAutoInstall(t *testing.T) {
	t.Parallel()
	s, _ := newService(t)
	f := &installs{}
	s.Install, s.AutoRetry = f.install, 10*time.Millisecond
	s.Restart = func(string) (string, int, error) { t.Error("restarted by itself"); return "", 0, nil }
	var mu sync.Mutex
	busy := "gate test: held by W5"
	s.Busy = func() string {
		mu.Lock()
		defer mu.Unlock()
		return busy
	}

	s.SetBuild(&uiv1.Build{ProjectId: "p", Branch: "feat/side", Sha: "side"})
	s.SetBuild(&uiv1.Build{ProjectId: "p", Branch: "feat/robustness", Sha: "line1", Running: true, Auto: true})
	until(t, s, "waiting for the gate", func(st *uiv1.UiServiceWatchUpdateResponse) bool {
		in := st.GetInstalling()
		return in.GetSha() == "line1" && in.GetStep() == uiv1.InstallStep_INSTALL_STEP_WAITING &&
			in.GetWaiting() == "gate test: held by W5" && in.GetAuto()
	})
	// A newer push of the line meanwhile: it is the one installed.
	s.SetBuild(&uiv1.Build{ProjectId: "p", Branch: "feat/robustness", Sha: "line2", Running: true, Auto: true})
	if shas, _ := f.installed(); len(shas) != 0 {
		t.Fatalf("installed %v while the machine is busy", shas)
	}
	mu.Lock()
	busy = ""
	mu.Unlock()
	st := until(t, s, "installed", func(st *uiv1.UiServiceWatchUpdateResponse) bool {
		return st.GetInstalling() == nil && len(st.GetBuilds()) == 1
	})
	if shas, low := f.installed(); !slices.Equal(shas, []string{"line2"}) || !slices.Equal(low, []bool{true}) {
		t.Errorf("installed %v at low %v, want line2 at low priority", shas, low)
	}
	if st.GetBuilds()[0].GetSha() != "side" {
		t.Errorf("builds = %v, want the other branch's still offered", st.GetBuilds())
	}
	// The other branch's build waits for the click.
	time.Sleep(50 * time.Millisecond)
	if shas, _ := f.installed(); len(shas) != 1 {
		t.Errorf("installed %v, want the other branch's build left alone", shas)
	}
}

// TestStepOf: a line of go tool task names the step it runs; another line names none.
func TestStepOf(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, line, want string }{
		{"a task", "task: [ui] npm run build", "ui: npm run build"},
		{"a task with quotes", `task: [install] go run ./tools/swapexe "a" "b"`, `install: go run ./tools/swapexe "a" "b"`},
		{"a line of the build", "vite v6 building for production...", ""},
		{"a long task", "task: [ui] " + strings.Repeat("a", 200), "ui: " + strings.Repeat("a", 115) + "…"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := stepOf(c.line); got != c.want {
				t.Errorf("stepOf(%q) = %q, want %q", c.line, got, c.want)
			}
		})
	}
}
