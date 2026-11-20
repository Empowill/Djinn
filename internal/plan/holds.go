package plan

// A question holds the work Djinn cannot start without its answer (W107, decision Q64 of wish Djinn gates): the lead
// names it when it asks (djinn question ask --holds W12,T16, or revise), or plans the task after the question (djinn
// task spawn --after Q64). Each held task keeps the question in Task.hold_question_ids: while the question is open,
// the scheduler starts neither the task nor, for an azima, any part of it, and the question is blocking
// (render.Blocked). The answer releases it, a withdrawal too, without a write: a question no longer open holds
// nothing. Its worker then starts with the decision, or the withdrawal's note (HeldPrompt).

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// questionName is a question's code as a name among tasks': Q64, any case.
var questionName = regexp.MustCompile(`^[Qq][0-9]{2,3}$`)

// splitNames splits names, repeated or separated by commas or spaces (--holds W12,T16), into single names.
func splitNames(lists []string) []string {
	var out []string
	for _, list := range lists {
		out = append(out, strings.FieldsFunc(list, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })...)
	}
	return out
}

// SplitQuestions splits what a task comes after (djinn task spawn --after W1,Q64) into the questions' codes and the
// tasks' names, each list as given.
func SplitQuestions(after []string) (questions, tasks []string) {
	for _, name := range splitNames(after) {
		if questionName.MatchString(name) {
			questions = append(questions, name)
		} else {
			tasks = append(tasks, name)
		}
	}
	return questions, tasks
}

// HoldingQuestions resolves the codes of questions a new task comes after into their identifiers, without repeats:
// each an open question of the wish wishID, which then holds the task.
func HoldingQuestions(ctx context.Context, r store.Reader, wishID string, codes []string) ([]string, error) {
	var ids []string
	for _, code := range codes {
		found, err := store.List[*planv1.Question](ctx, r, store.Where{"wish_id": wishID, "code": strings.ToUpper(code)})
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("question %s is not a question of the wish", code))
		}
		q := found[0]
		if !openQuestion(q) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"question %s is %s: it holds nothing; spawn the task without it, its decision with --decision %s",
				q.GetCode(), closedAs(q), q.GetCode()))
		}
		if !slices.Contains(ids, q.GetId()) {
			ids = append(ids, q.GetId())
		}
	}
	return ids, nil
}

// closedAs says how a question that is not open closed: decided, or withdrawn.
func closedAs(q *planv1.Question) string {
	if q.GetWithdrawal() != nil {
		return "withdrawn"
	}
	return "decided"
}

// setHolds makes question hold the work refs name (--holds), in tx, in place of what it held: a task it no longer
// names is released. Each must be a task or an azima of the question's wish that has not started (PENDING).
func setHolds(ctx context.Context, tx *store.Tx, question *planv1.Question, refs []string) error {
	tasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": question.GetWishId()})
	if err != nil {
		return err
	}
	var held []string
	for _, name := range splitNames(refs) {
		i := slices.IndexFunc(tasks, func(t *planv1.Task) bool {
			return t.GetId() == name || strings.EqualFold(t.GetCode(), name)
		})
		if i < 0 {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"--holds %s: not a task of the wish; plan it after the question instead (djinn task spawn --after %s)",
				name, codeOr(question)))
		}
		t := tasks[i]
		if t.GetId() == question.GetTaskId() {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"--holds %s: the task that asks; a worker waits for its own question with --wait", t.GetCode()))
		}
		if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || t.GetStartTime() != nil {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"--holds %s: it is %s; a question holds only work that has not started",
				t.GetCode(), strings.ToLower(strings.TrimPrefix(t.GetStatus().String(), "TASK_STATUS_"))))
		}
		if !slices.Contains(held, t.GetId()) {
			held = append(held, t.GetId())
		}
	}
	for _, t := range tasks {
		has, want := slices.Contains(t.GetHoldQuestionIds(), question.GetId()), slices.Contains(held, t.GetId())
		switch {
		case want && !has:
			t.HoldQuestionIds = append(t.HoldQuestionIds, question.GetId())
		case has && !want:
			t.HoldQuestionIds = slices.DeleteFunc(slices.Clone(t.GetHoldQuestionIds()), func(id string) bool {
				return id == question.GetId()
			})
		default:
			continue
		}
		if err := tx.Put(t); err != nil {
			return err
		}
	}
	return nil
}

// codeOr is the question's code, or Qnn before it has one.
func codeOr(q *planv1.Question) string {
	if q.GetCode() != "" {
		return q.GetCode()
	}
	return "Qnn"
}

// HeldPrompt is what the worker of a task some questions held reads after its prompt as it starts: the decision of
// each question that held it or its azima, or its withdrawal and note; "" when none held it. A question still open,
// or gone, says nothing: the task started without it (djinn task start).
func HeldPrompt(ctx context.Context, r store.Reader, t *planv1.Task) string {
	ids := slices.Clone(t.GetHoldQuestionIds())
	if azima := t.GetPartOf(); azima != "" {
		if a, err := store.Get[*planv1.Task](ctx, r, azima); err == nil {
			ids = append(ids, a.GetHoldQuestionIds()...)
		}
	}
	var lines []string
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		q, err := store.Get[*planv1.Question](ctx, r, id)
		if err != nil {
			continue
		}
		switch {
		case q.GetAnswer() != nil:
			lines = append(lines, "- "+decisionLine(q)+".")
		case q.GetWithdrawal() != nil:
			line := fmt.Sprintf("- %s was withdrawn without an answer", q.GetCode())
			if note := strings.TrimRight(strings.TrimSpace(q.GetWithdrawal().GetNote()), "."); note != "" {
				line += ": " + note
			}
			lines = append(lines, line+".")
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "This task waited for these questions; they are settled:\n" + strings.Join(lines, "\n")
}
