package plan

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// Investigating tells an open question whose last round is a request to investigate: it waits for the lead's
// revision, not for the developer.
func Investigating(q *planv1.Question) bool {
	rounds := q.GetRounds()
	return q.GetAnswer() == nil && len(rounds) > 0 &&
		rounds[len(rounds)-1].GetKind() == planv1.RoundKind_ROUND_KIND_ENLIGHTEN
}

// Enlighten records a request to investigate an open question, with what to dig into.
func (q *Questions) Enlighten(
	ctx context.Context, req *connect.Request[planv1.QuestionServiceEnlightenRequest],
) (*connect.Response[planv1.QuestionServiceEnlightenResponse], error) {
	question, err := q.round(ctx, req.Spec(), req.Msg, req.Msg.GetQuestion(), req.Msg.GetWishId(),
		func(question *planv1.Question, round *planv1.Round) {
			round.Kind, round.Note = planv1.RoundKind_ROUND_KIND_ENLIGHTEN, req.Msg.GetNote()
		})
	if err != nil {
		return nil, err
	}
	for _, f := range q.Enlightened {
		f(ctx, proto.CloneOf(question), req.Msg.GetNote())
	}
	return connect.NewResponse(&planv1.QuestionServiceEnlightenResponse{Question: question}), nil
}

// Revise changes an open question after investigating: what is given replaces the question's own, which the round
// keeps.
func (q *Questions) Revise(
	ctx context.Context, req *connect.Request[planv1.QuestionServiceReviseRequest],
) (*connect.Response[planv1.QuestionServiceReviseResponse], error) {
	m := req.Msg
	question, err := q.round(ctx, req.Spec(), m, m.GetQuestion(), m.GetWishId(),
		func(question *planv1.Question, round *planv1.Round) {
			round.Kind, round.TaskId = planv1.RoundKind_ROUND_KIND_REVISE, m.GetTaskId()
			round.Context, round.Options, round.Recommendation =
				question.GetContext(), slices.Clone(question.GetOptions()), question.GetRecommendation()
			if m.GetContext() != "" {
				question.Context = m.GetContext()
			}
			if len(m.GetOptions()) > 0 {
				question.Options = m.GetOptions()
			}
			if m.GetRecommendation() != "" {
				question.Recommendation = m.GetRecommendation()
			}
			if m.Before != nil {
				question.Before = strings.TrimSpace(m.GetBefore())
			}
			question.Revision++
		})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.QuestionServiceReviseResponse{Question: question}), nil
}

// round adds a round to an open question, set by fn, in one journaled transaction.
func (q *Questions) round(
	ctx context.Context, spec connect.Spec, msg proto.Message,
	ref *planv1.QuestionRef, wishID string, fn func(*planv1.Question, *planv1.Round),
) (*planv1.Question, error) {
	var question *planv1.Question
	err := write(ctx, q.Store, spec, msg, func(tx *store.Tx) error {
		var err error
		if question, err = find(ctx, tx, ref, wishID); err != nil {
			return err
		}
		if question.GetAnswer() != nil {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("question %s is decided: ask a new one", question.GetCode()))
		}
		round := &planv1.Round{Actor: actor, CreateTime: timestamppb.Now()}
		fn(question, round)
		if err := taskOfWish(ctx, tx, question.GetWishId(), round.GetTaskId()); err != nil {
			return err
		}
		question.Rounds = append(question.Rounds, round)
		return tx.Put(question)
	})
	return question, err
}
