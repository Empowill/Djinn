package dispatch

import (
	"maps"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
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
// resumed as a fork is its fork. A dependency failed (resumed maxResumes times among them) or stopped by a person
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
