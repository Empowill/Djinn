package harness

// The relay (T33, W381): a worker that cannot resume its session starts a new one, told what came before. Djinn keeps
// a task's session in the folder of the sign-in it ran on; moving to another sign-in, the session is copied there
// first (moveSession, account.go), and the worker resumes it. Should the copy fail, or claude not find the session
// where it resumes (sessionLost), the worker starts a new session instead, on its first prompt and the relay: what
// it was told after it, and what it wrote last, as its task's events keep them. Its worktree holds the rest.

import (
	"context"
	"slices"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/store"
)

const (
	// relayEntries is how many messages the relay carries at most: prompts after the first, and the worker's texts.
	relayEntries = 8
	// relayEntry is how many characters of each it keeps, and relayTotal of all of them.
	relayEntry = 1500
	relayTotal = 6000
)

// sessionNotFound is what claude says, and exits on, when --resume names a session its folder does not hold
// (claude 2.1.316, W381).
const sessionNotFound = "No conversation found with session ID"

// relayHead begins the relay.
const relayHead = "Djinn could not bring your earlier session here. Your worktree holds its work: " +
	"read its git log and its git status before you go on."

// relay is what a worker on a new session is told of the task's earlier ones, oldest first: the prompts it was given
// after its first one (continued, decisions), and what its workers wrote last.
func (h *Harness) relay(ctx context.Context, taskID string) string {
	var entries []string
	total := 0
	err := store.Latest(ctx, h.store, store.Where{"task_id": taskID}, func(e *planv1.TaskEvent) bool {
		text := strings.TrimSpace(e.GetText())
		var who string
		switch {
		case text == "":
			return true
		case e.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT && e.GetSeq() > 1:
			who = "You were told:"
		case e.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_TEXT:
			who = "You wrote:"
		default:
			return true
		}
		text = clipRunes(text, relayEntry)
		if total += len([]rune(text)); total > relayTotal && len(entries) > 0 {
			return false
		}
		entries = append(entries, who+"\n"+text)
		return len(entries) < relayEntries
	})
	if err != nil {
		logx.L().Warn("read a task's events for its relay", "task", taskID, "err", err)
	}
	if len(entries) == 0 {
		return relayHead + " It left no message."
	}
	slices.Reverse(entries)
	return relayHead + " What it was told after this prompt, and what it wrote last, oldest first:\n\n" +
		strings.Join(entries, "\n\n")
}

// sessionLost turns the failure of a Claude Code worker that could not resume its session (sessionNotFound) into a
// resume on a new session, told what came before: the task waits to start again at the next pass, spending none of
// its resumes. A worker that started a new session is not concerned: it never loops. The caller is end, which owns
// the run's task.
func (h *Harness) sessionLost(r *run, t *planv1.Task) {
	if r.resumed == "" || !strings.Contains(r.failure+"\n"+t.GetError(), sessionNotFound) {
		return
	}
	t.Status, t.Error, t.ResumeAfter, t.Hold = planv1.TaskStatus_TASK_STATUS_RESUMING, "", nil, 0
	t.WaitReason = whySessionLost + " on " + accountText(t.GetAccount()) + ": starts a new session, told what came before"
	logx.L().Info("task resumes on a new session", "task", t.GetCode(), "session", r.resumed, "account", t.GetAccount())
}
