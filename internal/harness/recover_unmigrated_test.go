package harness

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// TestRecoverUnmigrated (Q42, W79): a part of the store whose migration failed at start stopped djinn up in Recover,
// which read or wrote it; each step of Recover that needs it now passes its turn, and the rest recovers. A task the
// last djinn up left running is interrupted all the same, but where its own table is the part missing.
func TestRecoverUnmigrated(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		table       string // the table made unmigratable: an index takes its name
		interrupted bool   // the running task is interrupted
	}{
		{"command", false}, // the journal: nothing can change
		{"task", false},
		{"task_event", true},
		{"lead_event", true},
		{"wish", true},
	} {
		t.Run(c.table, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			// Started, with no runs: Recover rebuilds them from the journal first.
			running := &planv1.Task{Id: store.NewID(), WishId: store.NewID(), Code: "W7", Title: "cut short",
				Status: planv1.TaskStatus_TASK_STATUS_RUNNING, StartTime: timestamppb.Now()}
			putStored(t, home, running, "work")
			path := filepath.Join(home, store.File)
			raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
			if err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{
				`ALTER TABLE ` + c.table + ` RENAME TO broken`, `CREATE INDEX ` + c.table + ` ON broken (id)`,
			} {
				if _, err := raw.ExecContext(t.Context(), q); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			raw.Close()

			db, err := store.Open(t.Context(), path, plan.Entities()...)
			if err != nil {
				t.Fatalf("Open = %v, want the database opened", err)
			}
			defer db.Close()
			h := New(db, home, testProviders())
			defer h.Close()
			if err := h.Recover(t.Context()); err != nil {
				t.Fatalf("Recover = %v, want nil", err)
			}
			got, err := store.Get[*planv1.Task](t.Context(), db, running.GetId())
			if c.table == "task" {
				if !errors.Is(err, store.ErrUnmigrated) {
					t.Errorf("get the task = %v, want ErrUnmigrated", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := planv1.TaskStatus_TASK_STATUS_RUNNING
			if c.interrupted {
				want = planv1.TaskStatus_TASK_STATUS_INTERRUPTED
			}
			if got.GetStatus() != want {
				t.Errorf("task status = %v, want %v", got.GetStatus(), want)
			}
		})
	}
}
