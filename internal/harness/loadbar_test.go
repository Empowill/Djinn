package harness

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/testx"
)

// The load bar's rule (W577): the workers a notch lets run are the workers that run. Every agent takes a slot, an
// investigator's included; a watcher takes none; only a start or a resume by hand runs beyond, and the bar counts it.
// These tests drive the scheduler with fake workers and a capacity the test sets, no process of a real agent.

// counts is what the scheduler weighs against the bar, read at once: the workers in a slot, and those started or
// resumed by hand among them.
func counts(h *Harness) (running, forced int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.runs {
		if r.slotted() {
			running++
			if r.forced {
				forced++
			}
		}
	}
	return running, forced
}

// peak samples, every millisecond until stop, the most workers that ran at once beyond those started by hand.
func peak(h *Harness) (stop func() int) {
	var most atomic.Int64
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		for {
			running, forced := counts(h)
			if n := int64(running - forced); n > most.Load() {
				most.Store(n)
			}
			select {
			case <-done:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	return func() int {
		close(done)
		<-finished
		return int(most.Load())
	}
}

// settle waits until ok holds, then checks it still holds over a few scheduler passes.
func settle(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: never", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
	for range 10 {
		time.Sleep(10 * time.Millisecond)
		if !ok() {
			t.Fatalf("%s: held, then no longer", what)
		}
	}
}

// bar is a load bar a test moves: the slots the scheduler lets run now (capacity, the monitor's), and the ones each
// notch itself lets run (WithBarSlots), apart so that the memory's share can move alone.
type bar struct {
	mu       sync.Mutex
	slots    int
	notches  map[djinnv1.LoadNotch]int
	pressure string
}

func newBar(slots int) *bar {
	return &bar{slots: slots, notches: map[djinnv1.LoadNotch]int{djinnv1.LoadNotch_LOAD_NOTCH_HIGH: slots}}
}

func (b *bar) capacity() (int, string, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.slots, fmt.Sprintf("%d: set by the test", b.slots), b.pressure
}

func (b *bar) notch(n djinnv1.LoadNotch) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.notches[n]
}

// move sets the notch's slots, the memory allowing them all, then tells the harness, as the load service does
// (machine.loadService.applyNotch: the monitor's policy first, then the harness's).
func (b *bar) move(h *Harness, n djinnv1.LoadNotch, slots int) {
	b.mu.Lock()
	b.slots, b.notches[n] = slots, slots
	b.mu.Unlock()
	h.SetPolicy(machine.Policy{Notch: n})
}

// spawnWork spawns n work tasks that wait for a message, each in a write scope of its own, oldest first.
func (e *env) spawnWork(t *testing.T, wishID string, n int) []*planv1.Task {
	t.Helper()
	var out []*planv1.Task
	for i := range n {
		out = append(out, e.mustSpawn(t, wishID, fmt.Sprintf("Work %d", i+1), "wait",
			&planv1.TaskServiceSpawnRequest{WriteScopes: []string{fmt.Sprintf("s%d", i+1)}}))
		time.Sleep(2 * time.Millisecond) // Their start times order them.
	}
	return out
}

// statuses counts the tasks by status, as the store has them now.
func (e *env) statuses(t *testing.T, tasks []*planv1.Task) map[planv1.TaskStatus]int {
	t.Helper()
	out := map[planv1.TaskStatus]int{}
	for _, tk := range tasks {
		out[e.get(t, tk.GetId()).GetStatus()]++
	}
	return out
}

func newBarEnv(t *testing.T, slots int, opts ...Option) (*env, *bar, string) {
	t.Helper()
	b := newBar(slots)
	e := up(t, t.TempDir(), append([]Option{WithEnlightenWorkers(), WithCapacity(b.capacity), WithBarSlots(b.notch),
		WithTick(10 * time.Millisecond)}, opts...)...)
	e.h.SetPolicy(machine.Policy{Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH})
	wishID, _ := e.wish(t, fakeProject(t, ""))
	return e, b, wishID
}

// TestLoadBarLevels: at each level, from 1 to 5 slots, 6 tasks planned at once run exactly as many workers as the
// level lets run, the others waiting for the machine and saying so; as each ends, the next one takes its slot, never
// one more, until all are done.
func TestLoadBarLevels(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	for slots := 1; slots <= 5; slots++ {
		t.Run(fmt.Sprintf("%d slots", slots), func(t *testing.T) {
			t.Parallel()
			e, _, wishID := newBarEnv(t, slots)
			most := peak(e.h)
			tasks := e.spawnWork(t, wishID, 6)
			settle(t, "the level's workers run", func() bool { return e.h.Running() == slots })
			for _, tk := range tasks {
				got := e.get(t, tk.GetId())
				if got.GetStatus() == planv1.TaskStatus_TASK_STATUS_PENDING &&
					(!got.GetMachineWait() || !strings.Contains(got.GetWaitReason(), "the most this machine holds")) {
					t.Errorf("%s waits: %q (machine %v)", got.GetCode(), got.GetWaitReason(), got.GetMachineWait())
				}
			}
			if n := e.statuses(t, tasks)[planv1.TaskStatus_TASK_STATUS_PENDING]; n != 6-slots {
				t.Errorf("%d tasks wait, want %d", n, 6-slots)
			}
			for done := 0; done < len(tasks); {
				for _, tk := range tasks {
					if e.get(t, tk.GetId()).GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING {
						e.release(t, tk.GetId())
						e.ended(t, tk.GetId())
						done++
					}
				}
			}
			if got := most(); got != slots {
				t.Errorf("at most %d workers ran at once, want %d", got, slots)
			}
		})
	}
}

// TestLoadBarMovesDownAndUp: moving the bar down pauses the newest workers above what it lets run, at once, saying
// why; moving it up resumes them first, before any task waiting; each move changes the count of workers that run, and
// the tasks planned meanwhile keep waiting.
func TestLoadBarMovesDownAndUp(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e, b, wishID := newBarEnv(t, 4)
	tasks := e.spawnWork(t, wishID, 5)
	settle(t, "4 run", func() bool { return e.h.Running() == 4 })
	waiting, waitingRan := tasks[4], false

	for _, step := range []struct {
		notch                  djinnv1.LoadNotch
		slots, running, paused int
		waitingStarts          bool // the task planned meanwhile has a slot from this step on
		pausedWhy, resumedWhy  string
	}{
		{notch: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, slots: 2, running: 2, paused: 2,
			pausedWhy: "paused: operating load lowered to Medium"},
		{notch: djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, slots: 1, running: 1, paused: 3,
			pausedWhy: "paused: operating load lowered to Minimal"},
		{notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH, slots: 4, running: 4, paused: 0,
			resumedWhy: "resumed: a slot is free again"},
		{notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX, slots: 5, running: 5, paused: 0, waitingStarts: true},
		{notch: djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, slots: 1, running: 1, paused: 4,
			pausedWhy: "paused: operating load lowered to Minimal"},
		{notch: djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, slots: 5, running: 5, paused: 0,
			resumedWhy: "resumed: a slot is free again"},
	} {
		t.Run(machine.NotchName(step.notch), func(t *testing.T) {
			b.move(e.h, step.notch, step.slots)
			settle(t, fmt.Sprintf("%d run, %d paused", step.running, step.paused), func() bool {
				return e.h.Running() == step.running && e.h.Paused() == step.paused
			})
			most := peak(e.h)
			time.Sleep(50 * time.Millisecond) // A few passes.
			if got := most(); got > step.slots {
				t.Errorf("%d workers ran at once, the bar lets %d", got, step.slots)
			}
			if started := e.get(t, waiting.GetId()).GetStartTime() != nil; started != (step.waitingStarts || waitingRan) {
				t.Errorf("at %d slots, the task planned meanwhile started: %v", step.slots, started)
			}
			waitingRan = waitingRan || step.waitingStarts
			for _, why := range []string{step.pausedWhy, step.resumedWhy} {
				if why == "" {
					continue
				}
				found := false
				for _, tk := range tasks {
					if slices.ContainsFunc(eventTexts(e.storedEvents(t, tk.GetId())), func(s string) bool { return s == why }) {
						found = true
					}
				}
				if !found {
					t.Errorf("no event %q", why)
				}
			}
		})
	}
	// The newest paused first: at Minimal, the oldest runs.
	b.move(e.h, djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, 1)
	settle(t, "1 runs", func() bool { return e.h.Running() == 1 })
	if got := e.get(t, tasks[0].GetId()).GetStatus(); got != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("the oldest is %v at Minimal, want running", got)
	}
}

// TestLoadBarMemoryDipPausesNoOne: the memory available moving the scheduler's slots between 3 and 2 every few
// milliseconds pauses no worker running at a notch that lets 3 run (W39 was paused and resumed every 2 s at Medium,
// its notch unchanged); a task planned meanwhile waits.
func TestLoadBarMemoryDipPausesNoOne(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e, b, wishID := newBarEnv(t, 3)
	tasks := e.spawnWork(t, wishID, 4)
	settle(t, "3 run", func() bool { return e.h.Running() == 3 })
	stop := make(chan struct{})
	var flips sync.WaitGroup
	flips.Go(func() {
		for i := 0; ; i++ {
			b.mu.Lock()
			b.slots = 3 - i%2
			b.mu.Unlock()
			select {
			case <-stop:
				return
			case <-time.After(3 * time.Millisecond):
			}
		}
	})
	time.Sleep(300 * time.Millisecond)
	close(stop)
	flips.Wait()
	if paused := e.h.Paused(); paused != 0 {
		t.Errorf("%d workers paused by a memory dip", paused)
	}
	for _, tk := range tasks {
		for _, text := range eventTexts(e.storedEvents(t, tk.GetId())) {
			if strings.HasPrefix(text, "paused:") {
				t.Errorf("%s: %q", tk.GetCode(), text)
			}
		}
	}
	if got := e.get(t, tasks[3].GetId()); got.GetStartTime() != nil {
		t.Errorf("a 4th worker started at 3 slots: %v", got.GetStatus())
	}
}

// TestLoadBarStartByHand: djinn task start runs a task beyond the slots, as the developer asked; the bar counts it
// among the workers started by hand, it pushes no other worker out, and the tasks after it wait until the workers
// running fall below the slots, its own included.
func TestLoadBarStartByHand(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e, _, wishID := newBarEnv(t, 2)
	tasks := e.spawnWork(t, wishID, 3)
	settle(t, "2 run", func() bool { return e.h.Running() == 2 })
	forced := tasks[2]
	if got := e.get(t, forced.GetId()); !got.GetMachineWait() {
		t.Fatalf("the third does not wait for the machine: %q", got.GetWaitReason())
	}
	most := peak(e.h)
	if _, err := e.tasks.Start(t.Context(), connect.NewRequest(&planv1.TaskServiceStartRequest{TaskId: forced.GetId()})); err != nil {
		t.Fatal(err)
	}
	settle(t, "3 run, 1 by hand, none paused", func() bool {
		running, byHand := counts(e.h)
		return running == 3 && byHand == 1 && e.h.Forced() == 1 && e.h.Paused() == 0
	})
	after := e.spawnWork(t, wishID, 1)[0]
	release := func(tk *planv1.Task) {
		e.release(t, tk.GetId())
		e.ended(t, tk.GetId())
	}
	release(tasks[0])
	settle(t, "the 4th waits beside the one by hand", func() bool {
		return e.h.Running() == 2 && e.get(t, after.GetId()).GetStartTime() == nil
	})
	release(forced)
	settle(t, "the 4th takes the slot", func() bool {
		running, byHand := counts(e.h)
		return running == 2 && byHand == 0 && e.get(t, after.GetId()).GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING
	})
	if got := most(); got > 2 {
		t.Errorf("%d workers ran at once beyond the one by hand, the bar lets 2", got)
	}
}

// TestLoadBarResumeByHand: a worker the developer paused and resumes by hand runs beyond the slots its pause gave to
// the next task; the bar counts it, and no worker is paused to make up for it.
func TestLoadBarResumeByHand(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e, _, wishID := newBarEnv(t, 1)
	tasks := e.spawnWork(t, wishID, 2)
	settle(t, "1 runs", func() bool { return e.h.Running() == 1 })
	if _, err := e.tasks.Pause(t.Context(), connect.NewRequest(&planv1.TaskServicePauseRequest{TaskId: tasks[0].GetId()})); err != nil {
		t.Fatal(err)
	}
	e.until(t, tasks[1].GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	if _, err := e.tasks.Resume(t.Context(), connect.NewRequest(&planv1.TaskServiceResumeRequest{TaskId: tasks[0].GetId()})); err != nil {
		t.Fatal(err)
	}
	settle(t, "2 run, 1 by hand, none paused", func() bool {
		running, byHand := counts(e.h)
		return running == 2 && byHand == 1 && e.h.Paused() == 0
	})
}

// TestLoadBarInvestigator: an investigator takes a slot. On a full machine it takes the slot of the newest work task,
// which it pauses, never one more; a task planned meanwhile keeps waiting; once the investigator ends, the task it
// paused resumes before the one waiting.
func TestLoadBarInvestigator(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e, _, wishID := newBarEnv(t, 1)
	tasks := e.spawnWork(t, wishID, 2)
	settle(t, "1 runs", func() bool { return e.h.Running() == 1 })
	most := peak(e.h)
	q := e.asker(wishID)
	asked := q.ask(t, "wait")
	q.enlighten(t, asked.GetCode(), "Look closer")
	inv := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)[0]
	e.until(t, tasks[0].GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_PAUSED))
	e.until(t, inv.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	settle(t, "the investigator alone runs", func() bool {
		return e.h.Running() == 1 && e.get(t, tasks[1].GetId()).GetStartTime() == nil
	})
	e.release(t, inv.GetId())
	e.ended(t, inv.GetId())
	e.until(t, tasks[0].GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	settle(t, "the paused task resumed first", func() bool {
		return e.h.Running() == 1 && e.get(t, tasks[1].GetId()).GetStartTime() == nil
	})
	if !slices.Contains(eventTexts(e.storedEvents(t, tasks[0].GetId())), "resumed: a slot is free again") {
		t.Errorf("no resumed event: %v", eventTexts(e.storedEvents(t, tasks[0].GetId())))
	}
	if got := most(); got > 1 {
		t.Errorf("%d workers ran at once, the bar lets 1", got)
	}
}

// TestLoadBarInvestigatorWaits: an investigator finds nothing to pause, the worker running holding a gate: it waits
// for a slot, saying so, instead of running beyond the bar; once the gate is let go, it takes that worker's slot.
func TestLoadBarInvestigatorWaits(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e, _, wishID := newBarEnv(t, 1)
	work := e.spawnWork(t, wishID, 1)[0]
	settle(t, "1 runs", func() bool { return e.h.Running() == 1 })
	var holding atomic.Bool
	holding.Store(true)
	e.h.HeldGates(func(id string) []string {
		if id == work.GetId() && holding.Load() {
			return []string{"test"}
		}
		return nil
	}, nil)
	most := peak(e.h)
	q := e.asker(wishID)
	asked := q.ask(t, "wait")
	q.enlighten(t, asked.GetCode(), "Look closer")
	inv := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)[0]
	settle(t, "the investigator waits for a slot", func() bool {
		got := e.get(t, inv.GetId())
		return got.GetStatus() == planv1.TaskStatus_TASK_STATUS_PENDING && got.GetMachineWait() &&
			got.GetWaitReason() == "1 worker runs, the most this machine holds (1: set by the test)"
	})
	holding.Store(false)
	e.until(t, work.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_PAUSED))
	e.until(t, inv.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	if got := most(); got > 1 {
		t.Errorf("%d workers ran at once, the bar lets 1", got)
	}
}

// TestLoadBarGateHolderPausesLater: lowering the bar leaves a worker holding a gate running, and pauses it once it lets
// the gate go: the count comes down to the bar.
func TestLoadBarGateHolderPausesLater(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e, b, wishID := newBarEnv(t, 2)
	tasks := e.spawnWork(t, wishID, 2)
	settle(t, "2 run", func() bool { return e.h.Running() == 2 })
	var holding atomic.Bool
	holding.Store(true)
	e.h.HeldGates(func(string) []string {
		if holding.Load() {
			return []string{"test"}
		}
		return nil
	}, nil)
	b.move(e.h, djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, 1)
	settle(t, "both finish their gate", func() bool { return e.h.Running() == 2 && e.h.Paused() == 0 })
	holding.Store(false)
	settle(t, "the newest paused", func() bool { return e.h.Running() == 1 && e.h.Paused() == 1 })
	if got := e.get(t, tasks[1].GetId()).GetStatus(); got != planv1.TaskStatus_TASK_STATUS_PAUSED {
		t.Errorf("the newest is %v, want paused", got)
	}
}

// TestLowerPauses: lowering the bar pauses workers, except in Auto mode and on Windows, where they finish their run:
// the window says which.
func TestLowerPauses(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	e.h.goos = func() string { return "linux" }
	if !e.h.LowerPauses() {
		t.Error("linux, a manual notch: lowering should pause")
	}
	e.h.SetPolicy(machine.Policy{Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH, Auto: true})
	if e.h.LowerPauses() {
		t.Error("auto: lowering should let workers finish")
	}
	e.h.SetPolicy(machine.Policy{Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH})
	e.h.goos = func() string { return "windows" }
	if e.h.LowerPauses() {
		t.Error("windows: lowering should let workers finish")
	}
}
