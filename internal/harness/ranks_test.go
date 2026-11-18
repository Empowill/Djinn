package harness

import (
	"maps"
	"path/filepath"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// TestRanks covers Harness.Ranks, the order the gates serve their waiters in: each task gets its wish's position among
// the active wishes by the rank the developer sets in the window, the first from 0; the task of a paused wish, or an
// unknown task, gets none, and waits after them (W118).
func TestRanks(t *testing.T) {
	t.Parallel()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	active := planv1.WishState_WISH_STATE_ACTIVE
	wishes := []*planv1.Wish{
		{Id: store.NewID(), Title: "Third", State: active, Rank: 3},
		{Id: store.NewID(), Title: "First", State: active, Rank: 1},
		{Id: store.NewID(), Title: "Second", State: active, Rank: 2},
		{Id: store.NewID(), Title: "Paused", State: planv1.WishState_WISH_STATE_PAUSED},
	}
	tasks := map[string]string{} // Task id by its wish's title.
	if err := db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/put", wishes[0]); err != nil {
			return err
		}
		for _, w := range wishes {
			task := &planv1.Task{Id: store.NewID(), WishId: w.GetId(), Title: "work of " + w.GetTitle()}
			tasks[w.GetTitle()] = task.GetId()
			if err := tx.Put(w); err != nil {
				return err
			}
			if err := tx.Put(task); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := &Harness{store: db}
	got := h.Ranks(t.Context(), []string{tasks["Third"], tasks["First"], tasks["Second"], tasks["Paused"], store.NewID()})
	want := map[string]int{tasks["First"]: 0, tasks["Second"]: 1, tasks["Third"]: 2}
	if !maps.Equal(got, want) {
		t.Errorf("ranks = %v, want %v", got, want)
	}
}
