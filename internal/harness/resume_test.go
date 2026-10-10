package harness

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// fakeClock is the harness's time in a test: it moves only when the test says.
type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) set(at time.Time) {
	c.mu.Lock()
	c.at = at
	c.mu.Unlock()
}

// switchProvider starts its provider with the environment it holds now: a test changes what the next worker plays.
type switchProvider struct {
	Provider
	mu  *sync.Mutex
	env *[]string
}

func (p switchProvider) Start(ctx context.Context, spec Spec) (Worker, error) {
	p.mu.Lock()
	spec.Env = append(spec.Env, *p.env...)
	p.mu.Unlock()
	return p.Provider.Start(ctx, spec)
}

func (p switchProvider) play(env []string) {
	p.mu.Lock()
	*p.env = env
	p.mu.Unlock()
}

// claudePlaying makes claude workers play env, then what play gives.
func claudePlaying(env []string) (map[planv1.Provider]Provider, switchProvider) {
	sw := switchProvider{Provider: Claude{Command: os.Args[0]}, mu: &sync.Mutex{}, env: &env}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = sw
	return providers, sw
}

func (e *env) list(t *testing.T, wishID string) []*planv1.Task {
	t.Helper()
	res, err := e.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetTasks()
}

// until waits until the task is as ok says, and returns it.
func (e *env) until(t *testing.T, id string, ok func(*planv1.Task) bool) *planv1.Task {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := e.get(t, id)
		if ok(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s stays %v", got.GetCode(), got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func isStatus(s planv1.TaskStatus) func(*planv1.Task) bool {
	return func(t *planv1.Task) bool { return t.GetStatus() == s }
}

// resetAt is when the account's session limit of the recorded stream resets: its rate_limit_event's resetsAt.
var resetAt = time.Unix(1791436800, 0)

// TestSessionLimitWaitsThenResumes replays the real end of a claude worker the account's session limit stopped: the
// task does not fail, it waits for the reset the provider gave, and no other claude worker starts meanwhile. Once
// the reset is past, the scheduler resumes it on its session, and it finishes.
func TestSessionLimitWaitsThenResumes(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	limit, _, _ := fake{provider: "claude", fixture: "session-limit"}.env(t)
	success, args, _ := fake{provider: "claude", fixture: "success"}.env(t)
	providers, sw := claudePlaying(limit)
	clock := &fakeClock{at: time.Date(2026, 10, 8, 4, 28, 30, 0, time.UTC)}
	e := upWith(t, t.TempDir(), providers, WithClock(clock.now))
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Reopen the leads", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()

	got := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_RESUMING))
	why := "the account's session limit, resets at " + clockText(resetAt, clock.now())
	if got.GetWaitReason() != why || !got.GetResumeAfter().AsTime().Equal(resetAt.Add(time.Minute)) || got.GetError() != "" ||
		got.GetResumes() != 0 || got.GetSessionId() != "01a119c3-80f9-76b3-ba8a-13d6778efbdf" {
		t.Fatalf("after the limit: %v, want waiting for %q", got, why)
	}

	// No other claude worker starts while the limit holds; a fake one does.
	other, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Another", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if o := other.Msg.GetTask(); o.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || o.GetWaitReason() != "claude waits for "+why {
		t.Errorf("another claude task: %v", o)
	}
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: other.Msg.GetTask().GetId()})); err != nil {
		t.Fatal(err)
	}
	if f := e.spawn(t, wishID, "text free"); f.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("a fake task while claude's limit holds: %v", f)
	}

	// Before the reset nothing starts; after it, the task resumes on its session and finishes.
	sw.play(success)
	clock.set(resetAt.Add(30 * time.Second))
	e.h.wake()
	time.Sleep(50 * time.Millisecond)
	if got := e.get(t, id); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RESUMING {
		t.Fatalf("resumed before the limit reset: %v", got)
	}
	clock.set(resetAt.Add(2 * time.Minute))
	e.h.wake()
	got = e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if got.GetResumes() != 1 || got.GetWaitReason() != "" || got.GetResumeAfter() != nil {
		t.Errorf("after the resume: %v", got)
	}
	if b, err := os.ReadFile(args); err != nil || !strings.Contains(string(b), "--resume\n01a119c3-80f9-76b3-ba8a-13d6778efbdf") {
		t.Errorf("the resumed worker's arguments: %q, %v", b, err)
	}
	all := strings.Join(eventTexts(e.watch(t.Context(), t, id, 0)), "\n")
	for _, want := range []string{
		"waiting for the limit: " + why + " (You've hit your session limit · resets 7:20am (Europe/Paris)); Djinn resumes it then",
		"resumed after its usage limit reset (1 of 3): started claude in ",
		"done",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no event %q in:\n%s", want, all)
		}
	}
	if n := len(e.list(t, wishID)); n != 3 {
		t.Errorf("%d tasks, want the 3 spawned", n)
	}
}

// TestSessionLimitBounded: a task the limit stops each time it resumes waits after a backoff once the reset it was
// given is past (15 minutes, doubled each time), and after 3 resumes it fails, saying so.
func TestSessionLimitBounded(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	limit, _, _ := fake{provider: "claude", fixture: "session-limit"}.env(t)
	providers, _ := claudePlaying(limit)
	clock := &fakeClock{at: time.Date(2026, 10, 8, 4, 28, 30, 0, time.UTC)}
	e := upWith(t, t.TempDir(), providers, WithClock(clock.now))
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Reopen the leads", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()
	got := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_RESUMING))
	for i := range int32(maxResumes) {
		now := got.GetResumeAfter().AsTime().Add(time.Second)
		clock.set(now)
		e.h.wake()
		if i == maxResumes-1 {
			break
		}
		got = e.until(t, id, func(t *planv1.Task) bool {
			return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && t.GetResumes() == i+1 && t.GetResumeAfter() != nil
		})
		// The reset the stream gives is past: a backoff, 30 minutes after the first resume, then an hour.
		if wait := got.GetResumeAfter().AsTime().Sub(now); wait != limitBackoff(i+1) || !strings.Contains(got.GetWaitReason(), "tries again at") {
			t.Errorf("resume %d: waits %v (%s), want %v", i+1, wait, got.GetWaitReason(), limitBackoff(i+1))
		}
	}
	got = e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_FAILED))
	if got.GetResumes() != maxResumes || got.GetError() != "resumed 3 times without finishing; the last time: You've hit your session limit · resets 7:20am (Europe/Paris)" {
		t.Errorf("after %d resumes: %v", maxResumes, got)
	}
}

// TestResumeRules: at the start, a task cut short resumes; one stopped by a person, one already resumed as another
// task (a fork of it), one imported, never do; one resumed 3 times already fails.
func TestResumeRules(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	e := up(t, home)
	wishID, projectID := e.wish(t, gitRepo(t))
	worktree := t.TempDir()
	task := func(code string, s planv1.TaskStatus, edit func(*planv1.Task)) *planv1.Task {
		tk := &planv1.Task{
			Id: store.NewID(), WishId: wishID, ProjectId: projectID, Code: code, Title: code, Status: s,
			Provider: planv1.Provider_PROVIDER_FAKE, Worktree: worktree, SessionId: "s-" + code, Scheduled: true,
			Error: "djinn up ended while the worker ran", StartTime: timestamppb.Now(), EndTime: timestamppb.Now(),
		}
		if edit != nil {
			edit(tk)
		}
		putTask(t, e.db, tk, "text first prompt")
		return tk
	}
	cut := task("W1", planv1.TaskStatus_TASK_STATUS_INTERRUPTED, nil)
	stopped := task("W2", planv1.TaskStatus_TASK_STATUS_STOPPED, func(tk *planv1.Task) { tk.Error = "stopped on request" })
	forked := task("W3", planv1.TaskStatus_TASK_STATUS_INTERRUPTED, nil)
	task("W4", planv1.TaskStatus_TASK_STATUS_DONE, func(tk *planv1.Task) { tk.ForkOf, tk.Error = "W3", "" })
	imported := task("W5", planv1.TaskStatus_TASK_STATUS_INTERRUPTED, func(tk *planv1.Task) { tk.Scheduled = false })
	worn := task("W6", planv1.TaskStatus_TASK_STATUS_INTERRUPTED, func(tk *planv1.Task) { tk.Resumes = maxResumes })
	gone := task("W7", planv1.TaskStatus_TASK_STATUS_INTERRUPTED, func(tk *planv1.Task) { tk.Worktree = "" })
	e.down()

	e = up(t, home)
	got := e.until(t, cut.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if got.GetResumes() != 1 {
		t.Errorf("W1: %v", got)
	}
	if all := strings.Join(eventTexts(e.watch(t.Context(), t, cut.GetId(), 0)), "\n"); !strings.Contains(all, "resumed after djinn restarted (1 of 3): started fake in "+worktree+
		", resuming its session") || !strings.Contains(all, restartedLine) {
		t.Errorf("W1's events:\n%s", all)
	}
	for _, tk := range []*planv1.Task{stopped, forked, imported, gone} {
		if got := e.get(t, tk.GetId()); got.GetStatus() != tk.GetStatus() || got.GetResumes() != 0 {
			t.Errorf("%s: %v, want it left %s", tk.GetCode(), got, tk.GetStatus())
		}
	}
	if got := e.get(t, worn.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED ||
		got.GetError() != "resumed 3 times without finishing; the last time: djinn up ended while the worker ran" {
		t.Errorf("W6: %v", got)
	}
}
