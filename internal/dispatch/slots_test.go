package dispatch

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// kinds is one planned task of each kind the scheduler starts, in the order it serves them (Order), and a watcher:
// every agent takes a slot, the watcher none (W577).
func kinds(at time.Time) []*planv1.Task {
	task := func(code string, edit func(*planv1.Task)) *planv1.Task {
		t := &planv1.Task{Id: code, WishId: "w", Code: code, Title: code, Status: planv1.TaskStatus_TASK_STATUS_PENDING,
			Scheduled: true, ProjectId: "repo", CreateTime: timestamppb.New(at)}
		if edit != nil {
			edit(t)
		}
		return t
	}
	return []*planv1.Task{
		task("investigator", func(t *planv1.Task) { t.Role = planv1.TaskRole_TASK_ROLE_INVESTIGATOR }),
		task("correction", func(t *planv1.Task) { t.Correction = &planv1.TaskCorrection{Attempt: 1} }),
		task("review", func(t *planv1.Task) { t.Review = &planv1.TaskReview{Attempt: 1} }),
		task("restarted", func(t *planv1.Task) { t.Status = planv1.TaskStatus_TASK_STATUS_RESUMING }),
		task("unblocks", nil),
		task("merge", func(t *planv1.Task) { t.Title = "Merge: the work of W1" }),
		task("work", nil),
		task("blocked", func(t *planv1.Task) { t.DependsOn = []string{"unblocks"} }),
		task("watcher", func(t *planv1.Task) { t.Provider = planv1.Provider_PROVIDER_WATCH }),
	}
}

// TestSlotsHoldForEveryKind plays a pass on every machine from 1 to 6 slots, 0 to 7 workers running, 0 to 2 gates held
// outside them, with or without pressure, over one planned task of each kind: an investigator, a correction, a review,
// a task Djinn resumes after a restart, work that unblocks another, a merge task, plain work, and a watcher. The pass
// starts exactly the free slots' worth of agents, the first ones in the order, never one more; the watcher starts
// whatever the machine; under pressure no agent starts; each agent that waits says the machine holds it, and only the
// investigator, which may take a paused task's slot, waits for a slot alone.
func TestSlotsHoldForEveryKind(t *testing.T) {
	at := time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC)
	wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
	git := map[string]bool{"repo": true}
	for slots := 1; slots <= 6; slots++ {
		for running := 0; running <= 7; running++ {
			for gates := 0; gates <= 2; gates++ {
				for _, pressure := range []string{"", "simulated"} {
					name := fmt.Sprintf("slots=%d running=%d gates=%d pressure=%q", slots, running, gates, pressure)
					t.Run(name, func(t *testing.T) {
						tasks := kinds(at)
						m := &Machine{Slots: slots, Running: running, Gates: gates, Rule: "test", Pressure: pressure}
						s := New(tasks, []*planv1.Wish{wish}, git, m).At(at)
						order := s.Order()
						var started []string
						for _, d := range s.Pass() {
							id := d.Task.GetId()
							switch {
							case d.Failed != "":
								t.Errorf("%s failed: %s", id, d.Failed)
							case id == "watcher":
								if d.Why != "" {
									t.Errorf("the watcher waits: %s", d.Why)
								}
							case id == "blocked":
								if d.Why != "waits for unblocks (pending)" || d.Machine {
									t.Errorf("blocked: %+v, want a wait for its dependency", d)
								}
							case d.Why == "":
								started = append(started, id)
							case !d.Machine:
								t.Errorf("%s waits for more than the machine: %s", id, d.Why)
							case d.Slot != (id == "investigator" && pressure == ""):
								t.Errorf("%s: Slot = %v (%s)", id, d.Slot, d.Why)
							}
						}
						free := 0
						if pressure == "" {
							free = max(0, slots-running-gates)
						}
						var agents []string
						for _, tk := range order {
							if id := tk.GetId(); id != "watcher" && id != "blocked" {
								agents = append(agents, id)
							}
						}
						if want := agents[:min(free, len(agents))]; !slices.Equal(started, want) {
							t.Errorf("started %v, want %v (the first %d in the order)", started, want, free)
						}
					})
				}
			}
		}
	}
}

// TestSlotsCountWhatThisPassStarted: a slot taken in a pass is taken for the tasks after it in that pass, an
// investigator's included, so that a pass on a machine where a level frees 2 slots starts 2 agents, never 3.
func TestSlotsCountWhatThisPassStarted(t *testing.T) {
	at := time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC)
	wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
	tasks := kinds(at)
	s := New(tasks, []*planv1.Wish{wish}, map[string]bool{"repo": true}, &Machine{Slots: 6, Running: 4, Rule: "high"}).At(at)
	var started []string
	for _, d := range s.Pass() {
		if d.Why == "" && d.Task.GetId() != "watcher" {
			started = append(started, d.Task.GetId())
		}
	}
	if want := []string{"investigator", "correction"}; !slices.Equal(started, want) {
		t.Errorf("started %v, want %v", started, want)
	}
	// The Blocker of a task after them reads the slots this pass took.
	if why, _ := s.Blocker(tasks[6]); why != "6 workers run, the most this machine holds (high)" {
		t.Errorf("Blocker(work) = %q", why)
	}
}

// TestSlotKeptForMerges plays a pass, Djinn serving the gates, on every machine from 1 to 6 slots, 0 to 7 workers
// running, 0 to 2 gates held outside them of which 0 to all are merges' or pushes': on a machine of more than one
// slot, one is kept for the merges and pushes (Q26), unless a merge's gate takes it already; on a machine of one, none
// is kept. Each agent that waits on a full machine says so, and the kept slot when it is.
func TestSlotKeptForMerges(t *testing.T) {
	at := time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)
	wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
	git := map[string]bool{"repo": true}
	for slots := 1; slots <= 6; slots++ {
		for running := 0; running <= 7; running++ {
			for gates := 0; gates <= 2; gates++ {
				for merges := 0; merges <= gates; merges++ {
					name := fmt.Sprintf("slots=%d running=%d gates=%d merges=%d", slots, running, gates, merges)
					t.Run(name, func(t *testing.T) {
						tasks := kinds(at)
						m := &Machine{Slots: slots, Running: running, Gates: gates, Merges: merges, Keep: true, Rule: "test"}
						s := New(tasks, []*planv1.Wish{wish}, git, m).At(at)
						kept := 0
						if slots > 1 && merges == 0 {
							kept = 1
						}
						var started []string
						for _, d := range s.Pass() {
							id := d.Task.GetId()
							switch {
							case id == "watcher" || id == "blocked":
							case d.Why == "":
								started = append(started, id)
							case strings.Contains(d.Why, "slot is kept for merges and pushes") != (kept == 1) && id != "investigator":
								t.Errorf("%s waits because %q, kept %d", id, d.Why, kept)
							}
						}
						var agents []string
						for _, tk := range s.Order() {
							if id := tk.GetId(); id != "watcher" && id != "blocked" {
								agents = append(agents, id)
							}
						}
						free := max(0, slots-running-gates-kept)
						if want := agents[:min(free, len(agents))]; !slices.Equal(started, want) {
							t.Errorf("started %v, want %v (the first %d in the order)", started, want, free)
						}
					})
				}
			}
		}
	}
}
