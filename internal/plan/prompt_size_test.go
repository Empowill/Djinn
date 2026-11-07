package plan_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// put writes ms in one transaction, each event counted as Djinn writes it (CountGate, CountPrompt).
func put(t *testing.T, s *store.Store, count bool, ms ...proto.Message) {
	t.Helper()
	ctx := t.Context()
	err := s.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/put", &planv1.Task{}); err != nil {
			return err
		}
		for _, m := range ms {
			if ev, ok := m.(*planv1.TaskEvent); ok && count {
				if err := plan.CountGate(ctx, tx, ev); err != nil {
					return err
				}
				if err := plan.CountPrompt(ctx, tx, ev); err != nil {
					return err
				}
			}
			if err := tx.Put(m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func open(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func event(task string, seq int64, kind planv1.TaskEventKind, text string) *planv1.TaskEvent {
	return &planv1.TaskEvent{Id: store.NewID(), TaskId: task, Seq: seq, Kind: kind, Text: text}
}

func lengths(t *testing.T, r store.Reader, tasks ...*planv1.Task) []int64 {
	t.Helper()
	if err := plan.PromptLengths(t.Context(), r, tasks); err != nil {
		t.Fatal(err)
	}
	out := make([]int64, len(tasks))
	for i, task := range tasks {
		out[i] = task.GetPromptLength()
	}
	return out
}

// TestPromptSize pins that a task's prompt size is set as its first prompt is written, and read on a query: the
// Statistics tab's prompt card read the prompt on the stored task, where it never is, and stayed empty (W616).
func TestPromptSize(t *testing.T) {
	t.Parallel()
	prompt := planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT
	status := planv1.TaskEventKind_TASK_EVENT_KIND_STATUS

	t.Run("kept as written", func(t *testing.T) {
		t.Parallel()
		s := open(t)
		task := &planv1.Task{Id: store.NewID()}
		// Letters, not bytes: 4 accented letters are 8 bytes. A later prompt (a continue) keeps the first's size.
		put(t, s, true, task, event(task.GetId(), 1, status, "planned"),
			event(task.GetId(), 2, prompt, strings.Repeat(string(rune(0xe9)), 4)+" go"))
		put(t, s, true, event(task.GetId(), 3, prompt, "continue, please, and finish the work"))
		if got := lengths(t, s, task, &planv1.Task{Id: store.NewID()}); got[0] != 7 || got[1] != 0 {
			t.Errorf("lengths = %v; want 7 for the first prompt, 0 for a task without one", got)
		}
	})

	t.Run("kept for the tasks from before", func(t *testing.T) {
		t.Parallel()
		s := open(t)
		ctx := t.Context()
		old, none, kept := &planv1.Task{Id: store.NewID()}, &planv1.Task{Id: store.NewID()}, &planv1.Task{Id: store.NewID()}
		// Written without CountPrompt, as before PromptSize; kept has its size already, which the backfill keeps.
		put(t, s, false, old, none, kept, event(old.GetId(), 1, status, "planned"),
			event(old.GetId(), 2, prompt, "first prompt"), event(old.GetId(), 3, prompt, "a much longer second prompt"),
			event(none.GetId(), 1, status, "planned"), &planv1.PromptSize{Id: kept.GetId(), Length: 42})
		tasks := []*planv1.Task{old, none, kept}
		if err := plan.KeepPromptSizes(ctx, s, tasks); err != nil {
			t.Fatal(err)
		}
		if got := lengths(t, s, tasks...); got[0] != 12 || got[1] != 0 || got[2] != 42 {
			t.Errorf("lengths = %v; want [12 0 42]", got)
		}
		if _, err := store.Get[*planv1.PromptSize](ctx, s, none.GetId()); err != nil {
			t.Errorf("a task without a prompt: %v; want a size of 0 kept, never read again", err)
		}
		var n atomic.Int64
		if err := plan.KeepPromptSizes(ctx, s, tasks); err != nil {
			t.Fatal(err)
		}
		if lengths(t, store.Counted(s, &n), tasks...); n.Load() != 1 {
			t.Errorf("read %d statements; want 1 for 3 tasks", n.Load())
		}
	})
}

// TestReadTimesOnePass pins that a query's times read the kept gate waits in one statement per 500 tasks, and, when
// it holds every task, no wish again: each query of the Statistics tab read a gate wait per task, 2.4 s for the tab
// on a real Djinn (W616).
func TestReadTimesOnePass(t *testing.T) {
	t.Parallel()
	s := open(t)
	t0 := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	azima := &planv1.Task{Id: store.NewID(), WishId: "wish", Kind: planv1.TaskKind_TASK_KIND_AZIMA}
	all := []proto.Message{azima}
	for i := range 600 {
		id := store.NewID()
		all = append(all, &planv1.Task{Id: id, WishId: "wish", PartOf: azima.GetId(), StartTime: timestamppb.New(t0),
			EndTime: timestamppb.New(t0.Add(time.Hour)), Status: planv1.TaskStatus_TASK_STATUS_DONE},
			&planv1.GateWait{Id: id, Waited: durationpb.New(time.Duration(i) * time.Second)})
	}
	put(t, s, false, all...)
	tasks, err := store.List[*planv1.Task](t.Context(), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	var n atomic.Int64
	if err := plan.ReadTimes(context.Background(), store.Counted(s, &n), tasks, t0.Add(time.Hour), true); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 {
		t.Errorf("read %d statements for 600 tasks; want 2", n.Load())
	}
	var sum time.Duration
	for _, task := range tasks[1:] {
		sum += task.GetWaitingGate().AsDuration()
	}
	if want := 599 * 600 / 2 * time.Second; sum != want || tasks[0].GetWaitingGate().AsDuration() != want {
		t.Errorf("gate waits = %v, azima's %v; want %v each", sum, tasks[0].GetWaitingGate().AsDuration(), want)
	}
}
