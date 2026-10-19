package harness

import (
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// redAgain is the push checks of the wish's integration branch red once more, as a push due finds them.
func (in *integration) redAgain(t *testing.T, sha string) {
	t.Helper()
	wish, err1 := store.Get[*planv1.Wish](t.Context(), in.db, in.wishID)
	project, err2 := store.Get[*planv1.Project](t.Context(), in.db, in.projectID)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	in.h.holdPush(t.Context(), wish, project, nil, in.branch, sha, "test", "test: test exited 1", ":\n--- FAIL: TestE2E", nil)
}

// TestSpawn_PushRepair (W272): a repair the lead spawns while the push checks are red, its title naming them.
func TestSpawn_PushRepair(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	// As on 17/10/2026 (the lead's W248 repaired the red push checks of feat/qol, Djinn did not see it and started W267
	// beside it): the repair becomes their correction: it starts while the push is held, and red again, the push starts
	// no correction of Djinn's. Another repair of the same red is refused, naming it. Its work in, the push checks run
	// again and, green, the push goes.
	t.Run("adopted", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, "correction_attempts: 2\n"+lintAtCommit+"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n",
			map[string]int{"lint": 0})
		bare := in.remote(t)
		busy := in.mustSpawn(t, in.wishID, "Busy", "wait\ntext busy", nil)
		in.until(t, busy.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

		// Red once while work runs: Djinn waits for the next work before it corrects.
		tip := in.tip(t)
		in.redAgain(t, tip)
		if state := in.pushState(t); state.GetHeld() == "" || state.GetCorrectedBy() != "" || len(in.corrections(t)) != 0 {
			t.Fatalf("the push %v, corrections %d; want held, none yet", state, len(in.corrections(t)))
		}

		res, err := in.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: in.wishID, Provider: planv1.Provider_PROVIDER_FAKE, Prompt: "wait\ntext fixed",
			Title: "Repair " + in.branch + ": push checks red at " + short8(tip) + " (test exited 1)",
		}))
		if err != nil {
			t.Fatal(err)
		}
		repair := res.Msg.GetTask()
		if c := repair.GetCorrection(); !c.GetAdopted() || c.GetAttempt() != 1 || c.GetFailure().GetBase() != tip {
			t.Errorf("the lead's repair: correction %v; want adopted, attempt 1, on the red commit", c)
		}
		wantNote := "Djinn takes " + repair.GetCode() + " as the correction of the red push checks of " + in.branch
		if !strings.HasPrefix(res.Msg.GetNote(), wantNote) {
			t.Errorf("spawn note %q; want it to start %q", res.Msg.GetNote(), wantNote)
		}
		repair = in.until(t, repair.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		waitFor(t, "the push to name the repair", func() bool { return in.pushState(t).GetCorrectedBy() == repair.GetId() })
		if !hasText(in.events(t, repair), wantNote) {
			t.Error("no event of the repair says Djinn took it")
		}

		// Red again: Djinn starts no correction of its own beside the lead's.
		in.redAgain(t, tip)
		if cs := in.corrections(t); len(cs) != 1 || cs[0].GetId() != repair.GetId() || in.pushState(t).GetCorrectedBy() != repair.GetId() {
			t.Fatalf("corrections %v, the push %v; want the lead's repair alone", codes(cs), in.pushState(t))
		}

		// One repair per red: another is refused, naming the one that runs, flagged or named so.
		for _, req := range []*planv1.TaskServiceSpawnRequest{
			{Title: "Make the e2e pass", Repair: true},
			{Title: "Fix the e2e", Prompt: in.branch + "'s push checks are red at " + short8(tip) + ": find the failing test."},
		} {
			req.WishId, req.Provider = in.wishID, planv1.Provider_PROVIDER_FAKE
			_, err := in.tasks.Spawn(t.Context(), connect.NewRequest(req))
			if connect.CodeOf(err) != connect.CodeFailedPrecondition ||
				!strings.Contains(err.Error(), repair.GetCode()+" already repairs the push checks of "+in.branch) {
				t.Errorf("a second repair %q: %v; want it refused, naming %s", req.GetTitle(), err, repair.GetCode())
			}
		}
		// Plain work stays plain, waiting for the branch to be green.
		plain := in.mustSpawn(t, in.wishID, "Add a button", "text button; never run the push checks, Djinn runs them", nil)
		if plain.GetCorrection() != nil || plain.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
			t.Errorf("plain work %v, correction %v; want it waiting, no repair", plain.GetStatus(), plain.GetCorrection())
		}

		// Its work in, the push checks run again, green: the push goes.
		commit(t, in.get(t, repair.GetId()).GetWorktree(), "fix.txt")
		in.release(t, repair.GetId())
		if got := in.ended(t, repair.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Fatalf("the repair ended %v (%s)", got.GetStatus(), got.GetError())
		}
		in.mu.Lock()
		in.testCode = 0
		in.mu.Unlock()
		waitFor(t, "the push", func() bool {
			in.pass(t, time.Second)
			return in.pushState(t).GetHeld() == "" && in.remoteTip(t, bare) == in.tip(t)
		})
		if got, _ := in.integration(t, in.get(t, repair.GetId())); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
			t.Errorf("the repair's integration %v; want committed", got)
		}
		// The running work is not told to merge or rebase: it goes on once released, and ends.
		if got := in.told(t, busy); len(got) != 0 {
			t.Errorf("busy told %q after the repair merged; want nothing", got)
		}
		in.release(t, busy.GetId())
		in.ended(t, busy.GetId())
	})
	// While Djinn's own correction repairs the red push checks, the lead's repair of them is refused, naming it; a repair
	// flagged while the push checks are not red is refused too.
	t.Run("beside Djinn's correction", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir())
		repo := checkedRepo(t)
		wishID, projectID := e.wish(t, repo)
		if _, err := e.spawnReq(t, wishID, "Repair the checks", "text x", &planv1.TaskServiceSpawnRequest{Repair: true}); connect.CodeOf(err) !=
			connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "are not red: nothing to repair") {
			t.Errorf("--repair on green push checks: %v; want it refused", err)
		}

		head, err := git(t.Context(), repo, "rev-parse", "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		f := &planv1.IntegrationFailure{State: planv1.IntegrationState_INTEGRATION_STATE_RED, Base: head, Reason: "test: exit status 1"}
		err = e.db.Tx(t.Context(), func(tx *store.Tx) error {
			return editPush(t.Context(), tx, wishID, projectID, func(p *planv1.WishPush) {
				p.Held, p.FailedSha, p.Failure = "at "+short8(head)+", test: exit status 1", head, f
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		djinns, err := e.h.spawn(t.Context(), "test/correct", &planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: "Merge: push checks — test", Prompt: "wait", Provider: planv1.Provider_PROVIDER_FAKE,
		}, &planv1.Task{Correction: &planv1.TaskCorrection{Failure: f, Attempt: 1}})
		if err != nil {
			t.Fatal(err)
		}
		err = e.db.Tx(t.Context(), func(tx *store.Tx) error {
			return editPush(t.Context(), tx, wishID, projectID, func(p *planv1.WishPush) { p.CorrectedBy, p.Attempts = djinns.GetId(), 1 })
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.spawnReq(t, wishID, "Repair: push checks red at "+short8(head), "text fixed", nil)
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), djinns.GetCode()+" already repairs") ||
			!strings.Contains(err.Error(), "djinn task stop "+djinns.GetId()) {
			t.Errorf("the lead's repair beside Djinn's correction: %v; want it refused, naming %s", err, djinns.GetCode())
		}
		if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: djinns.GetId()})); err != nil {
			t.Fatal(err)
		}
	})
}

// TestAimsAtPushChecks: what names the red push checks a task repairs, from the tasks of 17/10/2026.
func TestAimsAtPushChecks(t *testing.T) {
	t.Parallel()
	p := &planv1.WishPush{Held: "at 76e975e2, test: go tool task test exited 201", FailedSha: "76e975e2aa11",
		Failure: &planv1.IntegrationFailure{Base: "76e975e2aa11"}}
	for _, c := range []struct {
		name, title, prompt string
		want                bool
	}{
		{"title with the red sha", "Repair feat/qol: push checks red at 76e975e2 (go tool task test exited 201)", "", true}, // W248
		{"prompt with the red sha", "Fix the build", "feat/qol's push checks are red at 76e975e2 ('go tool task test exited 201')", true},
		{"title naming the push checks", "Push checks: fix the flaky e2e", "", true},
		{"prompt telling of repairs", "A worker silent after its result is stopped and ended; one repair per red",
			"feat/qol stayed red for hours ... one repair per red per wish: Djinn sees a repair the lead spawned (a task " +
				"whose prompt or title targets the red push checks)", false}, // W272 itself
		{"documents them", "Document the push checks", "", false},
		{"told not to run them", "Add a button", "Never run the push checks: Djinn runs them. Fix what lint finds.", false},
		{"the sha alone", "Look at 76e975e2", "what changed at 76e975e2", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := aimsAtPushChecks(c.title, c.prompt, p); got != c.want {
				t.Errorf("%q / %q: %v, want %v", c.title, c.prompt, got, c.want)
			}
		})
	}
}
