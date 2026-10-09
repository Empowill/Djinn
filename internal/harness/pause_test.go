package harness

import (
	"cmp"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// ticks is a long script: a tick every 30 ms, for about 6 s.
func ticks() string {
	var b strings.Builder
	for i := range 200 {
		fmt.Fprintf(&b, "text tick %d\nsleep 30ms\n", i)
	}
	return b.String()
}

// storedEvents are the task's events as stored, in order.
func (e *env) storedEvents(t *testing.T, id string) []*planv1.TaskEvent {
	t.Helper()
	events, err := store.List[*planv1.TaskEvent](t.Context(), e.db, store.Where{"task_id": id})
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(events, func(a, b *planv1.TaskEvent) int { return cmp.Compare(a.GetSeq(), b.GetSeq()) })
	return events
}

// tickCount is how many ticks the task has said.
func (e *env) tickCount(t *testing.T, id string) int {
	t.Helper()
	n := 0
	for _, ev := range e.storedEvents(t, id) {
		if strings.HasPrefix(ev.GetText(), "tick ") {
			n++
		}
	}
	return n
}

func (e *env) pause(t *testing.T, id string) *planv1.Task {
	t.Helper()
	res, err := e.tasks.Pause(t.Context(), connect.NewRequest(&planv1.TaskServicePauseRequest{TaskId: id}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetTask()
}

func (e *env) resume(t *testing.T, id string) *planv1.Task {
	t.Helper()
	res, err := e.tasks.Resume(t.Context(), connect.NewRequest(&planv1.TaskServiceResumeRequest{TaskId: id}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetTask()
}

// TestPauseResume: a paused worker says nothing and frees its slot, goes on when resumed, and stops while paused.
func TestPauseResume(t *testing.T) {
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 1}).capacity), WithTick(20*time.Millisecond))
	wishID, _ := e.wish(t, gitRepo(t))
	long := e.mustSpawn(t, wishID, "Long", ticks(), nil)
	id := long.GetId()
	waitFor(t, "the first ticks", func() bool { return e.tickCount(t, id) >= 2 })

	// Pause: the task says so, and its worker says nothing more.
	if got := e.pause(t, id); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PAUSED {
		t.Fatalf("paused task = %v", got)
	}
	if _, err := e.tasks.Pause(t.Context(), connect.NewRequest(&planv1.TaskServicePauseRequest{TaskId: id})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("pause twice: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // A tick already on its way may still land.
	before := e.tickCount(t, id)
	time.Sleep(400 * time.Millisecond)
	if after := e.tickCount(t, id); after != before {
		t.Errorf("%d ticks while paused", after-before)
	}
	if !slices.ContainsFunc(e.storedEvents(t, id), func(ev *planv1.TaskEvent) bool {
		return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.HasPrefix(ev.GetText(), "paused: ")
	}) {
		t.Errorf("no paused event: %v", e.storedEvents(t, id))
	}

	// The paused worker takes no slot: with one slot, another task starts at once.
	other := e.mustSpawn(t, wishID, "Other", "text other", nil)
	if other.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("with a paused worker, another task waits: %v", other)
	}
	if got := e.ended(t, other.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("other = %v", got)
	}

	// Resume: the output goes on where it was.
	if got := e.resume(t, id); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("resumed task = %v", got)
	}
	if _, err := e.tasks.Resume(t.Context(), connect.NewRequest(&planv1.TaskServiceResumeRequest{TaskId: id})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("resume a running task: %v", err)
	}
	waitFor(t, "ticks after resuming", func() bool { return e.tickCount(t, id) > before })
	for i, ev := range e.storedEvents(t, id) {
		if want := int64(i + 1); ev.GetSeq() != want {
			t.Fatalf("event %d has seq %d", i, ev.GetSeq())
		}
	}
	var said []string
	for _, ev := range e.storedEvents(t, id) {
		if strings.HasPrefix(ev.GetText(), "tick ") {
			said = append(said, ev.GetText())
		}
	}
	for i, s := range said {
		if s != fmt.Sprintf("tick %d", i) {
			t.Fatalf("ticks out of order or lost: %q", said)
		}
	}

	// Stop while paused: the task stops, at once.
	e.pause(t, id)
	start := time.Now()
	res, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: id}))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Msg.GetTask(); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED {
		t.Errorf("stopped while paused = %v", got)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("stopping a paused task took %v", d)
	}

	// Only a running worker pauses.
	if _, err := e.tasks.Pause(t.Context(), connect.NewRequest(&planv1.TaskServicePauseRequest{TaskId: id})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("pause a stopped task: %v", err)
	}
}

// TestPauseHoldingGate: a worker that holds a gate is not paused, which would freeze the gate for every other
// worker, nor one that waits for a gate, which would hold it frozen once its turn comes; it is once it has given the
// gate back.
func TestPauseHoldingGate(t *testing.T) {
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, gitRepo(t))
	long := e.mustSpawn(t, wishID, "Long", ticks(), nil)
	id := long.GetId()
	var mu sync.Mutex
	held, waiting := map[string][]string{}, map[string][]string{id: {"test"}}
	of := func(gates map[string][]string) func(string) []string {
		return func(taskID string) []string {
			mu.Lock()
			defer mu.Unlock()
			return gates[taskID]
		}
	}
	e.h.HeldGates(of(held), of(waiting))
	_, err := e.tasks.Pause(t.Context(), connect.NewRequest(&planv1.TaskServicePauseRequest{TaskId: id}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), long.GetCode()+" waits for the gate test: wait or stop it") {
		t.Fatalf("pause while waiting for a gate: %v", err)
	}

	// Granted, it holds the gate.
	mu.Lock()
	delete(waiting, id)
	held[id] = []string{"test"}
	mu.Unlock()
	_, err = e.tasks.Pause(t.Context(), connect.NewRequest(&planv1.TaskServicePauseRequest{TaskId: id}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), long.GetCode()+" holds the gate test: wait or stop it") {
		t.Fatalf("pause while holding a gate: %v", err)
	}
	if got, err := store.Get[*planv1.Task](t.Context(), e.db, id); err != nil || got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("after a refused pause: %v, %v", got.GetStatus(), err)
	}
	mu.Lock()
	held[id] = []string{"e2e", "test"}
	mu.Unlock()
	if _, err := e.tasks.Pause(t.Context(), connect.NewRequest(&planv1.TaskServicePauseRequest{TaskId: id})); err == nil ||
		!strings.Contains(err.Error(), "holds the gates e2e, test") {
		t.Errorf("pause while holding two gates: %v", err)
	}

	// Given back: the pause goes through, wherever the worker can pause.
	mu.Lock()
	delete(held, id)
	mu.Unlock()
	if runtime.GOOS == "windows" {
		return
	}
	if got := e.pause(t, id); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PAUSED {
		t.Errorf("paused once the gate is given back = %v", got)
	}
}

// TestPausedInterrupted: a task paused when djinn up stops is interrupted, its worker stopped, and the next start
// resumes it; one left paused in the store by a crash is interrupted at the next start, as a running one, then
// resumed.
func TestPausedInterrupted(t *testing.T) {
	home := t.TempDir()
	e := up(t, home)
	wishID, _ := e.wish(t, gitRepo(t))
	long := e.mustSpawn(t, wishID, "Long", ticks(), nil)
	e.pause(t, long.GetId())
	start := time.Now()
	e.down()
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("stopping djinn up with a paused worker took %v", d)
	}
	if got := storedTask(t, home, long.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_INTERRUPTED {
		t.Errorf("paused task after djinn up stopped = %v", got)
	}
	e = up(t, home)
	got := e.until(t, long.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))

	crashed := proto.CloneOf(got)
	crashed.Id, crashed.Code, crashed.Status = store.NewID(), "W9", planv1.TaskStatus_TASK_STATUS_PAUSED
	crashed.EndTime, crashed.Error, crashed.Resumes = nil, "", 0
	putTask(t, e.db, crashed, "x")
	e.down()
	e = up(t, home)
	e.until(t, crashed.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if !hasText(e.watch(t.Context(), t, crashed.GetId(), 0), "interrupted: djinn up ended while the worker ran") {
		t.Errorf("the task left paused by a crash was not interrupted first")
	}
}
