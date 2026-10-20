package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// remote gives the repository a remote, origin, a bare repository in a temporary folder that holds the integration
// branch as it is now, and returns its folder.
func (in *integration) remote(t *testing.T) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "origin.git")
	in.git(t, in.repo, "init", "--quiet", "--bare", bare)
	in.git(t, in.repo, "remote", "add", "origin", bare)
	in.git(t, in.repo, "push", "--quiet", "origin", in.branch)
	return bare
}

// remoteTip is where the remote's integration branch is.
func (in *integration) remoteTip(t *testing.T, bare string) string {
	t.Helper()
	return in.git(t, bare, "rev-parse", "refs/heads/"+in.branch)
}

// pushFromOther commits file on top of bare's integration branch directly in bare without cloning a second repository
// or adding the commit object to in.repo.
func (in *integration) pushFromOther(t *testing.T, bare, file, content, msg string) string {
	t.Helper()
	wt := t.TempDir()
	in.git(t, bare, "symbolic-ref", "HEAD", "refs/heads/"+in.branch)
	in.git(t, bare, "read-tree", in.branch)
	writeFile(t, wt, file, content)
	in.git(t, bare, "--work-tree="+wt, "add", file)
	in.git(t, bare, "--work-tree="+wt, "-c", "user.name=Other", "-c", "user.email=other@example.com", "commit", "--quiet", "-m", msg)
	return in.remoteTip(t, bare)
}

// pushes are the pushes the journal recorded.
func (in *integration) pushes(t *testing.T) []*planv1.IntegrationPush {
	t.Helper()
	entries, err := store.Commands(t.Context(), in.db, func(c store.Command) bool { return c.Method == methodPush })
	if err != nil {
		t.Fatal(err)
	}
	var out []*planv1.IntegrationPush
	for _, e := range entries {
		p := &planv1.IntegrationPush{}
		if err := proto.Unmarshal(e.Request, p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// pushState is where the push of the wish's integration branch stands.
func (in *integration) pushState(t *testing.T) *planv1.WishPush {
	t.Helper()
	wish, err := store.Get[*planv1.Wish](t.Context(), in.db, in.wishID)
	if err != nil {
		t.Fatal(err)
	}
	return pushState(wish, in.projectID)
}

// hold is the hold of the wish's integration branch on its red push checks; nil when none.
func (in *integration) hold(t *testing.T) *planv1.PushHold {
	t.Helper()
	return in.holdOn(t, in.branch)
}

// holdOn is the hold of branch, the wish's integration branch or an azima's, on its red push checks; nil when none.
func (in *integration) holdOn(t *testing.T, branch string) *planv1.PushHold {
	t.Helper()
	wish, err := store.Get[*planv1.Wish](t.Context(), in.db, in.wishID)
	if err != nil {
		t.Fatal(err)
	}
	return holdOf(wish, in.projectID, branch)
}

// pushQuestion is the open question Djinn asked about the push.
func (in *integration) pushQuestion(t *testing.T) *planv1.Question {
	t.Helper()
	q, err := store.Get[*planv1.Question](t.Context(), in.db, in.pushState(t).GetQuestionId())
	if err != nil {
		t.Fatalf("no question about the push: %v; %v", in.pushState(t), err)
	}
	return q
}

// attachPushQuestion attaches a question to wish's push record, as ask mode asks it, to test answering it.
func (in *integration) attachPushQuestion(t *testing.T) *planv1.Question {
	t.Helper()
	q := &planv1.Question{
		Id:         store.NewID(),
		WishId:     in.wishID,
		Code:       "Q01",
		Text:       "Push " + in.branch + " to origin?",
		Options:    []string{pushOption, notNowOption},
		CreateTime: timestamppb.Now(),
	}
	if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actorLocal, "test/question", q); err != nil {
			return err
		}
		if err := plan.Ask(t.Context(), tx, q); err != nil {
			return err
		}
		wish, err := store.Get[*planv1.Wish](t.Context(), tx, in.wishID)
		if err != nil {
			return err
		}
		p := pushState(wish, in.projectID)
		if p == nil {
			p = &planv1.WishPush{ProjectId: in.projectID}
			wish.Pushes = append(wish.Pushes, p)
		}
		p.QuestionId, p.QuestionBranch = q.GetId(), in.branch
		return tx.Put(wish)
	}); err != nil {
		t.Fatal(err)
	}
	return q
}

// repairByLead spawns the lead's repair of the red push checks, title, and gives it once ended.
func (in *integration) repairByLead(t *testing.T, title string) *planv1.Task {
	t.Helper()
	res, err := in.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: in.wishID, ProjectId: in.projectID, Provider: planv1.Provider_PROVIDER_FAKE, Title: title, Repair: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return in.ended(t, res.Msg.GetTask().GetId())
}

// later moves the clock by d, without a pass.
func (in *integration) later(d time.Duration) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.now = in.now.Add(d)
}

// syncOf is the sync of branch among the project's, failing without one.
func (in *integration) syncOf(t *testing.T, project *planv1.Project, branch string) *planv1.ProjectSync {
	t.Helper()
	syncs, err := in.h.Syncs(t.Context(), project)
	if err != nil {
		t.Fatalf("Syncs: %v", err)
	}
	for _, s := range syncs {
		if s.GetBranch() == branch {
			return s
		}
	}
	t.Fatalf("no sync of %s among %v", branch, syncs)
	return nil
}

func TestPushDue(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	task := func(code string, status planv1.TaskStatus, state planv1.IntegrationState, partOf string) *planv1.Task {
		return &planv1.Task{
			Id: code, Code: code, Status: status, Kind: planv1.TaskKind_TASK_KIND_WORK, PartOf: partOf,
			Integration: &planv1.TaskIntegration{State: state},
		}
	}
	watcher := func(code string, status planv1.TaskStatus, partOf string) *planv1.Task {
		return &planv1.Task{Id: code, Code: code, Status: status, Kind: planv1.TaskKind_TASK_KIND_WORK, Provider: planv1.Provider_PROVIDER_WATCH, PartOf: partOf}
	}
	done, running := planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_RUNNING
	committed, pending := planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, planv1.IntegrationState_INTEGRATION_STATE_PENDING
	none := planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED
	t1 := &planv1.Task{Id: "a", Code: "T1", Kind: planv1.TaskKind_TASK_KIND_AZIMA}
	t2 := &planv1.Task{Id: "b", Code: "T2", Kind: planv1.TaskKind_TASK_KIND_AZIMA, PartOf: "a"}
	w1, w2, w3 := task("W1", done, committed, "a"), task("W2", done, committed, ""), task("W3", done, committed, "")
	for _, c := range []struct {
		name             string
		committed, tasks []*planv1.Task
		since            time.Duration
		want             string
	}{
		{"an azima with a part still running", []*planv1.Task{w1}, []*planv1.Task{t1, w1, task("W4", running, none, "a")}, 0, ""},
		{"an azima with a part still to commit", []*planv1.Task{w1}, []*planv1.Task{t1, w1, task("W4", done, pending, "a")}, 0, ""},
		{"an azima's last part committed", []*planv1.Task{w1}, []*planv1.Task{t1, w1}, 0, "the azima T1 ends"},
		// The rule of the azima's state (plan.FillAzimas): a failed part has not finished, one left interrupted has.
		{"an azima whose other part failed", []*planv1.Task{w1}, []*planv1.Task{t1, w1, task("W4", planv1.TaskStatus_TASK_STATUS_FAILED, none, "a")}, 0, ""},
		{"an azima whose other part was cut short", []*planv1.Task{w1}, []*planv1.Task{t1, w1, task("W4", planv1.TaskStatus_TASK_STATUS_INTERRUPTED, none, "a")}, 0, "the azima T1 ends"},
		{"an azima whose azima part waits", []*planv1.Task{w1}, []*planv1.Task{t1, w1, t2, task("W6", done, pending, "b")}, 0, ""},
		{"an azima whose azima part ended", []*planv1.Task{w1}, []*planv1.Task{t1, w1, t2, task("W6", done, committed, "b")}, 0, "the azima T1 ends"},
		// A watcher runs until the merge: it holds no push, a work part still open does (Q10).
		{"an azima with only its watcher running", []*planv1.Task{w1}, []*planv1.Task{t1, w1, watcher("W7", running, "a")}, 0, "the azima T1 ends"},
		{"an azima whose azima part has only its watcher running", []*planv1.Task{w1}, []*planv1.Task{t1, w1, t2, task("W6", done, committed, "b"), watcher("W7", running, "b")}, 0, "the azima T1 ends"},
		{"an azima with its watcher and a part running", []*planv1.Task{w1}, []*planv1.Task{t1, w1, watcher("W7", running, "a"), task("W4", running, none, "a")}, 0, ""},
		{"an azima with its watcher and a part still open", []*planv1.Task{w1}, []*planv1.Task{t1, w1, watcher("W7", running, "a"), task("W4", planv1.TaskStatus_TASK_STATUS_PENDING, none, "a")}, 0, ""},
		{"three tasks, 59 minutes", []*planv1.Task{w2, w3, task("W5", done, committed, "")}, nil, 59 * time.Minute, ""},
		{"three tasks, an hour", []*planv1.Task{w2, w3, task("W5", done, committed, "")}, nil, time.Hour, ""},
		{"three tasks, more than an hour", []*planv1.Task{w2, w3, task("W5", done, committed, "")}, nil, time.Hour + time.Second,
			"3 tasks committed, and more than an hour since the last push"},
		{"two tasks, a day", []*planv1.Task{w2, w3}, nil, 24 * time.Hour, ""},
	} {
		if got := pushDue(c.committed, c.tasks, now.Add(-c.since), now, time.Hour, 3); got != c.want {
			t.Errorf("%s: %q; want %q", c.name, got, c.want)
		}
	}
	// An azima whose only live work is its watcher ends for the push and reads watching all the same (W86).
	for _, tasks := range [][]*planv1.Task{
		{t1, w1, watcher("W7", running, "a")},
		{t1, w1, t2, task("W6", done, committed, "b"), watcher("W7", running, "b")},
	} {
		if s := plan.WithAzimas(tasks)[0].GetAzima().GetState(); s != planv1.AzimaState_AZIMA_STATE_WATCHING {
			t.Errorf("T1 with only a watcher running reads %v; want watching", s)
		}
	}
}

func TestCommittedSince(t *testing.T) {
	t.Parallel()
	push := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	committed := func(code, project string, at time.Time) *planv1.Task {
		return &planv1.Task{Id: code, Code: code, ProjectId: project, Integration: &planv1.TaskIntegration{
			State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, UpdateTime: timestamppb.New(at),
		}}
	}
	red := committed("W4", "p", push.Add(time.Minute))
	red.Integration.State = planv1.IntegrationState_INTEGRATION_STATE_RED
	tasks := []*planv1.Task{
		committed("W3", "p", push.Add(2*time.Minute)), committed("W1", "p", push.Add(-time.Minute)), committed("W2", "p", push.Add(time.Minute)),
		committed("W5", "q", push.Add(time.Minute)), red, committed("W0", "p", push),
	}
	// Only the project's work committed after the push, in the order it was: the count starts again at each push.
	if got := codes(committedSince(tasks, "p", push)); got != "W2, W3" {
		t.Errorf("committed since the push: %s", got)
	}
}

func TestPushIfDue(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	// The integration branch is pushed once an azima's last part is committed, not before, with the person's own git,
	// and the push recorded: in the journal, on the wish, in each task's events. An azima with a failed part has not
	// ended: its other part committed waits for the next push.
	t.Run("at an azima end", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		bare := in.remote(t)
		old := in.tip(t)
		numbers := in.azima(t, in.wishID, "Numbers")
		partOfNumbers := func(task *planv1.Task) { task.PartOf = numbers.GetId() }
		w0 := in.finished(t, "W0", map[string]string{"app/src/0.txt": "0\n"}, partOfNumbers)
		in.finished(t, "W9", nil, partOfNumbers, func(task *planv1.Task) { task.Status, task.Integration = planv1.TaskStatus_TASK_STATUS_FAILED, nil })
		azima := in.azima(t, in.wishID, "Letters")
		partOf := func(task *planv1.Task) { task.PartOf = azima.GetId() }
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, partOf)

		in.pass(t, 0)
		tip := in.tip(t)
		if got := in.remoteTip(t, bare); got != tip {
			t.Fatalf("origin's %s is at %s; the branch at %s", in.branch, got, tip)
		}
		pushes := in.pushes(t)
		want := &planv1.IntegrationPush{
			WishId: in.wishID, ProjectId: in.projectID, Branch: in.branch, Remote: "origin", OldSha: old, NewSha: tip, Count: 2,
			Commits: []string{"Work of W1", "Work of W0"}, TaskIds: []string{w0.GetId(), w1.GetId()},
		}
		if len(pushes) != 1 || pushes[0].GetPushTime() == nil {
			t.Fatalf("pushes %v", pushes)
		}
		pushes[0].PushTime = nil
		if !proto.Equal(pushes[0], want) {
			t.Errorf("push %v; want %v", pushes[0], want)
		}
		if last := in.pushState(t).GetLast(); last.GetNewSha() != tip || last.GetCount() != 2 || last.GetPushTime() == nil {
			t.Errorf("the wish's last push %v", last)
		}
		text := "pushed " + in.branch + " to origin as " + tip[:8] + ", 2 commits (the azima " + azima.GetCode() + " ends)"
		for _, task := range []*planv1.Task{w0, w1} {
			if _, texts := in.integration(t, task); !slices.Contains(texts, text) {
				t.Errorf("%s's events %q lack %q", task.GetCode(), texts, text)
			}
		}
	})

	// A watcher part of an azima (the babysit of its pull request) runs until the merge, so it never holds the azima's
	// push: its committed correction pushes the branch; a work part still running holds it.
	t.Run("past an azima watcher", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name   string
			other  planv1.Provider
			pushed bool
		}{
			{"its watcher runs", planv1.Provider_PROVIDER_WATCH, true},
			{"its work runs", planv1.Provider_PROVIDER_FAKE, false},
		} {
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				in := integrating(t)
				bare := in.remote(t)
				old := in.tip(t)
				azima := in.azima(t, in.wishID, "Letters")
				babysit := &planv1.Task{
					Id: store.NewID(), WishId: in.wishID, ProjectId: in.projectID, Code: "W2", Title: "Babysit the PR",
					Kind: planv1.TaskKind_TASK_KIND_WORK, Status: planv1.TaskStatus_TASK_STATUS_RUNNING, Provider: planv1.Provider_PROVIDER_WATCH,
					PartOf: azima.GetId(), Scheduled: true, Babysit: &planv1.TaskBabysit{Url: "https://example.com/pr/1", Number: 1},
				}
				putTask(t, in.db, babysit, "gh pr checks 1 --watch")
				running := &planv1.Task{
					Id: store.NewID(), WishId: in.wishID, ProjectId: in.projectID, Code: "W3", Title: "Something else",
					Kind: planv1.TaskKind_TASK_KIND_WORK, Status: planv1.TaskStatus_TASK_STATUS_RUNNING, Provider: c.other,
					PartOf: azima.GetId(), Scheduled: true,
				}
				putTask(t, in.db, running, "another watcher, or work")
				in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })

				in.pass(t, 0)
				want, pushes := old, 0
				if c.pushed {
					want, pushes = in.tip(t), 1
				}
				if got := in.remoteTip(t, bare); got != want || len(in.pushes(t)) != pushes {
					t.Errorf("origin's %s is at %s, pushes %v; want it at %s", in.branch, got, in.pushes(t), want)
				}
			})
		}
	})

	// Without an azima, the branch is pushed once three tasks are committed since the last push and more than an hour
	// has passed since it, checked as a task's merge ends; not before.
	t.Run("after three tasks and an hour", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		bare := in.remote(t)
		old := in.tip(t)
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
		in.pass(t, 0)
		in.later(2 * time.Hour)
		in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
		in.pass(t, 0)
		if in.remoteTip(t, bare) != old || len(in.pushes(t)) != 0 {
			t.Fatal("two tasks after two hours pushed")
		}
		w3 := in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})
		in.pass(t, 0)
		if got := in.remoteTip(t, bare); got != in.tip(t) {
			t.Fatalf("three tasks after the hour: origin at %s, the branch at %s", got, in.tip(t))
		}
		if p := in.pushes(t); len(p) != 1 || p[0].GetCount() != 3 || len(p[0].GetTaskIds()) != 3 {
			t.Errorf("pushes %v", p)
		}
		_, texts := in.integration(t, w3)
		if want := "3 tasks committed, and more than an hour since the last push)"; !strings.HasSuffix(texts[len(texts)-1], want) {
			t.Errorf("W3's last event %q; want it to end with %q", texts[len(texts)-1], want)
		}
	})

	// In ask mode, a push due asks the person, once, and pushes nothing; "Push it" pushes at the next pass, with the
	// work committed meanwhile.
	t.Run("ask mode", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		bare := in.remote(t)
		old := in.tip(t)
		if _, err := in.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
			WishId: in.wishID, PushMode: planv1.PushMode_PUSH_MODE_ASK,
		})); err != nil {
			t.Fatal(err)
		}
		azima := in.azima(t, in.wishID, "Letters")
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
		in.pass(t, 0)
		if in.remoteTip(t, bare) != old {
			t.Fatal("ask mode pushed by itself")
		}
		q := in.pushQuestion(t)
		if want := "Push " + in.branch + " to origin? (1 commit: Work of W1)"; q.GetText() != want ||
			!slices.Equal(q.GetOptions(), []string{pushOption, notNowOption}) || !strings.HasPrefix(q.GetRecommendation(), "A: ") {
			t.Errorf("the question %q, %q, %q; want %q", q.GetText(), q.GetOptions(), q.GetRecommendation(), want)
		}
		if _, texts := in.integration(t, w1); texts[len(texts)-1] != "a push of "+in.branch+" to origin is due (the azima "+
			azima.GetCode()+" ends); Djinn asks you "+q.GetCode() {
			t.Errorf("W1's events %q", texts)
		}
		// Another merge while it is open asks nothing more.
		in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
		in.pass(t, 2*time.Hour)
		if questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID}); err != nil || len(questions) != 1 {
			t.Fatalf("%d questions, %v", len(questions), err)
		}

		if did := in.answer(t, q, planv1.Choice_CHOICE_A); did != "Djinn pushes at the integration's next pass" {
			t.Errorf("what Djinn did: %q", did)
		}
		in.pass(t, 0)
		if got := in.remoteTip(t, bare); got != in.tip(t) {
			t.Fatalf("pushed it: origin at %s, the branch at %s", got, in.tip(t))
		}
		if s := in.pushState(t); s.GetQuestionId() != "" || s.GetApproved() || s.GetLast().GetCount() != 2 {
			t.Errorf("after the push: %v", s)
		}
	})

	// The remote has a commit the branch does not: the push is refused, never forced; Djinn says why and asks. Pushing
	// again while it is behind is refused again; once the person brings the remote's commit in, it passes.
	t.Run("refused push is not forced", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		bare := in.remote(t)
		// Someone else pushed to origin meanwhile.
		theirs := in.pushFromOther(t, bare, "NEWS", "theirs\n", "Theirs")
		// Djinn cannot fetch from origin, which takes pushes: it merges on main as it last saw it, and git refuses the push.
		in.git(t, in.repo, "remote", "set-url", "--push", "origin", bare)
		in.git(t, in.repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))

		azima := in.azima(t, in.wishID, "Letters")
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
		in.pass(t, 0)
		if got := in.remoteTip(t, bare); got != theirs || len(in.pushes(t)) != 0 {
			t.Fatalf("origin moved to %s from %s", got, theirs)
		}
		s := in.pushState(t)
		q := in.pushQuestion(t)
		if !strings.HasPrefix(s.GetRefused(), "its branch has commits that this one does not ([rejected] (fetch first))") ||
			!strings.HasPrefix(q.GetText(), "origin refused the push of "+in.branch+": its branch has commits that this one does not") ||
			!strings.HasSuffix(q.GetText(), "Djinn never forces a push: what should it do?") ||
			!slices.Equal(q.GetOptions(), []string{strings.Replace(pushAgainOption, "%s", in.branch, 1), leavePushOption}) {
			t.Fatalf("refused %q; the question %q, %q", s.GetRefused(), q.GetText(), q.GetOptions())
		}
		if _, texts := in.integration(t, w1); !strings.HasSuffix(texts[len(texts)-1], "; Djinn asks you "+q.GetCode()) {
			t.Errorf("W1's events %q", texts)
		}

		// Push again, still behind: refused again, asked again.
		in.answer(t, q, planv1.Choice_CHOICE_A)
		in.pass(t, 0)
		again := in.pushQuestion(t)
		if in.remoteTip(t, bare) != theirs || again.GetId() == q.GetId() {
			t.Fatalf("pushed again while behind: origin at %s; question %s", in.remoteTip(t, bare), again.GetCode())
		}

		// The person brings origin's commit in: pushed.
		in.git(t, in.repo, "pull", "--quiet", "--no-rebase", "--no-edit", bare, in.branch)
		in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+in.branch, theirs)
		in.answer(t, again, planv1.Choice_CHOICE_A)
		in.pass(t, 0)
		if got := in.remoteTip(t, bare); got != in.tip(t) {
			t.Fatalf("after the pull: origin at %s, the branch at %s", got, in.tip(t))
		}
		if s := in.pushState(t); s.GetRefused() != "" || s.GetQuestionId() != "" || s.GetLast().GetOldSha() != theirs {
			t.Errorf("after the push: %v", s)
		}
	})

	// Once Djinn pushes in a project whose settings name an install command, djinn up is told what to propose: the
	// commits' titles, and the tasks' summaries from what each worker said last. Installing it runs the command in the
	// project's install worktree at that commit, under the install gate.
	t.Run("build is proposed and installed", func(t *testing.T) {
		t.Parallel()
		var built []Built
		in := integrating(t, WithBuilt(func(b Built) { built = append(built, b) }))
		in.remote(t)
		writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
			"generated: \"gen/**\"\ngenerate: \"gen\"\ntest: \"test\"\ninstall: \"install\"\n")
		azima := in.azima(t, in.wishID, "Letters")
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
		if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
			if err := tx.Journal("test", "said", w1); err != nil {
				return err
			}
			return putEvents(t.Context(), tx, newEvent(w1.GetId(), Event{
				Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, Text: "Done: a.txt holds a.\n\nTo check: the  window\nshows a.",
			}))
		}); err != nil {
			t.Fatal(err)
		}
		in.pass(t, time.Hour)
		in.pass(t, 3*time.Hour) // Nothing left: nothing more to propose.
		tip := in.tip(t)
		want := Built{
			WishID: in.wishID, WishTitle: "Run Djinn on itself", ProjectID: in.projectID, Project: "app", Branch: in.branch, Sha: tip,
			Tasks: []string{"W1"}, Changes: []string{"Work of W1"},
			Summaries: []TaskSummary{{Code: "W1", Title: "Work of W1", Summary: "To check: the window shows a."}},
			Install:   "install",
		}
		if len(built) != 1 || !reflect.DeepEqual(built[0], want) {
			t.Fatalf("built %+v; want %+v", built, want)
		}

		in.git(t, in.repo, "checkout", "--quiet", "--detach", "HEAD~1") // The person's checkout is elsewhere: no matter.
		var steps []string
		progress := func(step InstallStep, waiting string) { steps = append(steps, fmt.Sprint(step, " ", waiting)) }
		// The integration is busy: the install does not wait for it.
		in.h.integrateMu.Lock()
		out, err := in.h.Install(t.Context(), in.wishID, in.projectID, tip, progress)
		in.h.integrateMu.Unlock()
		wt := filepath.Join(installDir(in.home, in.projectID), "app")
		// Its gate is no merge's nor push's: it does not go before the workers' (Q26).
		if err != nil || out != "installed a\n" || in.runs[len(in.runs)-1] != "install in "+wt || in.gates[len(in.gates)-1] != "install -" ||
			!slices.Equal(in.behind, []string{"install -"}) {
			t.Errorf("install: %q, %v; runs %v, gates %v, not first %v", out, err, in.runs, in.gates, in.behind)
		}
		wantSteps := []string{
			fmt.Sprint(InstallPreparing, " "), fmt.Sprint(InstallBuilding, " "),
			fmt.Sprint(InstallWaiting, " gate install: held by W9 (Another install): install, for 3s"), fmt.Sprint(InstallBuilding, " "),
		}
		if !slices.Equal(steps, wantSteps) {
			t.Errorf("install steps %q, want %q", steps, wantSteps)
		}
		// Only a build of the wish's integration branch installs.
		if _, err := in.h.Install(t.Context(), in.wishID, in.projectID, strings.Repeat("0", 40), nil); err == nil {
			t.Error("a commit that is not one installed")
		}
	})

	// A repository without a remote pushes nothing, and says nothing about it.
	t.Run("no remote no push", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		azima := in.azima(t, in.wishID, "Letters")
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
		in.pass(t, 0)
		if _, texts := in.integration(t, w1); len(in.pushes(t)) != 0 || in.pushState(t) != nil || strings.Contains(texts[len(texts)-1], "; push") {
			t.Errorf("pushes %v, state %v, events %q", in.pushes(t), in.pushState(t), texts)
		}
	})

	// In ON_DEMAND mode, the integration branch is never pushed automatically at an azima's end or on the cadence, and
	// no push question is asked.
	t.Run("on demand never pushes on azima or cadence", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		bare := in.remote(t)
		old := in.tip(t)

		if err := plan.SaveDeveloperPush(in.home, in.projectID, planv1.ProjectPush_PROJECT_PUSH_ON_DEMAND); err != nil {
			t.Fatal(err)
		}

		azima := in.azima(t, in.wishID, "Letters")
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
		in.pass(t, 0)

		if got := in.remoteTip(t, bare); got != old || len(in.pushes(t)) != 0 ||
			(in.pushState(t) != nil && in.pushState(t).GetQuestionId() != "") {
			t.Fatalf("on demand pushed at azima end: origin at %s, pushes %v", got, in.pushes(t))
		}

		in.later(2 * time.Hour)
		in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
		in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})
		in.pass(t, 0)

		if got := in.remoteTip(t, bare); got != old || len(in.pushes(t)) != 0 ||
			(in.pushState(t) != nil && in.pushState(t).GetQuestionId() != "") {
			t.Fatalf("on demand pushed on cadence: origin at %s, pushes %v", got, in.pushes(t))
		}
	})
}

// TestPush: Harness.Push pushes the integration branch, runs push checks under gates, refuses to force when remote is
// ahead (asking refusal question), and returns an error when already up to date.
func TestPush(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("pushes and runs push checks", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, lintAtCommit+"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
		bare := in.remote(t)
		old := in.tip(t)

		if err := plan.SaveDeveloperPush(in.home, in.projectID, planv1.ProjectPush_PROJECT_PUSH_ON_DEMAND); err != nil {
			t.Fatal(err)
		}

		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
		in.pass(t, 0)
		if in.remoteTip(t, bare) != old {
			t.Fatal("pushed before Push method called")
		}

		push, err := in.h.Push(t.Context(), in.wishID, in.projectID, "")
		if err != nil {
			t.Fatalf("Push failed: %v", err)
		}
		tip := in.tip(t)
		if got := in.remoteTip(t, bare); got != tip {
			t.Fatalf("Push did not update remote: origin at %s, want %s", got, tip)
		}
		if push == nil || push.GetNewSha() != tip || push.GetCount() != 1 {
			t.Fatalf("unexpected push record: %v", push)
		}
		_, gates := in.ran()
		if !slices.Contains(gates, "test -") {
			t.Errorf("gates %q; want push check test to run", gates)
		}

		// Push when up to date: returns error
		_, err = in.h.Push(t.Context(), in.wishID, in.projectID, "")
		if err == nil || !strings.Contains(err.Error(), "nothing to push") {
			t.Fatalf("expected nothing to push error, got %v", err)
		}
	})

	t.Run("held check and remote ahead refusal", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, lintAtCommit+"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
		bare := in.remote(t)

		if err := plan.SaveDeveloperPush(in.home, in.projectID, planv1.ProjectPush_PROJECT_PUSH_ON_DEMAND); err != nil {
			t.Fatal(err)
		}

		// Push checks held: push check fails, Push returns error and holds push
		in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
		in.pass(t, 0)
		in.testCode, in.testOut = 1, "test failed"
		_, err := in.h.Push(t.Context(), in.wishID, in.projectID, "")
		if err == nil || !strings.Contains(err.Error(), "the push is held") {
			t.Fatalf("expected held push error, got %v", err)
		}
		if in.remoteTip(t, bare) == in.tip(t) {
			t.Fatal("remote updated while checks red")
		}
		in.testCode, in.testOut = 0, ""

		// Remote is ahead: refusal, refuses to force, asks question
		theirs := in.pushFromOther(t, bare, "CONFLICT", "theirs\n", "Remote commit")

		_, err = in.h.Push(t.Context(), in.wishID, in.projectID, "")
		if err == nil || !strings.Contains(err.Error(), "refused the push") {
			t.Fatalf("expected refused push error, got %v", err)
		}
		if got := in.remoteTip(t, bare); got != theirs {
			t.Fatalf("forced push! origin moved to %s from %s", got, theirs)
		}
		q := in.pushQuestion(t)
		if !strings.Contains(q.GetText(), "refused the push") {
			t.Fatalf("expected refusal question, got %q", q.GetText())
		}
	})
}

func TestSyncs(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	// Computes ahead and behind commit counts between integration branch and remote tracking ref.
	t.Run("ahead and behind", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		bare := in.remote(t)

		wish, err := store.Get[*planv1.Wish](t.Context(), in.db, in.wishID)
		if err != nil {
			t.Fatal(err)
		}
		project, err := store.Get[*planv1.Project](t.Context(), in.db, in.projectID)
		if err != nil {
			t.Fatal(err)
		}

		sync := in.syncOf(t, project, in.branch)
		if sync.GetWishId() != wish.GetId() || sync.GetAhead() != 0 || sync.GetBehind() != 0 {
			t.Fatalf("initial sync: ahead=%d behind=%d, want 0/0", sync.GetAhead(), sync.GetBehind())
		}

		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
		in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
		in.pass(t, 0)

		// 2 tasks integrated: 2 task commits + 2 merge commits = 4 commits ahead of origin
		sync = in.syncOf(t, project, in.branch)
		if sync.GetAhead() != 4 || sync.GetBehind() != 0 {
			t.Fatalf("after 2 tasks integrated: ahead=%d behind=%d, want 4/0", sync.GetAhead(), sync.GetBehind())
		}

		in.pushFromOther(t, bare, "REMOTE", "remote\n", "Remote")
		in.git(t, in.repo, "fetch", "origin")

		sync = in.syncOf(t, project, in.branch)
		if sync.GetAhead() != 4 || sync.GetBehind() != 1 {
			t.Fatalf("with remote ahead: ahead=%d behind=%d, want 4/1", sync.GetAhead(), sync.GetBehind())
		}
	})

	// A done azima's branch whose remote copy lags behind, but whose commits main holds already, waits for no push:
	// ahead 0, landed. Its sync forgotten, the project's syncs leave it out, until a new commit on it waits for a push
	// again; a branch with commits to push is never forgotten.
	t.Run("landed in main and forget", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.remote(t)
		ctx := t.Context()
		in.git(t, in.repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+in.branch)

		commit := func(branch, title string) {
			tip := in.git(t, in.repo, "rev-parse", "refs/heads/"+branch)
			tip = in.git(t, in.repo, "commit-tree", tip+"^{tree}", "-p", tip, "-m", title)
			in.git(t, in.repo, "update-ref", "refs/heads/"+branch, tip)
		}
		// The hotfix branch, pushed once, then three more commits that main took by its merge request and pushed.
		in.git(t, in.repo, "branch", "hotfix-tenant", in.branch)
		commit("hotfix-tenant", "first")
		in.git(t, in.repo, "push", "--quiet", "origin", "hotfix-tenant")
		for _, title := range []string{"second", "third", "fourth"} {
			commit("hotfix-tenant", title)
		}
		main := in.git(t, in.repo, "rev-parse", "refs/heads/"+in.branch)
		in.git(t, in.repo, "update-ref", "refs/heads/"+in.branch, in.git(t, in.repo, "rev-parse", "refs/heads/hotfix-tenant"))
		in.git(t, in.repo, "push", "--quiet", "origin", in.branch)
		err := in.db.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal("local", "test", &planv1.Wish{}); err != nil {
				return err
			}
			for code, branch := range map[string]string{"T2": "hotfix-tenant", "T3": "feat/ahead"} {
				err := tx.Put(&planv1.Task{
					Id: store.NewID(), WishId: in.wishID, ProjectId: in.projectID, Code: code, Title: "Tenants apart",
					Kind: planv1.TaskKind_TASK_KIND_AZIMA, Status: planv1.TaskStatus_TASK_STATUS_DONE,
					Azima: &planv1.Azima{Branch: branch, BaseBranch: in.branch, CutFrom: main, Named: true},
				})
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		// A done azima whose branch has a commit main lacks: it waits for a push, and is no landed one.
		in.git(t, in.repo, "branch", "feat/ahead", main)
		commit("feat/ahead", "ahead")
		project, err := store.Get[*planv1.Project](ctx, in.db, in.projectID)
		if err != nil {
			t.Fatal(err)
		}

		if s := in.syncOf(t, project, "hotfix-tenant"); s.GetAhead() != 0 || s.GetBehind() != 0 || !s.GetLanded() {
			t.Errorf("hotfix-tenant, in main: ↑%d ↓%d landed %v; want ↑0 ↓0, landed", s.GetAhead(), s.GetBehind(), s.GetLanded())
		}
		if s := in.syncOf(t, project, "feat/ahead"); s.GetAhead() != 1 || s.GetLanded() {
			t.Errorf("feat/ahead: ↑%d landed %v; want ↑1, not landed", s.GetAhead(), s.GetLanded())
		}
		if _, err := in.h.Push(ctx, in.wishID, in.projectID, "hotfix-tenant"); !errors.Is(err, plan.ErrUpToDate) {
			t.Errorf("Push of hotfix-tenant: %v, want up to date", err)
		}

		if err := in.h.ForgetSync(ctx, project, "feat/ahead"); err == nil || !strings.Contains(err.Error(), "1 commits to push") {
			t.Errorf("ForgetSync of feat/ahead: %v, want refused, it has a commit to push", err)
		}
		if err := in.h.ForgetSync(ctx, project, "hotfix-tenant"); err != nil {
			t.Fatalf("ForgetSync of hotfix-tenant: %v", err)
		}
		branches := func() []string {
			syncs, err := in.h.Syncs(ctx, project)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, s := range syncs {
				got = append(got, s.GetBranch())
			}
			return got
		}
		if got := branches(); slices.Contains(got, "hotfix-tenant") || !slices.Contains(got, "feat/ahead") {
			t.Errorf("syncs once hotfix-tenant forgotten: %v, want feat/ahead and not hotfix-tenant", got)
		}
		commit("hotfix-tenant", "fifth")
		if s := in.syncOf(t, project, "hotfix-tenant"); s.GetAhead() != 1 || s.GetLanded() {
			t.Errorf("hotfix-tenant with a new commit: ↑%d landed %v; want ↑1, not landed", s.GetAhead(), s.GetLanded())
		}
	})

	// A project holds a sync for each branch Djinn pushes there that exists: the integration branch of each active or
	// paused wish, the active first, each followed by its azimas' own branches; not a granted wish's. Its push button
	// pushes that branch alone.
	t.Run("active and paused wishes", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		bare := in.remote(t)
		ctx := t.Context()

		branchAhead := func(branch string, commits int) {
			tip := in.git(t, in.repo, "rev-parse", "refs/heads/"+in.branch)
			for i := range commits {
				tip = in.git(t, in.repo, "commit-tree", tip+"^{tree}", "-p", tip, "-m", fmt.Sprintf("%s %d", branch, i))
			}
			in.git(t, in.repo, "update-ref", "refs/heads/"+branch, tip)
		}
		branchAhead("feat/other", 1)
		branchAhead("djinn/T1-stays-fast", 2)
		branchAhead("feat/old", 3)
		err := in.db.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal("local", "test", &planv1.Wish{}); err != nil {
				return err
			}
			for _, w := range []*planv1.Wish{
				{Id: store.NewID(), Title: "Paused", State: planv1.WishState_WISH_STATE_PAUSED},
				{Id: store.NewID(), Title: "Granted", State: planv1.WishState_WISH_STATE_GRANTED},
			} {
				w.ProjectIds = []string{in.projectID}
				plan.SetIntegrationBranch(w, in.projectID, map[string]string{"Paused": "feat/other", "Granted": "feat/old"}[w.GetTitle()])
				if err := tx.Put(w); err != nil {
					return err
				}
			}
			for code, title := range map[string]string{"T1": "Stays fast", "T2": "Not cut yet"} {
				err := tx.Put(&planv1.Task{
					Id: store.NewID(), WishId: in.wishID, ProjectId: in.projectID, Code: code, Title: title,
					Kind: planv1.TaskKind_TASK_KIND_AZIMA, Status: planv1.TaskStatus_TASK_STATUS_PENDING,
				})
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		project, err := store.Get[*planv1.Project](ctx, in.db, in.projectID)
		if err != nil {
			t.Fatal(err)
		}

		syncs, err := in.h.Syncs(ctx, project)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, s := range syncs {
			got = append(got, fmt.Sprintf("%s %s/%s ↑%d ↓%d %s %s", s.GetBranch(), s.GetRemote(), s.GetTarget(), s.GetAhead(),
				s.GetBehind(), s.GetWishTitle(), s.GetAzimaCode()))
		}
		wish, err := store.Get[*planv1.Wish](ctx, in.db, in.wishID)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{
			fmt.Sprintf("%s origin/%s ↑0 ↓0 %s ", in.branch, in.branch, wish.GetTitle()),
			fmt.Sprintf("djinn/T1-stays-fast origin/djinn/T1-stays-fast ↑2 ↓0 %s T1", wish.GetTitle()),
			"feat/other origin/feat/other ↑1 ↓0 Paused ",
		}
		if !slices.Equal(got, want) {
			t.Fatalf("syncs:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}

		// Its button pushes the azima's branch alone.
		if _, err := in.h.Push(ctx, in.wishID, in.projectID, "djinn/T1-stays-fast"); err != nil {
			t.Fatalf("Push of the azima's branch: %v", err)
		}
		if got := in.git(t, bare, "for-each-ref", "--format=%(refname:short)", "refs/heads"); got != in.branch+"\ndjinn/T1-stays-fast" &&
			got != "djinn/T1-stays-fast\n"+in.branch {
			t.Errorf("origin holds %q, want the integration branch and the azima's only", got)
		}
		if s := in.syncOf(t, project, "djinn/T1-stays-fast"); s.GetAhead() != 0 {
			t.Errorf("the azima's branch pushed: ahead %d, want 0", s.GetAhead())
		}
		// A branch of another wish, or none of Djinn's, is not the wish's to push.
		for _, branch := range []string{"feat/other", "main-of-someone"} {
			if _, err := in.h.Push(ctx, in.wishID, in.projectID, branch); err == nil || !strings.Contains(err.Error(), "no branch Djinn pushes") {
				t.Errorf("Push of %s: %v, want refused", branch, err)
			}
		}
	})
}

// TestPushTarget: one git reads the remote of each branch, its name there, and the remotes; an integration pass reads
// it once per repository, though the remotes change under it.
func TestPushTarget(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if _, err := git(t.Context(), repo, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	setConfig := func(config string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, ".git", "config"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name, config, branch, remote, target string
	}{
		{"no remote", "", "main", "", ""},
		{"the only remote", "[remote \"up\"]\n\turl = /a\n", "main", "up", "main"},
		{"origin among others", "[remote \"up\"]\n\turl = /a\n[remote \"origin\"]\n\turl = /b\n[remote]\n\tpushDefault = up\n",
			"main", "origin", "main"},
		{"two remotes, none origin", "[remote \"up\"]\n\turl = /a\n[remote \"down\"]\n\turl = /b\n", "main", "", ""},
		{"no branch", "[remote \"origin\"]\n\turl = /a\n", "", "", ""},
		{"the upstream", "[remote \"origin\"]\n\turl = /a\n[remote \"up\"]\n\turl = /b\n" +
			"[branch \"feat/x.y\"]\n\tremote = up\n\tmerge = refs/heads/x\n", "feat/x.y", "up", "x"},
		{"a local upstream", "[remote \"origin\"]\n\turl = /a\n[branch \"Dev\"]\n\tremote = .\n\tmerge = refs/heads/main\n",
			"Dev", "origin", "main"},
		{"another branch's upstream", "[remote \"origin\"]\n\turl = /a\n[branch \"other\"]\n\tremote = up\n", "main", "origin", "main"},
	} {
		setConfig(c.config)
		if remote, target := pushTarget(t.Context(), repo, c.branch); remote != c.remote || target != c.target {
			t.Errorf("%s: pushTarget = %q, %q, want %q, %q", c.name, remote, target, c.remote, c.target)
		}
	}

	setConfig("[remote \"origin\"]\n\turl = /a\n")
	ctx := withPassMemo(t.Context())
	if remote, _ := pushTarget(ctx, repo, "main"); remote != "origin" {
		t.Fatalf("remote %q, want origin", remote)
	}
	setConfig("")
	if remote, _ := pushTarget(ctx, repo, "main"); remote != "origin" {
		t.Errorf("remote %q within the pass, want origin as it read it first", remote)
	}
	if remote, _ := pushTarget(t.Context(), repo, "main"); remote != "" {
		t.Errorf("remote %q out of the pass, want none", remote)
	}
}
