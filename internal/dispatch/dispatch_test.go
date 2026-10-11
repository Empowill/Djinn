package dispatch

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
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
// resumed as a fork, or closed as continued in one, is its fork. A dependency stopped by a person
// fails it; one failed holds the task waiting until it is replayed, continued or marked done.
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

// TestQuestionWorkerTakesNoSlot: a question worker starts on a full machine and beside a writer of its folder, but
// waits while the machine is under pressure or its memory would not hold it; started, its memory counts for the next.
func TestQuestionWorkerTakesNoSlot(t *testing.T) {
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
	full := &Machine{Slots: 1, Rule: "test", Running: 1}
	if got, want := pass([]*planv1.Task{writer, investigator, agent}, full), map[string]string{
		"investigator": "", "agent": "writer writes the whole folder, which overlaps the whole folder",
	}; !maps.Equal(got, want) {
		t.Errorf("full: %v, want %v", got, want)
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

// TestWaitsForTheCommit: a dependency whose work Djinn integrates counts once committed into the wish's integration
// branch, not when its worker ends done; one whose work Djinn does not integrate counts once done.
func TestWaitsForTheCommit(t *testing.T) {
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
}

// TestWaitsForBranchWork: a DONE dependency with a branch counts as met only when its work is in the wish's
// integration branch: integration COMMITTED, or (no record) its branch's head is an ancestor of the integration
// branch's head, or it has no commit beyond the point it started from. Otherwise the dependent waits, with a
// reason that says so ("waits for W5's work to reach feat/wails-go"). The ancestry check is cached.
func TestWaitsForBranchWork(t *testing.T) {
	wish := &planv1.Wish{
		Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1,
		IntegrationBranches: []*planv1.IntegrationBranch{{ProjectId: "p1", Branch: "feat/wails-go"}},
	}
	cases := []struct {
		name       string
		branch     string
		state      planv1.IntegrationState
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
			if c.state != planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED {
				dep.Integration = &planv1.TaskIntegration{State: c.state}
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
			if c.bHead != "" && !c.noCommits && c.state == planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED {
				prevCalls := calls
				sit.Blocker(task)
				if calls != prevCalls {
					t.Errorf("ancestry not cached: calls went from %d to %d", prevCalls, calls)
				}
			}
		})
	}
}

// TestMemoryCommittableLimit: when machine Total is known, dispatch respects the load notch committable memory ceiling.
func TestMemoryCommittableLimit(t *testing.T) {
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
}

func TestWorkerMemory(t *testing.T) {
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

// TestStrict5TierOrder verifies the 5-tier dispatch order and tie-breaking:
//  1. Investigator workers, then answer workers;
//  2. Correction workers, then review workers;
//  3. Resumed work tasks;
//  4. Critical path: work tasks that unblock others (most waiting/planned dependents first);
//  5. Other work tasks.
//
// Within each tier/subtier: wish rank, oldest first, task ID.
func TestStrict5TierOrder(t *testing.T) {
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
}

// TestInvestigatorPriorityAndMemory verifies that investigators take memory before work tasks,
// that work tasks held back by an investigator/correction/review report the specific reason,
// and that investigators still wait under pressure.
func TestInvestigatorPriorityAndMemory(t *testing.T) {
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

	// 1. Investigator skips slot: starts when all slots are full
	mFull := &Machine{Slots: 1, Running: 1, Rule: "test"}
	pass := func(tasks []*planv1.Task, mach *Machine) map[string]string {
		got := map[string]string{}
		for _, d := range New(tasks, []*planv1.Wish{wish}, nil, mach).At(at).Pass() {
			got[d.Task.GetId()] = d.Why
		}
		return got
	}

	got := pass([]*planv1.Task{workTask, invTask}, mFull)
	if got["q1"] != "" {
		t.Errorf("investigator should start without slot, got why: %q", got["q1"])
	}
	if got["w1"] == "" {
		t.Error("work task should wait for slot")
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
}

// TestWorkerPeakWarmup:
// 1. A worker past the delay counts its own p95;
// 2. Before it, the forecast (median of finished workers' p95s);
// 3. A worker whose current memory exceeds its old p95 counts the current resident memory;
// 4. A paused worker keeps counting its own p95;
// 5. A worker without p95 falls back to peak.
func TestWorkerPeakWarmup(t *testing.T) {
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
}

// TestOverclockRoomForOneMore verifies that on an Overclock machine with 4 running Antigravity workers
// (p95s 0.9 to 2.1 GiB totaling 6.7 GiB p95s and 8.6 GiB engaged with margins) under a 14.8 GiB limit,
// Djinn leaves room to start a 5th worker (forecast 3.0 GiB).
func TestOverclockRoomForOneMore(t *testing.T) {
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
}

// TestPushHeld: while a wish's push is held on red push checks, the scheduler starts only
// push-check correction workers, other correction workers, and read-only question workers.
// Every other planned task of that wish in that project waits for the branch to be green again.
func TestPushHeld(t *testing.T) {
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

	// Unchecked push lifts gate
	wish.Pushes[0].Unchecked = true
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
}

func TestLostProcessNotWritingNorEngaged(t *testing.T) {
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
}
