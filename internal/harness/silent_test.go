package harness

import (
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// leadLinesOf are the lines the lead's queue got about the task code, as "KIND text".
func leadLinesOf(q *leadQueue, code string) []string {
	var out []string
	for _, ev := range q.all() {
		if ev.GetCode() == code {
			out = append(out, strings.TrimPrefix(ev.GetKind().String(), "LEAD_EVENT_KIND_")+" "+ev.GetText())
		}
	}
	return out
}

// TestSilentAfterResultEndsDone (W272, as W267 hung after its final note): a worker whose turn ended with its result
// and its final note, silent since with its CPU idle, runs on until SilentAfterResult, then Djinn stops it and it ends
// done, its lead told in one line, its done line. A worker as silent whose background work computes runs on; so does
// one Djinn holds paused (W226).
func TestSilentAfterResultEndsDone(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	clock := &fakeClock{at: time.Date(2026, 10, 17, 12, 8, 35, 0, time.UTC)}
	e := up(t, t.TempDir(), WithClock(clock.now), WithSilenceLimit(2*time.Hour))
	queue := &leadQueue{}
	e.h.NotifyLead(queue.notify)
	wishID, _ := e.wish(t, nativeFolder(t))
	script := "text Fixed and committed: lint and the e2e are green.\nrefuse Bash git merge --ff-only 56bc4246\nusage 48 9152 0.77\nwait"
	idle := e.spawnScoped(t, wishID, "idle", script)
	busy := e.spawnScoped(t, wishID, "busy", script)
	paused := e.spawnScoped(t, wishID, "paused", script)
	for _, task := range []*planv1.Task{idle, busy, paused} {
		e.follow(t, task.GetId())("its turn's result", saidUsage)
	}
	e.pause(t, paused.GetId())

	start := clock.now()
	e.readCPU(t, idle.GetId(), start, 1.8, 1.1, 0.9, 1.4, 1.2, 1.1, 0.7, 1.4, 1.2, 1.1)
	e.readCPU(t, busy.GetId(), start, 190, 177, 175, 197, 188, 210, 190, 153, 187, 204) // Its background tests.
	e.readCPU(t, paused.GetId(), start, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	clock.set(start.Add(9 * time.Minute))
	e.passes(3)
	if got := e.get(t, idle.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("silent 9 min after its result: %v (%s), want it running", got.GetStatus(), got.GetError())
	}
	clock.set(start.Add(11 * time.Minute))
	e.passes(3)
	done := e.ended(t, idle.GetId())
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || done.GetExitCode() != 0 {
		t.Fatalf("status %v exit %d (%s), want done", done.GetStatus(), done.GetExitCode(), done.GetError())
	}
	why := "silent 11 min after its result, CPU 1%"
	if texts := e.eventTexts(t, idle.GetId(), planv1.TaskEventKind_TASK_EVENT_KIND_STATUS); !slices.Contains(texts,
		"done: "+why+"; Djinn stopped it, its final note written") {
		t.Errorf("status events %q", texts)
	}
	if runs := done.GetRuns(); len(runs) != 1 || runs[0].GetReason() != "done" {
		t.Errorf("runs %v, want one, done", runs)
	}
	for _, task := range []*planv1.Task{busy, paused} {
		if got := e.get(t, task.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING &&
			got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PAUSED {
			t.Errorf("%s: %v (%s), want it on", task.GetCode(), got.GetStatus(), got.GetError())
		}
	}

	// Its one line: done (its refused call made it told at its end), why Djinn stopped it, its final note.
	waitFor(t, "its done line", func() bool { return len(leadLinesOf(queue, idle.GetCode())) > 0 })
	e.passes(2)
	lines := leadLinesOf(queue, idle.GetCode())
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "ENDED Djinn: "+idle.GetCode()+" done ("+why+": Djinn stopped it), ") ||
		!strings.Contains(lines[0], "Fixed and committed") {
		t.Errorf("lead lines %q, want its one done line", lines)
	}
	for _, task := range []*planv1.Task{busy, paused} {
		if lines := leadLinesOf(queue, task.GetCode()); len(lines) > 0 {
			t.Errorf("%s: lead lines %q, want none", task.GetCode(), lines)
		}
	}
	e.resume(t, paused.GetId())
	for _, task := range []*planv1.Task{busy, paused} {
		if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSilentAfterResultWithoutNote (W272): a worker silent after a result with no final note (its last word a tool
// call) is stopped and continued once on its session, told finishPrompt; its lead is told in one line, never that it
// failed. Continued, it writes its note and ends done.
func TestSilentAfterResultWithoutNote(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	clock := &fakeClock{at: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)}
	e := up(t, t.TempDir(), WithClock(clock.now), WithSilenceLimit(2*time.Hour))
	queue := &leadQueue{}
	e.h.NotifyLead(queue.notify)
	wishID, _ := e.wish(t, nativeFolder(t))
	task := e.spawn(t, wishID, "text Running the checks.\ntool Bash go tool task lint\nusage 100 20 0.01\nwait")
	e.follow(t, task.GetId())("its turn's result", saidUsage)
	start := clock.now()
	e.readCPU(t, task.GetId(), start, 2, 1, 3, 1, 2, 1, 1, 2, 1, 1, 2)
	clock.set(start.Add(10 * time.Minute))
	e.passes(3)

	done := e.ended(t, task.GetId())
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("status %v (%s), want done once continued", done.GetStatus(), done.GetError())
	}
	if runs := done.GetRuns(); len(runs) != 2 || runs[0].GetReason() != runSilent {
		t.Errorf("runs %v, want the first ended silent, then one more", runs)
	}
	if prompts := e.eventTexts(t, task.GetId(), planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT); len(prompts) != 2 || prompts[1] != finishPrompt {
		t.Errorf("prompts %q, want its first, then %q", prompts, finishPrompt)
	}
	why := "silent 10 min after its result, CPU 2%"
	if texts := e.eventTexts(t, task.GetId(), planv1.TaskEventKind_TASK_EVENT_KIND_STATUS); !slices.Contains(texts,
		"resuming: "+why+", with no final note; Djinn continues it once: "+finishPrompt) {
		t.Errorf("status events %q", texts)
	}
	if note := e.h.finalNote(t.Context(), task.GetId()); note != finishPrompt {
		t.Errorf("final note %q, want the one the continued fake wrote", note)
	}
	waitFor(t, "its lines", func() bool { return len(leadLinesOf(queue, task.GetCode())) >= 2 })
	lines := leadLinesOf(queue, task.GetCode())
	want := "HEALTH Djinn: " + task.GetCode() + " " + why + ", with no final note: stopped and continued once, told to " +
		"finish its task and write its final note; silent so again, it fails."
	if len(lines) != 2 || lines[0] != want || !strings.HasPrefix(lines[1], "ENDED ") {
		t.Errorf("lead lines %q, want %q, then its done line", lines, want)
	}
}

// TestEndedSilent: a worker stopped silent after its result ends done with its final note; without one, it is
// continued once, then fails; an agent that cannot resume its session fails at once.
func TestEndedSilent(t *testing.T) {
	t.Parallel()
	at := timestamppb.New(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC))
	running := &planv1.TaskRun{StartTime: at}
	silent := &planv1.TaskRun{StartTime: at, EndTime: at, Reason: runSilent}
	done := &planv1.TaskRun{StartTime: at, EndTime: at, Reason: "done"}
	why := "silent 10 min after its result, CPU 1%"
	for _, c := range []struct {
		name     string
		task     *planv1.Task
		note     bool
		status   planv1.TaskStatus
		errorHas string
	}{
		{"note", &planv1.Task{Provider: planv1.Provider_PROVIDER_CLAUDE, Runs: []*planv1.TaskRun{running}}, true,
			planv1.TaskStatus_TASK_STATUS_DONE, ""},
		{"no note, first", &planv1.Task{Provider: planv1.Provider_PROVIDER_CLAUDE, Runs: []*planv1.TaskRun{running}}, false,
			planv1.TaskStatus_TASK_STATUS_RESUMING, ""},
		{"no note, after a done run", &planv1.Task{Provider: planv1.Provider_PROVIDER_CLAUDE, Runs: []*planv1.TaskRun{silent, done, running}},
			false, planv1.TaskStatus_TASK_STATUS_RESUMING, ""},
		{"no note, continued already", &planv1.Task{Provider: planv1.Provider_PROVIDER_CLAUDE, Runs: []*planv1.TaskRun{silent, running}},
			false, planv1.TaskStatus_TASK_STATUS_FAILED, "once more after it was told to write it"},
		{"no session", &planv1.Task{Provider: planv1.Provider_PROVIDER_WATCH, Runs: []*planv1.TaskRun{running}}, false,
			planv1.TaskStatus_TASK_STATUS_FAILED, "cannot resume its session"},
	} {
		endedSilent(c.task, why, c.note)
		if c.task.GetStatus() != c.status || !strings.Contains(c.task.GetError(), c.errorHas) ||
			(c.status == planv1.TaskStatus_TASK_STATUS_RESUMING) != c.task.GetContinuing() {
			t.Errorf("%s: %v (%q), continuing %v; want %v with %q", c.name, c.task.GetStatus(), c.task.GetError(),
				c.task.GetContinuing(), c.status, c.errorHas)
		}
		if c.status == planv1.TaskStatus_TASK_STATUS_FAILED && !strings.HasPrefix(c.task.GetError(), why) {
			t.Errorf("%s: error %q, want it to say why first", c.name, c.task.GetError())
		}
	}
}
