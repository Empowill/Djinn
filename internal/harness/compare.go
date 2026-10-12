package harness

import (
	"cmp"
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// What a build holds against the running Djinn (W468). A build proposed, or a newer Djinn ready at the path of the
// running one, is named by its commit, the integration branch and wish it comes from, and compared with the commit the
// running Djinn was built from (its version names it): the tasks merged since, the commits it adds, and those it lacks
// when the running Djinn holds commits it does not. No model: Git and the plan only.

// LocalBuild is the commit a Djinn built from a checkout comes from, as go tool task install stamps its version
// (local-<git describe --always --dirty>: local-1a2b3c4, local-v0.1.0-3-g1a2b3c4-dirty), and whether its tree had
// changes not committed; ok is false for any other version.
func LocalBuild(version string) (commit string, dirty, ok bool) {
	rest, ok := strings.CutPrefix(version, "local-")
	if !ok {
		return "", false, false
	}
	rest, dirty = strings.CutSuffix(rest, "-dirty")
	if i := strings.LastIndex(rest, "-g"); i >= 0 && lowerHex(rest[i+2:]) {
		rest = rest[i+2:]
	}
	return rest, dirty, len(rest) >= 4 && lowerHex(rest)
}

// lowerHex tells whether s is made of lowercase hexadecimal digits only.
func lowerHex(s string) bool {
	return s != "" && strings.Trim(s, "0123456789abcdef") == ""
}

// versionCommit is the commit a Djinn of version was built from, in the repository holding repo: its commit when built
// from a checkout, its tag's when released; "" for a development build, which names none, or when the repository holds
// neither. A tag is looked up among the refs only: git 2.47 still read refs/tags/local-g1a2b3c4, which names no tag, as
// a git describe name, the commit 1a2b3c4, and the git 2.55 of CI did not.
func versionCommit(ctx context.Context, repo, version string) string {
	if version == "" || version == "dev" {
		return ""
	}
	if commit, _, ok := LocalBuild(version); ok {
		return commitOf(ctx, repo, commit)
	}
	tag := "refs/tags/" + version
	if _, err := git(ctx, repo, "show-ref", "--verify", "--quiet", tag); err != nil {
		return ""
	}
	return commitOf(ctx, repo, tag)
}

// mergeSubject is the subject of a merge the integration makes (Merge branch 'w5-…' into feat/x): the branch merged,
// and the one it went into.
var mergeSubject = regexp.MustCompile(`^Merge branch '([^']+)' into (\S+)$`)

// compareTasks is the most tasks a comparison names: the commits' titles cover the rest.
const compareTasks = 100

// compare fills b, a build of project, with what it holds against the running Djinn when the project's repository
// holds the commit that one was built from: Base, Running, Continues, the tasks merged since (Tasks, Summaries), the
// commits it adds (Changes, ChangesCount) and those it lacks (Lacks, LacksCount). It leaves b as it is otherwise.
func (h *Harness) compare(ctx context.Context, project *planv1.Project, b *Built) {
	repo := project.GetDirectory()
	if b.Sha == "" || repo == "" {
		return
	}
	base := versionCommit(ctx, repo, h.version)
	if base == "" {
		return
	}
	b.Base = base
	running := h.runningBranch(ctx, project, base)
	b.Running = b.Branch != "" && running == b.Branch
	b.Continues = ""
	// Another branch whose build holds the running commit carries its line on: a wish's integration branch moved to
	// one cut at the running branch's tip (W527) is an update, not a parallel branch.
	if !b.Running && b.Branch != "" && b.Sha != base && isAncestor(ctx, repo, base, b.Sha) {
		b.Continues = cmp.Or(running, base[:min(8, len(base))])
	}
	b.Changes, b.ChangesCount = commitTitles(ctx, repo, base+".."+b.Sha)
	b.Lacks, b.LacksCount = commitTitles(ctx, repo, b.Sha+".."+base)
	b.Tasks, b.Summaries = nil, nil
	for _, t := range h.mergedTasks(ctx, project, base+".."+b.Sha) {
		b.Tasks = append(b.Tasks, t.GetCode())
		b.Summaries = append(b.Summaries, TaskSummary{Code: t.GetCode(), Title: t.GetTitle(), Summary: h.lastWords(ctx, t.GetId())})
	}
}

// commitTitles is the titles of the commits of span, merges aside, the latest first, at most pushTitles of them, and
// how many there are.
func commitTitles(ctx context.Context, repo, span string) ([]string, int) {
	n, err := git(ctx, repo, "rev-list", "--count", "--no-merges", span)
	if err != nil {
		return nil, 0
	}
	count, _ := strconv.Atoi(n)
	if count == 0 {
		return nil, 0
	}
	out, err := git(ctx, repo, "log", "--no-merges", "--format=%s", "-n", strconv.Itoa(pushTitles), span)
	if err != nil || out == "" {
		return nil, count
	}
	return strings.Split(out, "\n"), count
}

// mergedTasks is the tasks whose branch the integration merged in span, the latest first: each merge names the task's
// branch and the integration branch it went into, whose wish holds the task.
func (h *Harness) mergedTasks(ctx context.Context, project *planv1.Project, span string) []*planv1.Task {
	out, err := git(ctx, project.GetDirectory(), "log", "--merges", "--format=%s", span)
	if err != nil || out == "" {
		return nil
	}
	wishes := h.integrationWishes(ctx, project.GetId())
	byWish := map[string][]*planv1.Task{}
	seen := map[string]bool{}
	var tasks []*planv1.Task
	for line := range strings.SplitSeq(out, "\n") {
		m := mergeSubject.FindStringSubmatch(line)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		wish := wishes[m[2]]
		if wish == nil {
			continue
		}
		list, ok := byWish[wish.GetId()]
		if !ok {
			list, _ = store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
			byWish[wish.GetId()] = list
		}
		if i := slices.IndexFunc(list, func(t *planv1.Task) bool { return t.GetBranch() == m[1] }); i >= 0 {
			tasks = append(tasks, list[i])
			if len(tasks) == compareTasks {
				break
			}
		}
	}
	return tasks
}

// integrationWishes is the wishes that integrate into a branch of the project projectID, by that branch.
func (h *Harness) integrationWishes(ctx context.Context, projectID string) map[string]*planv1.Wish {
	wishes, _ := store.List[*planv1.Wish](ctx, h.store, nil)
	out := map[string]*planv1.Wish{}
	for _, w := range wishes {
		if b := plan.IntegrationBranchOf(w, projectID); b != "" {
			out[b] = w
		}
	}
	return out
}

// runningBranch is the branch the running Djinn was built from, base, in project: found once, then kept.
func (h *Harness) runningBranch(ctx context.Context, project *planv1.Project, base string) string {
	h.mu.Lock()
	branch, ok := h.builtFrom[project.GetId()]
	h.mu.Unlock()
	if ok {
		return branch
	}
	branch, _ = h.originOf(ctx, project, base)
	h.mu.Lock()
	if h.builtFrom == nil {
		h.builtFrom = map[string]string{}
	}
	h.builtFrom[project.GetId()] = branch
	h.mu.Unlock()
	return branch
}

// originOf is the branch the commit sha of project comes from, and its wish (nil for the main branch): among the
// wishes' integration branches and main that hold it, the one its subject names when the integration made it (Merge
// branch '…' into feat/x), else the one whose first-parent history holds it nearest its tip. "" when none holds it.
func (h *Harness) originOf(ctx context.Context, project *planv1.Project, sha string) (string, *planv1.Wish) {
	repo := project.GetDirectory()
	out, err := git(ctx, repo, "for-each-ref", "--contains", sha, "--format=%(refname:short)", "refs/heads/")
	if err != nil || out == "" {
		return "", nil
	}
	wishes := h.integrationWishes(ctx, project.GetId())
	settings, _ := plan.LoadSettings(h.home, project)
	main, _, _ := mainRef(ctx, repo, settings.MainBranch)
	var candidates []string
	for b := range strings.SplitSeq(out, "\n") {
		if wishes[b] != nil || b == main && main != "" {
			candidates = append(candidates, b)
		}
	}
	if len(candidates) == 0 {
		return "", nil
	}
	if subject, err := git(ctx, repo, "log", "-1", "--format=%s", sha); err == nil {
		if m := mergeSubject.FindStringSubmatch(subject); m != nil && slices.Contains(candidates, m[2]) {
			return m[2], wishes[m[2]]
		}
	}
	best, distance := "", -1
	for _, b := range candidates {
		d := firstParentDistance(ctx, repo, sha, b)
		if distance < 0 || d < distance {
			best, distance = b, d
		}
	}
	return best, wishes[best]
}

// firstParentDistance is how many commits of branch's first-parent history come after sha; past them all when that
// history does not hold sha, by how many commits branch has since.
func firstParentDistance(ctx context.Context, repo, sha, branch string) int {
	const far = 1 << 20
	if out, err := git(ctx, repo, "log", "--first-parent", "--format=%H", "-n", "2000", "refs/heads/"+branch); err == nil {
		if i := slices.IndexFunc(strings.Split(out, "\n"), func(c string) bool { return strings.HasPrefix(c, sha) }); i >= 0 {
			return i
		}
	}
	n, err := git(ctx, repo, "rev-list", "--count", sha+"..refs/heads/"+branch)
	if err != nil {
		return 2 * far
	}
	count, _ := strconv.Atoi(n)
	return far + count
}

// DescribeVersion names the newer Djinn of version, ready at the path of the running one, as a build: the project whose
// repository holds its commit, the branch and wish it comes from, and what it holds against the running Djinn. ok is
// false when no project holds its commit: a release no checkout knows yet, or a development build.
func (h *Harness) DescribeVersion(ctx context.Context, version string) (Built, bool) {
	if version == "" || version == "dev" {
		return Built{}, false
	}
	projects, err := store.List[*planv1.Project](ctx, h.store, nil)
	if err != nil {
		return Built{}, false
	}
	for _, p := range projects {
		if !p.GetGit() {
			continue
		}
		sha := versionCommit(ctx, p.GetDirectory(), version)
		if sha == "" {
			continue
		}
		b := Built{ProjectID: p.GetId(), Project: p.GetName(), Sha: sha}
		branch, wish := h.originOf(ctx, p, sha)
		b.Branch, b.WishID, b.WishTitle = branch, wish.GetId(), wish.GetTitle()
		h.compare(ctx, p, &b)
		return b, true
	}
	return Built{}, false
}
