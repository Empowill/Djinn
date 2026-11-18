package harness

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

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

// TestFreeAge (Q75): a task done or cancelled 31 days ago loses its worktree and scratch, its branch kept; one done 29
// days ago keeps them.
func TestFreeAge(t *testing.T) {
	t.Parallel()
	e := newFreeEnv(t, planv1.WishState_WISH_STATE_ACTIVE)
	day := 24 * time.Hour
	young := e.task(t, "W1", planv1.TaskStatus_TASK_STATUS_DONE, 29*day, nil)
	old := e.task(t, "W2", planv1.TaskStatus_TASK_STATUS_DONE, 31*day, nil)
	cancelled := e.task(t, "W3", planv1.TaskStatus_TASK_STATUS_CANCELLED, 31*day, nil)
	failed := e.task(t, "W4", planv1.TaskStatus_TASK_STATUS_FAILED, 31*day, nil)

	e.h.freePass(t.Context())

	for _, kept := range []*planv1.Task{young, failed} {
		if !exists(kept.GetWorktree()) || !exists(kept.GetScratch()) {
			t.Errorf("%s lost its folders", kept.GetCode())
		}
		if got := e.get(t, kept); got.GetFreed() != nil || got.GetWorktree() == "" {
			t.Errorf("%s recorded freed: %v", kept.GetCode(), got.GetFreed())
		}
	}
	for _, gone := range []*planv1.Task{old, cancelled} {
		if exists(gone.GetWorktree()) || exists(gone.GetScratch()) {
			t.Errorf("%s keeps its folders", gone.GetCode())
		}
		if !e.hasBranch(t, gone.GetBranch()) {
			t.Errorf("%s lost its branch", gone.GetCode())
		}
		got := e.get(t, gone)
		if got.GetWorktree() != "" || got.GetScratch() != "" {
			t.Errorf("%s still records worktree %q, scratch %q", gone.GetCode(), got.GetWorktree(), got.GetScratch())
		}
		if f := got.GetFreed(); f.GetRule() != planv1.FreeRule_FREE_RULE_AGE || f.GetBranchDeleted() {
			t.Errorf("%s freed = %v, want by age, branch kept", gone.GetCode(), f)
		}
		texts := e.statusTexts(t, gone)
		if len(texts) == 0 || !strings.Contains(texts[len(texts)-1], "worktree and scratch freed; branch "+gone.GetBranch()+" kept (it ended 30 days ago)") {
			t.Errorf("%s events = %q", gone.GetCode(), texts)
		}
	}
	p := e.h.Freeing()
	if p.GetTotal() != 2 || p.GetDone() != 2 || p.GetKept() != 0 || p.GetFreedBytes() == 0 || p.GetEndTime() == nil {
		t.Errorf("progress = %v, want 2 of 2 done, bytes freed, ended", p)
	}

	// The young one turns 30 days old.
	e.clock.set(e.clock.now().Add(2 * day))
	e.h.freePass(t.Context())
	if exists(young.GetWorktree()) {
		t.Error("W1 keeps its worktree past 30 days")
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
	p := e.h.Freeing()
	if p.GetTotal() != 4 || p.GetDone() != 4 || p.GetKept() != 1 || len(p.GetWishIds()) != 1 || p.GetWishIds()[0] != e.wish.GetId() {
		t.Errorf("progress = %v, want 4 done, 1 kept, the wish named", p)
	}
	if a := p.GetAzimas(); len(a) != 2 || a[0].GetTotal() != 2 || a[1].GetTotal() != 2 || a[1].GetKept() != 1 {
		t.Errorf("progress by azima = %v", a)
	}
}

// TestFreedContinueSaysFork (Q75): continuing a task Djinn freed is refused with the reason and the fork to run.
func TestFreedContinueSaysFork(t *testing.T) {
	t.Parallel()
	e := newFreeEnv(t, planv1.WishState_WISH_STATE_ACTIVE)
	task := e.task(t, "W1", planv1.TaskStatus_TASK_STATUS_DONE, 31*24*time.Hour, nil)
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
