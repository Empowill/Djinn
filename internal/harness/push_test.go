package harness

import (
	"fmt"
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

// pushQuestion is the open question Djinn asked about the push.
func (in *integration) pushQuestion(t *testing.T) *planv1.Question {
	t.Helper()
	q, err := store.Get[*planv1.Question](t.Context(), in.db, in.pushState(t).GetQuestionId())
	if err != nil {
		t.Fatalf("no question about the push: %v; %v", in.pushState(t), err)
	}
	return q
}

// later moves the clock by d, without a pass.
func (in *integration) later(d time.Duration) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.now = in.now.Add(d)
}

// TestPushAtAnAzimasEnd: the integration branch is pushed once an azima's last part is committed, not before, with
// the person's own git, and the push recorded: in the journal, on the wish, in each task's events. An azima with a
// failed part has not ended, as its state says (plan.FillAzimas): its other part committed waits for the next push.
func TestPushAtAnAzimasEnd(t *testing.T) {
	testx.Portable(t)
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
	// W0's merge does not end Numbers, W9 failed: no push then (TestPushDue holds a part still to commit); W1's ends
	// Letters.
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
}

// TestPushAfterThreeTasksAndAnHour: without an azima, the branch is pushed once three tasks are committed since the
// last push and more than an hour has passed since it, checked as a task's merge ends; not before. TestPushDue holds
// three tasks within the hour.
func TestPushAfterThreeTasksAndAnHour(t *testing.T) {
	testx.Portable(t)
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
}

// TestPushAskMode: in ask mode, a push due asks the person, once, and pushes nothing; "Push it" pushes at the next
// pass, with the work committed meanwhile.
func TestPushAskMode(t *testing.T) {
	testx.Portable(t)
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
}

// TestARefusedPushIsNotForced: the remote has a commit the branch does not: the push is refused, never forced; Djinn
// says why and asks. Pushing again while it is behind is refused again; once the person brings the remote's commit
// in, it passes.
func TestARefusedPushIsNotForced(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	bare := in.remote(t)
	// Someone else pushed to origin meanwhile.
	other := filepath.Join(t.TempDir(), "other")
	in.git(t, in.repo, "clone", "--quiet", "--branch", in.branch, bare, other)
	writeFile(t, other, "NEWS", "theirs\n")
	in.git(t, other, "add", "NEWS")
	in.git(t, other, "-c", "user.name=Other", "-c", "user.email=other@example.com", "commit", "--quiet", "-m", "Theirs")
	in.git(t, other, "push", "--quiet", "origin", in.branch)
	theirs := in.remoteTip(t, bare)

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
	in.git(t, in.repo, "pull", "--quiet", "--no-rebase", "--no-edit", "origin", in.branch)
	in.answer(t, again, planv1.Choice_CHOICE_A)
	in.pass(t, 0)
	if got := in.remoteTip(t, bare); got != in.tip(t) {
		t.Fatalf("after the pull: origin at %s, the branch at %s", got, in.tip(t))
	}
	if s := in.pushState(t); s.GetRefused() != "" || s.GetQuestionId() != "" || s.GetLast().GetOldSha() != theirs {
		t.Errorf("after the push: %v", s)
	}
}

// TestABuildIsProposed: once Djinn pushes in a project whose settings name an install command, djinn up is told what
// to propose: the commits' titles, and the tasks' summaries from what each worker said last. Installing it runs the command
// in the project's install worktree at that commit, under the install gate, never in the person's checkout nor in an
// integration worktree, and says each step and what it waits for.
func TestABuildIsProposed(t *testing.T) {
	testx.Portable(t)
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
		return tx.Put(newEvent(w1.GetId(), 2, Event{
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
	if err != nil || out != "installed a\n" || in.runs[len(in.runs)-1] != "install in "+wt || in.gates[len(in.gates)-1] != "install -" {
		t.Errorf("install: %q, %v; runs %v, gates %v", out, err, in.runs, in.gates)
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
}

// TestNoRemoteNoPush: a repository without a remote pushes nothing, and says nothing about it.
func TestNoRemoteNoPush(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	azima := in.azima(t, in.wishID, "Letters")
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
	in.pass(t, 0)
	if _, texts := in.integration(t, w1); len(in.pushes(t)) != 0 || in.pushState(t) != nil || strings.Contains(texts[len(texts)-1], "push") {
		t.Errorf("pushes %v, state %v, events %q", in.pushes(t), in.pushState(t), texts)
	}
}

func TestPushDue(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	task := func(code string, status planv1.TaskStatus, state planv1.IntegrationState, partOf string) *planv1.Task {
		return &planv1.Task{
			Id: code, Code: code, Status: status, Kind: planv1.TaskKind_TASK_KIND_WORK, PartOf: partOf,
			Integration: &planv1.TaskIntegration{State: state},
		}
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
}

func TestCommittedSince(t *testing.T) {
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

// TestPushOnDemandNeverPushesOnAzimaOrCadence: in ON_DEMAND mode, the integration branch is never pushed
// automatically at an azima's end or on the cadence, and no push question is asked.
func TestPushOnDemandNeverPushesOnAzimaOrCadence(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	bare := in.remote(t)
	old := in.tip(t)

	if err := plan.SaveDeveloperPush(in.home, in.projectID, planv1.ProjectPush_PROJECT_PUSH_ON_DEMAND); err != nil {
		t.Fatal(err)
	}

	azima := in.azima(t, in.wishID, "Letters")
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
	in.pass(t, 0)

	if got := in.remoteTip(t, bare); got != old {
		t.Fatalf("on demand pushed at azima end: origin at %s, was %s", got, old)
	}
	if len(in.pushes(t)) != 0 {
		t.Fatalf("expected 0 pushes, got %v", in.pushes(t))
	}
	if in.pushState(t) != nil && in.pushState(t).GetQuestionId() != "" {
		t.Fatalf("on demand asked a push question: %v", in.pushQuestion(t))
	}

	in.later(2 * time.Hour)
	in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
	in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})
	in.pass(t, 0)

	if got := in.remoteTip(t, bare); got != old {
		t.Fatalf("on demand pushed on cadence: origin at %s, was %s", got, old)
	}
	if len(in.pushes(t)) != 0 {
		t.Fatalf("expected 0 pushes, got %v", in.pushes(t))
	}
	if in.pushState(t) != nil && in.pushState(t).GetQuestionId() != "" {
		t.Fatalf("on demand asked a push question: %v", in.pushQuestion(t))
	}
}

// TestPushMethod: Harness.Push pushes the integration branch, runs push checks under gates, refuses to force
// when remote is ahead (asking refusal question), and returns an error when already up to date.
func TestPushMethod(t *testing.T) {
	testx.Portable(t)
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

	push, err := in.h.Push(t.Context(), in.wishID, in.projectID)
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
	_, err = in.h.Push(t.Context(), in.wishID, in.projectID)
	if err == nil || !strings.Contains(err.Error(), "nothing to push") {
		t.Fatalf("expected nothing to push error, got %v", err)
	}

	// Push checks held: push check fails, Push returns error and holds push
	in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
	in.pass(t, 0)
	in.testCode, in.testOut = 1, "test failed"
	_, err = in.h.Push(t.Context(), in.wishID, in.projectID)
	if err == nil || !strings.Contains(err.Error(), "the push is held") {
		t.Fatalf("expected held push error, got %v", err)
	}
	if in.remoteTip(t, bare) == in.tip(t) {
		t.Fatal("remote updated while checks red")
	}
	in.testCode, in.testOut = 0, ""

	// Remote is ahead: refusal, refuses to force, asks question
	other := filepath.Join(t.TempDir(), "other")
	in.git(t, in.repo, "clone", "--quiet", "--branch", in.branch, bare, other)
	writeFile(t, other, "CONFLICT", "theirs\n")
	in.git(t, other, "add", "CONFLICT")
	in.git(t, other, "-c", "user.name=Other", "-c", "user.email=other@example.com", "commit", "--quiet", "-m", "Remote commit")
	in.git(t, other, "push", "--quiet", "origin", in.branch)
	theirs := in.remoteTip(t, bare)

	_, err = in.h.Push(t.Context(), in.wishID, in.projectID)
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
}

// TestPushSync: computes ahead and behind commit counts between integration branch and remote tracking ref.
func TestPushSync(t *testing.T) {
	testx.Portable(t)
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

	sync, err := in.h.Sync(t.Context(), wish, project)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if sync.GetAhead() != 0 || sync.GetBehind() != 0 {
		t.Fatalf("initial sync: ahead=%d behind=%d, want 0/0", sync.GetAhead(), sync.GetBehind())
	}

	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
	in.pass(t, 0)

	// 2 tasks integrated: 2 task commits + 2 merge commits = 4 commits ahead of origin
	sync, err = in.h.Sync(t.Context(), wish, project)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if sync.GetAhead() != 4 || sync.GetBehind() != 0 {
		t.Fatalf("after 2 tasks integrated: ahead=%d behind=%d, want 4/0", sync.GetAhead(), sync.GetBehind())
	}

	other := filepath.Join(t.TempDir(), "other")
	in.git(t, in.repo, "clone", "--quiet", "--branch", in.branch, bare, other)
	writeFile(t, other, "REMOTE", "remote\n")
	in.git(t, other, "add", "REMOTE")
	in.git(t, other, "-c", "user.name=Other", "-c", "user.email=other@example.com", "commit", "--quiet", "-m", "Remote")
	in.git(t, other, "push", "--quiet", "origin", in.branch)

	in.git(t, in.repo, "fetch", "origin")

	sync, err = in.h.Sync(t.Context(), wish, project)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if sync.GetAhead() != 4 || sync.GetBehind() != 1 {
		t.Fatalf("with remote ahead: ahead=%d behind=%d, want 4/1", sync.GetAhead(), sync.GetBehind())
	}
}
