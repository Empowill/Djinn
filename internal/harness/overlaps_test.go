package harness

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestOverlaps reads the files of a wish's open work from real worktrees and branches, and pairs the tasks that touch
// the same ones: a running task's uncommitted and untracked files, a done task's branch not merged yet, a planned
// task's write scopes; never main's own commits, generated files, merged or cancelled work.
func TestOverlaps(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	repo := gitRepoWith(t, map[string]string{
		"a.go": "package a\n", "b.go": "package b\n", "c.go": "package c\n", "docs/guide.md": "# Guide\n",
		"gen/g.go": "package gen\n",
	})
	main := plan.CheckedOutBranch(ctx, repo)
	run := func(dir string, args ...string) {
		t.Helper()
		if _, err := git(ctx, dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(dir, msg string) {
		t.Helper()
		run(dir, "add", ".")
		run(dir, "commit", "--quiet", "-m", msg)
	}
	worktree := func(branch string) string {
		t.Helper()
		wt := filepath.Join(t.TempDir(), branch)
		run(repo, "worktree", "add", "--quiet", "-b", branch, wt, main)
		return wt
	}

	// W1 runs: a.go changed and n.go added, neither committed; gen/g.go changed, generated.
	wt1 := worktree("w1")
	writeFile(t, wt1, "a.go", "package a\n\nvar one = 1\n")
	writeFile(t, wt1, "n.go", "package a\n")
	writeFile(t, wt1, "gen/g.go", "package gen\n\nvar g = 1\n")
	// W2 is done, its work committed on its branch, waiting to merge; its worktree is gone.
	wt2 := worktree("w2")
	writeFile(t, wt2, "a.go", "package a\n\nvar two = 2\n")
	writeFile(t, wt2, "b.go", "package b\n\nvar two = 2\n")
	writeFile(t, wt2, "gen/g.go", "package gen\n\nvar g = 2\n")
	commit(wt2, "W2's work")
	run(repo, "worktree", "remove", "--force", wt2)
	// Main moves on after W2 started: its guide is no file of W2's.
	writeFile(t, repo, "docs/guide.md", "# Guide\n\nMoved on.\n")
	commit(repo, "Main moves on")
	// W3 runs from the new main and changes the guide and c.go.
	wt3 := worktree("w3")
	writeFile(t, wt3, "docs/guide.md", "# Guide\n\nW3.\n")
	writeFile(t, wt3, "c.go", "package c\n\nvar three = 3\n")
	// W7, merged, and W8, cancelled, changed a.go too: no longer open work.
	wt7 := worktree("w7")
	writeFile(t, wt7, "a.go", "package a\n\nvar seven = 7\n")

	e := up(t, t.TempDir())
	wishID, projectID := e.wish(t, repo)
	put := func(task *planv1.Task) {
		t.Helper()
		task.Id, task.WishId, task.ProjectId, task.Title = store.NewID(), wishID, projectID, "Task "+task.GetCode()
		task.CreateTime = timestamppb.Now()
		err := e.db.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
				return err
			}
			return tx.Put(task)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	started := timestamppb.Now()
	running := planv1.TaskStatus_TASK_STATUS_RUNNING
	pending := planv1.TaskStatus_TASK_STATUS_PENDING
	put(&planv1.Task{Code: "W1", Status: running, Branch: "w1", Worktree: wt1, StartTime: started})
	put(&planv1.Task{Code: "W2", Status: planv1.TaskStatus_TASK_STATUS_DONE, Branch: "w2", Worktree: wt2,
		StartTime: started, Integration: &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_PENDING}})
	put(&planv1.Task{Code: "W3", Status: running, Branch: "w3", Worktree: wt3, StartTime: started})
	put(&planv1.Task{Code: "W4", Status: pending, WriteScopes: []string{"docs"}})
	put(&planv1.Task{Code: "W5", Status: pending, WriteScopes: []string{"Docs/guide.md", "e2e"}})
	put(&planv1.Task{Code: "W6", Status: pending})
	put(&planv1.Task{Code: "W7", Status: planv1.TaskStatus_TASK_STATUS_DONE, Branch: "w7", Worktree: wt7,
		StartTime: started, Integration: &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED}})
	put(&planv1.Task{Code: "W8", Status: planv1.TaskStatus_TASK_STATUS_CANCELLED, Branch: "w7", Worktree: wt7})
	put(&planv1.Task{Code: "W9", Status: running, Provider: planv1.Provider_PROVIDER_WATCH})

	res, err := e.tasks.Overlaps(ctx, connect.NewRequest(&planv1.TaskServiceOverlapsRequest{WishId: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range res.Msg.GetTasks() {
		got[f.GetCode()] = strings.TrimPrefix(f.GetSource().String(), "TASK_FILES_SOURCE_") + " " +
			strings.Join(f.GetPaths(), ",")
	}
	want := map[string]string{
		"W1": "DIFF a.go,n.go", "W2": "DIFF a.go,b.go", "W3": "DIFF c.go,docs/guide.md",
		"W4": "WRITE_SCOPES docs", "W5": "WRITE_SCOPES Docs/guide.md,e2e", "W6": "UNKNOWN ",
	}
	for code, w := range want {
		if got[code] != w {
			t.Errorf("%s touches %q, want %q", code, got[code], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("tasks = %v, want only W1 to W6: no merged, cancelled or watcher task", got)
	}
	var pairs []string
	for _, o := range res.Msg.GetOverlaps() {
		pairs = append(pairs, strings.Join(o.GetCodes(), "+")+": "+strings.Join(o.GetPaths(), ","))
	}
	wantPairs := []string{"W1+W2: a.go", "W3+W4: docs/guide.md", "W3+W5: docs/guide.md", "W4+W5: Docs/guide.md"}
	if !slices.Equal(pairs, wantPairs) {
		t.Errorf("overlaps = %q, want %q", pairs, wantPairs)
	}
	text := res.Msg.GetText()
	for _, line := range []string{
		"- W1 running: 2 files: a.go, n.go", "- W2 done, not merged: 2 files: a.go, b.go",
		"- W4 planned: write scopes docs", "- W6 planned: paths unknown, no write scopes", "- W1 and W2: a.go",
	} {
		if !strings.Contains(text, line) {
			t.Errorf("text lacks %q:\n%s", line, text)
		}
	}

	// The wish's brief carries the overlaps, short.
	wishes := &plan.Wishes{Store: e.db, Overlaps: e.h.OverlapsBrief}
	brief, err := wishes.Brief(ctx, connect.NewRequest(&planv1.WishServiceBriefRequest{WishId: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"## Overlaps", "- W1 and W2: a.go\n", "- W3 and W4: docs/guide.md\n",
		"Planned without write scopes, their paths unknown: W6.", "djinn task overlaps " + wishID,
	} {
		if !strings.Contains(brief.Msg.GetText(), line) {
			t.Errorf("brief lacks %q", line)
		}
	}
	if strings.Contains(brief.Msg.GetText(), "n.go") {
		t.Error("the brief lists each task's files: only the overlaps belong there")
	}
}

// TestOverlapsNone says so when no two open tasks touch the same file, and leaves the brief without the section.
func TestOverlapsNone(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	repo := gitRepoWith(t, map[string]string{"a.go": "package a\n"})
	main := plan.CheckedOutBranch(ctx, repo)
	wt := filepath.Join(t.TempDir(), "w1")
	if _, err := git(ctx, repo, "worktree", "add", "--quiet", "-b", "w1", wt, main); err != nil {
		t.Fatal(err)
	}
	writeFile(t, wt, "a.go", "package a\n\nvar one = 1\n")
	e := up(t, t.TempDir())
	wishID, projectID := e.wish(t, repo)
	err := e.db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
			return err
		}
		return tx.Put(&planv1.Task{Id: store.NewID(), WishId: wishID, ProjectId: projectID, Code: "W1", Title: "One",
			Status: planv1.TaskStatus_TASK_STATUS_RUNNING, Branch: "w1", Worktree: wt, StartTime: timestamppb.Now(),
			CreateTime: timestamppb.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.tasks.Overlaps(ctx, connect.NewRequest(&planv1.TaskServiceOverlapsRequest{WishId: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	if text := res.Msg.GetText(); !strings.Contains(text, "No overlap") || !strings.Contains(text, "- W1 running: 1 file: a.go") {
		t.Errorf("text = %q", text)
	}
	if section := e.h.OverlapsBrief(ctx, wishID); section != "" {
		t.Errorf("brief section = %q, want none", section)
	}
}

// TestSpawnOverlaps checks that spawning a task (or launching a plan from an answer) warns when its scope overlaps an
// open task's branch diff or prompt (paths, packages, specs), records the warning as a status event, omits tasks
// already ordered via --after or touching disjoint scopes, and never blocks a repair spawn.
func TestSpawnOverlaps(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	repo := gitRepoWith(t, map[string]string{
		"pkg/a/x.go":              "package a\n",
		"pkg/b/y.go":              "package b\n",
		"pkg/c/z.go":              "package c\n",
		"e2e/wish-render.spec.ts": "export {}\n",
	})
	main := plan.CheckedOutBranch(ctx, repo)
	wtDiff := filepath.Join(t.TempDir(), "w-diff")
	if _, err := git(ctx, repo, "worktree", "add", "--quiet", "-b", "w-diff", wtDiff, main); err != nil {
		t.Fatal(err)
	}
	writeFile(t, wtDiff, "pkg/a/x.go", "package a // diff on branch\n")

	e := up(t, t.TempDir())
	wishID, projectID := e.wish(t, repo)
	err := e.db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
			return err
		}
		// W1 touches pkg/a/x.go only in its worktree diff (not in its prompt).
		return tx.Put(&planv1.Task{
			Id: store.NewID(), WishId: wishID, ProjectId: projectID, Code: "W1", Title: "Open diff task",
			Status: planv1.TaskStatus_TASK_STATUS_RUNNING, Branch: "w-diff", Worktree: wtDiff,
			StartTime: timestamppb.Now(), CreateTime: timestamppb.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	spawn := func(req *planv1.TaskServiceSpawnRequest) *planv1.TaskServiceSpawnResponse {
		t.Helper()
		req.WishId = wishID
		req.Provider = planv1.Provider_PROVIDER_FAKE
		req.Later = true
		res, err := e.tasks.Spawn(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatalf("spawn %q: %v", req.GetTitle(), err)
		}
		return res.Msg
	}

	// 1. Overlap from a diff: new task names pkg/a/x.go in its prompt, which W1 modified in its worktree diff.
	resDiff := spawn(&planv1.TaskServiceSpawnRequest{
		Title: "Touch a", Prompt: "wait\ntext Update pkg/a/x.go to handle edge case",
	})
	noteDiff := resDiff.GetNote()
	if !strings.Contains(noteDiff, "may overlap with W1 (running; diff: pkg/a/x.go)") || !strings.Contains(noteDiff, "--after W1") {
		t.Errorf("diff overlap note = %q, want W1 (running; diff: pkg/a/x.go) and --after W1", noteDiff)
	}
	events, err := store.List[*planv1.TaskEvent](ctx, e.db, store.Where{"task_id": resDiff.GetTask().GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(events, func(ev *planv1.TaskEvent) bool {
		return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.Contains(ev.GetText(), "may overlap with W1")
	}) {
		t.Errorf("expected overlap warning status event on spawned task, got %v", events)
	}

	// 2. Overlap from prompts (package and spec): W3 names pkg/b and wish-render.spec.ts; W4 names pkg/b/y.go and wish-render.spec.ts.
	resPromptA := spawn(&planv1.TaskServiceSpawnRequest{
		Title: "Prompt A", Prompt: "wait\ntext Refactor package pkg/b and check wish-render.spec.ts",
	})
	if strings.Contains(resPromptA.GetNote(), "may overlap with") {
		t.Errorf("Prompt A should not warn against W1/W2, got note = %q", resPromptA.GetNote())
	}
	codePromptA := resPromptA.GetTask().GetCode()

	resPromptB := spawn(&planv1.TaskServiceSpawnRequest{
		Title: "Prompt B", Prompt: "wait\ntext Adjust pkg/b/y.go and e2e/wish-render.spec.ts",
	})
	notePromptB := resPromptB.GetNote()
	if !strings.Contains(notePromptB, codePromptA) || !strings.Contains(notePromptB, "pkg/b/y.go") || !strings.Contains(notePromptB, "e2e/wish-render.spec.ts") {
		t.Errorf("prompt overlap note = %q, want %s, pkg/b/y.go and e2e/wish-render.spec.ts", notePromptB, codePromptA)
	}

	// 3. None: disjoint scope (pkg/c/z.go) produces no warning; an ordered task (--after W1, W2) produces no warning.
	resNone := spawn(&planv1.TaskServiceSpawnRequest{
		Title: "Disjoint", Prompt: "wait\ntext Clean up pkg/c/z.go",
	})
	if strings.Contains(resNone.GetNote(), "may overlap with") {
		t.Errorf("disjoint spawn note = %q, want no overlap warning", resNone.GetNote())
	}

	resOrdered := spawn(&planv1.TaskServiceSpawnRequest{
		Title:       "Ordered after W1 and W2",
		Prompt:      "wait\ntext Follow-up in pkg/a/x.go",
		After:       []string{"W1", resDiff.GetTask().GetCode()},
		WriteScopes: []string{"pkg/a/x.go"},
	})
	if strings.Contains(resOrdered.GetNote(), "may overlap with") {
		t.Errorf("ordered spawn note = %q, want no overlap warning when ordered via --after", resOrdered.GetNote())
	}

	// 4. A repair is never blocked even when it collides with open work on pkg/a/x.go.
	resRepair := spawn(&planv1.TaskServiceSpawnRequest{
		Title: "Repair lint in pkg/a/x.go", Prompt: "wait\ntext Fix broken build in pkg/a/x.go",
	})
	if resRepair.GetTask().GetId() == "" {
		t.Fatal("repair spawn returned empty task")
	}
	if !strings.Contains(resRepair.GetNote(), "may overlap with W1") {
		t.Errorf("repair spawn note = %q, want overlap warning while permitting spawn", resRepair.GetNote())
	}

	// 5. Question / plan launch: answering a question whose planned task overlaps open work on pkg/c/z.go records the warning.
	questions := planv1connect.NewQuestionServiceClient(e.srv.Client(), e.srv.URL)
	planQ, err := questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		WishId:  wishID,
		Text:    "Launch another task on pkg/c/z.go?",
		Options: []string{"Yes", "No"},
		Plan:    []string{"option: A\ntitle: Plan on pkg/c\nprovider: fake\n\nwait\ntext Update pkg/c/z.go again"},
	}))
	if err != nil {
		t.Fatalf("ask plan question: %v", err)
	}
	ansRes, err := questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: planQ.Msg.GetQuestion().GetCode()}},
		Choice:   planv1.Choice_CHOICE_A,
		WishId:   wishID,
		By:       planv1.Closer_CLOSER_DEVELOPER,
	}))
	if err != nil {
		t.Fatalf("answer plan question: %v", err)
	}
	launchedID := ansRes.Msg.GetQuestion().GetPlans()[0].GetTaskId()
	if launchedID == "" {
		t.Fatal("expected planned task to launch on answer")
	}
	launchedEvents, err := store.List[*planv1.TaskEvent](ctx, e.db, store.Where{"task_id": launchedID})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(launchedEvents, func(ev *planv1.TaskEvent) bool {
		return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS &&
			strings.Contains(ev.GetText(), "may overlap with "+resNone.GetTask().GetCode()) &&
			strings.Contains(ev.GetText(), "pkg/c/z.go")
	}) {
		t.Errorf("launched plan task events = %v, want overlap warning on pkg/c/z.go", launchedEvents)
	}
}
