package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// corrections are the wish's correction workers.
func (in *integration) corrections(t *testing.T) []*planv1.Task {
	t.Helper()
	return slices.DeleteFunc(in.list(t, in.wishID), func(task *planv1.Task) bool { return task.GetCorrection() == nil })
}

// settleReadme makes the fake agent of the test's correction workers settle W2's conflict in README.md, then wait to
// be let go: the conflict stays under repair until the test says.
func (in *integration) settleReadme(t *testing.T) {
	in.correctWith(func(dir, prompt string) string {
		if !strings.Contains(prompt, "could not integrate the work of W2") {
			return ""
		}
		writeFile(t, dir, "README.md", "# One and Two\n")
		commitAll(t, dir, "Settle the readme") // Which concludes the merge.
		return "wait\ntext settled"
	})
}

// TestIntegrateLane (W126, Q39 A): while a correction worker repairs the branch, the work finished meanwhile
// is not merged: it waits, its reason naming the repair; a new failure on the branch starts no second correction (the
// push checks red stay held for it). The repair's own work goes in first; the queue then merges as one batch, checked
// once.
func TestIntegrateLane(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t, WithTick(20*time.Millisecond))
	in.checking(t, lintAtCommit, map[string]int{"lint": 1})
	in.correctWith(func(_, prompt string) string {
		if strings.Contains(prompt, "the work of W1") {
			return "wait\ntext fixed"
		}
		return ""
	})
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.pass(t, 0)
	got, _ := in.integration(t, w1)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_RED || got.GetCorrectedBy() == "" {
		t.Fatalf("W1's integration %v; want red, corrected", got)
	}
	w2 := in.until(t, got.GetCorrectedBy(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	in.exit("lint", 0)

	// W3 and W4 end while W2 repairs the branch: they wait for it.
	w3 := in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})
	w4 := in.finished(t, "W4", map[string]string{"app/src/d.txt": "d\n"})
	in.ran()
	tip := in.tip(t)
	in.pass(t, time.Second)
	why := "waits for " + w2.GetCode() + ", which repairs " + in.branch + ": it merges once that work is in"
	for _, task := range []*planv1.Task{w3, w4} {
		got, texts := in.integration(t, task)
		if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING || got.GetReason() != why ||
			!slices.Contains(texts, "waiting: "+why) {
			t.Errorf("%s while %s repairs: %v; events %q", task.GetCode(), w2.GetCode(), got, texts)
		}
	}
	if runs, _ := in.ran(); len(runs) != 0 || in.tip(t) != tip {
		t.Errorf("merged during the repair: runs %q, tip %s from %s", runs, in.tip(t), tip)
	}

	// The push checks go red meanwhile, twice: the push stays held, no second correction starts (one at a time).
	wish, err1 := store.Get[*planv1.Wish](t.Context(), in.db, in.wishID)
	project, err2 := store.Get[*planv1.Project](t.Context(), in.db, in.projectID)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	for range 2 {
		in.h.holdPush(t.Context(), wish, project, []*planv1.Task{w3}, in.branch, tip, "test", "test: test exited 1", "", nil)
	}
	if state := in.pushState(t); state.GetHeld() == "" || state.GetCorrectedBy() != "" {
		t.Errorf("the push %v; want held, no correction while %s repairs", state, w2.GetCode())
	}
	if _, texts := in.integration(t, w3); !slices.Contains(texts,
		"the push of "+in.branch+" stays held; its correction waits for "+w2.GetCode()+", which repairs it") {
		t.Errorf("W3's events %q", texts)
	}
	if n := len(in.corrections(t)); n != 1 {
		t.Errorf("%d correction workers; want one at a time", n)
	}

	// W2 ends: its work goes in first, with W1's; then W3 and W4, as one batch, linted once.
	in.release(t, w2.GetId())
	in.ended(t, w2.GetId())
	in.pass(t, time.Second)
	if got := in.states(t, w1, w2, w3, w4); got != "W1 COMMITTED, W2 COMMITTED, W3 COMMITTED, W4 COMMITTED" {
		t.Fatalf("after the repair: %s", got)
	}
	commits := in.commits(t)
	if len(commits) != 2 || !slices.Contains(commits[0].GetTaskIds(), w2.GetId()) ||
		!slices.Equal(commits[1].GetTaskIds(), []string{w3.GetId(), w4.GetId()}) {
		t.Errorf("commits %v; want W2's, then W3 and W4 as one batch", commits)
	}
	if runs, _ := in.ran(); !slices.Equal(runs, []string{"lint", "lint"}) {
		t.Errorf("runs %q; want lint once for W2, once for the batch", runs)
	}
}

// TestStepByStep (W126, Q30 A): a batch whose commit checks are red gets one correction worker for the batch; when that
// correction fails, Djinn merges the batch's work one branch at a time.
func TestStepByStep(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	t.Run("red batch repaired", func(t *testing.T) {
		t.Parallel()
		in := integrating(t, WithTick(20*time.Millisecond))
		in.testCode, in.testOut = 1, "--- FAIL: TestTogether"
		var prompts []string
		in.correctWith(func(_, prompt string) string {
			in.mu.Lock()
			prompts = append(prompts, prompt)
			in.testCode = 0
			in.mu.Unlock()
			return "text fixed together"
		})
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
		w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})

		in.pass(t, 0)
		if got := in.states(t, w1, w2); got != "W1 RED, W2 RED" {
			t.Fatalf("a red batch: %s", got)
		}
		got1, _ := in.integration(t, w1)
		got2, _ := in.integration(t, w2)
		if got1.GetCorrectedBy() == "" || got1.GetCorrectedBy() != got2.GetCorrectedBy() {
			t.Fatalf("W1 %v, W2 %v; want one correction for the batch", got1, got2)
		}
		w3 := in.correction(t, w1)
		if len(prompts) != 1 || !strings.Contains(prompts[0], "the work of W1, W2") || !strings.Contains(prompts[0], "--- FAIL: TestTogether") {
			t.Fatalf("prompts %q; want one naming W1, W2 and the failing output", prompts)
		}
		in.pass(t, time.Second)
		if got := in.states(t, w1, w2, w3); got != "W1 COMMITTED, W2 COMMITTED, W3 COMMITTED" {
			t.Errorf("after the batch's correction: %s", got)
		}
		if n := len(in.corrections(t)); n != 1 {
			t.Errorf("%d correction workers; want one", n)
		}
	})

	t.Run("red batch step by step", func(t *testing.T) {
		t.Parallel()
		in := integrating(t, WithTick(20*time.Millisecond))
		// The tests fail while bad.txt is there and fixed.txt is not.
		in.verdict = func(dir string) (string, int) {
			_, bad := os.Stat(filepath.Join(dir, "src", "bad.txt"))
			_, fixed := os.Stat(filepath.Join(dir, "src", "fixed.txt"))
			if bad == nil && fixed != nil {
				return "--- FAIL: TestBad", 1
			}
			return "", 0
		}
		var prompts []string
		in.correctWith(func(dir, prompt string) string {
			in.mu.Lock()
			prompts = append(prompts, prompt)
			n := len(prompts)
			in.mu.Unlock()
			if n == 1 {
				return "fail could not fix the batch"
			}
			writeFile(t, dir, "src/fixed.txt", "fixed\n")
			commitAll(t, dir, "Fix the tests")
			return "text fixed"
		})
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
		w2 := in.finished(t, "W2", map[string]string{"app/src/bad.txt": "bad\n"})

		in.pass(t, 0)
		got, _ := in.integration(t, w1)
		if in.states(t, w1, w2) != "W1 RED, W2 RED" || got.GetCorrectedBy() == "" {
			t.Fatalf("a red batch: %s, %v", in.states(t, w1, w2), got)
		}
		w3 := in.ended(t, got.GetCorrectedBy())
		if w3.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
			t.Fatalf("%s %v; want failed", w3.GetCode(), w3.GetStatus())
		}

		// The batch's correction failed: W1 alone is green and goes in; W2 alone is red, and gets its own correction.
		in.pass(t, time.Second)
		if got := in.states(t, w1, w2); got != "W1 COMMITTED, W2 RED" {
			t.Fatalf("step by step: %s", got)
		}
		if _, texts := in.integration(t, w2); !slices.ContainsFunc(texts, func(s string) bool {
			return strings.Contains(s, "step by step: Djinn merges W1, W2 one at a time")
		}) {
			t.Errorf("W2's events %q", texts)
		}
		w4 := in.correction(t, w2)
		if len(prompts) != 2 || !strings.Contains(prompts[1], "the work of W2 ") || strings.Contains(prompts[1], "W1") {
			t.Fatalf("prompts %q; want W2's own correction", prompts)
		}
		in.pass(t, time.Second)
		if got := in.states(t, w1, w2, w4); got != "W1 COMMITTED, W2 COMMITTED, "+w4.GetCode()+" COMMITTED" {
			t.Errorf("after W2's own correction: %s", got)
		}
	})
}

// TestLeaveBatch (W126, Q30 A): a branch that conflicts in a batch leaves it, the others merged and
// checked without it; it merges alone after them, a correction of its own starting when it conflicts again, which
// freezes the branch as any repair does.
func TestLeaveBatch(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t, WithTick(20*time.Millisecond))
	in.correctWith(func(_, prompt string) string {
		if strings.Contains(prompt, "the work of W2") {
			return "wait\ntext settled"
		}
		return ""
	})
	w1 := in.finished(t, "W1", map[string]string{"app/README.md": "# One\n"})
	w2 := in.finished(t, "W2", map[string]string{"app/README.md": "# Two\n"})
	w3 := in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})

	in.pass(t, 0)
	if got := in.states(t, w1, w2, w3); got != "W1 COMMITTED, W2 CONFLICT, W3 COMMITTED" {
		t.Fatalf("a conflict in a batch: %s", got)
	}
	if c := in.commits(t); len(c) != 1 || !slices.Equal(c[0].GetTaskIds(), []string{w1.GetId(), w3.GetId()}) {
		t.Errorf("commits %v; want W1 and W3 as one batch", c)
	}
	got, _ := in.integration(t, w2)
	if got.GetCorrectedBy() == "" || got.GetFailure().GetBase() != in.tip(t) {
		t.Fatalf("W2's integration %v; want corrected from the batch's commit %s", got, in.tip(t))
	}
	w9 := in.finished(t, "W9", map[string]string{"app/src/d.txt": "d\n"})
	in.pass(t, time.Second)
	if got, _ := in.integration(t, w9); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING ||
		!strings.HasPrefix(got.GetReason(), "waits for ") {
		t.Errorf("W9 while W2's conflict is corrected: %v", got)
	}
}

// TestPushWaitsForMerges (W126, Q39 A, W281): a push due waits for the work queued to merge into its branch (including
// corrections and uncommitted work under review), and goes once it is merged and checked.
func TestPushWaitsForMerges(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	t.Run("queued work and conflict", func(t *testing.T) {
		t.Parallel()
		in := integrating(t, WithSilenceLimit(2*time.Hour), WithTick(20*time.Millisecond))
		bare := in.remote(t)
		letters := in.azima(t, in.wishID, "Letters")
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = letters.GetId() })
		w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, func(task *planv1.Task) { task.Integration.Alone = true })

		// W1's batch ends Letters: the push is due, and waits for W2, merged alone after it; then it goes, once.
		in.pass(t, 0)
		if got := in.states(t, w1, w2); got != "W1 COMMITTED, W2 COMMITTED" {
			t.Fatalf("states %s", got)
		}
		pushes := in.pushes(t)
		if len(pushes) != 1 || in.remoteTip(t, bare) != in.tip(t) || !slices.Contains(pushes[0].GetTaskIds(), w2.GetId()) {
			t.Fatalf("pushes %v; want one, with W2's work", pushes)
		}
		want := "a push of " + in.branch + " is due (the azima " + letters.GetCode() + " ends); it waits for W2 to merge"
		if _, texts := in.integration(t, w1); !slices.Contains(texts, want) {
			t.Errorf("W1's events %q lack %q", texts, want)
		}

		// W3 ends Numbers while W4, which conflicts with it, is corrected: the push waits for the correction, past 15
		// minutes and an hour; its work in, the push goes with it.
		in.correctWith(func(dir, _ string) string {
			writeFile(t, dir, "src/3.txt", "3 and 4\n")
			commitAll(t, dir, "Settle 3.txt") // Which concludes the merge.
			return "wait\ntext settled"
		})
		numbers := in.azima(t, in.wishID, "Numbers")
		w3 := in.finished(t, "W3", map[string]string{"app/src/3.txt": "3\n"}, func(task *planv1.Task) { task.PartOf = numbers.GetId() })
		w4 := in.finished(t, "W4", map[string]string{"app/src/3.txt": "4\n"})
		wish, err1 := store.Get[*planv1.Wish](t.Context(), in.db, in.wishID)
		project, err2 := store.Get[*planv1.Project](t.Context(), in.db, in.projectID)
		if err1 != nil || err2 != nil {
			t.Fatal(err1, err2)
		}
		if committed, _ := in.h.integrateBatch(t.Context(), wish, project, []*planv1.Task{w3}); !committed {
			t.Fatalf("W3 not committed: %s", in.states(t, w3))
		}
		pushed := in.remoteTip(t, bare)
		in.pass(t, 0)
		got, _ := in.integration(t, w4)
		if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_CONFLICT || got.GetCorrectedBy() == "" {
			t.Fatalf("W4's integration %v; want a conflict, corrected", got)
		}
		fix := in.until(t, got.GetCorrectedBy(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		for _, d := range []time.Duration{16 * time.Minute, time.Hour} {
			in.mu.Lock()
			in.now = in.now.Add(d)
			in.mu.Unlock()
			in.h.pushIfDue(t.Context(), in.wishID, project)
			in.h.waitPushes()
			if got := in.remoteTip(t, bare); got != pushed {
				t.Fatalf("pushed %s later while %s corrects W4", d, fix.GetCode())
			}
		}
		want = "a push of " + in.branch + " is due (the azima " + numbers.GetCode() + " ends); it waits for " + fix.GetCode() + " to merge"
		if _, texts := in.integration(t, w3); !slices.Contains(texts, want) {
			t.Errorf("W3's events %q lack %q", texts, want)
		}
		in.release(t, fix.GetId())
		in.ended(t, fix.GetId())
		in.pass(t, time.Second)
		if got := in.states(t, fix, w4); got != fix.GetCode()+" COMMITTED, W4 COMMITTED" {
			t.Fatalf("after the correction: %s", got)
		}
		if got := in.remoteTip(t, bare); got != in.tip(t) {
			t.Errorf("origin at %s; want the branch's tip %s, with the correction", got, in.tip(t))
		}
	})

	t.Run("review at work", func(t *testing.T) {
		t.Parallel()
		in := integrating(t, WithTick(20*time.Millisecond))
		bare := in.remote(t)
		pushed := in.remoteTip(t, bare)
		in.correctWith(func(dir, _ string) string {
			commitAll(t, dir, "Add b")
			return "wait\ntext Committed: src/b.txt."
		})
		letters := in.azima(t, in.wishID, "Letters")
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = letters.GetId() })
		w3 := in.finished(t, "W3", nil)
		in.leave(t, w3, map[string]string{"app/src/b.txt": "b\n"})

		// W1 ends Letters while W3's work, left not committed, is reviewed: the push waits for the review.
		in.pass(t, 0)
		got, _ := in.integration(t, w3)
		if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED || got.GetReviewedBy() == "" {
			t.Fatalf("W3's integration %v; want reviewed", got)
		}
		review := in.until(t, got.GetReviewedBy(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		in.pass(t, time.Minute)
		if got := in.states(t, w1); got != "W1 COMMITTED" || in.remoteTip(t, bare) != pushed || len(in.pushes(t)) != 0 {
			t.Fatalf("%s; pushes %v while %s reviews W3", got, in.pushes(t), review.GetCode())
		}
		want := "a push of " + in.branch + " is due (the azima " + letters.GetCode() + " ends); it waits for " + review.GetCode() + " to merge"
		if _, texts := in.integration(t, w1); !slices.Contains(texts, want) {
			t.Errorf("W1's events %q lack %q", texts, want)
		}

		// The review's work merges, W3's with it: the push goes, once, with both.
		in.release(t, review.GetId())
		in.ended(t, review.GetId())
		in.pass(t, time.Second)
		if got := in.states(t, review, w3); got != review.GetCode()+" COMMITTED, W3 COMMITTED" {
			t.Fatalf("after the review: %s", got)
		}
		pushes := in.pushes(t)
		if len(pushes) != 1 || in.remoteTip(t, bare) != in.tip(t) || !slices.Contains(pushes[0].GetTaskIds(), w3.GetId()) {
			t.Errorf("pushes %v; want one, with W3's work", pushes)
		}
	})
}

// TestRepairOf (W126 7, W133, W233, Q44 A): repairOf reports an active repair or red push check on the wish, holding
// new work while letting urgent work and the repair itself run (preempting running work on a full machine).
func TestRepairOf(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	t.Run("starts on a red branch", func(t *testing.T) {
		t.Parallel()
		in := integrating(t, WithTick(20*time.Millisecond))
		in.checking(t, lintAtCommit+"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
		in.testCode = 1
		bare := in.remote(t)
		azima := in.azima(t, in.wishID, "Feature")
		in.correctWith(func(_, prompt string) string {
			if strings.HasPrefix(prompt, "The push checks of ") {
				return "wait\ntext push checks fixed"
			}
			return ""
		})
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
		in.pass(t, 0)
		state := in.pushState(t)
		if state.GetHeld() == "" || state.GetCorrectedBy() == "" {
			t.Fatalf("the push %v; want held, corrected", state)
		}
		planned := in.mustSpawn(t, in.wishID, "Planned", "text planned", nil)
		if got := in.get(t, planned.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING ||
			!strings.Contains(got.GetWaitReason(), "to be green again") {
			t.Fatalf("planned work on the red branch: %v", got)
		}
		fix := in.until(t, state.GetCorrectedBy(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

		w9 := in.finished(t, "W9", map[string]string{"app/src/b.txt": "b\n"})
		in.pass(t, time.Second)
		if got, _ := in.integration(t, w9); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING ||
			!strings.HasPrefix(got.GetReason(), "waits for "+fix.GetCode()) {
			t.Errorf("W9 while %s repairs: %v", fix.GetCode(), got)
		}
		in.mu.Lock()
		in.testCode = 0
		in.mu.Unlock()
		in.release(t, fix.GetId())
		in.ended(t, fix.GetId())
		in.pass(t, time.Second)
		if got := in.states(t, fix, w9); got != fix.GetCode()+" COMMITTED, W9 COMMITTED" {
			t.Errorf("after the repair: %s", got)
		}
		if s := in.pushState(t); s.GetHeld() != "" || in.remoteTip(t, bare) == "" {
			t.Errorf("the push %v", s)
		}
	})

	t.Run("conflict holds new work", func(t *testing.T) {
		t.Parallel()
		in := integrating(t, WithTick(20*time.Millisecond))
		in.settleReadme(t)
		in.finished(t, "W1", map[string]string{"app/README.md": "# One\n"})
		w2 := in.finished(t, "W2", map[string]string{"app/README.md": "# Two\n"})
		in.pass(t, 0) // W1 goes in; W2 conflicts with it, and its correction starts.
		got, _ := in.integration(t, w2)
		if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_CONFLICT || got.GetCorrectedBy() == "" {
			t.Fatalf("W2's integration %v; want a conflict, corrected", got)
		}
		fix := in.until(t, got.GetCorrectedBy(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

		planned := in.mustSpawn(t, in.wishID, "Planned", "text planned", nil)
		want := "waits for " + fix.GetCode() + " to settle the conflict in " + in.branch
		if got := in.get(t, planned.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || got.GetWaitReason() != want {
			t.Fatalf("new work while %s settles the conflict: %v; want it pending, %q", fix.GetCode(), got, want)
		}
		urgent := in.mustSpawn(t, in.wishID, "Urgent", "text urgent", &planv1.TaskServiceSpawnRequest{Urgent: true})
		if got := in.ended(t, urgent.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Errorf("the urgent task while %s settles the conflict: %v; want it run", fix.GetCode(), got)
		}
		if got := in.get(t, planned.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || got.GetWaitReason() != want {
			t.Errorf("new work after the urgent task: %v; want it still held", got)
		}

		// The correction ends done: its work waits to be merged, the branch still under repair.
		in.release(t, fix.GetId())
		in.ended(t, fix.GetId())
		if got := in.get(t, planned.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || got.GetWaitReason() != want {
			t.Errorf("new work before the repair is in: %v; want it still held", got)
		}
		// Its work in, the conflict is settled: the work held starts.
		in.pass(t, time.Second)
		if got := in.states(t, w2, fix); got != "W2 COMMITTED, "+fix.GetCode()+" COMMITTED" {
			t.Fatalf("after the repair: %s", got)
		}
		if got := in.ended(t, planned.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Errorf("the work held, once the conflict is settled: %v", got)
		}
	})

	t.Run("merge takes the slot of work", func(t *testing.T) {
		t.Parallel()
		lim := &limit{slots: 1}
		in := integrating(t, WithCapacity(lim.capacity), WithTick(20*time.Millisecond))
		in.settleReadme(t)
		in.finished(t, "W1", map[string]string{"app/README.md": "# One\n"})
		w2 := in.finished(t, "W2", map[string]string{"app/README.md": "# Two\n"})
		work := in.mustSpawn(t, in.wishID, "Work", "wait", nil) // It takes the one slot.
		in.until(t, work.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

		in.pass(t, 0) // W1 goes in; W2 conflicts with it, and its correction starts on the full machine.
		got, _ := in.integration(t, w2)
		if got.GetCorrectedBy() == "" {
			t.Fatalf("W2's integration %v; want it corrected", got)
		}
		fix := in.until(t, got.GetCorrectedBy(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		in.until(t, work.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_PAUSED))
		if !slices.ContainsFunc(in.storedEvents(t, fix.GetId()), func(ev *planv1.TaskEvent) bool {
			return ev.GetText() == "preempted "+work.GetCode()+" to make room"
		}) {
			t.Errorf("%s's events lack the preemption of %s", fix.GetCode(), work.GetCode())
		}

		in.release(t, fix.GetId())
		in.ended(t, fix.GetId())
		in.until(t, work.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		in.release(t, work.GetId())
		in.ended(t, work.GetId())
	})
}
