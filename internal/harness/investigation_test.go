package harness

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// checkedRepo is a repository whose settings name a commit check: a work task's done work goes in line to be merged.
func checkedRepo(t *testing.T) string {
	t.Helper()
	return gitRepoWith(t, map[string]string{
		plan.SettingsFile: "checks { name: \"lint\" command: \"git --version\" when: CHECK_WHEN_COMMIT }\n",
	})
}

// holdPush makes the push of the wish's integration branch held on red push checks, as Djinn leaves it once its
// corrections are spent: its planned work waits for the branch to be green again.
func (e *env) holdPush(t *testing.T, wishID, projectID string) {
	t.Helper()
	err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
		w, err := store.Get[*planv1.Wish](t.Context(), tx, wishID)
		if err != nil {
			return err
		}
		w.Pushes = append(w.Pushes, &planv1.WishPush{ProjectId: projectID, Held: "at 0f2e0240, test: exit status 1",
			FailedSha: "0f2e02401234", Failure: &planv1.IntegrationFailure{Reason: "test: exit status 1", Base: "0f2e02401234"}})
		if err := tx.Journal(actorHarness, "test/hold", w); err != nil {
			return err
		}
		return tx.Put(w)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// toldLines collects the lines Djinn tells the leads.
type toldLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *toldLines) tell(_ context.Context, _, line string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, line)
	return nil
}

func (l *toldLines) with(text string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if i := slices.IndexFunc(l.lines, func(s string) bool { return strings.Contains(s, text) }); i >= 0 {
		return l.lines[i]
	}
	return ""
}

// TestInvestigationServedFirst (W213): on a full machine, while the integration branch's push checks are red, an
// investigation starts at once, before the older planned work, in the slot of the work task it pauses; it ends done
// with its commit never merged, which its ENDED line says, and the paused work resumes. A work task's done commit, in
// the same project, goes in line to be merged.
func TestInvestigationServedFirst(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	told := &toldLines{}
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 1}).capacity), WithTick(time.Hour), WithTellDelay(10*time.Millisecond))
	e.h.TellLeads(told.tell)
	wishID, projectID := e.wish(t, checkedRepo(t))
	busy := e.mustSpawn(t, wishID, "Busy", "wait\ntext busy", nil)
	e.until(t, busy.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	e.holdPush(t, wishID, projectID)
	old := e.mustSpawn(t, wishID, "Old", "text old", nil)
	if old.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
		t.Fatalf("old = %v; want it waiting", old)
	}

	study := e.mustSpawn(t, wishID, "Study the flaky tests", "wait\ntext studied",
		&planv1.TaskServiceSpawnRequest{Kind: planv1.TaskKind_TASK_KIND_INVESTIGATION})
	if study.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || study.GetWorktree() == "" {
		t.Fatalf("study = %v; want it running in a worktree on the full machine and the red branch", study)
	}
	if study.GetMaxBudgetUsd() != DefaultInvestigationBudgetUSD || study.GetInvestigation() == nil {
		t.Errorf("study budget %v, investigation %v; want Djinn's defaults", study.GetMaxBudgetUsd(), study.GetInvestigation())
	}
	e.until(t, busy.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_PAUSED))
	if got := e.get(t, old.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING ||
		!strings.Contains(got.GetWaitReason(), "to be green again") {
		t.Errorf("old = %v; want it still held by the red branch", got)
	}

	// It commits in its worktree; done, its commit stays out of the branch.
	commit(t, study.GetWorktree(), "probe.txt")
	e.release(t, study.GetId())
	done := e.ended(t, study.GetId())
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || done.GetIntegration() != nil {
		t.Fatalf("study ended %v, integration %v; want done, never integrated", done.GetStatus(), done.GetIntegration())
	}
	if !slices.ContainsFunc(e.storedEvents(t, study.GetId()), func(ev *planv1.TaskEvent) bool {
		return ev.GetText() == "integration: none: "+investigationLeft
	}) {
		t.Error("no event says its integration is none")
	}
	waitFor(t, "the ENDED line", func() bool { e.h.FlushTell(t.Context()); return told.with("W3 done") != "" })
	if line := told.with("W3 done"); !strings.Contains(line, "integration none: an investigation") {
		t.Errorf("ENDED line %q; want it to say integration none", line)
	}
	e.until(t, busy.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	// A work task's commit, in the same project, goes in line to be merged.
	commit(t, e.get(t, busy.GetId()).GetWorktree(), "work.txt")
	e.release(t, busy.GetId())
	if got := e.ended(t, busy.GetId()); got.GetIntegration() == nil {
		t.Errorf("busy ended %v with no integration; want its work in line", got.GetStatus())
	}
}

// commit commits a new file name in the worktree dir, as a worker does.
func commit(t *testing.T, dir, name string) {
	t.Helper()
	writeFile(t, dir, name, "measured\n")
	for _, args := range [][]string{{"add", "."}, {"commit", "--quiet", "-m", "Add " + name}} {
		if _, err := git(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
}

// TestInvestigationCap (W213): on a machine of three slots, one investigation runs at a time; the next waits for the
// machine, saying so, and starts once the first ended.
func TestInvestigationCap(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 3}).capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))
	inv := &planv1.TaskServiceSpawnRequest{Kind: planv1.TaskKind_TASK_KIND_INVESTIGATION}
	first := e.mustSpawn(t, wishID, "Audit the gates", "wait", inv)
	e.until(t, first.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	second := e.mustSpawn(t, wishID, "Audit the merges", "text audited",
		&planv1.TaskServiceSpawnRequest{Kind: planv1.TaskKind_TASK_KIND_INVESTIGATION})
	if second.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING ||
		!strings.Contains(second.GetWaitReason(), "the most investigations this machine runs at once, mockups counted (1 of 3 slots)") {
		t.Fatalf("second = %v; want it waiting for the cap", second)
	}
	work := e.mustSpawn(t, wishID, "Work", "text worked", nil)
	if e.ended(t, work.GetId()).GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Error("work does not start beside the capped investigation")
	}
	e.release(t, first.GetId())
	e.ended(t, first.GetId())
	if got := e.ended(t, second.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("second ended %v; want it started once the first ended", got)
	}
}

// TestInvestigationAskedToPublish (W213): at its time limit, the Lamplighter asks an investigation to publish what it
// has, once, by a message it reads, and records when; one that goes on past the grace is stopped. At its budget, as
// its agent reports what it spent, it is asked the same.
func TestInvestigationAskedToPublish(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	clock := &fakeClock{at: time.Date(2026, 10, 17, 11, 0, 0, 0, time.UTC)}
	e := up(t, t.TempDir(), WithClock(clock.now), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))
	spawn := func(title, script string, minutes uint32) (*planv1.Task, func(string, func(*planv1.TaskEvent) bool) *planv1.Task) {
		task := e.mustSpawn(t, wishID, title, script,
			&planv1.TaskServiceSpawnRequest{Kind: planv1.TaskKind_TASK_KIND_INVESTIGATION, Minutes: minutes})
		return task, e.follow(t, task.GetId())
	}
	asked := func(ev *planv1.TaskEvent) bool {
		return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_MESSAGE && strings.Contains(ev.GetText(), "Publish what you have")
	}

	// Its time limit: asked, it publishes and ends done.
	study, follow := spawn("Study", "text studying\nwait\ntext published", 10)
	follow("it studies", said("studying"))
	clock.set(clock.now().Add(9 * time.Minute))
	e.h.Wake()
	clock.set(clock.now().Add(2 * time.Minute))
	e.h.Wake()
	follow("it is asked to publish", asked)
	follow("it publishes", said("published"))
	done := e.ended(t, study.GetId())
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || done.GetInvestigation().GetPublishWhy() != "its time limit, 10m0s" ||
		!done.GetInvestigation().GetPublishTime().AsTime().Equal(clock.now()) {
		t.Errorf("study ended %v, %v; want done, asked at its time limit", done.GetStatus(), done.GetInvestigation())
	}

	// One that goes on past the grace is stopped.
	stays, follow := spawn("Stays", "text studying\nwait\ntext going on\nsleep 1h", 5)
	follow("it studies", said("studying"))
	clock.set(clock.now().Add(6 * time.Minute))
	e.h.Wake()
	follow("it goes on", said("going on"))
	e.h.Wake() // Within the grace: asked once, never stopped.
	clock.set(clock.now().Add(investigationGrace))
	e.h.Wake()
	stopped := e.ended(t, stays.GetId())
	if stopped.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED ||
		!strings.HasPrefix(stopped.GetError(), "investigation stopped: asked to publish at its time limit") {
		t.Errorf("stays ended %v: %q; want stopped past the grace", stopped.GetStatus(), stopped.GetError())
	}
	n := 0
	for _, ev := range e.storedEvents(t, stays.GetId()) {
		if asked(ev) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("asked %d times; want once", n)
	}

	// Its budget, as its agent reports what it spent.
	costly, follow := spawn("Costly", "usage 1000 100 12.5\nwait\ntext published", 0)
	follow("it spent", saidUsage)
	e.h.Wake()
	follow("it is asked to publish", asked)
	if got := e.ended(t, costly.GetId()); got.GetInvestigation().GetPublishWhy() != "its budget, $10.00" {
		t.Errorf("costly asked for %q; want its budget", got.GetInvestigation().GetPublishWhy())
	}
}

// TestInvestigationSpawnRefusals (W213): an investigation works in a project in Git and is served first already; its
// time limit is for it alone.
func TestInvestigationSpawnRefusals(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir(), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))
	for name, c := range map[string]struct {
		req  *planv1.TaskServiceSpawnRequest
		want string
	}{
		"urgent":    {&planv1.TaskServiceSpawnRequest{Kind: planv1.TaskKind_TASK_KIND_INVESTIGATION, Urgent: true}, "served first already"},
		"feedback":  {&planv1.TaskServiceSpawnRequest{Kind: planv1.TaskKind_TASK_KIND_INVESTIGATION, Feedback: true}, "served first already"},
		"minutes":   {&planv1.TaskServiceSpawnRequest{Minutes: 30}, "--minutes is for an investigation"},
		"a watcher": {&planv1.TaskServiceSpawnRequest{Kind: planv1.TaskKind_TASK_KIND_INVESTIGATION, Provider: planv1.Provider_PROVIDER_WATCH}, "a watcher runs a command"},
	} {
		if _, err := e.spawnReq(t, wishID, "Study", "text x", c.req); connect.CodeOf(err) != connect.CodeInvalidArgument ||
			!strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v; want %q", name, err, c.want)
		}
	}
	outside, _ := e.wish(t, t.TempDir())
	if _, err := e.spawnReq(t, outside, "Study", "text x", &planv1.TaskServiceSpawnRequest{Kind: planv1.TaskKind_TASK_KIND_INVESTIGATION}); err == nil ||
		!strings.Contains(err.Error(), "a project in Git") {
		t.Errorf("outside Git: %v", err)
	}
}

// TestUrgentFeedbackBesideRepair (W213): a task for the developer's quick feedback (a mockup) may be urgent while a
// repair is: one of each class per wish, a second of either class refused naming the first.
func TestUrgentFeedbackBesideRepair(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 1}).capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))
	repair := e.mustSpawn(t, wishID, "Repair the branch", "wait", &planv1.TaskServiceSpawnRequest{Urgent: true})
	mockup := e.mustSpawn(t, wishID, "Mockup the empty states", "wait", &planv1.TaskServiceSpawnRequest{Feedback: true})
	if !mockup.GetUrgent() || !mockup.GetFeedback() || mockup.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("mockup = %v; want it urgent for feedback and running beside the repair", mockup)
	}
	if _, err := e.spawnReq(t, wishID, "Another mockup", "text x", &planv1.TaskServiceSpawnRequest{Feedback: true}); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "W2 (Mockup the empty states) is urgent for feedback already") {
		t.Errorf("second feedback: %v", err)
	}
	if _, err := e.spawnReq(t, wishID, "Another repair", "text x", &planv1.TaskServiceSpawnRequest{Urgent: true}); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "W1 (Repair the branch) is urgent already") {
		t.Errorf("second repair: %v", err)
	}
	e.release(t, repair.GetId())
	e.release(t, mockup.GetId())
	e.ended(t, repair.GetId())
	e.ended(t, mockup.GetId())
}
