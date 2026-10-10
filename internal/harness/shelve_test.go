package harness

import (
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// TestWishPauseStopsItsWorkers: pausing a wish stops its worker, and the task waits to resume with its wish; making
// the wish active again starts the worker on its session, told why, without spending a resume.
func TestWishPauseStopsItsWorkers(t *testing.T) {
	testx.Portable(t)
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
	// Told why, the fake ends at once: running lasts too short a moment to be seen (1.5 ms on macOS); its events say
	// it ran.
	got := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if got.GetResumes() != 0 {
		t.Errorf("a pause spent a resume: %v", got)
	}
	all := strings.Join(eventTexts(e.storedEvents(t, id)), "\n")
	for _, want := range []string{"resuming: " + whyWishPaused, "resumed " + byWish + ": started fake", wishLine} {
		if !strings.Contains(all, want) {
			t.Errorf("no event %q:\n%s", want, all)
		}
	}
}
