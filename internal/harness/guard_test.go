package harness

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// guardRepo is a repository with a bare remote, a worker's worktree and the integration worktree.
type guardRepo struct {
	dir, remote, worker, integration string
}

func newGuardRepo(t *testing.T) guardRepo {
	t.Helper()
	tmp := t.TempDir()
	g := guardRepo{
		dir: filepath.Join(tmp, "repo"), remote: filepath.Join(tmp, "remote.git"),
		worker: filepath.Join(tmp, "worker"), integration: filepath.Join(tmp, "integration"),
	}
	for _, args := range [][]string{
		{"init", "--quiet", "--bare", g.remote},
		{"init", "--quiet", "-b", "main", g.dir},
		{"-C", g.dir, "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "--quiet", "--allow-empty", "-m", "first"},
		{"-C", g.dir, "config", "user.name", "T"},
		{"-C", g.dir, "config", "user.email", "t@example.com"},
		{"-C", g.dir, "remote", "add", "origin", g.remote},
		{"-C", g.dir, "push", "--quiet", "origin", "main"},
		{"-C", g.dir, "worktree", "add", "--quiet", "-b", "w1", g.worker},
		{"-C", g.dir, "worktree", "add", "--quiet", "-b", "integration", g.integration},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return g
}

// runIn runs name in dir with env added to the test's own environment, and returns what it printed.
func runIn(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, append(os.Environ(), env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func TestGitGuardEnv(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if env, err := gitGuard(home, t.TempDir()); err != nil || env != nil {
		t.Errorf("outside Git: env %v, err %v; want none", env, err)
	}
	g := newGuardRepo(t)
	n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	for _, dir := range []string{g.dir, g.worker, filepath.Join(g.worker, "sub")} {
		env, err := gitGuard(home, dir)
		if err != nil {
			t.Fatal(err)
		}
		common, _ := filepath.EvalSymlinks(filepath.Join(g.dir, ".git"))
		file := gitGuardFile(home)
		want := []string{
			"GIT_CONFIG_COUNT=" + strconv.Itoa(n+2),
			"GIT_CONFIG_KEY_" + strconv.Itoa(n) + "=includeIf.gitdir:" + filepath.ToSlash(common) + ".path",
			"GIT_CONFIG_VALUE_" + strconv.Itoa(n) + "=" + file,
			"GIT_CONFIG_KEY_" + strconv.Itoa(n+1) + "=includeIf.gitdir:" + filepath.ToSlash(common) + "/.path",
			"GIT_CONFIG_VALUE_" + strconv.Itoa(n+1) + "=" + file,
		}
		if !slices.Equal(env, want) {
			t.Errorf("in %s: env\n%v\nwant\n%v", dir, env, want)
		}
	}
	if b, err := os.ReadFile(gitGuardFile(home)); err != nil || string(b) != noPushConfig {
		t.Errorf("guard file: %q, %v", b, err)
	}
	if got := globEscape(`/a/b*[c]?\d`); got != `/a/b\*\[c]\?\\d` {
		t.Errorf("globEscape = %q", got)
	}
}

// TestGitGuardKeepsWorkersFromPushing is the guard at work: whatever the command, a worker's push fails and the
// remote does not move, while its other Git commands work, and the orchestrator, without the guard, pushes.
func TestGitGuardKeepsWorkersFromPushing(t *testing.T) {
	t.Parallel()
	g := newGuardRepo(t)
	guard, err := gitGuard(t.TempDir(), g.worker)
	if err != nil {
		t.Fatal(err)
	}
	refs := func() string {
		out, err := runIn(g.remote, nil, "git", "for-each-ref")
		if err != nil {
			t.Fatalf("remote refs: %v\n%s", err, out)
		}
		return out
	}
	before := refs()
	if out, err := runIn(g.worker, guard, "sh", "-c",
		"echo w1 > w1.txt && git add w1.txt && git commit --quiet -m w1 && git remote add mine "+g.remote); err != nil {
		t.Fatalf("worker's commit: %v\n%s", err, out)
	}
	for _, args := range [][]string{
		{"git", "push", "origin", "w1"},
		{"sh", "-c", "git push origin w1:main"},
		{"git", "-C", ".", "push", "origin", "w1"},
		{"git", "push", "--no-verify", "origin", "w1"},
		{"git", "push", g.remote, "w1"},
		{"git", "push", "file://" + g.remote, "w1"},
		{"git", "push", "mine", "w1"},
		{"git", "-C", g.integration, "push", "origin", "integration"},
	} {
		out, err := runIn(g.worker, guard, args[0], args[1:]...)
		if err == nil || !strings.Contains(out, noPushScheme) {
			t.Errorf("%v: err %v, output %q; want a refusal naming %s", args, err, out, noPushScheme)
		}
	}
	if after := refs(); after != before {
		t.Fatalf("the remote moved:\n%s\nthen\n%s", before, after)
	}

	// The worker's other commands work.
	for _, args := range [][]string{
		{"fetch", "--quiet", "origin"},
		{"status", "--porcelain"},
		{"diff", "--quiet", "main", "--", "missing"},
		{"rebase", "--quiet", "origin/main"},
		{"worktree", "add", "--quiet", "-b", "w2", filepath.Join(filepath.Dir(g.worker), "w2"), "main"},
		{"merge", "--quiet", "--no-edit", "main"},
	} {
		if out, err := runIn(g.worker, guard, "git", args...); err != nil {
			t.Errorf("git %v: %v\n%s", args, err, out)
		}
	}
	if out, err := runIn(g.worker, guard, "sh", "-c",
		"echo more >> w1.txt && git stash --quiet && git stash pop --quiet && git commit --quiet -am more"); err != nil {
		t.Errorf("stash and commit: %v\n%s", err, out)
	}

	// Another repository pushes as before, the guard on: a test's, run by a worker.
	other, bare := filepath.Join(t.TempDir(), "other"), filepath.Join(t.TempDir(), "other.git")
	if out, err := runIn(t.TempDir(), guard, "sh", "-c", "git init --quiet --bare "+bare+" && git init --quiet "+other+
		" && cd "+other+" && git -c user.name=T -c user.email=t@example.com commit --quiet --allow-empty -m x"+
		" && git push --quiet "+bare+" HEAD:refs/heads/main"); err != nil {
		t.Errorf("push from another repository: %v\n%s", err, out)
	}

	// The orchestrator pushes the integration branch with its own environment.
	if out, err := runIn(g.integration, nil, "git", "merge", "--quiet", "w1"); err != nil {
		t.Fatalf("merge w1: %v\n%s", err, out)
	}
	if _, out, err := gitPush(t.Context(), g.integration, "origin", "integration", "main"); err != nil {
		t.Fatalf("orchestrator's push: %v\n%s", err, out)
	}
	if after := refs(); after == before {
		t.Errorf("the orchestrator's push left the remote at\n%s", after)
	}
}

// TestStartGivesTheGuard: a worker the harness starts in a project's worktree gets the guard in its environment,
// next to its task, whatever its provider (here the fake).
func TestStartGivesTheGuard(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := up(t, home)
	rec := recorder{specs: make(chan Spec, 1)}
	e.h.providers[planv1.Provider_PROVIDER_FAKE] = rec
	wishID, _ := e.wish(t, gitRepo(t))
	task := e.spawn(t, wishID, "say hello")
	spec := <-rec.specs
	e.watch(t.Context(), t, task.GetId(), 0)
	guard, err := gitGuard(home, spec.Dir)
	if err != nil || len(guard) == 0 {
		t.Fatalf("guard of %s: %v, %v", spec.Dir, guard, err)
	}
	want := append([]string{"DJINN_TASK_ID=" + task.GetId(), "DJINN_WISH_ID=" + wishID}, guard...)
	if !slices.Equal(spec.Env, want) {
		t.Errorf("worker's environment\n%v\nwant\n%v", spec.Env, want)
	}
}
