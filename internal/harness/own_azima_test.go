package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// committing makes e's fake worker commit the file a prompt "file <name>" names, on its first line, in its worktree.
func committing(e *env) {
	e.h.setProvider(planv1.Provider_PROVIDER_FAKE, corrector{work: func(dir, prompt string) string {
		name, ok := strings.CutPrefix(prompt, "file ")
		if !ok {
			return ""
		}
		name, _, _ = strings.Cut(name, "\n") // The worker's rules follow the task's prompt.
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o600); err != nil {
			return "fail " + err.Error()
		}
		for _, args := range [][]string{{"add", name}, {"commit", "--quiet", "-m", "work: " + name}} {
			if out, err := git(context.Background(), dir, args...); err != nil {
				return "fail " + err.Error() + ": " + out
			}
		}
		return "text Committed " + name + "."
	}})
}

// TestOwnAzima (Q268 B, review of PR #17): in a wish that pushes per azima, Djinn pushes the azimas' branches, never the
// wish's integration branch, main itself on djinn. Work spawned with no azima merged there and stayed in the clone,
// never pushed. It is now made part of an azima made for it at the spawn, titled as it, its branch cut from main before
// its worker starts, and that branch is pushed once its work is merged.
func TestOwnAzima(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("spawned work of no azima", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		committing(in.env)
		bare := in.remote(t)
		in.setAzimaStrategy(t)
		mainSha := in.tip(t)

		w1 := spawnPart(t, in.env, in.wishID, in.projectID, "", "w1.txt")
		azima := in.stored(t, w1.GetPartOf())
		if !plan.IsAzima(azima) || azima.GetTitle() != w1.GetTitle() || azima.GetCode() != "T1" || azima.GetProjectId() != in.projectID {
			t.Fatalf("W1 part of %v, want an azima T1 titled %q in its project", azima, w1.GetTitle())
		}
		b1 := "djinn/T1-silae-w1-txt"
		if a := azima.GetAzima(); a.GetBranch() != b1 || a.GetCutFrom() != mainSha {
			t.Errorf("T1 records %v, want %s cut from main's %s", a, b1, mainSha)
		}
		events, err := store.List[*planv1.TaskEvent](t.Context(), in.db, store.Where{"task_id": azima.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 0 || !strings.Contains(events[0].GetText(), "made for W1") {
			t.Errorf("T1's events %v, want one saying it was made for W1", events)
		}

		in.pass(t, 0)
		tip := in.git(t, in.repo, "rev-parse", "refs/heads/"+b1)
		if got := in.get(t, w1.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED ||
			got.GetBranch() != b1 || got.GetSha() != tip {
			t.Fatalf("W1's integration %v, want committed into %s at %s", got, b1, tip)
		}
		if parent := in.git(t, in.repo, "rev-parse", tip+"^"); parent != mainSha {
			t.Errorf("%s's commit sits on %s, want main's %s", b1, parent, mainSha)
		}
		if got := in.tip(t); got != mainSha {
			t.Errorf("%s moved to %s, want %s: no work lands in it", in.branch, got, mainSha)
		}
		if got := commitOf(t.Context(), bare, "refs/heads/"+b1); got != tip {
			t.Errorf("origin's %s at %q, want %s pushed", b1, got, tip)
		}
	})

	// Only work that merges gets an azima, only in a wish that pushes per azima: an investigation merges nothing, and
	// work of a wish that pushes per wish lands in the wish's branch, which Djinn pushes.
	t.Run("investigation and per wish stay out", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		committing(in.env)
		if w1 := spawnPart(t, in.env, in.wishID, in.projectID, "", "w1.txt"); w1.GetPartOf() != "" {
			t.Errorf("per wish, W1 part of %s, want none", w1.GetPartOf())
		}
		in.setAzimaStrategy(t)
		res, err := in.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: in.wishID, ProjectId: in.projectID, Title: "Study", Prompt: "study",
			Provider: planv1.Provider_PROVIDER_FAKE, Kind: planv1.TaskKind_TASK_KIND_INVESTIGATION,
		}))
		if err != nil {
			t.Fatal(err)
		}
		if got := res.Msg.GetTask().GetPartOf(); got != "" {
			t.Errorf("the investigation is part of %s, want none", got)
		}
		tasks, err := store.List[*planv1.Task](t.Context(), in.db, store.Where{"wish_id": in.wishID})
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range tasks {
			if plan.IsAzima(task) {
				t.Errorf("azima %s %q made, want none", task.GetCode(), task.GetTitle())
			}
		}
	})

	// Its azima would wait for another, which no azima of such a wish does: refused, saying what to do, nothing made.
	t.Run("after work of another azima refused", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.setAzimaStrategy(t)
		t1 := in.azima(t, in.wishID, "First")
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
		_, err := in.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: in.wishID, ProjectId: in.projectID, Title: "Later", Prompt: "later", After: []string{w1.GetId()},
			Provider: planv1.Provider_PROVIDER_FAKE,
		}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "--part-of") || !strings.Contains(err.Error(), "T2 after T1") {
			t.Fatalf("spawn after W1 with no azima: %v, want refused naming --part-of", err)
		}
		tasks, err := store.List[*planv1.Task](t.Context(), in.db, store.Where{"wish_id": in.wishID})
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks) != 2 {
			t.Errorf("%d tasks, want T1 and W1 only", len(tasks))
		}
	})
}
