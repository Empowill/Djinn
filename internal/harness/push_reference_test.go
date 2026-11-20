package harness

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
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
		if _, err := in.h.Push(t.Context(), in.wishID, in.projectID, ""); !errors.Is(err, errProtectedPush) {
			t.Errorf("Push: %v; want the reference branch refused", err)
		}
		if _, err := in.h.Push(t.Context(), in.wishID, in.projectID, in.branch); !errors.Is(err, errProtectedPush) {
			t.Errorf("Push of %s: %v; want the reference branch refused", in.branch, err)
		}
		if got := in.remoteTip(t, bare); got != old {
			t.Fatalf("origin's %s at %s; want it at %s", in.branch, got, old)
		}
	})

	// W62: two passes read the wish, the first starts its push apart, refused; the second starts its own once the
	// first ended, with its copy of the wish read before the refusal. Under -race, the refusal was said twice.
	t.Run("a push with the wish read before the last refusal", func(t *testing.T) {
		t.Parallel()
		in, bare := onReference(t)
		old := in.remoteTip(t, bare)
		stale, err := store.Get[*planv1.Wish](t.Context(), in.db, in.wishID)
		if err != nil {
			t.Fatal(err)
		}
		project, err := store.Get[*planv1.Project](t.Context(), in.db, in.projectID)
		if err != nil {
			t.Fatal(err)
		}
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
		in.pass(t, 0)
		for range 2 {
			if _, err := in.h.pushBranch(t.Context(), stale, project, in.branch, []*planv1.Task{w1}, "", false); !errors.Is(err, errProtectedPush) {
				t.Errorf("push with the wish read before: %v; want the reference branch refused", err)
			}
		}
		if got := in.remoteTip(t, bare); got != old {
			t.Fatalf("origin's %s at %s; want it at %s", in.branch, got, old)
		}
		_, texts := in.integration(t, w1)
		if refused := slices.DeleteFunc(slices.Clone(texts), func(s string) bool { return !strings.Contains(s, "never pushes") }); len(refused) != 1 {
			t.Errorf("W1's events %q; want the refusal once", texts)
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
		if _, err := in.h.Push(t.Context(), in.wishID, in.projectID, ""); !errors.Is(err, errProtectedPush) ||
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

	// onForge is an integration whose wish has a branch of its own, feat/wish, a task's work merged into it, and whose
	// origin is address on a forge, forge, that git reaches in a bare repository: its folder.
	onForge := func(t *testing.T, address string, forge *fakeForge) (in *integration, bare string) {
		t.Helper()
		in = integrating(t)
		bare = in.remote(t)
		in.git(t, in.repo, "remote", "set-url", "origin", address)
		in.git(t, in.repo, "config", "url."+bare+".insteadOf", address)
		in.mu.Lock()
		in.forge = forge
		in.mu.Unlock()
		azima := in.azima(t, in.wishID, "Letters") // Its work done, its branch is pushed at once.
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
		return in, bare
	}

	t.Run("a branch the forge protects", func(t *testing.T) {
		t.Parallel()
		forge := &fakeForge{api: "main\nfeat/wish\n"}
		in, bare := onForge(t, "https://github.com/acme/app.git", forge)
		old := in.remoteTip(t, bare)
		in.pass(t, 0)
		if got := in.remoteTip(t, bare); got != old || in.tip(t) == old {
			t.Fatalf("origin's %s at %s, from %s, the branch at %s; want it not pushed", in.branch, got, old, in.tip(t))
		}
		run := in.pushState(t).GetRun()
		if run.GetStage() != planv1.PushStage_PUSH_STAGE_FAILED ||
			!strings.HasPrefix(run.GetNote(), "Djinn never pushes feat/wish: github.com/acme/app protects feat/wish, which") {
			t.Errorf("the push's run %v; want failed, its note naming the forge's protection", run)
		}
		look := []string{"gh api --hostname github.com --paginate repos/acme/app/branches?protected=true&per_page=100 --jq .[].name"}
		if calls := forge.called(); !slices.Equal(calls, look) {
			t.Errorf("forge calls %q; want %q", calls, look)
		}

		// The protection lifted, Djinn reads it again only once its last read is protectedLookEvery old.
		forge.mu.Lock()
		forge.api = "main\n"
		forge.mu.Unlock()
		if _, err := in.h.Push(t.Context(), in.wishID, in.projectID, ""); !errors.Is(err, errProtectedPush) {
			t.Errorf("Push: %v; want feat/wish refused, as last read", err)
		}
		in.mu.Lock()
		in.now = in.now.Add(protectedLookEvery)
		in.mu.Unlock()
		if _, err := in.h.Push(t.Context(), in.wishID, in.projectID, ""); err != nil {
			t.Fatalf("Push: %v; want feat/wish pushed", err)
		}
		if got, tip := in.remoteTip(t, bare), in.tip(t); got != tip {
			t.Errorf("origin's %s at %s; want it pushed at %s", in.branch, got, tip)
		}
		if calls := forge.called(); len(calls) != 2 {
			t.Errorf("forge calls %q; want two reads", calls)
		}
	})

	t.Run("a branch a GitLab wildcard protects", func(t *testing.T) {
		t.Parallel()
		forge := &fakeForge{api: `{"name":"main"}` + "\n" + `{"name":"feat/*"}` + "\n"}
		in, bare := onForge(t, "git@gitlab.example.com:acme/app.git", forge)
		old := in.remoteTip(t, bare)
		if _, err := in.h.Push(t.Context(), in.wishID, in.projectID, ""); !errors.Is(err, errProtectedPush) ||
			!strings.Contains(err.Error(), "gitlab.example.com/acme/app protects feat/wish") {
			t.Errorf("Push: %v; want feat/wish refused, as feat/* protects it", err)
		}
		if got := in.remoteTip(t, bare); got != old {
			t.Errorf("origin's %s at %s; want it at %s", in.branch, got, old)
		}
		look := []string{"glab api --hostname gitlab.example.com --paginate --output ndjson projects/acme%2Fapp/protected_branches?per_page=100"}
		if calls := forge.called(); !slices.Equal(calls, look) {
			t.Errorf("forge calls %q; want %q", calls, look)
		}
	})

	t.Run("a forge Djinn cannot read", func(t *testing.T) {
		t.Parallel()
		in, bare := onForge(t, "https://github.com/acme/app.git", &fakeForge{}) // Its gh api fails.
		in.pass(t, 0)
		if got, tip := in.remoteTip(t, bare), in.tip(t); got != tip {
			t.Fatalf("origin's %s at %s; want it pushed at %s; push %v", in.branch, got, tip, in.pushState(t))
		}
	})
}

// TestProtects: a branch is protected by its name, or by a GitLab wildcard whose * stands for any text.
func TestProtects(t *testing.T) {
	t.Parallel()
	names := []string{"main", "release/*", "*-stable"}
	for branch, want := range map[string]bool{
		"main": true, "release/1.2": true, "release/a/b": true, "v2-stable": true,
		"mains": false, "feat/main": false, "release": false, "v2-stable-x": false,
	} {
		if got := protects(names, branch); got != want {
			t.Errorf("protects(%q, %q) = %v; want %v", names, branch, got, want)
		}
	}
	if protects(nil, "main") {
		t.Error("protects(nil, main); want false: nothing read, nothing protected")
	}
}
