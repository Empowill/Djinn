package plan

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/store"
)

// ErrNoLead says that a wish has no lead to tell: no session is recorded for it, or its lead does not run and the
// wish is not active. What the lead missed waits in the wish's brief.
var ErrNoLead = errors.New("this wish has no lead to tell")

// Tell types line in the terminal of the lead, then Enter, once the person is not typing there:
// the lead reads it as a message, and acts. A lead that does not run is reopened first, on its session, as Resume
// does, without bringing the window to the front. ErrNoLead when there is none to tell.
func (w *Wishes) Tell(ctx context.Context, wishID, line string) error {
	if w.Leads == nil {
		return errors.New("this server runs no terminal")
	}
	lead, err := store.Get[*planv1.Lead](ctx, w.Store, "lead")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if !hasLead(lead) {
		return ErrNoLead
	}
	name := LeadTerminal
	if wishID != "" {
		wish, err := store.Get[*planv1.Wish](ctx, w.Store, wishID)
		if err == nil && wish.GetState() != planv1.WishState_WISH_STATE_ACTIVE {
			// A wish not active does not reopen the lead: the line goes to the lead if it runs, never to a shell.
			command := w.Leads.Command(name)
			if command == nil {
				return fmt.Errorf("%w: the lead does not run", ErrNoLead)
			}
			if !runsLead(command, lead) {
				return fmt.Errorf("%w: the lead's terminal runs %s, not the lead", ErrNoLead, strings.Join(command, " "))
			}
			if err := w.Leads.Say(name, line); err != nil {
				return fmt.Errorf("%w: %w", ErrNoLead, err)
			}
			return nil
		}
	}
	resume, dir, err := sessionLine(lead)
	if err != nil {
		return err
	}
	command, _, attached, err := w.Leads.Open(name, resume, dir, lead.GetSessionId())
	if err != nil {
		return err
	}
	if attached && !runsLead(command, lead) {
		return fmt.Errorf("the lead's terminal runs %s, not the lead", strings.Join(command, " "))
	}
	return w.Leads.Say(name, line)
}

// Answered tells the lead that the developer answered q, in one line, with what Djinn did with the
// answer (did) when it settled it: an event in the lead's queue, which its watch hands out (decision Q191), never
// lost. Handlers calls it once the answer is stored, when it serves the leads; the answer stays in the brief whatever
// happens here. While no lead runs, the question is marked waiting for a lead, until the lead acknowledges it. was is
// the answer it replaced, when the developer changed their choice: the line says so. One answer is one event: the
// answer events of q that still wait for the lead give way to this one.
func (w *Wishes) Answered(ctx context.Context, q *planv1.Question, was *planv1.Answer, did string) {
	if q.GetAnswer() == nil || q.GetAnswer().GetActor() == planv1.Closer_CLOSER_LEAD {
		return
	}
	line := AnswerLine(q, did)
	if was != nil && was.GetChoice() != q.GetAnswer().GetChoice() {
		line = ChangedLine(q, was, did)
	}
	if routeOption(q) != nil {
		line = RoutedLine(q)
	}
	if did == "" && routeOption(q) == nil && (w.Leads == nil || !w.Leads.Running(LeadTerminal)) && w.Store != nil {
		// No lead runs at all: the answer waits, marked waiting for a lead.
		spec := connect.Spec{Procedure: planv1connect.QuestionServiceAnswerProcedure}
		req := &planv1.QuestionServiceAnswerRequest{Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q.GetId()}}}
		if err := write(ctx, w.Store, spec, req, func(tx *store.Tx) error {
			cur, err := store.Get[*planv1.Question](ctx, tx, q.GetId())
			if err != nil {
				return err
			}
			cur.WaitingLead = true
			return tx.Put(cur)
		}); err != nil {
			logx.L().Error("mark an answer waiting for a lead", "wish", q.GetWishId(), "question", q.GetCode(), "err", err)
		}
		q.WaitingLead = true
	}
	w.replaceAnswer(ctx, q, func(*planv1.LeadEvent) string { return line })
}

// NoteEdited tells the lead that the developer changed the note of their answer to q, which had the note was: the
// answer event of q that still waits for the lead takes the new note in its place; or else one event says it, which
// the next edits change in turn while it waits. However many times the note is saved, the lead reads it once.
func (w *Wishes) NoteEdited(ctx context.Context, q *planv1.Question, was string) {
	if q.GetAnswer() == nil || q.GetAnswer().GetActor() == planv1.Closer_CLOSER_LEAD {
		return
	}
	w.replaceAnswer(ctx, q, func(waiting *planv1.LeadEvent) string {
		if waiting == nil {
			return NoteEditedLine(q)
		}
		text, old, note := waiting.GetText(), noteText(was), noteText(q.GetAnswer().GetNote())
		if old != "" && strings.Contains(text, old) {
			return strings.Replace(text, old, note, 1)
		}
		if note == "" {
			note = " The note is removed."
		}
		return text + note
	})
}

// replaceAnswer queues an answer event about q, its text line(the newest answer event of q that waits for the lead,
// or nil), in place of the answer events of q that wait: the lead has not acknowledged them, and reads the one.
// It is a new event, after the watch's cursor: a lead whose digest held the one it replaces reads this one too.
func (w *Wishes) replaceAnswer(ctx context.Context, q *planv1.Question, line func(waiting *planv1.LeadEvent) string) {
	if w.Store == nil {
		return
	}
	e := &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER, WishId: q.GetWishId(), Code: q.GetCode()}
	err := w.notifyInPlace(ctx, e, func(waiting []*planv1.LeadEvent) []*planv1.LeadEvent {
		var mine []*planv1.LeadEvent
		for _, o := range waiting {
			if o.GetKind() == e.GetKind() && o.GetWishId() == e.GetWishId() && o.GetCode() == e.GetCode() && e.GetCode() != "" {
				mine = append(mine, o)
			}
		}
		var newest *planv1.LeadEvent
		if len(mine) > 0 {
			newest = mine[len(mine)-1]
		}
		e.Text = line(newest)
		return mine
	})
	if err != nil {
		logx.L().Error("tell the lead about a question", "wish", q.GetWishId(), "question", q.GetCode(), "err", err)
	}
}

// queueWaitingAnswers queues an answer event for each answer waiting for a lead that none is queued for: an answer
// marked so before the lead's events were queued.
func (w *Wishes) queueWaitingAnswers(ctx context.Context) {
	questions, err := store.List[*planv1.Question](ctx, w.Store, nil)
	if err != nil {
		logx.L().Error("list the answers waiting for a lead", "err", err)
		return
	}
	events, err := store.List[*planv1.LeadEvent](ctx, w.Store, nil)
	if err != nil {
		logx.L().Error("list the lead's events", "err", err)
		return
	}
	queued := map[string]bool{}
	for _, e := range events {
		if e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER {
			queued[e.GetWishId()+"/"+e.GetCode()] = true
		}
	}
	for _, q := range questions {
		if !q.GetWaitingLead() || queued[q.GetWishId()+"/"+q.GetCode()] {
			continue
		}
		e := &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER, WishId: q.GetWishId(), Code: q.GetCode(),
			Text: AnswerLine(q, "")}
		if err := QueueLeadEvent(ctx, w.Store, e, w.now()); err != nil {
			logx.L().Error("queue an answer for the lead", "wish", q.GetWishId(), "question", q.GetCode(), "err", err)
		}
	}
}

// Enlightened tells the lead of q's wish that the developer wants to know more before answering q, with their note.
func (w *Wishes) Enlightened(ctx context.Context, q *planv1.Question, note string) {
	if w.workerOf(ctx, q, planv1.TaskRole_TASK_ROLE_INVESTIGATOR) != "" {
		// Decision Q173: when an investigator runs, its start is silent in the terminal
		// (journal and window only); the lead hears when the worker ends.
		return
	}
	w.notify(ctx, q, planv1.LeadEventKind_LEAD_EVENT_KIND_ENLIGHTEN, EnlightenLine(q, note, ""))
}

// Approved tells the lead that the developer approved the answer the lead or a worker gave q: the decision is the
// developer's now. Marks calls it once the approval is stored, and only for such an answer (decision Q173).
func (w *Wishes) Approved(ctx context.Context, q *planv1.Question) {
	by := "you"
	if id := q.GetAnswer().GetTaskId(); id != "" {
		by = "a worker"
		if t, err := store.Get[*planv1.Task](ctx, w.Store, id); err == nil && t.GetCode() != "" {
			by = t.GetCode()
		}
	}
	w.notify(ctx, q, planv1.LeadEventKind_LEAD_EVENT_KIND_APPROVED, ApprovedLine(q, by))
}

// ApprovedLine is the line that tells a lead the developer approved the answer by gave q (you, or a worker's code):
// the decision is theirs now.
func ApprovedLine(q *planv1.Question, by string) string {
	choice := choiceText(q)
	if letter, option, ok := strings.Cut(choice, ": "); ok {
		choice = fmt.Sprintf("%s — %q", letter, option)
	}
	return fmt.Sprintf("Djinn: %s approved by the developer, as %s answered it: %s. The decision is theirs now.",
		q.GetCode(), by, choice)
}

// workerOf is the code of the question worker of role that works on q, started just before the lead is told; ""
// when none does.
func (w *Wishes) workerOf(ctx context.Context, q *planv1.Question, role planv1.TaskRole) string {
	t, err := QuestionWorkerOf(ctx, w.Store, q, role)
	if err != nil {
		logx.L().Error("find a question's worker", "wish", q.GetWishId(), "question", q.GetCode(), "err", err)
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

// notify queues an event of kind about q for the lead.
func (w *Wishes) notify(ctx context.Context, q *planv1.Question, kind planv1.LeadEventKind, line string) {
	if w.Store == nil {
		return
	}
	e := &planv1.LeadEvent{Kind: kind, WishId: q.GetWishId(), Code: q.GetCode(), Text: line}
	if err := w.Notify(ctx, e); err != nil {
		logx.L().Error("tell the lead about a question", "wish", q.GetWishId(), "question", q.GetCode(), "err", err)
	}
}

// AnswerLine is the line that tells a lead the answer to q: the choice, the option and the note, on one line; then
// what Djinn did with it, when did says it settled it; or else that the lead turns it into tasks itself.
func AnswerLine(q *planv1.Question, did string) string {
	choice := quotedChoice(q)
	return answerLine(q, fmt.Sprintf("Djinn: %s answered %s.", q.GetCode(), choice), did)
}

// ChangedLine is AnswerLine for an answer that replaces was, another choice: the line says the developer changed it.
func ChangedLine(q *planv1.Question, was *planv1.Answer, did string) string {
	return answerLine(q, fmt.Sprintf("Djinn: %s answer changed from %s to %s.", q.GetCode(),
		choiceLetter(was.GetChoice()), quotedChoice(q)), did)
}

// NoteEditedLine is the line that tells a lead the developer changed the note of their answer to q, and only it.
func NoteEditedLine(q *planv1.Question) string {
	head := fmt.Sprintf("Djinn: %s note edited, the answer stays %s.", q.GetCode(), quotedChoice(q))
	if q.GetAnswer().GetNote() == "" {
		return head + " The note is removed."
	}
	return head + noteText(q.GetAnswer().GetNote())
}

// quotedChoice is the choice that answers q, its letter then its option quoted: B — "Olive".
func quotedChoice(q *planv1.Question) string {
	choice := choiceText(q)
	if letter, option, ok := strings.Cut(choice, ": "); ok {
		choice = fmt.Sprintf("%s — %q", letter, option)
	}
	return choice
}

// choiceLetter is a choice in a word: its letter, or yes or no.
func choiceLetter(c planv1.Choice) string {
	if c >= planv1.Choice_CHOICE_A && c <= planv1.Choice_CHOICE_D {
		return string(rune('A' + int(c-planv1.Choice_CHOICE_A)))
	}
	return strings.ToLower(strings.TrimPrefix(c.String(), "CHOICE_"))
}

// noteText is the part of an answer line that gives its note; "" without one.
func noteText(note string) string {
	if note == "" {
		return ""
	}
	return fmt.Sprintf(" Note: %q.", clipLine(note))
}

// answerLine is head, then the note of q's answer, then what Djinn did with it, or else that the lead turns it into
// tasks itself.
func answerLine(q *planv1.Question, head, did string) string {
	var b strings.Builder
	b.WriteString(head)
	b.WriteString(noteText(q.GetAnswer().GetNote()))
	if did != "" {
		fmt.Fprintf(&b, " %s.", strings.TrimSuffix(did, "."))
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
