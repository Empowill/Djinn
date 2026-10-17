package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// copyState is what a developer's copy holds: its HEAD, the branch it has checked out, its status and the files it
// changed without committing them.
type copyState struct {
	head, branch, status, readme, scratch string
}

// copyOf reads the state of the copy repo.
func copyOf(t *testing.T, in *integration) copyState {
	t.Helper()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(in.repo, "app", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	status, err := gitRaw(t.Context(), in.repo, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		t.Fatal(err)
	}
	return copyState{
		head: in.git(t, in.repo, "rev-parse", "HEAD"), branch: in.git(t, in.repo, "symbolic-ref", "--short", "HEAD"),
		status: status, readme: read("README.md"), scratch: read("scratch.txt"),
	}
}

// developersCopy leaves the project's copy as a developer works in it (W227): origin, a bare repository whose main a
// teammate moved on since this copy last fetched it; the copy checked out on a branch of its own, mine, with a commit
// of its own, a tracked file changed and an untracked one, none of it committed. It returns the commit origin's main
// is at, which this copy has not fetched, and the copy's state, to check it ends as it was.
func developersCopy(t *testing.T, in *integration) (string, copyState) {
	t.Helper()
	in.remote(t)
	in.git(t, in.repo, "fetch", "--quiet", "origin")
	in.git(t, in.repo, "remote", "set-head", "origin", in.branch)
	old := in.tip(t)
	// The teammate's commit, pushed to origin; this copy's view of origin left where it was: not fetched yet.
	ahead := in.git(t, in.repo, "commit-tree", old+"^{tree}", "-p", old, "-m", "A teammate's commit")
	in.git(t, in.repo, "push", "--quiet", "origin", ahead+":refs/heads/"+in.branch)
	in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+in.branch, old)
	in.git(t, in.repo, "switch", "--quiet", "-c", "mine")
	writeFile(t, in.repo, "app/mine.txt", "mine\n")
	in.git(t, in.repo, "add", "app/mine.txt")
	in.git(t, in.repo, "commit", "--quiet", "-m", "Mine")
	writeFile(t, in.repo, "app/README.md", "# App, being edited\n")
	writeFile(t, in.repo, "app/scratch.txt", "not committed\n")
	return ahead, copyOf(t, in)
}

// startsFrom checks that the worker of task, running, started from want, the commit its branch was made at, said so
// in its start event as from shown; that its worktree holds nothing of the developer's copy; and that the copy ends as
// it was, before.
func startsFrom(t *testing.T, in *integration, task *planv1.Task, want, shown string, before copyState) {
	t.Helper()
	task = in.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	if got := branchStartCommit(t.Context(), in.repo, task.GetBranch()); got != want {
		t.Errorf("%s's branch %s starts at %s, want %s", task.GetCode(), task.GetBranch(), got, want)
	}
	for _, name := range []string{"mine.txt", "scratch.txt"} {
		if _, err := os.Stat(filepath.Join(task.GetWorktree(), "app", name)); !os.IsNotExist(err) {
			t.Errorf("%s's worktree holds the copy's %s: %v", task.GetCode(), name, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(task.GetWorktree(), "app", "README.md")); err != nil || string(b) != "# App\n" {
		t.Errorf("%s's worktree README = %q, %v; want it as committed, without the copy's change", task.GetCode(), b, err)
	}
	in.release(t, task.GetId())
	in.ended(t, task.GetId())
	events, err := store.List[*planv1.TaskEvent](t.Context(), in.db, store.Where{"task_id": task.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	from := ", from " + shown + " at " + want[:8] + ","
	if texts := eventTexts(events); !slices.ContainsFunc(texts, func(s string) bool {
		return strings.HasPrefix(s, "started fake") && strings.Contains(s, from)
	}) {
		t.Errorf("%s's events, without %q: %q", task.GetCode(), from, texts)
	}
	if after := copyOf(t, in); after != before {
		t.Errorf("the developer's copy changed:\n%+v\nwant\n%+v", after, before)
	}
}

// TestWorkersNeverStartFromTheCopy: whatever the developer's copy has checked out, and its changes not committed, a
// worker starts from the commit its push strategy names (W227): per wish, from the wish's integration branch, by
// default the reference branch, origin's main fetched first; per azima, an azima's part from the azima's branch, cut
// from origin's main; work of no azima from the wish's integration branch, the reference branch by default. The
// copy's HEAD, branch and files end as they were.
func TestWorkersNeverStartFromTheCopy(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	// A wish made while the copy is on mine: it lands in the reference branch, never in mine.
	wishOn := func(t *testing.T, in *integration, strategy planv1.PushStrategy) string {
		t.Helper()
		made, err := in.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
			Title: "Made on mine", ProjectIds: []string{in.projectID}, PushStrategy: strategy.Enum(),
		}))
		if err != nil {
			t.Fatal(err)
		}
		if got := plan.IntegrationBranchOf(made.Msg.GetWish(), in.projectID); got != in.branch {
			t.Fatalf("the wish lands in %q, want the reference branch %s, not mine, the copy's", got, in.branch)
		}
		return made.Msg.GetWish().GetId()
	}
	t.Run("per wish", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		ahead, before := developersCopy(t, in)
		wishID := wishOn(t, in, planv1.PushStrategy_PUSH_STRATEGY_WISH)
		task := in.mustSpawn(t, wishID, "Per wish", "wait\ndone", &planv1.TaskServiceSpawnRequest{})
		startsFrom(t, in, task, ahead, "origin/"+in.branch, before)
	})
	t.Run("an azima's part", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		ahead, before := developersCopy(t, in)
		wishID := wishOn(t, in, planv1.PushStrategy_PUSH_STRATEGY_AZIMA)
		azima := in.azima(t, wishID, "Alpha")
		branch := "djinn/" + azima.GetCode() + "-alpha"
		if got := in.git(t, in.repo, "rev-parse", "refs/heads/"+branch); got != ahead {
			t.Fatalf("%s cut at %s, want origin's main at %s", branch, got, ahead)
		}
		task := in.mustSpawn(t, wishID, "Part", "wait\ndone", &planv1.TaskServiceSpawnRequest{PartOf: azima.GetId()})
		startsFrom(t, in, task, ahead, branch, before)
	})
	t.Run("work of no azima", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		ahead, before := developersCopy(t, in)
		wishID := wishOn(t, in, planv1.PushStrategy_PUSH_STRATEGY_AZIMA)
		task := in.mustSpawn(t, wishID, "Loose", "wait\ndone", &planv1.TaskServiceSpawnRequest{})
		startsFrom(t, in, task, ahead, "origin/"+in.branch, before)
	})
}
