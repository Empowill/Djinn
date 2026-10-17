package harness

import (
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestUrgentTask (W133): on a full machine, an urgent task starts at once, before the older one that waits, and runs
// beyond the slots (Forced); a second urgent task of the wish is refused, at its spawn or its update, naming the first;
// once the first has ended, the older one may be marked urgent and starts. Not marked portable: it runs workers.
func TestUrgentTask(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 1}).capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))
	busy := e.mustSpawn(t, wishID, "Busy", "wait\ntext busy", nil)
	e.until(t, busy.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	old := e.mustSpawn(t, wishID, "Old", "text old", nil)
	if old.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || !strings.Contains(old.GetWaitReason(), "the most this machine holds") {
		t.Fatalf("old = %v; want it waiting for a slot", old)
	}

	urgent := e.mustSpawn(t, wishID, "Repair the branch", "wait\ntext repaired", &planv1.TaskServiceSpawnRequest{Urgent: true})
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
	if _, err := e.spawnReq(t, wishID, "Another", "text x", &planv1.TaskServiceSpawnRequest{Urgent: true}); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
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
