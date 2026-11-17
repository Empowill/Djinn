package harness

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// TestPushRefusesTheReferenceBranch: Djinn never pushes a project's reference branch, whatever the wish and its push
// strategy; a wish on a branch of its own pushes it as before. Tilasm L09: the wish Djinn gates integrated into main
// with the wish strategy, and Djinn pushed main ten times from 07/11/2026, with no pull request.
func TestPushRefusesTheReferenceBranch(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	// onReference is an integration whose wish integrates into the reference branch, master, which origin holds.
	onReference := func(t *testing.T) (in *integration, bare string) {
		t.Helper()
		in = integrating(t)
		bare = filepath.Join(t.TempDir(), "origin.git")
		in.git(t, in.repo, "init", "--quiet", "--bare", bare)
		in.git(t, in.repo, "remote", "add", "origin", bare)
		in.git(t, in.repo, "push", "--quiet", "origin", in.branch)
		return in, bare
	}

	t.Run("a wish strategy wish integrated into the reference branch", func(t *testing.T) {
		t.Parallel()
		in, bare := onReference(t)
		old := in.remoteTip(t, bare)
		azima := in.azima(t, in.wishID, "Letters")
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
		in.pass(t, 0)
		if got := in.remoteTip(t, bare); got != old || in.tip(t) == old {
			t.Fatalf("origin's %s at %s, from %s, the branch at %s; want it not pushed", in.branch, got, old, in.tip(t))
		}
		if pushes := in.pushes(t); len(pushes) != 0 {
			t.Errorf("pushes %v; want none", pushes)
		}
		run := in.pushState(t).GetRun()
		want := "djinn wish set-integration " + in.wishID + " --project-id " + in.projectID + " --branch <branch> --create-branch"
		if run.GetBranch() != in.branch || run.GetStage() != planv1.PushStage_PUSH_STAGE_FAILED ||
			!strings.HasPrefix(run.GetNote(), "Djinn never pushes "+in.branch+": "+in.branch+" is the reference branch of app") ||
			!strings.Contains(run.GetNote(), `The wish "Run Djinn on itself" integrates into it`) || !strings.HasSuffix(run.GetNote(), want) {
			t.Errorf("the push's run %v; want failed, its note naming the branch, the wish and %q", run, want)
		}
		_, texts := in.integration(t, w1)
		if refused := slices.DeleteFunc(slices.Clone(texts), func(s string) bool { return !strings.Contains(s, "never pushes") }); len(refused) != 1 {
			t.Fatalf("W1's events %q; want the refusal once", texts)
		}

		// The push due again, three tasks and an hour, is refused again with no new event: the line says it.
		in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
		in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})
		in.pass(t, 2*DefaultPushAfter)
		if got := in.remoteTip(t, bare); got != old {
			t.Fatalf("origin's %s at %s; want it at %s", in.branch, got, old)
		}
		if _, again := in.integration(t, w1); len(again) != len(texts) {
			t.Errorf("W1's events %q; want no more than %q", again, texts)
		}

		// Nor does the person's own push: the button, or djinn project push.
		if _, err := in.h.Push(t.Context(), in.wishID, in.projectID, ""); !errors.Is(err, errReferencePush) {
			t.Errorf("Push: %v; want the reference branch refused", err)
		}
		if _, err := in.h.Push(t.Context(), in.wishID, in.projectID, in.branch); !errors.Is(err, errReferencePush) {
			t.Errorf("Push of %s: %v; want the reference branch refused", in.branch, err)
		}
		if got := in.remoteTip(t, bare); got != old {
			t.Fatalf("origin's %s at %s; want it at %s", in.branch, got, old)
		}
	})

	t.Run("a branch whose upstream is the reference branch", func(t *testing.T) {
		t.Parallel()
		in, bare := onReference(t)
		main := in.branch
		old := in.remoteTip(t, bare)
		in.git(t, in.repo, "switch", "--quiet", "-c", "feat/x", "--track", "origin/"+main)
		in.branch = "feat/x"
		if _, err := in.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
			WishId: in.wishID, ProjectId: in.projectID, Branch: in.branch,
		})); err != nil {
			t.Fatal(err)
		}
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
		if _, err := in.h.Push(t.Context(), in.wishID, in.projectID, ""); !errors.Is(err, errReferencePush) ||
			!strings.Contains(err.Error(), "Djinn never pushes feat/x: feat/x goes to "+main+", the reference branch of app") {
			t.Errorf("Push: %v; want feat/x refused, as it goes to %s", err, main)
		}
		if got := in.git(t, bare, "rev-parse", "refs/heads/"+main); got != old {
			t.Fatalf("origin's %s at %s; want it at %s", main, got, old)
		}
	})

	t.Run("a wish on a branch of its own", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		main := in.branch
		bare := in.remote(t) // The wish's own branch, feat/wish.
		if in.branch == main {
			t.Fatalf("the wish integrates into %s; want a branch of its own", main)
		}
		old := in.git(t, bare, "rev-parse", "refs/heads/"+main)
		azima := in.azima(t, in.wishID, "Letters")
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
		in.pass(t, 0)
		if got, tip := in.remoteTip(t, bare), in.tip(t); got != tip {
			t.Fatalf("origin's %s at %s; want it pushed at %s; push %v", in.branch, got, tip, in.pushState(t))
		}
		if got := in.git(t, bare, "rev-parse", "refs/heads/"+main); got != old {
			t.Errorf("origin's %s moved to %s; want it at %s", main, got, old)
		}
		if run := in.pushState(t).GetRun(); run.GetStage() != planv1.PushStage_PUSH_STAGE_PUSHED {
			t.Errorf("the push's run %v; want pushed", run)
		}
	})
}
