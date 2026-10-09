package dispatch

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
)

func TestOverlap(t *testing.T) {
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

// TestResumingFirst: the tasks Djinn resumes start before new ones, whatever their age; one waiting for its
// provider's usage limit waits until it resets, and holds every task of that provider meanwhile.
func TestResumingFirst(t *testing.T) {
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
}

// TestDependencyResumes: a dependency Djinn resumes, interrupted or resuming, holds the task until it ends; one
// resumed as a fork, or closed as continued in one, is its fork. A dependency failed (resumed maxResumes times among them) or stopped by a person
// fails it.
func TestDependencyResumes(t *testing.T) {
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
			Error: "resumed 3 times without finishing"}}, "", "its dependency W1 ended failed"},
		{"stopped", []*planv1.Task{task("W1", planv1.TaskStatus_TASK_STATUS_STOPPED)}, "", "its dependency W1 ended stopped"},
		{"forked, running", []*planv1.Task{task("W1", planv1.TaskStatus_TASK_STATUS_INTERRUPTED),
			{Id: "W5", WishId: "w", Code: "W5", ForkOf: "W1", Status: planv1.TaskStatus_TASK_STATUS_RUNNING}},
			"waits for W5 (running)", ""},
		{"forked twice, done", []*planv1.Task{task("W1", planv1.TaskStatus_TASK_STATUS_INTERRUPTED),
			{Id: "W5", WishId: "w", Code: "W5", ForkOf: "W1", Status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED},
			{Id: "W6", WishId: "w", Code: "W6", ForkOf: "W5", Status: planv1.TaskStatus_TASK_STATUS_DONE}}, "", ""},
		{"forked, failed", []*planv1.Task{task("W1", planv1.TaskStatus_TASK_STATUS_INTERRUPTED),
			{Id: "W5", WishId: "w", Code: "W5", ForkOf: "W1", Status: planv1.TaskStatus_TASK_STATUS_FAILED}},
			"", "its dependency W1, resumed as W5, ended failed"},
		{"closed as continued, running", []*planv1.Task{{Id: "W1", WishId: "w", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_DONE,
			Closed: &planv1.Closure{ContinuedIn: "W5"}},
			{Id: "W5", WishId: "w", Code: "W5", ForkOf: "W1", Status: planv1.TaskStatus_TASK_STATUS_RUNNING}},
			"waits for W5 (running)", ""},
		{"closed as continued, failed", []*planv1.Task{{Id: "W1", WishId: "w", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_DONE,
			Closed: &planv1.Closure{ContinuedIn: "W5"}},
			{Id: "W5", WishId: "w", Code: "W5", ForkOf: "W1", Status: planv1.TaskStatus_TASK_STATUS_FAILED}},
			"", "its dependency W1, resumed as W5, ended failed"},
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
}

// TestWatcherTakesNoSlot: a watcher starts on a full machine under pressure, outside Git next to a writer of the
// whole folder; it takes no slot from the task after it, and holds no writer back. Its wish and its dependencies
// still hold it.
func TestWatcherTakesNoSlot(t *testing.T) {
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
}

// TestRestartQueue plays the passes after a restart: 6 tasks Djinn resumes over 2 wishes, a planned one, and 2
// resumed tasks of a paused wish, on a machine of 2 slots where each pass ends the oldest worker. The first wish's
// resumed tasks start first, then the second's, each wish in its own order, then the planned one: 2 at a time, the
// others waiting for a slot, saying so; nothing starts under pressure; the paused wish's tasks wait for it, then
// start once it is active. No task starts twice.
func TestRestartQueue(t *testing.T) {
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
}

// TestMemoryHoldsWorker: a worker starts only when the memory available, less what the running workers may still
// take, holds the typical peak of its provider (the median of its finished workers, a default when none is
// measured); the reason names those numbers. A resumed task the memory holds keeps its turn: a lighter one behind it
// waits. Once the memory frees, it starts.
func TestMemoryHoldsWorker(t *testing.T) {
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
}
