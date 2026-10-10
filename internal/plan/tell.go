package plan

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// ErrNoLead says that a wish has no lead to tell: no session is recorded for it, or its lead does not run and the
// wish is not active. What the lead missed waits in the wish's brief.
var ErrNoLead = errors.New("this wish has no lead to tell")

// Tell types line in the terminal of the lead of the wish wishID, then Enter, once the person is not typing there:
// the lead reads it as a message, and acts. A lead that does not run is reopened first, on its session, as Resume
// does, without bringing the window to the front; only an active wish's lead is reopened. ErrNoLead when there is
// none to tell.
func (w *Wishes) Tell(ctx context.Context, wishID, line string) error {
	if w.Leads == nil {
		return errors.New("this server runs no terminal")
	}
	wish, err := store.Get[*planv1.Wish](ctx, w.Store, wishID)
	if err != nil {
		return err
	}
	session := wish.GetLead().GetSessionId()
	if session == "" {
		return ErrNoLead
	}
	name := LeadTerminal(wish.GetId())
	if wish.GetState() != planv1.WishState_WISH_STATE_ACTIVE {
		if err := w.Leads.Say(name, line); err != nil {
			return fmt.Errorf("%w: %w", ErrNoLead, err)
		}
		return nil
	}
	resume, dir, err := sessionLine(wish)
	if err != nil {
		return err
	}
	command, _, attached, err := w.Leads.Open(name, resume, dir, session)
	if err != nil {
		return err
	}
	// The lead's terminal runs something else, a shell perhaps: a line typed there would run as a command.
	if running := strings.Join(command, " "); attached && !strings.Contains(running, session) {
		return fmt.Errorf("the lead's terminal runs %s, not the lead", running)
	}
	return w.Leads.Say(name, line)
}

// Answered tells the lead of q's wish that the developer answered q, in one line, with what Djinn did with the
// answer (did) when it settled it. Handlers calls it once the answer is stored, when it serves the leads; the answer
// stays in the brief whatever happens here.
func (w *Wishes) Answered(ctx context.Context, q *planv1.Question, did string) {
	if q.GetAnswer() == nil {
		return
	}
	if routeOption(q) != nil {
		w.tell(ctx, q, RoutedLine(q))
		return
	}
	w.tell(ctx, q, AnswerLine(q, w.workerOf(ctx, q, planv1.TaskRole_TASK_ROLE_CONVERTER), did))
}

// Enlightened tells the lead of q's wish that the developer wants to know more before answering q, with their note.
func (w *Wishes) Enlightened(ctx context.Context, q *planv1.Question, note string) {
	w.tell(ctx, q, EnlightenLine(q, note, w.workerOf(ctx, q, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)))
}

// workerOf is the code of the question worker of role that works on q, started just before the lead is told; ""
// when none does.
func (w *Wishes) workerOf(ctx context.Context, q *planv1.Question, role planv1.TaskRole) string {
	t, err := QuestionWorkerOf(ctx, w.Store, q, role)
	if err != nil {
		log.Printf("djinn: question %s: find its worker: %v", q.GetCode(), err)
	}
	return t.GetCode()
}

// QuestionWorkerOf is the question worker of role that still works on q (Task.role): planned, running, paused, or
// one Djinn resumes; nil when none does.
func QuestionWorkerOf(ctx context.Context, r store.Reader, q *planv1.Question, role planv1.TaskRole) (*planv1.Task, error) {
	tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": q.GetWishId()})
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if t.GetRole() != role || t.GetQuestion() != q.GetCode() {
			continue
		}
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskStatus_TASK_STATUS_RUNNING,
			planv1.TaskStatus_TASK_STATUS_PAUSED, planv1.TaskStatus_TASK_STATUS_RESUMING,
			planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
			return t, nil
		}
	}
	return nil, nil
}

func (w *Wishes) tell(ctx context.Context, q *planv1.Question, line string) {
	if err := w.Tell(ctx, q.GetWishId(), line); err != nil && !errors.Is(err, ErrNoLead) {
		log.Printf("djinn: wish %s: tell the lead about %s: %v", q.GetWishId(), q.GetCode(), err)
	}
}

// AnswerLine is the line that tells a lead the answer to q: the choice, the option and the note, on one line; then
// what Djinn did with it, when did says it settled it; else the question worker that turns it into tasks, when worker
// names one, or else that the lead acts on it.
func AnswerLine(q *planv1.Question, worker, did string) string {
	choice := choiceText(q)
	if letter, option, ok := strings.Cut(choice, ": "); ok {
		choice = fmt.Sprintf("%s — %q", letter, option)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Djinn: %s answered %s.", q.GetCode(), choice)
	if note := q.GetAnswer().GetNote(); note != "" {
		fmt.Fprintf(&b, " Note: %q.", clipLine(note))
	}
	if did != "" {
		fmt.Fprintf(&b, " %s.", strings.TrimSuffix(did, "."))
		return b.String()
	}
	if worker != "" {
		fmt.Fprintf(&b, " %s turns it into tasks; you will hear when it ends.", worker)
		return b.String()
	}
	fmt.Fprintf(&b, " Act on it: djinn wish brief %s has the context.", q.GetWishId())
	return b.String()
}

// EnlightenLine is the line that tells a lead the developer wants to know more before answering q: what they want to
// know, as they typed it, on one line; then the question worker that investigates, when worker names one, or else
// that the lead does.
func EnlightenLine(q *planv1.Question, note, worker string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Djinn: %s, the developer wants to know more before answering.", q.GetCode())
	if note := oneLine(note); note != "" {
		fmt.Fprintf(&b, " Note: \"%s\".", note)
	}
	if worker != "" {
		fmt.Fprintf(&b, " %s investigates, then revises it; you will hear when it ends.", worker)
		return b.String()
	}
	fmt.Fprintf(&b, " Investigate, then revise %s: djinn wish brief %s has the context.", q.GetCode(), q.GetWishId())
	return b.String()
}
