package plan_test

import (
	"runtime"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

func TestFillLeafTimesTwoRunsAndPause(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	tPlanned := t0.Add(-5 * time.Minute)
	tRun1End := t0.Add(12 * time.Minute)
	tRun2Start := tRun1End.Add(4 * time.Minute)
	tRun2End := tRun2Start.Add(8 * time.Minute)

	task := &planv1.Task{
		Id:             "task-1",
		WishId:         "wish-1",
		CreateTime:     timestamppb.New(tPlanned),
		FirstStartTime: timestamppb.New(t0),
		StartTime:      timestamppb.New(tRun2Start),
		EndTime:        timestamppb.New(tRun2End),
		Status:         planv1.TaskStatus_TASK_STATUS_DONE,
		Runs: []*planv1.TaskRun{
			{
				StartTime: timestamppb.New(t0),
				EndTime:   timestamppb.New(tRun1End),
				Reason:    "pause",
			},
			{
				StartTime: timestamppb.New(tRun2Start),
				EndTime:   timestamppb.New(tRun2End),
				Reason:    "done",
			},
		},
	}

	if err := plan.FillTimes(t.Context(), nil, []*planv1.Task{task}, tRun2End); err != nil {
		t.Fatal(err)
	}

	if got := task.GetRunning().AsDuration(); got != 20*time.Minute {
		t.Errorf("running = %v, want 20m", got)
	}
	if got := task.GetPaused().AsDuration(); got != 4*time.Minute {
		t.Errorf("paused = %v, want 4m", got)
	}
	if got := task.GetWaitingGate().AsDuration(); got != 0 {
		t.Errorf("waiting gate = %v, want 0", got)
	}
	if got := task.GetWaitingBeforeStart().AsDuration(); got != 5*time.Minute {
		t.Errorf("waiting before start = %v, want 5m", got)
	}
}

func TestFillLeafTimesPausedCounterFrozen(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(12 * time.Minute)

	task := &planv1.Task{
		Id:             "task-paused",
		WishId:         "wish-1",
		FirstStartTime: timestamppb.New(t0),
		StartTime:      timestamppb.New(t0),
		Status:         planv1.TaskStatus_TASK_STATUS_PAUSED,
		Runs: []*planv1.TaskRun{
			{
				StartTime: timestamppb.New(t0),
				EndTime:   timestamppb.New(t1),
				Reason:    "pause",
			},
		},
	}

	now1 := t1.Add(4 * time.Minute)
	if err := plan.FillTimes(t.Context(), nil, []*planv1.Task{task}, now1); err != nil {
		t.Fatal(err)
	}
	if got := task.GetRunning().AsDuration(); got != 12*time.Minute {
		t.Errorf("running at now1 = %v, want 12m", got)
	}
	if got := task.GetPaused().AsDuration(); got != 4*time.Minute {
		t.Errorf("paused at now1 = %v, want 4m", got)
	}

	// Later check: running counter is completely frozen while paused
	now2 := t1.Add(30 * time.Minute)
	if err := plan.FillTimes(t.Context(), nil, []*planv1.Task{task}, now2); err != nil {
		t.Fatal(err)
	}
	if got := task.GetRunning().AsDuration(); got != 12*time.Minute {
		t.Errorf("running at now2 = %v, want 12m (frozen)", got)
	}
	if got := task.GetPaused().AsDuration(); got != 30*time.Minute {
		t.Errorf("paused at now2 = %v, want 30m", got)
	}
}

func TestFillLeafTimesWithGateWait(t *testing.T) {
	t.Parallel()
	s, err := store.Open(t.Context(), "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}

	t0 := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	tGateAsk := t0.Add(2 * time.Minute)
	tGateTake := t0.Add(5 * time.Minute)
	tEnd := t0.Add(15 * time.Minute)

	task := &planv1.Task{
		Id:             store.NewID(),
		WishId:         "wish-1",
		FirstStartTime: timestamppb.New(t0),
		StartTime:      timestamppb.New(t0),
		EndTime:        timestamppb.New(tEnd),
		Status:         planv1.TaskStatus_TASK_STATUS_DONE,
		Runs: []*planv1.TaskRun{
			{
				StartTime: timestamppb.New(t0),
				EndTime:   timestamppb.New(tEnd),
				Reason:    "done",
			},
		},
	}

	ev1 := &planv1.TaskEvent{
		Id:         store.NewID(),
		TaskId:     task.GetId(),
		Seq:        1,
		Kind:       planv1.TaskEventKind_TASK_EVENT_KIND_GATE,
		Text:       "gate build: asked",
		CreateTime: timestamppb.New(tGateAsk),
	}
	ev2 := &planv1.TaskEvent{
		Id:         store.NewID(),
		TaskId:     task.GetId(),
		Seq:        2,
		Kind:       planv1.TaskEventKind_TASK_EVENT_KIND_GATE,
		Text:       "gate build: taken",
		CreateTime: timestamppb.New(tGateTake),
	}

	if err := s.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/put", task); err != nil {
			return err
		}
		if err := tx.Put(task); err != nil {
			return err
		}
		if err := tx.Journal("test", "test/put", ev1); err != nil {
			return err
		}
		if err := tx.Put(ev1); err != nil {
			return err
		}
		if err := tx.Journal("test", "test/put", ev2); err != nil {
			return err
		}
		return tx.Put(ev2)
	}); err != nil {
		t.Fatal(err)
	}

	if err := plan.FillTimes(t.Context(), s, []*planv1.Task{task}, tEnd); err != nil {
		t.Fatal(err)
	}

	// 15m total run - 3m gate wait = 12m running
	if got := task.GetWaitingGate().AsDuration(); got != 3*time.Minute {
		t.Errorf("waiting gate = %v, want 3m", got)
	}
	if got := task.GetRunning().AsDuration(); got != 12*time.Minute {
		t.Errorf("running = %v, want 12m", got)
	}
}

func TestAzimaTimesSum(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	azima := &planv1.Task{
		Id:     "azima-1",
		WishId: "wish-1",
		Kind:   planv1.TaskKind_TASK_KIND_AZIMA,
		Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
	}

	p1 := &planv1.Task{
		Id:             "part-1",
		WishId:         "wish-1",
		PartOf:         "azima-1",
		Kind:           planv1.TaskKind_TASK_KIND_WORK,
		FirstStartTime: timestamppb.New(t0),
		StartTime:      timestamppb.New(t0),
		EndTime:        timestamppb.New(t0.Add(10 * time.Minute)),
		Status:         planv1.TaskStatus_TASK_STATUS_DONE,
		Runs: []*planv1.TaskRun{
			{
				StartTime: timestamppb.New(t0),
				EndTime:   timestamppb.New(t0.Add(10 * time.Minute)),
				Reason:    "done",
			},
		},
	}

	p2 := &planv1.Task{
		Id:             "part-2",
		WishId:         "wish-1",
		PartOf:         "azima-1",
		Kind:           planv1.TaskKind_TASK_KIND_WORK,
		FirstStartTime: timestamppb.New(t0),
		StartTime:      timestamppb.New(t0),
		EndTime:        timestamppb.New(t0.Add(15 * time.Minute)),
		Status:         planv1.TaskStatus_TASK_STATUS_DONE,
		Runs: []*planv1.TaskRun{
			{
				StartTime: timestamppb.New(t0),
				EndTime:   timestamppb.New(t0.Add(15 * time.Minute)),
				Reason:    "done",
			},
		},
	}

	tasks := []*planv1.Task{azima, p1, p2}
	if err := plan.FillTimes(t.Context(), nil, tasks, t0.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Azima sums running times of its parts: 10m + 15m = 25m
	if got := azima.GetRunning().AsDuration(); got != 25*time.Minute {
		t.Errorf("azima running = %v, want 25m", got)
	}
}

// TestGateWaitFollowsNewEvents: the gate wait of a running task follows the events written after the last fill, which
// reads only those (W504), and ignores an event that only says "gate".
func TestGateWaitFollowsNewEvents(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := store.Open(ctx, "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	t0 := time.Date(2026, 10, 12, 18, 0, 0, 0, time.UTC)
	task := &planv1.Task{
		Id: store.NewID(), WishId: "wish-1", FirstStartTime: timestamppb.New(t0), StartTime: timestamppb.New(t0),
		Status: planv1.TaskStatus_TASK_STATUS_RUNNING, Runs: []*planv1.TaskRun{{StartTime: timestamppb.New(t0)}},
	}
	seq := int64(0)
	write := func(kind planv1.TaskEventKind, text string, at time.Duration) {
		t.Helper()
		seq++
		ev := &planv1.TaskEvent{
			Id: store.NewID(), TaskId: task.GetId(), Seq: seq, Kind: kind, Text: text, CreateTime: timestamppb.New(t0.Add(at)),
		}
		err := s.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal("test", "test/put", ev); err != nil {
				return err
			}
			return tx.Put(ev)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	waited := func(now time.Duration) time.Duration {
		t.Helper()
		if err := plan.FillTimes(ctx, s, []*planv1.Task{task}, t0.Add(now)); err != nil {
			t.Fatal(err)
		}
		return task.GetWaitingGate().AsDuration()
	}
	gate := planv1.TaskEventKind_TASK_EVENT_KIND_GATE
	write(planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, "gate build: asked", time.Minute) // A worker's words, no gate.
	if got := waited(10 * time.Minute); got != 0 {
		t.Errorf("waiting gate before any gate = %v, want 0", got)
	}
	write(gate, "gate build: asked", 2*time.Minute)
	if got := waited(4 * time.Minute); got != 2*time.Minute {
		t.Errorf("waiting gate on a gate asked = %v, want 2m", got)
	}
	write(gate, "gate build: taken", 5*time.Minute)
	write(planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, "built", 6*time.Minute)
	write(gate, "gate test: asked", 7*time.Minute)
	if got := waited(10 * time.Minute); got != 6*time.Minute {
		t.Errorf("waiting gate = %v, want 3m for build and 3m for test so far", got)
	}
	write(gate, "gate test: taken", 11*time.Minute)
	if got := waited(20 * time.Minute); got != 7*time.Minute {
		t.Errorf("waiting gate = %v, want 7m", got)
	}
}

// TestFillTimesDecodesNoWorkerLine is the cause of W504: a task list filled each task's times by decoding every event
// of the task, half a gigabyte on the developer's wishes, holding the store's one connection for half a minute while
// every other call waited. A fill decodes the gate events only, and a fill again reads none it read.
func TestFillTimesDecodesNoWorkerLine(t *testing.T) {
	ctx := t.Context()
	s, err := store.Open(ctx, "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	t0 := time.Date(2026, 10, 12, 18, 0, 0, 0, time.UTC)
	task := &planv1.Task{
		Id: store.NewID(), WishId: "wish-1", FirstStartTime: timestamppb.New(t0), StartTime: timestamppb.New(t0),
		Status: planv1.TaskStatus_TASK_STATUS_RUNNING, Runs: []*planv1.TaskRun{{StartTime: timestamppb.New(t0)}},
	}
	const lines = 3000
	err = s.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/put", task); err != nil {
			return err
		}
		for seq := int64(1); seq <= lines+2; seq++ {
			ev := &planv1.TaskEvent{
				Id: store.NewID(), TaskId: task.GetId(), Seq: seq, Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT,
				Text: "a line a worker wrote", CreateTime: timestamppb.New(t0.Add(time.Duration(seq) * time.Second)),
			}
			switch seq {
			case 10:
				ev.Kind, ev.Text = planv1.TaskEventKind_TASK_EVENT_KIND_GATE, "gate test: asked"
			case 70:
				ev.Kind, ev.Text = planv1.TaskEventKind_TASK_EVENT_KIND_GATE, "gate test: taken"
			}
			if err := tx.Put(ev); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	now := t0.Add(2 * time.Hour)
	fill := func() {
		if err := plan.FillTimes(ctx, s, []*planv1.Task{task}, now); err != nil {
			t.Fatal(err)
		}
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fill()
	runtime.ReadMemStats(&after)
	// Decoding each line costs a few allocations: 3000 lines cost over 10 000.
	if allocs := after.Mallocs - before.Mallocs; allocs > lines/2 {
		t.Errorf("a first fill allocated %d times: it decodes the worker's lines", allocs)
	}
	if got := task.GetWaitingGate().AsDuration(); got != time.Minute {
		t.Errorf("waiting gate = %v, want 1m", got)
	}
	if allocs := testing.AllocsPerRun(5, fill); allocs > 200 {
		t.Errorf("a fill again allocated %v times: it reads the events it read", allocs)
	}
}
