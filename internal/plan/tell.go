package plan

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
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
	if !attached {
		w.tellWaitingAnswers(ctx, wish.GetId(), name)
	}
	return w.Leads.Say(name, line)
}

// Answered tells the lead of q's wish that the developer answered q, in one line, with what Djinn did with the
// answer (did) when it settled it. Handlers calls it once the answer is stored, when it serves the leads; the answer
// stays in the brief whatever happens here.
func (w *Wishes) Answered(ctx context.Context, q *planv1.Question, did string) {
	if q.GetAnswer() == nil || q.GetAnswer().GetActor() == planv1.Closer_CLOSER_LEAD {
		return
	}
	if routeOption(q) != nil {
		w.tell(ctx, q, RoutedLine(q))
		return
	}
	if did != "" {
		w.tell(ctx, q, AnswerLine(q, "", did))
		return
	}
	worker := w.workerOf(ctx, q, planv1.TaskRole_TASK_ROLE_CONVERTER)
	if worker != "" {
		w.tell(ctx, q, AnswerLine(q, worker, ""))
		return
	}

	ownTerminal := LeadTerminal(q.GetWishId())
	if w.Leads != nil && w.Leads.Running(ownTerminal) {
		w.tell(ctx, q, AnswerLine(q, "", ""))
		return
	}

	pw, err := PrincipalWish(ctx, w.Store)
	if err == nil && pw != nil {
		principalTerminal := LeadTerminal(pw.GetId())
		if w.Leads != nil && w.Leads.Running(principalTerminal) {
			qWish, err := store.Get[*planv1.Wish](ctx, w.Store, q.GetWishId())
			if err != nil {
				log.Printf("djinn: wish %s: get question wish: %v", q.GetWishId(), err)
			}
			if err := w.Tell(ctx, pw.GetId(), PrincipalAnswerLine(q, qWish)); err != nil && !errors.Is(err, ErrNoLead) {
				log.Printf("djinn: wish %s: tell principal lead about %s: %v", pw.GetId(), q.GetCode(), err)
			}
			return
		}
	}

	// No lead runs at all: the answer waits, marked waiting for a lead.
	if w.Store != nil {
		spec := connect.Spec{Procedure: planv1connect.QuestionServiceAnswerProcedure}
		req := &planv1.QuestionServiceAnswerRequest{Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q.GetId()}}}
		_ = write(ctx, w.Store, spec, req, func(tx *store.Tx) error {
			cur, err := store.Get[*planv1.Question](ctx, tx, q.GetId())
			if err != nil {
				return err
			}
			cur.WaitingLead = true
			return tx.Put(cur)
		})
	}
	q.WaitingLead = true
}

// tellWaitingAnswers tells terminal of every answer waiting for a lead, and marks them no longer waiting.
func (w *Wishes) tellWaitingAnswers(ctx context.Context, wishID, terminal string) {
	if w.Leads == nil || w.Store == nil {
		return
	}
	questions, err := store.List[*planv1.Question](ctx, w.Store, nil)
	if err != nil {
		log.Printf("djinn: wish %s: list waiting answers: %v", wishID, err)
		return
	}
	for _, q := range questions {
		if !q.GetWaitingLead() {
			continue
		}
		var line string
		if q.GetWishId() == wishID {
			line = AnswerLine(q, "", "")
		} else {
			qWish, err := store.Get[*planv1.Wish](ctx, w.Store, q.GetWishId())
			if err != nil {
				log.Printf("djinn: wish %s: get question wish %s: %v", wishID, q.GetWishId(), err)
			}
			line = PrincipalAnswerLine(q, qWish)
		}
		if err := w.Leads.Say(terminal, line); err != nil {
			log.Printf("djinn: wish %s: tell waiting answer %s: %v", wishID, q.GetCode(), err)
			continue
		}
		spec := connect.Spec{Procedure: planv1connect.QuestionServiceAnswerProcedure}
		req := &planv1.QuestionServiceAnswerRequest{Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q.GetId()}}}
		_ = write(ctx, w.Store, spec, req, func(tx *store.Tx) error {
			cur, err := store.Get[*planv1.Question](ctx, tx, q.GetId())
			if err != nil {
				return err
			}
			cur.WaitingLead = false
			return tx.Put(cur)
		})
		q.WaitingLead = false
	}
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
// names one, or else that the lead turns it into tasks itself.
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
	fmt.Fprintf(&b, " Turn it into tasks: you hold the plan's graph (djinn wish brief %s).", q.GetWishId())
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

// PrincipalAnswerLine is the line that tells a principal lead that q was answered in another wish,
// which has no running lead: the choice, the option, the wish title and ID, and where to find context.
func PrincipalAnswerLine(q *planv1.Question, wish *planv1.Wish) string {
	choice := choiceText(q)
	if letter, option, ok := strings.Cut(choice, ": "); ok {
		choice = fmt.Sprintf("%s — %q", letter, option)
	}
	title := ""
	if wish != nil {
		title = wish.GetTitle()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Djinn: %s answered %s in %s (wish %s): turn it into tasks there; djinn wish brief %s has its context.",
		q.GetCode(), choice, title, q.GetWishId(), q.GetWishId())
	if note := q.GetAnswer().GetNote(); note != "" {
		fmt.Fprintf(&b, " Note: %q.", clipLine(note))
	}
	return b.String()
}
