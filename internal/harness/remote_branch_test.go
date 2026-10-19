package harness

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

func TestCatchUp(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	// W164: an azima's branch that falls behind the remote's once named, a teammate pushing to it, is fetched and
	// brought up to the remote's before work merges into it.
	t.Run("falls behind the remote", func(t *testing.T) {
		t.Parallel()
		r := withRequests(t)
		const behind = "hotfix-company-tenant-isolation"
		named := r.onRemote(t, r.fork, behind, "tenant.txt")
		r.git(t, r.repo, "branch", behind, named)
		t1 := r.azima(t, r.wishID, "Tenant")
		if _, err := r.nameBranch(t, t1.GetId(), behind); err != nil {
			t.Fatal(err)
		}

		// A teammate pushes to it; this repository has not fetched it.
		teammate := r.onRemote(t, named, behind, "mate.txt")
		r.git(t, r.repo, "update-ref", "refs/remotes/origin/"+behind, named)
		w1 := r.finished(t, "W1", map[string]string{"app/src/w1.txt": "w1\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
		r.pass(t, 0)
		if got := r.get(t, w1.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || got.GetBranch() != behind {
			t.Fatalf("W1's integration %v, want committed into %s brought up to origin's", got, behind)
		}
		if !isAncestor(t.Context(), r.repo, teammate, "refs/heads/"+behind) {
			t.Errorf("%s lacks the teammate's %s: not fetched and brought up before the merge", behind, teammate)
		}
	})

	// W164: an azima's branch that diverges from the remote's once named, a teammate pushing over its pushed work by
	// force, takes no more work and is not pushed nor pulled, the lead told once, until a person aligns them.
	t.Run("diverged from the remote", func(t *testing.T) {
		t.Parallel()
		r := withRequests(t)
		var mu sync.Mutex
		var told []string
		r.h.NotifyLead(func(_ context.Context, ev *planv1.LeadEvent) error {
			mu.Lock()
			defer mu.Unlock()
			told = append(told, ev.GetText())
			return nil
		})
		const behind = "hotfix-company-tenant-isolation"
		named := r.onRemote(t, r.fork, behind, "tenant.txt")
		r.git(t, r.repo, "branch", behind, named)
		t1 := r.azima(t, r.wishID, "Tenant")
		if _, err := r.nameBranch(t, t1.GetId(), behind); err != nil {
			t.Fatal(err)
		}

		// Work on it is pushed; then the teammate pushes another commit over it, by force: they diverged.
		r.git(t, r.repo, "switch", "--quiet", behind)
		writeFile(t, r.repo, "app/src/w1.txt", "w1\n")
		commitAll(t, r.repo, "W1")
		r.git(t, r.repo, "push", "--quiet", "origin", behind)
		r.git(t, r.repo, "switch", "--quiet", r.branch)
		merged := r.git(t, r.repo, "rev-parse", "refs/heads/"+behind)
		r.git(t, r.repo, "switch", "--quiet", "--detach", named)
		writeFile(t, r.repo, "app/src/mate2.txt", "mate2\n")
		commitAll(t, r.repo, "Request: mate2")
		r.git(t, r.repo, "push", "--quiet", "--force", "origin", "HEAD:refs/heads/"+behind)
		r.git(t, r.repo, "switch", "--quiet", r.branch)
		w2 := r.finished(t, "W2", map[string]string{"app/src/w2.txt": "w2\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
		r.pass(t, 0)
		r.pass(t, 0)
		want := behind + " has diverged from origin/" + behind + " (ahead 1, behind 1)"
		if got := r.get(t, w2.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING ||
			!strings.Contains(got.GetReason(), want) {
			t.Fatalf("W2's integration %v, want pending: %s", got, want)
		}
		if got := r.git(t, r.repo, "rev-parse", "refs/heads/"+behind); got != merged {
			t.Errorf("%s moved to %s, want left at %s", behind, got, merged)
		}
		if _, err := r.h.Push(t.Context(), r.wishID, r.projectID, behind); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("push of %s: %v, want refused: %s", behind, err, want)
		}
		if _, err := r.h.Pull(t.Context(), r.project(t), behind); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("pull of %s: %v, want refused: %s", behind, err, want)
		}
		mu.Lock()
		if n := len(slices.DeleteFunc(slices.Clone(told), func(s string) bool { return !strings.Contains(s, want) })); n != 1 {
			t.Errorf("the lead was told %d times that %s: %q, want once", n, want, told)
		}
		mu.Unlock()

		// The lead aligns them by hand: the work merges.
		r.git(t, r.repo, "switch", "--quiet", behind)
		r.git(t, r.repo, "merge", "--quiet", "--no-edit", "origin/"+behind)
		r.git(t, r.repo, "switch", "--quiet", r.branch)
		r.pass(t, 0)
		if got := r.get(t, w2.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || got.GetBranch() != behind {
			t.Fatalf("W2's integration %v once aligned, want committed into %s", got, behind)
		}
	})

	// W164: main, the wish's integration branch, behind origin's after someone else's push, is fetched and brought up
	// to it, the person's clean checkout following, before the work merges on top: the push then goes, never forced.
	t.Run("main behind the remote", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		bare := in.remote(t)
		theirs := in.pushFromOther(t, bare, "NEWS", "theirs\n", "Theirs")

		azima := in.azima(t, in.wishID, "Letters")
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
		in.pass(t, 0)
		if got := in.get(t, w1.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
			t.Fatalf("W1's integration %v, want committed", got)
		}
		if !isAncestor(t.Context(), in.repo, theirs, in.tip(t)) {
			t.Errorf("%s at %s lacks origin's %s", in.branch, in.tip(t), theirs)
		}
		if got := in.remoteTip(t, bare); got != in.tip(t) {
			t.Errorf("origin at %s, want pushed at %s", got, in.tip(t))
		}
		if got := in.git(t, in.repo, "status", "--porcelain", "--untracked-files=no"); got != "" {
			t.Errorf("the person's checkout reads %q, want following, clean", got)
		}
		if got := in.git(t, in.repo, "show", "HEAD:NEWS"); got != "theirs" {
			t.Errorf("NEWS reads %q in the checkout's HEAD, want origin's", got)
		}
	})
}

// TestPull (W164): a branch Djinn pushes, behind the remote's, is pulled from the projects list, a fast-forward; one
// checked out in the developer's own worktree with changes not committed is left as it is, by the pull and by the
// merge of work into it, which waits and tells the lead; committed, both go on.
func TestPull(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("fast forwards from list", func(t *testing.T) {
		t.Parallel()
		r := withRequests(t)
		const branch = "ET-77-pull"
		start := r.onRemote(t, r.fork, branch, "start.txt")
		t1 := r.azima(t, r.wishID, "Pull")
		if _, err := r.nameBranch(t, t1.GetId(), branch); err != nil {
			t.Fatal(err)
		}

		// Pulled from the list: fetched, then fast-forwarded.
		mate := r.onRemote(t, start, branch, "mate.txt")
		r.git(t, r.repo, "update-ref", "refs/remotes/origin/"+branch, start)
		pulls, err := r.h.Pull(t.Context(), r.project(t), "")
		if err != nil {
			t.Fatal(err)
		}
		if len(pulls) != 1 || pulls[0].GetBranch() != branch || pulls[0].GetOldSha() != start || pulls[0].GetSha() != mate ||
			pulls[0].GetRefused() != "" {
			t.Errorf("pulls %v, want %s from %s to %s", pulls, branch, start, mate)
		}
		if got := r.git(t, r.repo, "rev-parse", "refs/heads/"+branch); got != mate {
			t.Errorf("%s at %s, want pulled to %s", branch, got, mate)
		}
		if pulls, err := r.h.Pull(t.Context(), r.project(t), ""); err != nil || len(pulls) != 0 {
			t.Errorf("pull again: %v, %v, want nothing to do", pulls, err)
		}
	})

	t.Run("refuses under dirty developer checkout", func(t *testing.T) {
		t.Parallel()
		r := withRequests(t)
		var mu sync.Mutex
		var told []string
		r.h.NotifyLead(func(_ context.Context, ev *planv1.LeadEvent) error {
			mu.Lock()
			defer mu.Unlock()
			told = append(told, ev.GetText())
			return nil
		})
		const branch = "ET-77-pull"
		start := r.onRemote(t, r.fork, branch, "start.txt")
		t1 := r.azima(t, r.wishID, "Pull")
		if _, err := r.nameBranch(t, t1.GetId(), branch); err != nil {
			t.Fatal(err)
		}

		// The developer's own checkout of it holds changes: nothing moves it.
		// Git names a worktree by its resolved path: on macOS t.TempDir() is a link (docs/pitfalls.md).
		tmp, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		dev := filepath.Join(tmp, "dev")
		r.git(t, r.repo, "worktree", "add", "--quiet", dev, branch)
		writeFile(t, dev, "app/src/start.txt", "mine\n")
		mate2 := r.onRemote(t, start, branch, "mate2.txt")
		want := "your checkout of it, " + dev + ", has changes not committed"
		if _, err := r.h.Pull(t.Context(), r.project(t), branch); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("pull under a checkout with changes: %v, want refused: %s", err, want)
		}
		w1 := r.finished(t, "W1", map[string]string{"app/src/w1.txt": "w1\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
		r.pass(t, 0)
		if got := r.get(t, w1.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING ||
			!strings.Contains(got.GetReason(), want) {
			t.Fatalf("W1's integration %v, want pending: %s", got, want)
		}
		if got := r.git(t, r.repo, "rev-parse", "refs/heads/"+branch); got != start {
			t.Errorf("%s moved to %s under a checkout with changes, want left at %s", branch, got, start)
		}
		if got := r.git(t, dev, "status", "--porcelain", "--untracked-files=no"); got != "M app/src/start.txt" {
			t.Errorf("the developer's checkout reads %q, want its change kept", got)
		}
		mu.Lock()
		if !slices.ContainsFunc(told, func(s string) bool { return strings.Contains(s, want) }) {
			t.Errorf("the lead was told %q, want: %s", told, want)
		}
		mu.Unlock()

		// Committed: the branch is brought up to the remote's, the checkout following, and the work merges.
		r.git(t, dev, "checkout", "--quiet", "--", ".")
		r.pass(t, 0)
		if got := r.get(t, w1.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
			t.Fatalf("W1's integration %v, want committed", got)
		}
		if !isAncestor(t.Context(), r.repo, mate2, "refs/heads/"+branch) {
			t.Errorf("%s lacks the remote's %s", branch, mate2)
		}
		if got, want := r.git(t, dev, "rev-parse", "HEAD"), r.git(t, r.repo, "rev-parse", "refs/heads/"+branch); got != want {
			t.Errorf("the developer's checkout at %s, want following %s at %s", got, branch, want)
		}
	})
}
