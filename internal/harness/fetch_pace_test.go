package harness

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/testx"
)

// fetchCount counts the fetches of in's repository from origin: git runs origin's upload-pack through a script that
// writes a line each time. It returns the count so far.
func fetchCount(t *testing.T, in *integration) func() int {
	t.Helper()
	dir := t.TempDir()
	lines, script := filepath.Join(dir, "fetches"), filepath.Join(dir, "upload-pack")
	body := "#!/bin/sh\necho fetch >> '" + lines + "'\nexec git upload-pack \"$@\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	in.git(t, in.repo, "config", "remote.origin.uploadpack", script)
	return func() int {
		b, err := os.ReadFile(lines)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return strings.Count(string(b), "fetch\n")
	}
}

// advance moves in's clock by d.
func (in *integration) advance(d time.Duration) {
	in.mu.Lock()
	in.now = in.now.Add(d)
	in.mu.Unlock()
}

// paced is an integration paced by presence (PaceByPresence) whose project's main lives on origin, its fetches
// counted; no tick of the integration of its own: the test makes each pass.
func paced(t *testing.T) (*integration, func() int) {
	t.Helper()
	in := integrating(t, WithTick(time.Hour))
	in.remote(t)
	in.git(t, in.repo, "remote", "set-head", "origin", in.branch)
	fetches := fetchCount(t, in)
	in.h.PaceByPresence()
	return in, fetches
}

// TestFetchPace (W65, L11, Q44 B): the watch of the configuration looks every 15 minutes while the developer is
// there, every hour once they left Djinn alone for 15 minutes, and at once when they come back, at most once each 5
// minutes; after a push, at the pushed project alone. The merge of main reads main as the watch fetched it lately,
// fetches the branches of a wish's lines together, and looks every 3 hours while the developer is away.
func TestFetchPace(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	t.Run("the watch of the configuration", func(t *testing.T) {
		t.Parallel()
		in, fetches := paced(t)
		ctx := t.Context()
		step := func(d time.Duration, touch bool, want int, what string) {
			t.Helper()
			in.advance(d)
			if touch {
				in.h.Presence().Touch()
			}
			before := fetches()
			in.h.configPass(ctx)
			if got := fetches() - before; got != want {
				t.Errorf("%s: %d fetches, want %d", what, got, want)
			}
		}
		step(0, true, 1, "the first look")
		step(5*time.Minute, true, 0, "5 minutes later, there")
		step(10*time.Minute, true, 1, "15 minutes after the first look, there")
		if in.h.away() {
			t.Fatal("away, just touched")
		}
		step(15*time.Minute, false, 0, "15 minutes after the last look, away")
		if !in.h.away() {
			t.Fatal("not away, 15 minutes untouched and no worker")
		}
		step(30*time.Minute, false, 0, "45 minutes after the last look, away")
		step(15*time.Minute, false, 1, "an hour after the last look, away")
		in.h.mainNow.Store(false)
		step(5*time.Minute, true, 1, "back after 20 minutes away")
		if !in.h.mainNow.Load() {
			t.Error("back: no look at main asked")
		}
		step(time.Minute, true, 0, "a touch a minute later")
		in.h.Back() // The window comes to the front: within 5 minutes of the last return, no new look.
		step(0, false, 0, "the window in front a minute after a return")
		in.advance(5 * time.Minute)
		in.h.Back()
		step(0, false, 1, "the window in front 6 minutes after a return")

		// A push asks for a look at its project alone.
		in.h.kickConfig("another project")
		step(0, false, 0, "a push in another project")
		in.h.kickConfig(in.projectID)
		step(0, false, 1, "a push in this project")
	})

	t.Run("the lines of main", func(t *testing.T) {
		t.Parallel()
		in, fetches := paced(t)
		ctx := t.Context()
		in.setAzimaStrategy(t)
		// Merge of main on, as by default.
		writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
			"generated: \"gen/**\"\ngenerate: \"gen\"\ntest: \"test\"\n")
		var branches []string
		for _, title := range []string{"One", "Two", "Three"} {
			a := in.azima(t, in.wishID, title)
			branch := in.stored(t, a.GetId()).GetAzima().GetBranch()
			in.git(t, in.repo, "push", "--quiet", "origin", branch)
			branches = append(branches, branch)
		}
		pass := func(d time.Duration, touch bool, want int, what string) {
			t.Helper()
			in.advance(d)
			if touch {
				in.h.Presence().Touch()
			}
			in.h.mainNow.Store(false)
			before := fetches()
			in.h.mainPass(withPassMemo(ctx))
			if got := fetches() - before; got != want {
				t.Errorf("%s: %d fetches, want %d", what, got, want)
			}
		}
		in.h.Presence().Touch()
		in.h.configPass(ctx)
		// The wish's branch, main, and the three azimas' in one fetch; main and its tags as the watch just fetched them.
		pass(time.Minute, true, 1, "the first pass, the watch looked a minute ago")
		pass(time.Minute, true, 0, "a minute later")
		pass(time.Hour, true, 2, "an hour later, there: the lines together, then main and its tags")
		pass(time.Hour, false, 0, "an hour later, away")
		if !in.h.away() {
			t.Fatal("not away")
		}
		pass(2*time.Hour, false, 2, "3 hours after the last look, away")

		// A branch the remote no longer holds fails the fetch together: each line fetches alone, that branch's
		// remote-tracking ref pruned; the pass after fetches the others together, that one alone.
		in.git(t, in.repo, "push", "--quiet", "origin", "--delete", branches[1])
		in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+branches[1], in.tip(t)) // as seen before the deletion
		pass(time.Hour, true, 6, "a branch deleted on the remote: the fetch together, each alone, then main")
		if got := commitOf(ctx, in.repo, "refs/remotes/origin/"+branches[1]); got != "" {
			t.Errorf("origin/%s at %s, want pruned", branches[1], got)
		}
		pass(time.Hour, true, 3, "the next pass: three together, the deleted one alone, then main")
	})

	t.Run("a worker starts from a fresh base, away", func(t *testing.T) {
		t.Parallel()
		in, fetches := paced(t)
		ctx := t.Context()
		in.h.configPass(ctx) // main fetched now: fresh for the watch's pace, an hour while away
		if n := fetches(); n != 1 || !in.h.away() {
			t.Fatalf("%d fetches, away %v; want 1, away", n, in.h.away())
		}
		// A teammate pushes on main, then on the wish's branch: Djinn has not fetched them.
		old := in.tip(t)
		ahead := in.git(t, in.repo, "commit-tree", old+"^{tree}", "-p", old, "-m", "A teammate's commit on main")
		in.git(t, in.repo, "push", "--quiet", "origin", ahead+":refs/heads/"+in.branch)
		in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+in.branch, old)
		in.advance(2 * time.Minute)

		starts := func(title, want string) {
			t.Helper()
			before := fetches()
			task := in.mustSpawn(t, in.wishID, title, "wait\ndone", &planv1.TaskServiceSpawnRequest{})
			task = in.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
			if got := branchStartCommit(ctx, in.repo, task.GetBranch()); got != want {
				t.Errorf("%s starts at %s, want the remote's %s", title, got, want)
			}
			if got := fetches() - before; got != 1 {
				t.Errorf("%s: %d fetches before its worktree, want 1", title, got)
			}
			if in.h.away() {
				t.Errorf("away while %s runs", title)
			}
			in.release(t, task.GetId())
			in.ended(t, task.GetId())
		}
		starts("From main", ahead)

		// The wish lands in a branch of its own, on the remote, where a teammate pushed too.
		in.git(t, in.repo, "branch", "feat/x", ahead)
		in.git(t, in.repo, "push", "--quiet", "origin", "feat/x")
		if _, err := in.wishes.SetIntegration(ctx, connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
			WishId: in.wishID, ProjectId: in.projectID, Branch: "feat/x",
		})); err != nil {
			t.Fatal(err)
		}
		theirs := in.git(t, in.repo, "commit-tree", ahead+"^{tree}", "-p", ahead, "-m", "A teammate's commit on feat/x")
		in.git(t, in.repo, "push", "--quiet", "origin", theirs+":refs/heads/feat/x")
		in.git(t, in.repo, "update-ref", "refs/remotes/origin/feat/x", ahead)
		in.advance(2 * time.Minute)
		starts("From the wish's branch", theirs)

		// Workers that start together share one fetch.
		in.advance(2 * time.Minute)
		project := in.project(t)
		settings, err := plan.LoadSettings(in.home, project)
		if err != nil {
			t.Fatal(err)
		}
		before := fetches()
		var wg sync.WaitGroup
		for range 3 {
			wg.Go(func() { in.h.fetchReference(ctx, project.GetDirectory(), settings) })
		}
		wg.Wait()
		if got := fetches() - before; got != 1 {
			t.Errorf("three workers starting together: %d fetches, want 1", got)
		}
	})
}

// TestFetchMainTagClash: main and the tags come in one fetch; a tag that would clobber a local one fails that fetch
// for git, not for Djinn: main is fetched all the same, and the clash told once on the wish.
func TestFetchMainTagClash(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t, WithTick(time.Hour))
	in.remote(t)
	ctx := t.Context()
	old := in.tip(t)
	in.git(t, in.repo, "tag", "v1", old)
	in.git(t, in.repo, "push", "--quiet", "origin", "v1")
	mine := in.git(t, in.repo, "commit-tree", old+"^{tree}", "-p", old, "-m", "Mine")
	in.git(t, in.repo, "tag", "-f", "v1", mine)
	ahead := in.git(t, in.repo, "commit-tree", old+"^{tree}", "-p", old, "-m", "On main")
	in.git(t, in.repo, "push", "--quiet", "origin", ahead+":refs/heads/"+in.branch)
	in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+in.branch, old)
	if err := in.h.fetchMain(ctx, in.wishID, in.repo, "refs/remotes/origin/"+in.branch); err != nil {
		t.Fatalf("fetchMain: %v", err)
	}
	if got := commitOf(ctx, in.repo, "refs/remotes/origin/"+in.branch); got != ahead {
		t.Errorf("origin/%s at %s, want %s", in.branch, got, ahead)
	}
	if got := commitOf(ctx, in.repo, "refs/tags/v1"); got != mine {
		t.Errorf("v1 at %s, want the local one kept, %s", got, mine)
	}
	if !onlyTagClashes(" ! [rejected]        v1         -> v1  (would clobber existing tag)\n") ||
		onlyTagClashes("fatal: couldn't find remote ref refs/heads/nope\n") ||
		onlyTagClashes(" ! [rejected]        main       -> origin/main  (non-fast-forward)\n") {
		t.Error("onlyTagClashes: a clash alone, nothing else")
	}
}
