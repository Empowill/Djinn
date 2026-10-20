package harness

import (
	"os"
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

// askAbout stores a question the worker of task asks, blocking (--wait) or not, as djinn question ask does: the fake
// agent of a test runs no djinn command.
func (e *env) askAbout(t *testing.T, task *planv1.Task, wait bool) *planv1.Question {
	t.Helper()
	q := &planv1.Question{
		Id: store.NewID(), WishId: task.GetWishId(), Text: "Which cache?", CreateTime: timestamppb.Now(),
		Options: []string{"Keep the cache", "Drop the cache"}, TaskId: task.GetId(), Wait: wait,
	}
	if err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/ask", q); err != nil {
			return err
		}
		return plan.Ask(t.Context(), tx, q)
	}); err != nil {
		t.Fatal(err)
	}
	return q
}

// answerNoted answers q with choice and a note, as the developer does from the window.
func (e *env) answerNoted(t *testing.T, q *planv1.Question, choice planv1.Choice, note string) {
	t.Helper()
	if _, err := e.questions().Answer(t.Context(), connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q.GetId()}}, Choice: choice, Note: note,
	})); err != nil {
		t.Fatal(err)
	}
}

// waitsOn tells a task that waits on the blocking question q, its worker gone.
func waitsOn(q *planv1.Question) func(*planv1.Task) bool {
	return func(t *planv1.Task) bool {
		return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_WAITING && t.GetWaitQuestionId() == q.GetId()
	}
}

// TestBlockingQuestionWaits (W260, decision Q50): a worker that asked with --wait ends its turn, not its task: the task
// waits on the question, its slot free, its worktree and session kept, and its dependents wait for the question. The
// answer puts it back in line, and it starts only once a slot is free: then its session goes on with the decision.
func TestBlockingQuestionWaits(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := &limit{slots: 2}
	e := up(t, t.TempDir(), WithCapacity(l.capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))

	w1 := e.mustSpawn(t, wishID, "Pick a cache", "text working\nwait\ntext committed, waiting for the answer", nil)
	w2 := e.mustSpawn(t, wishID, "Use the cache", "text using it", &planv1.TaskServiceSpawnRequest{DependsOn: []string{"w1"}})
	q := e.askAbout(t, e.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING)), true)
	e.release(t, w1.GetId())
	waiting := e.until(t, w1.GetId(), waitsOn(q))
	if waiting.GetError() != "waiting for the answer to "+q.GetCode() || waiting.GetSessionId() == "" {
		t.Errorf("waiting task = %v", waiting)
	}
	if _, err := os.Stat(waiting.GetWorktree()); err != nil {
		t.Errorf("its worktree is gone: %v", err)
	}
	// The task is written WAITING before its run lets go of its slot (forget): wait for the slot, not for the status.
	settle(t, "its slot free while it waits", func() bool { return e.h.Running() == 0 })
	if !hasText(e.storedEvents(t, w1.GetId()), "waiting: waiting for the answer to "+q.GetCode()+"; its slot is free") {
		t.Errorf("events: %q", eventTexts(e.storedEvents(t, w1.GetId())))
	}
	want := "waits for " + q.GetCode() + ", which W1 waits for"
	e.until(t, w2.GetId(), func(t *planv1.Task) bool { return t.GetWaitReason() == want })

	// A task takes the only slot: the answer puts W1 back in line, it does not force a slot.
	l.set(1, "")
	w3 := e.mustSpawn(t, wishID, "Hold the slot", "wait\ntext released", nil)
	e.until(t, w3.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	e.answerNoted(t, q, planv1.Choice_CHOICE_B, "keep it small")
	queued := e.get(t, w1.GetId())
	if queued.GetStatus() != planv1.TaskStatus_TASK_STATUS_RESUMING || !queued.GetContinuing() ||
		queued.GetWaitQuestionId() != "" || !strings.Contains(queued.GetWaitReason(), "the most this machine holds") {
		t.Errorf("answered while no slot is free = %v", queued)
	}
	if did := e.lastDid(); !strings.Contains(did, "Djinn continues W1 with the decision once it may start") {
		t.Errorf("Djinn did %q", did)
	}
	e.release(t, w3.GetId())
	done := e.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if done.GetSessionId() != waiting.GetSessionId() || done.GetWorktree() != waiting.GetWorktree() {
		t.Errorf("continued elsewhere: %v", done)
	}
	texts := eventTexts(e.storedEvents(t, w1.GetId()))
	decision := "Decision " + q.GetCode() + ": Drop the cache (B) - keep it small. Go on with your task."
	if !slices.Contains(texts, decision) ||
		!slices.ContainsFunc(texts, func(s string) bool {
			return strings.HasPrefix(s, "continued by the developer, was waiting (waiting for the answer to "+q.GetCode()+")")
		}) {
		t.Errorf("events of the continued task: %q", texts)
	}
}

// TestBlockingQuestionAcrossARestart: a task waiting on its blocking question has no worker, so a restart of Djinn
// loses nothing: the answer given after it continues the task. One answered as Djinn stopped, before it continued the
// task, is followed up at the next start (ResumeDecisions).
func TestBlockingQuestionAcrossARestart(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	e := up(t, home, WithTick(time.Hour))
	wishID, _ := e.wish(t, nativeFolder(t))
	ask := func(title string) (*planv1.Task, *planv1.Question) {
		task := e.mustSpawn(t, wishID, title, "text working\nwait", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{title}})
		q := e.askAbout(t, e.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING)), true)
		e.release(t, task.GetId())
		e.until(t, task.GetId(), waitsOn(q))
		return task, q
	}
	w1, q1 := ask("Pick a cache")
	w2, q2 := ask("Pick a queue")
	e.down()

	e = up(t, home, WithTick(time.Hour))
	if got := e.get(t, w1.GetId()); !waitsOn(q1)(got) {
		t.Fatalf("after a restart: %v", got)
	}
	e.answerNoted(t, q1, planv1.Choice_CHOICE_A, "")
	e.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if !hasText(e.storedEvents(t, w1.GetId()), "Decision "+q1.GetCode()+": Keep the cache (A). Go on with your task.") {
		t.Errorf("events: %q", eventTexts(e.storedEvents(t, w1.GetId())))
	}

	// Q2's answer is stored, and Djinn stops before it continues W2.
	answered := proto.CloneOf(q2)
	answered.Answer = &planv1.Answer{Choice: planv1.Choice_CHOICE_B, CreateTime: timestamppb.Now(), Actor: planv1.Closer_CLOSER_LEAD}
	if err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/answer", answered); err != nil {
			return err
		}
		return tx.Put(answered)
	}); err != nil {
		t.Fatal(err)
	}
	e.down()
	e = up(t, home, WithTick(time.Hour))
	e.h.ResumeDecisions()
	e.until(t, w2.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if !hasText(e.storedEvents(t, w2.GetId()), "Decision "+q2.GetCode()+", by the lead: Drop the cache (B). Go on with your task.") {
		t.Errorf("events: %q", eventTexts(e.storedEvents(t, w2.GetId())))
	}
}

// TestDecisionWhileRunning: a question answered while its worker runs is sent to it between two turns, blocking or
// not; the worker takes it in and ends done, as a blocking question it took in no longer holds its task. One that does
// not block, answered once the worker ended, is the lead's alone.
func TestDecisionWhileRunning(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir(), WithTick(time.Hour))
	wishID, _ := e.wish(t, nativeFolder(t))
	for _, wait := range []bool{false, true} {
		task := e.mustSpawn(t, wishID, "Pick a cache", "text working\nwait\ntext went on", nil)
		q := e.askAbout(t, e.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING)), wait)
		e.answerNoted(t, q, planv1.Choice_CHOICE_A, "")
		if did := e.lastDid(); !strings.Contains(did, "Djinn sends the decision to "+task.GetCode()+", which runs") {
			t.Errorf("wait %v: Djinn did %q", wait, did)
		}
		ended := e.ended(t, task.GetId())
		if ended.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || ended.GetWaitQuestionId() != "" {
			t.Errorf("wait %v: ended %v", wait, ended)
		}
		decision := "Decision " + q.GetCode() + ": Keep the cache (A). Go on with your task."
		events := e.storedEvents(t, task.GetId())
		if !slices.ContainsFunc(events, func(ev *planv1.TaskEvent) bool {
			return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_MESSAGE && ev.GetText() == decision
		}) {
			t.Errorf("wait %v: the decision was not sent: %q", wait, eventTexts(events))
		}
	}
	after := e.ended(t, e.mustSpawn(t, wishID, "Pick a queue", "text done", nil).GetId())
	q := e.askAbout(t, after, false)
	e.answerNoted(t, q, planv1.Choice_CHOICE_B, "")
	if did := e.lastDid(); did != "" {
		t.Errorf("an answer after its worker ended: Djinn did %q, want nothing (the lead's)", did)
	}
	if got := e.get(t, after.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("an answer after its worker ended changed its task: %v", got)
	}
}
