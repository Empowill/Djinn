package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// overlapsTimeout bounds the git reads of one Overlaps: a brief never waits long on a repository.
const overlapsTimeout = 20 * time.Second

// Overlaps is TaskService.Overlaps: the files the open work of a wish touches, and the pairs of tasks of a project that
// touch the same ones. A task's diff that git cannot read leaves its paths unknown, never the call failed.
func (h *Harness) Overlaps(ctx context.Context, wishID string) (*planv1.TaskServiceOverlapsResponse, error) {
	wish, err := store.Get[*planv1.Wish](ctx, h.store, wishID)
	if err != nil {
		return nil, err
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wishID})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, overlapsTimeout)
	defer cancel()
	projects := map[string]*planv1.Project{}
	var files []*planv1.TaskFiles
	for _, t := range tasks {
		if !touchesFiles(t) {
			continue
		}
		project, ok := projects[t.GetProjectId()]
		if !ok {
			project, _ = store.Get[*planv1.Project](ctx, h.store, t.GetProjectId())
			projects[t.GetProjectId()] = project
		}
		if !project.GetGit() {
			continue
		}
		files = append(files, h.taskFiles(ctx, wish, project, t))
	}
	overlaps := overlapsOf(files)
	return &planv1.TaskServiceOverlapsResponse{
		Text: overlapsText(files, overlaps), Tasks: files, Overlaps: overlaps,
	}, nil
}

// OverlapsBrief is the Overlaps section of a wish's brief: the pairs of open tasks that touch the same files, and the
// planned tasks whose paths nobody knows; empty when there is neither.
func (h *Harness) OverlapsBrief(ctx context.Context, wishID string) string {
	res, err := h.Overlaps(ctx, wishID)
	if err != nil {
		return ""
	}
	unknown := unknownCodes(res.GetTasks())
	if len(res.GetOverlaps()) == 0 && len(unknown) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Overlaps\n\n")
	if len(res.GetOverlaps()) > 0 {
		b.WriteString("Open tasks that touch the same files: bundle new work on them into one, or order it with " +
			"`--after`; never side by side.\n\n")
		for _, o := range res.GetOverlaps() {
			b.WriteString(overlapLine(o))
		}
		b.WriteString("\n")
	}
	if len(unknown) > 0 {
		fmt.Fprintf(&b, "Planned without write scopes, their paths unknown: %s.\n\n", strings.Join(unknown, ", "))
	}
	fmt.Fprintf(&b, "`djinn task overlaps %s` lists each open task's files.\n", wishID)
	return b.String()
}

// touchesFiles tells a task whose files Overlaps reads: open work, or done work not merged yet, in a project, that
// may edit; never an azima, a watcher or an investigation, which edit nothing.
func touchesFiles(t *planv1.Task) bool {
	if t.GetProjectId() == "" || plan.IsAzima(t) || t.GetProvider() == planv1.Provider_PROVIDER_WATCH ||
		t.GetKind() == planv1.TaskKind_TASK_KIND_INVESTIGATION {
		return false
	}
	return openTask(t) || doneNotMerged(t)
}

// taskFiles is what t touches: its diff against its base once it ran, else its write scopes.
func (h *Harness) taskFiles(ctx context.Context, wish *planv1.Wish, project *planv1.Project, t *planv1.Task) *planv1.TaskFiles {
	out := &planv1.TaskFiles{
		TaskId: t.GetId(), Code: t.GetCode(), Title: t.GetTitle(), Status: t.GetStatus(), ProjectId: t.GetProjectId(),
		Source: planv1.TaskFilesSource_TASK_FILES_SOURCE_UNKNOWN,
	}
	if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_PENDING && t.GetStartTime() == nil {
		if scopes := t.GetWriteScopes(); len(scopes) > 0 {
			out.Source, out.Paths = planv1.TaskFilesSource_TASK_FILES_SOURCE_WRITE_SCOPES, scopes
		}
		return out
	}
	base, paths, err := h.diffPaths(ctx, wish, project, t)
	if err != nil {
		return out
	}
	settings, _ := plan.LoadSettings(h.home, project)
	globs := append([]string{"gen/**"}, settings.Generated...)
	paths = slices.DeleteFunc(paths, func(p string) bool { return isGenerated(p, globs) })
	out.Source, out.Base, out.Paths = planv1.TaskFilesSource_TASK_FILES_SOURCE_DIFF, base, paths
	return out
}

// errNoDiff is a task with neither worktree nor branch left to read.
var errNoDiff = errors.New("no worktree nor branch")

// diffPaths lists the files t changed from its base, the commit where its work leaves the branch it merges into: in
// its worktree, what the worker wrote, committed or not, untracked files included; without one, its branch.
func (h *Harness) diffPaths(ctx context.Context, wish *planv1.Wish, project *planv1.Project, t *planv1.Task) (string, []string, error) {
	dir, head := project.GetDirectory(), "HEAD"
	if wt := t.GetWorktree(); wt != "" {
		if _, err := os.Stat(wt); err == nil {
			dir = wt
		}
	}
	if dir == project.GetDirectory() {
		if t.GetBranch() == "" {
			return "", nil, errNoDiff
		}
		head = "refs/heads/" + t.GetBranch()
		if _, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", head+"^{commit}"); err != nil {
			return "", nil, errNoDiff
		}
	}
	base := ""
	if target := h.overlapTarget(ctx, wish, project, t); target != "" {
		base, _ = git(ctx, dir, "merge-base", head, "refs/heads/"+target)
	}
	if base == "" {
		base = branchStartCommit(ctx, project.GetDirectory(), t.GetBranch())
	}
	if base == "" {
		return "", nil, errNoDiff
	}
	args := []string{"diff", "--name-only", "-z", "--no-renames", base}
	if dir == project.GetDirectory() {
		args = append(args, head)
	}
	out, err := gitRaw(ctx, dir, args...)
	if err != nil {
		return "", nil, err
	}
	paths := splitNull(out)
	if dir != project.GetDirectory() {
		untracked, err := untrackedFiles(ctx, dir, "")
		if err != nil {
			return "", nil, err
		}
		paths = append(paths, untracked...)
	}
	slices.Sort(paths)
	return base, slices.Compact(paths), nil
}

// overlapTarget is the branch t's work merges into: its azima's when the wish pushes per azima and that branch exists,
// else the wish's integration branch in project, else the project's reference branch. It records nothing.
func (h *Harness) overlapTarget(ctx context.Context, wish *planv1.Wish, project *planv1.Project, t *planv1.Task) string {
	settings, _ := plan.LoadSettings(h.home, project)
	if strat, _ := plan.ResolveWishPushStrategy(wish, settings); strat == planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
		if azima, err := h.taskAzima(ctx, t); err == nil && azima != nil {
			b := plan.AzimaBranchName(azima)
			if _, err := git(ctx, project.GetDirectory(), "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
				return b
			}
		}
	}
	if b := plan.IntegrationBranchOf(wish, project.GetId()); b != "" {
		return b
	}
	b, _ := plan.ReferenceName(ctx, h.home, project)
	return b
}

func splitNull(s string) []string {
	var out []string
	for _, f := range strings.Split(s, "\x00") {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// overlapsOf pairs the tasks of a project whose paths meet, in the order of files: a task with paths unknown meets
// none.
func overlapsOf(files []*planv1.TaskFiles) []*planv1.TaskOverlap {
	var out []*planv1.TaskOverlap
	for i, a := range files {
		for _, b := range files[i+1:] {
			if a.GetProjectId() != b.GetProjectId() {
				continue
			}
			if paths := sharedPaths(a, b); len(paths) > 0 {
				out = append(out, &planv1.TaskOverlap{
					TaskIds: []string{a.GetTaskId(), b.GetTaskId()}, Codes: []string{a.GetCode(), b.GetCode()}, Paths: paths,
				})
			}
		}
	}
	return out
}

// sharedPaths are the paths both a and b touch: a file both changed, a file of one under a scope of the other, the
// narrower of two scopes that hold one another. Case aside, as write scopes compare.
func sharedPaths(a, b *planv1.TaskFiles) []string {
	if a.GetSource() == planv1.TaskFilesSource_TASK_FILES_SOURCE_UNKNOWN ||
		b.GetSource() == planv1.TaskFilesSource_TASK_FILES_SOURCE_UNKNOWN {
		return nil
	}
	var out []string
	for _, x := range a.GetPaths() {
		for _, y := range b.GetPaths() {
			if p := sharedPath(x, y); p != "" && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	slices.Sort(out)
	return out
}

// sharedPath is x or y when one holds the other (the same path, or a folder and what it holds): the narrower one.
func sharedPath(x, y string) string {
	lx, ly := strings.ToLower(x), strings.ToLower(y)
	switch {
	case lx == ly, strings.HasPrefix(lx, ly+"/"):
		return x
	case strings.HasPrefix(ly, lx+"/"):
		return y
	}
	return ""
}

// unknownCodes are the codes of the tasks whose paths nobody knows.
func unknownCodes(files []*planv1.TaskFiles) []string {
	var out []string
	for _, f := range files {
		if f.GetSource() == planv1.TaskFilesSource_TASK_FILES_SOURCE_UNKNOWN {
			out = append(out, f.GetCode())
		}
	}
	return out
}

// pathsShown is how many paths a line names before it counts the others.
const pathsShown = 8

// pathsText names the first paths, then how many others: "a, b (+3)".
func pathsText(paths []string) string {
	if len(paths) <= pathsShown {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s (+%d)", strings.Join(paths[:pathsShown], ", "), len(paths)-pathsShown)
}

func overlapLine(o *planv1.TaskOverlap) string {
	return fmt.Sprintf("- %s: %s\n", strings.Join(o.GetCodes(), " and "), pathsText(o.GetPaths()))
}

// filesStatus is where a task of Overlaps stands, in words: running, planned, done, not merged.
func filesStatus(f *planv1.TaskFiles) string {
	switch f.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_DONE:
		return "done, not merged"
	case planv1.TaskStatus_TASK_STATUS_PENDING:
		return "planned"
	}
	return strings.ToLower(strings.TrimPrefix(f.GetStatus().String(), "TASK_STATUS_"))
}

// overlapsText is Overlaps for a reader: a line per open task and what it touches, then the overlaps.
func overlapsText(files []*planv1.TaskFiles, overlaps []*planv1.TaskOverlap) string {
	if len(files) == 0 {
		return "No open work in a Git project."
	}
	var b strings.Builder
	b.WriteString("Open work and the files it touches (a diff against its base; planned: its write scopes):\n")
	for _, f := range files {
		fmt.Fprintf(&b, "- %s %s: ", f.GetCode(), filesStatus(f))
		switch {
		case f.GetSource() == planv1.TaskFilesSource_TASK_FILES_SOURCE_UNKNOWN &&
			f.GetStatus() == planv1.TaskStatus_TASK_STATUS_PENDING:
			b.WriteString("paths unknown, no write scopes\n")
		case f.GetSource() == planv1.TaskFilesSource_TASK_FILES_SOURCE_UNKNOWN:
			b.WriteString("paths unknown, no worktree nor branch to read\n")
		case f.GetSource() == planv1.TaskFilesSource_TASK_FILES_SOURCE_WRITE_SCOPES:
			fmt.Fprintf(&b, "write scopes %s\n", pathsText(f.GetPaths()))
		case len(f.GetPaths()) == 0:
			b.WriteString("no file changed yet\n")
		case len(f.GetPaths()) == 1:
			fmt.Fprintf(&b, "1 file: %s\n", f.GetPaths()[0])
		default:
			fmt.Fprintf(&b, "%d files: %s\n", len(f.GetPaths()), pathsText(f.GetPaths()))
		}
	}
	if len(overlaps) == 0 {
		b.WriteString("\nNo overlap: no two open tasks touch the same file.")
		return b.String()
	}
	b.WriteString("\nOverlaps (bundle new work on these files into one task, or order it with --after):\n")
	for _, o := range overlaps {
		b.WriteString(overlapLine(o))
	}
	return strings.TrimSuffix(b.String(), "\n")
}
