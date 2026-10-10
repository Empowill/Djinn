package harness

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// TestDepend: a task's dependencies are set after it was made, by code; a cycle, a wait on itself or a task of
// another wish is refused; none clears them; a planned task waits for its new dependencies.
func TestDepend(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	// No slot: the planned tasks wait, for their dependencies first.
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 0}).capacity))
	wishID, _ := e.wish(t, gitRepo(t))
	plan := func(title string) *planv1.Task {
		t.Helper()
		res, err := e.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: title, Prompt: "text " + title, Provider: planv1.Provider_PROVIDER_FAKE, Later: true,
		}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetTask()
	}
	depend := func(task *planv1.Task, on ...string) (*planv1.Task, error) {
		res, err := e.tasks.Depend(ctx, connect.NewRequest(&planv1.TaskServiceDependRequest{TaskId: task.GetId(), DependsOn: on}))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetTask(), nil
	}
	a, b, c := plan("A"), plan("B"), plan("C")

	// C waits for A and B; B for A: a graph without cycle.
	got, err := depend(c, a.GetCode(), b.GetCode())
	if err != nil || !slices.Equal(got.GetDependsOn(), []string{a.GetId(), b.GetId()}) {
		t.Fatalf("C on A and B: %v, %v", got, err)
	}
	if _, err := depend(b, a.GetCode()); err != nil {
		t.Fatal(err)
	}
	// A waiting for C would close A → C → A, and for B, A → B → A.
	_, err = depend(a, c.GetCode())
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "no cycle") ||
		!strings.Contains(err.Error(), a.GetCode()+" → "+c.GetCode()+" → "+a.GetCode()) {
		t.Errorf("A on C: %v", err)
	}
	if _, err := depend(a, a.GetCode()); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("A on itself: %v", err)
	}
	if _, err := depend(a, "W99"); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("A on a task of no wish: %v", err)
	}
	// The scheduler says what C waits for.
	waited := e.until(t, c.GetId(), func(task *planv1.Task) bool { return strings.Contains(task.GetWaitReason(), "waits for") })
	if !strings.Contains(waited.GetWaitReason(), a.GetCode()) && !strings.Contains(waited.GetWaitReason(), b.GetCode()) {
		t.Errorf("C waits: %q", waited.GetWaitReason())
	}
	// None clears them.
	if got, err := depend(c); err != nil || len(got.GetDependsOn()) != 0 {
		t.Errorf("C on nothing: %v, %v", got, err)
	}
}

// TestSpawnBlocksWhilePassesRun: a new task spawned --blocks W5 is in W5's dependencies in the same step as its spawn,
// while scheduler passes run in a tight loop: as the slots free, W5 never starts before the new task is done. When a
// pass started W5 first, the spawn is refused, saying where W5 stands, and makes nothing.
func TestSpawnBlocksWhilePassesRun(t *testing.T) {
	testx.Portable(t)
	l := &limit{}
	e := up(t, t.TempDir(), WithCapacity(l.capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, nativeFolder(t)) // Outside Git: no worktree to make, the test stays fast.
	ctx, stop := context.WithCancel(t.Context())
	var passes sync.WaitGroup
	passes.Go(func() {
		for ctx.Err() == nil {
			e.h.schedule(ctx)
			runtime.Gosched() // The spawn waits for the same lock: it may come first.
		}
	})
	t.Cleanup(func() { stop(); passes.Wait() })

	const rounds = 10
	var inserted, refused int
	for i := range rounds {
		// W5 waits for a slot; the slots free as the spawn comes, and a pass may take them first. In the first round
		// they free after it: the spawn comes first.
		l.set(0, "")
		w5 := e.mustSpawn(t, wishID, fmt.Sprintf("Planned %d", i), "text planned", &planv1.TaskServiceSpawnRequest{Later: true})
		if i > 0 {
			go l.set(8, "")
		}
		n, err := e.h.Spawn(t.Context(), "test", &planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: fmt.Sprintf("Before %d", i), Prompt: "text before", Provider: planv1.Provider_PROVIDER_FAKE,
			Blocks: []string{w5.GetCode()},
		})
		l.set(8, "")
		w5 = e.ended(t, w5.GetId())
		if err != nil {
			if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "task "+w5.GetCode()+" has started (") {
				t.Fatalf("spawn --blocks %s: %v", w5.GetCode(), err)
			}
			refused++
			continue
		}
		n = e.ended(t, n.GetId())
		if !slices.Contains(w5.GetDependsOn(), n.GetId()) || w5.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE ||
			w5.GetStartTime().AsTime().Before(n.GetEndTime().AsTime()) {
			t.Fatalf("%s started at %v, %s ended at %v: %v", w5.GetCode(), w5.GetStartTime().AsTime(), n.GetCode(),
				n.GetEndTime().AsTime(), w5)
		}
		inserted++
	}
	tasks, err := e.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(tasks.Msg.GetTasks()); got != rounds+inserted {
		t.Errorf("%d tasks, want %d: a refused spawn makes none", got, rounds+inserted)
	}
	if inserted == 0 {
		t.Error("no spawn came before a pass")
	}
	t.Logf("%d inserted, %d refused", inserted, refused)
}

// TestSpawnAfterAndBlocks: --after takes a comma list; --blocks puts the new task before planned tasks, and is refused
// for a task that has started, saying where it stands, and for one that would close a cycle, naming it.
func TestSpawnAfterAndBlocks(t *testing.T) {
	testx.Portable(t)
	l := &limit{slots: 1}
	e := up(t, t.TempDir(), WithCapacity(l.capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, nativeFolder(t))
	running := e.mustSpawn(t, wishID, "Running", "sleep 1h", nil) // W1, in the only slot: the planned tasks only wait.
	later := &planv1.TaskServiceSpawnRequest{Later: true}
	a, b := e.mustSpawn(t, wishID, "A", "text a", later), e.mustSpawn(t, wishID, "B", "text b", later)

	c := e.mustSpawn(t, wishID, "C", "text c", &planv1.TaskServiceSpawnRequest{After: []string{strings.ToLower(a.GetCode()) + ", " + b.GetCode()}})
	if !slices.Equal(c.GetDependsOn(), []string{a.GetId(), b.GetId()}) {
		t.Errorf("C after A, B: %v", c.GetDependsOn())
	}
	// --depends-on, its former name, still works, beside --after.
	d := e.mustSpawn(t, wishID, "D", "text d", &planv1.TaskServiceSpawnRequest{DependsOn: []string{a.GetCode()}, After: []string{c.GetCode()}})
	if !slices.Equal(d.GetDependsOn(), []string{a.GetId(), c.GetId()}) {
		t.Errorf("D on A after C: %v", d.GetDependsOn())
	}

	// Before A and B: both wait for it, A keeps nothing else, B nothing else.
	n := e.mustSpawn(t, wishID, "N", "text n", &planv1.TaskServiceSpawnRequest{Blocks: []string{a.GetCode() + "," + b.GetCode()}})
	for _, task := range []*planv1.Task{e.get(t, a.GetId()), e.get(t, b.GetId())} {
		if !slices.Equal(task.GetDependsOn(), []string{n.GetId()}) {
			t.Errorf("%s waits for %v, want %s", task.GetCode(), task.GetDependsOn(), n.GetCode())
		}
	}
	e.until(t, a.GetId(), func(task *planv1.Task) bool { return task.GetWaitReason() == "waits for "+n.GetCode()+" (pending)" })

	// After C and before A closes A → the new task → C → A; nothing changes.
	_, err := e.spawnReq(t, wishID, "Loop", "text loop", &planv1.TaskServiceSpawnRequest{After: []string{c.GetCode()}, Blocks: []string{a.GetCode()}})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), a.GetCode()+" → the new task → "+c.GetCode()+" → "+a.GetCode()) {
		t.Errorf("a cycle: %v", err)
	}
	if got := e.get(t, a.GetId()); !slices.Equal(got.GetDependsOn(), []string{n.GetId()}) {
		t.Errorf("A after a refused spawn: %v", got.GetDependsOn())
	}
	if _, err := e.spawnReq(t, wishID, "Lost", "text lost", &planv1.TaskServiceSpawnRequest{Blocks: []string{"W99"}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("before a task of no wish: %v", err)
	}

	// A task that runs, or has stopped, has started.
	for _, want := range []string{"running", "stopped"} {
		_, err := e.spawnReq(t, wishID, "Late", "text late", &planv1.TaskServiceSpawnRequest{Blocks: []string{running.GetCode()}})
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "task "+running.GetCode()+" has started ("+want+")") {
			t.Errorf("before a task %s: %v", want, err)
		}
		if want == "running" {
			if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: running.GetId()})); err != nil {
				t.Fatal(err)
			}
			e.ended(t, running.GetId())
		}
	}
	// An azima goes before a planned task too.
	t1, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Design", Kind: planv1.TaskKind_TASK_KIND_AZIMA, Blocks: []string{d.GetCode()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, d.GetId()); !slices.Contains(got.GetDependsOn(), t1.Msg.GetTask().GetId()) {
		t.Errorf("D after the azima: %v", got.GetDependsOn())
	}
}

// TestDependSeveral: depend sets several tasks at once, all or none: one cyclic edge among them changes nothing.
func TestDependSeveral(t *testing.T) {
	testx.Portable(t)
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 0}).capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, nativeFolder(t))
	later := &planv1.TaskServiceSpawnRequest{Later: true}
	a, b, c := e.mustSpawn(t, wishID, "A", "text a", later), e.mustSpawn(t, wishID, "B", "text b", later),
		e.mustSpawn(t, wishID, "C", "text c", later)
	depend := func(req *planv1.TaskServiceDependRequest) (*planv1.TaskServiceDependResponse, error) {
		res, err := e.tasks.Depend(t.Context(), connect.NewRequest(req))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}
	deps := func() [][]string {
		var out [][]string
		for _, task := range []*planv1.Task{a, b, c} {
			out = append(out, e.get(t, task.GetId()).GetDependsOn())
		}
		return out
	}

	// C after A and B, B after A: a chain, in one call.
	res, err := depend(&planv1.TaskServiceDependRequest{
		TaskId: c.GetId(), After: []string{"W1,W2"}, Also: []*planv1.TaskAfter{{Task: "w2", After: []string{"W1"}}},
	})
	if err != nil || !slices.Equal(res.GetTask().GetDependsOn(), []string{a.GetId(), b.GetId()}) || len(res.GetAlso()) != 1 ||
		!slices.Equal(res.GetAlso()[0].GetDependsOn(), []string{a.GetId()}) {
		t.Fatalf("C after A and B, B after A: %v, %v", res, err)
	}
	before := deps()

	// B after nothing, and A after C: A → C → B? No: A → C → A, through C after A. Nothing changes, B included.
	_, err = depend(&planv1.TaskServiceDependRequest{TaskId: b.GetId(), Also: []*planv1.TaskAfter{{Task: "W1", After: []string{"W3"}}}})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "W1 → W3 → W1") {
		t.Errorf("a cyclic edge: %v", err)
	}
	if got := deps(); !slices.EqualFunc(got, before, slices.Equal) {
		t.Errorf("after a refused call: %v, want %v", got, before)
	}
	// The same task twice, or a task of no wish, refuses the call too.
	if _, err := depend(&planv1.TaskServiceDependRequest{TaskId: a.GetId(), Also: []*planv1.TaskAfter{{Task: a.GetCode()}}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("twice: %v", err)
	}
	if _, err := depend(&planv1.TaskServiceDependRequest{TaskId: a.GetId(), Also: []*planv1.TaskAfter{{Task: "W99"}}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a task of no wish: %v", err)
	}
	if got := deps(); !slices.EqualFunc(got, before, slices.Equal) {
		t.Errorf("after refused calls: %v, want %v", got, before)
	}
}
