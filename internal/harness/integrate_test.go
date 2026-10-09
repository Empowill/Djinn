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

	mu       sync.Mutex
	now      time.Time
	runs     []string // the commands run: "test in <dir>"
	gates    []string // the gates taken: "test W1"
	testCode int      // what the test command exits with
	testOut  string
}

// integrating starts djinn up on a repository with app/README.md, app/gen/index.txt and app/src, a wish on it whose
// integration branch is the one its checkout is on, and project settings that name gen/** as generated, "gen" as
// the command that makes it, "test" as the test command.
func integrating(t *testing.T) *integration {
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
	in.env = up(t, t.TempDir(), WithClock(in.clock), WithCommands(in.run), WithGates(in.gate))
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
		return in.testOut, in.testCode, nil
	}
	return "unknown command", 127, nil
}

func (in *integration) gate(ctx context.Context, name, taskID, what, dir string) (func(), error) {
	task, err := store.Get[*planv1.Task](ctx, in.db, taskID)
	if err != nil {
		return nil, err
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	in.gates = append(in.gates, name+" "+task.GetCode())
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
// repository's HEAD, files written there and not committed, its work waiting for its batch. Each finishes a second
// after the one before.
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
	for name, content := range files {
		writeFile(t, task.GetWorktree(), name, content)
	}
	putTask(t, in.db, task, "work")
	return task
}

// integration is where the task's work stands, and its events' texts about it.
func (in *integration) integration(t *testing.T, task *planv1.Task) (*planv1.TaskIntegration, []string) {
	t.Helper()
	got := in.get(t, task.GetId())
	events, err := store.List[*planv1.TaskEvent](t.Context(), in.db, store.Where{"task_id": task.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(events, func(a, b *planv1.TaskEvent) int { return int(a.GetSeq() - b.GetSeq()) })
	var texts []string
	for _, e := range events {
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

func TestIntegrateACleanBatch(t *testing.T) {
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

	in.pass(t, time.Hour)
	if got := in.states(t, w1, w2, w3); got != "W1 COMMITTED, W2 COMMITTED, W3 COMMITTED" {
		t.Fatalf("after an hour and three tasks: %s", got)
	}
	tip := in.tip(t)
	got, texts := in.integration(t, w1)
	if got.GetSha() != tip || tip == old || got.GetBranch() != in.branch {
		t.Errorf("W1's integration %v; the branch is at %s, was at %s", got, tip, old)
	}
	want := []string{
		"integrating into " + in.branch + ", with W1, W2, W3",
		"merged " + w1.GetBranch(),
		"testing W1, W2, W3: test",
		"committed into " + in.branch + " as " + tip[:8] + ", with W1, W2, W3; your checkout of it, " + in.repo + ", follows",
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
	// The tests ran once, in the integration worktree, never in the person's checkout, under the test gate.
	wt := filepath.Join(integrationDir(in.home, in.projectID, in.wishID), "app")
	if !slices.Equal(in.runs, []string{"test in " + wt}) || !slices.Equal(in.gates, []string{"test W1"}) {
		t.Errorf("runs %v, gates %v", in.runs, in.gates)
	}
	// The person's checkout was clean and on the branch: it follows, clean.
	if b, err := os.ReadFile(filepath.Join(in.repo, "app", "src", "c.txt")); err != nil || string(b) != "c\n" {
		t.Errorf("the checkout did not follow: %q, %v", b, err)
	}
	if out := in.git(t, in.repo, "status", "--porcelain"); out != "" {
		t.Errorf("the checkout is not clean: %q", out)
	}
	commits := in.commits(t)
	if len(commits) != 1 || commits[0].GetOldSha() != old || commits[0].GetNewSha() != tip || commits[0].GetBranch() != in.branch ||
		!slices.Equal(commits[0].GetTaskIds(), []string{w1.GetId(), w2.GetId(), w3.GetId()}) {
		t.Errorf("journal: %v", commits)
	}

	// Nothing waits any more: the next pass does nothing.
	in.pass(t, time.Hour)
	if len(in.runs) != 1 || len(in.commits(t)) != 1 {
		t.Errorf("a pass with nothing waiting ran %v", in.runs)
	}
}

func TestIntegrateGeneratedConflict(t *testing.T) {
	in := integrating(t)
	old := in.tip(t)
	// Each worker made the generated index again on its own: the two branches change it differently.
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n", "app/gen/index.txt": "a.txt\n"})
	w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n", "app/gen/index.txt": "b.txt\n"})
	in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})

	in.pass(t, time.Hour)
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
	if !slices.Equal(in.runs, []string{"gen in " + wt, "test in " + wt}) || !slices.Equal(in.gates, []string{"gen W2", "test W1"}) {
		t.Errorf("runs %v, gates %v", in.runs, in.gates)
	}
	if in.tip(t) == old || in.states(t, w1) != "W1 COMMITTED" {
		t.Errorf("the branch did not move, or W1 is not in: %s", in.states(t, w1))
	}
}

func TestIntegrateCodeConflict(t *testing.T) {
	in := integrating(t)
	old := in.tip(t)
	w1 := in.finished(t, "W1", map[string]string{"app/README.md": "# One\n"})
	w2 := in.finished(t, "W2", map[string]string{"app/README.md": "# Two\n", "app/gen/index.txt": "two\n"})
	w3 := in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})

	in.pass(t, time.Hour)
	if got := in.states(t, w1, w2, w3); got != "W1 CONFLICT, W2 CONFLICT, W3 CONFLICT" {
		t.Fatalf("a conflict in code: %s", got)
	}
	got, texts := in.integration(t, w3)
	if want := "W2 conflicts with " + in.branch + " in app/README.md"; got.GetReason() != want {
		t.Errorf("W3's reason %q; want %q", got.GetReason(), want)
	}
	if last := texts[len(texts)-1]; last != "conflict: "+got.GetReason()+"; "+in.branch+" stays as it was" {
		t.Errorf("W3's last event %q", last)
	}
	if in.tip(t) != old || len(in.commits(t)) != 0 || len(in.runs) != 0 {
		t.Errorf("the branch moved to %s from %s; commits %v; runs %v", in.tip(t), old, in.commits(t), in.runs)
	}
	if b, _ := os.ReadFile(filepath.Join(in.repo, "app", "README.md")); string(b) != "# App\n" {
		t.Errorf("the checkout changed: %q", b)
	}
	// The integration worktree is left without a merge under way: the next batch starts clean from the branch.
	if out := in.git(t, integrationDir(in.home, in.projectID, in.wishID), "status", "--porcelain"); out != "" {
		t.Errorf("the integration worktree holds %q", out)
	}
	// A failed batch waits for its correction: it is not tried again.
	in.pass(t, time.Hour)
	if got := in.states(t, w1); got != "W1 CONFLICT" {
		t.Errorf("tried again: %s", got)
	}
}

func TestIntegrateRedTests(t *testing.T) {
	in := integrating(t)
	old := in.tip(t)
	in.testCode, in.testOut = 1, "--- FAIL: TestLogin\nFAIL"
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
	w3 := in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})

	in.pass(t, time.Hour)
	if got := in.states(t, w1, w2, w3); got != "W1 RED, W2 RED, W3 RED" {
		t.Fatalf("red tests: %s", got)
	}
	if got, _ := in.integration(t, w2); got.GetReason() != "test exited 1:\n--- FAIL: TestLogin\nFAIL" {
		t.Errorf("W2's reason %q", got.GetReason())
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
	in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
	in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})

	in.pass(t, time.Hour)
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
	// Once the person's changes are committed, the batch tested green goes in on top of them, tested again.
	in.git(t, in.repo, "commit", "--quiet", "-am", "Mine")
	in.pass(t, time.Minute)
	if got, texts := in.integration(t, w1); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || len(in.runs) != 2 {
		t.Errorf("W1's integration %v, events %q; runs %v", got, texts, in.runs)
	}
	if b, _ := os.ReadFile(filepath.Join(in.repo, "app", "src", "b.txt")); string(b) != "b\n" {
		t.Errorf("the checkout did not follow: %q", b)
	}
}

func TestIntegrateFollowsACleanedCheckout(t *testing.T) {
	in := integrating(t)
	writeFile(t, in.repo, "app/README.md", "# Mine, not committed\n")
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.pass(t, time.Hour) // Not due: one task only.
	if got := in.states(t, w1); got != "W1 PENDING" || len(in.runs) != 0 {
		t.Fatalf("%s; runs %v", got, in.runs)
	}
	in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
	in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})
	in.pass(t, 0)
	// The person puts their changes away: the batch tested green moves the branch, without running the tests again.
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
		WishId: in.wishID, Branch: "feat/x", CommitAfterTasks: 1, CommitAfterMinutes: 10,
	})); err != nil {
		t.Fatal(err)
	}
	head := in.git(t, in.repo, "rev-parse", "HEAD")
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.pass(t, 9*time.Minute)
	if got := in.states(t, w1); got != "W1 PENDING" {
		t.Fatalf("before 10 minutes: %s", got)
	}
	in.pass(t, time.Minute)
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
	got, texts := in.integration(t, task)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING || !slices.Equal(texts, []string{"pending, waiting for its batch"}) {
		t.Fatalf("a worker done: %v, %q", got, texts)
	}
	// A task another task waits for is committed at once, alone. The other one waits for an azima too, so that it
	// stays planned.
	azima, err := in.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: in.wishID, Title: "Later", Kind: planv1.TaskKind_TASK_KIND_AZIMA,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: in.wishID, Title: "Next", Prompt: "text next", Provider: planv1.Provider_PROVIDER_FAKE,
		DependsOn: []string{task.GetCode(), azima.Msg.GetTask().GetCode()},
	})); err != nil {
		t.Fatal(err)
	}
	in.pass(t, time.Second)
	if got, texts := in.integration(t, task); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
		t.Fatalf("an awaited task: %v, %q", got, texts)
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

func TestDue(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	task := func(code string, status planv1.TaskStatus, edit func(*planv1.Task)) *planv1.Task {
		t := &planv1.Task{Id: code, Code: code, Status: status, Kind: planv1.TaskKind_TASK_KIND_WORK, Scheduled: true}
		if edit != nil {
			edit(t)
		}
		return t
	}
	done := planv1.TaskStatus_TASK_STATUS_DONE
	partOf := func(a string) func(*planv1.Task) { return func(t *planv1.Task) { t.PartOf = a } }
	batches := func(b [][]*planv1.Task) string {
		var s []string
		for _, batch := range b {
			s = append(s, "["+codes(batch)+"]")
		}
		return strings.Join(s, " ")
	}
	t1 := task("T1", planv1.TaskStatus_TASK_STATUS_PENDING, func(t *planv1.Task) { t.Kind = planv1.TaskKind_TASK_KIND_AZIMA })
	w1, w2, w3 := task("W1", done, partOf("T1")), task("W2", done, partOf("T1")), task("W3", done, nil)
	running := task("W4", planv1.TaskStatus_TASK_STATUS_RUNNING, partOf("T1"))
	failed := task("W4", planv1.TaskStatus_TASK_STATUS_FAILED, partOf("T1"))
	planned := task("W5", planv1.TaskStatus_TASK_STATUS_PENDING, func(t *planv1.Task) { t.DependsOn = []string{"W3"} })
	started := task("W5", planv1.TaskStatus_TASK_STATUS_RUNNING, func(t *planv1.Task) { t.DependsOn = []string{"W3"} })

	for _, c := range []struct {
		name           string
		waiting, tasks []*planv1.Task
		since          time.Duration
		want           string
	}{
		{"an azima with a part still running", []*planv1.Task{w1}, []*planv1.Task{t1, w1, running}, time.Minute, ""},
		{"an azima's last part done", []*planv1.Task{w1, w2}, []*planv1.Task{t1, w1, w2}, time.Minute, "[W1, W2]"},
		{"an azima whose other part failed", []*planv1.Task{w1}, []*planv1.Task{t1, w1, failed}, time.Minute, "[W1]"},
		{"three tasks, before the hour", []*planv1.Task{w1, w2, w3}, []*planv1.Task{t1, w1, w2, w3, running}, 59 * time.Minute, ""},
		{"three tasks, an hour", []*planv1.Task{w1, w2, w3}, []*planv1.Task{t1, w1, w2, w3, running}, time.Hour, "[W1, W2, W3]"},
		{"two tasks, a day", []*planv1.Task{w1, w3}, []*planv1.Task{t1, w1, w3, running}, 24 * time.Hour, ""},
		{"a task another waits for, at once and alone", []*planv1.Task{w1, w3}, []*planv1.Task{t1, w1, w3, running, planned}, time.Minute, "[W3]"},
		{"a task another started after", []*planv1.Task{w3}, []*planv1.Task{w3, started}, time.Minute, ""},
		{"alone, then the batch", []*planv1.Task{w1, w2, w3}, []*planv1.Task{t1, w1, w2, w3, planned}, time.Minute, "[W3] [W1, W2]"},
	} {
		if got := batches(due(c.waiting, c.tasks, now.Add(-c.since), now, time.Hour, 3)); got != c.want {
			t.Errorf("%s: %q; want %q", c.name, got, c.want)
		}
	}
}

func TestLastCommit(t *testing.T) {
	made := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	wish := &planv1.Wish{CreateTime: timestamppb.New(made)}
	committed := func(project string, at time.Time) *planv1.Task {
		return &planv1.Task{ProjectId: project, Integration: &planv1.TaskIntegration{
			State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, UpdateTime: timestamppb.New(at),
		}}
	}
	if got := lastCommit(wish, nil, "p"); !got.Equal(made) {
		t.Errorf("none: %v", got)
	}
	tasks := []*planv1.Task{committed("p", made.Add(time.Hour)), committed("p", made.Add(2*time.Hour)), committed("q", made.Add(3*time.Hour))}
	if got := lastCommit(wish, tasks, "p"); !got.Equal(made.Add(2 * time.Hour)) {
		t.Errorf("the project's last: %v", got)
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
