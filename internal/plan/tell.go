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
			return fmt.Errorf("%w: %v", ErrNoLead, err)
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

// Answered tells the lead of q's wish that the developer answered q, in one line. Handlers calls it once the
// answer is stored, when it serves the leads; the answer stays in the brief whatever happens here.
func (w *Wishes) Answered(ctx context.Context, q *planv1.Question) {
	if q.GetAnswer() == nil {
		return
	}
	w.tell(ctx, q, AnswerLine(q))
}

// Enlightened tells the lead of q's wish that the developer wants to know more before answering q, with their note.
func (w *Wishes) Enlightened(ctx context.Context, q *planv1.Question, note string) {
	w.tell(ctx, q, EnlightenLine(q, note))
}

func (w *Wishes) tell(ctx context.Context, q *planv1.Question, line string) {
	if err := w.Tell(ctx, q.GetWishId(), line); err != nil && !errors.Is(err, ErrNoLead) {
		log.Printf("djinn: wish %s: tell the lead about %s: %v", q.GetWishId(), q.GetCode(), err)
	}
}

// AnswerLine is the line that tells a lead the answer to q: the choice, the option and the note, on one line.
func AnswerLine(q *planv1.Question) string {
	choice := choiceText(q)
	if letter, option, ok := strings.Cut(choice, ": "); ok {
		choice = fmt.Sprintf("%s — %q", letter, option)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Djinn: %s answered %s.", q.GetCode(), choice)
	if note := q.GetAnswer().GetNote(); note != "" {
		fmt.Fprintf(&b, " Note: %q.", clipLine(note))
	}
	fmt.Fprintf(&b, " Act on it: djinn wish brief %s has the context.", q.GetWishId())
	return b.String()
}

// EnlightenLine is the line that tells a lead the developer wants to know more before answering q.
func EnlightenLine(q *planv1.Question, note string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Djinn: %s, the developer wants to know more before answering.", q.GetCode())
	if note != "" {
		fmt.Fprintf(&b, " Note: %q.", clipLine(note))
	}
	fmt.Fprintf(&b, " Investigate, then revise %s: djinn wish brief %s has the context.", q.GetCode(), q.GetWishId())
	return b.String()
}
