package harness

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/gitx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/render"
	"github.com/empowill/djinn/internal/store"
)

// worktreeDir is where a task's worktree goes: in Djinn's data folder, on the project's side, named after the
// task. Djinn writes nothing in the project's folder but what git worktree add records under .git/worktrees.
func worktreeDir(home, projectID, taskID string) string {
	return filepath.Join(home, "projects", projectID, "worktrees", taskID)
}

// scratchDir is the folder of a task outside any project, under Djinn's data folder.
func scratchDir(home, taskID string) string {
	return filepath.Join(home, "tasks", taskID)
}

// branchName is the branch of a task: the project's template (plan.DefaultBranch when empty) with {code} and {slug}
// its code and title as slugs, {uuid8} the last 8 characters of its UUIDv7, the random part. A placeholder that comes
// out empty takes the separator after it away, or else the one before it: W3 "!!!" is w3-89abcdef.
func branchName(template, code, title, taskID string) string {
	values := []string{"{code}", slug(code, 20), "{slug}", slug(title, 40), "{uuid8}", taskID[max(0, len(taskID)-8):]}
	var pairs []string
	for kv := range slices.Chunk(values, 2) {
		if kv[1] != "" {
			continue
		}
		for _, sep := range []string{"-", "_", ".", "/"} {
			pairs = append(pairs, kv[0]+sep, "", sep+kv[0], "")
		}
	}
	out := strings.NewReplacer(pairs...).Replace(cmp.Or(template, plan.DefaultBranch))
	return strings.NewReplacer(values...).Replace(out)
}

// accents are folded to their letter in a slug.
var accents = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "á", "a", "ç", "c", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "í", "i", "ô", "o", "ö", "o", "ó", "o", "ù", "u", "û", "u", "ü", "u", "ú", "u", "ñ", "n",
)

// slug keeps lower-case letters and digits, joined by single dashes, at most n characters cut on a dash.
func slug(s string, n int) string {
	s = accents.Replace(strings.ToLower(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	out := b.String()
	if len(out) > n {
		out = out[:n]
		if i := strings.LastIndexByte(out, '-'); i > 0 {
			out = out[:i]
		}
	}
	return strings.Trim(out, "-")
}

// git runs git in dir and returns its output, trimmed.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := gitRaw(ctx, dir, args...)
	return strings.TrimSpace(out), err
}

// gitRaw runs git in dir and returns its output as it is: a status's first column may be a space.
func gitRaw(ctx context.Context, dir string, args ...string) (string, error) {
	cmd, cancel := gitx.Command(ctx, dir, args...)
	defer cancel()
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		if missingIdentity(msg) {
			return "", fmt.Errorf("git %s: %w", args[0], errNoIdentity)
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return out.String(), nil
}

// noIdentity is what Djinn says when git refuses a merge or a commit for want of a name and an email: Djinn signs
// with the person's identity and never invents one, so the person sets it. The window knows it by its start, "git has
// no identity", and says it in its language (gitReason, src/data/format.ts).
const noIdentity = `git has no identity to sign Djinn's merges and commits: run git config --global user.name "Your Name" ` +
	`and git config --global user.email you@example.com (or the same without --global, in the project's repository)`

// errNoIdentity is a git command refused for want of an identity; its text is noIdentity.
var errNoIdentity = errors.New(noIdentity)

// missingIdentity tells whether msg, what git wrote on failing, is its refusal to sign without a name and an email.
func missingIdentity(msg string) bool {
	for _, s := range []string{"identity unknown", "Please tell me who you are", "empty ident name", "unable to auto-detect email address"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// checkIdentity tells whether git can sign a commit in the repository holding dir: errNoIdentity when it cannot.
func checkIdentity(ctx context.Context, dir string) error {
	_, err := git(ctx, dir, "var", "GIT_COMMITTER_IDENT")
	return err
}

// addWorktree creates the worktree path of the repository holding dir, on a new branch from its HEAD. It returns
// the folder the worker runs in: the project's own folder within the worktree.
func addWorktree(ctx context.Context, dir, path, branch string) (string, error) {
	return addWorktreeFrom(ctx, dir, path, branch, "HEAD")
}

// addWorktreeFrom is addWorktree, its branch starting from the commit from.
func addWorktreeFrom(ctx context.Context, dir, path, branch, from string) (string, error) {
	prefix, err := git(ctx, dir, "rev-parse", "--show-prefix")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	keep, done := gitx.Uncut(ctx) // A worktree begun is made whole though djinn up stops (W52).
	defer done()
	if _, err := git(keep, dir, "worktree", "add", "--quiet", "-b", branch, path, from); err != nil {
		return "", err
	}
	return filepath.Join(path, filepath.FromSlash(prefix)), nil
}

// reviewWorktree is the folder a review worker runs in: the project's folder within path, the worktree of the task
// whose work it reviews, which must still be there.
func reviewWorktree(ctx context.Context, dir, path string) (string, error) {
	prefix, err := git(ctx, dir, "rev-parse", "--show-prefix")
	if err != nil {
		return "", err
	}
	in := filepath.Join(path, filepath.FromSlash(prefix))
	if _, err := os.Stat(in); err != nil {
		return "", fmt.Errorf("the worktree it reviews is gone: %w", err)
	}
	return in, nil
}

// correctionWorktree adds the worktree path of a correction worker to the repository holding dir, on a new branch from
// the failure's base, and returns the project's folder within it. For a conflict, the branch that conflicted is merged
// there again, its conflicts left for the worker.
func correctionWorktree(ctx context.Context, dir, path, branch string, f *planv1.IntegrationFailure) (string, error) {
	prefix, err := git(ctx, dir, "rev-parse", "--show-prefix")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	keep, done := gitx.Uncut(ctx) // The worktree and its merge are made whole though djinn up stops (W52).
	defer done()
	if _, err := git(keep, dir, "worktree", "add", "--quiet", "-b", branch, path, f.GetBase()); err != nil {
		return "", err
	}
	if b := f.GetMergeBranch(); b != "" {
		if _, err := git(keep, path, "merge", "--no-ff", "--no-edit", "--quiet", b); err != nil && len(conflicted(keep, path)) == 0 {
			return "", fmt.Errorf("merge %s again: %w", b, err)
		}
	}
	return filepath.Join(path, filepath.FromSlash(prefix)), nil
}

// startPoint is the commit a task's worktree starts from in the repository holding dir: the tip of its wish's
// integration branch there, integration, so that it builds on the work committed before it (T07); the checkout's
// HEAD when the wish has none, or the branch is not in the repository. from is "" for HEAD.
func startPoint(ctx context.Context, dir, integration string) (sha, from string, err error) {
	if integration != "" {
		if sha, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+integration+"^{commit}"); err == nil {
			return sha, integration, nil
		}
	}
	sha, err = git(ctx, dir, "rev-parse", "HEAD")
	return sha, "", err
}

// removeWorktree removes the worktree path of the repository holding dir; its branch stays. Without force, git
// refuses a worktree with changes not committed. A worktree already deleted by hand is forgotten. The agy project
// of its workers goes with it.
func removeWorktree(ctx context.Context, dir, path string, force bool) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		forgetAgyProject(path)
		_, err := git(ctx, dir, "worktree", "prune")
		return err
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	keep, done := gitx.Uncut(ctx) // Never half removed (W52).
	defer done()
	_, err := git(keep, dir, append(args, path)...)
	if err == nil {
		forgetAgyProject(path)
	}
	return err
}

// branchStartCommit returns the commit from which branch in dir was created, from its reflog.
func branchStartCommit(ctx context.Context, dir, branch string) string {
	if branch == "" {
		return ""
	}
	reflog, err := git(ctx, dir, "log", "-g", "--format=%H", "refs/heads/"+branch)
	if err != nil || reflog == "" {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(reflog), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// branchHasNoCommits tells whether branch in dir is still at the commit it started from.
func branchHasNoCommits(ctx context.Context, dir, branch string) bool {
	if branch == "" {
		return false
	}
	head, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
	if err != nil || head == "" {
		return false
	}
	start := branchStartCommit(ctx, dir, branch)
	return start != "" && start == head
}

// branchHoldsUnmergedCommits tells whether branch in dir has commits of its own that are not in integration.
func branchHoldsUnmergedCommits(ctx context.Context, dir, branch, integration string) bool {
	if branch == "" {
		return false
	}
	head, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
	if err != nil || head == "" {
		head, err = git(ctx, dir, "rev-parse", "--verify", "--quiet", branch+"^{commit}")
		if err != nil || head == "" {
			return false
		}
	}
	// A branch with no commit beyond the point it started from holds no unmerged work.
	if start := branchStartCommit(ctx, dir, branch); start != "" && start == head {
		return false
	}
	intTarget := "refs/heads/" + integration + "^{commit}"
	intHead, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", intTarget)
	if err != nil || intHead == "" {
		intHead, _ = git(ctx, dir, "rev-parse", "--verify", "--quiet", integration+"^{commit}")
	}
	if intHead == "" {
		intHead, _ = git(ctx, dir, "rev-parse", "HEAD")
	}
	if intHead != "" {
		if _, err := git(ctx, dir, "merge-base", "--is-ancestor", head, intHead); err == nil {
			return false
		}
	}
	return true
}

// depWorkInSha tells whether the work of dep is reachable from sha.
func depWorkInSha(ctx context.Context, dir string, dep *planv1.Task, sha string) bool {
	if sha == "" {
		return false
	}
	if dep.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
		return true
	}
	head, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+dep.GetBranch()+"^{commit}")
	if err != nil || head == "" {
		head, _ = git(ctx, dir, "rev-parse", "--verify", "--quiet", dep.GetBranch()+"^{commit}")
	}
	if head == "" {
		return true // Branch does not exist; no work to check.
	}
	// A branch with no commit beyond the point it started from holds no unmerged work.
	if start := branchStartCommit(ctx, dir, dep.GetBranch()); start != "" && start == head {
		return true
	}
	if _, err := git(ctx, dir, "merge-base", "--is-ancestor", head, sha); err == nil {
		return true
	}
	if intSha := dep.GetIntegration().GetSha(); intSha != "" {
		if _, err := git(ctx, dir, "merge-base", "--is-ancestor", intSha, sha); err == nil {
			return true
		}
	}
	return false
}

// taskWorkInBranch reports whether the task's work is already in the integration branch:
// its branch head is an ancestor of the integration branch head (W425's check).
func taskWorkInBranch(ctx context.Context, dir string, task *planv1.Task, integration string) bool {
	if task == nil || task.GetBranch() == "" || integration == "" {
		return false
	}
	return taskWorkIn(ctx, dir, task, integrationHead(ctx, dir, integration))
}

// integrationHead is the commit the integration branch is at in dir, else the commit dir's checkout is at; "" when
// neither is found.
func integrationHead(ctx context.Context, dir, integration string) string {
	head, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+integration+"^{commit}")
	if err != nil || head == "" {
		head, _ = git(ctx, dir, "rev-parse", "--verify", "--quiet", integration+"^{commit}")
	}
	if head == "" {
		head, _ = git(ctx, dir, "rev-parse", "HEAD")
	}
	return head
}

// taskWorkIn is taskWorkInBranch with the integration branch's head, intHead, read once for a batch of tasks: each
// git costs a process, a tenth of a second on Windows.
func taskWorkIn(ctx context.Context, dir string, task *planv1.Task, intHead string) bool {
	if task == nil || task.GetBranch() == "" || intHead == "" {
		return false
	}
	head, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+task.GetBranch()+"^{commit}")
	if err != nil || head == "" {
		head, err = git(ctx, dir, "rev-parse", "--verify", "--quiet", task.GetBranch()+"^{commit}")
		if err != nil || head == "" {
			return false
		}
	}
	if _, err := git(ctx, dir, "merge-base", "--is-ancestor", head, intHead); err == nil {
		return true
	}
	if intSha := task.GetIntegration().GetSha(); intSha != "" {
		if _, err := git(ctx, dir, "merge-base", "--is-ancestor", intSha, intHead); err == nil {
			return true
		}
	}
	return false
}

// dependenciesUnmergedIn checks that each dependency's work is in sha; if not, it returns why the task must wait.
func (h *Harness) dependenciesUnmergedIn(ctx context.Context, dir string, task *planv1.Task, sha, from string) string {
	if len(task.GetDependsOn()) == 0 {
		return ""
	}
	targetBranch := from
	if targetBranch == "" {
		targetBranch = "the integration branch"
	}
	all, _ := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": task.GetWishId()})
	byID := make(map[string]*planv1.Task, len(all))
	byCode := make(map[string]*planv1.Task, len(all))
	for _, t := range all {
		byID[t.GetId()] = t
		if t.GetCode() != "" {
			byCode[t.GetCode()] = t
		}
	}
	for _, depID := range task.GetDependsOn() {
		dep := byID[depID]
		if dep == nil {
			var err error
			dep, err = store.Get[*planv1.Task](ctx, h.store, depID)
			if err != nil || dep == nil {
				continue
			}
		}
		seen := map[string]bool{}
		for !seen[dep.GetId()] {
			seen[dep.GetId()] = true
			code := dep.GetClosed().GetContinuedIn()
			if dep.GetStatus() == planv1.TaskStatus_TASK_STATUS_INTERRUPTED {
				code = render.ForkedAs(dep, all)
			}
			if code == "" || byCode[code] == nil {
				break
			}
			dep = byCode[code]
		}
		if plan.IsAzima(dep) {
			continue
		}
		if dep.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			continue
		}
		if (dep.GetClosed() != nil && dep.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED) ||
			strings.HasPrefix(dep.GetIntegration().GetReason(), "left out (") {
			continue
		}
		if dep.GetProjectId() != "" && task.GetProjectId() != "" && dep.GetProjectId() != task.GetProjectId() {
			continue
		}
		if dep.GetBranch() == "" {
			continue
		}
		if !depWorkInSha(ctx, dir, dep, sha) {
			return fmt.Sprintf("waits for %s's work to reach %s", dep.GetCode(), targetBranch)
		}
	}
	return ""
}
