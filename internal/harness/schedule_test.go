package harness

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/store"
)

// spawnReq spawns a fake task with req's own fields, the wish and the provider set.
func (e *env) spawnReq(t *testing.T, wishID, title, prompt string, req *planv1.TaskServiceSpawnRequest) (*planv1.Task, error) {
	t.Helper()
	if req == nil {
		req = &planv1.TaskServiceSpawnRequest{}
	}
	req.WishId, req.Title, req.Prompt, req.Provider = wishID, title, prompt, planv1.Provider_PROVIDER_FAKE
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetTask(), nil
}

func (e *env) mustSpawn(t *testing.T, wishID, title, prompt string, req *planv1.TaskServiceSpawnRequest) *planv1.Task {
	t.Helper()
	task, err := e.spawnReq(t, wishID, title, prompt, req)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// release sends a message to the task's fake worker, which waits for one (its wait step): it goes on at once.
func (e *env) release(t *testing.T, id string) {
	t.Helper()
	e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{TaskId: id, Text: "go on"}))
		if err == nil {
			return
		}
		// Running, its worker may not be there yet.
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ended waits until the task has ended, and returns it.
func (e *env) ended(t *testing.T, id string) *planv1.Task {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	e.watch(ctx, t, id, 0)
	task := e.get(t, id)
	if task.GetEndTime() == nil {
		t.Fatalf("task %s did not end: %v", task.GetCode(), task)
	}
	return task
}

// nativeFolder is a folder outside Git with an agent configuration: its workers edit without asking.
func nativeFolder(t *testing.T) string {
	dir := folder(t)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// limit is a capacity a test changes as it goes.
type limit struct {
	mu       sync.Mutex
	slots    int
	pressure string
}

func (l *limit) capacity() (int, string, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.slots, "set by the test", l.pressure
}

func (l *limit) set(slots int, pressure string) {
	l.mu.Lock()
	l.slots, l.pressure = slots, pressure
	l.mu.Unlock()
}

// TestDependsOn: a task waits for its dependencies, says so, and starts once they are done; a task whose
// dependency failed fails too, and so do its own dependents.
func TestDependsOn(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir(), WithTick(time.Hour)) // Only a task that ends wakes the scheduler.
	wishID, _ := e.wish(t, gitRepo(t))

	first := e.mustSpawn(t, wishID, "First", "wait\ntext one", nil)
	second := e.mustSpawn(t, wishID, "Second", "text two", &planv1.TaskServiceSpawnRequest{DependsOn: []string{"w1"}})
	if second.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || second.GetWaitReason() != "waits for W1 (running)" ||
		!slices.Equal(second.GetDependsOn(), []string{first.GetId()}) || second.GetStartTime() != nil {
		t.Fatalf("second = %v", second)
	}
	e.release(t, first.GetId())
	// Watching a planned task follows it from its plan to its end.
	events := e.watch(t.Context(), t, second.GetId(), 0)
	checkSeqs(t, events, 1)
	texts := eventTexts(events)
	if len(texts) < 4 || texts[1] != "waiting: waits for W1 (running)" || !strings.HasPrefix(texts[2], "started fake") ||
		texts[len(texts)-1] != "done" {
		t.Errorf("events of the second task: %q", texts)
	}
	first, second = e.get(t, first.GetId()), e.get(t, second.GetId())
	if second.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || second.GetWaitReason() != "" ||
		second.GetStartTime().AsTime().Before(first.GetEndTime().AsTime()) {
		t.Errorf("second started at %v, the first ended at %v: %v", second.GetStartTime().AsTime(), first.GetEndTime().AsTime(), second)
	}

	// A dependency that fails takes its dependents with it, down the chain.
	failing := e.mustSpawn(t, wishID, "Failing", "wait\nfail boom", nil)
	child := e.mustSpawn(t, wishID, "Child", "text never", &planv1.TaskServiceSpawnRequest{DependsOn: []string{failing.GetCode()}})
	grandchild := e.mustSpawn(t, wishID, "Grandchild", "text never", &planv1.TaskServiceSpawnRequest{DependsOn: []string{child.GetId()}})
	e.release(t, failing.GetId())
	if got := e.ended(t, grandchild.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED ||
		got.GetError() != "its dependency "+child.GetCode()+" ended failed" || got.GetStartTime() != nil {
		t.Errorf("grandchild = %v", got)
	}
	if got := e.get(t, child.GetId()); got.GetError() != "its dependency "+failing.GetCode()+" ended failed" {
		t.Errorf("child = %v", got)
	}

	// A dependency that already failed, or that is not a task of the wish, refuses the spawn.
	if _, err := e.spawnReq(t, wishID, "Late", "x", &planv1.TaskServiceSpawnRequest{DependsOn: []string{failing.GetCode()}}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("on a failed dependency: %v", err)
	}
	for _, dep := range []string{"W99", store.NewID()} {
		if _, err := e.spawnReq(t, wishID, "Lost", "x", &planv1.TaskServiceSpawnRequest{DependsOn: []string{dep}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("on dependency %s: %v", dep, err)
		}
	}
}

// TestGateHeldOutside: a gate held outside the running workers (a person's terminal, a lead, a script) takes a slot:
// on a full machine a new worker waits, saying so, and starts once the gate is given back, woken with no tick. A
// running worker holds its own gates in its slot (Works).
func TestGateHeldOutside(t *testing.T) {
	t.Parallel()
	l := &limit{slots: 2}
	e := up(t, t.TempDir(), WithCapacity(l.capacity), WithTick(time.Hour))
	var outside atomic.Int32
	e.h.GatesOutside(func() int { return int(outside.Load()) })
	wishID, _ := e.wish(t, gitRepo(t))
	busy := e.mustSpawn(t, wishID, "Busy", "sleep 1h", nil)
	if !e.h.Works(busy.GetId()) {
		t.Errorf("the running worker of %s does not work", busy.GetCode())
	}

	outside.Store(1)
	next := e.mustSpawn(t, wishID, "Next", "text one", nil)
	if next.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING ||
		next.GetWaitReason() != "1 worker runs and 1 gate is held outside the workers, the most this machine holds (set by the test)" {
		t.Fatalf("spawned beside a gate held outside: %v", next)
	}
	if e.h.Works(next.GetId()) {
		t.Errorf("the planned %s works", next.GetCode())
	}
	outside.Store(0)
	e.h.Wake()
	if got := e.ended(t, next.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("once the gate is given back: %v", got)
	}
}

// TestSlots: never more workers than the slots, the next planned task starts as soon as one frees, and a planned
// task waits while the machine is under pressure.
func TestSlots(t *testing.T) {
	t.Parallel()
	l := &limit{slots: 2}
	e := up(t, t.TempDir(), WithCapacity(l.capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))

	var tasks []*planv1.Task
	for i := range 4 {
		tasks = append(tasks, e.mustSpawn(t, wishID, "Busy", "wait", nil))
		if want := i < 2; (tasks[i].GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING) != want {
			t.Fatalf("task %d: %v", i+1, tasks[i])
		}
	}
	if why := tasks[2].GetWaitReason(); why != "2 workers run, the most this machine holds (set by the test)" {
		t.Errorf("third waits because %q", why)
	}
	type span struct{ start, end time.Time }
	var spans []span
	// Each ends once told to: the first frees a slot for the third, the second for the fourth.
	for _, task := range tasks {
		e.release(t, task.GetId())
		got := e.ended(t, task.GetId())
		if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Fatalf("%s: %v", got.GetCode(), got)
		}
		spans = append(spans, span{got.GetStartTime().AsTime(), got.GetEndTime().AsTime()})
	}
	for _, s := range spans {
		running := 0
		for _, o := range spans {
			if !o.start.After(s.start) && o.end.After(s.start) {
				running++
			}
		}
		if running > 2 {
			t.Errorf("%d workers ran at %v", running, s.start)
		}
	}
	// The scheduler wakes when a worker ends: the third starts with no tick, right after a slot frees.
	freed := spans[0].end
	if spans[1].end.Before(freed) {
		freed = spans[1].end
	}
	if gap := spans[2].start.Sub(freed); gap > 2*time.Second {
		t.Errorf("the third started %v after a slot freed", gap)
	}

	// Under pressure nothing starts, and the task says why; once the pressure falls, it starts.
	l.set(2, "simulated")
	e.down()
	e = up(t, e.home, WithCapacity(l.capacity), WithTick(20*time.Millisecond))
	waiting := e.mustSpawn(t, wishID, "Heavy", "text ok", nil)
	if waiting.GetWaitReason() != "the machine is under pressure: simulated" {
		t.Fatalf("under pressure: %v", waiting)
	}
	time.Sleep(100 * time.Millisecond)
	if got := e.get(t, waiting.GetId()); got.GetStartTime() != nil {
		t.Fatalf("started under pressure: %v", got)
	}
	l.set(2, "")
	if got := e.ended(t, waiting.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("after the pressure: %v", got)
	}
}

// TestMemoryHoldsWorker: on a tight machine the next worker waits, a slot free, with a reason naming the typical
// peak of a worker and the memory free; once the memory frees, it starts, the first one still running. The fake
// worker runs in Djinn's process, never measured: its typical peak is the policy's default.
func TestMemoryHoldsWorker(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	free := uint64(2 * machine.GiB)
	available := func() uint64 {
		mu.Lock()
		defer mu.Unlock()
		return free
	}
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 4}).capacity), WithMemory(available, machine.DefaultPolicy()),
		WithTick(20*time.Millisecond))
	wishID, _ := e.wish(t, gitRepo(t))

	first := e.mustSpawn(t, wishID, "First", "sleep 30s", nil)
	if first.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("first: %v", first)
	}
	// The first may still take its 1 GiB: 1 GiB is left, not the 1 GiB and 512 MiB a second needs.
	second := e.mustSpawn(t, wishID, "Second", "text ok", nil)
	want := "a fake worker peaks at 1.0 GiB (none measured yet), 2.0 GiB free, 1.0 GiB of it for the workers running, " +
		"512 MiB kept"
	if second.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || second.GetWaitReason() != want {
		t.Fatalf("second: %v, want it waiting because %q", second, want)
	}
	time.Sleep(100 * time.Millisecond)
	if got := e.get(t, second.GetId()); got.GetStartTime() != nil {
		t.Fatalf("started on a tight machine: %v", got)
	}
	mu.Lock()
	free = 3 * machine.GiB
	mu.Unlock()
	if got := e.ended(t, second.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("once the memory freed: %v", got)
	}
	if got := e.get(t, first.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("the first should still run: %v", got)
	}
}

// TestPlannedSurvivesRestart: a task planned for later waits across a restart of djinn up, then starts.
func TestPlannedSurvivesRestart(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	l := &limit{slots: 1, pressure: "simulated"}
	e := up(t, home, WithCapacity(l.capacity), WithTick(20*time.Millisecond))
	wishID, _ := e.wish(t, gitRepo(t))
	later := e.mustSpawn(t, wishID, "Later", "text later", &planv1.TaskServiceSpawnRequest{Later: true})
	if later.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || !later.GetScheduled() {
		t.Fatalf("planned = %v", later)
	}
	e.down()
	l.set(1, "")
	e = up(t, home, WithCapacity(l.capacity), WithTick(20*time.Millisecond))
	if got := e.ended(t, later.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetWorktree() == "" {
		t.Errorf("after the restart: %v", got)
	}
}

// TestStopPlanned: a planned task stops at once, and its dependents fail.
func TestStopPlanned(t *testing.T) {
	t.Parallel()
	l := &limit{slots: 0}
	e := up(t, t.TempDir(), WithCapacity(l.capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))
	first := e.mustSpawn(t, wishID, "First", "text one", nil)
	second := e.mustSpawn(t, wishID, "Second", "text two", &planv1.TaskServiceSpawnRequest{DependsOn: []string{"W1"}})
	res, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: first.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Msg.GetTask(); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED || got.GetStartTime() != nil {
		t.Errorf("stopped = %v", got)
	}
	if got := e.ended(t, second.GetId()); got.GetError() != "its dependency W1 ended stopped" {
		t.Errorf("dependent = %v", got)
	}
}

// TestWriteScopes: outside Git, tasks whose write scopes overlap never run together; in Git they do.
func TestWriteScopes(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir(), WithTick(time.Hour))
	wishID, _ := e.wish(t, nativeFolder(t))

	src := e.mustSpawn(t, wishID, "Src", "wait", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"src/"}})
	app := e.mustSpawn(t, wishID, "App", "text app", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"./src/app"}})
	docs := e.mustSpawn(t, wishID, "Docs", "wait", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"docs"}})
	if src.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || docs.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("separate scopes: %v, %v", src, docs)
	}
	if app.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || app.GetWaitReason() != "W1 writes src, which overlaps src/app" {
		t.Fatalf("overlapping scope: %v", app)
	}
	whole := e.mustSpawn(t, wishID, "Whole", "text all", nil)
	if whole.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
		t.Fatalf("the whole folder ran beside a scope: %v", whole)
	}
	e.release(t, src.GetId())
	srcDone := e.ended(t, src.GetId())
	appDone := e.ended(t, app.GetId())
	e.release(t, docs.GetId())
	docsDone := e.ended(t, docs.GetId())
	wholeDone := e.ended(t, whole.GetId())
	if appDone.GetStartTime().AsTime().Before(srcDone.GetEndTime().AsTime()) {
		t.Errorf("W2 started before W1 ended")
	}
	for _, other := range []*planv1.Task{srcDone, appDone, docsDone} {
		if wholeDone.GetStartTime().AsTime().Before(other.GetEndTime().AsTime()) && other.GetEndTime().AsTime().After(wholeDone.GetStartTime().AsTime()) &&
			!other.GetStartTime().AsTime().After(wholeDone.GetStartTime().AsTime()) {
			t.Errorf("the whole folder ran beside %s", other.GetCode())
		}
	}

	// In Git each task has its worktree: overlapping scopes run together.
	wishID, _ = e.wish(t, gitRepo(t))
	a := e.mustSpawn(t, wishID, "A", "wait", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"app"}})
	b := e.mustSpawn(t, wishID, "B", "text b", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"app"}})
	if a.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || b.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("in Git: %v, %v", a, b)
	}
	e.release(t, a.GetId())
	e.ended(t, a.GetId())

	if _, err := e.spawnReq(t, wishID, "Out", "x", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"../other"}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a scope out of the folder: %v", err)
	}
}

func TestScopes(t *testing.T) {
	t.Parallel()
	got, err := cleanScopes([]string{"src/", "./docs/../docs", "src", "b"})
	if err != nil || !slices.Equal(got, []string{"b", "docs", "src"}) {
		t.Errorf("cleanScopes = %v, %v", got, err)
	}
	if got, err := cleanScopes([]string{"src", "."}); err != nil || got != nil {
		t.Errorf("with the whole folder: %v, %v", got, err)
	}
	for _, bad := range []string{"/etc", "..", "a/../../b"} {
		if _, err := cleanScopes([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// TestRank: when a slot frees, the first wish of the rank is served first, whatever was planned first; the
// tasks of a paused wish wait until it is active again.
func TestRank(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 1}).capacity), WithTick(50*time.Millisecond))
	repo := gitRepo(t)
	older, _ := e.wish(t, repo)
	busy := e.mustSpawn(t, older, "Busy", "wait", nil)
	olderNext := e.mustSpawn(t, older, "Older", "text older", nil)
	w, err := e.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Newer", ProjectIds: []string{busy.GetProjectId()}}))
	if err != nil {
		t.Fatal(err)
	}
	newer := w.Msg.GetWish().GetId()
	if _, err := e.wishes.Move(t.Context(), connect.NewRequest(&planv1.WishServiceMoveRequest{WishId: newer, To: 1})); err != nil {
		t.Fatal(err)
	}
	newerNext := e.mustSpawn(t, newer, "Newer", "text newer", nil)
	e.release(t, busy.GetId())
	a, b := e.ended(t, olderNext.GetId()), e.ended(t, newerNext.GetId())
	if !b.GetStartTime().AsTime().Before(a.GetStartTime().AsTime()) {
		t.Errorf("the newer wish, ranked first, started at %v, after the older one at %v", b.GetStartTime().AsTime(), a.GetStartTime().AsTime())
	}

	// A paused wish keeps its planned tasks waiting, and says why.
	if _, err := e.wishes.Pause(t.Context(), connect.NewRequest(&planv1.WishServicePauseRequest{WishId: newer})); err != nil {
		t.Fatal(err)
	}
	paused := e.mustSpawn(t, newer, "Paused", "text later", nil)
	if paused.GetWaitReason() != "its wish is paused" {
		t.Fatalf("on a paused wish: %v", paused)
	}
	time.Sleep(150 * time.Millisecond)
	if got := e.get(t, paused.GetId()); got.GetStartTime() != nil {
		t.Fatalf("started on a paused wish: %v", got)
	}
	if _, err := e.wishes.Activate(t.Context(), connect.NewRequest(&planv1.WishServiceActivateRequest{WishId: newer})); err != nil {
		t.Fatal(err)
	}
	if got := e.ended(t, paused.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("once active again: %v", got)
	}
}

// TestNote: an event from outside the worker reaches the task's events, whether its worker runs or not.
func TestNote(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, gitRepo(t))
	task := e.mustSpawn(t, wishID, "Gated", "sleep 300ms", nil)
	e.h.Note(task.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_GATE, Text: "gate codegen: taken"})
	e.ended(t, task.GetId())
	e.h.Note(task.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_GATE, Text: "gate codegen: given back"})
	events := e.watch(t.Context(), t, task.GetId(), 0)
	checkSeqs(t, events, 1)
	texts := eventTexts(events)
	if !slices.Contains(texts, "gate codegen: taken") || texts[len(texts)-1] != "gate codegen: given back" {
		t.Errorf("events: %q", texts)
	}
}
