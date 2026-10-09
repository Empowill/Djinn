package harness

import (
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestWishPauseStopsItsWorkers: pausing a wish stops its worker, and the task waits to resume with its wish; making
// the wish active again starts the worker on its session, told why, without spending a resume.
func TestWishPauseStopsItsWorkers(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir(), WithTick(20*time.Millisecond))
	wishID, _ := e.wish(t, gitRepo(t))
	long := e.mustSpawn(t, wishID, "Long", ticks(), nil)
	id := long.GetId()
	waitFor(t, "the first ticks", func() bool { return e.tickCount(t, id) >= 2 })

	if _, err := e.wishes.Pause(t.Context(), connect.NewRequest(&planv1.WishServicePauseRequest{WishId: wishID})); err != nil {
		t.Fatal(err)
	}
	held := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_RESUMING))
	if held.GetWaitReason() != whyWishPaused || held.GetResumes() != 0 {
		t.Fatalf("held = %v", held)
	}
	// The scheduler leaves it while its wish is paused.
	time.Sleep(150 * time.Millisecond)
	if got := e.get(t, id); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RESUMING {
		t.Fatalf("started while its wish is paused: %v", got)
	}

	if _, err := e.wishes.Activate(t.Context(), connect.NewRequest(&planv1.WishServiceActivateRequest{WishId: wishID})); err != nil {
		t.Fatal(err)
	}
	e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	got := e.get(t, id)
	if got.GetResumes() != 0 {
		t.Errorf("a pause spent a resume: %v", got)
	}
	all := func() string { return strings.Join(eventTexts(e.storedEvents(t, id)), "\n") }
	// The line that tells the worker why comes once it has started again.
	waitFor(t, "the worker started again, and told why", func() bool {
		return strings.Contains(all(), "resumed "+byWish+": started fake") && strings.Contains(all(), wishLine)
	})
	if !strings.Contains(all(), "resuming: "+whyWishPaused) {
		t.Errorf("events:\n%s", all())
	}
	e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
}
