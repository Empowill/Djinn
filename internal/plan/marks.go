package plan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

// Marks implements MarkService. A mark lives on what it marks: a question's or a block's own list, one mark of each
// kind, so that it travels with them in an export.
type Marks struct {
	planv1connect.UnimplementedMarkServiceHandler
	Store *store.Store
	// Answered are called with a question once an approval has answered it.
	Answered []func(context.Context, *planv1.Question)
}

func (m *Marks) Put(
	ctx context.Context, req *connect.Request[planv1.MarkServicePutRequest],
) (*connect.Response[planv1.MarkServicePutResponse], error) {
	msg := req.Msg
	var marked *planv1.Marked
	var answered *planv1.Question
	err := write(ctx, m.Store, req.Spec(), msg, func(tx *store.Tx) error {
		question, block, err := target(ctx, tx, msg.GetTarget(), msg.GetWishId())
		if err != nil {
			return err
		}
		mark := &planv1.Mark{Kind: msg.GetKind(), Actor: actor, CreateTime: timestamppb.Now()}
		if question != nil {
			// Approving an open question answers it with the option its recommendation names.
			if msg.GetKind() == planv1.MarkKind_MARK_KIND_APPROVED && !msg.GetRemove() && question.GetAnswer() == nil {
				choice, ok := Recommended(question)
				if !ok {
					return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
						"the recommendation of %s names no option: answer it with djinn question answer", question.GetCode()))
				}
				question.Answer = &planv1.Answer{Choice: choice, CreateTime: mark.GetCreateTime()}
				answered = question
			}
			question.Marks = setMark(question.GetMarks(), mark, msg.GetRemove())
			marked = questionMarked(question, mark)
			return tx.Put(question)
		}
		block.Marks = setMark(block.GetMarks(), mark, msg.GetRemove())
		marked = blockMarked(block, mark)
		return tx.Put(block)
	})
	if err != nil {
		return nil, err
	}
	if answered != nil {
		for _, f := range m.Answered {
			f(ctx, proto.CloneOf(answered))
		}
	}
	return connect.NewResponse(&planv1.MarkServicePutResponse{Marked: marked}), nil
}

// target finds what a mark goes on: a question by its code, or a question or a block by its identifier.
func target(
	ctx context.Context, tx *store.Tx, ref *planv1.MarkTarget, wishID string,
) (*planv1.Question, *planv1.Block, error) {
	if ref.GetCode() != "" {
		q, err := find(ctx, tx, &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: ref.GetCode()}}, wishID)
		return q, nil, err
	}
	q, err := store.Get[*planv1.Question](ctx, tx, ref.GetId())
	if err == nil || !errors.Is(err, store.ErrNotFound) {
		return q, nil, err
	}
	b, err := store.Get[*planv1.Block](ctx, tx, ref.GetId())
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no question nor block %s", ref.GetId()))
	}
	return nil, b, err
}

// setMark puts mark in marks, in place of one of its kind, or takes that one off.
func setMark(marks []*planv1.Mark, mark *planv1.Mark, remove bool) []*planv1.Mark {
	marks = slices.DeleteFunc(slices.Clone(marks), func(x *planv1.Mark) bool { return x.GetKind() == mark.GetKind() })
	if remove {
		return marks
	}
	return append(marks, mark)
}

func questionMarked(q *planv1.Question, mark *planv1.Mark) *planv1.Marked {
	return &planv1.Marked{
		WishId: q.GetWishId(), QuestionId: q.GetId(), Label: q.GetCode(), Title: clipLine(q.GetText()), Mark: mark,
	}
}

func blockMarked(b *planv1.Block, mark *planv1.Mark) *planv1.Marked {
	return &planv1.Marked{
		WishId: b.GetWishId(), BlockId: b.GetId(), Label: b.GetKind(), Title: clipLine(b.GetTitle()), Mark: mark,
	}
}

func (m *Marks) List(
	ctx context.Context, req *connect.Request[planv1.MarkServiceListRequest],
) (*connect.Response[planv1.MarkServiceListResponse], error) {
	where := store.Where{"wish_id": req.Msg.GetWishId()}
	questions, err := store.List[*planv1.Question](ctx, m.Store, where)
	if err != nil {
		return nil, Status(err)
	}
	blocks, err := store.List[*planv1.Block](ctx, m.Store, where)
	if err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.MarkServiceListResponse{Marks: marksOf(questions, blocks)}), nil
}

// marksOf are the marks on questions and blocks, the oldest first.
func marksOf(questions []*planv1.Question, blocks []*planv1.Block) []*planv1.Marked {
	var out []*planv1.Marked
	for _, q := range questions {
		for _, mark := range q.GetMarks() {
			out = append(out, questionMarked(q, mark))
		}
	}
	for _, b := range blocks {
		for _, mark := range b.GetMarks() {
			out = append(out, blockMarked(b, mark))
		}
	}
	slices.SortStableFunc(out, func(x, y *planv1.Marked) int {
		return cmp.Or(x.GetMark().GetCreateTime().AsTime().Compare(y.GetMark().GetCreateTime().AsTime()),
			strings.Compare(x.GetQuestionId()+x.GetBlockId(), y.GetQuestionId()+y.GetBlockId()))
	})
	return out
}

// Recommended is the choice a question's recommendation names, for a one-click approval. A question without options
// is answered yes. Otherwise the recommendation names an option by its letter first ("B: …", "**B**, because…"), or
// starts with the first word of one option only ("Yes: it is measured" for an option "Yes, pour it"). The window
// reads it the same way (recommendedChoice in src/data/format.ts).
func Recommended(q *planv1.Question) (planv1.Choice, bool) {
	options := q.GetOptions()
	if len(options) == 0 {
		return planv1.Choice_CHOICE_YES, true
	}
	text := strings.TrimLeft(q.GetRecommendation(), " \t\n*_#>`")
	for _, prefix := range []string{"Option ", "option "} {
		text = strings.TrimPrefix(text, prefix)
	}
	if r, size := utf8.DecodeRuneInString(text); r >= 'A' && r < 'A'+rune(len(options)) && r <= 'D' {
		rest := strings.TrimLeft(text[size:], " ")
		next, _ := utf8.DecodeRuneInString(rest)
		if rest == "" || strings.ContainsRune(":.)*,;—–-(", next) {
			return planv1.Choice_CHOICE_A + planv1.Choice(r-'A'), true
		}
	}
	word := firstWord(text)
	if word == "" {
		return 0, false
	}
	found := -1
	for i, option := range options {
		if !strings.EqualFold(firstWord(option), word) {
			continue
		}
		if found >= 0 {
			return 0, false
		}
		found = i
	}
	if found < 0 {
		return 0, false
	}
	return planv1.Choice_CHOICE_A + planv1.Choice(found), true
}

// firstWord is the letters text starts with.
func firstWord(text string) string {
	text = strings.TrimSpace(text)
	if end := strings.IndexFunc(text, func(r rune) bool { return !unicode.IsLetter(r) }); end >= 0 {
		return text[:end]
	}
	return text
}
