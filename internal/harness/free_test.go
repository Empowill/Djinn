package harness

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// freeing is a harness without a scheduler on a project in a Git repository, its clock the test's, to free by hand.
type freeEnv struct {
	h       *Harness
	db      *store.Store
	home    string
	repo    string
	clock   *fakeClock
	wish    *planv1.Wish
	project *planv1.Project
}

func newFreeEnv(t *testing.T, state planv1.WishState) *freeEnv {
	t.Helper()
	home := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clock := &fakeClock{at: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
	h := New(db, home, testProviders(), WithClock(clock.now), WithFreeEvery(0))
	t.Cleanup(h.Close)
	e := &freeEnv{h: h, db: db, home: home, repo: gitRepo(t), clock: clock}
	e.project = &planv1.Project{Id: store.NewID(), Name: "app", Directory: e.repo, Git: true}
	e.wish = &planv1.Wish{Id: store.NewID(), Title: "Payroll", State: state, ProjectIds: []string{e.project.GetId()}}
	if err := db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "put", e.wish); err != nil {
			return err
		}
		if err := tx.Put(e.project); err != nil {
			return err
		}
		return tx.Put(e.wish)
	}); err != nil {
		t.Fatal(err)
	}
	return e
}

// task puts a finished task of the wish, ended ago, with a worktree on its branch holding one commit, and a scratch
// folder holding a file; mod changes it before it is stored.
func (e *freeEnv) task(t *testing.T, code string, status planv1.TaskStatus, ago time.Duration, mod func(*planv1.Task)) *planv1.Task {
	t.Helper()
	id := store.NewID()
	branch := strings.ToLower(code) + "-" + id[len(id)-8:]
	wt := worktreeDir(e.home, e.project.GetId(), id)
	if _, err := addWorktreeFrom(t.Context(), e.repo, wt, branch, "HEAD"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, wt, code+".txt", code)
	for _, args := range [][]string{{"add", "."}, {"commit", "--quiet", "-m", code}} {
		if _, err := git(t.Context(), wt, args...); err != nil {
			t.Fatal(err)
		}
	}
	scratch := workScratchDir(e.home, e.project.GetId(), id)
	writeFile(t, scratch, "notes.txt", "scratch of "+code)
	task := &planv1.Task{
		Id: id, Code: code, Title: code, WishId: e.wish.GetId(), ProjectId: e.project.GetId(), Status: status,
		Worktree: wt, Scratch: scratch, Branch: branch, Scheduled: true,
		EndTime: timestamppb.New(e.clock.now().Add(-ago)),
	}
	if status == planv1.TaskStatus_TASK_STATUS_CANCELLED {
		task.Cancelled = &planv1.Cancellation{Actor: planv1.Closer_CLOSER_LEAD, CreateTime: task.GetEndTime()}
	}
	if mod != nil {
		mod(task)
	}
	putTaskInDB(t, e.db, task)
	return task
}

// azima puts an azima of the wish; mod changes it before it is stored.
func (e *freeEnv) azima(t *testing.T, code string, mod func(*planv1.Task)) *planv1.Task {
	t.Helper()
	a := &planv1.Task{
		Id: store.NewID(), Code: code, Title: code, WishId: e.wish.GetId(), ProjectId: e.project.GetId(),
		Kind: planv1.TaskKind_TASK_KIND_AZIMA, Status: planv1.TaskStatus_TASK_STATUS_PENDING, Azima: &planv1.Azima{},
	}
	if mod != nil {
		mod(a)
	}
	putTaskInDB(t, e.db, a)
	return a
}

// integrate makes branch integration point at task's branch: its work merged there.
func (e *freeEnv) integrate(t *testing.T, integration string, tasks ...*planv1.Task) {
	t.Helper()
	for _, task := range tasks {
		if _, err := git(t.Context(), e.repo, "branch", "-f", integration, task.GetBranch()); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *freeEnv) get(t *testing.T, task *planv1.Task) *planv1.Task {
	t.Helper()
	got, err := store.Get[*planv1.Task](t.Context(), e.db, task.GetId())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (e *freeEnv) hasBranch(t *testing.T, branch string) bool {
	t.Helper()
	out, err := git(t.Context(), e.repo, "branch", "--list", branch)
	if err != nil {
		t.Fatal(err)
	}
	return out != ""
}

// statusTexts are the texts of the status events of task.
func (e *freeEnv) statusTexts(t *testing.T, task *planv1.Task) []string {
	t.Helper()
	events, err := store.List[*planv1.TaskEvent](t.Context(), e.db, store.Where{"task_id": task.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ev := range events {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS {
			out = append(out, ev.GetText())
		}
	}
	return out
}

// committed marks a task done with its work committed into the integration branch.
func committed(task *planv1.Task) {
	task.Integration = &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, Branch: "djinn/payroll"}
}

// dropped marks a task done with its work dropped (done --drop): no integration left, closed with why.
func dropped(task *planv1.Task) {
	task.Closed = &planv1.Closure{Actor: planv1.Closer_CLOSER_LEAD, CreateTime: task.GetEndTime(), Note: "not needed"}
}

// TestFreeAge (Q75, Q81): a task done 31 days ago with its work integrated loses its worktree and scratch, its branch
// kept; one done 29 days ago keeps them, until it turns 30 days old.
func TestFreeAge(t *testing.T) {
	t.Parallel()
	e := newFreeEnv(t, planv1.WishState_WISH_STATE_ACTIVE)
	day := 24 * time.Hour
	young := e.task(t, "W1", planv1.TaskStatus_TASK_STATUS_DONE, 29*day, committed)
	old := e.task(t, "W2", planv1.TaskStatus_TASK_STATUS_DONE, 31*day, committed)

	e.h.freePass(t.Context())

	if !exists(young.GetWorktree()) || !exists(young.GetScratch()) {
		t.Error("W1 lost its folders at 29 days")
	}
	if got := e.get(t, young); got.GetFreed() != nil || got.GetWorktree() == "" {
		t.Errorf("W1 recorded freed: %v", got.GetFreed())
	}
	if exists(old.GetWorktree()) || exists(old.GetScratch()) {
		t.Error("W2 keeps its folders")
	}
	if !e.hasBranch(t, old.GetBranch()) {
		t.Error("W2 lost its branch")
	}
	got := e.get(t, old)
	if got.GetWorktree() != "" || got.GetScratch() != "" {
		t.Errorf("W2 still records worktree %q, scratch %q", got.GetWorktree(), got.GetScratch())
	}
	if f := got.GetFreed(); f.GetRule() != planv1.FreeRule_FREE_RULE_AGE || f.GetBranchDeleted() {
		t.Errorf("W2 freed = %v, want by age, branch kept", f)
	}
	texts := e.statusTexts(t, old)
	if len(texts) == 0 || !strings.Contains(texts[len(texts)-1],
		"worktree and scratch freed; branch "+old.GetBranch()+" kept (it ended 30 days ago, its work integrated)") {
		t.Errorf("W2 events = %q", texts)
	}
	p := e.h.Freeing()
	if p.GetTotal() != 1 || p.GetDone() != 1 || p.GetKept() != 0 || p.GetFreedBytes() == 0 || p.GetEndTime() == nil {
		t.Errorf("progress = %v, want 1 of 1 done, bytes freed, ended", p)
	}

	// The young one turns 30 days old.
	e.clock.set(e.clock.now().Add(2 * day))
	e.h.freePass(t.Context())
	if exists(young.GetWorktree()) {
		t.Error("W1 keeps its worktree past 30 days")
	}
}

// TestFreeAgeOneStatus (Q81 A): a task cancelled, done with its work dropped or left unmerged, failed or stopped is not
// freed by its age: it keeps its folders at 30 days and long after, and each is freed once its azima is validated.
func TestFreeAgeOneStatus(t *testing.T) {
	t.Parallel()
	day := 24 * time.Hour
	for _, c := range []struct {
		name   string
		status planv1.TaskStatus
		mod    func(*planv1.Task)
	}{
		{"cancelled", planv1.TaskStatus_TASK_STATUS_CANCELLED, nil},
		{"cancelled integrated", planv1.TaskStatus_TASK_STATUS_CANCELLED, committed},
		{"done dropped", planv1.TaskStatus_TASK_STATUS_DONE, dropped},
		{"done by hand, nothing integrated", planv1.TaskStatus_TASK_STATUS_DONE, func(task *planv1.Task) {
			task.Closed = &planv1.Closure{Actor: planv1.Closer_CLOSER_DEVELOPER, CreateTime: task.GetEndTime()}
		}},
		{"done with problems", planv1.TaskStatus_TASK_STATUS_DONE, func(task *planv1.Task) {
			task.Refused = &planv1.TaskRefusals{Count: 1}
		}},
		{"done red", planv1.TaskStatus_TASK_STATUS_DONE, func(task *planv1.Task) {
			task.Integration = &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_RED, Branch: "djinn/payroll"}
		}},
		{"failed", planv1.TaskStatus_TASK_STATUS_FAILED, nil},
		{"stopped", planv1.TaskStatus_TASK_STATUS_STOPPED, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newFreeEnv(t, planv1.WishState_WISH_STATE_ACTIVE)
			a := e.azima(t, "T1", func(a *planv1.Task) { a.Azima.Branch = "djinn/payroll" })
			task := e.task(t, "W1", c.status, 31*day, func(task *planv1.Task) {
				task.PartOf = a.GetId()
				if c.mod != nil {
					c.mod(task)
				}
			})
			e.integrate(t, "djinn/payroll", task) // Its commits are in its azima's branch: only the rule keeps it.

			e.h.freePass(t.Context())
			e.clock.set(e.clock.now().Add(365 * day))
			e.h.freePass(t.Context())

			if !exists(task.GetWorktree()) || !exists(task.GetScratch()) || !e.hasBranch(t, task.GetBranch()) {
				t.Fatalf("%s freed by its age", c.name)
			}
			if got := e.get(t, task); got.GetFreed() != nil {
				t.Fatalf("%s recorded freed: %v", c.name, got.GetFreed())
			}

			a.Azima.Merged = &planv1.AzimaMerged{Time: timestamppb.New(e.clock.now()), MainBranch: "main"}
			putTaskInDB(t, e.db, a)
			e.h.freePass(t.Context())

			if exists(task.GetWorktree()) || exists(task.GetScratch()) || e.hasBranch(t, task.GetBranch()) {
				t.Errorf("%s of a validated azima keeps its worktree, scratch or branch", c.name)
			}
			if f := e.get(t, task).GetFreed(); f.GetRule() != planv1.FreeRule_FREE_RULE_AZIMA || !f.GetBranchDeleted() {
				t.Errorf("%s freed = %v, want by its azima, branch deleted", c.name, f)
			}
		})
	}
}

// TestFreeAgeNothingToMerge (Q84 B): a task done with nothing to merge, its worker ended done with no commit of its own
// or an investigation whose commits are never merged, is freed at 30 days as one merged: its worktree and scratch go,
// its branch stays. One cancelled, or done with its work dropped, with no commit either, keeps them (Q81 A).
func TestFreeAgeNothingToMerge(t *testing.T) {
	t.Parallel()
	e := newFreeEnv(t, planv1.WishState_WISH_STATE_ACTIVE)
	day := 24 * time.Hour
	noCommit := func(mod func(*planv1.Task)) func(*planv1.Task) {
		return func(task *planv1.Task) {
			if _, err := git(t.Context(), task.GetWorktree(), "reset", "--quiet", "--hard", "HEAD~1"); err != nil {
				t.Fatal(err)
			}
			if mod != nil {
				mod(task)
			}
		}
	}
	empty := e.task(t, "W1", planv1.TaskStatus_TASK_STATUS_DONE, 31*day, noCommit(nil))
	young := e.task(t, "W2", planv1.TaskStatus_TASK_STATUS_DONE, 29*day, noCommit(nil))
	investigation := e.task(t, "W3", planv1.TaskStatus_TASK_STATUS_DONE, 31*day, func(task *planv1.Task) {
		task.Kind = planv1.TaskKind_TASK_KIND_INVESTIGATION
	})
	cancelled := e.task(t, "W4", planv1.TaskStatus_TASK_STATUS_CANCELLED, 31*day, noCommit(nil))
	drop := e.task(t, "W5", planv1.TaskStatus_TASK_STATUS_DONE, 31*day, noCommit(dropped))

	e.h.freePass(t.Context())

	for _, task := range []*planv1.Task{empty, investigation} {
		if exists(task.GetWorktree()) || exists(task.GetScratch()) {
			t.Errorf("%s keeps its folders past 30 days", task.GetCode())
		}
		if !e.hasBranch(t, task.GetBranch()) {
			t.Errorf("%s lost its branch", task.GetCode())
		}
		if f := e.get(t, task).GetFreed(); f.GetRule() != planv1.FreeRule_FREE_RULE_AGE || f.GetBranchDeleted() {
			t.Errorf("%s freed = %v, want by age, branch kept", task.GetCode(), f)
		}
		texts := e.statusTexts(t, task)
		if len(texts) == 0 || !strings.Contains(texts[len(texts)-1], "(it ended 30 days ago, nothing to merge)") {
			t.Errorf("%s events = %q", task.GetCode(), texts)
		}
	}
	for _, task := range []*planv1.Task{young, cancelled, drop} {
		if !exists(task.GetWorktree()) || !exists(task.GetScratch()) {
			t.Errorf("%s lost its folders", task.GetCode())
		}
		if got := e.get(t, task); got.GetFreed() != nil {
			t.Errorf("%s recorded freed: %v", task.GetCode(), got.GetFreed())
		}
	}
}

// TestFreeAzimaValidated (Q75): the parts of an azima merged, or closed as done by the developer, lose their worktree,
// scratch and local branch at once; a branch with commits found neither in the integration branch nor on the remote
// stays, with its worktree, and the azima's events say so; a branch on the remote goes locally only.
func TestFreeAzimaValidated(t *testing.T) {
	t.Parallel()
	e := newFreeEnv(t, planv1.WishState_WISH_STATE_ACTIVE)
	bare := filepath.Join(t.TempDir(), "origin.git")
	if _, err := git(t.Context(), e.repo, "init", "--quiet", "--bare", bare); err != nil {
		t.Fatal(err)
	}
	if _, err := git(t.Context(), e.repo, "remote", "add", "origin", bare); err != nil {
		t.Fatal(err)
	}
	merged := e.azima(t, "T1", func(a *planv1.Task) {
		a.Azima.Merged = &planv1.AzimaMerged{Time: timestamppb.New(e.clock.now()), MainBranch: "main"}
	})
	closed := e.azima(t, "T2", func(a *planv1.Task) {
		a.Status = planv1.TaskStatus_TASK_STATUS_DONE
		a.Closed = &planv1.Closure{Actor: planv1.Closer_CLOSER_DEVELOPER, CreateTime: timestamppb.New(e.clock.now())}
	})
	open := e.azima(t, "T3", nil)
	part := func(code string, a *planv1.Task) *planv1.Task {
		return e.task(t, code, planv1.TaskStatus_TASK_STATUS_DONE, time.Hour, func(task *planv1.Task) {
			task.PartOf = a.GetId()
			task.Integration = &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, Branch: "djinn/payroll"}
		})
	}
	w1 := part("W1", merged)
	w2 := part("W2", merged) // Never merged nor pushed: kept.
	w3 := part("W3", closed) // Pushed, not merged: its local branch goes, the remote's stays.
	w4 := part("W4", open)
	e.integrate(t, "djinn/payroll", w1)
	if _, err := git(t.Context(), e.repo, "push", "--quiet", "origin", w3.GetBranch()); err != nil {
		t.Fatal(err)
	}

	e.h.freePass(t.Context())

	for _, gone := range []*planv1.Task{w1, w3} {
		if exists(gone.GetWorktree()) || exists(gone.GetScratch()) || e.hasBranch(t, gone.GetBranch()) {
			t.Errorf("%s keeps its worktree, scratch or branch", gone.GetCode())
		}
		if f := e.get(t, gone).GetFreed(); f.GetRule() != planv1.FreeRule_FREE_RULE_AZIMA || !f.GetBranchDeleted() {
			t.Errorf("%s freed = %v, want by its azima, branch deleted", gone.GetCode(), f)
		}
	}
	if out, _ := git(t.Context(), bare, "branch", "--list", w3.GetBranch()); out == "" {
		t.Error("the remote's branch of W3 was deleted")
	}
	if !exists(w2.GetWorktree()) || !exists(w2.GetScratch()) || !e.hasBranch(t, w2.GetBranch()) {
		t.Error("W2 lost work: its worktree, scratch or branch is gone")
	}
	if e.get(t, w2).GetFreed() != nil {
		t.Error("W2 recorded freed")
	}
	if !exists(w4.GetWorktree()) || !e.hasBranch(t, w4.GetBranch()) {
		t.Error("W4, of an open azima, was freed")
	}
	texts := e.statusTexts(t, merged)
	want := []string{"freeing its tasks' folders: 1/2", "freeing its tasks' folders: 2/2"}
	if len(texts) != 2 || !strings.HasPrefix(texts[0], want[0]) || !strings.HasPrefix(texts[1], want[1]) {
		t.Fatalf("T1 events = %q, want two progress lines", texts)
	}
	if !strings.Contains(texts[0], "W1 freed") || !strings.Contains(texts[1], "1 kept; W2 worktree kept: 1 commits found neither in djinn/payroll nor on the remote") {
		t.Errorf("T1 events = %q", texts)
	}
	p := e.h.Freeing()
	if len(p.GetAzimas()) != 2 || p.GetAzimas()[0].GetAzimaId() != merged.GetId() || p.GetAzimas()[0].GetKept() != 1 ||
		p.GetAzimas()[1].GetDone() != 1 || p.GetKept() != 1 {
		t.Errorf("progress = %v", p)
	}
	wantKept := &machinev1.DiskFreeingKept{TaskId: w2.GetId(), Part: machinev1.DiskFreeingPart_DISK_FREEING_PART_WORKTREE,
		Reason: machinev1.DiskFreeingReason_DISK_FREEING_REASON_UNPUSHED, Commits: 1,
		Detail: "1 commits found neither in djinn/payroll nor on the remote"}
	if k := p.GetAzimas()[0].GetKeptItems(); len(k) != 1 || !proto.Equal(k[0], wantKept) {
		t.Errorf("T1 kept = %v, want W2's worktree, 1 commit not pushed", k)
	}
	if k := p.GetAzimas()[1].GetKeptItems(); len(k) != 0 {
		t.Errorf("T2 kept = %v, want none", k)
	}

	// A pass right after looks at nothing again: W2 waits freeKeptAgain.
	e.h.freePass(t.Context())
	if got := e.statusTexts(t, merged); len(got) != 2 {
		t.Errorf("a second pass wrote again on T1: %q", got)
	}
}

// TestFreeGrantCascade (Q75): a wish granted frees every task of it at once, azima validated or not, and the progress
// names the wish; a worktree with changes not committed stays, with its scratch and branch, said.
func TestFreeGrantCascade(t *testing.T) {
	t.Parallel()
	e := newFreeEnv(t, planv1.WishState_WISH_STATE_GRANTED)
	a1, a2 := e.azima(t, "T1", nil), e.azima(t, "T2", nil)
	var tasks []*planv1.Task
	for i, a := range []*planv1.Task{a1, a1, a2} {
		tasks = append(tasks, e.task(t, "W"+string(rune('1'+i)), planv1.TaskStatus_TASK_STATUS_DONE, time.Minute, func(task *planv1.Task) {
			task.PartOf = a.GetId()
			task.Integration = &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, Branch: "djinn/payroll"}
		}))
	}
	dirty := e.task(t, "W9", planv1.TaskStatus_TASK_STATUS_STOPPED, time.Minute, func(task *planv1.Task) { task.PartOf = a2.GetId() })
	writeFile(t, dirty.GetWorktree(), "draft.txt", "not committed")
	// W8 is part of no azima, its worktree gone already: its scratch goes, its branch, never pushed, stays.
	unpushed := e.task(t, "W8", planv1.TaskStatus_TASK_STATUS_DONE, time.Minute, nil)
	if err := removeWorktree(t.Context(), e.repo, unpushed.GetWorktree(), false); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks { // Merged into the project's main: none holds work any more.
		if _, err := git(t.Context(), e.repo, "merge", "--quiet", "--no-ff", "--no-edit", task.GetBranch()); err != nil {
			t.Fatal(err)
		}
	}
	e.h.Free() // The grant's kick: no loop here, the pass runs by hand.
	e.h.freePass(t.Context())

	for _, gone := range tasks {
		if exists(gone.GetWorktree()) || exists(gone.GetScratch()) || e.hasBranch(t, gone.GetBranch()) {
			t.Errorf("%s keeps its worktree, scratch or branch", gone.GetCode())
		}
		if f := e.get(t, gone).GetFreed(); f.GetRule() != planv1.FreeRule_FREE_RULE_GRANT || !f.GetBranchDeleted() {
			t.Errorf("%s freed = %v, want by the grant", gone.GetCode(), f)
		}
	}
	if !exists(dirty.GetWorktree()) || !exists(dirty.GetScratch()) || !e.hasBranch(t, dirty.GetBranch()) {
		t.Error("W9 lost its changes not committed")
	}
	if texts := e.statusTexts(t, dirty); len(texts) != 1 || !strings.Contains(texts[0], "worktree kept: it holds changes not committed") {
		t.Errorf("W9 events = %q", texts)
	}
	if exists(unpushed.GetScratch()) || !e.hasBranch(t, unpushed.GetBranch()) {
		t.Error("W8 keeps its scratch, or lost its branch not pushed")
	}
	p := e.h.Freeing()
	if p.GetTotal() != 5 || p.GetDone() != 5 || p.GetKept() != 2 || len(p.GetWishIds()) != 1 || p.GetWishIds()[0] != e.wish.GetId() {
		t.Errorf("progress = %v, want 5 done, 2 kept, the wish named", p)
	}
	a := p.GetAzimas()
	if len(a) != 3 || a[0].GetAzimaId() != "" || a[0].GetWishId() != e.wish.GetId() || a[0].GetTotal() != 1 ||
		a[1].GetAzimaId() != a1.GetId() || a[1].GetTotal() != 2 || a[2].GetTotal() != 2 || a[2].GetKept() != 1 {
		t.Fatalf("progress by azima = %v, want the tasks of no azima first, then T1 and T2", a)
	}
	if a[0].GetFreedBytes() == 0 || a[1].GetFreedBytes() == 0 {
		t.Errorf("freed bytes = %d, %d, want some", a[0].GetFreedBytes(), a[1].GetFreedBytes())
	}
	if k := a[0].GetKeptItems(); len(k) != 1 || k[0].GetTaskId() != unpushed.GetId() ||
		k[0].GetPart() != machinev1.DiskFreeingPart_DISK_FREEING_PART_BRANCH ||
		k[0].GetReason() != machinev1.DiskFreeingReason_DISK_FREEING_REASON_UNPUSHED || k[0].GetCommits() != 1 {
		t.Errorf("W8 kept = %v, want its branch, 1 commit not pushed", k)
	}
	if k := a[2].GetKeptItems(); len(k) != 1 || k[0].GetTaskId() != dirty.GetId() ||
		k[0].GetPart() != machinev1.DiskFreeingPart_DISK_FREEING_PART_WORKTREE ||
		k[0].GetReason() != machinev1.DiskFreeingReason_DISK_FREEING_REASON_UNCOMMITTED {
		t.Errorf("W9 kept = %v, want its worktree, changes not committed", k)
	}
}

// TestFreedContinueSaysFork (Q75): continuing a task Djinn freed is refused with the reason and the fork to run.
func TestFreedContinueSaysFork(t *testing.T) {
	t.Parallel()
	e := newFreeEnv(t, planv1.WishState_WISH_STATE_ACTIVE)
	task := e.task(t, "W1", planv1.TaskStatus_TASK_STATUS_DONE, 31*24*time.Hour, committed)
	e.h.freePass(t.Context())
	got := e.get(t, task)
	gone := connect.NewError(connect.CodeFailedPrecondition, &worktreeGoneError{task: got, why: "has no worktree any more"})
	err := e.h.forkInstead(t.Context(), gone, got, "")
	for _, want := range []string{"Djinn freed it once the task was finished, 30 days after it ended", "--fork W1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal = %q, want %q", err, want)
		}
	}
}

// TestFreeOnGrant (Q75): granting a wish kicks the freeing loop, which frees its tasks at once, without its tick.
func TestFreeOnGrant(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	e := up(t, t.TempDir(), WithFreeEvery(time.Hour))
	wishID, projectID := e.wish(t, repo)
	id := store.NewID()
	wt := worktreeDir(e.home, projectID, id)
	if _, err := addWorktreeFrom(t.Context(), repo, wt, "w1-"+id[len(id)-8:], "HEAD"); err != nil {
		t.Fatal(err)
	}
	putTaskInDB(t, e.db, &planv1.Task{
		Id: id, Code: "W1", Title: "W1", WishId: wishID, ProjectId: projectID, Status: planv1.TaskStatus_TASK_STATUS_DONE,
		Worktree: wt, Branch: "w1-" + id[len(id)-8:], Scheduled: true, EndTime: timestamppb.Now(),
	})
	if _, err := e.wishes.Grant(t.Context(), connect.NewRequest(&planv1.WishServiceGrantRequest{WishId: wishID})); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "W1's worktree freed", func() bool { return !exists(wt) })
	waitFor(t, "W1 recorded freed", func() bool {
		got, err := store.Get[*planv1.Task](t.Context(), e.db, id)
		return err == nil && got.GetFreed().GetRule() == planv1.FreeRule_FREE_RULE_GRANT && got.GetFreed().GetBranchDeleted()
	})
}
