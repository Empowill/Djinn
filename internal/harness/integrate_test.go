package harness

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// integration is djinn up with what integrating finished work needs in a test: a fake clock, a fake gen and a fake
// test command, and gates that say which were taken.
type integration struct {
	*env
	repo, wishID, projectID, branch string

	mu        sync.Mutex
	now       time.Time
	runs      []string // the commands run: "test in <dir>"
	gates     []string // the gates taken: "test W1"
	testCode  int      // what the test command exits with
	testOut   string
	afterTest func() // run once by the next test command, then forgotten
}

// integrating starts djinn up on a repository with app/README.md, app/gen/index.txt and app/src, a wish on it whose
// integration branch is the one its checkout is on, and project settings that name gen/** as generated, "gen" as
// the command that makes it, "test" as the test command. opts are djinn up's options beside these.
func integrating(t *testing.T, opts ...Option) *integration {
	t.Helper()
	in := &integration{repo: gitRepo(t), now: time.Now()}
	for _, args := range [][]string{{"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}} {
		if _, err := git(t.Context(), in.repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, in.repo, "app/gen/index.txt", "\n")
	writeFile(t, in.repo, "app/src/.keep", "")
	in.git(t, in.repo, "add", ".")
	in.git(t, in.repo, "commit", "--quiet", "-m", "Generated files")
	in.env = up(t, t.TempDir(), append([]Option{WithClock(in.clock), WithCommands(in.run), WithGates(in.gate)}, opts...)...)
	in.wishID, in.projectID = in.wish(t, filepath.Join(in.repo, "app"))
	in.branch = plan.CheckedOutBranch(t.Context(), in.repo)
	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
		"generated: \"gen/**\"\ngenerate: \"gen\"\ntest: \"test\"\n")
	return in
}

func (in *integration) clock() time.Time {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.now
}

// pass moves the clock by d, then lets the integration make a pass.
func (in *integration) pass(t *testing.T, d time.Duration) {
	t.Helper()
	in.mu.Lock()
	in.now = in.now.Add(d)
	in.mu.Unlock()
	in.h.integratePass(t.Context())
}

// run is the fake of the project's commands: gen lists the files of src in gen/index.txt, test says testOut and
// exits testCode.
func (in *integration) run(_ context.Context, dir string, args []string) (string, int, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.runs = append(in.runs, strings.Join(args, " ")+" in "+dir)
	switch args[0] {
	case "gen":
		entries, err := os.ReadDir(filepath.Join(dir, "src"))
		if err != nil {
			return "", -1, err
		}
		var names []string
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ".") {
				names = append(names, e.Name())
			}
		}
		return "", 0, os.WriteFile(filepath.Join(dir, "gen", "index.txt"), []byte(strings.Join(names, "\n")+"\n"), 0o600)
	case "test":
		if f := in.afterTest; f != nil {
			in.afterTest = nil
			f()
		}
		return in.testOut, in.testCode, nil
	case "install":
		b, err := os.ReadFile(filepath.Join(dir, "src", "a.txt"))
		return "installed " + string(b), 0, err
	}
	return "unknown command", 127, nil
}

func (in *integration) gate(ctx context.Context, name, taskID, what, dir string) (func(), error) {
	code := "-" // Taken for no task.
	if taskID != "" {
		task, err := store.Get[*planv1.Task](ctx, in.db, taskID)
		if err != nil {
			return nil, err
		}
		code = task.GetCode()
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	in.gates = append(in.gates, name+" "+code)
	return func() {}, nil
}

func (in *integration) git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(t.Context(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// tip is where the integration branch is.
func (in *integration) tip(t *testing.T) string {
	t.Helper()
	return in.git(t, in.repo, "rev-parse", "refs/heads/"+in.branch)
}

// finished makes a task of the wish as its worker leaves it done: a worktree on a branch of its own from the
// repository's HEAD, files written and committed there with the task's title, its work waiting to be merged. Each
// finishes a second after the one before.
func (in *integration) finished(t *testing.T, code string, files map[string]string, opts ...func(*planv1.Task)) *planv1.Task {
	t.Helper()
	id := store.NewID()
	task := &planv1.Task{
		Id: id, WishId: in.wishID, ProjectId: in.projectID, Code: code, Title: "Work of " + code, Kind: planv1.TaskKind_TASK_KIND_WORK,
		Status: planv1.TaskStatus_TASK_STATUS_DONE, Provider: planv1.Provider_PROVIDER_FAKE, Scheduled: true,
		Branch: strings.ToLower(code) + "-" + id[len(id)-8:], Worktree: worktreeDir(in.home, in.projectID, id),
		CreateTime: timestamppb.New(in.clock()), StartTime: timestamppb.New(in.clock()), EndTime: timestamppb.New(in.clock()),
		Integration: &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_PENDING},
	}
	in.mu.Lock()
	in.now = in.now.Add(time.Second)
	in.mu.Unlock()
	for _, o := range opts {
		o(task)
	}
	if _, err := addWorktree(t.Context(), in.repo, task.GetWorktree(), task.GetBranch()); err != nil {
		t.Fatal(err)
	}
	if len(files) > 0 {
		in.leave(t, task, files)
		commitAll(t, task.GetWorktree(), task.GetTitle())
	}
	putTask(t, in.db, task, "work")
	return task
}

// leave writes files in task's worktree, as a worker leaves them: not committed.
func (in *integration) leave(t *testing.T, task *planv1.Task, files map[string]string) {
	t.Helper()
	for name, content := range files {
		writeFile(t, task.GetWorktree(), name, content)
	}
}

// commitAll commits everything the worktree wt holds, as a worker that commits its work does; a merge under way is
// concluded.
func commitAll(t *testing.T, wt, message string) {
	t.Helper()
	if _, err := git(t.Context(), wt, "add", "--all"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(t.Context(), wt, "commit", "--quiet", "-m", message); err != nil {
		t.Fatal(err)
	}
}

// events are the task's events, in their order.
func (in *integration) events(t *testing.T, task *planv1.Task) []*planv1.TaskEvent {
	t.Helper()
	events, err := store.List[*planv1.TaskEvent](t.Context(), in.db, store.Where{"task_id": task.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(events, func(a, b *planv1.TaskEvent) int { return int(a.GetSeq() - b.GetSeq()) })
	return events
}

// integration is where the task's work stands, and its events' texts about it.
func (in *integration) integration(t *testing.T, task *planv1.Task) (*planv1.TaskIntegration, []string) {
	t.Helper()
	got := in.get(t, task.GetId())
	var texts []string
	for _, e := range in.events(t, task) {
		if strings.HasPrefix(e.GetText(), "integration: ") {
			texts = append(texts, strings.TrimPrefix(e.GetText(), "integration: "))
		}
	}
	return got.GetIntegration(), texts
}

// commits are the batches the journal recorded as committed.
func (in *integration) commits(t *testing.T) []*planv1.IntegrationCommit {
	t.Helper()
	entries, err := store.Commands(t.Context(), in.db, func(c store.Command) bool { return c.Method == methodCommit })
	if err != nil {
		t.Fatal(err)
	}
	var out []*planv1.IntegrationCommit
	for _, e := range entries {
		c := &planv1.IntegrationCommit{}
		if err := proto.Unmarshal(e.Request, c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// state is the state of each task's integration, with its reason when it has one.
func (in *integration) states(t *testing.T, tasks ...*planv1.Task) string {
	t.Helper()
	var s []string
	for _, task := range tasks {
		got, _ := in.integration(t, task)
		state := strings.TrimPrefix(got.GetState().String(), "INTEGRATION_STATE_")
		s = append(s, task.GetCode()+" "+state)
	}
	return strings.Join(s, ", ")
}

// TestCommitEachTaskAlone: each task's work is committed into the integration branch as soon as it ends, alone, in
// the order the tasks ended: merged without fast-forward, tested once each, the journal recording each commit; its
// worktree is then removed, its branch kept.
func TestCommitEachTaskAlone(t *testing.T) {
	in := integrating(t)
	old := in.tip(t)
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
	w3 := in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})

	// The wish recorded, when it was made, the branch the project's checkout was on.
	wish, err := store.Get[*planv1.Wish](t.Context(), in.db, in.wishID)
	if err != nil || plan.IntegrationBranchOf(wish, in.projectID) != in.branch || in.branch == "" {
		t.Fatalf("the wish's integration branches: %v, %v; want %s", wish.GetIntegrationBranches(), err, in.branch)
	}

	in.pass(t, 0) // No wait: neither an hour nor a count.
	if got := in.states(t, w1, w2, w3); got != "W1 COMMITTED, W2 COMMITTED, W3 COMMITTED" {
		t.Fatalf("at once: %s", got)
	}
	tip := in.tip(t)
	commits := in.commits(t)
	if len(commits) != 3 || commits[0].GetOldSha() != old || commits[2].GetNewSha() != tip {
		t.Fatalf("journal: %v", commits)
	}
	for i, task := range []*planv1.Task{w1, w2, w3} {
		c := commits[i]
		if !slices.Equal(c.GetTaskIds(), []string{task.GetId()}) || i > 0 && c.GetOldSha() != commits[i-1].GetNewSha() {
			t.Errorf("commit %d: %v", i, c)
		}
		if got, _ := in.integration(t, task); got.GetSha() != c.GetNewSha() || got.GetBranch() != in.branch {
			t.Errorf("%s's integration %v; its commit %s", task.GetCode(), got, c.GetNewSha())
		}
	}
	got, texts := in.integration(t, w1)
	want := []string{
		"integrating into " + in.branch + ", with W1",
		"merged " + w1.GetBranch(),
		"testing W1: test",
		"committed into " + in.branch + " as " + got.GetSha()[:8] + ", with W1; your checkout of it, " + in.repo + ", follows",
	}
	if !slices.Equal(texts, want) {
		t.Errorf("W1's events:\n%s\nwant\n%s", strings.Join(texts, "\n"), strings.Join(want, "\n"))
	}
	// Each task's work, committed on its branch, then merged without fast-forward, in the order they ended.
	if out := in.git(t, in.repo, "log", "--format=%s", "--first-parent", old+".."+tip); out != strings.Join([]string{
		"Merge branch '" + w3.GetBranch() + "' into " + in.branch,
		"Merge branch '" + w2.GetBranch() + "' into " + in.branch,
		"Merge branch '" + w1.GetBranch() + "' into " + in.branch,
	}, "\n") {
		t.Errorf("the branch's history:\n%s", out)
	}
	if out := in.git(t, in.repo, "log", "--format=%s", "-1", w2.GetBranch()); out != "Work of W2" {
		t.Errorf("W2's branch ends with %q", out)
	}
	// The tests ran once per task, in the integration worktree, never in the person's checkout, under the test gate.
	wt := filepath.Join(integrationDir(in.home, in.projectID, in.wishID), "app")
	if !slices.Equal(in.runs, []string{"test in " + wt, "test in " + wt, "test in " + wt}) ||
		!slices.Equal(in.gates, []string{"test W1", "test W2", "test W3"}) {
		t.Errorf("runs %v, gates %v", in.runs, in.gates)
	}
	// The person's checkout was clean and on the branch: it follows, clean.
	if b, err := os.ReadFile(filepath.Join(in.repo, "app", "src", "c.txt")); err != nil || string(b) != "c\n" {
		t.Errorf("the checkout did not follow: %q, %v", b, err)
	}
	if out := in.git(t, in.repo, "status", "--porcelain"); out != "" {
		t.Errorf("the checkout is not clean: %q", out)
	}

	// Nothing waits any more: the next pass does nothing.
	in.pass(t, time.Hour)
	if len(in.runs) != 3 || len(in.commits(t)) != 3 {
		t.Errorf("a pass with nothing waiting ran %v", in.runs)
	}
}

// TestRemoveTheWorktreeOnceCommitted: once a task's work is committed, its worktree is removed, as djinn task clean
// does, its branch kept; a worktree that holds changes not committed stays, said.
func TestRemoveTheWorktreeOnceCommitted(t *testing.T) {
	in := integrating(t)
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.pass(t, 0)
	got := in.get(t, w1.GetId())
	if _, err := os.Stat(w1.GetWorktree()); !os.IsNotExist(err) || got.GetWorktree() != "" {
		t.Errorf("W1's worktree %q is still there: %v", got.GetWorktree(), err)
	}
	if b := in.git(t, in.repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+w1.GetBranch()); b == "" {
		t.Error("W1's branch is gone")
	}
	if texts := eventTexts(in.events(t, w1)); texts[len(texts)-1] != "worktree removed, branch "+w1.GetBranch()+" kept" {
		t.Errorf("W1's events %q", texts)
	}

	// A file written in the worktree after Djinn committed it, and before it removes it: the worktree stays.
	w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
	in.mu.Lock()
	in.afterTest = func() { writeFile(t, w2.GetWorktree(), "app/late.txt", "late\n") }
	in.mu.Unlock()
	in.pass(t, 0)
	if got := in.get(t, w2.GetId()); got.GetWorktree() != w2.GetWorktree() || in.states(t, w2) != "W2 COMMITTED" {
		t.Errorf("W2: worktree %q, %s", got.GetWorktree(), in.states(t, w2))
	}
	if texts := eventTexts(in.events(t, w2)); texts[len(texts)-1] != "worktree kept: it holds changes not committed, "+w2.GetWorktree() {
		t.Errorf("W2's events %q", texts)
	}
}

func TestIntegrateGeneratedConflict(t *testing.T) {
	in := integrating(t)
	old := in.tip(t)
	// Each worker made the generated index again on its own: the two branches change it differently.
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n", "app/gen/index.txt": "a.txt\n"})
	w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n", "app/gen/index.txt": "b.txt\n"})

	in.pass(t, 0)
	got, texts := in.integration(t, w2)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || got.GetSha() != in.tip(t) {
		t.Fatalf("W2's integration %v; events %q", got, texts)
	}
	if want := "merged " + w2.GetBranch() + "; its conflict, only in generated files (app/gen/index.txt), settled by gen"; !slices.Contains(texts, want) {
		t.Errorf("W2's events %q; want %q", texts, want)
	}
	if out := in.git(t, in.repo, "show", in.tip(t)+":app/gen/index.txt"); out != "a.txt\nb.txt" {
		t.Errorf("gen/index.txt = %q, made again from the sources", out)
	}
	wt := filepath.Join(integrationDir(in.home, in.projectID, in.wishID), "app")
	if !slices.Equal(in.runs, []string{"test in " + wt, "gen in " + wt, "test in " + wt}) ||
		!slices.Equal(in.gates, []string{"test W1", "gen W2", "test W2"}) {
		t.Errorf("runs %v, gates %v", in.runs, in.gates)
	}
	if in.tip(t) == old || in.states(t, w1) != "W1 COMMITTED" {
		t.Errorf("the branch did not move, or W1 is not in: %s", in.states(t, w1))
	}
}

// noCorrection makes the project start no correction worker: Djinn asks at once (correct_test.go).
func (in *integration) noCorrection(t *testing.T) {
	t.Helper()
	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
		"generated: \"gen/**\"\ngenerate: \"gen\"\ntest: \"test\"\ncorrection_attempts: 0\n")
}

func TestIntegrateCodeConflict(t *testing.T) {
	in := integrating(t)
	in.noCorrection(t)
	w1 := in.finished(t, "W1", map[string]string{"app/README.md": "# One\n"})
	w2 := in.finished(t, "W2", map[string]string{"app/README.md": "# Two\n", "app/gen/index.txt": "two\n"})
	w3 := in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})

	in.pass(t, 0)
	// W1 went in first; W2 conflicts with it, alone; W3 goes in after it, on its own.
	if got := in.states(t, w1, w2, w3); got != "W1 COMMITTED, W2 CONFLICT, W3 COMMITTED" {
		t.Fatalf("a conflict in code: %s", got)
	}
	got, texts := in.integration(t, w2)
	if want := "W2 conflicts with " + in.branch + " in app/README.md"; got.GetReason() != want {
		t.Errorf("W2's reason %q; want %q", got.GetReason(), want)
	}
	q := in.question(t, w2)
	if last := texts[len(texts)-1]; last != "conflict: "+got.GetReason()+"; "+in.branch+" stays as it was; Djinn asks you "+q.GetCode() {
		t.Errorf("W2's last event %q", last)
	}
	if !strings.Contains(q.GetText(), "The project's settings start no correction worker") {
		t.Errorf("the question %q", q.GetText())
	}
	if commits := in.commits(t); len(commits) != 2 || len(in.runs) != 2 {
		t.Errorf("commits %v; runs %v", commits, in.runs)
	}
	if b, _ := os.ReadFile(filepath.Join(in.repo, "app", "README.md")); string(b) != "# One\n" {
		t.Errorf("the checkout's README: %q", b)
	}
	// W2's worktree stays, its work not committed into the branch.
	if _, err := os.Stat(w2.GetWorktree()); err != nil {
		t.Errorf("W2's worktree: %v", err)
	}
	// The integration worktree is left without a merge under way: the next task starts clean from the branch.
	if out := in.git(t, integrationDir(in.home, in.projectID, in.wishID), "status", "--porcelain"); out != "" {
		t.Errorf("the integration worktree holds %q", out)
	}
	// Failed work waits for its correction, or the person: it is not tried again.
	in.pass(t, time.Hour)
	if got := in.states(t, w2); got != "W2 CONFLICT" || len(in.runs) != 2 {
		t.Errorf("tried again: %s, runs %v", got, in.runs)
	}
}

func TestIntegrateRedTests(t *testing.T) {
	in := integrating(t)
	in.noCorrection(t)
	old := in.tip(t)
	in.testCode, in.testOut = 1, "--- FAIL: TestLogin\nFAIL"
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})

	in.pass(t, 0)
	if got := in.states(t, w1); got != "W1 RED" {
		t.Fatalf("red tests: %s", got)
	}
	if got, _ := in.integration(t, w1); got.GetReason() != "test exited 1:\n--- FAIL: TestLogin\nFAIL" {
		t.Errorf("W1's reason %q", got.GetReason())
	}
	if in.tip(t) != old || len(in.commits(t)) != 0 {
		t.Errorf("the branch moved to %s from %s", in.tip(t), old)
	}
	if _, err := os.Stat(filepath.Join(in.repo, "app", "src", "a.txt")); !os.IsNotExist(err) {
		t.Errorf("the checkout got the red work: %v", err)
	}
}

func TestIntegrateLeavesADirtyCheckout(t *testing.T) {
	in := integrating(t)
	old := in.tip(t)
	writeFile(t, in.repo, "app/README.md", "# Mine, not committed\n")
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})

	in.pass(t, 0)
	got, texts := in.integration(t, w1)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING || !strings.Contains(got.GetReason(), "your checkout of it, "+in.repo+
		", has changes not committed and is left as it is") || !strings.HasPrefix(texts[len(texts)-1], "waiting: tested green as ") {
		t.Fatalf("W1's integration %v; events %q", got, texts)
	}
	if b, _ := os.ReadFile(filepath.Join(in.repo, "app", "README.md")); in.tip(t) != old || string(b) != "# Mine, not committed\n" {
		t.Errorf("the branch moved to %s from %s, or the checkout changed: %q", in.tip(t), old, b)
	}
	// A pass while it stays dirty says nothing new and tests nothing again.
	in.pass(t, time.Minute)
	if _, again := in.integration(t, w1); len(again) != len(texts) || len(in.runs) != 1 {
		t.Errorf("events %q after %q; runs %v", again, texts, in.runs)
	}
	// Once the person's changes are committed, the work tested green goes in on top of them, tested again.
	in.git(t, in.repo, "commit", "--quiet", "-am", "Mine")
	in.pass(t, time.Minute)
	if got, texts := in.integration(t, w1); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || len(in.runs) != 2 {
		t.Errorf("W1's integration %v, events %q; runs %v", got, texts, in.runs)
	}
	if b, _ := os.ReadFile(filepath.Join(in.repo, "app", "src", "a.txt")); string(b) != "a\n" {
		t.Errorf("the checkout did not follow: %q", b)
	}
}

func TestIntegrateFollowsACleanedCheckout(t *testing.T) {
	in := integrating(t)
	writeFile(t, in.repo, "app/README.md", "# Mine, not committed\n")
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.pass(t, 0)
	if got := in.states(t, w1); got != "W1 PENDING" || len(in.runs) != 1 {
		t.Fatalf("%s; runs %v", got, in.runs)
	}
	// The person puts their changes away: the work tested green moves the branch, without running the tests again.
	in.git(t, in.repo, "checkout", "--", ".")
	in.pass(t, time.Minute)
	if got := in.states(t, w1); got != "W1 COMMITTED" || len(in.runs) != 1 {
		t.Errorf("%s; runs %v", got, in.runs)
	}
}

func TestIntegrateABranchNoCheckoutHolds(t *testing.T) {
	in := integrating(t)
	in.git(t, in.repo, "branch", "feat/x")
	if _, err := in.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
		WishId: in.wishID, Branch: "feat/x",
	})); err != nil {
		t.Fatal(err)
	}
	head := in.git(t, in.repo, "rev-parse", "HEAD")
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.pass(t, 0)
	got, texts := in.integration(t, w1)
	tip := in.git(t, in.repo, "rev-parse", "refs/heads/feat/x")
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || got.GetSha() != tip || got.GetBranch() != "feat/x" {
		t.Fatalf("W1's integration %v", got)
	}
	if want := "committed into feat/x as " + tip[:8] + ", with W1"; texts[len(texts)-1] != want {
		t.Errorf("last event %q; want %q", texts[len(texts)-1], want)
	}
	// The person's checkout is on another branch: it stays where it was.
	if now := in.git(t, in.repo, "rev-parse", "HEAD"); now != head {
		t.Errorf("the checkout moved to %s from %s", now, head)
	}
}

func TestIntegrateADoneWorker(t *testing.T) {
	in := integrating(t)
	task := in.spawn(t, in.wishID, "write src/a.txt a")
	in.watch(t.Context(), t, task.GetId(), 0)
	commitAll(t, in.get(t, task.GetId()).GetWorktree(), "Write a") // Its worker committed its work.
	got, texts := in.integration(t, task)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING ||
		!slices.Equal(texts, []string{"pending, to be merged into the integration branch"}) {
		t.Fatalf("a worker done: %v, %q", got, texts)
	}
	in.pass(t, time.Second)
	if got, texts := in.integration(t, task); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
		t.Fatalf("a task done: %v, %q", got, texts)
	}
	if out := in.git(t, in.repo, "show", in.tip(t)+":app/src/a.txt"); out != "a" {
		t.Errorf("a.txt in the branch: %q", out)
	}
}

func TestIntegrateNeedsATestCommand(t *testing.T) {
	in := integrating(t)
	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"), "generate: \"gen\"\n")
	task := in.spawn(t, in.wishID, "write src/a.txt a")
	in.watch(t.Context(), t, task.GetId(), 0)
	if got := in.get(t, task.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetIntegration() != nil {
		t.Errorf("without a test command: %v", got)
	}
}

func TestRecoverAnIntegration(t *testing.T) {
	in := integrating(t)
	w1 := in.finished(t, "W1", nil, func(task *planv1.Task) {
		task.Integration = &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING, Branch: "main"}
	})
	if err := in.h.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, texts := in.integration(t, w1)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING || got.GetBranch() != "main" ||
		!slices.Equal(texts, []string{"djinn up stopped during it; Djinn integrates the work again"}) {
		t.Errorf("recovered: %v, %q", got, texts)
	}
}

func TestMatchGlob(t *testing.T) {
	for _, c := range []struct {
		pattern, name string
		want          bool
	}{
		{"gen/**", "gen/go/plan/v1/plan.pb.go", true},
		{"gen/**", "gen", true},
		{"gen/**", "src/gen/x.go", false},
		{"docs/openapi.json", "docs/openapi.json", true},
		{"docs/openapi.json", "docs/other.json", false},
		{"**/*.pb.go", "api/v1/x.pb.go", true},
		{"**/*.pb.go", "x.pb.go", true},
		{"*.gen", "a/b.gen", false},
		{"src/*/index.ts", "src/a/index.ts", true},
	} {
		if got := matchGlob(c.pattern, c.name); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v", c.pattern, c.name, got)
		}
	}
}

// TestADependentStartsFromTheCommit: a task that waits for another whose work Djinn integrates waits for that work to
// be committed, not only done; the work is committed at once, alone, and the task then starts from the integration
// branch, its worktree holding the dependency's change, even when the person's checkout is on another branch.
func TestADependentStartsFromTheCommit(t *testing.T) {
	in := integrating(t, WithTick(time.Hour)) // Only the commit wakes the scheduler.
	in.git(t, in.repo, "checkout", "--quiet", "-b", "elsewhere")
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	w2 := in.mustSpawn(t, in.wishID, "Second", "wait\ntext two", &planv1.TaskServiceSpawnRequest{DependsOn: []string{"W1"}})
	if w2.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || w2.GetWaitReason() != "waits for W1 to be committed" {
		t.Fatalf("W2 = %v", w2)
	}

	in.pass(t, 0)
	tip := in.tip(t)
	if got := in.states(t, w1); got != "W1 COMMITTED" {
		t.Fatalf("a task another waits for is committed at once: %s", got)
	}
	w2 = in.until(t, w2.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	if b, err := os.ReadFile(filepath.Join(w2.GetWorktree(), "app", "src", "a.txt")); err != nil || string(b) != "a\n" {
		t.Errorf("W2's worktree does not hold W1's work: %q, %v", b, err)
	}
	if base := in.git(t, in.repo, "rev-parse", w2.GetBranch()); base != tip {
		t.Errorf("W2's branch starts at %s; the integration branch is at %s", base, tip)
	}
	// The person's checkout stays where it was.
	if _, err := os.Stat(filepath.Join(in.repo, "app", "src", "a.txt")); !os.IsNotExist(err) {
		t.Errorf("the checkout on another branch got W1's work: %v", err)
	}
	in.release(t, w2.GetId())
	in.ended(t, w2.GetId())
	events, err := store.List[*planv1.TaskEvent](t.Context(), in.db, store.Where{"task_id": w2.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	texts := eventTexts(events)
	from := ", on branch " + w2.GetBranch() + ", from " + in.branch + " at " + tip[:8] + ","
	if !slices.Contains(texts, "waiting: waits for W1 to be committed") ||
		!slices.ContainsFunc(texts, func(s string) bool { return strings.HasPrefix(s, "started fake") && strings.Contains(s, from) }) {
		t.Errorf("W2's events, without %q: %q", from, texts)
	}
}
