package plan_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

func TestFillTimes(t *testing.T) {
	t.Parallel()
	t.Run("two runs and pause", func(t *testing.T) {
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
	})

	t.Run("paused counter frozen", func(t *testing.T) {
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
	})

	t.Run("gate wait", func(t *testing.T) {
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
	})

	t.Run("azima sums parts", func(t *testing.T) {
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
	})
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

// gateStore opens the store of a data folder, as djinn up does at each start.
func gateStore(t *testing.T, home string) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// putGateEvents writes the events in one transaction; counted, as Djinn writes them since W283, or not, as Djinn wrote
// them before.
func putGateEvents(ctx context.Context, t *testing.T, s *store.Store, counted bool, events ...*planv1.TaskEvent) {
	t.Helper()
	err := s.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/put", events[0]); err != nil {
			return err
		}
		for _, ev := range events {
			if counted {
				if err := plan.CountGate(ctx, tx, ev); err != nil {
					return err
				}
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
}

// forgetEvents deletes the task's events: a fill that still finds its gate wait did not read them.
func forgetEvents(ctx context.Context, t *testing.T, s *store.Store, taskID string) {
	t.Helper()
	err := s.Tx(ctx, func(tx *store.Tx) error {
		events, err := store.List[*planv1.TaskEvent](ctx, tx, store.Where{"task_id": taskID})
		if err != nil {
			return err
		}
		if err := tx.Journal("test", "test/forget", events[0]); err != nil {
			return err
		}
		for _, ev := range events {
			if err := tx.Delete(ev); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGateWait(t *testing.T) {
	t.Parallel()
	// The gate wait of a running task follows the gate events written after the last fill,
	// counted as they are written (W283), and ignores an event that only says "gate".
	t.Run("follows new events", func(t *testing.T) {
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
				if err := plan.CountGate(ctx, tx, ev); err != nil {
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
	})

	// After a start of Djinn, a task's gate wait is the one kept as its gate events were
	// written, read without any of them (W283: each start re-read every event of every task, 3 to 25 s at the first
	// task list), and goes on with the gates of the task after it: one still asked counts while the task runs.
	t.Run("kept across restart", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		home := t.TempDir()
		t0 := time.Date(2026, 10, 17, 18, 0, 0, 0, time.UTC)
		task := &planv1.Task{
			Id: store.NewID(), WishId: "wish-1", FirstStartTime: timestamppb.New(t0), StartTime: timestamppb.New(t0),
			Status: planv1.TaskStatus_TASK_STATUS_RUNNING, Runs: []*planv1.TaskRun{{StartTime: timestamppb.New(t0)}},
		}
		gate := func(seq int64, text string, at time.Duration) *planv1.TaskEvent {
			return &planv1.TaskEvent{
				Id: store.NewID(), TaskId: task.GetId(), Seq: seq, Kind: planv1.TaskEventKind_TASK_EVENT_KIND_GATE, Text: text,
				CreateTime: timestamppb.New(t0.Add(at)),
			}
		}
		waited := func(s *store.Store, now time.Duration) time.Duration {
			t.Helper()
			if err := plan.FillTimes(ctx, s, []*planv1.Task{task}, t0.Add(now)); err != nil {
				t.Fatal(err)
			}
			return task.GetWaitingGate().AsDuration()
		}

		s := gateStore(t, home)
		putGateEvents(ctx, t, s, true, gate(1, "gate build: asked", time.Minute), gate(2, "gate build: taken", 4*time.Minute),
			gate(3, "gate test: asked", 5*time.Minute))
		if got := waited(s, 6*time.Minute); got != 4*time.Minute {
			t.Errorf("before the restart: waiting gate = %v, want 3m for build and 1m for test so far", got)
		}
		s.Close()

		s = gateStore(t, home)
		t.Cleanup(func() { s.Close() })
		forgetEvents(ctx, t, s, task.GetId())
		if got := waited(s, 8*time.Minute); got != 6*time.Minute {
			t.Errorf("after the restart: waiting gate = %v, want 3m for build and 3m for test so far", got)
		}
		putGateEvents(ctx, t, s, true, gate(4, "gate test: taken", 9*time.Minute))
		if got := waited(s, 20*time.Minute); got != 7*time.Minute {
			t.Errorf("after the restart, test taken: waiting gate = %v, want 7m", got)
		}
	})

	// A task whose gate events were written before Djinn kept its gate wait gets it counted
	// from them at its first fill, once: kept, a fill after a restart reads none of them; and a gate event written for
	// such a task counts those before it.
	t.Run("task from before", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		home := t.TempDir()
		t0 := time.Date(2026, 10, 17, 18, 0, 0, 0, time.UTC)
		newTask := func() *planv1.Task {
			return &planv1.Task{
				Id: store.NewID(), WishId: "wish-1", FirstStartTime: timestamppb.New(t0), StartTime: timestamppb.New(t0),
				EndTime: timestamppb.New(t0.Add(time.Hour)), Status: planv1.TaskStatus_TASK_STATUS_DONE,
				Runs: []*planv1.TaskRun{{StartTime: timestamppb.New(t0), EndTime: timestamppb.New(t0.Add(time.Hour))}},
			}
		}
		gate := func(task *planv1.Task, seq int64, text string, at time.Duration) *planv1.TaskEvent {
			return &planv1.TaskEvent{
				Id: store.NewID(), TaskId: task.GetId(), Seq: seq, Kind: planv1.TaskEventKind_TASK_EVENT_KIND_GATE, Text: text,
				CreateTime: timestamppb.New(t0.Add(at)),
			}
		}
		listed, written := newTask(), newTask()
		s := gateStore(t, home)
		for _, task := range []*planv1.Task{listed, written} {
			putGateEvents(ctx, t, s, false, gate(task, 1, "gate lint: asked", time.Minute), gate(task, 2, "gate lint: taken", 3*time.Minute))
		}
		// The other task gets a gate event first: it counts the two before it.
		putGateEvents(ctx, t, s, true, gate(written, 3, "gate test: asked", 10*time.Minute))
		putGateEvents(ctx, t, s, true, gate(written, 4, "gate test: taken", 15*time.Minute))

		if err := plan.FillTimes(ctx, s, []*planv1.Task{listed, written}, t0.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if got := listed.GetWaitingGate().AsDuration(); got != 2*time.Minute {
			t.Errorf("first fill: waiting gate = %v, want 2m", got)
		}
		if got := written.GetWaitingGate().AsDuration(); got != 7*time.Minute {
			t.Errorf("first fill, after a gate event: waiting gate = %v, want 2m for lint and 5m for test", got)
		}
		counted, err := store.CommandsOf(ctx, s, "harness/gate-wait", func(store.Command) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		if len(counted) != 1 {
			t.Errorf("%d gate waits counted from the events, want 1: the listed task's", len(counted))
		}
		s.Close()

		s = gateStore(t, home)
		t.Cleanup(func() { s.Close() })
		forgetEvents(ctx, t, s, listed.GetId())
		forgetEvents(ctx, t, s, written.GetId())
		listed.WaitingGate, written.WaitingGate = nil, nil
		if err := plan.FillTimes(ctx, s, []*planv1.Task{listed, written}, t0.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if got, again := listed.GetWaitingGate().AsDuration(), written.GetWaitingGate().AsDuration(); got != 2*time.Minute || again != 7*time.Minute {
			t.Errorf("after the restart: waiting gates = %v and %v, want 2m and 7m", got, again)
		}
	})
}
