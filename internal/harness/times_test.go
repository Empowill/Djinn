package harness

import (
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestTaskTimesTwoRunsAndPause: a task with two runs and a pause (fake clock) gives the three times,
// and the counter is frozen during pause.
func TestTaskTimesTwoRunsAndPause(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	t0 := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	clock := &fakeClock{at: t0}
	e := up(t, t.TempDir(), WithClock(clock.now))
	wishID, _ := e.wish(t, nativeFolder(t))

	// Fake worker prompt: start, wait for Send, then done
	task := e.mustSpawn(t, wishID, "Two Runs", "text start\nwait\ntext done", nil)
	id := task.GetId()

	got := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	if got.GetFirstStartTime() == nil {
		t.Fatalf("first start time not set on start")
	}

	// 12 minutes of first run
	t1 := t0.Add(12 * time.Minute)
	clock.set(t1)

	// Pause the task
	paused := e.pause(t, id)
	if paused.GetStatus() != planv1.TaskStatus_TASK_STATUS_PAUSED {
		t.Fatalf("task status = %v, want PAUSED", paused.GetStatus())
	}
	if len(paused.GetRuns()) != 1 {
		t.Fatalf("runs count = %d, want 1", len(paused.GetRuns()))
	}
	r1 := paused.GetRuns()[0]
	if r1.GetReason() != "pause" {
		t.Errorf("run 1 reason = %q, want pause", r1.GetReason())
	}

	// Counter frozen during pause:
	// Advance fake clock by 4m, then 10m more, verify running time stays frozen at 12m
	t2 := t1.Add(4 * time.Minute)
	clock.set(t2)
	got = e.get(t, id)
	if d := got.GetRunning().AsDuration(); d != 12*time.Minute {
		t.Errorf("running time during pause = %v, want 12m", d)
	}
	if d := got.GetPaused().AsDuration(); d != 4*time.Minute {
		t.Errorf("paused time during pause = %v, want 4m", d)
	}

	clock.set(t2.Add(10 * time.Minute))
	got = e.get(t, id)
	if d := got.GetRunning().AsDuration(); d != 12*time.Minute {
		t.Errorf("running counter frozen check = %v, want 12m", d)
	}
	clock.set(t2)

	// Resume the task
	resumed := e.resume(t, id)
	if resumed.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("resumed status = %v, want RUNNING", resumed.GetStatus())
	}
	if len(resumed.GetRuns()) != 2 {
		t.Fatalf("runs count after resume = %d, want 2", len(resumed.GetRuns()))
	}

	// 8 minutes of second run
	t3 := t2.Add(8 * time.Minute)
	clock.set(t3)

	// Unblock wait step
	if _, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{TaskId: id, Text: "go"})); err != nil {
		t.Fatal(err)
	}

	final := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if len(final.GetRuns()) != 2 {
		t.Fatalf("final runs count = %d, want 2", len(final.GetRuns()))
	}
	if final.GetRuns()[0].GetReason() != "pause" {
		t.Errorf("run 0 reason = %q, want pause", final.GetRuns()[0].GetReason())
	}
	if final.GetRuns()[1].GetReason() != "done" {
		t.Errorf("run 1 reason = %q, want done", final.GetRuns()[1].GetReason())
	}

	// Running = 12m + 8m = 20m, Paused = 4m, WaitingGate = 0
	if d := final.GetRunning().AsDuration(); d != 20*time.Minute {
		t.Errorf("final running = %v, want 20m", d)
	}
	if d := final.GetPaused().AsDuration(); d != 4*time.Minute {
		t.Errorf("final paused = %v, want 4m", d)
	}
	if d := final.GetWaitingGate().AsDuration(); d != 0 {
		t.Errorf("final waiting gate = %v, want 0", d)
	}
}

// TestRebuildTaskRunsFromJournal: existing tasks without runs are rebuilt once from the journal at Recover.
func TestRebuildTaskRunsFromJournal(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	t0 := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(12 * time.Minute)
	t2 := t1.Add(4 * time.Minute)
	t3 := t2.Add(8 * time.Minute)

	e := up(t, t.TempDir())
	wishID, projectID := e.wish(t, nativeFolder(t))

	taskID := store.NewID()
	oldTask := &planv1.Task{
		Id:         taskID,
		WishId:     wishID,
		ProjectId:  projectID,
		Code:       "W99",
		Title:      "Existing legacy task",
		Status:     planv1.TaskStatus_TASK_STATUS_DONE,
		StartTime:  timestamppb.New(t2),
		EndTime:    timestamppb.New(t3),
		CreateTime: timestamppb.New(t0.Add(-5 * time.Minute)),
		// FirstStartTime and Runs intentionally nil (legacy task before rebuild)
	}

	snapStart := &planv1.Task{Id: taskID, Status: planv1.TaskStatus_TASK_STATUS_RUNNING, StartTime: timestamppb.New(t0)}
	snapPause := &planv1.Task{Id: taskID, Status: planv1.TaskStatus_TASK_STATUS_PAUSED, EndTime: timestamppb.New(t1)}
	snapResume := &planv1.Task{Id: taskID, Status: planv1.TaskStatus_TASK_STATUS_RUNNING, StartTime: timestamppb.New(t2)}
	snapEnd := &planv1.Task{Id: taskID, Status: planv1.TaskStatus_TASK_STATUS_DONE, EndTime: timestamppb.New(t3)}

	if err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actorHarness, methodStart, snapStart); err != nil {
			return err
		}
		if err := tx.Journal(actorHarness, methodHold, snapPause); err != nil {
			return err
		}
		if err := tx.Journal(actorHarness, methodHold, snapResume); err != nil {
			return err
		}
		if err := tx.Journal(actorHarness, methodEnd, snapEnd); err != nil {
			return err
		}
		return tx.Put(oldTask)
	}); err != nil {
		t.Fatal(err)
	}

	// Trigger recover
	if err := e.h.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}

	recovered, err := store.Get[*planv1.Task](t.Context(), e.db, taskID)
	if err != nil {
		t.Fatal(err)
	}

	if recovered.GetFirstStartTime() == nil || !recovered.GetFirstStartTime().AsTime().Equal(t0) {
		t.Errorf("recovered first start time = %v, want %v", recovered.GetFirstStartTime(), t0)
	}
	if len(recovered.GetRuns()) != 2 {
		t.Fatalf("recovered runs count = %d, want 2", len(recovered.GetRuns()))
	}
	if r0 := recovered.GetRuns()[0]; r0.GetReason() != "pause" || !r0.GetStartTime().AsTime().Equal(t0) || !r0.GetEndTime().AsTime().Equal(t1) {
		t.Errorf("recovered run 0 = %v", r0)
	}
	if r1 := recovered.GetRuns()[1]; r1.GetReason() != "done" || !r1.GetStartTime().AsTime().Equal(t2) || !r1.GetEndTime().AsTime().Equal(t3) {
		t.Errorf("recovered run 1 = %v", r1)
	}

	// Check times via FillTimes
	if err := plan.FillTimes(t.Context(), e.db, []*planv1.Task{recovered}, t3); err != nil {
		t.Fatal(err)
	}
	if d := recovered.GetRunning().AsDuration(); d != 20*time.Minute {
		t.Errorf("running = %v, want 20m", d)
	}
	if d := recovered.GetPaused().AsDuration(); d != 4*time.Minute {
		t.Errorf("paused = %v, want 4m", d)
	}
}

// TestGateWaitAcrossRestart: the gate events of a running task count in its gate wait as Djinn writes them; after a
// restart, its list shows the same wait read from that count, none of its events (W283).
func TestGateWaitAcrossRestart(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	t0 := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	clock := &fakeClock{at: t0}
	e := up(t, home, WithClock(clock.now))
	wishID, _ := e.wish(t, nativeFolder(t))
	task := e.mustSpawn(t, wishID, "Gated", "wait\ntext done", nil)
	e.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	note := func(at time.Duration, text string) {
		t.Helper()
		clock.set(t0.Add(at))
		e.h.Note(task.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_GATE, Text: text})
		e.untilText(t, task.GetId(), text)
	}
	note(time.Minute, "gate build: asked")
	note(4*time.Minute, "gate build: taken")
	note(5*time.Minute, "gate test: asked")
	note(7*time.Minute, "gate test: taken")
	clock.set(t0.Add(8 * time.Minute))
	e.release(t, task.GetId())
	if got := e.ended(t, task.GetId()).GetWaitingGate().AsDuration(); got != 5*time.Minute {
		t.Errorf("before the restart: waiting gate = %v, want 3m for build and 2m for test", got)
	}
	e.down()

	s, err := store.Open(t.Context(), filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Tx(t.Context(), func(tx *store.Tx) error {
		events, err := store.List[*planv1.TaskEvent](t.Context(), tx, store.Where{"task_id": task.GetId()})
		if err != nil {
			return err
		}
		if err := tx.Journal("test", "test/forget", events[0]); err != nil {
			return err
		}
		for _, ev := range events {
			if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_GATE {
				if err := tx.Delete(ev); err != nil {
					return err
				}
			}
		}
		return nil
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}

	e = up(t, home, WithClock(clock.now))
	res, err := e.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range res.Msg.GetTasks() {
		if got.GetId() == task.GetId() && got.GetWaitingGate().AsDuration() != 5*time.Minute {
			t.Errorf("after the restart: waiting gate = %v, want 5m", got.GetWaitingGate().AsDuration())
		}
	}
}
