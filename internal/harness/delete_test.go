package harness

import (
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestDelete: a task no worker ran goes, with its events; a task a worker ran stays.
func TestDelete(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())
	planned := &planv1.Task{Id: "01a118d0-0000-7000-8000-0000000000d1", WishId: wishID, Code: "T01", Title: "a plan item",
		Status: planv1.TaskStatus_TASK_STATUS_PENDING}
	ev := &planv1.TaskEvent{Id: "01a118d0-0000-7000-8000-0000000000e1", TaskId: planned.GetId(), Seq: 1,
		Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "imported"}
	err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "put", planned); err != nil {
			return err
		}
		if err := tx.Put(planned); err != nil {
			return err
		}
		return tx.Put(ev)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.Delete(t.Context(), connect.NewRequest(&planv1.TaskServiceDeleteRequest{TaskId: planned.GetId()})); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get[*planv1.Task](t.Context(), e.db, planned.GetId()); err == nil {
		t.Error("the planned task is still there")
	}
	if left, _ := store.List[*planv1.TaskEvent](t.Context(), e.db, store.Where{"task_id": planned.GetId()}); len(left) != 0 {
		t.Errorf("%d events left", len(left))
	}

	ran := e.spawn(t, wishID, "text done")
	e.watch(t.Context(), t, ran.GetId(), 0)
	_, err = e.tasks.Delete(t.Context(), connect.NewRequest(&planv1.TaskServiceDeleteRequest{TaskId: ran.GetId()}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("delete a task a worker ran: %v, want failed precondition", err)
	}
}
