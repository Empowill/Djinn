package harness

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

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

// spawnOverlap is an open task of the wish and project whose files, write scopes or prompt overlap with a task being
// spawned.
type spawnOverlap struct {
	code    string
	status  string
	diff    []string
	scopes  []string
	prompts []string
}

const spawnOverlapPrefix = "may overlap with "

// spawnOverlaps lists the open tasks of wish in project whose branch diff, write scopes, or paths, packages and specs
// named in their prompt overlap with task's write scopes or prompt. Tasks already ordered before or after task in the
// DAG are excluded.
func (h *Harness) spawnOverlaps(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, task, parent *planv1.Task, prompt string,
	tasks []*planv1.Task,
) []spawnOverlap {
	if !project.GetGit() || !touchesFiles(task) || task.GetKind() == planv1.TaskKind_TASK_KIND_MOCKUP ||
		questionWorker(task) || task.GetReview() != nil {
		return nil
	}
	var wishTasks, candidates []*planv1.Task
	for _, o := range tasks {
		if o.GetWishId() != wish.GetId() {
			continue
		}
		wishTasks = append(wishTasks, o)
	}
	wishTasks = append(wishTasks, task)
	for _, o := range wishTasks {
		if o.GetId() == task.GetId() || o.GetProjectId() != project.GetId() {
			continue
		}
		if parent != nil && o.GetId() == parent.GetId() {
			continue
		}
		if !touchesFiles(o) || o.GetKind() == planv1.TaskKind_TASK_KIND_MOCKUP ||
			questionWorker(o) || o.GetReview() != nil {
			continue
		}
		if orderedInDAG(task, o, wishTasks) {
			continue
		}
		candidates = append(candidates, o)
	}
	if len(candidates) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, overlapsTimeout)
	defer cancel()
	_ = plan.FillPrompts(ctx, h.store, candidates)

	repo := h.loadRepoIndex(ctx, project)
	candFiles := make([]*planv1.TaskFiles, len(candidates))
	for i, o := range candidates {
		f := h.taskFiles(ctx, wish, project, o)
		candFiles[i] = f
		for _, p := range f.GetPaths() {
			repo.add(p)
		}
	}
	for _, p := range rawSlashPaths(task.GetTitle(), prompt, repo) {
		repo.add(p)
	}
	for _, o := range candidates {
		for _, p := range rawSlashPaths(o.GetTitle(), o.GetPrompt(), repo) {
			repo.add(p)
		}
	}

	tPrompts := promptPaths(task.GetTitle(), prompt, repo)
	tAllPaths := mergePaths(task.GetWriteScopes(), tPrompts)
	if len(tAllPaths) == 0 {
		return nil
	}
	tAll := &planv1.TaskFiles{
		Source: planv1.TaskFilesSource_TASK_FILES_SOURCE_WRITE_SCOPES,
		Paths:  tAllPaths,
	}

	var out []spawnOverlap
	for i, o := range candidates {
		f := candFiles[i]
		var diffShared, scopeShared []string
		switch f.GetSource() {
		case planv1.TaskFilesSource_TASK_FILES_SOURCE_DIFF:
			diffShared = sharedPaths(tAll, f)
		case planv1.TaskFilesSource_TASK_FILES_SOURCE_WRITE_SCOPES:
			scopeShared = sharedPaths(tAll, f)
		}
		if len(o.GetWriteScopes()) > 0 && f.GetSource() != planv1.TaskFilesSource_TASK_FILES_SOURCE_WRITE_SCOPES {
			oScopes := &planv1.TaskFiles{
				Source: planv1.TaskFilesSource_TASK_FILES_SOURCE_WRITE_SCOPES,
				Paths:  o.GetWriteScopes(),
			}
			for _, p := range sharedPaths(tAll, oScopes) {
				if !slices.Contains(diffShared, p) {
					scopeShared = append(scopeShared, p)
				}
			}
		}
		oPrompts := &planv1.TaskFiles{
			Source: planv1.TaskFilesSource_TASK_FILES_SOURCE_WRITE_SCOPES,
			Paths:  promptPaths(o.GetTitle(), o.GetPrompt(), repo),
		}
		var promptShared []string
		for _, p := range sharedPaths(tAll, oPrompts) {
			if !slices.Contains(diffShared, p) && !slices.Contains(scopeShared, p) {
				promptShared = append(promptShared, p)
			}
		}
		if len(diffShared) == 0 && len(scopeShared) == 0 && len(promptShared) == 0 {
			continue
		}
		out = append(out, spawnOverlap{
			code:    o.GetCode(),
			status:  filesStatus(f),
			diff:    diffShared,
			scopes:  scopeShared,
			prompts: promptShared,
		})
	}
	slices.SortFunc(out, func(a, b spawnOverlap) int { return compareTaskCodes(a.code, b.code) })
	return out
}

func compareTaskCodes(a, b string) int {
	if len(a) > 1 && len(b) > 1 && a[0] == b[0] {
		na, erra := strconv.Atoi(a[1:])
		nb, errb := strconv.Atoi(b[1:])
		if erra == nil && errb == nil {
			return cmp.Compare(na, nb)
		}
	}
	return strings.Compare(a, b)
}

// formatSpawnOverlaps writes the warning for a newly spawned task that may overlap with open tasks, naming each task
// with its reason and suggesting --after.
func formatSpawnOverlaps(code string, overlaps []spawnOverlap) string {
	if len(overlaps) == 0 {
		return ""
	}
	parts := make([]string, 0, len(overlaps))
	codes := make([]string, 0, len(overlaps))
	for _, o := range overlaps {
		codes = append(codes, o.code)
		var reasons []string
		if len(o.diff) > 0 {
			reasons = append(reasons, "diff: "+pathsText(o.diff))
		}
		if len(o.scopes) > 0 {
			reasons = append(reasons, "write scopes: "+pathsText(o.scopes))
		}
		if len(o.prompts) > 0 {
			reasons = append(reasons, "prompts: "+pathsText(o.prompts))
		}
		parts = append(parts, fmt.Sprintf("%s (%s; %s)", o.code, o.status, strings.Join(reasons, "; ")))
	}
	return fmt.Sprintf("%s %s%s: place it with --after (djinn task depend %s --after %s, or --blocks), or bundle the work.",
		code, spawnOverlapPrefix, strings.Join(parts, ", "), code, strings.Join(codes, ","))
}

// spawnOverlapNote returns the overlap warning recorded on t when it was spawned; "" when none was recorded.
func (h *Harness) spawnOverlapNote(ctx context.Context, t *planv1.Task) string {
	events, err := store.List[*planv1.TaskEvent](ctx, h.store, store.Where{"task_id": t.GetId()})
	if err != nil {
		return ""
	}
	for _, ev := range events {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS &&
			strings.HasPrefix(ev.GetText(), t.GetCode()+" "+spawnOverlapPrefix) {
			return ev.GetText()
		}
	}
	return ""
}

// orderedInDAG reports whether a and b already have a dependency path between them in either direction.
func orderedInDAG(a, b *planv1.Task, tasks []*planv1.Task) bool {
	byID := make(map[string]*planv1.Task, len(tasks))
	for _, t := range tasks {
		byID[t.GetId()] = t
	}
	var reaches func(from, target string, seen map[string]bool) bool
	reaches = func(from, target string, seen map[string]bool) bool {
		if from == target {
			return true
		}
		if seen[from] {
			return false
		}
		seen[from] = true
		t := byID[from]
		if t == nil {
			return false
		}
		for _, dep := range t.GetDependsOn() {
			if reaches(dep, target, seen) {
				return true
			}
		}
		return false
	}
	return reaches(a.GetId(), b.GetId(), map[string]bool{}) || reaches(b.GetId(), a.GetId(), map[string]bool{})
}

type repoIndex struct {
	dir    string
	module string
	globs  []string
	files  map[string]bool
	dirs   map[string]bool
	byBase map[string][]string
}

func (h *Harness) loadRepoIndex(ctx context.Context, project *planv1.Project) repoIndex {
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil {
		settings = plan.ResolveSettings(nil, nil)
	}
	idx := repoIndex{
		dir:    project.GetDirectory(),
		module: readGoModule(project.GetDirectory()),
		globs:  append([]string{"gen/**"}, settings.Generated...),
		files:  map[string]bool{},
		dirs:   map[string]bool{},
		byBase: map[string][]string{},
	}
	if out, err := gitRaw(ctx, project.GetDirectory(), "ls-files", "-z"); err == nil {
		for p := range strings.SplitSeq(out, "\x00") {
			idx.add(p)
		}
	}
	return idx
}

func cleanRepoPath(p string) (string, bool) {
	cleaned, err := cleanScopes([]string{p})
	if err != nil || len(cleaned) != 1 || cleaned[0] == "" {
		return "", false
	}
	return cleaned[0], true
}

func (r *repoIndex) add(p string) {
	clean, ok := cleanRepoPath(p)
	if !ok || isGenerated(clean, r.globs) {
		return
	}
	if !r.files[clean] {
		r.files[clean] = true
		base := strings.ToLower(filepath.Base(clean))
		if !slices.Contains(r.byBase[base], clean) {
			r.byBase[base] = append(r.byBase[base], clean)
		}
	}
	for dir := filepath.ToSlash(filepath.Dir(clean)); dir != "." && dir != "" && dir != "/"; dir = filepath.ToSlash(filepath.Dir(dir)) {
		r.dirs[dir] = true
	}
}

func readGoModule(dir string) string {
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func mergePaths(a, b []string) []string {
	var out []string
	for _, list := range [][]string{a, b} {
		for _, p := range list {
			if p != "" && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	slices.Sort(out)
	return out
}

// rawSlashPaths returns the slash-containing paths named in title and prompt before basename resolution, so repoIndex
// can index files that open tasks plan to create.
func rawSlashPaths(title, prompt string, repo repoIndex) []string {
	prompt, _, _ = strings.Cut(prompt, "\n# Tilasms given as context\n")
	var out []string
	for _, tok := range promptTokens(title + "\n" + prompt) {
		clean, ok := classifyPromptToken(tok, repo, false)
		if ok && strings.Contains(clean, "/") && !slices.Contains(out, clean) {
			out = append(out, clean)
		}
	}
	return out
}

// promptPaths extracts the repository file paths, packages and specs named in title and prompt.
func promptPaths(title, prompt string, repo repoIndex) []string {
	prompt, _, _ = strings.Cut(prompt, "\n# Tilasms given as context\n")
	var out []string
	for _, tok := range promptTokens(title + "\n" + prompt) {
		clean, ok := classifyPromptToken(tok, repo, true)
		if ok && !slices.Contains(out, clean) {
			out = append(out, clean)
		}
	}
	slices.Sort(out)
	return out
}

func promptTokens(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		if unicode.IsSpace(r) {
			return true
		}
		switch r {
		case '`', '"', '\'', '(', ')', '[', ']', '{', '}', '<', '>', ',', ';', '*', '!', '?':
			return true
		}
		return false
	})
}

func classifyPromptToken(raw string, repo repoIndex, resolveBase bool) (string, bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimRight(s, ".:")
	if s == "" {
		return "", false
	}
	if rest, ok := strings.CutPrefix(s, "file://"); ok {
		s = rest
		if repo.dir != "" {
			prefix := strings.TrimSuffix(filepath.ToSlash(repo.dir), "/") + "/"
			if after, ok := strings.CutPrefix(s, prefix); ok {
				s = after
			}
		}
		if strings.HasPrefix(s, "/") {
			return "", false
		}
	}
	if strings.Contains(s, "://") {
		return "", false
	}
	s, _, _ = strings.Cut(s, "#")
	s, _, _ = strings.Cut(s, ":")
	s = strings.TrimSuffix(s, "/...")
	s = strings.TrimRight(s, ".")
	if repo.module != "" && strings.HasPrefix(s, repo.module+"/") {
		s = strings.TrimPrefix(s, repo.module+"/")
	}
	hadDotSlash := strings.HasPrefix(s, "./")
	clean, ok := cleanRepoPath(s)
	if !ok || strings.Contains(clean, "..") || isGenerated(clean, repo.globs) {
		return "", false
	}
	if strings.HasPrefix(clean, ".") && !repo.files[clean] && !repo.dirs[clean] {
		return "", false
	}
	if strings.Contains(clean, "/") {
		if repo.files[clean] || repo.dirs[clean] {
			return clean, true
		}
		if !validPathSegments(clean) {
			return "", false
		}
		top, _, _ := strings.Cut(clean, "/")
		if hadDotSlash || isPromptFileExt(filepath.Ext(clean)) || repo.dirs[top] || isRepoRootDir(top) {
			return clean, true
		}
		return "", false
	}
	if hadDotSlash && (repo.files[clean] || repo.dirs[clean] || isRepoRootDir(clean)) {
		return clean, true
	}
	ext := strings.ToLower(filepath.Ext(clean))
	if !isPromptFileExt(ext) || isProseDotWord(clean) || !validPathSegments(clean) {
		return "", false
	}
	if resolveBase {
		if matches := repo.byBase[strings.ToLower(clean)]; len(matches) == 1 {
			return matches[0], true
		}
	}
	return clean, true
}

func validPathSegments(p string) bool {
	for seg := range strings.SplitSeq(p, "/") {
		if seg == "" {
			return false
		}
		hasLetter := false
		for _, r := range seg {
			switch {
			case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
				hasLetter = true
			case (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.':
			default:
				return false
			}
		}
		if !hasLetter {
			return false
		}
	}
	return true
}

func isRepoRootDir(top string) bool {
	switch top {
	case "internal", "pkg", "cmd", "tools", "src", "lib", "app", "apps", "packages", "services",
		"api", "proto", "docs", "e2e", "test", "tests", "web", "ui", "scripts", "config",
		"locales", "lead", "plan", "wishicons":
		return true
	}
	return false
}

func isPromptFileExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".css", ".scss", ".html", ".md",
		".proto", ".sql", ".sh", ".yml", ".yaml", ".json", ".txt", ".txtpb", ".toml", ".py", ".rs":
		return true
	}
	return false
}

func isProseDotWord(word string) bool {
	switch strings.ToLower(word) {
	case "node.js", "next.js", "react.js", "vue.js", "three.js", "chart.js", "mermaid.js":
		return true
	}
	return false
}
