package harness

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// openAzimaRequest makes an azima of in, T1, with one part done and its branch pushed, whose request the forge says
// open for its branch with view: past its draft, Djinn records it open. It returns the azima and its branch.
func openAzimaRequest(t *testing.T, in *integration, forge *fakeForge, view string) (*planv1.Task, string) {
	t.Helper()
	b1 := "djinn/T1-one-alpha"
	forge.mu.Lock()
	forge.views[b1] = view
	forge.mu.Unlock()
	t1 := in.azima(t, in.wishID, "One Alpha")
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
	in.pass(t, 0)
	in.git(t, in.repo, "push", "origin", b1)
	in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+b1, in.git(t, in.repo, "rev-parse", "refs/heads/"+b1))
	pastDraft(t, in)
	if got := in.get(t, t1.GetId()).GetAzima().GetPr().GetState(); got != planv1.AzimaPrState_AZIMA_PR_STATE_OPEN {
		t.Fatalf("T1's request = %v, want open", got)
	}
	return t1, b1
}

// TestAzimaPRFollowed: a request recorded open is read on the forge again by its number, whatever the azima's parts
// do and once its branch is gone, so that Djinn never shows it open once merged or closed (W81, wish Dew: dolmen!3011
// merged beside a watcher that runs for good, dolmen!2776 closed without a merge, both left open).
func TestAzimaPRFollowed(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	// A watcher that runs for good is part of the azima: its request, merged and its branch deleted, shows merged, and
	// the azima's work is in main; the azima stays open while the watcher runs.
	t.Run("merged beside a watcher", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.setAzimaStrategy(t)
		in.remote(t)
		forge := &fakeForge{views: map[string]string{}}
		in.mu.Lock()
		in.forge = forge
		in.mu.Unlock()
		const url = "https://github.com/acme/app/pull/42"
		t1, b1 := openAzimaRequest(t, in, forge,
			`{"number":42,"state":"OPEN","baseRefName":"main","mergeable":"MERGEABLE","url":"`+url+`"}`)

		putTask(t, in.db, &planv1.Task{
			Id: store.NewID(), WishId: in.wishID, ProjectId: in.projectID, Code: "W2", Title: "Watch the deploys",
			Kind: planv1.TaskKind_TASK_KIND_WATCHER, Provider: planv1.Provider_PROVIDER_WATCH, Restart: true,
			Status: planv1.TaskStatus_TASK_STATUS_RUNNING, PartOf: t1.GetId(), StartTime: timestamppb.New(in.clock()),
		}, "watch the deploys")
		forge.mu.Lock()
		delete(forge.views, b1) // deleted with the merge: the forge finds no request for it any more
		forge.views["42"] = `{"number":42,"state":"MERGED","baseRefName":"main","mergeable":"UNKNOWN","url":"` + url + `"}`
		forge.mu.Unlock()

		in.pass(t, 0)

		got := in.get(t, t1.GetId())
		if pr := got.GetAzima().GetPr(); pr.GetState() != planv1.AzimaPrState_AZIMA_PR_STATE_MERGED || pr.GetUrl() != url {
			t.Errorf("T1's request = %v, want merged at %s", pr, url)
		}
		if got.GetAzima().GetMerged().GetRequestUrl() != url {
			t.Errorf("T1 merged into main = %v, want by %s", got.GetAzima().GetMerged(), url)
		}
		if got.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE {
			t.Errorf("T1 is done while its watcher runs")
		}
		if !slices.Contains(forge.called(), "gh pr view 42 --json number,state,baseRefName,mergeable,url") {
			t.Errorf("the request was not read by its number: %v", forge.called())
		}
	})

	// A merge request closed without a merge shows closed, and the lead hears it once; Djinn closes nothing, proposes
	// none again while the forge lists only open ones for the branch, and follows it again once it is reopened.
	t.Run("closed without merging", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.setAzimaStrategy(t)
		in.remote(t)
		queue := &leadQueue{}
		in.h.NotifyLead(queue.notify)
		forge := &fakeForge{views: map[string]string{}}
		in.mu.Lock()
		in.forge = forge
		in.mu.Unlock()
		err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
			p, err := store.Get[*planv1.Project](t.Context(), tx, in.projectID)
			if err != nil {
				return err
			}
			p.Remote = "https://gitlab.example.com/acme/project.git"
			if err := tx.Journal("test", "remote", p); err != nil {
				return err
			}
			return tx.Put(p)
		})
		if err != nil {
			t.Fatal(err)
		}
		const url = "https://gitlab.example.com/acme/project/-/merge_requests/301"
		opened := `{"iid":301,"state":"opened","target_branch":"main","web_url":"` + url + `","detailed_merge_status":"mergeable","head_pipeline":{"id":9,"status":"running"}}`
		t1, b1 := openAzimaRequest(t, in, forge, opened)

		forge.mu.Lock()
		delete(forge.views, b1)
		forge.views["301"] = `{"iid":301,"state":"closed","target_branch":"main","web_url":"` + url + `"}`
		forge.mu.Unlock()
		in.pass(t, 0)
		in.pass(t, 0)

		got := in.get(t, t1.GetId())
		if pr := got.GetAzima().GetPr(); pr.GetState() != planv1.AzimaPrState_AZIMA_PR_STATE_CLOSED || pr.GetUrl() != url {
			t.Errorf("T1's request = %v, want closed at %s", pr, url)
		}
		if got.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE || got.GetAzima().GetMerged() != nil {
			t.Errorf("T1 = %v, merged %v; want it open, not merged", got.GetStatus(), got.GetAzima().GetMerged())
		}
		lines := leadLines(queue, "T1")
		closed := slices.DeleteFunc(slices.Clone(lines), func(l string) bool { return !strings.Contains(l, "closed without merging") })
		if want := "move T1: MR !301 closed without merging, the azima stays open"; len(closed) != 1 || closed[0] != want {
			t.Errorf("lead lines about T1 = %q, want %q once", lines, want)
		}
		if slices.ContainsFunc(in.questions(t), func(q *planv1.Question) bool {
			return strings.Contains(q.GetText(), "Open a pull request")
		}) {
			t.Errorf("a pull request was proposed again for the closed one: %v", in.questions(t))
		}

		forge.mu.Lock()
		forge.views[b1] = opened
		forge.mu.Unlock()
		in.pass(t, 0)
		if pr := in.get(t, t1.GetId()).GetAzima().GetPr(); pr.GetState() != planv1.AzimaPrState_AZIMA_PR_STATE_OPEN {
			t.Errorf("T1's request reopened = %v, want open", pr)
		}
	})
}
