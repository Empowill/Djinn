package harness

import (
	"os"
	"path/filepath"
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

// TestCommentStorage verifies that diff comments are stored on the task with file, line range, commit,
// excerpt, and create_time, and journaled as the TaskService.Comment procedure.
func TestCommentStorage(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()

	repo := gitRepo(t)
	codeGo := filepath.Join(repo, "code.go")
	lines := "package main\n\nfunc Hello() string {\n\treturn \"hello\"\n}\n"
	if err := os.WriteFile(codeGo, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "--quiet", "-m", "Add code.go"}} {
		if _, err := git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	sha, err := git(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	e := up(t, t.TempDir())
	wishID, projectID := e.wish(t, repo)

	taskID := store.NewID()
	err = e.db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
			return err
		}
		return tx.Put(&planv1.Task{
			Id:         taskID,
			WishId:     wishID,
			ProjectId:  projectID,
			Code:       "W1",
			Title:      "Reviewable task",
			Branch:     plan.CheckedOutBranch(ctx, repo),
			Status:     planv1.TaskStatus_TASK_STATUS_DONE,
			CreateTime: timestamppb.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Store a comment without explicit commit and excerpt (they should be auto-populated).
	commentReq := &planv1.TaskServiceCommentRequest{
		TaskId:  taskID,
		File:    "code.go",
		Line:    3,
		EndLine: 5,
		Text:    "Consider returning a formatted greeting",
	}
	res, err := e.tasks.Comment(ctx, connect.NewRequest(commentReq))
	if err != nil {
		t.Fatalf("TaskService.Comment failed: %v", err)
	}
	c := res.Msg.GetComment()
	if c.GetId() == "" {
		t.Errorf("comment ID is empty")
	}
	if c.GetFile() != "code.go" {
		t.Errorf("comment File = %q, want code.go", c.GetFile())
	}
	if c.GetLine() != 3 || c.GetEndLine() != 5 {
		t.Errorf("comment range = %d-%d, want 3-5", c.GetLine(), c.GetEndLine())
	}
	if c.GetCommit() != sha {
		t.Errorf("comment Commit = %q, want %q", c.GetCommit(), sha)
	}
	wantExcerpt := "func Hello() string {\n\treturn \"hello\"\n}"
	if c.GetExcerpt() != wantExcerpt {
		t.Errorf("comment Excerpt = %q, want %q", c.GetExcerpt(), wantExcerpt)
	}
	if c.GetText() != "Consider returning a formatted greeting" {
		t.Errorf("comment Text = %q", c.GetText())
	}
	if c.GetResolved() {
		t.Errorf("comment should not be resolved initially")
	}
	if c.GetCreateTime() == nil {
		t.Errorf("comment CreateTime is nil")
	}
	if c.GetSentTo() != "" {
		t.Errorf("comment on an ended task went to %q, want to no worker", c.GetSentTo())
	}

	// A commit given as a branch name is kept as the commit it is now: a later commit on the branch resolves it.
	byBranch, err := e.tasks.Comment(ctx, connect.NewRequest(&planv1.TaskServiceCommentRequest{
		TaskId: taskID, File: "code.go", Line: 1, Commit: plan.CheckedOutBranch(ctx, repo), Text: "Name the package",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := byBranch.Msg.GetComment().GetCommit(); got != sha {
		t.Errorf("comment on a branch: Commit = %q, want %q", got, sha)
	}

	// Verify task in store has the comment.
	task, err := store.Get[*planv1.Task](ctx, e.db, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.GetComments()) != 2 {
		t.Fatalf("task comments count = %d, want 2", len(task.GetComments()))
	}
	if task.GetComments()[0].GetId() != c.GetId() {
		t.Errorf("stored comment ID = %q, want %q", task.GetComments()[0].GetId(), c.GetId())
	}

	// Verify journal entry.
	commands, err := store.Commands(ctx, e.db, func(cmd store.Command) bool {
		return cmd.Method == planv1connect.TaskServiceCommentProcedure
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 || commands[0].Actor != actorLocal {
		t.Fatalf("journaled commands = %+v, want 2 by %s", commands, actorLocal)
	}

	// 2. Validate empty text rejection.
	_, err = e.tasks.Comment(ctx, connect.NewRequest(&planv1.TaskServiceCommentRequest{
		TaskId: taskID,
		File:   "code.go",
		Line:   1,
		Text:   "   ",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("expected CodeInvalidArgument for empty text, got %v", err)
	}
}

// TestCommentSend verifies that when a task's worker runs, a comment is sent to it as an instruction
// quoting the file and line range (djinn task send).
func TestCommentSend(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()

	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())
	task := e.spawn(t, wishID, "text working\nwait\ntext done")

	s, err := e.tasks.Watch(ctx, connect.NewRequest(&planv1.TaskServiceWatchRequest{TaskId: task.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for s.Receive() {
		if s.Msg().GetEvent().GetText() == "working" {
			break
		}
	}

	commentReq := &planv1.TaskServiceCommentRequest{
		TaskId:  task.GetId(),
		File:    "src/calc.go",
		Line:    15,
		EndLine: 18,
		Text:    "Check boundary conditions",
		Excerpt: "for i := 0; i <= n; i++ {\n\tprocess(i)\n}",
	}
	res, err := e.tasks.Comment(ctx, connect.NewRequest(commentReq))
	if err != nil {
		t.Fatalf("Comment failed on running task: %v", err)
	}
	if res.Msg.GetComment().GetId() == "" {
		t.Fatalf("comment was not created")
	}
	if got := res.Msg.GetComment().GetSentTo(); got != task.GetId() {
		t.Errorf("comment went to %q, want the running task %s", got, task.GetId())
	}

	// Worker should receive the instruction, unblock from wait, and complete.
	var events []*planv1.TaskEvent
	for s.Receive() {
		events = append(events, s.Msg().GetEvent())
	}

	var msgEvent *planv1.TaskEvent
	for _, ev := range events {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_MESSAGE {
			msgEvent = ev
			break
		}
	}
	if msgEvent == nil {
		t.Fatalf("no MESSAGE event received, events: %v", eventKinds(events))
	}

	text := msgEvent.GetText()
	if !strings.Contains(text, "Review comment on src/calc.go, lines 15-18:") {
		t.Errorf("message lacks file and line range header: %q", text)
	}
	if !strings.Contains(text, "> for i := 0; i <= n; i++ {") {
		t.Errorf("message lacks quoted excerpt: %q", text)
	}
	if !strings.Contains(text, "Check boundary conditions") {
		t.Errorf("message lacks comment text: %q", text)
	}

	// Verify the fake worker acknowledges the message.
	var received bool
	for _, ev := range events {
		if strings.HasPrefix(ev.GetText(), "received: Review comment on src/calc.go") {
			received = true
			break
		}
	}
	if !received {
		t.Errorf("worker did not process message: %v", eventTexts(events))
	}

	// The worker's end writes its copy of the task, older than the comment: the comment stays.
	if got := e.ended(t, task.GetId()).GetComments(); len(got) != 1 || got[0].GetSentTo() != task.GetId() {
		t.Errorf("comments after the worker ended = %v, want the one sent to it", got)
	}
}

// TestCommentTurnEnded verifies that when a task has ended, TurnComments continues the task
// if its provider can resume, or spawns a correction task in the same azima whose prompt quotes
// each comment with its code excerpt.
func TestCommentTurnEnded(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()

	// Case 1: Continuable task (fake provider with session and worktree).
	{
		e := up(t, t.TempDir())
		wishID, _ := e.wish(t, gitRepo(t))
		first := e.ended(t, e.mustSpawn(t, wishID, "Initial implementation", "text first\nusage 100 10 0.01", nil).GetId())
		if first.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Fatalf("first run status = %v, want DONE", first.GetStatus())
		}

		// Add comment.
		_, err := e.tasks.Comment(ctx, connect.NewRequest(&planv1.TaskServiceCommentRequest{
			TaskId:  first.GetId(),
			File:    "app/main.go",
			Line:    5,
			EndLine: 5,
			Text:    "Fix typo here",
			Excerpt: "print(\"helllo\")",
		}))
		if err != nil {
			t.Fatalf("Comment failed: %v", err)
		}

		// Turn comments into continuation.
		res, err := e.tasks.TurnComments(ctx, connect.NewRequest(&planv1.TaskServiceTurnCommentsRequest{
			TaskId: first.GetId(),
			By:     planv1.Closer_CLOSER_DEVELOPER,
		}))
		if err != nil {
			t.Fatalf("TurnComments failed: %v", err)
		}
		if !res.Msg.GetContinued() {
			t.Errorf("res.Continued = false, want true for continuable task")
		}
		if res.Msg.GetTask().GetId() != first.GetId() {
			t.Errorf("continued task ID = %q, want same ID %q", res.Msg.GetTask().GetId(), first.GetId())
		}

		// Wait for continued turn to finish.
		again := e.ended(t, first.GetId())
		if got := again.GetComments()[0].GetSentTo(); got != first.GetId() {
			t.Errorf("comment went to %q, want the continued task %s", got, first.GetId())
		}
		if p, err := lastPrompt(ctx, e.db, first.GetId()); err != nil || !strings.Contains(p, "Fix typo here") {
			t.Errorf("continued prompt = %q, %v; want the comment", p, err)
		}
		// The comment went to a worker: turning comments again finds none waiting, instead of a second turn.
		_, err = e.tasks.TurnComments(ctx, connect.NewRequest(&planv1.TaskServiceTurnCommentsRequest{TaskId: first.GetId()}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("second TurnComments = %v, want FailedPrecondition", err)
		}
	}

	// Case 2: Non-continuable task (spawns correction task in the same azima).
	{
		repo := gitRepo(t)
		e := up(t, t.TempDir())
		wishID, projectID := e.wish(t, repo)

		// Create an Azima and a finished task under it.
		azimaID := store.NewID()
		taskID := store.NewID()
		err := e.db.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
				return err
			}
			azima := &planv1.Task{
				Id:         azimaID,
				WishId:     wishID,
				ProjectId:  projectID,
				Code:       "A1",
				Title:      "First feature azima",
				Kind:       planv1.TaskKind_TASK_KIND_AZIMA,
				Status:     planv1.TaskStatus_TASK_STATUS_RUNNING,
				CreateTime: timestamppb.Now(),
			}
			if err := tx.Put(azima); err != nil {
				return err
			}
			task := &planv1.Task{
				Id:         taskID,
				WishId:     wishID,
				ProjectId:  projectID,
				Code:       "W1",
				Title:      "Non-resumable finished task",
				PartOf:     azimaID,
				Status:     planv1.TaskStatus_TASK_STATUS_DONE,
				Scheduled:  false, // Not scheduled by this harness session -> cannot be continued
				Provider:   planv1.Provider_PROVIDER_FAKE,
				Model:      "fake-model",
				CreateTime: timestamppb.Now(),
				Comments: []*planv1.TaskComment{
					{
						Id:         store.NewID(),
						File:       "lib/math.go",
						Line:       10,
						EndLine:    12,
						Commit:     "abc1234",
						Excerpt:    "func Sqrt(x float64) float64 {\n\treturn 0\n}",
						Text:       "Implement the actual square root algorithm",
						CreateTime: timestamppb.Now(),
						Resolved:   false,
					},
					{
						Id:         store.NewID(),
						File:       "lib/math.go",
						Line:       20,
						EndLine:    20,
						Commit:     "abc1234",
						Excerpt:    "const Pi = 3.14",
						Text:       "Use higher precision for Pi",
						CreateTime: timestamppb.Now(),
						Resolved:   false,
					},
				},
			}
			return tx.Put(task)
		})
		if err != nil {
			t.Fatal(err)
		}

		res, err := e.tasks.TurnComments(ctx, connect.NewRequest(&planv1.TaskServiceTurnCommentsRequest{
			TaskId: taskID,
		}))
		if err != nil {
			t.Fatalf("TurnComments failed: %v", err)
		}
		if res.Msg.GetContinued() {
			t.Errorf("res.Continued = true, want false")
		}
		spawned := res.Msg.GetTask()
		if spawned.GetId() == taskID {
			t.Errorf("spawned task has same ID as original task")
		}
		if spawned.GetPartOf() != azimaID {
			t.Errorf("spawned task PartOf = %q, want azima %q", spawned.GetPartOf(), azimaID)
		}
		if len(spawned.GetDependsOn()) != 1 || spawned.GetDependsOn()[0] != taskID {
			t.Errorf("spawned task DependsOn = %v, want [%s]", spawned.GetDependsOn(), taskID)
		}
		original, err := store.Get[*planv1.Task](ctx, e.db, taskID)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range original.GetComments() {
			if c.GetSentTo() != spawned.GetId() {
				t.Errorf("comment %s went to %q, want the correction task %s", c.GetFile(), c.GetSentTo(), spawned.GetId())
			}
		}
		p, err := lastPrompt(ctx, e.db, spawned.GetId())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(p, "Address review comments on W1:") {
			t.Errorf("prompt lacks header: %s", p)
		}
		if !strings.Contains(p, "File lib/math.go, lines 10-12:") || !strings.Contains(p, "Implement the actual square root algorithm") {
			t.Errorf("prompt lacks first comment: %s", p)
		}
		if !strings.Contains(p, "> func Sqrt(x float64) float64 {") {
			t.Errorf("prompt lacks first comment excerpt: %s", p)
		}
		if !strings.Contains(p, "File lib/math.go, line 20:") || !strings.Contains(p, "Use higher precision for Pi") {
			t.Errorf("prompt lacks second comment: %s", p)
		}
	}

	// Preconditions:
	// Running task cannot turn comments.
	{
		e := up(t, t.TempDir())
		wishID, _ := e.wish(t, t.TempDir())
		task := e.spawn(t, wishID, "wait")
		_, err := e.tasks.TurnComments(ctx, connect.NewRequest(&planv1.TaskServiceTurnCommentsRequest{TaskId: task.GetId()}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("expected FailedPrecondition on running task, got %v", err)
		}
		_, _ = e.h.Stop(ctx, "test", &planv1.TaskServiceStopRequest{TaskId: task.GetId()})
	}
}

// TestCommentResolution verifies that comments survive the task's merge and show as resolved
// once a later commit changes those lines.
func TestCommentResolution(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()

	repo := gitRepo(t)
	calcGo := filepath.Join(repo, "calc.go")
	initialContent := `package calc

func Add(a, b int) int {
	return a + b
}

func Sub(a, b int) int {
	return a - b
}
`
	if err := os.WriteFile(calcGo, []byte(initialContent), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "--quiet", "-m", "Initial calc.go"}} {
		if _, err := git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	c1, err := git(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	e := up(t, t.TempDir())
	wishID, projectID := e.wish(t, repo)

	taskBranch := "djinn/w1-calc"
	if _, err := git(ctx, repo, "checkout", "-b", taskBranch); err != nil {
		t.Fatal(err)
	}

	taskID := store.NewID()
	err = e.db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
			return err
		}
		return tx.Put(&planv1.Task{
			Id:         taskID,
			WishId:     wishID,
			ProjectId:  projectID,
			Code:       "W1",
			Title:      "Calc implementation",
			Branch:     taskBranch,
			Status:     planv1.TaskStatus_TASK_STATUS_DONE,
			CreateTime: timestamppb.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add comment on lines 3-5 (func Add) referring to commit c1.
	res, err := e.tasks.Comment(ctx, connect.NewRequest(&planv1.TaskServiceCommentRequest{
		TaskId:  taskID,
		File:    "calc.go",
		Line:    3,
		EndLine: 5,
		Commit:  c1,
		Text:    "Add overflow check here",
	}))
	if err != nil {
		t.Fatal(err)
	}
	commentID := res.Msg.GetComment().GetId()

	// 1. Commit C2 on task branch that changes Sub (lines 7-9), NOT lines 3-5.
	subModified := `package calc

func Add(a, b int) int {
	return a + b
}

func Sub(a, b int) int {
	// modified Sub
	return a - b
}
`
	if err := os.WriteFile(calcGo, []byte(subModified), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "--quiet", "-m", "Modify Sub"}} {
		if _, err := git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}

	// Trigger resolution check via Tasks.Get.
	getRes, err := e.tasks.Get(ctx, connect.NewRequest(&planv1.TaskServiceGetRequest{TaskId: taskID}))
	if err != nil {
		t.Fatal(err)
	}
	tComments := getRes.Msg.GetTask().GetComments()
	if len(tComments) != 1 {
		t.Fatalf("comments count = %d, want 1", len(tComments))
	}
	if tComments[0].GetResolved() {
		t.Errorf("comment resolved prematurely after editing different lines")
	}

	// 2. Commit C3 on task branch that modifies Add (line 4).
	addModified := `package calc

func Add(a, b int) int {
	// modified Add
	return a + b
}

func Sub(a, b int) int {
	// modified Sub
	return a - b
}
`
	if err := os.WriteFile(calcGo, []byte(addModified), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "--quiet", "-m", "Modify Add"}} {
		if _, err := git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	c3, err := git(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	// Trigger resolution check via Tasks.Get.
	getRes, err = e.tasks.Get(ctx, connect.NewRequest(&planv1.TaskServiceGetRequest{TaskId: taskID}))
	if err != nil {
		t.Fatal(err)
	}
	tComments = getRes.Msg.GetTask().GetComments()
	if !tComments[0].GetResolved() {
		t.Errorf("comment was not marked resolved after modifying its lines")
	}
	if tComments[0].GetResolvedCommit() != c3 {
		t.Errorf("ResolvedCommit = %q, want %q", tComments[0].GetResolvedCommit(), c3)
	}
	if tComments[0].GetResolveTime() == nil {
		t.Errorf("ResolveTime is nil")
	}
	// The resolution is stored, journaled as Djinn's, not only answered.
	stored, err := store.Get[*planv1.Task](ctx, e.db, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.GetComments()[0].GetResolved() {
		t.Errorf("stored comment is not resolved")
	}
	resolves, err := store.Commands(ctx, e.db, func(cmd store.Command) bool { return cmd.Method == methodResolve })
	if err != nil || len(resolves) != 1 || resolves[0].Actor != actorHarness {
		t.Errorf("journaled resolutions = %+v, %v; want 1 by %s", resolves, err, actorHarness)
	}

	// 3. Verify comments survive merge into target branch.
	// Mark task as integrated at C3.
	err = e.db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
			return err
		}
		t, err := store.Get[*planv1.Task](ctx, tx, taskID)
		if err != nil {
			return err
		}
		// Reset resolved state to verify post-merge resolution on target branch.
		t.Comments[0].Resolved = false
		t.Comments[0].ResolvedCommit = ""
		t.Comments[0].ResolveTime = nil
		t.Integration = &planv1.TaskIntegration{
			State:      planv1.IntegrationState_INTEGRATION_STATE_COMMITTED,
			Sha:        c3,
			UpdateTime: timestamppb.Now(),
		}
		return tx.Put(t)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Checkout base branch and fast-forward or merge C3.
	if plan.CheckedOutBranch(ctx, repo) == taskBranch {
		if _, err := git(ctx, repo, "checkout", "master"); err != nil {
			_, _ = git(ctx, repo, "checkout", "main")
		}
	}
	if _, err := git(ctx, repo, "merge", "--quiet", c3); err != nil {
		t.Fatal(err)
	}

	// Commit C4 on base branch modifying Add further.
	postMergeContent := `package calc

func Add(a, b int) int {
	// further modified Add in main branch
	return a + b
}

func Sub(a, b int) int {
	return a - b
}
`
	if err := os.WriteFile(calcGo, []byte(postMergeContent), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "--quiet", "-m", "Post-merge changes on main"}} {
		if _, err := git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	c4, err := git(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	// Trigger resolution check via Tasks.Get on merged task.
	getRes, err = e.tasks.Get(ctx, connect.NewRequest(&planv1.TaskServiceGetRequest{TaskId: taskID}))
	if err != nil {
		t.Fatal(err)
	}
	tComments = getRes.Msg.GetTask().GetComments()
	if len(tComments) != 1 || tComments[0].GetId() != commentID {
		t.Fatalf("comment did not survive merge: %v", tComments)
	}
	if !tComments[0].GetResolved() {
		t.Errorf("comment on merged task was not resolved after target branch changes")
	}
	if tComments[0].GetResolvedCommit() != c4 {
		t.Errorf("ResolvedCommit = %q, want %q", tComments[0].GetResolvedCommit(), c4)
	}
}

// TestCommentFile: the command line makes a comment's file absolute from the folder it runs in; a path inside the
// task's worktree or its project's folder comes back relative to it, as the diff names it. Others stay as they were.
func TestCommentFile(t *testing.T) {
	t.Parallel()
	worktree := filepath.Join(t.TempDir(), "w1")
	project := filepath.Join(t.TempDir(), "lamp")
	for _, c := range []struct{ file, want string }{
		{"", ""},
		{"src/lamp.go", "src/lamp.go"},
		{"./src/../lamp.txt", "lamp.txt"},
		{filepath.Join(worktree, "src", "lamp.go"), "src/lamp.go"},
		{filepath.Join(project, "lamp.txt"), "lamp.txt"},
	} {
		if got := commentFile(c.file, worktree, project); got != c.want {
			t.Errorf("commentFile(%q) = %q, want %q", c.file, got, c.want)
		}
	}
	// Outside both, nothing to make relative: the path stays whole, without its root.
	outside := filepath.Join(t.TempDir(), "notes.md")
	if got := commentFile(outside, worktree, project); got == "notes.md" || !strings.HasSuffix(got, "/notes.md") {
		t.Errorf("commentFile(%q) = %q, want the whole path", outside, got)
	}
}
