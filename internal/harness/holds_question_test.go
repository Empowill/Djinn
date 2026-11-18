package harness

import (
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// TestSpawnAfterQuestion (W107, Q64 C): a task spawned after an open question (--after Qnn) waits for its answer,
// held by the question, which is blocking meanwhile; the answer releases it, and its worker starts with the decision.
// A question decided already holds nothing: spawning after it is refused.
func TestSpawnAfterQuestion(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())
	res, err := e.questions().Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which cache?", Options: []string{"Keep the cache", "Drop it"}, WishId: wishID,
	}))
	if err != nil {
		t.Fatal(err)
	}
	q := res.Msg.GetQuestion()
	task := e.mustSpawn(t, wishID, "Wire the cache", "text wiring", &planv1.TaskServiceSpawnRequest{
		After: []string{strings.ToLower(q.GetCode())},
	})
	held := e.until(t, task.GetId(), waking(e.h, func(t *planv1.Task) bool {
		return t.GetHold() == planv1.TaskHold_TASK_HOLD_QUESTION
	}))
	if !slices.Equal(held.GetHoldQuestionIds(), []string{q.GetId()}) || held.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING ||
		!strings.Contains(held.GetWaitReason(), "waits for the answer to "+q.GetCode()) {
		t.Fatalf("spawned after %s: %v", q.GetCode(), held)
	}
	e.answerNoted(t, q, planv1.Choice_CHOICE_B, "small first")
	e.until(t, task.GetId(), waking(e.h, func(t *planv1.Task) bool { return t.GetStartTime() != nil }))
	// Its fake worker says its prompt back: the decision is in it.
	e.untilText(t, task.GetId(), "Decision "+q.GetCode()+": Drop it (B) - small first.")
	if _, err := e.spawnReq(t, wishID, "Late", "text late", &planv1.TaskServiceSpawnRequest{
		After: []string{q.GetCode()},
	}); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "is decided") {
		t.Errorf("spawned after a decided question: %v", err)
	}
}
