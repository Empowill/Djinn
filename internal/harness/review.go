package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
)

// Review (T07): a task whose worktree holds changes not committed is not merged, and nothing commits them blindly:
// debug output, scratch files or artifacts would go in with the work. Djinn starts a review worker by itself: a work
// task part of the same azima, of the task's provider, in that task's worktree and on its branch, the files and their
// diff in its first prompt. It judges each path: it commits what belongs to the task, leaves the rest where it is, not
// committed, and ends its final note with what it committed, what it left and why. Its branch then integrates like any
// task's, the reviewed work with it, the paths it named as left staying in the worktree; a path it left without naming
// it there starts another review. Past the project's correction_attempts, Djinn asks the person.

// How much of a task's prompt, of its worker's final note, and of the diff of its work not committed, a review worker's first prompt holds.
const (
	maxReviewPrompt = 4 << 10
	maxReviewNote   = 4 << 10
	maxReviewDiff   = 12 << 10
)

// uncommitted lists what the worktree of t holds not committed, as git status --porcelain says it, untracked files
// one by one, the project's .gitignore applying: none when it is clean. A worktree removed already holds nothing.
func uncommitted(ctx context.Context, t *planv1.Task) ([]string, error) {
	return uncommittedDir(ctx, t.GetWorktree())
}

// uncommittedDir lists what dir holds not committed, as git status --porcelain says it, untracked files
// one by one, the project's .gitignore applying: none when it is clean. A directory that does not exist holds nothing.
func uncommittedDir(ctx context.Context, dir string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	out, err := gitRaw(ctx, dir, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var lines []string
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		if f[0] == 'R' || f[0] == 'C' { // A rename or a copy: the path it came from follows.
			if i+1 < len(fields) {
				f += " <- " + fields[i+1]
			}
			i++
		}
		lines = append(lines, f)
	}
	return lines, nil
}

// statusPaths are the files of status lines, as uncommitted gives them.
func statusPaths(status []string) []string {
	out := make([]string, 0, len(status))
	for _, s := range status {
		p, _, _ := strings.Cut(s[3:], " <- ")
		out = append(out, p)
	}
	return out
}

// reviewUncommitted takes out of batch, tasks of wish in project whose work would be merged into branch, the ones whose
// worktree holds changes not committed: none of them is merged, and a review worker starts for each. It returns the
// others.
func (h *Harness) reviewUncommitted(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, branch string, batch []*planv1.Task,
) []*planv1.Task {
	var clean []*planv1.Task
	for _, t := range batch {
		status, err := uncommitted(ctx, t)
		if err != nil {
			why := "read its worktree: " + err.Error()
			h.settleIntegration(ctx, []*planv1.Task{t}, pending(branch, why), "integration: waiting: "+why, nil)
			continue
		}
		if len(status) == 0 {
			clean = append(clean, t)
			continue
		}
		if t.GetReview() != nil {
			// What a review worker left and named in its final note it left on purpose: the work integrates without it.
			if left := statusPaths(status); len(unnamed(h.finalNote(ctx, t.GetId()), left)) == 0 {
				h.Note(t.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: fmt.Sprintf(
					"integration: %s left not committed, as its note says: %s; its worktree keeps them", t.GetCode(),
					strings.Join(left, ", "))})
				clean = append(clean, t)
				continue
			}
		}
		// A review whose own work is left not committed, a path not named: the next one reviews the same work, its
		// attempts counted.
		group := []*planv1.Task{t}
		var attempts int32
		if r := t.GetReview(); r != nil {
			attempts = r.GetAttempt()
			before, err := tasksByID(ctx, h.store, r.GetTaskIds())
			if err != nil {
				logx.L().Error("read the reviewed tasks", "loop", "review", "task", t.GetCode(), "err", err)
			}
			group = append(before, t)
		}
		files := strings.Join(statusPaths(status), ", ")
		in := &planv1.TaskIntegration{
			State: planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED, Branch: branch, Reason: "uncommitted: " + files,
		}
		h.review(ctx, wish, project, settings, group, in, fmt.Sprintf(
			"integration: uncommitted: %s's worktree holds changes not committed (%s); not merged, nothing committed", t.GetCode(), files),
			attempts, true)
	}
	return clean
}

// review settles the integration of group, tasks whose work is left not committed in the worktree of the first one,
// after attempts review workers: another one starts when more are allowed and the project's correction_attempts are
// not spent; otherwise Djinn tells the lead. text is the event that says why. It says what Djinn did, for the lead.
func (h *Harness) review(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, group []*planv1.Task,
	in *planv1.TaskIntegration, text string, attempts int32, more bool,
) string {
	if status, err := uncommitted(ctx, group[0]); err == nil && len(status) == 0 {
		// Committed meanwhile (a review that failed after its commit, the person): the work waits to be merged.
		left := group[0].GetCode() + "'s worktree holds nothing not committed any more: it waits to be merged"
		h.settleIntegration(ctx, group, pending(in.GetBranch(), ""), text+"; "+left, nil)
		h.kickIntegrate()
		return "Djinn starts no review: " + left
	}
	in = proto.CloneOf(in)
	in.State, in.ReviewedBy, in.QuestionId, in.Attempts = planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED, "", "", attempts
	if more && int(attempts) < settings.CorrectionAttempts {
		r, err := h.spawnReview(ctx, wish, project, group, in.GetBranch(), attempts+1)
		if err == nil {
			in.ReviewedBy, in.Attempts, in.Reason = r.GetId(), attempts+1, "uncommitted: reviewed by "+r.GetCode()
			h.settleIntegration(ctx, group, in, fmt.Sprintf("%s; %s reviews it, attempt %d of %d", text, r.GetCode(),
				attempts+1, settings.CorrectionAttempts), nil)
			return didStart(r)
		}
		text += "; the review worker could not start: " + err.Error()
	}
	c := codes(group)
	line := fmt.Sprintf("Djinn: the work of %s into %s has uncommitted changes: %s. The move is yours: retry a review, drop the work (djinn task done --drop / djinn task cancel), or leave it waiting.",
		c, in.GetBranch(), headline(in.GetReason()))
	h.tellLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_MOVE, wish.GetId(), c, line)
	in.QuestionId = ""
	text += "; left to the lead"
	h.settleIntegration(ctx, group, in, text, nil)
	return line
}

// spawnReview starts the attempt-th review worker of group's work, left not committed in the worktree of its first task:
// a work task on the project's provider and model, not that task's (Q95), part of its azima, in that worktree and on
// its branch, the files and their diff in its first prompt.
func (h *Harness) spawnReview(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, group []*planv1.Task, branch string, attempt int32,
) (*planv1.Task, error) {
	from := group[0]
	status, err := uncommitted(ctx, from)
	if err != nil {
		return nil, err
	}
	if len(status) == 0 {
		return nil, errors.New(from.GetCode() + "'s worktree holds nothing not committed any more")
	}
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil {
		return nil, err
	}
	prompt, err := firstPrompt(h.store, from.GetId())
	if err != nil {
		prompt = "(" + err.Error() + ")"
	}
	var unsaid []string // What the review before this one left without naming it.
	if last := group[len(group)-1]; last.GetReview() != nil {
		unsaid = unnamed(h.finalNote(ctx, last.GetId()), statusPaths(status))
	}
	review := &planv1.TaskReview{TaskIds: ids(group), Attempt: attempt}
	azima, note := h.azimaOf(ctx, from)
	r, err := h.spawn(context.WithoutCancel(ctx), planv1connect.TaskServiceSpawnProcedure, &planv1.TaskServiceSpawnRequest{
		WishId: wish.GetId(), ProjectId: project.GetId(), Title: mergeTitle(servedTasks(ctx, h.store, group), "work left not committed"),
		Prompt: reviewPrompt(from, prompt, h.finalNote(ctx, from.GetId()), status, uncommittedDiff(ctx, from.GetWorktree(), status),
			branch, settings, attempt, unsaid),
		PartOf: azima,
	}, &planv1.Task{Review: review, Branch: from.GetBranch(), Worktree: from.GetWorktree()})
	if err == nil && note != "" {
		h.Note(r.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: note})
	}
	return r, err
}

// reviewPrompt is the first prompt of the attempt-th review worker of the work from left not committed, as status and
// diff say it, on its way into branch, note the final note of from's worker: the task, its files, how to judge each
// one, and the line its final note ends with. unsaid are the paths the review before it left without naming them.
func reviewPrompt(
	from *planv1.Task, prompt, note string, status []string, diff, branch string, settings plan.Settings, attempt int32,
	unsaid []string,
) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Djinn does not integrate the work of %s into %s as it is: its worktree, where you are, on its branch %s, "+
		"holds changes no commit has. Djinn commits nothing blindly: you judge them, path by path. Your work is that commit, "+
		"not the task's: do not redo it or go on with it.\n\n", from.GetCode(), branch, from.GetBranch())
	fmt.Fprintf(&b, "The task, %s: %s. Its prompt:\n\n%s\n\n", from.GetCode(), from.GetTitle(), indent(clipTo(prompt, maxReviewPrompt)))
	if note == "" {
		note = "(none)"
	}
	fmt.Fprintf(&b, "Its worker's final note:\n\n%s\n\n", indent(clipTo(note, maxReviewNote)))
	lines := make([]string, len(status))
	for i, s := range status {
		lines[i] = s
		if hint := pathHint(s); hint != "" {
			lines[i] += "  (" + hint + ")"
		}
	}
	fmt.Fprintf(&b, "The paths not committed (`git status --porcelain`, the project's .gitignore applied):\n\n%s\n\n",
		indent(strings.Join(lines, "\n")))
	fmt.Fprintf(&b, "Their diff, against the branch's last commit:\n\n%s\n\n", indent(diff))
	fmt.Fprintf(&b, "Judge each path:\n"+
		"- Commit what belongs to the task's change: what its prompt asks for, what its final note describes, the tests "+
		"and documentation of it. A worker may leave real work not committed, even saying it commits nothing: commit it.\n"+
		"- Leave out what does not: scratch files, local probes, screenshots, logs, test and build outputs, generated files "+
		"the project does not track, env files, keys and other secrets, files of a kind the project ignores elsewhere. "+
		"When in doubt, leave it out and say so.\n"+
		"- Stage each path by its name (`git add -- <path>`), never all at once: `git add -A`, `git add .` and "+
		"`git commit -a` are refused. A file that mixes the change with debug lines: edit them out, then commit it.\n"+
		"- Leave what you leave out where it is, not committed: do not delete, revert or stash it (no `git clean`, "+
		"`git stash`, nor `git restore` of its changes). Djinn integrates your commit and keeps the worktree with what you "+
		"left. What your own checks write (a log, a test output) is yours to delete.\n"+
		"- Commit with a message in the repository's style (`git log` shows it). Do not push: Djinn integrates the branch "+
		"like any task's, its commit checks (%s) run through their gates.\n\n", settings.CommitGates())
	b.WriteString("End your final note with this line, alone, which the lead reads:\n\n" +
		"    Committed: <path>, <path>. Left: <path> (why), <path> (why).\n\n" +
		"Write `Committed: nothing.` or `Left: nothing.` when so. Name after `Left:` each path you leave, or a folder of them " +
		"ending with a slash: a path left without its name there is not judged, and Djinn starts another review.")
	if attempt > 1 {
		fmt.Fprintf(&b, "\n\nThis is attempt %d: the review before yours left changes not committed.", attempt)
	}
	if len(unsaid) > 0 {
		fmt.Fprintf(&b, "\n\nThe review before yours left these paths not committed without naming them after Left: in its "+
			"final note: %s.", strings.Join(unsaid, ", "))
	}
	return b.String()
}

// unnamed are the paths a review worker's final note does not say it left, in their order: named after the Left: of
// its report (reviewReport). A path it says it committed, still not committed, is not one it left.
func unnamed(note string, paths []string) []string {
	_, left, _ := strings.Cut(reviewReport(note), "Left:")
	var out []string
	for _, p := range paths {
		if !named(left, p) {
			out = append(out, p)
		}
	}
	return out
}

// named tells whether note names path, from the repository's root or from one of its folders down (src/b.txt names
// app/src/b.txt, from the project's folder), or names a folder holding it, ending with a slash (test-results/).
func named(note, path string) bool {
	parts := strings.Split(path, "/")
	for i := range parts {
		if namesPath(note, strings.Join(parts[i:], "/")) {
			return true
		}
		for j := i + 1; j < len(parts); j++ {
			if namesPath(note, strings.Join(parts[i:j], "/")+"/") {
				return true
			}
		}
	}
	return false
}

// namesPath tells whether text holds name as a whole path: not part of a longer one; a sentence's full stop after it
// aside.
func namesPath(text, name string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], name)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(name)
		from = start + 1
		if start > 0 && pathByte(text[start-1]) {
			continue
		}
		if strings.HasSuffix(name, "/") || end == len(text) || !pathByte(text[end]) {
			return true
		}
		if text[end] == '.' && (end+1 == len(text) || !pathByte(text[end+1])) {
			return true
		}
	}
}

// pathByte tells whether c may be part of a path, as a note writes it.
func pathByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("_-./@+~", c) >= 0 ||
		c >= 0x80
}

// Kinds of names a review worker weighs before committing a file: secrets, outputs, pictures.
var (
	secretNames = []string{".env", ".netrc", ".pgpass", "credentials.json", "id_rsa", "id_ecdsa", "id_ed25519"}
	secretExts  = []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore"}
	sampleExts  = []string{".example", ".sample", ".template", ".dist"}
	outputDirs  = []string{"node_modules", "test-results", "playwright-report", "coverage", "dist", "build", "out", "tmp",
		"temp", ".cache", "__pycache__", ".pytest_cache", "target"}
	outputExts = []string{".log", ".out", ".prof", ".pprof", ".trace", ".orig", ".rej", ".swp", ".tmp", ".bak"}
	imageExts  = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".webm", ".mp4", ".mov"}
)

// pathHint says what the name of a file a status line adds suggests, for the review worker to weigh: a secret, an
// output, a picture; "" for a tracked file, or a name that suggests nothing.
func pathHint(status string) string {
	if !strings.HasPrefix(status, "??") && status[0] != 'A' {
		return ""
	}
	path, _, _ := strings.Cut(status[3:], " <- ")
	base := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(base))
	switch {
	case slices.Contains(sampleExts, ext):
		return ""
	case slices.ContainsFunc(secretNames, func(n string) bool { return base == n || strings.HasPrefix(base, n+".") }),
		slices.Contains(secretExts, ext):
		return "a secret or an env file: never commit it"
	case slices.ContainsFunc(strings.Split(path, "/")[:strings.Count(path, "/")], func(d string) bool {
		return slices.Contains(outputDirs, d)
	}), slices.Contains(outputExts, ext):
		return "a build or test output, a log or a leftover: leave it out unless the change makes it"
	case slices.Contains(imageExts, ext):
		return "an image or a video: a screenshot, unless the change uses it"
	}
	return ""
}

// reviewReport is what a review worker's final note ends with, saying what it committed and what it left and why:
// from its last "Committed:" on, in one line, its bold marks aside; "" when it wrote none.
func reviewReport(note string) string {
	note = strings.ReplaceAll(note, "**", "")
	i := strings.LastIndex(note, "Committed:")
	if i < 0 {
		return ""
	}
	return strings.Join(strings.Fields(note[i:]), " ")
}

// uncommittedDiff is what the worktree wt holds not committed, status saying which files, as a diff clipped to
// maxReviewDiff bytes: the changes to tracked files against HEAD, then the text of each untracked one.
func uncommittedDiff(ctx context.Context, wt string, status []string) string {
	var b strings.Builder
	if d, err := gitRaw(ctx, wt, "diff", "--no-color", "--no-ext-diff", "HEAD"); err != nil {
		fmt.Fprintf(&b, "(%v)\n", err)
	} else {
		b.WriteString(d)
	}
	for _, s := range status {
		if b.Len() > maxReviewDiff {
			break
		}
		if !strings.HasPrefix(s, "??") {
			continue
		}
		name := s[3:]
		f, err := os.Open(filepath.Join(wt, filepath.FromSlash(name)))
		if err != nil {
			fmt.Fprintf(&b, "new file %s: %v\n", name, err)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, int64(maxReviewDiff-b.Len()+1)))
		_ = f.Close()
		switch {
		case err != nil:
			fmt.Fprintf(&b, "new file %s: %v\n", name, err)
		case bytes.IndexByte(data, 0) >= 0:
			fmt.Fprintf(&b, "new file %s: binary\n", name)
		default:
			fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n", name)
			for line := range strings.Lines(string(data)) {
				b.WriteString("+" + line)
			}
			if len(data) > 0 && data[len(data)-1] != '\n' {
				b.WriteString("\n")
			}
		}
	}
	return strings.TrimRight(clipTo(b.String(), maxReviewDiff), "\n")
}

// clipTo is s cut to n bytes, saying how much it left out.
func clipTo(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + fmt.Sprintf("\n… (%d bytes cut)", len(s)-n)
}

// indent puts four spaces before each line of s.
func indent(s string) string {
	var b strings.Builder
	for line := range strings.Lines(s) {
		b.WriteString("    " + line)
	}
	return b.String()
}

// settleReviewed records the work the review workers of batch reviewed as committed with theirs, in in.Sha:
// "uncommitted: reviewed by W5". It returns the tasks it settled.
func (h *Harness) settleReviewed(ctx context.Context, batch []*planv1.Task, in *planv1.TaskIntegration) []*planv1.Task {
	var out []*planv1.Task
	for _, r := range batch {
		if r.GetReview() == nil {
			continue
		}
		group, err := tasksByID(ctx, h.store, r.GetReview().GetTaskIds())
		if err != nil {
			logx.L().Error("read the reviewed tasks", "loop", "review", "task", r.GetCode(), "err", err)
			continue
		}
		h.settleIntegration(ctx, group, &planv1.TaskIntegration{
			State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, Branch: in.GetBranch(), Sha: in.GetSha(),
			Reason: "uncommitted: reviewed by " + r.GetCode(), ReviewedBy: r.GetId(), Attempts: r.GetReview().GetAttempt(),
		}, fmt.Sprintf("integration: committed into %s as %s, its work not committed reviewed by %s", in.GetBranch(),
			short8(in.GetSha()), r.GetCode()), nil)
		out = append(out, group...)
	}
	return out
}

// reviewedIDs are the tasks the review workers of batch reviewed.
func reviewedIDs(batch []*planv1.Task) []string {
	var out []string
	for _, t := range batch {
		out = append(out, t.GetReview().GetTaskIds()...)
	}
	return out
}
