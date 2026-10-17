package harness

import (
	"context"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// W196 replays task W103 of wish Dew 13 oct (17/10/2026): in azima T2, done and its work in main through another
// merge request, W103 deleted T2's branch and made no commit. Djinn made the branch again from the remote's, merged
// W103 into it as an empty merge commit, and pushed it.

// TestNoCommitTaskIsNotMerged: a worker that ends done with no commit of its own is not merged: no integration record,
// said in its end's line to the lead; the integration branch does not move, nothing is pushed.
func TestNoCommitTaskIsNotMerged(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	bare := in.remote(t)
	queue := &leadQueue{}
	in.h.NotifyLead(queue.notify)
	old := in.tip(t)
	task := in.spawn(t, in.wishID, "text Deleted the branches: nothing to commit.")
	in.watch(t.Context(), t, task.GetId(), 0)
	in.pass(t, 0)

	got, texts := in.integration(t, task)
	if got != nil || len(texts) > 0 {
		t.Errorf("integration %v, events %q; want none", got, texts)
	}
	want := "Djinn: " + task.GetCode() + " done, " + nothingToMerge + ". Its final note: "
	if lines := endedEvents(queue, task.GetCode()); len(lines) != 1 || !strings.HasPrefix(lines[0], want) {
		t.Errorf("ended events %q; want one, %q…", lines, want)
	}
	if tip := in.tip(t); tip != old || in.remoteTip(t, bare) != old || len(in.commits(t)) > 0 || len(in.pushes(t)) > 0 {
		t.Errorf("branch at %s, origin at %s, commits %v, pushes %v; want all at %s, none", tip, in.remoteTip(t, bare),
			in.commits(t), in.pushes(t), old)
	}
}

// TestEmptyMergeMakesNoCommit: work whose merge changes nothing, its commits undoing each other, makes no merge commit:
// the branch stays where it is, and the lead hears there was nothing to merge.
func TestEmptyMergeMakesNoCommit(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	var told []string
	in.h.TellLeads(func(_ context.Context, _, line string) error {
		told = append(told, line)
		return nil
	})
	old := in.tip(t)
	w3 := in.finished(t, "W3", map[string]string{"app/src/b.txt": "b\n"})
	in.git(t, w3.GetWorktree(), "rm", "--quiet", "app/src/b.txt")
	commitAll(t, w3.GetWorktree(), "Drop b.txt again")
	in.pass(t, 0)

	got, texts := in.integration(t, w3)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || got.GetSha() != old {
		t.Errorf("W3 %v; want committed at %s", got, old)
	}
	if !slices.Contains(texts, w3.GetBranch()+" changes nothing in "+in.branch+": no merge commit") {
		t.Errorf("W3's events %q lack the merge undone", texts)
	}
	if tip := in.tip(t); tip != old || len(in.commits(t)) > 0 {
		t.Errorf("branch at %s, commits %v; want at %s, none", tip, in.commits(t), old)
	}
	if want := []string{"Djinn: W3 done, nothing to merge into " + in.branch + ". It left no final note."}; !slices.Equal(told, want) {
		t.Errorf("told %q; want %q", told, want)
	}
}

// TestDoneAzimaBranchNotMadeAgain: an azima's branch, pushed then deleted, is never made again nor pushed for work
// done in it since, the azima done (as the developer sees T2) or reopened by that work (as Djinn did): the work waits
// and the lead hears why once, in one line. Named again, the branch takes the work.
func TestDoneAzimaBranchNotMadeAgain(t *testing.T) {
	testx.Portable(t)
	for _, c := range []struct {
		name   string
		status planv1.TaskStatus
		why    func(branch string) string
	}{
		{"done", planv1.TaskStatus_TASK_STATUS_DONE, func(b string) string {
			return "azima T1 is done: Djinn merges into its branch " + b + " and pushes it no more; reopen T1 or drop the work"
		}},
		{"reopened", planv1.TaskStatus_TASK_STATUS_PENDING, func(b string) string {
			return b + ", the branch of azima T1, is deleted: Djinn makes it no more; reopen T1 (djinn task update T1 --branch) or drop the work"
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := integrating(t)
			in.setAzimaStrategy(t)
			bare := in.remote(t)
			queue := &leadQueue{}
			in.h.NotifyLead(queue.notify)
			t1 := in.azima(t, in.wishID, "Tenant isolation")
			branch := "djinn/T1-tenant-isolation"
			in.git(t, in.repo, "push", "--quiet", "origin", branch) // Its merge request's, closed since.
			pushed := in.git(t, bare, "rev-parse", "refs/heads/"+branch)
			in.git(t, in.repo, "branch", "--quiet", "-D", branch)
			azima := in.stored(t, t1.GetId())
			azima.Status = c.status
			putTaskInDB(t, in.db, azima)

			w2 := in.finished(t, "W2", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
			in.pass(t, 0)
			in.pass(t, 0)

			if sha := commitOf(t.Context(), in.repo, "refs/heads/"+branch); sha != "" {
				t.Errorf("%s made again at %s", branch, sha)
			}
			if got := in.git(t, bare, "rev-parse", "refs/heads/"+branch); got != pushed || len(in.pushes(t)) > 0 {
				t.Errorf("origin's %s at %s, pushes %v; want at %s, none", branch, got, in.pushes(t), pushed)
			}
			why := c.why(branch)
			if got := in.get(t, w2.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING ||
				got.GetReason() != why || got.GetBranch() != branch {
				t.Errorf("W2 %v; want pending into %s: %s", got, branch, why)
			}
			var lines []string
			for _, e := range queue.all() {
				if strings.Contains(e.GetText(), branch) {
					lines = append(lines, e.GetText())
				}
			}
			if want := []string{"Djinn: " + why + "."}; !slices.Equal(lines, want) {
				t.Errorf("lead told %q; want %q", lines, want)
			}
			if c.status == planv1.TaskStatus_TASK_STATUS_DONE {
				return
			}
			// Named again by the developer, the branch is made from the remote's, and the work merges into it.
			if _, err := in.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
				TaskId: t1.GetId(), Branch: &branch,
			})); err != nil {
				t.Fatal(err)
			}
			in.pass(t, 0)
			if got := in.get(t, w2.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED ||
				!isAncestor(t.Context(), in.repo, pushed, got.GetSha()) {
				t.Errorf("W2 named again %v; want committed on %s's %s", got, branch, pushed[:8])
			}
		})
	}
}

// TestLiveAzimaMergesAndPushes: an azima not done, its branch there, still takes its work and pushes its branch at its
// end, as before W196.
func TestLiveAzimaMergesAndPushes(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.setAzimaStrategy(t)
	bare := in.remote(t)
	t1 := in.azima(t, in.wishID, "Tenant isolation")
	branch := "djinn/T1-tenant-isolation"
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
	in.pass(t, 0)

	got := in.get(t, w1.GetId()).GetIntegration()
	tip := in.git(t, in.repo, "rev-parse", "refs/heads/"+branch)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || got.GetSha() != tip {
		t.Fatalf("W1 %v; want committed at %s's %s", got, branch, tip[:8])
	}
	if remote := in.git(t, bare, "rev-parse", "refs/heads/"+branch); remote != tip {
		t.Errorf("origin's %s at %s; want pushed at %s", branch, remote, tip)
	}
}
