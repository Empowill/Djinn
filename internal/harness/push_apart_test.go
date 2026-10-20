package harness

import (
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// pushChecking is a check of the push before it, "test" at the push, a commit check "lint" before each merge.
const pushChecking = lintAtCommit + "checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n"

// runsOf are the commands named name run so far, each "name in <dir>", the runs kept.
func (in *integration) runsOf(name string) []string {
	in.mu.Lock()
	defer in.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(in.runs), func(r string) bool { return !strings.HasPrefix(r, name+" in ") })
}

// holdTest makes the next test commands wait until the function it returns is called, once or more.
func (in *integration) holdTest() (release func()) {
	wait := make(chan struct{})
	in.mu.Lock()
	in.testWait = wait
	in.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			in.mu.Lock()
			in.testWait = nil
			in.mu.Unlock()
			close(wait)
		})
	}
}

func TestPushApart(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	// W180, Q40 A; W281: a push's checks run apart from the integration's pass, in the push worktree, on the commit
	// the branch was at: work that ends meanwhile merges at once. Queued for the branch, it stops them: nothing is
	// pushed, nor held. Once no merge is queued, the push checks the new tip and pushes it.
	t.Run("merges go on during push checks", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, pushChecking, map[string]int{"lint": 0})
		bare := in.remote(t)
		old := in.tip(t)
		letters := in.azima(t, in.wishID, "Letters")
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = letters.GetId() })

		// W1 ends Letters: the push is due, its check runs and waits.
		release := in.holdTest()
		defer release()
		in.passApart(t, 0)
		checked := in.tip(t)
		for deadline := time.Now().Add(30 * time.Second); len(in.runsOf("test")) == 0; time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the push check never ran")
			}
		}
		if got := in.runsOf("test"); !slices.Equal(got, []string{"test in " + filepath.Join(pushDir(in.home, in.projectID, in.wishID), "app")}) {
			t.Errorf("push checks %q; want one, in the push worktree", got)
		}
		if run := in.pushState(t).GetRun(); run.GetStage() != planv1.PushStage_PUSH_STAGE_CHECKING {
			t.Errorf("push run %v; want checking", run)
		}

		// W2 ends while the check runs: it merges, the pass does not wait for the check, which stops.
		w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
		merged := make(chan struct{})
		go func() {
			defer close(merged)
			in.passApart(t, time.Minute)
		}()
		select {
		case <-merged:
		case <-time.After(time.Minute):
			release()
			t.Fatal("the merge waited for the push checks")
		}
		if got := in.states(t, w1, w2); got != "W1 COMMITTED, W2 COMMITTED" {
			t.Fatalf("states %s", got)
		}
		tip := in.tip(t)
		stop := "the push checks of " + in.branch + " at " + short8(checked) + " stop: W2 queued to merge into it; nothing " +
			"pushed nor held, Djinn checks it again once the queue is empty"
		for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			if _, texts := in.integration(t, w1); slices.Contains(texts, stop) {
				break
			} else if time.Now().After(deadline) {
				t.Fatalf("W1's events %q lack %q", texts, stop)
			}
		}

		// No merge queued: the push checks the tip, and pushes it with both.
		release()
		in.h.waitPushes()
		in.pass(t, time.Minute)
		pushes := in.pushes(t)
		if len(pushes) != 1 || pushes[0].GetNewSha() != tip || pushes[0].GetOldSha() != old || in.remoteTip(t, bare) != tip ||
			!slices.Contains(pushes[0].GetTaskIds(), w2.GetId()) {
			t.Fatalf("pushes %v; want one of %s, from %s, with W2's work", pushes, tip, old)
		}
		if got := len(in.runsOf("test")); got != 2 {
			t.Errorf("push checks run %d times; want 2: the one stopped, the one pushed", got)
		}
	})

	// W281, Q39/Q40: work queued for a branch during its push checks, a conflict being merged and corrected, stops
	// them at once: the test command's context is cancelled, nothing is pushed, held nor recorded red. The push waits
	// for the correction, then checks the tip that holds it, and pushes it.
	t.Run("queued work stops push checks", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, pushChecking, map[string]int{"lint": 0})
		bare := in.remote(t)
		old := in.tip(t)
		in.correctWith(func(dir, _ string) string {
			writeFile(t, dir, "src/a.txt", "a and b\n")
			commitAll(t, dir, "Settle a.txt") // Which concludes the merge.
			return "wait\ntext settled"
		})
		letters := in.azima(t, in.wishID, "Letters")
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = letters.GetId() })
		w2 := in.finished(t, "W2", map[string]string{"app/src/a.txt": "b\n"}, func(task *planv1.Task) { task.Integration = nil })

		// W1 ends Letters: the push is due, its check runs and waits.
		release := in.holdTest()
		defer release()
		in.passApart(t, 0)
		checked := in.tip(t)
		for deadline := time.Now().Add(30 * time.Second); len(in.runsOf("test")) == 0; time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the push check never ran")
			}
		}

		// W2's work, conflicting with W1's, waits to be merged: the push check stops at once, never released.
		w2.Integration = &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_PENDING}
		putTaskInDB(t, in.db, w2)
		in.passApart(t, time.Second)
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			in.h.waitPushes()
		}()
		select {
		case <-stopped:
		case <-time.After(30 * time.Second):
			t.Fatal("the push checks went on while W2 was queued")
		}
		in.mu.Lock()
		cut := in.testCut
		in.mu.Unlock()
		if cut != 1 || in.remoteTip(t, bare) != old || len(in.pushes(t)) != 0 {
			t.Fatalf("test commands cut %d, origin at %s, pushes %v; want the one cut, nothing pushed", cut, in.remoteTip(t, bare), in.pushes(t))
		}
		if state, hold := in.pushState(t), in.hold(t); hold.GetHeld() != "" || state.GetRun() != nil {
			t.Errorf("push state %v; want not held, no run shown", state)
		}
		if got := in.checkRuns(t); slices.ContainsFunc(got, func(r string) bool { return strings.HasPrefix(r, "test ") }) {
			t.Errorf("check runs %q; want no test recorded", got)
		}
		_, texts := in.integration(t, w1)
		if !slices.ContainsFunc(texts, func(s string) bool {
			return strings.HasPrefix(s, "the push checks of "+in.branch+" at "+short8(checked)+" stop: ") && strings.HasSuffix(s,
				" queued to merge into it; nothing pushed nor held, Djinn checks it again once the queue is empty")
		}) {
			t.Errorf("W1's events %q lack the push checks stopped", texts)
		}
		got, _ := in.integration(t, w2)
		if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_CONFLICT || got.GetCorrectedBy() == "" {
			t.Fatalf("W2's integration %v; want a conflict, corrected", got)
		}

		// While the correction runs, no push check; its work in, the tip is checked and pushed.
		release()
		fix := in.until(t, got.GetCorrectedBy(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		in.pass(t, time.Minute)
		if n := len(in.runsOf("test")); n != 1 {
			t.Errorf("push checks run %d times while %s corrects W2; want the one stopped", n, fix.GetCode())
		}
		in.release(t, fix.GetId())
		in.ended(t, fix.GetId())
		in.pass(t, time.Second)
		if got := in.states(t, fix, w2); got != fix.GetCode()+" COMMITTED, W2 COMMITTED" {
			t.Fatalf("after the correction: %s", got)
		}
		if tip := in.tip(t); in.remoteTip(t, bare) != tip || len(in.pushes(t)) != 1 {
			t.Errorf("origin at %s, pushes %v; want one, of %s", in.remoteTip(t, bare), in.pushes(t), tip)
		}
	})

	// W180, Q40 A: a push due checks nothing while merges are queued for its branch, and runs once after the last of
	// them, on the tip that holds them all.
	t.Run("after the last merge", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, pushChecking, map[string]int{"lint": 0})
		bare := in.remote(t)
		old := in.tip(t)
		letters := in.azima(t, in.wishID, "Letters")
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = letters.GetId() })
		w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
		w3 := in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})
		wish, err1 := store.Get[*planv1.Wish](t.Context(), in.db, in.wishID)
		project, err2 := store.Get[*planv1.Project](t.Context(), in.db, in.projectID)
		if err1 != nil || err2 != nil {
			t.Fatal(err1, err2)
		}

		// W1 ends Letters, W2 and W3 are queued: no push check, now nor ten minutes later.
		if committed, _ := in.h.integrateBatch(t.Context(), wish, project, []*planv1.Task{w1}); !committed {
			t.Fatalf("W1 not committed: %s", in.states(t, w1))
		}
		for _, d := range []time.Duration{0, 10 * time.Minute} {
			in.mu.Lock()
			in.now = in.now.Add(d)
			in.mu.Unlock()
			in.h.pushPass(t.Context())
			if in.h.pushApartRuns(in.wishID, in.projectID, in.branch) || len(in.runsOf("test")) != 0 {
				t.Fatalf("a push check after %s, while W2 and W3 are queued: %q", d, in.runsOf("test"))
			}
		}
		want := "a push of " + in.branch + " is due (the azima " + letters.GetCode() + " ends); it waits for W2, W3 to merge"
		if _, texts := in.integration(t, w1); !slices.Contains(texts, want) {
			t.Errorf("W1's events %q lack %q", texts, want)
		}

		// W2 and W3 merge as one batch: the push follows, checked once on the tip that holds them.
		in.pass(t, time.Minute)
		if got := in.states(t, w2, w3); got != "W2 COMMITTED, W3 COMMITTED" {
			t.Fatalf("states %s", got)
		}
		tip := in.tip(t)
		pushes := in.pushes(t)
		if len(pushes) != 1 || pushes[0].GetNewSha() != tip || pushes[0].GetOldSha() != old || in.remoteTip(t, bare) != tip ||
			!slices.Contains(pushes[0].GetTaskIds(), w3.GetId()) {
			t.Fatalf("pushes %v; want one of %s with W3's work", pushes, tip)
		}
		if got := len(in.runsOf("test")); got != 1 {
			t.Errorf("push checks run %d times; want once", got)
		}
		if got := in.checkRuns(t); !slices.Contains(got, "test ok at "+short8(tip)) {
			t.Errorf("check runs %q; want test ok at the tip %s", got, short8(tip))
		}
	})

	// W180: a push the person asks for, made moot by a merge during its push checks, checks the new tip at once, and
	// pushes it.
	t.Run("person push checks new tip", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, pushChecking, map[string]int{"lint": 0})
		bare := in.remote(t)
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
		in.pass(t, 0)

		release := in.holdTest()
		defer release()
		type pushed struct {
			record *planv1.IntegrationPush
			err    error
		}
		done := make(chan pushed, 1)
		go func() {
			record, err := in.h.Push(t.Context(), in.wishID, in.projectID, "")
			done <- pushed{record, err}
		}()
		for deadline := time.Now().Add(30 * time.Second); len(in.runsOf("test")) == 0; time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the push check never ran")
			}
		}
		w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
		in.passApart(t, time.Minute)
		if got := in.states(t, w2); got != "W2 COMMITTED" {
			t.Fatalf("states %s", got)
		}
		release()
		got := <-done
		if tip := in.tip(t); got.err != nil || got.record.GetNewSha() != tip || in.remoteTip(t, bare) != tip {
			t.Fatalf("push %v, %v; want %s pushed", got.record, got.err, tip)
		}
		if n := len(in.runsOf("test")); n != 2 {
			t.Errorf("push checks run %d times; want 2: the moot one, the one on the new tip", n)
		}
	})
}
