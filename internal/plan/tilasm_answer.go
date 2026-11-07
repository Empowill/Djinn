package plan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// A tilasm that proposes to build or to iterate and the question that links it settle each other (W13, decision Q13 D):
// the lead no longer ties them by hand.
//
//   - Answering the question sends the comments the developer left in draft on the tilasms it links, one send per
//     tilasm, with the answer: an iteration round reads them (its round's send), a launched plan has them in its
//     prompt (AnswerComments).
//   - Sending comments with Launch on a tilasm that an open question links answers that question with the option
//     Launch names (its recommendation), and a note that cites the send (Answer.send_id): its plan launches with the
//     comments in its prompt, and no comments event waits for the lead. With Launch on an option that iterates, it is
//     an iteration round, as Iterate is.

// sendDrafts sends the drafts of tilasm in tx as one send of choice, for question (nil for none), at now: the send and
// its comments, nil when tilasm has no draft.
func sendDrafts(
	ctx context.Context, tx *store.Tx, tilasm *planv1.Tilasm, question *planv1.Question, choice planv1.SendChoice,
	now *timestamppb.Timestamp,
) (*planv1.TilasmSend, []*planv1.TilasmComment, error) {
	all, err := store.List[*planv1.TilasmComment](ctx, tx, store.Where{"tilasm_id": tilasm.GetId()})
	if err != nil {
		return nil, nil, err
	}
	drafts := slices.DeleteFunc(slices.Clone(all), func(c *planv1.TilasmComment) bool {
		return c.GetState() != planv1.CommentState_COMMENT_STATE_DRAFT
	})
	if len(drafts) == 0 {
		return nil, nil, nil
	}
	slices.SortFunc(drafts, func(a, b *planv1.TilasmComment) int { return strings.Compare(a.GetId(), b.GetId()) })
	send := &planv1.TilasmSend{
		Id: store.NewID(), TilasmId: tilasm.GetId(), WishId: tilasm.GetWishId(), Choice: choice,
		QuestionId: question.GetId(), CreateTime: now,
	}
	if send.Number, err = nextSend(ctx, tx, tilasm, all); err != nil {
		return nil, nil, err
	}
	for _, c := range drafts {
		c.State, c.Send, c.SendTime = planv1.CommentState_COMMENT_STATE_SENT, send.GetNumber(), now
		if err := tx.Put(c); err != nil {
			return nil, nil, err
		}
	}
	return send, drafts, tx.Put(send)
}

// questionTilasms are the identifiers of the tilasms q links, by their djinn:// links, then those its plan for the
// option choice is given, each once, in that order.
func questionTilasms(q *planv1.Question, choice planv1.Choice) []string {
	var ids []string
	for _, s := range append([]string{q.GetText(), q.GetContext(), q.GetRecommendation()}, q.GetOptions()...) {
		for _, m := range tilasmLinkID.FindAllStringSubmatch(s, -1) {
			ids = append(ids, strings.ToLower(m[1]))
		}
	}
	for _, t := range planOf(q, choice).GetTilasm() {
		id, _, _ := strings.Cut(t, "@")
		ids = append(ids, strings.ToLower(id))
	}
	var out []string
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// sendWithAnswer sends, in tx, the drafts on the tilasms of q's wish that q links or that its chosen plan is given,
// now that the developer answered it: what they wrote on them goes with their answer. An answer of the lead or a
// worker sends none: the drafts are the developer's to send.
func sendWithAnswer(ctx context.Context, tx *store.Tx, q *planv1.Question) error {
	a := q.GetAnswer()
	if a == nil || a.GetActor() != planv1.Closer_CLOSER_DEVELOPER || a.GetTaskId() != "" {
		return nil
	}
	for _, id := range questionTilasms(q, a.GetChoice()) {
		tilasm, err := store.Get[*planv1.Tilasm](ctx, tx, id)
		switch {
		case errors.Is(err, store.ErrNotFound):
			continue
		case err != nil:
			return err
		case tilasm.GetWishId() != q.GetWishId():
			continue
		}
		if _, _, err := sendDrafts(ctx, tx, tilasm, q, planv1.SendChoice_SEND_CHOICE_LAUNCH, a.GetCreateTime()); err != nil {
			return err
		}
	}
	return nil
}

// sendWithIteration sends, in tx, the drafts on the tilasm the iteration round iterates on, as the developer's answer
// records it, and names the send in round: its mockup task reads them.
func sendWithIteration(ctx context.Context, tx *store.Tx, q *planv1.Question, round *planv1.Round) error {
	if round.GetTilasmId() == "" || round.GetSend() > 0 {
		return nil
	}
	tilasm, err := store.Get[*planv1.Tilasm](ctx, tx, round.GetTilasmId())
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	send, _, err := sendDrafts(ctx, tx, tilasm, q, planv1.SendChoice_SEND_CHOICE_ITERATE, round.GetCreateTime())
	if err != nil || send == nil {
		return err
	}
	round.Send = send.GetNumber()
	return nil
}

// SentWith is a send of a tilasm's comments that went with the answer of a question: the tilasm, the send, and the
// comments it carried, by code.
type SentWith struct {
	Tilasm   *planv1.Tilasm
	Send     *planv1.TilasmSend
	Comments []*planv1.TilasmComment
}

// AnswerComments are the sends of comments with Launch that went with q's answer, by tilasm then number: the one that
// answered it (Answer.send_id), and those its answer sent. A send without a comment is not in it.
func AnswerComments(ctx context.Context, r store.Reader, q *planv1.Question) ([]SentWith, error) {
	if q.GetAnswer() == nil {
		return nil, nil
	}
	sends, err := store.List[*planv1.TilasmSend](ctx, r, store.Where{"wish_id": q.GetWishId()})
	if err != nil {
		return nil, err
	}
	sends = slices.DeleteFunc(sends, func(s *planv1.TilasmSend) bool {
		return s.GetQuestionId() != q.GetId() || s.GetChoice() != planv1.SendChoice_SEND_CHOICE_LAUNCH
	})
	slices.SortFunc(sends, func(a, b *planv1.TilasmSend) int {
		return cmp.Or(strings.Compare(a.GetTilasmId(), b.GetTilasmId()), cmp.Compare(a.GetNumber(), b.GetNumber()))
	})
	var out []SentWith
	for _, s := range sends {
		tilasm, err := store.Get[*planv1.Tilasm](ctx, r, s.GetTilasmId())
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		comments, err := store.List[*planv1.TilasmComment](ctx, r, store.Where{"tilasm_id": s.GetTilasmId()})
		if err != nil {
			return nil, err
		}
		comments = slices.DeleteFunc(comments, func(c *planv1.TilasmComment) bool {
			return c.GetState() == planv1.CommentState_COMMENT_STATE_DRAFT || c.GetSend() != s.GetNumber()
		})
		if len(comments) == 0 {
			continue
		}
		slices.SortFunc(comments, func(a, b *planv1.TilasmComment) int { return CompareCodes(a.GetCode(), b.GetCode()) })
		out = append(out, SentWith{Tilasm: tilasm, Send: s, Comments: comments})
	}
	return out, nil
}

// CommentLine is a comment as a worker's prompt lists it: its code, what it is on, its text in a line.
func CommentLine(c *planv1.TilasmComment) string {
	on := "the whole tilasm"
	if a := c.GetAnchor(); a.GetExact() != "" {
		on = fmt.Sprintf("%q", a.GetExact())
	} else if a.GetElementId() != "" || a.GetPath() != "" || a.GetFingerprint() != "" {
		on = "an element"
	}
	return fmt.Sprintf("- %s, on %s: %s", c.GetCode(), on, oneLine(c.GetText()))
}

// commentsCommand is the command that reads the comments of send of tilasm.
func commentsCommand(tilasm *planv1.Tilasm, send int32) string {
	return fmt.Sprintf("djinn tilasm comments %s --wish %s --send %d", tilasm.GetCode(), tilasm.GetWishId(), send)
}

// CommentsPrompt is the part of a launched plan's prompt that gives the comments sent with the answer that launched
// it: "" when none went with it.
func CommentsPrompt(sent []SentWith) string {
	if len(sent) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## The developer's comments, sent with the answer\n\n")
	b.WriteString("Take each into account in your work; they weigh over the plan where they differ.\n")
	for _, s := range sent {
		fmt.Fprintf(&b, "\nOn %s v%d, send %d (`%s`):\n", s.Tilasm.GetCode(), s.Comments[0].GetVersion(),
			s.Send.GetNumber(), commentsCommand(s.Tilasm, s.Send.GetNumber()))
		for _, c := range s.Comments {
			b.WriteString(CommentLine(c) + "\n")
		}
	}
	return b.String()
}

// SentWithLine says, for the lead, which sends of comments went with an answer: "" when none did.
func SentWithLine(sent []SentWith) string {
	var parts []string
	for _, s := range sent {
		count := fmt.Sprintf("%d comments", len(s.Comments))
		if len(s.Comments) == 1 {
			count = "1 comment"
		}
		parts = append(parts, fmt.Sprintf("send %d of %s went with the answer, %s: %s", s.Send.GetNumber(),
			s.Tilasm.GetCode(), count, commentsCommand(s.Tilasm, s.Send.GetNumber())))
	}
	return strings.Join(parts, "; ")
}

// SendNote is the note of the answer a send of comments with Launch gives the question that links its tilasm: it
// cites the send.
func SendNote(tilasm *planv1.Tilasm, send *planv1.TilasmSend, comments []*planv1.TilasmComment) string {
	count := fmt.Sprintf("%d comments", len(comments))
	if len(comments) == 1 {
		count = "1 comment"
	}
	return fmt.Sprintf("Launched from %s v%d with send %d of its comments (%s): %s.", tilasm.GetCode(),
		latestVersion(tilasm), send.GetNumber(), count, commentsCommand(tilasm, send.GetNumber()))
}
