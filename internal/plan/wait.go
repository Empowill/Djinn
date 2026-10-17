package plan

// A worker in real doubt asks a blocking question (djinn question ask --wait, decision Q50 of wish QoL, W260): it
// commits what does not depend on the answer and ends its turn; Djinn puts its task WAITING on the question, its slot
// free, its worktree, branch and session kept; the answer continues it with the decision (DecisionPrompt), once a slot
// is free. A question that does not block its worker gets the decision sent while the worker runs, and else goes to
// the lead as any answer. A worker asks a few questions at most (the caps below): past them, it decides.

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/store"
)

// The questions a worker may ask about its own task: blocking ones one at a time and BlockingCap in all, and
// FreeCap that do not block. The questions Djinn asks about the task (a move it proposes, its edit question) do not
// count.
const (
	BlockingCap = 2
	FreeCap     = 3
)

// WaitingQuestionRule is how a lead takes a worker's blocking question (the brief's rules, and a driven lead's).
const WaitingQuestionRule = "An `asked` event says a worker asked with `--wait`: its task waits on the question once " +
	"its turn ends, its slot free, and the answer continues it with the decision. Answer it yourself (`djinn question " +
	"answer <question> <choice> --note \"…\"`) only when a decision the wish took or your plan settles it, naming " +
	"that decision or plan in the note; revise it (`djinn question revise`) when the developer lacks what they need to " +
	"choose; otherwise leave it to them: a new choice is theirs. Never spawn a task for its answer, nor continue its " +
	"task by hand: Djinn does, once a slot is free."

// decideLine is what a worker past its caps is told: the choice is its own now.
const decideLine = "decide, and say it in your final note"

// askedBy says why the worker of task may not ask question, given wait (--wait): a blocking question is a worker's,
// about its own work task, one at a time and BlockingCap in all; FreeCap questions that do not block. nil when it may.
// The caller is QuestionService.Ask for a worker, in its transaction.
func askedBy(ctx context.Context, tx store.Reader, task *planv1.Task, question *planv1.Question, wait bool) error {
	refuse := func(format string, args ...any) error {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(format, args...))
	}
	if wait {
		switch {
		case task.GetRole() != planv1.TaskRole_TASK_ROLE_UNSPECIFIED || task.GetKind() == planv1.TaskKind_TASK_KIND_INVESTIGATION:
			return refuse("an investigation ends with its question: ask it without --wait")
		case task.GetKind() == planv1.TaskKind_TASK_KIND_IFRIT || task.GetProvider() == planv1.Provider_PROVIDER_WATCH ||
			IsAzima(task):
			return refuse("only a work task waits for an answer: ask it without --wait")
		case len(question.GetPlans()) > 0:
			return refuse("a blocking question comes back to its worker: it plans no task (drop --plan or --wait)")
		}
	}
	asked, err := store.List[*planv1.Question](ctx, tx, store.Where{"task_id": task.GetId()})
	if err != nil {
		return err
	}
	var blocking, free []string
	open := ""
	for _, q := range asked {
		if q.GetWithdrawal() != nil || q.GetDjinnMove() != nil || q.GetGrant() || q.GetRoute() != nil {
			continue
		}
		if !q.GetWait() {
			free = append(free, q.GetCode())
			continue
		}
		blocking = append(blocking, q.GetCode())
		if q.GetAnswer() == nil {
			open = q.GetCode()
		}
	}
	switch {
	case wait && open != "":
		return refuse("%s already waits for its answer, one blocking question at a time: add this one to it "+
			"(djinn question revise %s), or %s", open, open, decideLine)
	case wait && len(blocking) >= BlockingCap:
		return refuse("%s asked its %d blocking questions (%s): %s", task.GetCode(), BlockingCap,
			strings.Join(blocking, ", "), decideLine)
	case !wait && len(free) >= FreeCap:
		return refuse("%s asked its %d questions that do not block (%s): %s", task.GetCode(), FreeCap,
			strings.Join(free, ", "), decideLine)
	}
	return nil
}

// DecisionPrompt is what continues, or is sent to, the worker that asked q, once q is answered, such as "Decision Q51:
// Keep the cache (B) - the note. Go on with your task." for option B with a note.
func DecisionPrompt(q *planv1.Question) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Decision %s", q.GetCode())
	if q.GetAnswer().GetActor() == planv1.Closer_CLOSER_LEAD {
		b.WriteString(", by the lead")
	}
	b.WriteString(": ")
	c := q.GetAnswer().GetChoice()
	if i := int(c - planv1.Choice_CHOICE_A); c >= planv1.Choice_CHOICE_A && i < len(q.GetOptions()) {
		fmt.Fprintf(&b, "%s (%s)", strings.TrimRight(strings.TrimSpace(q.GetOptions()[i]), "."), choiceLetter(c))
	} else {
		b.WriteString(choiceLetter(c))
	}
	if note := strings.TrimRight(strings.TrimSpace(q.GetAnswer().GetNote()), "."); note != "" {
		b.WriteString(" - " + note)
	}
	b.WriteString(". Go on with your task.")
	return b.String()
}

// IsDecision tells whether text is the decision of the question code given to its worker (DecisionPrompt).
func IsDecision(text, code string) bool {
	return strings.HasPrefix(text, "Decision "+code+":") || strings.HasPrefix(text, "Decision "+code+", ")
}

// AskedLine is the line that tells a lead a worker asked q, a blocking question: its task waits for the answer once
// its turn ends.
func AskedLine(q *planv1.Question, task string) string {
	return fmt.Sprintf("Djinn: %s asked %s with --wait: %q. It waits for the answer once its turn ends. "+
		"Answer it only from a decision the wish took or from your plan (djinn question answer %s), or revise it "+
		"to enrich it; a new choice is the developer's.", task, q.GetCode(), clipLine(q.GetText()), q.GetCode())
}

// Asked tells the lead of q's wish that a worker asked q, a blocking question (djinn question ask --wait). Questions
// calls it once q is stored.
func (w *Wishes) Asked(ctx context.Context, q *planv1.Question) {
	if !q.GetWait() {
		return
	}
	code := q.GetTaskId()
	if t, err := store.Get[*planv1.Task](ctx, w.Store, q.GetTaskId()); err == nil && t.GetCode() != "" {
		code = t.GetCode()
	} else if err != nil {
		logx.L().Warn("read the task that asked a question", "question", q.GetCode(), "err", err)
	}
	w.notify(ctx, q, planv1.LeadEventKind_LEAD_EVENT_KIND_ASKED, AskedLine(q, code), "")
}
