package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// azimasOf are the codes of the azimas of the wish id, in the order the store lists them.
func (in *integration) azimasOf(t *testing.T, id string) []string {
	t.Helper()
	tasks, err := store.List[*planv1.Task](t.Context(), in.db, store.Where{"wish_id": id})
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, task := range tasks {
		if plan.IsAzima(task) {
			codes = append(codes, task.GetCode())
		}
	}
	return codes
}

// TestStartedOn (W355, wish Dew, 05/11/2026; tilasm L44, decision Q273 A): work of no azima whose worker started from
// the head of a merge request an azima carries, or from an azima's branch, lands on that azima's branch, never in main,
// nor in an azima made for it whose branch, cut from main, would carry the whole request again.
func TestStartedOn(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	// The incident: T1 carries a teammate's merge request, its local branch behind the remote's, the request's head.
	// W2, of no azima, starts from that head and commits on it. Djinn merged it into main, which then held the whole
	// request: it is now part of T1 and lands on the request's branch; main stays as it was, and no azima is made.
	t.Run("from the head of an azima's merge request", func(t *testing.T) {
		t.Parallel()
		r := withRequests(t)
		mr := r.onRemote(t, r.fork, "feature/doc-api", "doc.txt")
		t1 := r.azima(t, r.wishID, "Doc API")
		if _, err := r.nameBranch(t, t1.GetId(), "feature/doc-api"); err != nil {
			t.Fatal(err)
		}
		head := r.onRemote(t, mr, "feature/doc-api", "openapi.txt") // The request moves on; the local branch stays behind.
		if got := r.git(t, r.repo, "rev-parse", "refs/heads/feature/doc-api"); got != mr {
			t.Fatalf("local feature/doc-api at %s, want %s, behind the remote's", got, mr)
		}

		w2 := r.finished(t, "W2", nil)
		r.git(t, w2.GetWorktree(), "reset", "--quiet", "--hard", "origin/feature/doc-api")
		writeFile(t, w2.GetWorktree(), "app/src/parse.txt", "parse\n")
		commitAll(t, w2.GetWorktree(), "parseNumber")
		work := r.git(t, w2.GetWorktree(), "rev-parse", "HEAD")
		r.pass(t, 0)

		got := r.get(t, w2.GetId())
		if got.GetPartOf() != t1.GetId() {
			t.Errorf("W2 part of %q, want T1 (%s), whose request it started from", got.GetPartOf(), t1.GetId())
		}
		in := got.GetIntegration()
		if in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || in.GetBranch() != "feature/doc-api" {
			t.Fatalf("W2's integration %v, want committed into feature/doc-api", in)
		}
		tip := r.git(t, r.repo, "rev-parse", "refs/heads/feature/doc-api")
		if !isAncestor(t.Context(), r.repo, work, tip) || !isAncestor(t.Context(), r.repo, head, tip) {
			t.Errorf("feature/doc-api at %s, want W2's %s on the request's head %s", tip, work, head)
		}
		if got := r.tip(t); got != r.fork {
			t.Errorf("%s moved to %s, want %s: the request's work never lands in it", r.branch, got, r.fork)
		}
		if got := r.azimasOf(t, r.wishID); len(got) != 1 {
			t.Errorf("azimas %v, want T1 only: none made for W2", got)
		}
		events, err := store.List[*planv1.TaskEvent](t.Context(), r.db, store.Where{"task_id": w2.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		said := false
		for _, e := range events {
			said = said || strings.Contains(e.GetText(), "its branch started from origin/feature/doc-api, the branch of T1")
		}
		if !said {
			t.Errorf("W2's events say nothing of the branch it started from: %v", events)
		}
	})

	// Since W401, work spawned with no azima gets an azima of its own, its branch cut from main. W3's worker moves to
	// the branch of T1 and commits there: W3 joins T1 and lands on its branch; the azima made for it, emptied, is
	// closed, its branch holding nothing. W4, started from main, keeps the azima made for it.
	t.Run("from an azima's branch, its own azima closed", func(t *testing.T) {
		t.Parallel()
		r := withRequests(t)
		committing(r.env)
		t1 := r.azima(t, r.wishID, "First")
		w1 := spawnPart(t, r.env, r.wishID, r.projectID, t1.GetId(), "w1.txt")
		r.pass(t, 0)
		b1 := r.get(t, w1.GetId()).GetIntegration().GetBranch()
		if b1 == "" || b1 == r.branch {
			t.Fatalf("W1 merged into %q, want T1's own branch", b1)
		}

		r.h.providers[planv1.Provider_PROVIDER_FAKE] = corrector{work: func(dir, prompt string) string {
			name, ok := strings.CutPrefix(prompt, "file ")
			if !ok {
				return ""
			}
			name, _, _ = strings.Cut(name, "\n")
			if name == "w3.txt" { // Its worker checks out the branch of T1 first.
				if out, err := git(context.Background(), dir, "reset", "--quiet", "--hard", "refs/heads/"+b1); err != nil {
					return "fail " + err.Error() + ": " + out
				}
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o600); err != nil {
				return "fail " + err.Error()
			}
			for _, args := range [][]string{{"add", name}, {"commit", "--quiet", "-m", "work: " + name}} {
				if out, err := git(context.Background(), dir, args...); err != nil {
					return "fail " + err.Error() + ": " + out
				}
			}
			return "text Committed " + name + "."
		}}
		w3 := spawnPart(t, r.env, r.wishID, r.projectID, "", "w3.txt")
		own := r.stored(t, w3.GetPartOf())
		if !plan.IsAzima(own) || own.GetId() == t1.GetId() {
			t.Fatalf("W3 spawned part of %v, want an azima made for it", own)
		}
		cut := r.git(t, r.repo, "rev-parse", "refs/heads/"+azimaBranchName(own))
		w4 := spawnPart(t, r.env, r.wishID, r.projectID, "", "w4.txt")
		r.pass(t, 0)

		got := r.get(t, w3.GetId())
		if got.GetPartOf() != t1.GetId() || got.GetIntegration().GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED ||
			got.GetIntegration().GetBranch() != b1 {
			t.Fatalf("W3 part of %q, integration %v, want part of T1, committed into %s", got.GetPartOf(), got.GetIntegration(), b1)
		}
		if tip := r.git(t, r.repo, "rev-parse", "refs/heads/"+b1); !isAncestor(t.Context(), r.repo, got.GetIntegration().GetSha(), tip) {
			t.Errorf("%s at %s lacks W3's merge %s", b1, tip, got.GetIntegration().GetSha())
		}
		if a := r.stored(t, own.GetId()); a.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Errorf("%s, made for W3, is %v, want closed", own.GetCode(), a.GetStatus())
		}
		if tip := r.git(t, r.repo, "rev-parse", "refs/heads/"+azimaBranchName(own)); tip != cut {
			t.Errorf("%s's branch moved to %s, want %s: W3's work lands elsewhere", own.GetCode(), tip, cut)
		}
		if got := r.get(t, w4.GetId()); got.GetPartOf() == t1.GetId() || got.GetPartOf() == "" ||
			got.GetIntegration().GetBranch() == b1 {
			t.Errorf("W4, started from main, part of %q into %q, want the azima made for it", got.GetPartOf(), got.GetIntegration().GetBranch())
		}
		if got := r.tip(t); got != r.fork {
			t.Errorf("%s moved to %s, want %s", r.branch, got, r.fork)
		}
	})
}
