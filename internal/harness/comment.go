package harness

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// methodResolve: a later commit changed the lines of a task's diff comments; the request is the task.
const methodResolve = "harness/resolve"

// Comment stores a review comment on a line or range of lines of a task's diff. While the task's worker runs,
// the comment is sent to it as an instruction (djinn task send, with the file and lines quoted).
func (h *Harness) Comment(ctx context.Context, procedure string, req *planv1.TaskServiceCommentRequest) (*planv1.TaskServiceCommentResponse, error) {
	task, err := store.Get[*planv1.Task](ctx, h.store, req.GetTaskId())
	if err != nil {
		return nil, plan.Status(err)
	}
	text := strings.TrimSpace(req.GetText())
	if text == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("comment text is required"))
	}
	line := req.GetLine()
	if line < 1 {
		line = 1
	}
	endLine := req.GetEndLine()
	if endLine < line {
		endLine = line
	}

	commit := req.GetCommit()
	var projectDir string
	if task.GetProjectId() != "" {
		if project, err := store.Get[*planv1.Project](ctx, h.store, task.GetProjectId()); err == nil && project.GetGit() {
			projectDir = project.GetDirectory()
			if commit == "" {
				wish, _ := store.Get[*planv1.Wish](ctx, h.store, task.GetWishId())
				if _, head, err := h.resolveBaseAndHead(ctx, project, wish, task); err == nil {
					commit = head
				}
			}
			// The diff's head is the task's branch until it is merged: the comment keeps the commit it was then, for
			// a later commit to resolve it.
			if commit != "" {
				if sha, err := git(ctx, projectDir, "rev-parse", "--verify", "--quiet", commit+"^{commit}"); err == nil && sha != "" {
					commit = sha
				}
			}
		}
	}

	file := commentFile(req.GetFile(), task.GetWorktree(), projectDir)
	excerpt := req.GetExcerpt()
	if excerpt == "" && projectDir != "" && commit != "" && file != "" {
		excerpt = fileExcerpt(ctx, projectDir, commit, file, int(line), int(endLine))
	}

	comment := &planv1.TaskComment{
		Id:         store.NewID(),
		File:       file,
		Line:       line,
		EndLine:    endLine,
		Commit:     commit,
		Text:       text,
		CreateTime: timestamppb.Now(),
		Excerpt:    excerpt,
	}
	// While the task's worker runs, the comment goes to it as an instruction (djinn task send); otherwise it waits
	// for TurnComments.
	if _, err := h.Send(ctx, planv1connect.TaskServiceSendProcedure, &planv1.TaskServiceSendRequest{
		TaskId: task.GetId(),
		Text:   formatCommentInstruction(comment),
	}); err == nil {
		comment.SentTo = task.GetId()
	}

	var savedTask *planv1.Task
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		t, err := store.Get[*planv1.Task](ctx, tx, req.GetTaskId())
		if err != nil {
			return err
		}
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		t.Comments = append(t.Comments, comment)
		savedTask = t
		return tx.Put(t)
	})
	if err != nil {
		return nil, plan.Status(err)
	}

	h.notify()
	return &planv1.TaskServiceCommentResponse{
		Comment: comment,
		Task:    savedTask,
	}, nil
}

// commentFile is the file a comment is on, relative to the repository's root. The command line makes a relative path
// absolute, from the folder it runs in: a path inside the task's worktree or its project's folder comes back relative
// to it.
func commentFile(file string, roots ...string) string {
	if file == "" {
		return ""
	}
	if filepath.IsAbs(file) {
		for _, root := range roots {
			if root == "" {
				continue
			}
			if rel, err := filepath.Rel(root, file); err == nil && rel != "." && filepath.IsLocal(rel) {
				file = rel
				break
			}
		}
	}
	return strings.TrimPrefix(filepath.ToSlash(filepath.Clean(file)), "/")
}

// formatCommentInstruction formats a diff comment as an instruction for a running worker.
func formatCommentInstruction(c *planv1.TaskComment) string {
	var b strings.Builder
	lineDesc := fmt.Sprintf("line %d", c.GetLine())
	if c.GetEndLine() > c.GetLine() {
		lineDesc = fmt.Sprintf("lines %d-%d", c.GetLine(), c.GetEndLine())
	}
	if c.GetFile() != "" {
		fmt.Fprintf(&b, "Review comment on %s, %s:\n\n", c.GetFile(), lineDesc)
	} else {
		fmt.Fprintf(&b, "Review comment on %s:\n\n", lineDesc)
	}
	if exc := strings.TrimSpace(c.GetExcerpt()); exc != "" {
		for line := range strings.Lines(exc) {
			b.WriteString("> " + line)
		}
		if !strings.HasSuffix(exc, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(c.GetText())
	return b.String()
}

// fileExcerpt extracts lines from file at commit in dir.
func fileExcerpt(ctx context.Context, dir, commit, file string, startLine, endLine int) string {
	out, err := gitRaw(ctx, dir, "show", fmt.Sprintf("%s:%s", commit, file))
	if err != nil || out == "" {
		return ""
	}
	lines := strings.Split(out, "\n")
	if startLine < 1 || startLine > len(lines) {
		return ""
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	return strings.Join(lines[startLine-1:endLine], "\n")
}

// TurnComments turns the comments of an ended task that went to no worker and are not resolved into work: it
// continues the task when it can be continued (djinn task continue), or spawns a correction task in its azima whose
// prompt quotes each comment with its code excerpt. Each comment then records the task it went to.
func (h *Harness) TurnComments(ctx context.Context, procedure string, req *planv1.TaskServiceTurnCommentsRequest) (*planv1.TaskServiceTurnCommentsResponse, error) {
	task, err := store.Get[*planv1.Task](ctx, h.store, req.GetTaskId())
	if err != nil {
		return nil, plan.Status(err)
	}
	if !taskEnded(task.GetStatus()) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s has not ended: %s", task.GetCode(), short(task.GetStatus())))
	}

	_, _ = h.checkCommentsResolution(ctx, task)

	var waiting []*planv1.TaskComment
	for _, c := range task.GetComments() {
		if !c.GetResolved() && c.GetSentTo() == "" {
			waiting = append(waiting, c)
		}
	}
	if len(waiting) == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s has no comment waiting to be turned into work", task.GetCode()))
	}

	prompt := formatTurnCommentsPrompt(task, waiting)

	logx.Lock(&h.sched, "scheduler")
	h.mu.Lock()
	running := h.runs[task.GetId()] != nil
	h.mu.Unlock()
	contErr := h.continuable(ctx, task, running)
	h.sched.Unlock()

	var to *planv1.Task
	continued := contErr == nil
	if continued {
		to, err = h.Continue(ctx, planv1connect.TaskServiceContinueProcedure, &planv1.TaskServiceContinueRequest{
			TaskId: task.GetId(),
			Prompt: &prompt,
			By:     req.GetBy(),
		})
	} else {
		// The task cannot be continued: a correction task in its azima, after it when its work is done.
		azima, _ := h.azimaOf(ctx, task)
		var after []string
		if task.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE {
			after = []string{task.GetCode()}
		} else if b := task.GetBranch(); b != "" {
			prompt += fmt.Sprintf("\n\n%s ended %s: its work is on branch %s.", task.GetCode(), short(task.GetStatus()), b)
		}
		to, err = h.spawn(ctx, planv1connect.TaskServiceSpawnProcedure, &planv1.TaskServiceSpawnRequest{
			WishId:    task.GetWishId(),
			ProjectId: task.GetProjectId(),
			Title:     fmt.Sprintf("Address review comments on %s", task.GetCode()),
			Prompt:    prompt,
			Provider:  task.GetProvider(),
			Model:     task.GetModel(),
			PartOf:    azima,
			After:     after,
		}, nil)
	}
	if err != nil {
		return nil, err
	}

	sent := make(map[string]bool, len(waiting))
	for _, c := range waiting {
		sent[c.GetId()] = true
	}
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		t, err := store.Get[*planv1.Task](ctx, tx, task.GetId())
		if err != nil {
			return err
		}
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		for _, c := range t.GetComments() {
			if sent[c.GetId()] {
				c.SentTo = to.GetId()
			}
		}
		return tx.Put(t)
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.notify()
	if continued {
		if got, err := store.Get[*planv1.Task](ctx, h.store, task.GetId()); err == nil {
			to = got
		}
	}
	return &planv1.TaskServiceTurnCommentsResponse{
		Task:      to,
		Continued: continued,
	}, nil
}

// formatTurnCommentsPrompt formats the prompt for continuing or spawning a correction task from review comments.
func formatTurnCommentsPrompt(task *planv1.Task, comments []*planv1.TaskComment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Address review comments on %s:\n\n", task.GetCode())
	for i, c := range comments {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		lineDesc := fmt.Sprintf("line %d", c.GetLine())
		if c.GetEndLine() > c.GetLine() {
			lineDesc = fmt.Sprintf("lines %d-%d", c.GetLine(), c.GetEndLine())
		}
		if c.GetFile() != "" {
			fmt.Fprintf(&b, "File %s, %s:\n\n", c.GetFile(), lineDesc)
		} else {
			fmt.Fprintf(&b, "%s:\n\n", lineDesc)
		}
		if exc := strings.TrimSpace(c.GetExcerpt()); exc != "" {
			for line := range strings.Lines(exc) {
				b.WriteString("> " + line)
			}
			if !strings.HasSuffix(exc, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
		b.WriteString(c.GetText())
	}
	return b.String()
}

// checkCommentsResolution checks if any unresolved comments on task have been resolved by later commits modifying those lines.
func (h *Harness) checkCommentsResolution(ctx context.Context, task *planv1.Task) (bool, error) {
	if len(task.GetComments()) == 0 {
		return false, nil
	}
	var unresolved []*planv1.TaskComment
	for _, c := range task.GetComments() {
		if !c.GetResolved() {
			unresolved = append(unresolved, c)
		}
	}
	if len(unresolved) == 0 {
		return false, nil
	}

	if task.GetProjectId() == "" {
		return false, nil
	}
	project, err := store.Get[*planv1.Project](ctx, h.store, task.GetProjectId())
	if err != nil {
		return false, plan.Status(err)
	}
	if !project.GetGit() {
		return false, nil
	}
	dir := project.GetDirectory()

	// Determine current head to compare against.
	var currentHead string
	if task.GetIntegration() != nil && task.GetIntegration().GetSha() != "" {
		// Task was merged. Check integration branch / target branch or merge commit parent vs head.
		wish, _ := store.Get[*planv1.Wish](ctx, h.store, task.GetWishId())
		if targetBranch, _ := h.integrationBranch(ctx, wish, project); targetBranch != "" {
			if sha, err := git(ctx, dir, "rev-parse", targetBranch); err == nil && sha != "" {
				currentHead = sha
			}
		}
		if currentHead == "" {
			if sha, err := git(ctx, dir, "rev-parse", "HEAD"); err == nil && sha != "" {
				currentHead = sha
			}
		}
	} else {
		// Task not merged. Check worktree HEAD or branch HEAD.
		if task.GetWorktree() != "" {
			if sha, err := git(ctx, task.GetWorktree(), "rev-parse", "HEAD"); err == nil && sha != "" {
				currentHead = sha
			}
		}
		if currentHead == "" && task.GetBranch() != "" {
			if sha, err := git(ctx, dir, "rev-parse", task.GetBranch()); err == nil && sha != "" {
				currentHead = sha
			}
		}
		if currentHead == "" {
			if sha, err := git(ctx, dir, "rev-parse", "HEAD"); err == nil && sha != "" {
				currentHead = sha
			}
		}
	}

	if currentHead == "" {
		return false, nil
	}

	var changed bool
	now := timestamppb.Now()
	for _, c := range unresolved {
		commit := c.GetCommit()
		if commit == "" || commit == currentHead {
			continue
		}
		// Run diff between comment commit and currentHead on comment file.
		diffOut, err := gitRaw(ctx, dir, "diff", "-U0", commit+".."+currentHead, "--", c.GetFile())
		if err != nil && task.GetWorktree() != "" {
			diffOut, err = gitRaw(ctx, task.GetWorktree(), "diff", "-U0", commit+".."+currentHead, "--", c.GetFile())
		}
		if err != nil {
			continue
		}
		if diffTouchesRange(diffOut, int(c.GetLine()), int(c.GetEndLine())) {
			c.Resolved = true
			c.ResolvedCommit = currentHead
			c.ResolveTime = now
			changed = true
		}
	}

	if changed {
		err := h.store.Tx(ctx, func(tx *store.Tx) error {
			t, err := store.Get[*planv1.Task](ctx, tx, task.GetId())
			if err != nil {
				return err
			}
			if err := tx.Journal(actorHarness, methodResolve, task); err != nil {
				return err
			}
			for _, updated := range task.GetComments() {
				if updated.GetResolved() {
					for _, existing := range t.GetComments() {
						if existing.GetId() == updated.GetId() && !existing.GetResolved() {
							existing.Resolved = true
							existing.ResolvedCommit = updated.GetResolvedCommit()
							existing.ResolveTime = updated.GetResolveTime()
						}
					}
				}
			}
			return tx.Put(t)
		})
		if err != nil {
			return changed, plan.Status(err)
		}
		h.notify()
	}

	return changed, nil
}

var hunkHeaderRegex = regexp.MustCompile(`^@@\s+-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s+@@`)

// diffTouchesRange returns true if any hunk in diffOut modifies, deletes, or inserts within [line, endLine].
func diffTouchesRange(diffOut string, line, endLine int) bool {
	for lineStr := range strings.Lines(diffOut) {
		m := hunkHeaderRegex.FindStringSubmatch(lineStr)
		if m == nil {
			continue
		}
		oldStart, _ := strconv.Atoi(m[1])
		oldCount := 1
		if m[2] != "" {
			oldCount, _ = strconv.Atoi(m[2])
		}
		if oldCount == 0 {
			// Insertion at oldStart
			if (oldStart >= line && oldStart <= endLine) || (oldStart == 0 && line == 1) {
				return true
			}
		} else {
			hunkStart := oldStart
			hunkEnd := oldStart + oldCount - 1
			if max(hunkStart, line) <= min(hunkEnd, endLine) {
				return true
			}
		}
	}
	return false
}

func taskEnded(s planv1.TaskStatus) bool {
	switch s {
	case planv1.TaskStatus_TASK_STATUS_DONE,
		planv1.TaskStatus_TASK_STATUS_FAILED,
		planv1.TaskStatus_TASK_STATUS_STOPPED,
		planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
		return true
	}
	return false
}
