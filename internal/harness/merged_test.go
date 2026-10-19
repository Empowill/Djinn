package harness

import (
	"cmp"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// landedLines are the lines of kind landed the lead got, about code.
func landedLines(q *leadQueue, code string) []string {
	var lines []string
	for _, e := range q.all() {
		if e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_LANDED && e.GetCode() == code {
			lines = append(lines, e.GetText())
		}
	}
	return lines
}

// TestLookMerged: an azima is marked merged into main by its commits all in the remote's main, fetched, or by
// its request merged as its babysit watcher said; not by a request closed without a merge. A task added to it clears
// the mark; once its work is in main too, the mark comes back. The lead hears each mark once, and, once every azima
// carries it, to propose to grant the wish; the brief says it.
func TestLookMerged(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	t.Run("into main", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		bare := in.remote(t)
		in.git(t, in.repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+in.branch)
		queue := &leadQueue{}
		in.h.NotifyLead(queue.notify)
		ctx := t.Context()
		main := in.tip(t)

		// work is a done task part of azima on a branch of its own: one commit on top of from, merged as it; the task id
		// when given, a new one else.
		work := func(code string, azima *planv1.Task, from string, id ...string) *planv1.Task {
			t.Helper()
			in.mu.Lock()
			in.now = in.now.Add(time.Second)
			in.mu.Unlock()
			branch := strings.ToLower(code)
			tip := in.git(t, in.repo, "commit-tree", from+"^{tree}", "-p", from, "-m", "Work of "+code)
			in.git(t, in.repo, "update-ref", "refs/heads/"+branch, tip)
			task := &planv1.Task{
				Id: cmp.Or(strings.Join(id, ""), store.NewID()), WishId: in.wishID, ProjectId: in.projectID, Code: code,
				Title: "Work of " + code, Kind: planv1.TaskKind_TASK_KIND_WORK, Status: planv1.TaskStatus_TASK_STATUS_DONE, Provider: planv1.Provider_PROVIDER_FAKE,
				Branch: branch, PartOf: azima.GetId(), EndTime: timestamppb.New(in.clock()),
				Integration: &planv1.TaskIntegration{
					State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, Sha: tip, UpdateTime: timestamppb.New(in.clock()),
				},
			}
			putTaskInDB(t, in.db, task)
			return task
		}
		// watcher babysits url, part of azima: merged when its done line came at merged, closed without a merge else.
		watcher := func(code, url string, azima *planv1.Task, merged *time.Time) {
			t.Helper()
			b := &planv1.TaskBabysit{Url: url, Number: 7, DoneWhen: "MERGED"}
			if merged != nil {
				b.DoneTime = timestamppb.New(*merged)
			}
			putTaskInDB(t, in.db, &planv1.Task{
				Id: store.NewID(), WishId: in.wishID, ProjectId: in.projectID, Code: code, Title: "Babysit " + url,
				Status: planv1.TaskStatus_TASK_STATUS_DONE, Provider: planv1.Provider_PROVIDER_WATCH, PartOf: azima.GetId(), Babysit: b,
			})
		}
		wish := func() *planv1.Wish {
			w, err := store.Get[*planv1.Wish](ctx, in.db, in.wishID)
			if err != nil {
				t.Fatal(err)
			}
			return w
		}
		look := func(now bool) { in.h.lookMerged(ctx, wish(), now, map[string]error{}) }
		merged := func(a *planv1.Task) *planv1.AzimaMerged { return in.stored(t, a.GetId()).GetAzima().GetMerged() }
		toMain := func(branch string) {
			t.Helper()
			in.git(t, in.repo, "push", "--quiet", "origin", "refs/heads/"+branch+":refs/heads/"+in.branch)
			in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+in.branch, main) // as if another clone pushed it: fetched
		}

		t1 := in.azima(t, in.wishID, "Merged by its request")
		t2 := in.azima(t, in.wishID, "Merged by its commits")
		t3 := in.azima(t, in.wishID, "Request closed")
		work("W1", t1, main)
		w2 := work("W2", t2, main)
		work("W3", t3, main)
		at := in.clock().Add(time.Minute)
		watcher("W4", "https://github.com/acme/lamp/pull/7", t1, &at)
		watcher("W5", "https://github.com/acme/lamp/pull/9", t3, nil)

		look(true)
		if m := merged(t1); m.GetRequestUrl() != "https://github.com/acme/lamp/pull/7" || !m.GetTime().AsTime().Equal(at) {
			t.Errorf("T1 merged %v, want by its request, when its watcher said so", m)
		}
		if m := merged(t2); m != nil {
			t.Errorf("T2 merged %v, its commit not in main yet", m)
		}
		if m := merged(t3); m != nil {
			t.Errorf("T3 merged %v, its request closed without a merge", m)
		}
		if got := landedLines(queue, "T1"); len(got) != 1 || !strings.Contains(got[0], "pull/7") ||
			!strings.HasSuffix(got[0], "not in main yet: T2, T3.") {
			t.Errorf("the lead heard of T1 %q, want once, its request, and T2, T3 not in main yet", got)
		}

		// W2's commit reaches the remote's main from another clone: Djinn fetches it.
		toMain("w2")
		main = in.git(t, bare, "rev-parse", "refs/heads/"+in.branch)
		in.mu.Lock()
		in.now = in.now.Add(time.Hour) // its look is due: the merge of main's pass looks
		in.mu.Unlock()
		in.h.mainPass(ctx)
		if m := merged(t2); m.GetMainSha() != main || m.GetRequestUrl() != "" {
			t.Errorf("T2 merged %v, want by its commits in main at %s", m, main)
		}
		if got := len(landedLines(queue, "T1")); got != 1 {
			t.Errorf("the lead heard %d times of T1, want once", got)
		}

		// A task added to T2 clears its mark; settled, its work not in main, T2 stays unmarked.
		// It waits for T3, which no worker closes: it stays planned.
		added, err := in.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: in.wishID, Title: "More", Prompt: "text More.", Provider: planv1.Provider_PROVIDER_FAKE,
			PartOf: t2.GetId(), DependsOn: []string{t3.GetId()},
		}))
		if err != nil {
			t.Fatal(err)
		}
		if m := merged(t2); m != nil {
			t.Errorf("T2 merged %v once a task was added to it, want cleared", m)
		}
		look(true)
		if m := merged(t2); m != nil {
			t.Errorf("T2 merged %v while its new task waits", m)
		}
		// It ends done, its work merged into its branch, not in main.
		w6 := work(added.Msg.GetTask().GetCode(), t2, w2.GetIntegration().GetSha(), added.Msg.GetTask().GetId())
		look(true)
		if m := merged(t2); m != nil {
			t.Errorf("T2 merged %v, W6 not in main yet", m)
		}
		// Its merge brings it back; the look waits for its cadence, unless asked now.
		toMain(w6.GetBranch())
		look(false)
		if m := merged(t2); m != nil {
			t.Errorf("T2 merged %v before its look is due", m)
		}
		look(true)
		if m := merged(t2); m.GetMainSha() != w6.GetIntegration().GetSha() {
			t.Errorf("T2 merged %v, want by its commits in main at W6's", m)
		}
		if got := landedLines(queue, "T2"); len(got) != 2 {
			t.Errorf("the lead heard of T2 %q, want twice: before and after W6", got)
		}

		// T3's commit in main too: every azima is, and the lead hears it to propose to grant the wish.
		in.git(t, in.repo, "fetch", "--quiet", "origin")
		merge := in.git(t, in.repo, "commit-tree", main+"^{tree}", "-p", "refs/remotes/origin/"+in.branch, "-p", "refs/heads/w3",
			"-m", "Merge W3")
		in.git(t, in.repo, "update-ref", "refs/heads/w3m", merge)
		toMain("w3m")
		look(true)
		if got := landedLines(queue, "T3"); len(got) != 1 || !strings.Contains(got[0], "propose to grant the wish") {
			t.Errorf("the lead heard of T3 %q, want every azima in main", got)
		}
		tasks, err := store.List[*planv1.Task](ctx, in.db, store.Where{"wish_id": in.wishID})
		if err != nil {
			t.Fatal(err)
		}
		if !plan.AllMerged(plan.WithAzimas(tasks)) {
			t.Error("every azima in main, as read: want all merged")
		}
	})

	// A task part of an azima opens a merge request; the babysit watcher Djinn starts on it prints MERGED: the azima's
	// work is all in main, by its request.
	t.Run("by its watcher", func(t *testing.T) {
		t.Parallel()
		const url = "https://gitlab.com/acme/dolmen/-/merge_requests/123"
		forge := &fakeForge{views: map[string]string{"123": `{"iid":123,"state":"opened","web_url":"` + url + `"}`}}
		e, queue, wishID, projectID := babysitWish(t, forge, babysitRepo(t, "git@gitlab.com:acme/dolmen.git", gitlabBabysit, ""))
		azima := e.azima(t, wishID, "Open the MR")
		opener := endTask(t, e, wishID, projectID, strings.Join([]string{
			`tool Bash {"command":"glab mr create --draft --fill --yes"}`,
			"result Creating draft merge request for w1 into main in acme/dolmen " + url,
			"text Draft MR opened.",
		}, "\n"), azima.GetId())
		if opener.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Fatalf("the opener: %s %s", opener.GetStatus(), opener.GetError())
		}
		w := watchers(t, e, wishID)[0]
		waitFor(t, "the watcher ended on MERGED", func() bool {
			return e.get(t, w.GetId()).GetBabysit().GetDoneTime() != nil
		})
		wish, err := store.Get[*planv1.Wish](t.Context(), e.db, wishID)
		if err != nil {
			t.Fatal(err)
		}
		e.h.lookMerged(t.Context(), wish, false, map[string]error{})
		if m := e.get(t, azima.GetId()).GetAzima().GetMerged(); m.GetRequestUrl() != url {
			t.Errorf("merged %v, want by %s", m, url)
		}
		if got := landedLines(queue, azima.GetCode()); len(got) != 1 || !strings.Contains(got[0], "propose to grant the wish") {
			t.Errorf("the lead heard %q, want once, every azima in main", got)
		}
	})
}
