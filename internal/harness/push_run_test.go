package harness

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

const testAtPush = "checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n"

// project is the integration's project, as stored.
func (in *integration) project(t *testing.T) *planv1.Project {
	t.Helper()
	project, err := store.Get[*planv1.Project](t.Context(), in.db, in.projectID)
	if err != nil {
		t.Fatal(err)
	}
	return project
}

// projectChanges counts the commits that tell the window to read its projects again (a ProjectSync noted), and the
// pushes recorded on the wish when they did.
type projectChanges struct {
	mu      sync.Mutex
	changes int
	pushed  []string // the last push's commit on the wish, at each such commit
}

func (in *integration) watchProjects() *projectChanges {
	c := &projectChanges{}
	in.db.OnCommit(func(ms []proto.Message) {
		var told bool
		var last string
		for _, m := range ms {
			switch m := m.(type) {
			case *planv1.ProjectSync:
				told = true
			case *planv1.Wish:
				last = plan.PushStateOf(m, in.projectID).GetLast().GetNewSha()
			}
		}
		if told {
			c.mu.Lock()
			c.changes++
			c.pushed = append(c.pushed, last)
			c.mu.Unlock()
		}
	})
	return c
}

func (c *projectChanges) seen() (int, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.changes, c.pushed
}

// eventually waits until ok says so, failing after a few seconds with what last said.
func eventually(t *testing.T, ok func() bool, last func() string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !ok(); {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting: %s", last())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPushShowsOnItsSyncLine: W489. The developer pressed the push of a sync line: its push checks ran for minutes
// with nothing shown; once pushed, the line kept ↑3, and a second click said "nothing to push: the remote is up to
// date" as an error. A push's state is now readable on its branch's sync line while its checks run, whoever started
// it, and the push goes on when its caller goes (a window reloaded); the projects change once it is pushed, its counts
// follow the remote, and a second push is up to date, not failed.
func TestPushShowsOnItsSyncLine(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)
	in.checking(t, testAtPush, nil)
	bare := in.remote(t)
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.pass(t, 0) // One task committed: no push due.
	if sync := in.syncOf(t, in.project(t), in.branch); sync.GetAhead() == 0 || sync.GetPush() != nil {
		t.Fatalf("before the push: %v; want ahead, no push", sync)
	}

	changes := in.watchProjects()
	wait := make(chan struct{})
	in.mu.Lock()
	in.testWait = wait
	in.mu.Unlock()
	done := make(chan error, 1)
	caller, leave := context.WithCancel(t.Context())
	go func() {
		_, err := in.h.Push(caller, in.wishID, in.projectID, in.branch)
		done <- err
	}()

	// While the push check runs, its line says so, and the window was told.
	var sync *planv1.ProjectSync
	eventually(t, func() bool {
		sync = in.syncOf(t, in.project(t), in.branch)
		return sync.GetPush().GetStage() == planv1.PushStage_PUSH_STAGE_CHECKING
	}, func() string { return sync.String() })
	if sync.GetPush().GetCheck() != "test" || sync.GetPush().GetEndTime() != nil || sync.GetAhead() == 0 {
		t.Errorf("while the checks run: %v; want the test check running, the branch still ahead", sync)
	}
	if n, _ := changes.seen(); n == 0 {
		t.Error("the push checks started, the projects did not change")
	}

	leave() // The window reloads: its request goes, the push goes on.
	close(wait)
	if err := <-done; err != nil {
		t.Fatalf("Push: %v", err)
	}
	tip := in.tip(t)
	if got := in.remoteTip(t, bare); got != tip {
		t.Fatalf("origin at %s, the branch at %s", got, tip)
	}
	// Pushed: the counts follow the remote, the line says it pushed, and the projects changed with the push recorded.
	sync = in.syncOf(t, in.project(t), in.branch)
	if sync.GetAhead() != 0 || sync.GetBehind() != 0 {
		t.Errorf("after the push: ↑%d ↓%d; want in sync", sync.GetAhead(), sync.GetBehind())
	}
	if p := sync.GetPush(); p.GetStage() != planv1.PushStage_PUSH_STAGE_PUSHED || p.GetEndTime() == nil || p.GetBranch() != in.branch {
		t.Errorf("after the push: %v; want pushed, ended", p)
	}
	if _, pushed := changes.seen(); len(pushed) == 0 || pushed[len(pushed)-1] != tip {
		t.Errorf("the projects changed with the pushes %q; want the last with %s", pushed, tip)
	}

	// The second click: nothing to push, said as such.
	if _, err := in.h.Push(t.Context(), in.wishID, in.projectID, in.branch); !errors.Is(err, plan.ErrUpToDate) {
		t.Errorf("a second push: %v; want %v", err, plan.ErrUpToDate)
	}
}

// TestHeldPushShowsWhy: red push checks hold the push: its line says which check failed, the first lines of what it
// printed, and the correction Djinn started.
func TestHeldPushShowsWhy(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)
	in.checking(t, testAtPush, nil)
	in.testCode, in.testOut = 1, "--- FAIL: TestLogin\n    login_test.go:12: no session\nFAIL"
	in.correctWith(func(dir, p string) string { return "wait" })
	in.remote(t)
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.pass(t, 0)

	if _, err := in.h.Push(t.Context(), in.wishID, in.projectID, in.branch); err == nil {
		t.Fatal("pushed with its push checks red")
	}
	sync := in.syncOf(t, in.project(t), in.branch)
	p := sync.GetPush()
	if p.GetStage() != planv1.PushStage_PUSH_STAGE_HELD || p.GetEndTime() == nil {
		t.Fatalf("held: %v; want held, ended", p)
	}
	for _, want := range []string{"test: test exited 1", "--- FAIL: TestLogin", "login_test.go:12: no session"} {
		if !strings.Contains(p.GetNote(), want) {
			t.Errorf("the note %q lacks %q", p.GetNote(), want)
		}
	}
	state := in.pushState(t)
	if state.GetCorrectedBy() == "" {
		t.Fatalf("no correction started: %v", state)
	}
	correction, err := store.Get[*planv1.Task](t.Context(), in.db, state.GetCorrectedBy())
	if err != nil {
		t.Fatal(err)
	}
	if sync.GetCorrection() == "" || sync.GetCorrection() != correction.GetCode() {
		t.Errorf("the line's correction %q; want %s", sync.GetCorrection(), correction.GetCode())
	}
}

// TestStalePushRunIsNotShown: a push recorded running that no longer runs, Djinn stopped during it, shows no spinner.
func TestStalePushRunIsNotShown(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)
	in.remote(t)
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.pass(t, 0)
	run := &pushRun{h: in.h, wishID: in.wishID, projectID: in.projectID, branch: in.branch}
	run.set(t.Context(), planv1.PushStage_PUSH_STAGE_CHECKING, "test", "")
	if got := in.pushState(t).GetRun().GetStage(); got != planv1.PushStage_PUSH_STAGE_CHECKING {
		t.Fatalf("recorded %v; want checking", got)
	}
	if sync := in.syncOf(t, in.project(t), in.branch); sync.GetPush() != nil {
		t.Errorf("a push no longer running shows: %v", sync.GetPush())
	}
}

// TestHeldNote: the check and why, then the first lines of what it printed last.
func TestHeldNote(t *testing.T) {
	t.Parallel()
	out := strings.Repeat("line\n", 20) + "end"
	got := heldNote("test: test exited 1", out)
	if lines := strings.Split(got, "\n"); len(lines) != 1+heldLines || lines[0] != "test: test exited 1" {
		t.Errorf("heldNote = %q", got)
	}
	if got := heldNote("lint: lint exited 2", ""); got != "lint: lint exited 2" {
		t.Errorf("heldNote without output = %q", got)
	}
}
