// Package bench is the dispatch bench (T16): dispatch situations written as data, each with the decision expected
// by hand, and what a contender decides for each. The plain Go scheduler (internal/dispatch) is the first
// contender; a local model would read the same cases. go tool task bench-dispatch prints the table.
package bench

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/dispatch"
)

//go:embed cases.json
var casesJSON []byte

// The decisions a case expects for a planned task.
const (
	Start = "start"
	Wait  = "wait"
	Fail  = "fail"
)

// Case is a dispatch situation and the decision expected for each of its planned tasks.
type Case struct {
	// Name says what the case checks.
	Name string `json:"name"`
	// Machine is what the machine allows; absent, nothing limits.
	Machine *Machine `json:"machine,omitempty"`
	// Wishes, oldest first.
	Wishes []Wish `json:"wishes"`
	// Projects; a project not listed is not in Git.
	Projects []Project `json:"projects,omitempty"`
	// Tasks, oldest first.
	Tasks []Task `json:"tasks"`
	// Gates held now. The Go scheduler does not read them: a worker waits for its gate when it runs the command.
	Gates []Gate `json:"gates,omitempty"`
	// Expect is the decision for each planned or resuming task, by code: start, wait or fail.
	Expect map[string]string `json:"expect"`
}

// Machine is what the machine allows in a case.
type Machine struct {
	Slots    int    `json:"slots"`
	Running  int    `json:"running"`
	Pressure string `json:"pressure,omitempty"`
}

// Wish is a wish of a case.
type Wish struct {
	ID string `json:"id"`
	// State: active (the default), paused or granted.
	State string `json:"state,omitempty"`
	// Rank among the active wishes, 1 first; 0 comes after the ranked ones.
	Rank int32 `json:"rank,omitempty"`
}

// Project is a project of a case.
type Project struct {
	ID  string `json:"id"`
	Git bool   `json:"git"`
}

// Task is a task of a case. Its code is its identifier, unique in the case.
type Task struct {
	Code string `json:"code"`
	Wish string `json:"wish"`
	// Project; empty outside any project.
	Project string `json:"project,omitempty"`
	// Status: planned (pending, started by the scheduler), resuming (cut short, started again by the scheduler),
	// running, waiting (for its edit question), done, failed, stopped or interrupted.
	Status    string   `json:"status"`
	DependsOn []string `json:"depends_on,omitempty"`
	// Scopes are the write scopes; none is the whole folder.
	Scopes []string `json:"scopes,omitempty"`
	// ForkOf is the code of the task this one forks: an interrupted task forked is resumed as its fork.
	ForkOf string `json:"fork_of,omitempty"`
}

// Gate is a gate held in a case.
type Gate struct {
	Name   string `json:"name"`
	Holder string `json:"holder"`
}

// Cases are the bench's cases.
func Cases() ([]Case, error) {
	var out []Case
	if err := json.Unmarshal(casesJSON, &out); err != nil {
		return nil, fmt.Errorf("cases.json: %w", err)
	}
	for _, c := range out {
		if err := c.check(); err != nil {
			return nil, fmt.Errorf("case %q: %w", c.Name, err)
		}
	}
	return out, nil
}

// check refuses a case that names what it does not have, or expects nothing of a planned task.
func (c Case) check() error {
	codes := map[string]bool{}
	for _, t := range c.Tasks {
		if codes[t.Code] {
			return fmt.Errorf("task %s twice", t.Code)
		}
		codes[t.Code] = true
		if _, ok := statuses[t.Status]; !ok {
			return fmt.Errorf("task %s: status %q", t.Code, t.Status)
		}
		if !slices.ContainsFunc(c.Wishes, func(w Wish) bool { return w.ID == t.Wish }) {
			return fmt.Errorf("task %s: no wish %q", t.Code, t.Wish)
		}
		if _, ok := c.Expect[t.Code]; ok != scheduled(t.Status) {
			return fmt.Errorf("task %s: an expected decision for each planned task, and only for them", t.Code)
		}
	}
	for code, d := range c.Expect {
		if d != Start && d != Wait && d != Fail {
			return fmt.Errorf("task %s: expected %q, not start, wait or fail", code, d)
		}
	}
	for _, w := range c.Wishes {
		if _, ok := wishStates[w.State]; !ok {
			return fmt.Errorf("wish %s: state %q", w.ID, w.State)
		}
	}
	return nil
}

// scheduled tells whether the scheduler decides for a task of that status: it starts it, or starts it again.
func scheduled(status string) bool { return status == "planned" || status == "resuming" }

var statuses = map[string]planv1.TaskStatus{
	"planned": planv1.TaskStatus_TASK_STATUS_PENDING, "resuming": planv1.TaskStatus_TASK_STATUS_RESUMING,
	"running": planv1.TaskStatus_TASK_STATUS_RUNNING,
	"waiting": planv1.TaskStatus_TASK_STATUS_WAITING, "done": planv1.TaskStatus_TASK_STATUS_DONE,
	"failed": planv1.TaskStatus_TASK_STATUS_FAILED, "stopped": planv1.TaskStatus_TASK_STATUS_STOPPED,
	"interrupted": planv1.TaskStatus_TASK_STATUS_INTERRUPTED,
}

var wishStates = map[string]planv1.WishState{
	"": planv1.WishState_WISH_STATE_ACTIVE, "active": planv1.WishState_WISH_STATE_ACTIVE,
	"paused": planv1.WishState_WISH_STATE_PAUSED, "granted": planv1.WishState_WISH_STATE_GRANTED,
}

// Situation is the case as the Go scheduler reads it. The tasks are a second apart, in the order of the case.
func (c Case) Situation() *dispatch.Situation {
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	var tasks []*planv1.Task
	for i, t := range c.Tasks {
		task := &planv1.Task{
			Id: t.Code, Code: t.Code, WishId: t.Wish, ProjectId: t.Project, Status: statuses[t.Status],
			DependsOn: t.DependsOn, WriteScopes: t.Scopes, ForkOf: t.ForkOf, CreateTime: timestamppb.New(start.Add(time.Duration(i) * time.Second)),
		}
		task.Scheduled = scheduled(t.Status)
		if t.Status != "planned" {
			task.StartTime = task.CreateTime
		}
		tasks = append(tasks, task)
	}
	var wishes []*planv1.Wish
	for _, w := range c.Wishes {
		wishes = append(wishes, &planv1.Wish{Id: w.ID, State: wishStates[w.State], Rank: w.Rank})
	}
	git := map[string]bool{}
	for _, p := range c.Projects {
		git[p.ID] = p.Git
	}
	var m *dispatch.Machine
	if c.Machine != nil {
		m = &dispatch.Machine{Slots: c.Machine.Slots, Rule: "the case's", Pressure: c.Machine.Pressure, Running: c.Machine.Running}
	}
	return dispatch.New(tasks, wishes, git, m)
}

// Answer is what a contender decided for one planned task.
type Answer struct {
	Code     string
	Decision string // start, wait or fail
	Reason   string // why it waits or fails
}

// Result is a contender's answer to a case.
type Result struct {
	Case    Case
	Answers []Answer // in the order the contender decided
	// Wrong are the codes whose decision is not the expected one.
	Wrong []string
	// PerPass is the time of one pass over the case, the situation built included.
	PerPass time.Duration
}

// Right tells whether every decision is the expected one.
func (r Result) Right() bool { return len(r.Wrong) == 0 }

// Go runs the plain Go scheduler on the case, then times passes over it for about budget.
func Go(c Case, budget time.Duration) Result {
	r := Result{Case: c}
	for _, d := range c.Situation().Pass() {
		a := Answer{Code: d.Task.GetCode(), Decision: Start}
		switch {
		case d.Failed != "":
			a.Decision, a.Reason = Fail, d.Failed
		case d.Why != "":
			a.Decision, a.Reason = Wait, d.Why
		}
		if c.Expect[a.Code] != a.Decision {
			r.Wrong = append(r.Wrong, a.Code)
		}
		r.Answers = append(r.Answers, a)
	}
	n, began := 0, time.Now()
	for n == 0 || time.Since(began) < budget {
		for range 100 {
			c.Situation().Pass()
		}
		n += 100
	}
	r.PerPass = time.Since(began) / time.Duration(n)
	return r
}

// Table writes the results as a Markdown table, then the score.
func Table(w io.Writer, contender string, results []Result) {
	fmt.Fprintf(w, "| Case | %s decides | Right | Time per pass |\n|---|---|---|---|\n", contender)
	right, decisions, wrong := 0, 0, 0
	var total time.Duration
	for _, r := range results {
		var parts []string
		for _, a := range r.Answers {
			s := a.Code + " " + a.Decision
			if a.Reason != "" {
				s += ": " + a.Reason
			}
			if slices.Contains(r.Wrong, a.Code) {
				s += " (expected " + r.Case.Expect[a.Code] + ")"
			}
			parts = append(parts, s)
		}
		mark := "yes"
		if !r.Right() {
			mark = "**no**"
		} else {
			right++
		}
		decisions += len(r.Answers)
		wrong += len(r.Wrong)
		total += r.PerPass
		fmt.Fprintf(w, "| %s | %s | %s | %s |\n", r.Case.Name, cell(strings.Join(parts, "; ")), mark, r.PerPass)
	}
	fmt.Fprintf(w, "\n%s: %d of %d cases right; %d of %d decisions right; %s per pass on average.\n", contender,
		right, len(results), decisions-wrong, decisions, total/time.Duration(max(len(results), 1)))
}

// cell escapes the pipes of a Markdown table cell.
func cell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
