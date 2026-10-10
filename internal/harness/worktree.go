package harness

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
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
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return out.String(), nil
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
	if _, err := git(ctx, dir, "worktree", "add", "--quiet", "-b", branch, path, from); err != nil {
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
	if _, err := git(ctx, dir, "worktree", "add", "--quiet", "-b", branch, path, f.GetBase()); err != nil {
		return "", err
	}
	if b := f.GetMergeBranch(); b != "" {
		if _, err := git(ctx, path, "merge", "--no-ff", "--no-edit", "--quiet", b); err != nil && len(conflicted(ctx, path)) == 0 {
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
// refuses a worktree with changes not committed. A worktree already deleted by hand is forgotten.
func removeWorktree(ctx context.Context, dir, path string, force bool) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		_, err := git(ctx, dir, "worktree", "prune")
		return err
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	_, err := git(ctx, dir, append(args, path)...)
	return err
}
