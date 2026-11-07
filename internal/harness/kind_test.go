package harness

import (
	"path/filepath"
	"sync/atomic"
	"testing"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// assertKind fails unless task, as stored, is of kind want: Djinn gives a correction, a review and a watcher their
// kind at creation (Q382).
func assertKind(t *testing.T, task *planv1.Task, want planv1.TaskKind) {
	t.Helper()
	if got := task.GetKind(); got != want {
		t.Errorf("%s stored as %s; want %s", task.GetCode(), got, want)
	}
}

// TestMigrateKinds: djinn up gives the corrections, reviews and watchers stored before they were kinds the kind their
// fields tell, leaves work and every other kind as they are, and finds none to change the next time.
func TestMigrateKinds(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	wishID := store.NewID()
	done := planv1.TaskStatus_TASK_STATUS_DONE
	task := func(code string, set func(*planv1.Task)) *planv1.Task {
		x := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: code, Title: code, Status: done}
		set(x)
		return x
	}
	correction, review := &planv1.TaskCorrection{Attempt: 1}, &planv1.TaskReview{}
	want := map[*planv1.Task]planv1.TaskKind{
		task("W1", func(x *planv1.Task) { x.Correction = correction }):                   planv1.TaskKind_TASK_KIND_CORRECTION,
		task("W2", func(x *planv1.Task) { x.Review, x.Correction = review, correction }): planv1.TaskKind_TASK_KIND_REVIEW,
		task("W3", func(x *planv1.Task) { x.Provider = planv1.Provider_PROVIDER_WATCH }): planv1.TaskKind_TASK_KIND_WATCHER,
		task("W4", func(x *planv1.Task) {
			x.Kind, x.Correction = planv1.TaskKind_TASK_KIND_WORK, correction
		}): planv1.TaskKind_TASK_KIND_CORRECTION,
		task("W5", func(x *planv1.Task) {
			x.Provider = planv1.Provider_PROVIDER_CLAUDE
		}): planv1.TaskKind_TASK_KIND_UNSPECIFIED,
		task("W6", func(x *planv1.Task) {
			x.Kind = planv1.TaskKind_TASK_KIND_INVESTIGATION
		}): planv1.TaskKind_TASK_KIND_INVESTIGATION,
	}
	err = db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
			return err
		}
		for x := range want {
			if err := tx.Put(x); err != nil {
				return err
			}
		}
		return nil
	})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	e := up(t, home)
	for x, k := range want {
		assertKind(t, e.get(t, x.GetId()), k)
	}

	// Once more, as the next djinn up: nothing is written.
	var puts atomic.Int32
	e.db.OnCommit(func(ms []proto.Message) {
		for _, m := range ms {
			if x, ok := m.(*planv1.Task); ok && x.GetWishId() == wishID {
				puts.Add(1)
			}
		}
	})
	tasks, err := store.List[*planv1.Task](t.Context(), e.db, store.Where{"wish_id": wishID})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.h.migrateKinds(t.Context(), tasks); err != nil || puts.Load() != 0 {
		t.Errorf("migrateKinds again: %v, %d tasks written; want none", err, puts.Load())
	}
}
