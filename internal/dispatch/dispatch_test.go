package dispatch

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// investigationCase is a wish whose integration branch's push checks are red, a work task running, and planned tasks
// as made.
func investigationCase(now time.Time, made ...func() *planv1.Task) ([]*planv1.Task, *planv1.Wish) {
	wish := &planv1.Wish{
		Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE,
		IntegrationBranches: []*planv1.IntegrationBranch{{ProjectId: "p", Branch: "feat/qol"}},
		Pushes:              []*planv1.WishPush{{ProjectId: "p", Held: "at 0f2e0240, test: exit status 1", FailedSha: "0f2e02401234"}},
	}
	running := &planv1.Task{Id: "running", WishId: "w", ProjectId: "p", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		Scheduled: true, CreateTime: timestamppb.New(now.Add(-3 * time.Hour))}
	tasks := []*planv1.Task{running}
	for _, m := range made {
		tasks = append(tasks, m())
	}
	return tasks, wish
}

func planned(id string, at time.Time, kind planv1.TaskKind) *planv1.Task {
	return &planv1.Task{Id: id, WishId: "w", ProjectId: "p", Code: id, Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		Scheduled: true, CreateTime: timestamppb.New(at), Kind: kind}
}

// mockupHolds are the holds of the other workers' state that hold a planned work task and never a mockup (W268): the
// wish's red push checks, a merge in conflict that a correction settles, red checks that one repairs.
func mockupHolds(now time.Time) map[string]func(*planv1.Wish, []*planv1.Task) []*planv1.Task {
	repair := func(state planv1.IntegrationState) func(*planv1.Wish, []*planv1.Task) []*planv1.Task {
		return func(w *planv1.Wish, tasks []*planv1.Task) []*planv1.Task {
			w.Pushes = nil
			return append(tasks, &planv1.Task{Id: "fix", WishId: "w", ProjectId: "p", Code: "W9", Scheduled: true,
				Status: planv1.TaskStatus_TASK_STATUS_RUNNING, CreateTime: timestamppb.New(now.Add(-time.Hour)),
				Correction: &planv1.TaskCorrection{Attempt: 1, Failure: &planv1.IntegrationFailure{State: state}}})
		}
	}
	return map[string]func(*planv1.Wish, []*planv1.Task) []*planv1.Task{
		"red branch":     func(_ *planv1.Wish, tasks []*planv1.Task) []*planv1.Task { return tasks },
		"conflict hold":  repair(planv1.IntegrationState_INTEGRATION_STATE_CONFLICT),
		"repair running": repair(planv1.IntegrationState_INTEGRATION_STATE_RED),
	}
}

// gib is a fraction of a GiB in bytes.
func gib(f float64) uint64 { return uint64(f * machine.GiB) }

// spread is a worker's memory series: its percentiles from p50 to p95, evenly between p50 and p95.
func spread(p50, p95 float64) *planv1.Resources {
	step := (p95 - p50) / 4
	return &planv1.Resources{
		P50MemoryBytes: gib(p50), P60MemoryBytes: gib(p50 + step), P70MemoryBytes: gib(p50 + 2*step),
		P80MemoryBytes: gib(p50 + 3*step), P95MemoryBytes: gib(p95), PeakMemoryBytes: gib(p95 + 0.9),
	}
}

// incident is the developer's machine on 12/10/2026 (decision block "Higher notches exploit the machine..."): 16 cores,
// 32 GiB, 12.9 GiB available, 5 claude workers running, which hold 3.63 GiB (0.5 + 0.75 + 2.2 + 0.18 GiB, the fifth
// not read yet), and 10 finished claude workers whose p95 is 2.1 GiB, their p50 0.6 GiB. Today's rule engaged them at
// 11.3 GiB: each at the p95 of its run plus 512 MiB, the fifth at the providers' 2.1 GiB, and held the next one with
// "load overclock commits up to 12.9 GiB of memory, 11.3 GiB already engaged, needs 2.6 GiB".
func incident(at time.Time, planned int) []*planv1.Task {
	var tasks []*planv1.Task
	for i := range 10 {
		res := spread(0.6, 2.1)
		tasks = append(tasks, &planv1.Task{
			Id: fmt.Sprintf("done%d", i), WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_DONE,
			Provider: planv1.Provider_PROVIDER_CLAUDE, EndTime: timestamppb.New(at.Add(-time.Duration(i+1) * time.Hour)),
			Resources: res,
		})
	}
	for i, w := range []struct{ uses, p50, p95 float64 }{
		{0.5, 0.45, 1.6}, {0.75, 0.7, 2.0}, {2.2, 1.2, 2.4}, {0.18, 0.17, 0.7},
	} {
		res := spread(w.p50, w.p95)
		res.MemoryBytes = gib(w.uses)
		tasks = append(tasks, &planv1.Task{
			Id: fmt.Sprintf("run%d", i), Code: fmt.Sprintf("W%d", 460+i), WishId: "w",
			Status: planv1.TaskStatus_TASK_STATUS_RUNNING, Provider: planv1.Provider_PROVIDER_CLAUDE,
			StartTime: timestamppb.New(at.Add(-20 * time.Minute)), Resources: res,
		})
	}
	tasks = append(tasks, &planv1.Task{
		Id: "run4", Code: "W464", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		Provider: planv1.Provider_PROVIDER_CLAUDE, StartTime: timestamppb.New(at.Add(-time.Minute)),
	})
	for i := range planned {
		tasks = append(tasks, &planv1.Task{
			Id: fmt.Sprintf("new%d", i), Code: fmt.Sprintf("W%d", 470+i), WishId: "w",
			Status: planv1.TaskStatus_TASK_STATUS_PENDING, Provider: planv1.Provider_PROVIDER_CLAUDE, Scheduled: true,
			CreateTime: timestamppb.New(at.Add(time.Duration(i) * time.Minute)),
		})
	}
	return tasks
}

// kinds is one planned task of each kind the scheduler starts, in the order it serves them (Order), and a watcher:
// every agent takes a slot, the watcher none (W577). The correction repairs another project's branch: one of repo
// would hold its new work (W233).
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
		task("correction", func(t *planv1.Task) { t.Correction, t.ProjectId = &planv1.TaskCorrection{Attempt: 1}, "fix" }),
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
// investigator, the correction and the review, which may take a paused task's slot (W233), wait for a slot alone.

func TestOverlap(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		a, b []string
		want bool
	}{
		{nil, []string{"x"}, true},
		{[]string{"src"}, []string{"src/app"}, true},
		{[]string{"SRC/App"}, []string{"src"}, true},
		{[]string{"src"}, []string{"srcs"}, false},
		{[]string{"docs", "src/a"}, []string{"src/b"}, false},
	} {
		if got := Overlap(tt.a, tt.b); got != tt.want {
			t.Errorf("Overlap(%v, %v) = %v", tt.a, tt.b, got)
		}
	}
}

func TestPass(t *testing.T) {
	t.Parallel()

	// TestResumingFirst: the tasks Djinn resumes start before new ones, whatever their age; one waiting for its
	// provider's usage limit waits until it resets, and holds every task of that provider meanwhile.
	t.Run("resuming_first", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 10, 8, 4, 30, 0, 0, time.UTC)
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
		task := func(id string, s planv1.TaskStatus, p planv1.Provider, after time.Duration) *planv1.Task {
			t := &planv1.Task{Id: id, WishId: "w", Code: id, Status: s, Provider: p, Scheduled: true,
				CreateTime: timestamppb.New(now.Add(-time.Hour))}
			if s == planv1.TaskStatus_TASK_STATUS_RESUMING {
				t.CreateTime = timestamppb.New(now) // Newer than the planned ones, still first.
			}
			if after != 0 {
				t.ResumeAfter, t.WaitReason = timestamppb.New(now.Add(after)), "the account's session limit, resets at 05:20"
			}
			return t
		}
		tasks := []*planv1.Task{
			task("new-claude", planv1.TaskStatus_TASK_STATUS_PENDING, planv1.Provider_PROVIDER_CLAUDE, 0),
			task("new-codex", planv1.TaskStatus_TASK_STATUS_PENDING, planv1.Provider_PROVIDER_CODEX, 0),
			task("restarted", planv1.TaskStatus_TASK_STATUS_RESUMING, planv1.Provider_PROVIDER_CODEX, 0),
			task("limited", planv1.TaskStatus_TASK_STATUS_RESUMING, planv1.Provider_PROVIDER_UNSPECIFIED, 50*time.Minute),
		}
		m := &Machine{Slots: 2, Rule: "test"}
		got := map[string]string{}
		var order []string
		for _, d := range New(tasks, []*planv1.Wish{wish}, nil, m).At(now).Pass() {
			got[d.Task.GetId()] = d.Why
			order = append(order, d.Task.GetId())
		}
		if first := slices.Sorted(slices.Values(order[:2])); !slices.Equal(first, []string{"limited", "restarted"}) {
			t.Errorf("order = %v, want the resumed first", order)
		}
		want := map[string]string{
			"restarted":  "",
			"limited":    "the account's session limit, resets at 05:20",
			"new-claude": "claude waits for the account's session limit, resets at 05:20",
			"new-codex":  "",
		}
		if !maps.Equal(got, want) {
			t.Errorf("decisions = %v, want %v", got, want)
		}
		// Once the limit has reset, it starts, and claude's tasks with it.
		got = map[string]string{}
		for _, d := range New(tasks, []*planv1.Wish{wish}, nil, nil).At(now.Add(time.Hour)).Pass() {
			got[d.Task.GetId()] = d.Why
		}
		if got["limited"] != "" || got["new-claude"] != "" {
			t.Errorf("after the reset: %v", got)
		}
	})

	// TestWatcherTakesNoSlot: a watcher starts on a full machine under pressure, outside Git next to a writer of the
	// whole folder; it takes no slot from the task after it, and holds no writer back. Its wish and its dependencies
	// still hold it.
	t.Run("watcher_takes_no_slot", func(t *testing.T) {
		t.Parallel()
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
		task := func(id string, s planv1.TaskStatus, p planv1.Provider, deps ...string) *planv1.Task {
			return &planv1.Task{Id: id, WishId: "w", Code: id, Status: s, Provider: p, Scheduled: true, ProjectId: "folder",
				DependsOn: deps, CreateTime: timestamppb.Now()}
		}
		running := task("writer", planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.Provider_PROVIDER_CLAUDE)
		watchers := []*planv1.Task{
			task("watch", planv1.TaskStatus_TASK_STATUS_PENDING, planv1.Provider_PROVIDER_WATCH),
			task("watch-after", planv1.TaskStatus_TASK_STATUS_PENDING, planv1.Provider_PROVIDER_WATCH, "writer"),
		}
		full := &Machine{Slots: 1, Rule: "test", Running: 1, Pressure: "simulated"}
		got := map[string]string{}
		for _, d := range New(append([]*planv1.Task{running}, watchers...), []*planv1.Wish{wish}, nil, full).Pass() {
			got[d.Task.GetId()] = d.Why
		}
		if want := map[string]string{"watch": "", "watch-after": "waits for writer (running)"}; !maps.Equal(got, want) {
			t.Errorf("under pressure: %v, want %v", got, want)
		}

		// A running watcher holds no agent: neither its slot nor its folder.
		watching := task("watching", planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.Provider_PROVIDER_WATCH)
		agent := task("agent", planv1.TaskStatus_TASK_STATUS_PENDING, planv1.Provider_PROVIDER_CLAUDE)
		free := &Machine{Slots: 1, Rule: "test"}
		s := New([]*planv1.Task{watching, task("watch-new", planv1.TaskStatus_TASK_STATUS_PENDING, planv1.Provider_PROVIDER_WATCH), agent},
			[]*planv1.Wish{wish}, nil, free)
		for _, d := range s.Pass() {
			if d.Why != "" || d.Failed != "" {
				t.Errorf("%s waits: %q %q", d.Task.GetId(), d.Why, d.Failed)
			}
		}
		paused := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_PAUSED, Rank: 1}
		if why, _ := New(watchers[:1], []*planv1.Wish{paused}, nil, nil).Blocker(watchers[0]); why != "its wish is paused" {
			t.Errorf("a watcher of a paused wish: %q", why)
		}
	})

	// TestQuestionWorkerTakesASlot: a question worker starts beside a writer of its folder, takes a slot like any worker
	// (W577), and waits while the machine is under pressure or its memory would not hold it; started, its slot and its
	// memory count for the next.
	t.Run("question_worker_takes_a_slot", func(t *testing.T) {
		t.Parallel()
		at := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
		task := func(id string, s planv1.TaskStatus, role planv1.TaskRole, minute int) *planv1.Task {
			return &planv1.Task{Id: id, WishId: "w", Code: id, Status: s, Role: role, Scheduled: true, ProjectId: "folder",
				CreateTime: timestamppb.New(at.Add(time.Duration(minute) * time.Minute))}
		}
		writer := task("writer", planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskRole_TASK_ROLE_UNSPECIFIED, 0)
		investigator := task("investigator", planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskRole_TASK_ROLE_INVESTIGATOR, 1)
		agent := task("agent", planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskRole_TASK_ROLE_UNSPECIFIED, 2)
		pass := func(tasks []*planv1.Task, m *Machine) map[string]string {
			got := map[string]string{}
			for _, d := range New(tasks, []*planv1.Wish{wish}, nil, m).At(at).Pass() {
				got[d.Task.GetId()] = d.Why
			}
			return got
		}
		two := &Machine{Slots: 2, Rule: "test", Running: 1}
		if got, want := pass([]*planv1.Task{writer, investigator, agent}, two), map[string]string{
			"investigator": "", "agent": "writer writes the whole folder, which overlaps the whole folder",
		}; !maps.Equal(got, want) {
			t.Errorf("one slot free: %v, want %v", got, want)
		}
		other := task("other", planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskRole_TASK_ROLE_UNSPECIFIED, 3)
		other.ProjectId = "elsewhere"
		if got := pass([]*planv1.Task{investigator, other}, two); got["investigator"] != "" ||
			got["other"] != "2 workers run, the most this machine holds (test)" {
			t.Errorf("the investigator's slot: %v, want it taken from the task after it", got)
		}
		full := &Machine{Slots: 1, Rule: "test", Running: 1}
		if got := pass([]*planv1.Task{investigator}, full); got["investigator"] != "1 worker runs, the most this machine holds (test)" {
			t.Errorf("full: %v, want the investigator to wait for a slot", got)
		}
		full.Pressure = "simulated"
		if got := pass([]*planv1.Task{investigator}, full); got["investigator"] != "the machine is under pressure: simulated" {
			t.Errorf("under pressure: %v", got)
		}
		// 2 GiB free: the investigator, a claude worker never measured (1 GiB), starts; the agent after it counts its GiB.
		roomy := &Machine{Slots: 4, Rule: "test", Available: 2 * machine.GiB, Policy: machine.DefaultPolicy()}
		if got, want := pass([]*planv1.Task{investigator, agent}, roomy), map[string]string{
			"investigator": "", "agent": "a claude worker peaks at 1.0 GiB (none measured yet), 2.0 GiB free, 1.0 GiB of it " +
				"for the workers running, 512 MiB kept",
		}; !maps.Equal(got, want) {
			t.Errorf("memory: %v, want %v", got, want)
		}
		roomy.Available = machine.GiB
		if got := pass([]*planv1.Task{investigator}, roomy); got["investigator"] == "" {
			t.Error("the investigator started where the memory does not hold it")
		}
	})

	// TestRestartQueue plays the passes after a restart: 6 tasks Djinn resumes over 2 wishes, a planned one, and 2
	// resumed tasks of a paused wish, on a machine of 2 slots where each pass ends the oldest worker. The first wish's
	// resumed tasks start first, then the second's, each wish in its own order, then the planned one: 2 at a time, the
	// others waiting for a slot, saying so; nothing starts under pressure; the paused wish's tasks wait for it, then
	// start once it is active. No task starts twice.
	t.Run("restart_queue", func(t *testing.T) {
		t.Parallel()
		at := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
		first := &planv1.Wish{Id: "first", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
		second := &planv1.Wish{Id: "second", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 2}
		paused := &planv1.Wish{Id: "paused", State: planv1.WishState_WISH_STATE_PAUSED}
		wishes := []*planv1.Wish{first, second, paused}
		var tasks []*planv1.Task
		task := func(id, wish string, s planv1.TaskStatus, age time.Duration) {
			tasks = append(tasks, &planv1.Task{Id: id, WishId: wish, Code: id, Status: s, Scheduled: true,
				CreateTime: timestamppb.New(at.Add(-age))})
		}
		resuming, pending := planv1.TaskStatus_TASK_STATUS_RESUMING, planv1.TaskStatus_TASK_STATUS_PENDING
		// The second wish's tasks are older, the planned one older still: the rank comes first, then resumed before new.
		task("planned", "first", pending, 3*time.Hour)
		for i, id := range []string{"s1", "s2", "s3"} {
			task(id, "second", resuming, 2*time.Hour-time.Duration(i)*time.Minute)
		}
		for i, id := range []string{"f1", "f2", "f3"} {
			task(id, "first", resuming, time.Hour-time.Duration(i)*time.Minute)
		}
		task("p1", "paused", resuming, 4*time.Hour)
		task("p2", "paused", resuming, 4*time.Hour)

		byID := map[string]*planv1.Task{}
		for _, tk := range tasks {
			byID[tk.GetId()] = tk
		}
		var running, starts []string
		// pass makes one scheduling pass on machine m and starts what it decides; it returns why each other task waits.
		pass := func(m *Machine) map[string]string {
			m.Slots, m.Rule, m.Running = 2, "test", len(running)
			waits := map[string]string{}
			for _, d := range New(tasks, wishes, nil, m).At(at).Pass() {
				switch {
				case d.Failed != "":
					t.Fatalf("%s fails: %s", d.Task.GetId(), d.Failed)
				case d.Why != "":
					waits[d.Task.GetId()] = d.Why
				default:
					d.Task.Status = planv1.TaskStatus_TASK_STATUS_RUNNING
					running, starts = append(running, d.Task.GetId()), append(starts, d.Task.GetId())
				}
			}
			if len(running) > 2 {
				t.Fatalf("%d workers run: %v", len(running), running)
			}
			return waits
		}

		if waits := pass(&Machine{Pressure: "swap in use"}); len(starts) != 0 || waits["f1"] != "the machine is under pressure: swap in use" {
			t.Fatalf("under pressure: started %v, waits %v", starts, waits)
		}
		for range 20 {
			waits := pass(&Machine{})
			for id, why := range waits {
				want := "2 workers run, the most this machine holds (test)"
				if byID[id].GetWishId() == "paused" {
					want = "its wish is paused"
				}
				if why != want {
					t.Errorf("after %v started, %s waits: %q, want %q", starts, id, why, want)
				}
			}
			if len(running) == 0 {
				break
			}
			byID[running[0]].Status, running = planv1.TaskStatus_TASK_STATUS_DONE, running[1:]
		}
		if want := []string{"f1", "f2", "f3", "s1", "s2", "s3", "planned"}; !slices.Equal(starts, want) {
			t.Errorf("started %v, want %v", starts, want)
		}

		// Active again, the paused wish's tasks start, in their order.
		paused.State, paused.Rank, starts = planv1.WishState_WISH_STATE_ACTIVE, 3, nil
		for range 5 {
			pass(&Machine{})
			running = nil
		}
		if want := []string{"p1", "p2"}; !slices.Equal(starts, want) {
			t.Errorf("once its wish is active: started %v, want %v", starts, want)
		}
	})

	// TestMemoryHoldsWorker: a worker starts only when the memory available, less what the running workers may still
	// take, holds the typical peak of its provider (the median of its finished workers, a default when none is
	// measured); the reason names those numbers. A resumed task the memory holds keeps its turn: a lighter one behind it
	// waits. Once the memory frees, it starts.
	t.Run("memory_holds_worker", func(t *testing.T) {
		t.Parallel()
		at := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
		var tasks []*planv1.Task
		// Three finished claude workers peaked at 1, 1.5 and 4 GiB: a claude worker typically peaks at 1.5 GiB.
		for i, peak := range []uint64{machine.GiB, 3 * machine.GiB / 2, 4 * machine.GiB} {
			tasks = append(tasks, &planv1.Task{Id: fmt.Sprint("done", i), WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_DONE,
				Provider: planv1.Provider_PROVIDER_CLAUDE, EndTime: timestamppb.New(at.Add(-time.Duration(i) * time.Hour)),
				Resources: &planv1.Resources{PeakMemoryBytes: peak}})
		}
		// One claude worker runs at 1 GiB: it may take 0.5 GiB more.
		tasks = append(tasks, &planv1.Task{Id: "busy", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
			Resources: &planv1.Resources{MemoryBytes: machine.GiB}})
		resumed := &planv1.Task{Id: "r", Code: "W5", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_RESUMING, Scheduled: true,
			CreateTime: timestamppb.New(at)}
		codex := &planv1.Task{Id: "c", Code: "W6", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_PENDING, Scheduled: true,
			Provider: planv1.Provider_PROVIDER_CODEX, CreateTime: timestamppb.New(at.Add(-time.Hour))}
		tasks = append(tasks, resumed, codex)
		pass := func(available uint64) map[string]string {
			m := &Machine{Slots: 4, Rule: "test", Running: 1, Available: available, Policy: machine.DefaultPolicy()}
			got := map[string]string{}
			for _, d := range New(tasks, []*planv1.Wish{wish}, nil, m).At(at).Pass() {
				got[d.Task.GetId()] = d.Why
			}
			return got
		}

		// 2.2 GiB free, 0.5 of it for the running worker: W5 needs 1.5 GiB and the 512 MiB kept. W6, a codex worker
		// never measured (1 GiB), would fit, but waits for it.
		tight := "a claude worker peaks at 1.5 GiB (the median of the last 3 measured), 2.2 GiB free, " +
			"512 MiB of it for the workers running, 512 MiB kept"
		if got, want := pass(22*machine.GiB/10), map[string]string{"r": tight, "c": "W5 goes first: " + tight}; !maps.Equal(got, want) {
			t.Errorf("tight: %v, want %v", got, want)
		}
		// 3 GiB free: W5 starts; W6 waits, the 1.5 GiB W5 may take counted.
		want := map[string]string{"r": "", "c": "a codex worker peaks at 1.0 GiB (none measured yet), 3.0 GiB free, " +
			"2.0 GiB of it for the workers running, 512 MiB kept"}
		if got := pass(3 * machine.GiB); !maps.Equal(got, want) {
			t.Errorf("freed: %v, want %v", got, want)
		}
		// W5 runs at 1.5 GiB, the busy worker ended: W6 starts.
		resumed.Status, resumed.Resources = planv1.TaskStatus_TASK_STATUS_RUNNING, &planv1.Resources{MemoryBytes: 3 * machine.GiB / 2}
		tasks[3].Status = planv1.TaskStatus_TASK_STATUS_DONE
		if got := pass(3 * machine.GiB); got["c"] != "" {
			t.Errorf("once W5 reached its peak: %v", got)
		}
		// A machine whose memory is unknown holds no worker back.
		resumed.Status = planv1.TaskStatus_TASK_STATUS_RESUMING
		if got := pass(0); got["r"] != "" || got["c"] != "" {
			t.Errorf("memory unknown: %v", got)
		}
	})

	// TestWaitsForTheCommit: a dependency whose work Djinn integrates counts once committed into the wish's integration
	// branch, not when its worker ends done; one whose work Djinn does not integrate counts once done.
	t.Run("waits_for_the_commit", func(t *testing.T) {
		t.Parallel()
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
		for _, c := range []struct {
			state planv1.IntegrationState
			want  string
		}{
			{planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED, ""},
			{planv1.IntegrationState_INTEGRATION_STATE_PENDING, "waits for W5 to be committed"},
			{planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING, "waits for W5 to be committed"},
			{planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, "waits for W5 to be committed (conflict)"},
			{planv1.IntegrationState_INTEGRATION_STATE_RED, "waits for W5 to be committed (red)"},
			{planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED, "waits for W5 to be committed (uncommitted)"},
			{planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, ""},
		} {
			dep := &planv1.Task{Id: "d", WishId: "w", Code: "W5", Status: planv1.TaskStatus_TASK_STATUS_DONE}
			if c.state != planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED {
				dep.Integration = &planv1.TaskIntegration{State: c.state}
			}
			task := &planv1.Task{Id: "t", WishId: "w", Code: "W6", Status: planv1.TaskStatus_TASK_STATUS_PENDING, Scheduled: true,
				DependsOn: []string{"d"}}
			d := New([]*planv1.Task{dep, task}, []*planv1.Wish{wish}, nil, nil).Pass()[0]
			if d.Why != c.want || d.Failed != "" || d.Commit != (c.want != "") {
				t.Errorf("W5 %s: %+v; want %q", c.state, d, c.want)
			}
		}
	})

	// TestWaitsForBranchWork: a DONE dependency with a branch counts as met only when its work is in the wish's
	// integration branch: integration COMMITTED, or (no record) its branch's head is an ancestor of the integration
	// branch's head, or it has no commit beyond the point it started from. Otherwise the dependent waits, with a
	// reason that says so ("waits for W5's work to reach feat/wails-go"). The ancestry check is cached.
	t.Run("waits_for_branch_work", func(t *testing.T) {
		t.Parallel()
		wish := &planv1.Wish{
			Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1,
			IntegrationBranches: []*planv1.IntegrationBranch{{ProjectId: "p1", Branch: "feat/wails-go"}},
		}
		cases := []struct {
			name       string
			branch     string
			state      planv1.IntegrationState
			reason     string
			closed     bool
			bHead      string
			noCommits  bool
			intHead    string
			isAncestor bool
			want       string
		}{
			{
				name:   "no branch and no record starts",
				branch: "", state: planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED,
				want: "",
			},
			{
				name:   "committed record starts",
				branch: "w5-feat", state: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED,
				want: "",
			},
			{
				name:   "cleared record with unmerged commits waits",
				branch: "w5-feat", state: planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED,
				bHead: "b1111111", noCommits: false, intHead: "i2222222", isAncestor: false,
				want: "waits for W5's work to reach feat/wails-go",
			},
			{
				name:   "closed with --drop and unmerged commits starts",
				branch: "w5-feat", state: planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED, closed: true,
				bHead: "b1111111", noCommits: false, intHead: "i2222222", isAncestor: false,
				want: "",
			},
			{
				name:   "integration left out with unmerged commits starts",
				branch: "w5-feat", state: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT,
				reason: "left out (Q12): merge conflict",
				bHead:  "b1111111", noCommits: false, intHead: "i2222222", isAncestor: false,
				want: "",
			},
			{
				name:   "cleared record with merged commits starts",
				branch: "w5-feat", state: planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED,
				bHead: "b1111111", noCommits: false, intHead: "i2222222", isAncestor: true,
				want: "",
			},
			{
				name:   "no record and no commits starts",
				branch: "w5-feat", state: planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED,
				bHead: "b1111111", noCommits: true, intHead: "i2222222", isAncestor: false,
				want: "",
			},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				dep := &planv1.Task{
					Id: "d", WishId: "w", ProjectId: "p1", Code: "W5", Status: planv1.TaskStatus_TASK_STATUS_DONE,
					Branch: c.branch,
				}
				if c.closed {
					dep.Closed = &planv1.Closure{Actor: planv1.Closer_CLOSER_LEAD, Note: "dropped"}
				}
				if c.state != planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED || c.reason != "" {
					dep.Integration = &planv1.TaskIntegration{State: c.state, Reason: c.reason}
				}
				task := &planv1.Task{
					Id: "t", WishId: "w", ProjectId: "p1", Code: "W6", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
					Scheduled: true, DependsOn: []string{"d"},
				}
				calls := 0
				branchHead := func(projectID, branch string) (string, bool) {
					if branch == c.branch {
						return c.bHead, c.noCommits
					}
					if branch == "feat/wails-go" {
						return c.intHead, false
					}
					return "", false
				}
				isAncestor := func(projectID, a, b string) bool {
					calls++
					return c.isAncestor
				}
				sit := New([]*planv1.Task{dep, task}, []*planv1.Wish{wish}, map[string]bool{"p1": true}, nil)
				sit.WithAncestry(branchHead, isAncestor)
				pass := sit.Pass()
				if len(pass) == 0 {
					t.Fatalf("no decision returned")
				}
				d := pass[0]
				if d.Why != c.want {
					t.Errorf("got why %q, want %q", d.Why, c.want)
				}
				// Verify ancestry cache: running Blocker again should not call isAncestor again.
				if c.bHead != "" && !c.noCommits && !c.closed && c.state == planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED {
					prevCalls := calls
					sit.Blocker(task)
					if calls != prevCalls {
						t.Errorf("ancestry not cached: calls went from %d to %d", prevCalls, calls)
					}
				}
			})
		}
	})

	// TestMemoryCommittableLimit: when machine Total is known, dispatch respects the load notch committable memory ceiling.
	t.Run("memory_committable_limit", func(t *testing.T) {
		t.Parallel()
		at := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
		// Measured claude worker peak is 1 GiB.
		done := &planv1.Task{Id: "done", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_DONE,
			Provider: planv1.Provider_PROVIDER_CLAUDE, EndTime: timestamppb.New(at.Add(-time.Hour)),
			Resources: &planv1.Resources{PeakMemoryBytes: machine.GiB}}

		// One claude worker is already running at 1 GiB (forecast is 1 GiB).
		busy := &planv1.Task{Id: "busy", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
			Provider: planv1.Provider_PROVIDER_CLAUDE, Resources: &planv1.Resources{MemoryBytes: machine.GiB}}

		// Two pending claude tasks.
		t1 := &planv1.Task{Id: "t1", Code: "W1", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
			Provider: planv1.Provider_PROVIDER_CLAUDE, Scheduled: true, CreateTime: timestamppb.New(at)}
		t2 := &planv1.Task{Id: "t2", Code: "W2", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
			Provider: planv1.Provider_PROVIDER_CLAUDE, Scheduled: true, CreateTime: timestamppb.New(at.Add(time.Minute))}

		tasks := []*planv1.Task{done, busy, t1, t2}

		// 24 GiB total, 22 GiB available, 1 GiB held by busy: Djinn could use 23 GiB, 21 GiB beyond the 2 GiB reserve.
		// On minimal load (20%), ceiling is 4.2 GiB.
		// busy engaged is 1.5 GiB (1 GiB peak + 512 MiB margin).
		// t1 forecast is 1.5 GiB.
		// Total engaged with t1 would be 1.5 + 1.5 = 3.0 GiB <= 4.0 GiB -> t1 starts.
		// Total engaged with t2 would be 3.0 + 1.5 = 4.5 GiB > 4.2 GiB -> t2 blocked by committable limit.
		minPolicy := machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL)
		m := &Machine{
			Slots:     4,
			Rule:      "test",
			Running:   1,
			Total:     24 * machine.GiB,
			Available: 22 * machine.GiB,
			Policy:    minPolicy,
		}

		got := map[string]string{}
		for _, d := range New(tasks, []*planv1.Wish{wish}, nil, m).At(at).Pass() {
			got[d.Task.GetId()] = d.Why
		}

		if got["t1"] != "" {
			t.Errorf("t1 should start, but got why: %q", got["t1"])
		}
		wantWhy := "load minimal commits up to 4.2 GiB of memory (22.0 GiB available, 1.0 GiB held by the workers running), " +
			"3.0 GiB already engaged, needs 1.5 GiB at p95"
		if got["t2"] != wantWhy {
			t.Errorf("t2 why:\ngot:  %q\nwant: %q", got["t2"], wantWhy)
		}
	})

	// TestInvestigatorPriorityAndMemory verifies that investigators take memory before work tasks,
	// that work tasks held back by an investigator/correction/review report the specific reason,
	// and that investigators still wait under pressure.
	t.Run("investigator_priority_and_memory", func(t *testing.T) {
		t.Parallel()
		at := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}

		workTask := &planv1.Task{
			Id: "w1", WishId: "w", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
			Scheduled: true, CreateTime: timestamppb.New(at.Add(-time.Hour)),
		}
		invTask := &planv1.Task{
			Id: "q1", WishId: "w", Code: "Q1", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
			Scheduled: true, Role: planv1.TaskRole_TASK_ROLE_INVESTIGATOR,
			CreateTime: timestamppb.New(at), // created after W1
		}

		// 1. An investigator takes a slot (W577): on a full machine it waits for one alone (Slot), the harness pausing a
		// work task for it; with one free, it takes it before the older work task.
		mFull := &Machine{Slots: 1, Running: 1, Rule: "test"}
		pass := func(tasks []*planv1.Task, mach *Machine) map[string]string {
			got := map[string]string{}
			for _, d := range New(tasks, []*planv1.Wish{wish}, nil, mach).At(at).Pass() {
				got[d.Task.GetId()] = d.Why
			}
			return got
		}

		for _, d := range New([]*planv1.Task{workTask, invTask}, []*planv1.Wish{wish}, nil, mFull).At(at).Pass() {
			switch {
			case d.Task == invTask && (d.Why == "" || !d.Slot || !d.Machine):
				t.Errorf("investigator on a full machine: %+v, want a wait for a slot alone", d)
			case d.Task == workTask && (d.Why == "" || d.Slot):
				t.Errorf("work task on a full machine: %+v, want a wait, not for a slot it may take", d)
			}
		}
		got := pass([]*planv1.Task{workTask, invTask}, &Machine{Slots: 2, Running: 1, Rule: "test"})
		if got["q1"] != "" || got["w1"] == "" {
			t.Errorf("one free slot: %v, want the investigator to take it", got)
		}

		// 2. Machine under pressure: investigator still waits, saying why
		mPressure := &Machine{Slots: 1, Pressure: "high load"}
		got = pass([]*planv1.Task{invTask}, mPressure)
		if got["q1"] != "the machine is under pressure: high load" {
			t.Errorf("investigator under pressure: got %q, want 'the machine is under pressure: high load'", got["q1"])
		}

		// 3. Memory for one task only: investigator takes it, work task waits
		tightPolicy := machine.Policy{
			WorkerPeak:   1 * machine.GiB,
			WorkerMargin: 512 * machine.MiB,
		}
		mMem := &Machine{
			Slots: 4, Rule: "test", Total: 10 * machine.GiB, Available: 2 * machine.GiB,
			Policy: tightPolicy,
		}
		got = pass([]*planv1.Task{workTask, invTask}, mMem)
		if got["q1"] != "" {
			t.Errorf("investigator should take memory, got why: %q", got["q1"])
		}
		if got["w1"] == "" {
			t.Error("work task should wait when memory taken by investigator")
		}

		// 4. Memory short for investigator: investigator goes first, work task waits behind it saying so
		mNoRoom := &Machine{
			Slots: 4, Rule: "test", Total: 10 * machine.GiB, Available: 1 * machine.GiB,
			Policy: tightPolicy,
		}
		got = pass([]*planv1.Task{workTask, invTask}, mNoRoom)
		if got["q1"] == "" {
			t.Error("investigator should wait when memory is short")
		}
		if !strings.HasPrefix(got["w1"], "an investigator goes first: ") {
			t.Errorf("work task held back by investigator: got %q, want prefix 'an investigator goes first: '", got["w1"])
		}

		// 5. Memory short reasons for other priority roles when memory is insufficient

		// a) Correction worker
		corrTask := &planv1.Task{
			Id: "corr1", WishId: "w", Code: "W_corr", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
			Scheduled: true, Correction: &planv1.TaskCorrection{},
			CreateTime: timestamppb.New(at.Add(-2 * time.Hour)),
		}
		got = pass([]*planv1.Task{corrTask, workTask}, mNoRoom)
		if !strings.HasPrefix(got["w1"], "a correction goes first: ") {
			t.Errorf("work task held back by correction: got %q, want prefix 'a correction goes first: '", got["w1"])
		}

		// b) Review worker
		revTask := &planv1.Task{
			Id: "rev1", WishId: "w", Code: "W_rev", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
			Scheduled: true, Review: &planv1.TaskReview{},
			CreateTime: timestamppb.New(at.Add(-2 * time.Hour)),
		}
		got = pass([]*planv1.Task{revTask, workTask}, mNoRoom)
		if !strings.HasPrefix(got["w1"], "a review goes first: ") {
			t.Errorf("work task held back by review: got %q, want prefix 'a review goes first: '", got["w1"])
		}
	})

	// TestOverclockRoomForOneMore verifies that on an Overclock machine with 4 running Antigravity workers
	// (p95s 0.9 to 2.1 GiB totaling 6.7 GiB p95s and 8.6 GiB engaged with margins) under a 14.8 GiB limit,
	// Djinn leaves room to start a 5th worker (forecast 3.0 GiB).
	t.Run("overclock_room_for_one_more", func(t *testing.T) {
		t.Parallel()
		at := time.Date(2026, 10, 11, 12, 15, 0, 0, time.UTC)
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}

		// 10 finished tasks giving a median typical p95 of 2.5 GiB (forecast = 3.0 GiB).
		var finished []*planv1.Task
		for i := 0; i < 10; i++ {
			finished = append(finished, &planv1.Task{
				Id: fmt.Sprintf("fin-%d", i), WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_DONE,
				Provider: planv1.Provider_PROVIDER_ANTIGRAVITY, EndTime: timestamppb.New(at.Add(-time.Hour)),
				Resources: &planv1.Resources{
					PeakMemoryBytes: 3 * machine.GiB,
					P95MemoryBytes:  2560 * machine.MiB,
				},
			})
		}

		// 4 running Antigravity workers with peaks 1.1, 2.0, 2.5, 2.6 GiB and p95s 0.9, 1.6, 2.0, 2.1 GiB,
		// using 3.9 GiB in all (0.8 + 1.0 + 1.0 + 1.1), running for 5 minutes.
		w1 := &planv1.Task{
			Id: "w1", Code: "W401", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
			Provider:  planv1.Provider_PROVIDER_ANTIGRAVITY,
			StartTime: timestamppb.New(at.Add(-5 * time.Minute)),
			Resources: &planv1.Resources{
				PeakMemoryBytes: 1126 * machine.MiB, // 1.1 GiB peak
				P95MemoryBytes:  921 * machine.MiB,  // 0.9 GiB p95
				MemoryBytes:     819 * machine.MiB,  // 0.8 GiB uses
			},
		}
		w2 := &planv1.Task{
			Id: "w2", Code: "W402", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
			Provider:  planv1.Provider_PROVIDER_ANTIGRAVITY,
			StartTime: timestamppb.New(at.Add(-5 * time.Minute)),
			Resources: &planv1.Resources{
				PeakMemoryBytes: 2048 * machine.MiB, // 2.0 GiB peak
				P95MemoryBytes:  1638 * machine.MiB, // 1.6 GiB p95
				MemoryBytes:     1024 * machine.MiB, // 1.0 GiB uses
			},
		}
		w3 := &planv1.Task{
			Id: "w3", Code: "W403", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
			Provider:  planv1.Provider_PROVIDER_ANTIGRAVITY,
			StartTime: timestamppb.New(at.Add(-5 * time.Minute)),
			Resources: &planv1.Resources{
				PeakMemoryBytes: 2560 * machine.MiB, // 2.5 GiB peak
				P95MemoryBytes:  2048 * machine.MiB, // 2.0 GiB p95
				MemoryBytes:     1024 * machine.MiB, // 1.0 GiB uses
			},
		}
		w4 := &planv1.Task{
			Id: "w4", Code: "W404", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
			Provider:  planv1.Provider_PROVIDER_ANTIGRAVITY,
			StartTime: timestamppb.New(at.Add(-5 * time.Minute)),
			Resources: &planv1.Resources{
				PeakMemoryBytes: 2662 * machine.MiB, // 2.6 GiB peak
				P95MemoryBytes:  2150 * machine.MiB, // 2.1 GiB p95
				MemoryBytes:     1126 * machine.MiB, // 1.1 GiB uses
			},
		}

		// 5th worker pending to start
		w5 := &planv1.Task{
			Id: "w5", Code: "W405", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
			Provider:   planv1.Provider_PROVIDER_ANTIGRAVITY,
			Scheduled:  true,
			CreateTime: timestamppb.New(at),
		}

		tasks := append(finished, w1, w2, w3, w4, w5)

		// Machine with 16 cores, 16 GiB RAM, 14.8 GiB available.
		// Overclock limit is 14.8 GiB.
		policy := machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK)
		avail := uint64(14*machine.GiB + 8*machine.GiB/10)
		total := uint64(16 * machine.GiB)

		m := &Machine{
			Slots:     16,
			Rule:      "16 cores",
			Running:   4,
			Total:     total,
			Available: avail,
			Policy:    policy,
		}

		sit := New(tasks, []*planv1.Wish{wish}, nil, m).At(at)

		// Engaged memory (no p50 recorded: each at its p95; Overclock keeps a 256 MiB margin):
		// Worker 1: 921 + 256 = 1177 MiB
		// Worker 2: 1638 + 256 = 1894 MiB
		// Worker 3: 2048 + 256 = 2304 MiB
		// Worker 4: 2150 + 256 = 2406 MiB
		// Total engaged: 7781 MiB (~7.6 GiB), down from 4 * 3.0 GiB = 12.0 GiB (or old 4 * 3.5 = 14.0 GiB).
		engaged := sit.Engaged()
		wantEngaged := uint64((921 + 1638 + 2048 + 2150 + 4*256) * machine.MiB)
		if engaged != wantEngaged {
			t.Fatalf("sit.Engaged(): got %d, want %d", engaged, wantEngaged)
		}

		decisions := sit.Pass()
		var w5Dec *Decision
		for i := range decisions {
			if decisions[i].Task.GetId() == "w5" {
				w5Dec = &decisions[i]
				break
			}
		}
		if w5Dec == nil {
			t.Fatal("w5 decision not found")
		}
		if w5Dec.Why != "" {
			t.Errorf("w5 should start (room for one more), but blocked with why: %q", w5Dec.Why)
		}
	})

	// TestOverclockIncident replays the incident: at Overclock, the 6th worker was held while the workers running held
	// 3.6 GiB of 32 GiB. Their memory is now counted once (back into what Djinn could use, not only out of what is
	// available), and each is engaged at its p50 plus 256 MiB: the scheduler fills the 8 slots the CPU allows.
	t.Run("overclock_incident", func(t *testing.T) {
		t.Parallel()
		at := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE}
		tasks := incident(at, 5)
		m := &Machine{
			Slots: 8, Rule: "cpu_slots 8, as djinn load get said", Running: 5,
			Total: 32 * machine.GiB, Available: gib(12.9),
			Policy: machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK),
		}
		started := 0
		for _, d := range New(tasks, []*planv1.Wish{wish}, nil, m).At(at).Pass() {
			switch {
			case d.Why == "" && d.Failed == "":
				started++
			case !strings.Contains(d.Why, "the most this machine holds"):
				t.Errorf("%s waits for memory, not for a slot: %q", d.Task.GetCode(), d.Why)
			}
		}
		if running := 5 + started; running < 8 {
			t.Errorf("%d workers run at Overclock, want at least 8 (the CPU allows 8)", running)
		}

		// Even engaged at p95, the 6th worker fits once the running workers' memory is counted once.
		m.Policy.Percentile = 95
		m.Policy.WorkerMargin = 512 * machine.MiB
		if d := New(incident(at, 1), []*planv1.Wish{wish}, nil, m).At(at).Pass(); d[0].Why != "" {
			t.Errorf("at p95, the 6th worker waits: %q", d[0].Why)
		}

		// Minimal stays cautious on the same machine: no 6th worker.
		m.Policy = machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL)
		m.Slots = 8
		d := New(incident(at, 1), []*planv1.Wish{wish}, nil, m).At(at).Pass()
		if !strings.HasPrefix(d[0].Why, "load minimal commits up to") {
			t.Errorf("at Minimal, the 6th worker: %q, want it held by what minimal commits", d[0].Why)
		}
	})

	// TestRunningWorkerCountedOnce: a running worker's resident memory is left out of the memory available; WorkerRoom
	// counts it back into what Djinn could use, and what the worker may still take is what it is engaged at beyond what it
	// holds.
	t.Run("running_worker_counted_once", func(t *testing.T) {
		t.Parallel()
		at := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE}
		// One worker holds 3 GiB, its whole series at 3 GiB: at Max, engaged at 3 GiB + 256 MiB.
		res := spread(3, 3)
		res.MemoryBytes = 3 * machine.GiB
		busy := &planv1.Task{Id: "busy", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
			StartTime: timestamppb.New(at.Add(-time.Hour)), Resources: res}
		next := &planv1.Task{Id: "next", Code: "W2", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
			Scheduled: true, CreateTime: timestamppb.New(at)}
		// 8 GiB total, 4 GiB available: 3 GiB for busy, 1 GiB for the rest. Djinn could use 7 GiB, 5 GiB beyond the
		// reserve; at Max, 3.25 GiB engaged plus 1.25 GiB for the next (1 GiB default + 256 MiB) = 4.5 GiB fits.
		m := &Machine{Slots: 4, Running: 1, Total: 8 * machine.GiB, Available: 4 * machine.GiB,
			Policy: machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MAX)}
		sit := New([]*planv1.Task{busy, next}, []*planv1.Wish{wish}, nil, m).At(at)
		if got := sit.resident(); got != 3*machine.GiB {
			t.Errorf("resident = %d MiB, want 3072", got>>20)
		}
		if got := sit.growing(); got != 0 {
			t.Errorf("growing = %d MiB, want 0: busy holds what it is engaged at", got>>20)
		}
		// Counted twice, the limit was the share of 4 GiB available beyond the reserve: 2 GiB, against 4.5 GiB engaged.
		if why := m.Policy.WorkerRoom(m.Total, m.Available, 0, 0, sit.Engaged(), "claude", machine.GiB, 0); !strings.Contains(why,
			"commits up to 2.0 GiB") {
			t.Errorf("counted twice: %q, want the old 2.0 GiB limit", why)
		}
		if d := sit.Pass(); d[0].Why != "" {
			t.Errorf("next waits: %q", d[0].Why)
		}
	})

	// TestNetworkHolds (W285): while the network is down, no task starts, a watcher neither, each waiting for the machine
	// (djinn task start may start it); once it is back, one task starts a pass, a correction first, and none when the
	// harness resumed a paused worker in that pass already. A watcher takes no slot: it starts whatever the pace.
	t.Run("network_holds", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 10, 17, 15, 30, 0, 0, time.UTC)
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
		task := func(id string, s planv1.TaskStatus, edit func(*planv1.Task)) *planv1.Task {
			t := &planv1.Task{Id: id, WishId: "w", Code: id, Status: s, Scheduled: true, CreateTime: timestamppb.New(now)}
			if edit != nil {
				edit(t)
			}
			return t
		}
		tasks := func() []*planv1.Task {
			return []*planv1.Task{
				task("new", planv1.TaskStatus_TASK_STATUS_PENDING, nil),
				task("cut", planv1.TaskStatus_TASK_STATUS_RESUMING, func(t *planv1.Task) { t.WaitReason = "network down since 15:12" }),
				task("repair", planv1.TaskStatus_TASK_STATUS_PENDING, func(t *planv1.Task) {
					t.Correction = &planv1.TaskCorrection{Attempt: 1}
				}),
				task("watch", planv1.TaskStatus_TASK_STATUS_PENDING, func(t *planv1.Task) { t.Provider = planv1.Provider_PROVIDER_WATCH }),
			}
		}
		pass := func(n *Network) (started []string, why map[string]Decision) {
			why = map[string]Decision{}
			for _, d := range New(tasks(), []*planv1.Wish{wish}, nil, &Machine{Slots: 8, Rule: "test"}).At(now).WithNetwork(n).Pass() {
				if d.Why == "" && d.Failed == "" {
					started = append(started, d.Task.GetCode())
				}
				why[d.Task.GetCode()] = d
			}
			return started, why
		}

		down := "network down since 15:12: Djinn starts no worker until it is back"
		if started, why := pass(&Network{Down: down}); len(started) != 0 {
			t.Errorf("down: %v started", started)
		} else {
			for code, d := range why {
				if d.Why != down || !d.Machine || !d.Room {
					t.Errorf("down: %s %+v; want waiting for the machine, %q", code, d, down)
				}
			}
		}

		started, why := pass(&Network{OnePerPass: true})
		if strings.Join(started, ",") != "repair,watch" {
			t.Errorf("back: %v started; want the correction first, alone, and the watcher", started)
		}
		if d := why["cut"]; !strings.HasPrefix(d.Why, "the network is back: ") {
			t.Errorf("back: cut %+v; want its turn after the correction", d)
		}

		if started, _ := pass(&Network{OnePerPass: true, Spent: true}); strings.Join(started, ",") != "watch" {
			t.Errorf("back, a paused worker resumed in the pass: %v started; want the watcher alone", started)
		}
		if started, _ := pass(nil); len(started) != 4 {
			t.Errorf("up: %v started; want all", started)
		}
	})

	// TestRepairHoldsNewWork (W233, Q44 A): while a correction repairs a wish's integration branch in a project (it is
	// planned, runs, or its work waits to be merged), no new worker of the wish starts in that project, its reason naming
	// the repair. The correction itself, a review, an urgent task, a question worker, a task Djinn resumes and a watcher
	// still start; so do the tasks of another project or another wish. Once the repair's work is in, or the repair is
	// over, the hold lifts.
	t.Run("repair_holds_new_work", func(t *testing.T) {
		t.Parallel()
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE,
			IntegrationBranches: []*planv1.IntegrationBranch{{ProjectId: "p", Branch: "feat/qol"}}}
		other := &planv1.Wish{Id: "v", State: planv1.WishState_WISH_STATE_ACTIVE}
		task := func(id, code string, edit func(*planv1.Task)) *planv1.Task {
			t := &planv1.Task{Id: id, WishId: "w", ProjectId: "p", Code: code, Status: planv1.TaskStatus_TASK_STATUS_PENDING, Scheduled: true}
			if edit != nil {
				edit(t)
			}
			return t
		}
		w118 := task("w118", "W118", func(t *planv1.Task) {
			t.Status = planv1.TaskStatus_TASK_STATUS_DONE
			t.Integration = &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, Branch: "feat/qol",
				CorrectedBy: "w225"}
		})
		w225 := task("w225", "W225", func(t *planv1.Task) {
			t.Status = planv1.TaskStatus_TASK_STATUS_RUNNING
			t.Correction = &planv1.TaskCorrection{Attempt: 1, Failure: &planv1.IntegrationFailure{
				State: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, TaskIds: []string{"w118"}}}
		})
		work := task("work", "W226", nil)
		// Another correction, planned, would hold the work too: it is only checked to pass.
		next := task("correction", "W227", func(t *planv1.Task) { t.Correction = &planv1.TaskCorrection{Attempt: 1} })
		pass := []*planv1.Task{
			task("review", "W228", func(t *planv1.Task) { t.Review = &planv1.TaskReview{Attempt: 1} }),
			task("urgent", "W229", func(t *planv1.Task) { t.Urgent = true }),
			task("investigator", "W230", func(t *planv1.Task) { t.Role = planv1.TaskRole_TASK_ROLE_INVESTIGATOR }),
			task("resumed", "W231", func(t *planv1.Task) { t.Status = planv1.TaskStatus_TASK_STATUS_RESUMING }),
			task("watcher", "W232", func(t *planv1.Task) { t.Provider = planv1.Provider_PROVIDER_WATCH }),
			task("project", "W234", func(t *planv1.Task) { t.ProjectId = "q" }),
			task("wish", "W1", func(t *planv1.Task) { t.WishId = "v" }),
		}
		tasks := append([]*planv1.Task{w118, w225, work}, pass...)
		git := map[string]bool{"p": true, "q": true}
		blocker := func() string {
			why, _ := New(tasks, []*planv1.Wish{wish, other}, git, nil).Blocker(work)
			return why
		}
		const conflict = "waits for W225 to settle the conflict in feat/qol"
		if why := blocker(); why != conflict {
			t.Errorf("work while W225 runs: %q; want %q", why, conflict)
		}
		s := New(append(tasks, next), []*planv1.Wish{wish, other}, git, nil)
		for _, p := range append(pass, next) {
			if why, failed := s.Blocker(p); why != "" || failed != "" {
				t.Errorf("%s while W225 runs: %q %q; want it started", p.GetId(), why, failed)
			}
		}

		for _, c := range []struct {
			status planv1.TaskStatus
			state  planv1.IntegrationState
			want   string
		}{
			{planv1.TaskStatus_TASK_STATUS_PENDING, 0, conflict},
			{planv1.TaskStatus_TASK_STATUS_PAUSED, 0, conflict},
			{planv1.TaskStatus_TASK_STATUS_RESUMING, 0, conflict},
			{planv1.TaskStatus_TASK_STATUS_DONE, planv1.IntegrationState_INTEGRATION_STATE_PENDING, conflict},
			{planv1.TaskStatus_TASK_STATUS_DONE, planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING, conflict},
			{planv1.TaskStatus_TASK_STATUS_DONE, planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, ""},
			{planv1.TaskStatus_TASK_STATUS_DONE, planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, ""},
			{planv1.TaskStatus_TASK_STATUS_DONE, planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED, ""},
			{planv1.TaskStatus_TASK_STATUS_FAILED, 0, ""},
			{planv1.TaskStatus_TASK_STATUS_CANCELLED, 0, ""},
			{planv1.TaskStatus_TASK_STATUS_STOPPED, 0, ""},
		} {
			w225.Status, w225.Integration = c.status, nil
			if c.state != 0 {
				w225.Integration = &planv1.TaskIntegration{State: c.state}
			}
			if why := blocker(); why != c.want {
				t.Errorf("work, W225 %s %s: %q; want %q", c.status, c.state, why, c.want)
			}
		}
		// A review commits the work its correction left not committed: the repair goes on.
		w225.Status = planv1.TaskStatus_TASK_STATUS_DONE
		w225.Integration = &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED, ReviewedBy: "review"}
		if why := blocker(); why != conflict {
			t.Errorf("work while W228 reviews W225's work: %q; want %q", why, conflict)
		}

		// A red merge, or main's merge, says so.
		w225.Status, w225.Integration = planv1.TaskStatus_TASK_STATUS_RUNNING, nil
		w225.Correction.Failure.State = planv1.IntegrationState_INTEGRATION_STATE_RED
		if why := blocker(); why != "waits for W225 to repair the red checks of feat/qol" {
			t.Errorf("work while W225 repairs red checks: %q", why)
		}
		w225.Correction.Failure = &planv1.IntegrationFailure{State: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, MainSha: "0f2e0240"}
		if why := blocker(); why != "waits for W225 to settle the merge of main into feat/qol" {
			t.Errorf("work while W225 settles main's merge: %q", why)
		}
	})

	// TestMergeTakesASlot (W233): on a full machine a correction or a review waits for a slot alone, the harness pausing a
	// work task for it (Decision.Slot); a work task waits for the machine. The paused workers that resume first keep their
	// slots from work, not from a merge.
	t.Run("merge_takes_a_slot", func(t *testing.T) {
		t.Parallel()
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE}
		task := func(id string, edit func(*planv1.Task)) *planv1.Task {
			t := &planv1.Task{Id: id, WishId: "w", ProjectId: id, Code: id, Status: planv1.TaskStatus_TASK_STATUS_PENDING, Scheduled: true}
			edit(t)
			return t
		}
		correction := task("correction", func(t *planv1.Task) { t.Correction = &planv1.TaskCorrection{Attempt: 1} })
		review := task("review", func(t *planv1.Task) { t.Review = &planv1.TaskReview{Attempt: 1} })
		work := task("work", func(*planv1.Task) {})
		git := map[string]bool{"correction": true, "review": true, "work": true}
		for _, m := range []*Machine{{Slots: 2, Running: 2, Rule: "test"}, {Slots: 3, Running: 1, Resuming: 2, Rule: "test"}} {
			s := New([]*planv1.Task{correction, review, work}, []*planv1.Wish{wish}, git, m)
			full := m.Resuming == 0
			for _, d := range s.Pass() {
				switch id := d.Task.GetId(); {
				case id == "work" && (d.Slot || !d.Machine):
					t.Errorf("%+v: work %+v; want it waiting for the machine", m, d)
				case id != "work" && full && (!d.Slot || d.Why == ""):
					t.Errorf("%+v: %s %+v; want it waiting for a slot alone", m, id, d)
				case id != "work" && !full && d.Why != "":
					t.Errorf("%+v: %s %+v; want it started before the paused workers", m, id, d)
				}
			}
		}
		// Under pressure, none starts.
		s := New([]*planv1.Task{correction}, []*planv1.Wish{wish}, git, &Machine{Slots: 2, Pressure: "simulated", Rule: "test"})
		if d := s.Pass()[0]; d.Slot || d.Why == "" {
			t.Errorf("a correction under pressure: %+v", d)
		}
	})

	// TestSlotsHoldForEveryKind plays a pass on every machine from 1 to 6 slots, 0 to 7 workers running, 0 to 2 gates held
	// outside them, with or without pressure, over one planned task of each kind: an investigator, a correction, a review,
	// a task Djinn resumes after a restart, work that unblocks another, a merge task, plain work, and a watcher. The pass
	// starts exactly the free slots' worth of agents, the first ones in the order, never one more; the watcher starts
	// whatever the machine; under pressure no agent starts; each agent that waits says the machine holds it, and only the
	// investigator, the correction and the review, which may take a paused task's slot (W233), wait for a slot alone.
	t.Run("slots_hold_for_every_kind", func(t *testing.T) {
		t.Parallel()
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
								case d.Slot != (slices.Contains([]string{"investigator", "correction", "review"}, id) && pressure == ""):
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
	})

	// TestSlotsCountWhatThisPassStarted: a slot taken in a pass is taken for the tasks after it in that pass, an
	// investigator's included, so that a pass on a machine where a level frees 2 slots starts 2 agents, never 3.
	t.Run("slots_count_what_this_pass_started", func(t *testing.T) {
		t.Parallel()
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
	})

	// TestSlotKeptForMerges plays a pass, Djinn serving the gates, on every machine from 1 to 6 slots, 0 to 7 workers
	// running, 0 to 2 gates held outside them of which 0 to all are merges' or pushes': on a machine of more than one
	// slot, one is kept for the merges and pushes (Q26), unless a merge's gate takes it already; on a machine of one, none
	// is kept. Each agent that waits on a full machine says so, and the kept slot when it is.
	t.Run("slot_kept_for_merges", func(t *testing.T) {
		t.Parallel()
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
	})

	// TestUrgentStartsOnARedBranch (W133): once the push checks' corrections are spent (the push held, no correction
	// working), every planned task of the wish waits for the branch to be green again, except the urgent task the lead
	// marks to repair it: nothing else could ever make it green.
	t.Run("urgent_starts_on_a_red_branch", func(t *testing.T) {
		t.Parallel()
		wish := &planv1.Wish{
			Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE,
			IntegrationBranches: []*planv1.IntegrationBranch{{ProjectId: "p", Branch: "feat/qol"}},
			Pushes: []*planv1.WishPush{{
				ProjectId: "p", Held: "at 0f2e0240, test: exit status 1; left to the lead", FailedSha: "0f2e02401234", Attempts: 2,
				Failure: &planv1.IntegrationFailure{Reason: "test: exit status 1", Base: "0f2e02401234"},
			}},
		}
		work := &planv1.Task{Id: "work", WishId: "w", ProjectId: "p", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_PENDING, Scheduled: true}
		repair := &planv1.Task{Id: "repair", WishId: "w", ProjectId: "p", Code: "W2", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
			Scheduled: true, Urgent: true}
		git := map[string]bool{"p": true}
		sit := New([]*planv1.Task{work, repair}, []*planv1.Wish{wish}, git, &Machine{Slots: 4, Rule: "test"})
		if why, _ := sit.Blocker(work); why != "waits for feat/qol to be green again (push checks red at 0f2e0240: test)" {
			t.Errorf("work: %q", why)
		}
		if why, failed := sit.Blocker(repair); why != "" || failed != "" {
			t.Errorf("urgent repair: %q %q; want it started on the red branch", why, failed)
		}
	})

	// TestUrgentRepairAndFeedback (W213): a repair and a task for the developer's quick feedback may be urgent at once in a
	// wish, one of each class; both pass the queue, the repair first, and both start on a full machine and a red branch.
	t.Run("urgent_repair_and_feedback", func(t *testing.T) {
		t.Parallel()
		wish := &planv1.Wish{
			Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE,
			IntegrationBranches: []*planv1.IntegrationBranch{{ProjectId: "p", Branch: "feat/qol"}},
			Pushes:              []*planv1.WishPush{{ProjectId: "p", Held: "at 0f2e0240, test: exit status 1", FailedSha: "0f2e02401234"}},
		}
		now := time.Date(2026, 10, 17, 11, 0, 0, 0, time.UTC)
		task := func(id string, age time.Duration) *planv1.Task {
			return &planv1.Task{Id: id, WishId: "w", ProjectId: "p", Code: id, Status: planv1.TaskStatus_TASK_STATUS_PENDING,
				Scheduled: true, CreateTime: timestamppb.New(now.Add(-age))}
		}
		work, mockup, repair := task("work", time.Hour), task("mockup", 30*time.Minute), task("repair", 0)
		mockup.Urgent, mockup.Feedback = true, true
		repair.Urgent = true
		tasks := []*planv1.Task{work, mockup, repair}
		decisions := New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, &Machine{Slots: 1, Rule: "test", Running: 1}).At(now).Pass()
		var order []string
		for _, d := range decisions {
			order = append(order, d.Task.GetId())
			if urgent := d.Task.GetId() != "work"; urgent != (d.Why == "") {
				t.Errorf("%s: why %q", d.Task.GetId(), d.Why)
			}
		}
		if order[0] != "repair" || order[1] != "mockup" {
			t.Errorf("order = %v; want the repair, then the feedback task, before the work", order)
		}
		if !Feedback(mockup) || Feedback(repair) {
			t.Error("Feedback does not tell the feedback task from the repair")
		}
		if UrgentOf(tasks, "w", "", true) != mockup || UrgentOf(tasks, "w", "", false) != repair ||
			UrgentOf(tasks, "w", "mockup", true) != nil {
			t.Error("UrgentOf does not find each class's urgent task")
		}
	})
}

func TestBlocker(t *testing.T) {
	t.Parallel()

	// TestDependencyResumes: a dependency Djinn resumes, interrupted or resuming, holds the task until it ends; one
	// resumed as a fork, or closed as continued in one, is its fork. A dependency stopped by a person
	// fails it; one failed holds the task waiting until it is replayed, continued or marked done.
	t.Run("dependency_resumes", func(t *testing.T) {
		t.Parallel()
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE}
		task := func(code string, s planv1.TaskStatus) *planv1.Task {
			return &planv1.Task{Id: code, WishId: "w", Code: code, Status: s, CreateTime: timestamppb.Now()}
		}
		for _, tt := range []struct {
			name        string
			dep         []*planv1.Task // the first is W1, the dependency
			why, failed string
		}{
			{"interrupted", []*planv1.Task{task("W1", planv1.TaskStatus_TASK_STATUS_INTERRUPTED)}, "waits for W1 (interrupted)", ""},
			{"resuming", []*planv1.Task{task("W1", planv1.TaskStatus_TASK_STATUS_RESUMING)}, "waits for W1 (resuming)", ""},
			{"resumed 3 times", []*planv1.Task{{Id: "W1", WishId: "w", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_FAILED,
				Error: "resumed 3 times without finishing"}}, "waits while its dependency W1 is failed", ""},
			{"stopped", []*planv1.Task{task("W1", planv1.TaskStatus_TASK_STATUS_STOPPED)}, "", "its dependency W1 ended stopped"},
			{"forked, running", []*planv1.Task{task("W1", planv1.TaskStatus_TASK_STATUS_INTERRUPTED),
				{Id: "W5", WishId: "w", Code: "W5", ForkOf: "W1", Status: planv1.TaskStatus_TASK_STATUS_RUNNING}},
				"waits for W5 (running)", ""},
			{"forked twice, done", []*planv1.Task{task("W1", planv1.TaskStatus_TASK_STATUS_INTERRUPTED),
				{Id: "W5", WishId: "w", Code: "W5", ForkOf: "W1", Status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED},
				{Id: "W6", WishId: "w", Code: "W6", ForkOf: "W5", Status: planv1.TaskStatus_TASK_STATUS_DONE}}, "", ""},
			{"forked, failed", []*planv1.Task{task("W1", planv1.TaskStatus_TASK_STATUS_INTERRUPTED),
				{Id: "W5", WishId: "w", Code: "W5", ForkOf: "W1", Status: planv1.TaskStatus_TASK_STATUS_FAILED}},
				"waits while its dependency W1, resumed as W5, is failed", ""},
			{"closed as continued, running", []*planv1.Task{{Id: "W1", WishId: "w", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_DONE,
				Closed: &planv1.Closure{ContinuedIn: "W5"}},
				{Id: "W5", WishId: "w", Code: "W5", ForkOf: "W1", Status: planv1.TaskStatus_TASK_STATUS_RUNNING}},
				"waits for W5 (running)", ""},
			{"closed as continued, failed", []*planv1.Task{{Id: "W1", WishId: "w", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_DONE,
				Closed: &planv1.Closure{ContinuedIn: "W5"}},
				{Id: "W5", WishId: "w", Code: "W5", ForkOf: "W1", Status: planv1.TaskStatus_TASK_STATUS_FAILED}},
				"waits while its dependency W1, resumed as W5, is failed", ""},
			{"closed by hand, a fork done meanwhile", []*planv1.Task{{Id: "W1", WishId: "w", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_DONE,
				Closed: &planv1.Closure{Note: "merged"}},
				{Id: "W5", WishId: "w", Code: "W5", ForkOf: "W1", Status: planv1.TaskStatus_TASK_STATUS_RUNNING}}, "", ""},
			{"forked in another wish", []*planv1.Task{task("W1", planv1.TaskStatus_TASK_STATUS_INTERRUPTED),
				{Id: "W5", WishId: "other", Code: "W5", ForkOf: "W1", Status: planv1.TaskStatus_TASK_STATUS_DONE}},
				"waits for W1 (interrupted)", ""},
		} {
			t.Run(tt.name, func(t *testing.T) {
				planned := task("W2", planv1.TaskStatus_TASK_STATUS_PENDING)
				planned.Scheduled, planned.DependsOn = true, []string{"W1"}
				why, failed := New(append(tt.dep, planned), []*planv1.Wish{wish}, nil, nil).Blocker(planned)
				if why != tt.why || failed != tt.failed {
					t.Errorf("Blocker = %q, %q; want %q, %q", why, failed, tt.why, tt.failed)
				}
			})
		}
	})

	// TestInvestigationDependency (W213): a task after a done investigation does not wait for its work to reach the
	// integration branch: it never will.
	t.Run("investigation_dependency", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 10, 17, 11, 0, 0, 0, time.UTC)
		study := planned("study", now.Add(-time.Hour), planv1.TaskKind_TASK_KIND_INVESTIGATION)
		study.Status, study.Branch = planv1.TaskStatus_TASK_STATUS_DONE, "w2-study"
		next := planned("next", now, planv1.TaskKind_TASK_KIND_WORK)
		next.DependsOn = []string{"study"}
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE}
		if why, failed := New([]*planv1.Task{study, next}, []*planv1.Wish{wish}, map[string]bool{"p": true}, nil).Blocker(next); why != "" || failed != "" {
			t.Errorf("next: %q %q; want it free once the investigation is done", why, failed)
		}
	})

	// TestMockupWriteScope (W268): outside Git, a mockup is held by no write scope another worker takes.
	t.Run("mockup_write_scope", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 10, 17, 14, 2, 0, 0, time.UTC)
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE}
		writer := &planv1.Task{Id: "writer", WishId: "w", ProjectId: "p", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_PAUSED,
			Scheduled: true, CreateTime: timestamppb.New(now.Add(-time.Hour))}
		work := planned("work", now.Add(-time.Minute), planv1.TaskKind_TASK_KIND_WORK)
		mock := planned("mock", now, planv1.TaskKind_TASK_KIND_MOCKUP)
		s := New([]*planv1.Task{writer, work, mock}, []*planv1.Wish{wish}, map[string]bool{"p": false}, nil).At(now)
		if why, _ := s.Held(work); !strings.Contains(why, "overlaps") {
			t.Errorf("work: %q; want it held by W1's scope", why)
		}
		if why, failed := s.Held(mock); why != "" || failed != "" {
			t.Errorf("mock: %q %q; want no scope to hold it", why, failed)
		}
	})
}

func TestOrder(t *testing.T) {
	t.Parallel()

	// TestStrict5TierOrder verifies the 5-tier dispatch order and tie-breaking:
	//  1. Investigator workers, then answer workers;
	//  2. Correction workers, then review workers;
	//  3. Resumed work tasks;
	//  4. Critical path: work tasks that unblock others (most waiting/planned dependents first);
	//  5. Other work tasks.
	//
	// Within each tier/subtier: wish rank, oldest first, task ID.
	t.Run("strict_5_tier_order", func(t *testing.T) {
		t.Parallel()
		at := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
		wishA := &planv1.Wish{Id: "wishA", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
		wishB := &planv1.Wish{Id: "wishB", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 2}
		wishes := []*planv1.Wish{wishA, wishB}

		makeTask := func(id, wish string, status planv1.TaskStatus, age time.Duration, opts ...func(*planv1.Task)) *planv1.Task {
			tk := &planv1.Task{
				Id: id, Code: id, WishId: wish, Status: status, Scheduled: true,
				CreateTime: timestamppb.New(at.Add(-age)),
			}
			for _, opt := range opts {
				opt(tk)
			}
			return tk
		}

		// Tier 1: Investigator
		invB := makeTask("inv_b", "wishB", planv1.TaskStatus_TASK_STATUS_PENDING, 10*time.Minute, func(tk *planv1.Task) {
			tk.Role = planv1.TaskRole_TASK_ROLE_INVESTIGATOR
		})
		invA := makeTask("inv_a", "wishA", planv1.TaskStatus_TASK_STATUS_PENDING, 5*time.Minute, func(tk *planv1.Task) {
			tk.Role = planv1.TaskRole_TASK_ROLE_INVESTIGATOR
		})

		// Tier 2: Correction and Review
		corr := makeTask("corr_a", "wishA", planv1.TaskStatus_TASK_STATUS_PENDING, 10*time.Minute, func(tk *planv1.Task) {
			tk.Correction = &planv1.TaskCorrection{}
		})
		rev := makeTask("rev_a", "wishA", planv1.TaskStatus_TASK_STATUS_PENDING, 10*time.Minute, func(tk *planv1.Task) {
			tk.Review = &planv1.TaskReview{}
		})

		// Tier 3: Resumed work tasks
		resB := makeTask("res_b", "wishB", planv1.TaskStatus_TASK_STATUS_RESUMING, 30*time.Minute)
		resA := makeTask("res_a", "wishA", planv1.TaskStatus_TASK_STATUS_RESUMING, 15*time.Minute)

		// Tier 4: Critical path work tasks
		// block2 unblocks mid and leaf (2 dependents)
		block2 := makeTask("block_2deps", "wishA", planv1.TaskStatus_TASK_STATUS_PENDING, 10*time.Minute)
		// block1 unblocks leaf2 (1 dependent)
		block1 := makeTask("block_1dep", "wishA", planv1.TaskStatus_TASK_STATUS_PENDING, 10*time.Minute)
		mid := makeTask("mid", "wishA", planv1.TaskStatus_TASK_STATUS_PENDING, 10*time.Minute, func(tk *planv1.Task) {
			tk.DependsOn = []string{"block_2deps"}
		})
		leaf := makeTask("leaf", "wishA", planv1.TaskStatus_TASK_STATUS_WAITING, 10*time.Minute, func(tk *planv1.Task) {
			tk.DependsOn = []string{"mid"}
			tk.Scheduled = false
		})
		leaf2 := makeTask("leaf2", "wishA", planv1.TaskStatus_TASK_STATUS_PENDING, 10*time.Minute, func(tk *planv1.Task) {
			tk.DependsOn = []string{"block_1dep"}
		})

		// Tier 5: Other work tasks (0 dependents)
		otherAOld := makeTask("other_a_old", "wishA", planv1.TaskStatus_TASK_STATUS_PENDING, 30*time.Minute)
		otherAYoung := makeTask("other_a_young", "wishA", planv1.TaskStatus_TASK_STATUS_PENDING, 5*time.Minute)
		otherB := makeTask("other_b", "wishB", planv1.TaskStatus_TASK_STATUS_PENDING, 25*time.Minute)

		tasks := []*planv1.Task{
			otherB, otherAYoung, otherAOld, leaf2, leaf, mid, block1, block2,
			resA, resB, rev, corr, invA, invB,
		}

		ordered := New(tasks, wishes, nil, nil).At(at).Order()
		var gotIDs []string
		for _, tk := range ordered {
			gotIDs = append(gotIDs, tk.GetId())
		}

		wantIDs := []string{
			"inv_a",         // Tier 1, sub 0, wish rank 1
			"inv_b",         // Tier 1, sub 0, wish rank 2
			"corr_a",        // Tier 2, sub 0
			"rev_a",         // Tier 2, sub 1
			"res_a",         // Tier 3, wish rank 1
			"res_b",         // Tier 3, wish rank 2
			"block_2deps",   // Tier 4, 2 dependents
			"block_1dep",    // Tier 4, 1 dependent (ID tie-break before mid)
			"mid",           // Tier 4, 1 dependent
			"other_a_old",   // Tier 5, wish rank 1, age 30m
			"leaf2",         // Tier 5, wish rank 1, age 10m
			"other_a_young", // Tier 5, wish rank 1, age 5m
			"other_b",       // Tier 5, wish rank 2
		}

		if !slices.Equal(gotIDs, wantIDs) {
			t.Errorf("Order():\ngot:  %v\nwant: %v", gotIDs, wantIDs)
		}
	})

	// TestAzimaRankFirst (W134): within its wish's rank, the waiting work of the azima ranked first starts first, before
	// older work of a lower azima, even one that unblocks others; work of no azima comes after the ranked azimas'; an
	// azima never ranked comes after the ranked ones; a wish ranked first comes before them all.
	t.Run("azima_rank_first", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
		first := &planv1.Wish{Id: "first", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
		second := &planv1.Wish{Id: "second", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 2}
		azima := func(id, wish string, rank int32) *planv1.Task {
			return &planv1.Task{Id: id, WishId: wish, Code: id, Kind: planv1.TaskKind_TASK_KIND_AZIMA, Rank: rank,
				Status: planv1.TaskStatus_TASK_STATUS_PENDING, CreateTime: timestamppb.New(now.Add(-time.Hour))}
		}
		work := func(id, wish, partOf string, age time.Duration) *planv1.Task {
			return &planv1.Task{Id: id, WishId: wish, ProjectId: "p", Code: id, PartOf: partOf, Scheduled: true,
				Status: planv1.TaskStatus_TASK_STATUS_PENDING, CreateTime: timestamppb.New(now.Add(-age))}
		}
		// T1 ranked second, T2 first, T3 never ranked; in the second wish, T9 ranked first.
		t1, t2, t3, t9 := azima("T1", "second", 2), azima("T2", "second", 1), azima("T3", "second", 0), azima("T9", "first", 1)
		low := work("low", "second", "T1", 3*time.Hour)
		blocker := work("blocker", "second", "T1", 2*time.Hour)
		child := work("child", "second", "T1", time.Minute)
		child.DependsOn = []string{"blocker"}
		high := work("high", "second", "T2", time.Minute)
		unranked := work("unranked", "second", "T3", 4*time.Hour)
		loose := work("loose", "second", "", 5*time.Hour)
		top := work("top", "first", "T9", 0)
		wishes := []*planv1.Wish{first, second}
		tasks := []*planv1.Task{t1, t2, t3, t9, loose, unranked, low, blocker, child, high, top}

		var got []string
		for _, tk := range New(tasks, wishes, map[string]bool{"p": true}, nil).At(now).Order() {
			got = append(got, tk.GetId())
		}
		if want := []string{"top", "high", "blocker", "low", "child", "unranked", "loose"}; !slices.Equal(got, want) {
			t.Errorf("Order() = %v, want %v", got, want)
		}

		// Two azimas, one slot: the higher one's task starts, the older one of the lower azima waits.
		decisions := New([]*planv1.Task{t1, t2, low, high}, wishes, map[string]bool{"p": true},
			&Machine{Slots: 1, Rule: "test"}).At(now).Pass()
		for _, d := range decisions {
			switch id := d.Task.GetId(); {
			case id == "high" && d.Why != "":
				t.Errorf("the higher azima's task waits: %q", d.Why)
			case id == "low" && d.Why == "":
				t.Error("the lower azima's task starts before the higher one's")
			}
		}

		// Ranked the other way, the other task starts.
		t1.Rank, t2.Rank = 1, 2
		if d := New([]*planv1.Task{t1, t2, low, high}, wishes, map[string]bool{"p": true},
			&Machine{Slots: 1, Rule: "test"}).At(now).Pass(); d[0].Task.GetId() != "low" || d[0].Why != "" {
			t.Errorf("after the rank changed, first decision = %s %q, want low starting", d[0].Task.GetId(), d[0].Why)
		}
	})

	// TestInvestigationServedFirst (W213): on a full machine and while the integration branch's push checks are red, an
	// investigation goes before the older planned work and waits for a slot alone (Decision.Slot: the harness pauses a
	// work task for it, as for an investigator), while the work waits for the branch to be green again.
	t.Run("investigation_served_first", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 10, 17, 11, 0, 0, 0, time.UTC)
		tasks, wish := investigationCase(now,
			func() *planv1.Task { return planned("work", now.Add(-time.Hour), planv1.TaskKind_TASK_KIND_WORK) },
			func() *planv1.Task { return planned("study", now, planv1.TaskKind_TASK_KIND_INVESTIGATION) })
		m := &Machine{Slots: 1, Rule: "test", Running: 1, Resuming: 1}
		decisions := New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, m).At(now).Pass()
		if got := decisions[0].Task.GetId(); got != "study" {
			t.Fatalf("first = %s; want the investigation before the older work", got)
		}
		if d := decisions[0]; !d.Slot || !strings.Contains(d.Why, "the most this machine holds") {
			t.Errorf("study: slot %v, why %q; want it waiting for a slot alone, a work task's to take", d.Slot, d.Why)
		}
		if d := decisions[1]; !strings.HasPrefix(d.Why, "waits for feat/qol to be green again") {
			t.Errorf("work: why %q; want it held by the red push checks", d.Why)
		}
		// A free slot: it starts at once, on the red branch.
		m = &Machine{Slots: 4, Rule: "test", Running: 1}
		for _, d := range New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, m).At(now).Pass() {
			if d.Task.GetId() == "study" && (d.Why != "" || d.Failed != "") {
				t.Errorf("study waits on a free machine and a red branch: %q %q", d.Why, d.Failed)
			}
		}
	})

	// TestMockupServedFirst (W268, W213, W234): a mockup takes an investigation's place, after the investigators and
	// beside the investigations, within their cap; marked urgent for feedback, it goes before them all, after the urgent
	// repair. A task after a done mockup waits for no merge of its work.
	t.Run("mockup_served_first", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 10, 17, 14, 2, 0, 0, time.UTC)
		mark := func(task *planv1.Task, feedback bool) *planv1.Task {
			task.Urgent, task.Feedback = true, feedback
			return task
		}
		tasks, wish := investigationCase(now,
			func() *planv1.Task { return planned("work", now.Add(-2*time.Hour), planv1.TaskKind_TASK_KIND_WORK) },
			func() *planv1.Task {
				return planned("study", now.Add(-time.Hour), planv1.TaskKind_TASK_KIND_INVESTIGATION)
			},
			func() *planv1.Task { return planned("mock", now, planv1.TaskKind_TASK_KIND_MOCKUP) },
			func() *planv1.Task {
				t := planned("enlighten", now, planv1.TaskKind_TASK_KIND_UNSPECIFIED)
				t.Role = planv1.TaskRole_TASK_ROLE_INVESTIGATOR
				return t
			})
		order := func() string {
			var ids []string
			for _, t := range New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, nil).At(now).Order() {
				ids = append(ids, t.GetId())
			}
			return strings.Join(ids, ",")
		}
		if got := order(); got != "enlighten,study,mock,work" {
			t.Errorf("order = %s; want the investigator, then the investigation and the mockup by age, then the work", got)
		}
		// One investigation runs: on three slots, the mockup waits for the cap they share.
		m := &Machine{Slots: 3, Rule: "test", Running: 1}
		for _, d := range New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, m).At(now).Pass() {
			if d.Task.GetId() == "mock" && !strings.Contains(d.Why, "the most investigations this machine runs at once, mockups counted") {
				t.Errorf("mock beside the investigation on 3 slots: why %q", d.Why)
			}
		}
		mark(tasks[3], true)
		repair := mark(planned("repair", now, planv1.TaskKind_TASK_KIND_WORK), false)
		tasks = append(tasks, repair)
		if got := order(); got != "repair,mock,enlighten,study,work" {
			t.Errorf("order = %s; want the urgent repair, then the mockup marked for feedback, then the others", got)
		}
		done := planned("done", now.Add(-time.Hour), planv1.TaskKind_TASK_KIND_MOCKUP)
		done.Status, done.Branch = planv1.TaskStatus_TASK_STATUS_DONE, "w3-mock"
		next := planned("next", now, planv1.TaskKind_TASK_KIND_WORK)
		next.DependsOn = []string{"done"}
		free := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE}
		if why, failed := New([]*planv1.Task{done, next}, []*planv1.Wish{free}, map[string]bool{"p": true}, nil).Blocker(next); why != "" || failed != "" {
			t.Errorf("next: %q %q; want it free once the mockup is done", why, failed)
		}
	})

	// TestUrgentPassesTheQueue (W133): an urgent task goes before every older waiting task, a correction and one that
	// unblocks others included, and takes a slot on a full machine, under pressure or short of memory, as djinn task
	// start does; the tasks after it keep waiting.
	t.Run("urgent_passes_the_queue", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 10, 16, 20, 0, 0, 0, time.UTC)
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
		task := func(id string, age time.Duration) *planv1.Task {
			return &planv1.Task{Id: id, WishId: "w", ProjectId: "p", Code: id, Status: planv1.TaskStatus_TASK_STATUS_PENDING,
				Scheduled: true, CreateTime: timestamppb.New(now.Add(-age))}
		}
		running := task("running", 3*time.Hour)
		running.Status = planv1.TaskStatus_TASK_STATUS_RUNNING
		old, blocker, correction := task("old", 2*time.Hour), task("blocker", time.Hour), task("correction", 30*time.Minute)
		correction.Correction = &planv1.TaskCorrection{Attempt: 1}
		child := task("child", 10*time.Minute)
		child.DependsOn = []string{"blocker"}
		urgent := task("urgent", 0)
		urgent.Urgent = true
		tasks := []*planv1.Task{running, old, blocker, correction, child, urgent}
		git := map[string]bool{"p": true}

		for name, m := range map[string]*Machine{
			"full":           {Slots: 1, Rule: "test", Running: 1},
			"under pressure": {Slots: 4, Rule: "test", Running: 1, Pressure: "memory at 95%"},
			"short of memory": {Slots: 4, Rule: "test", Running: 1, Available: machine.GiB / 4, Total: 8 * machine.GiB,
				Policy: machine.DefaultPolicy()},
		} {
			t.Run(name, func(t *testing.T) {
				decisions := New(tasks, []*planv1.Wish{wish}, git, m).At(now).Pass()
				if got := decisions[0].Task.GetId(); got != "urgent" {
					t.Errorf("first = %s, want the urgent task before the older ones", got)
				}
				for _, d := range decisions {
					switch id := d.Task.GetId(); {
					case id == "urgent" && (d.Why != "" || d.Failed != ""):
						t.Errorf("urgent waits: %q %q", d.Why, d.Failed)
					case id != "urgent" && d.Why == "":
						t.Errorf("%s starts; want it waiting behind the urgent one", id)
					}
				}
				if why, failed := New(tasks, []*planv1.Wish{wish}, git, m).At(now).Blocker(urgent); why != "" || failed != "" {
					t.Errorf("Blocker(urgent) = %q, %q; want none", why, failed)
				}
			})
		}

		// Done, it is history: the order and the slots are the others' again.
		done := task("urgent", 0)
		done.Urgent, done.Status = true, planv1.TaskStatus_TASK_STATUS_DONE
		if Urgent(done) || UrgentOf([]*planv1.Task{done}, "w", "", false) != nil {
			t.Error("a done task counts as urgent")
		}
		if UrgentOf(tasks, "w", "", false) != urgent || UrgentOf(tasks, "w", "urgent", false) != nil || UrgentOf(tasks, "other", "", false) != nil {
			t.Error("UrgentOf does not find the wish's urgent task alone")
		}
	})
}

func TestHeld(t *testing.T) {
	t.Parallel()

	// TestPushHeld: while a wish's push is held on red push checks, the scheduler starts only
	// push-check correction workers, other correction workers, and read-only question workers.
	// Every other planned task of that wish in that project waits for the branch to be green again.
	t.Run("push_held", func(t *testing.T) {
		t.Parallel()
		wish := &planv1.Wish{
			Id:    "w",
			State: planv1.WishState_WISH_STATE_ACTIVE,
			IntegrationBranches: []*planv1.IntegrationBranch{
				{ProjectId: "p1", Branch: "feat/wails-go"},
			},
			Pushes: []*planv1.WishPush{
				{
					ProjectId: "p1",
					Held:      "at 12345678, test: exit status 1",
					FailedSha: "1234567890abcdef",
					Failure: &planv1.IntegrationFailure{
						Reason: "test: exit status 1",
						Base:   "1234567890abcdef",
					},
					CorrectedBy: "corr-push",
				},
			},
		}

		workTask := &planv1.Task{
			Id: "work", WishId: "w", ProjectId: "p1", Code: "W1",
			Status: planv1.TaskStatus_TASK_STATUS_PENDING, Scheduled: true,
		}
		pushCorr := &planv1.Task{
			Id: "corr-push", WishId: "w", ProjectId: "p1", Code: "W2",
			Status: planv1.TaskStatus_TASK_STATUS_PENDING, Scheduled: true,
			Correction: &planv1.TaskCorrection{Attempt: 1},
		}
		otherCorr := &planv1.Task{
			Id: "corr-other", WishId: "w", ProjectId: "p1", Code: "W3",
			Status: planv1.TaskStatus_TASK_STATUS_PENDING, Scheduled: true,
			Correction: &planv1.TaskCorrection{Attempt: 1},
		}
		questionWorker := &planv1.Task{
			Id: "quest", WishId: "w", ProjectId: "p1", Code: "W4",
			Status: planv1.TaskStatus_TASK_STATUS_PENDING, Scheduled: true,
			Role: planv1.TaskRole_TASK_ROLE_INVESTIGATOR,
		}
		otherProjTask := &planv1.Task{
			Id: "other-proj", WishId: "w", ProjectId: "p2", Code: "W5",
			Status: planv1.TaskStatus_TASK_STATUS_PENDING, Scheduled: true,
		}

		tasks := []*planv1.Task{workTask, pushCorr, otherCorr, questionWorker, otherProjTask}
		git := map[string]bool{"p1": true, "p2": true}
		sit := New(tasks, []*planv1.Wish{wish}, git, nil)

		// Work task must wait with the exact format
		wantWhy := "waits for feat/wails-go to be green again (push checks red at 12345678: test)"
		if why, failed := sit.Blocker(workTask); why != wantWhy || failed != "" {
			t.Errorf("workTask: Blocker = (%q, %q); want (%q, %q)", why, failed, wantWhy, "")
		}

		// Push-check correction worker starts
		if why, failed := sit.Blocker(pushCorr); why != "" || failed != "" {
			t.Errorf("pushCorr: Blocker = (%q, %q); want empty", why, failed)
		}

		// Other correction worker starts
		if why, failed := sit.Blocker(otherCorr); why != "" || failed != "" {
			t.Errorf("otherCorr: Blocker = (%q, %q); want empty", why, failed)
		}

		// Question worker (investigator) starts
		if why, failed := sit.Blocker(questionWorker); why != "" || failed != "" {
			t.Errorf("questionWorker: Blocker = (%q, %q); want empty", why, failed)
		}

		// Other project task starts
		if why, failed := sit.Blocker(otherProjTask); why != "" || failed != "" {
			t.Errorf("otherProjTask: Blocker = (%q, %q); want empty", why, failed)
		}

		// Unchecked push lifts gate; the corrections, still to run, repair the branch and hold its new work (W233)
		wish.Pushes[0].Unchecked = true
		sit = New(tasks, []*planv1.Wish{wish}, git, nil)
		if why, _ := sit.Blocker(workTask); why != "waits for W2 to repair feat/wails-go" {
			t.Errorf("unchecked push, corrections planned: Blocker = %q; want it held by W2", why)
		}
		for _, c := range []*planv1.Task{pushCorr, otherCorr} {
			c.Status = planv1.TaskStatus_TASK_STATUS_DONE
			c.Integration = &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED}
		}
		sit = New(tasks, []*planv1.Wish{wish}, git, nil)
		if why, failed := sit.Blocker(workTask); why != "" || failed != "" {
			t.Errorf("unchecked push: Blocker = (%q, %q); want empty", why, failed)
		}

		// Held cleared lifts gate
		wish.Pushes[0].Unchecked = false
		wish.Pushes[0].Held = ""
		sit = New(tasks, []*planv1.Wish{wish}, git, nil)
		if why, failed := sit.Blocker(workTask); why != "" || failed != "" {
			t.Errorf("cleared push: Blocker = (%q, %q); want empty", why, failed)
		}

		// Fallback parsing from Held string when Failure is nil
		wish.Pushes[0].Held = "at abcdef12, lint: failed"
		wish.Pushes[0].Failure = nil
		wish.Pushes[0].FailedSha = ""
		sit = New(tasks, []*planv1.Wish{wish}, git, nil)
		wantWhyFallback := "waits for feat/wails-go to be green again (push checks red at abcdef12: lint)"
		if why, failed := sit.Blocker(workTask); why != wantWhyFallback || failed != "" {
			t.Errorf("fallback: Blocker = (%q, %q); want (%q, %q)", why, failed, wantWhyFallback, "")
		}
	})

	// TestMachineAlone: a decision says the task waits for the machine alone when slots, pressure or memory hold it, never
	// when its provider's usage limit, a push hold, a dependency or a task going first in the order does: djinn task start
	// frees the machine only. It waits for room (Task.room_wait, the window's count of workers waiting for a place) in the
	// same cases, and behind a task the memory holds too, never for a limit, a push hold or a dependency.
	t.Run("machine_alone", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1,
			Pushes: []*planv1.WishPush{{ProjectId: "held", Held: "at 12345678, test: exit status 1", FailedSha: "1234567890abcdef"}}}
		task := func(id string, p planv1.Provider) *planv1.Task {
			return &planv1.Task{Id: id, WishId: "w", Code: id, Status: planv1.TaskStatus_TASK_STATUS_PENDING, Scheduled: true,
				Provider: p, CreateTime: timestamppb.New(now.Add(-time.Hour))}
		}
		free := task("free", planv1.Provider_PROVIDER_CODEX)
		limitHolder := task("limit", planv1.Provider_PROVIDER_CLAUDE)
		limitHolder.Status = planv1.TaskStatus_TASK_STATUS_RESUMING
		limitHolder.ResumeAfter, limitHolder.WaitReason = timestamppb.New(now.Add(time.Hour)), "the session limit"
		limited := task("limited", planv1.Provider_PROVIDER_CLAUDE)
		pushed := task("pushed", planv1.Provider_PROVIDER_CODEX)
		pushed.ProjectId = "held"
		after := task("after", planv1.Provider_PROVIDER_CODEX)
		after.DependsOn = []string{"free"}
		tasks := []*planv1.Task{free, limitHolder, limited, pushed, after}
		pass := func(m *Machine) map[string]Decision {
			got := map[string]Decision{}
			for _, d := range New(tasks, []*planv1.Wish{wish}, map[string]bool{"held": true}, m).At(now).Pass() {
				got[d.Task.GetId()] = d
			}
			return got
		}
		for _, m := range []*Machine{{Slots: 4, Rule: "test", Pressure: "simulated"}, {Slots: 1, Running: 1, Rule: "test"}} {
			got := pass(m)
			if d := got["free"]; d.Why == "" || !d.Machine || !d.Room {
				t.Errorf("%+v: free = %q, machine %v, room %v; want the machine alone", m, d.Why, d.Machine, d.Room)
			}
			for _, id := range []string{"limit", "limited", "pushed", "after"} {
				if d := got[id]; d.Why == "" || d.Machine || d.Room {
					t.Errorf("%+v: %s = %q, machine %v, room %v; want held by more than the machine", m, id, d.Why, d.Machine, d.Room)
				}
			}
		}
		// Memory short for the first in the order: it waits for the machine; the next one waits for it, not the machine.
		limitHolder.Status = planv1.TaskStatus_TASK_STATUS_DONE
		limitHolder.Resources = &planv1.Resources{PeakMemoryBytes: 3 * machine.GiB}
		limited.CreateTime = timestamppb.New(now.Add(-2 * time.Hour))
		tasks = append(tasks, task("next", planv1.Provider_PROVIDER_CODEX))
		got := pass(&Machine{Slots: 4, Rule: "test", Available: 2 * machine.GiB, Total: 16 * machine.GiB,
			Policy: machine.DefaultPolicy()})
		if d := got["limited"]; d.Why == "" || !d.Machine || !d.Room {
			t.Errorf("short of memory: %q, machine %v, room %v", d.Why, d.Machine, d.Room)
		}
		// Behind it, the next one waits for room all the same: not for the machine alone, which djinn task start frees.
		if d := got["next"]; !strings.Contains(d.Why, "limited goes first") || d.Machine || !d.Room {
			t.Errorf("behind one short of memory: %q, machine %v, room %v", d.Why, d.Machine, d.Room)
		}
		// A task that starts waits for nothing.
		if d := pass(&Machine{Slots: 4, Rule: "test"})["next"]; d.Why != "" || d.Machine || d.Room {
			t.Errorf("free machine: next = %q, machine %v, room %v", d.Why, d.Machine, d.Room)
		}
	})

	// TestHold: a decision says what holds a task that waits (Task.hold), the window's count of a wish's waiting workers by
	// reason: room on the machine (behind one the memory holds too, or a write scope outside Git), its branch's red push
	// checks, a dependency, a usage limit, the network, a dependency's blocking question, its wish. A task that starts or
	// fails is held by nothing.
	t.Run("hold", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
		wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1,
			Pushes: []*planv1.WishPush{{ProjectId: "held", Held: "at 12345678, test: exit status 1", FailedSha: "1234567890abcdef"}}}
		paused := &planv1.Wish{Id: "p", State: planv1.WishState_WISH_STATE_PAUSED}
		task := func(id string, edit func(*planv1.Task)) *planv1.Task {
			t := &planv1.Task{Id: id, WishId: "w", Code: id, Status: planv1.TaskStatus_TASK_STATUS_PENDING, Scheduled: true,
				Provider: planv1.Provider_PROVIDER_CODEX, CreateTime: timestamppb.New(now.Add(-time.Hour))}
			if edit != nil {
				edit(t)
			}
			return t
		}
		tasks := []*planv1.Task{
			task("full", nil),
			task("limit", func(t *planv1.Task) {
				t.Status, t.Provider = planv1.TaskStatus_TASK_STATUS_RESUMING, planv1.Provider_PROVIDER_CLAUDE
				t.ResumeAfter, t.WaitReason = timestamppb.New(now.Add(time.Hour)), "the session limit"
			}),
			task("limited", func(t *planv1.Task) { t.Provider = planv1.Provider_PROVIDER_CLAUDE }),
			task("pushed", func(t *planv1.Task) { t.ProjectId = "held" }),
			task("after", func(t *planv1.Task) { t.DependsOn = []string{"full"} }),
			task("asked", func(t *planv1.Task) {
				t.Status, t.StartTime, t.WaitQuestionId = planv1.TaskStatus_TASK_STATUS_WAITING, timestamppb.New(now), "q1"
			}),
			task("answer", func(t *planv1.Task) { t.DependsOn = []string{"asked"} }),
			task("shelved", func(t *planv1.Task) { t.WishId = "p" }),
			task("orphan", func(t *planv1.Task) { t.DependsOn = []string{"gone"} }),
		}
		pass := func(n *Network, slots int) map[string]Decision {
			got := map[string]Decision{}
			for _, d := range New(tasks, []*planv1.Wish{wish, paused}, map[string]bool{"held": true},
				&Machine{Slots: slots, Running: 1, Rule: "test"}).At(now).WithNetwork(n).Pass() {
				got[d.Task.GetId()] = d
			}
			return got
		}
		got := pass(nil, 1)
		for id, want := range map[string]planv1.TaskHold{
			"full":    planv1.TaskHold_TASK_HOLD_PLACE,
			"limit":   planv1.TaskHold_TASK_HOLD_LIMIT,
			"limited": planv1.TaskHold_TASK_HOLD_LIMIT,
			"pushed":  planv1.TaskHold_TASK_HOLD_PUSH,
			"after":   planv1.TaskHold_TASK_HOLD_DEPENDENCY,
			"answer":  planv1.TaskHold_TASK_HOLD_QUESTION,
			"shelved": planv1.TaskHold_TASK_HOLD_WISH,
			"orphan":  planv1.TaskHold_TASK_HOLD_UNSPECIFIED, // It fails.
		} {
			if d := got[id]; d.Hold != want {
				t.Errorf("%s: hold %v, want %v (why %q, failed %q)", id, d.Hold, want, d.Why, d.Failed)
			}
		}
		if d := pass(nil, 8)["full"]; d.Why != "" || d.Hold != planv1.TaskHold_TASK_HOLD_UNSPECIFIED {
			t.Errorf("free machine: full = %q, hold %v; want it started, held by nothing", d.Why, d.Hold)
		}
		// The network down holds what else would start, or wait for room: the network, not a place.
		for _, slots := range []int{1, 8} {
			got = pass(&Network{Down: "network down since 08:40"}, slots)
			if d := got["full"]; d.Hold != planv1.TaskHold_TASK_HOLD_NETWORK {
				t.Errorf("network down, %d slots: full hold %v (why %q)", slots, d.Hold, d.Why)
			}
			if d := got["pushed"]; d.Hold != planv1.TaskHold_TASK_HOLD_PUSH {
				t.Errorf("network down, %d slots: pushed hold %v (why %q)", slots, d.Hold, d.Why)
			}
		}
	})

	// TestMockupNotHeld (W268): a mockup changes no code of its project, so no hold of the other workers' state applies
	// to it: on a red branch, while a correction settles a merge's conflict, or while one repairs red checks, the work
	// waits and the mockup starts, at the clock the pass reads.
	t.Run("mockup_not_held", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 10, 17, 14, 2, 0, 0, time.UTC)
		for name, hold := range mockupHolds(now) {
			t.Run(name, func(t *testing.T) {
				tasks, wish := investigationCase(now,
					func() *planv1.Task { return planned("work", now.Add(-time.Hour), planv1.TaskKind_TASK_KIND_WORK) },
					func() *planv1.Task { return planned("mock", now, planv1.TaskKind_TASK_KIND_MOCKUP) })
				tasks = hold(wish, tasks)
				m := &Machine{Slots: 4, Rule: "test", Running: 2}
				decisions := New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, m).At(now).Pass()
				if got := decisions[0].Task.GetId(); got != "mock" {
					t.Fatalf("first = %s; want the mockup before the older work", got)
				}
				if d := decisions[0]; d.Why != "" || d.Failed != "" {
					t.Errorf("mock: %q %q; want it started", d.Why, d.Failed)
				}
				if d := decisions[1]; !strings.HasPrefix(d.Why, "waits for ") {
					t.Errorf("work: why %q; want it held", d.Why)
				}
			})
		}
	})
}

func TestWithRunning(t *testing.T) {
	t.Parallel()

	t.Run("lost_process_not_writing_nor_engaged", func(t *testing.T) {
		t.Parallel()
		wish := &planv1.Wish{Id: "wish1", State: planv1.WishState_WISH_STATE_ACTIVE}
		lost := &planv1.Task{
			Id:        "lost-1",
			WishId:    "wish1",
			ProjectId: "proj1",
			Code:      "W1",
			Provider:  planv1.Provider_PROVIDER_ANTIGRAVITY,
			Status:    planv1.TaskStatus_TASK_STATUS_RUNNING,
		}
		next := &planv1.Task{
			Id:        "next-1",
			WishId:    "wish1",
			ProjectId: "proj1",
			Code:      "W2",
			Provider:  planv1.Provider_PROVIDER_ANTIGRAVITY,
			Status:    planv1.TaskStatus_TASK_STATUS_PENDING,
			Scheduled: true,
		}
		tasks := []*planv1.Task{lost, next}
		policy := machine.Policy{WorkerPeak: 1 * machine.GiB, WorkerMargin: 512 * machine.MiB}
		m := &Machine{Slots: 4, Policy: policy}

		// Without WithRunning, lost is seen as RUNNING: writing returns true, memory is engaged.
		sitDefault := New(tasks, []*planv1.Wish{wish}, nil, m)
		if !sitDefault.writing(lost) {
			t.Errorf("expected default writing to be true for RUNNING task")
		}
		if why, _ := sitDefault.Blocker(next); why != "W1 writes the whole folder, which overlaps the whole folder" {
			t.Errorf("expected next to be blocked by W1, got %q", why)
		}

		// With WithRunning reporting false for lost-1:
		sitWithRunning := New(tasks, []*planv1.Wish{wish}, nil, m).WithRunning(func(id string) bool {
			return id != "lost-1"
		})
		if sitWithRunning.writing(lost) {
			t.Errorf("expected writing to be false for lost process")
		}
		if sitWithRunning.growing() != 0 {
			t.Errorf("expected growing to be 0 for lost process, got %d", sitWithRunning.growing())
		}
		if sitWithRunning.Engaged() != 0 {
			t.Errorf("expected engaged to be 0 for lost process, got %d", sitWithRunning.Engaged())
		}
		if why, failed := sitWithRunning.Blocker(next); why != "" || failed != "" {
			t.Errorf("expected next to be unblocked when lost process is not running, got why=%q, failed=%q", why, failed)
		}
	})
}

func TestWorkerMemory(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	policy := machine.Policy{
		WorkerPeak:   2 * machine.GiB,
		WorkerPeaks:  5,
		WorkerMargin: 512 * machine.MiB,
	}

	// Ended claude task with peak 3 GiB.
	doneClaude := &planv1.Task{
		Id: "done-claude", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_DONE,
		Provider: planv1.Provider_PROVIDER_CLAUDE, EndTime: timestamppb.New(at.Add(-time.Hour)),
		Resources: &planv1.Resources{PeakMemoryBytes: 3 * machine.GiB},
	}

	// Running claude worker using 1 GiB: typical peak is 3 GiB (> 1 GiB), so forecast is 3 GiB + 512 MiB = 3.5 GiB.
	runningClaude := &planv1.Task{
		Id: "running-claude", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		Provider:  planv1.Provider_PROVIDER_CLAUDE,
		Resources: &planv1.Resources{MemoryBytes: 1 * machine.GiB},
	}

	// Running codex worker using 2.5 GiB: no past peak so fallback peak is WorkerPeak (2 GiB).
	// Since uses (2.5 GiB) > peak (2 GiB), forecast is 2.5 GiB + 512 MiB = 3.0 GiB.
	runningCodex := &planv1.Task{
		Id: "running-codex", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		Provider:  planv1.Provider_PROVIDER_CODEX,
		Resources: &planv1.Resources{MemoryBytes: 2560 * machine.MiB}, // 2.5 GiB
	}

	// Running watcher using 100 MiB: should be ignored.
	runningWatcher := &planv1.Task{
		Id: "running-watcher", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		Provider:  planv1.Provider_PROVIDER_WATCH,
		Resources: &planv1.Resources{MemoryBytes: 100 * machine.MiB},
	}

	// Pending task: should be ignored.
	pending := &planv1.Task{
		Id: "pending", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		Provider: planv1.Provider_PROVIDER_CLAUDE,
	}

	tasks := []*planv1.Task{doneClaude, runningClaude, runningCodex, runningWatcher, pending}

	engaged, actual := WorkerMemory(tasks, policy)

	wantEngaged := uint64((3*machine.GiB + 512*machine.MiB) + (2560*machine.MiB + 512*machine.MiB))
	wantActual := uint64((1 * machine.GiB) + (2560 * machine.MiB))

	if engaged != wantEngaged {
		t.Errorf("engaged: got %d, want %d", engaged, wantEngaged)
	}
	if actual != wantActual {
		t.Errorf("actual: got %d, want %d", actual, wantActual)
	}

	// Verify Situation.Engaged() matches
	s := New(tasks, []*planv1.Wish{{Id: "w"}}, nil, &Machine{Policy: policy}).At(at)
	if sEngaged := s.Engaged(); sEngaged != wantEngaged {
		t.Errorf("Situation.Engaged(): got %d, want %d", sEngaged, wantEngaged)
	}
}

// TestProviderPeaks: finished workers' p95 memories are grouped by provider, latest first,
// falling back to peak memory when p95 is unset, and ignoring running tasks, watchers,
// and tasks with no measurements.
func TestProviderPeaks(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	tasks := []*planv1.Task{
		{
			// Unset p95: falls back to PeakMemoryBytes.
			Id: "c1", Provider: planv1.Provider_PROVIDER_CLAUDE, Status: planv1.TaskStatus_TASK_STATUS_DONE,
			EndTime: timestamppb.New(now.Add(-2 * time.Hour)), Resources: &planv1.Resources{PeakMemoryBytes: 500 * machine.MiB},
		},
		{
			// Has p95: uses P95MemoryBytes.
			Id: "c2", Provider: planv1.Provider_PROVIDER_CLAUDE, Status: planv1.TaskStatus_TASK_STATUS_DONE,
			EndTime: timestamppb.New(now.Add(-1 * time.Hour)), Resources: &planv1.Resources{
				PeakMemoryBytes: 800 * machine.MiB,
				P95MemoryBytes:  750 * machine.MiB,
			},
		},
		{
			// Watcher: ignored.
			Id: "c-watch", Provider: planv1.Provider_PROVIDER_WATCH, Status: planv1.TaskStatus_TASK_STATUS_DONE,
			EndTime: timestamppb.New(now), Resources: &planv1.Resources{PeakMemoryBytes: 100 * machine.MiB, P95MemoryBytes: 90 * machine.MiB},
		},
		{
			// Running: ignored.
			Id: "c-run", Provider: planv1.Provider_PROVIDER_CLAUDE, Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
			Resources: &planv1.Resources{PeakMemoryBytes: 900 * machine.MiB, P95MemoryBytes: 850 * machine.MiB},
		},
		{
			// No peak/p95 memory: ignored.
			Id: "c-no-peak", Provider: planv1.Provider_PROVIDER_CLAUDE, Status: planv1.TaskStatus_TASK_STATUS_DONE,
			EndTime: timestamppb.New(now),
		},
		{
			Id: "k1", Provider: planv1.Provider_PROVIDER_CODEX, Status: planv1.TaskStatus_TASK_STATUS_DONE,
			EndTime: timestamppb.New(now.Add(-30 * time.Minute)), Resources: &planv1.Resources{
				PeakMemoryBytes: 1200 * machine.MiB,
				P95MemoryBytes:  1100 * machine.MiB,
			},
		},
	}

	peaks := ProviderPeaks(tasks)
	// Claude should have [750 MiB (p95), 500 MiB (fallback peak)] (latest first).
	wantClaude := []uint64{750 * machine.MiB, 500 * machine.MiB}
	if !slices.Equal(peaks["claude"], wantClaude) {
		t.Errorf("claude peaks: got %v, want %v", peaks["claude"], wantClaude)
	}
	wantCodex := []uint64{1100 * machine.MiB}
	if !slices.Equal(peaks["codex"], wantCodex) {
		t.Errorf("codex peaks: got %v, want %v", peaks["codex"], wantCodex)
	}
	if len(peaks) != 2 {
		t.Errorf("expected 2 providers, got %d", len(peaks))
	}
}

func TestMemoryAt(t *testing.T) {
	t.Parallel()

	// TestEngagedAtNotchPercentile: a running worker is engaged at its own memory at the notch's percentile plus the
	// notch's margin, a new one at the median of the finished workers' value at that percentile.
	t.Run("engaged_at_notch_percentile", func(t *testing.T) {
		t.Parallel()
		at := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
		res := &planv1.Resources{
			MemoryBytes:    100 * machine.MiB,
			P50MemoryBytes: 500 * machine.MiB, P60MemoryBytes: 600 * machine.MiB, P70MemoryBytes: 700 * machine.MiB,
			P80MemoryBytes: 800 * machine.MiB, P95MemoryBytes: 950 * machine.MiB, PeakMemoryBytes: 3000 * machine.MiB,
		}
		running := &planv1.Task{Id: "r", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
			StartTime: timestamppb.New(at.Add(-time.Hour)), Resources: res}
		done := &planv1.Task{Id: "d", Status: planv1.TaskStatus_TASK_STATUS_DONE, EndTime: timestamppb.New(at), Resources: res}
		for _, tt := range []struct {
			notch      djinnv1.LoadNotch
			percentile int
			at, margin uint64
		}{
			{djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, 95, 950, 512},
			{djinnv1.LoadNotch_LOAD_NOTCH_LIGHT, 80, 800, 512}, // Light is Medium (NotchPolicy)
			{djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, 80, 800, 512},
			{djinnv1.LoadNotch_LOAD_NOTCH_HIGH, 70, 700, 384},
			{djinnv1.LoadNotch_LOAD_NOTCH_MAX, 60, 600, 256},
			{djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, 50, 500, 256},
		} {
			p := machine.NotchPolicy(tt.notch)
			if got := p.MemoryPercentile(); got != tt.percentile {
				t.Errorf("%v: percentile %d, want %d", tt.notch, got, tt.percentile)
			}
			want := (tt.at + tt.margin) * machine.MiB
			if engaged, actual := WorkerMemory([]*planv1.Task{running}, p, at); engaged != want || actual != 100*machine.MiB {
				t.Errorf("%v: engaged %d MiB (actual %d MiB), want %d MiB", tt.notch, engaged>>20, actual>>20, want>>20)
			}
			if got := TypicalForecast([]*planv1.Task{done}, p); got != want {
				t.Errorf("%v: a new worker's forecast %d MiB, want %d MiB", tt.notch, got>>20, want>>20)
			}
		}

		// A worker measured before the percentiles were kept falls back to its p95, then its peak.
		old := &planv1.Resources{P95MemoryBytes: 950 * machine.MiB, PeakMemoryBytes: 3000 * machine.MiB}
		if got := MemoryAt(old, 50); got != 950*machine.MiB {
			t.Errorf("MemoryAt(no p50, 50) = %d MiB, want its p95", got>>20)
		}
		if got := MemoryAt(&planv1.Resources{PeakMemoryBytes: 3 * machine.GiB}, 50); got != 3*machine.GiB {
			t.Errorf("MemoryAt(peak only, 50) = %d MiB, want its peak", got>>20)
		}
		// The guardrail keeps the p95, whatever the notch.
		if got := ProviderPeaks([]*planv1.Task{done})["claude"]; len(got) != 1 || got[0] != 950*machine.MiB {
			t.Errorf("ProviderPeaks = %v, want the p95", got)
		}
	})
}

func TestTypicalForecast(t *testing.T) {
	t.Parallel()

	// TestWorkerPeakWarmup:
	// 1. A worker past the delay counts its own p95;
	// 2. Before it, the forecast (median of finished workers' p95s);
	// 3. A worker whose current memory exceeds its old p95 counts the current resident memory;
	// 4. A paused worker keeps counting its own p95;
	// 5. A worker without p95 falls back to peak.
	t.Run("worker_peak_warmup", func(t *testing.T) {
		t.Parallel()
		at := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
		policy := machine.Policy{
			WorkerPeak:   2 * machine.GiB,
			WorkerMargin: 512 * machine.MiB,
		}

		// 1 past finished worker with p95 2.5 GiB sets provider typical p95 to 2.5 GiB.
		// So typical forecast = 2.5 GiB + 512 MiB = 3072 MiB.
		done := &planv1.Task{
			Id: "done", Status: planv1.TaskStatus_TASK_STATUS_DONE,
			Provider: planv1.Provider_PROVIDER_ANTIGRAVITY, EndTime: timestamppb.New(at.Add(-time.Hour)),
			Resources: &planv1.Resources{
				PeakMemoryBytes: 3 * machine.GiB,
				P95MemoryBytes:  2560 * machine.MiB,
			},
		}

		// Case 1: worker past the delay (started 5 minutes ago >= WorkerPeakWarmup (3 min))
		// Peak is 1.8 GiB, p95 is 1.5 GiB, resident is 1.0 GiB.
		// Should count max(1.5, 1.0) + 512 MiB = 1.5 GiB + 512 MiB = 2048 MiB.
		pastWorker := &planv1.Task{
			Id: "past", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
			Provider:  planv1.Provider_PROVIDER_ANTIGRAVITY,
			StartTime: timestamppb.New(at.Add(-5 * time.Minute)),
			Resources: &planv1.Resources{
				PeakMemoryBytes: 1843 * machine.MiB, // 1.8 GiB
				P95MemoryBytes:  1536 * machine.MiB, // 1.5 GiB
				MemoryBytes:     1024 * machine.MiB, // 1.0 GiB
			},
		}
		engagedPast, actualPast := WorkerMemory([]*planv1.Task{done, pastWorker}, policy, at)
		wantPastEngaged := uint64(1536*machine.MiB + 512*machine.MiB)
		if engagedPast != wantPastEngaged {
			t.Errorf("worker past delay engaged: got %d, want %d (own p95 + margin)", engagedPast, wantPastEngaged)
		}
		if actualPast != 1024*machine.MiB {
			t.Errorf("worker past delay actual: got %d, want %d", actualPast, 1024*machine.MiB)
		}

		// Case 2: worker before the delay (started 1 minute ago < WorkerPeakWarmup)
		// Peak is 1.8 GiB, p95 is 1.5 GiB, resident is 1.0 GiB.
		// Should count provider forecast (2.5 GiB) + margin (512 MiB) = 3072 MiB.
		beforeWorker := &planv1.Task{
			Id: "before", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
			Provider:  planv1.Provider_PROVIDER_ANTIGRAVITY,
			StartTime: timestamppb.New(at.Add(-1 * time.Minute)),
			Resources: &planv1.Resources{
				PeakMemoryBytes: 1843 * machine.MiB,
				P95MemoryBytes:  1536 * machine.MiB,
				MemoryBytes:     1024 * machine.MiB,
			},
		}
		engagedBefore, _ := WorkerMemory([]*planv1.Task{done, beforeWorker}, policy, at)
		wantBeforeEngaged := uint64(2560*machine.MiB + 512*machine.MiB)
		if engagedBefore != wantBeforeEngaged {
			t.Errorf("worker before delay engaged: got %d, want %d (forecast + margin)", engagedBefore, wantBeforeEngaged)
		}

		// Case 3: worker whose current resident memory exceeds its old p95
		// Running past delay (5 min), p95 is 1.4 GiB, current resident memory is 2.2 GiB.
		// Should count max(1.4, 2.2) + margin = 2.2 GiB + 512 MiB = 2764 MiB.
		exceedWorker := &planv1.Task{
			Id: "exceed", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
			Provider:  planv1.Provider_PROVIDER_ANTIGRAVITY,
			StartTime: timestamppb.New(at.Add(-5 * time.Minute)),
			Resources: &planv1.Resources{
				PeakMemoryBytes: 1536 * machine.MiB, // 1.5 GiB
				P95MemoryBytes:  1433 * machine.MiB, // 1.4 GiB
				MemoryBytes:     2252 * machine.MiB, // 2.2 GiB
			},
		}
		engagedExceed, _ := WorkerMemory([]*planv1.Task{done, exceedWorker}, policy, at)
		wantExceedEngaged := uint64(2252*machine.MiB + 512*machine.MiB)
		if engagedExceed != wantExceedEngaged {
			t.Errorf("worker exceeding old peak engaged: got %d, want %d (current memory + margin)", engagedExceed, wantExceedEngaged)
		}

		// Case 4: paused worker keeps counting its own p95
		pausedWorker := &planv1.Task{
			Id: "paused", Status: planv1.TaskStatus_TASK_STATUS_PAUSED,
			Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
			Resources: &planv1.Resources{
				PeakMemoryBytes: 2048 * machine.MiB,
				P95MemoryBytes:  1843 * machine.MiB,
				MemoryBytes:     1228 * machine.MiB,
			},
		}
		engagedPaused, _ := WorkerMemory([]*planv1.Task{done, pausedWorker}, policy, at)
		wantPausedEngaged := uint64(1843*machine.MiB + 512*machine.MiB)
		if engagedPaused != wantPausedEngaged {
			t.Errorf("paused worker engaged: got %d, want %d (own p95 + margin)", engagedPaused, wantPausedEngaged)
		}

		// Case 5: paused worker without p95 falls back to peak
		fallbackWorker := &planv1.Task{
			Id: "fallback", Status: planv1.TaskStatus_TASK_STATUS_PAUSED,
			Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
			Resources: &planv1.Resources{
				PeakMemoryBytes: 2048 * machine.MiB,
				MemoryBytes:     1228 * machine.MiB,
			},
		}
		engagedFallback, _ := WorkerMemory([]*planv1.Task{done, fallbackWorker}, policy, at)
		wantFallbackEngaged := uint64(2048*machine.MiB + 512*machine.MiB)
		if engagedFallback != wantFallbackEngaged {
			t.Errorf("fallback worker engaged: got %d, want %d (fallback peak + margin)", engagedFallback, wantFallbackEngaged)
		}
	})
}

// TestInvestigationCap (W213): at most one investigation runs at once on a machine of three slots or fewer, two above;
// the next one waits for the machine, saying so, whatever the free slots.
func TestInvestigationCap(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 17, 11, 0, 0, 0, time.UTC)
	if InvestigationCap(1) != 1 || InvestigationCap(3) != 1 || InvestigationCap(4) != 2 || InvestigationCap(16) != 2 {
		t.Errorf("InvestigationCap = %d %d %d %d", InvestigationCap(1), InvestigationCap(3), InvestigationCap(4), InvestigationCap(16))
	}
	study := func(id string, age time.Duration) func() *planv1.Task {
		return func() *planv1.Task { return planned(id, now.Add(-age), planv1.TaskKind_TASK_KIND_INVESTIGATION) }
	}
	tasks, wish := investigationCase(now, study("a", 3*time.Minute), study("b", 2*time.Minute), study("c", time.Minute))
	for _, c := range []struct {
		slots   int
		started []string
	}{{3, []string{"a"}}, {8, []string{"a", "b"}}} {
		var started []string
		for _, d := range New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, &Machine{Slots: c.slots, Rule: "test", Running: 1}).At(now).Pass() {
			switch {
			case d.Why == "":
				started = append(started, d.Task.GetId())
			case !d.Machine || !strings.Contains(d.Why, "the most investigations this machine runs at once"):
				t.Errorf("%d slots, %s: why %q, machine %v", c.slots, d.Task.GetId(), d.Why, d.Machine)
			}
		}
		if strings.Join(started, ",") != strings.Join(c.started, ",") {
			t.Errorf("%d slots: started %v, want %v", c.slots, started, c.started)
		}
	}
	// One running counts: the next waits on a machine of three slots.
	tasks[1].Status = planv1.TaskStatus_TASK_STATUS_RUNNING
	for _, d := range New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, &Machine{Slots: 3, Rule: "test", Running: 2}).At(now).Pass() {
		if d.Why == "" {
			t.Errorf("%s starts beside a running investigation on 3 slots", d.Task.GetId())
		}
	}
}
