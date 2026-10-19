package harness

import (
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// TestUrgentTask (W133): on a full machine, an urgent task starts at once, before the older one that waits, and runs
// beyond the slots (Forced); a second urgent task of the wish is refused, at its spawn or its update, naming the first;
// once the first has ended, the older one may be marked urgent and starts. Not marked portable: it runs workers.
func TestUrgentTask(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 1}).capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, nativeFolder(t))
	busy := e.mustSpawn(t, wishID, "Busy", "wait\ntext busy", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"s1"}})
	e.until(t, busy.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	old := e.mustSpawn(t, wishID, "Old", "text old", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"s2"}})
	if old.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || !strings.Contains(old.GetWaitReason(), "the most this machine holds") {
		t.Fatalf("old = %v; want it waiting for a slot", old)
	}

	urgent := e.mustSpawn(t, wishID, "Repair the branch", "wait\ntext repaired", &planv1.TaskServiceSpawnRequest{Urgent: true, WriteScopes: []string{"s3"}})
	if !urgent.GetUrgent() || urgent.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("urgent = %v; want it running on the full machine", urgent)
	}
	if n := e.h.Forced(); n != 1 {
		t.Errorf("Forced = %d; want the urgent worker beyond the slots", n)
	}
	if got := e.get(t, old.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
		t.Errorf("old = %v; want it still waiting", got)
	}

	// A second one, spawned or updated, is refused with the first's name.
	if _, err := e.spawnReq(t, wishID, "Another", "text x", &planv1.TaskServiceSpawnRequest{Urgent: true, WriteScopes: []string{"s4"}}); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "W3 (Repair the branch) is urgent already") {
		t.Errorf("second urgent spawn: %v", err)
	}
	update := func(id string, urgent bool) (*planv1.Task, error) {
		res, err := e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{TaskId: id, Urgent: proto.Bool(urgent)}))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetTask(), nil
	}
	if _, err := update(old.GetId(), true); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "W3 (Repair the branch)") {
		t.Errorf("second urgent update: %v", err)
	}

	// The first ended, the old one goes urgent and starts beyond the slot the busy worker holds.
	e.release(t, urgent.GetId())
	if got := e.ended(t, urgent.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("urgent ended %v", got)
	}
	if _, err := update(urgent.GetId(), true); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "W3 is done") {
		t.Errorf("urgent on a done task: %v", err)
	}
	if got, err := update(old.GetId(), true); err != nil || !got.GetUrgent() {
		t.Fatalf("old urgent: %v, %v", got, err)
	}
	e.until(t, old.GetId(), func(t *planv1.Task) bool { return t.GetStartTime() != nil })
	e.release(t, busy.GetId())
	e.ended(t, busy.GetId())
}

// TestUrgentMarkAfterSpawn (W234): djinn task update puts the urgent mark on, and takes it off, after the spawn: on a
// task planned, waiting on its question, paused or running, and the run's own writes keep a mark put while it runs
// (carry). A repair and a feedback task of the wish are urgent together, put so by update as by spawn; a second of
// either class is refused, naming the first; --feedback=false leaves a repair's mark.
func TestUrgentMarkAfterSpawn(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 1}).capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, nativeFolder(t))
	mark := func(id string, urgent, feedback *bool) (*planv1.Task, error) {
		res, err := e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{TaskId: id, Urgent: urgent, Feedback: feedback}))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetTask(), nil
	}
	isMarked := func(id string, urgent, feedback bool) {
		t.Helper()
		if got := e.get(t, id); got.GetUrgent() != urgent || got.GetFeedback() != feedback {
			t.Errorf("%s: urgent %v, feedback %v; want %v, %v", got.GetCode(), got.GetUrgent(), got.GetFeedback(), urgent, feedback)
		}
	}

	// W1 waits on its blocking question, its slot free; W2 runs; W3, planned, waits for W2.
	asker := e.mustSpawn(t, wishID, "Pick a cache", "text working\nwait\ntext waiting for the answer", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"s1"}})
	q := e.askAbout(t, e.until(t, asker.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING)), true)
	e.release(t, asker.GetId())
	e.until(t, asker.GetId(), waitsOn(q))
	busy := e.mustSpawn(t, wishID, "Busy", "wait\ntext busy", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"s2"}})
	e.until(t, busy.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	planned := e.mustSpawn(t, wishID, "Use the cache", "text using it", &planv1.TaskServiceSpawnRequest{DependsOn: []string{"w2"}, WriteScopes: []string{"s3"}})

	for _, task := range []*planv1.Task{asker, busy, planned} {
		status := e.get(t, task.GetId()).GetStatus()
		if got, err := mark(task.GetId(), proto.Bool(true), nil); err != nil || !got.GetUrgent() || got.GetFeedback() {
			t.Fatalf("%s %s marked urgent: %v, %v", task.GetCode(), short(status), got, err)
		}
		if got, err := mark(task.GetId(), proto.Bool(false), nil); err != nil || got.GetUrgent() {
			t.Fatalf("%s %s mark taken off: %v, %v", task.GetCode(), short(status), got, err)
		}
	}

	// The running worker goes urgent as a repair; beside it, the waiting task goes urgent for feedback; a second of
	// either class is refused, naming the first.
	if _, err := mark(busy.GetId(), proto.Bool(true), nil); err != nil {
		t.Fatal(err)
	}
	if got, err := mark(asker.GetId(), nil, proto.Bool(true)); err != nil || !got.GetUrgent() || !got.GetFeedback() {
		t.Fatalf("feedback beside the repair: %v, %v", got, err)
	}
	if _, err := mark(planned.GetId(), proto.Bool(true), nil); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "W2 (Busy) is urgent already") {
		t.Errorf("second repair: %v", err)
	}
	if _, err := mark(planned.GetId(), nil, proto.Bool(true)); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "W1 (Pick a cache) is urgent for feedback already") {
		t.Errorf("second feedback: %v", err)
	}

	// --feedback=false takes off a feedback mark only; --urgent=false takes off either.
	if _, err := mark(busy.GetId(), nil, proto.Bool(false)); err != nil {
		t.Fatal(err)
	}
	isMarked(busy.GetId(), true, false)
	if _, err := mark(asker.GetId(), nil, proto.Bool(false)); err != nil {
		t.Fatal(err)
	}
	isMarked(asker.GetId(), false, false)
	if _, err := mark(asker.GetId(), nil, proto.Bool(true)); err != nil {
		t.Fatal(err)
	}
	if _, err := mark(asker.GetId(), proto.Bool(false), nil); err != nil {
		t.Fatal(err)
	}
	isMarked(asker.GetId(), false, false)

	// The run's own writes keep the mark and the title put while it ran: its pause, its resume, its end. Paused, it
	// takes the mark off and on.
	if _, err := e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{TaskId: busy.GetId(), Title: proto.String("Busy, renamed")})); err != nil {
		t.Fatal(err)
	}
	if got := e.pause(t, busy.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PAUSED {
		t.Fatalf("paused = %v", got)
	}
	isMarked(busy.GetId(), true, false)
	if _, err := mark(busy.GetId(), proto.Bool(false), nil); err != nil {
		t.Fatalf("mark taken off the paused task: %v", err)
	}
	isMarked(busy.GetId(), false, false)
	if _, err := mark(busy.GetId(), nil, proto.Bool(true)); err != nil {
		t.Fatalf("paused task marked urgent: %v", err)
	}
	e.resume(t, busy.GetId())
	isMarked(busy.GetId(), true, true)
	deadline := time.Now().Add(10 * time.Second)
	for e.get(t, busy.GetId()).GetEndTime() == nil { // The resumed fake may wait again: a message lets it go on.
		if time.Now().After(deadline) {
			t.Fatal("the resumed worker never ended")
		}
		_, _ = e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{TaskId: busy.GetId(), Text: "go on"}))
		time.Sleep(5 * time.Millisecond)
	}
	if got := e.get(t, busy.GetId()); got.GetUrgent() != true || got.GetFeedback() != true || got.GetTitle() != "Busy, renamed" {
		t.Errorf("ended = %v; want its mark and its new title kept", got)
	}
}
