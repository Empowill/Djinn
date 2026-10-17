package plan

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// HistoryHeader introduced the history of earlier versions Revise appended to a question's context until W114. A
// context holds its current version only now, the earlier ones live in the question's rounds: CleanContext cuts what
// a caller still pastes, and CleanVersions what questions stored before.
const HistoryHeader = "## Historique des versions pr\u00e9c\u00e9dentes"

// CleanContext strips the HistoryHeader section from a question's context, and all that follows it.
func CleanContext(context string) string {
	if i := strings.Index(context, "\n"+HistoryHeader); i >= 0 {
		return strings.TrimRight(context[:i], "\r\n ")
	}
	if strings.HasPrefix(context, HistoryHeader) {
		return ""
	}
	return context
}

// CleanVersions cuts from every stored question the history Revise appended to its context until W114, in its
// context and in its rounds' (CleanContext), each change journaled: run as djinn up opens the store, it changes
// nothing once done.
func CleanVersions(ctx context.Context, s *store.Store) error {
	questions, err := store.List[*planv1.Question](ctx, s, nil)
	if err != nil {
		return err
	}
	for _, q := range questions {
		changed := false
		clean := func(text *string) {
			if c := CleanContext(*text); c != *text {
				*text, changed = c, true
			}
		}
		clean(&q.Context)
		for _, r := range q.GetRounds() {
			clean(&r.Context)
		}
		if !changed {
			continue
		}
		if err := s.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal(actor, methodCleanVersions, q); err != nil {
				return err
			}
			return tx.Put(q)
		}); err != nil {
			return fmt.Errorf("clean the versions of %s: %w", q.GetCode(), err)
		}
	}
	return nil
}

// methodCleanVersions journals CleanVersions; the request is the question, cleaned.
const methodCleanVersions = "plan/clean-versions"

// Investigating tells an open question whose last round is a request to investigate: it waits for the lead's
// revision, not for the developer.
func Investigating(q *planv1.Question) bool {
	rounds := q.GetRounds()
	return q.GetAnswer() == nil && q.GetWithdrawal() == nil && len(rounds) > 0 &&
		rounds[len(rounds)-1].GetKind() == planv1.RoundKind_ROUND_KIND_ENLIGHTEN
}

// Enlighten records a request to investigate an open question, with what to dig into.
func (q *Questions) Enlighten(
	ctx context.Context, req *connect.Request[planv1.QuestionServiceEnlightenRequest],
) (*connect.Response[planv1.QuestionServiceEnlightenResponse], error) {
	question, err := q.round(ctx, req.Spec(), req.Msg, req.Msg.GetQuestion(), req.Msg.GetWishId(),
		func(_ *store.Tx, question *planv1.Question, round *planv1.Round) error {
			round.Kind, round.Note = planv1.RoundKind_ROUND_KIND_ENLIGHTEN, req.Msg.GetNote()
			round.Context, round.Options, round.Recommendation =
				question.GetContext(), slices.Clone(question.GetOptions()), question.GetRecommendation()
			return nil
		})
	if err != nil {
		return nil, err
	}
	// The request is stored: its investigator starts, or its lead hears of it, even when the caller hangs up
	// meanwhile. A window that gave up left it under investigation with neither (W536).
	ctx = context.WithoutCancel(ctx)
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
	if path := m.GetContextFile(); path != "" {
		if m.GetContext() != "" {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("give context or context_file, not both"))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("read context_file: %w", err))
		}
		text := strings.TrimRight(string(data), "\r\n")
		if len(text) > 20000 {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("context_file exceeds 20000 bytes (%d)", len(text)))
		}
		m = proto.CloneOf(m)
		m.Context, m.ContextFile = text, ""
	}
	if len(m.GetPlanFile()) > 0 {
		// The journal keeps the plans' texts, not where they were read.
		plans, err := readPlans(m.GetPlan(), m.GetPlanFile())
		if err != nil {
			return nil, err
		}
		m = proto.CloneOf(m)
		m.Plan, m.PlanFile = plans, nil
	}
	question, err := q.round(ctx, req.Spec(), m, m.GetQuestion(), m.GetWishId(),
		func(tx *store.Tx, question *planv1.Question, round *planv1.Round) error {
			if m.About != nil {
				about, err := aboutTask(ctx, tx, question.GetWishId(), m.GetAbout())
				if err != nil {
					return err
				}
				question.AboutTaskId = about
			}
			round.Kind, round.TaskId = planv1.RoundKind_ROUND_KIND_REVISE, m.GetTaskId()
			round.Context, round.Options, round.Recommendation =
				question.GetContext(), slices.Clone(question.GetOptions()), question.GetRecommendation()
			// The context is the current version only: the former one is the round's. What a caller pastes from an
			// older output, with the history Djinn appended to it, is cut.
			if m.GetContext() != "" {
				question.Context = CleanContext(m.GetContext())
			}
			if len(m.GetOptions()) > 0 {
				question.Options = m.GetOptions()
				fitPlans(question)
			}
			if err := setPlans(ctx, tx, question, m.GetPlan(), true); err != nil {
				return err
			}
			if m.GetRecommendation() != "" {
				question.Recommendation = m.GetRecommendation()
			}
			if m.Before != nil {
				question.Before = strings.TrimSpace(m.GetBefore())
			}
			if len(m.GetSleepUntil()) > 0 {
				if err := sleepUntil(ctx, tx, question, m.GetSleepUntil(), round.GetCreateTime().AsTime()); err != nil {
					return err
				}
			}
			question.Revision++
			return nil
		})
	if err != nil {
		return nil, err
	}
	if !m.GetHistory() {
		question.Rounds = nil // The current version only: its earlier ones are behind history.
	}
	return connect.NewResponse(&planv1.QuestionServiceReviseResponse{
		Question: question, Warnings: ContextWarnings(question.GetContext()),
	}), nil
}

// round adds a round to an open question, set by fn, in one journaled transaction.
func (q *Questions) round(
	ctx context.Context, spec connect.Spec, msg proto.Message,
	ref *planv1.QuestionRef, wishID string, fn func(*store.Tx, *planv1.Question, *planv1.Round) error,
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
		if question.GetWithdrawal() != nil {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("question %s was withdrawn", question.GetCode()))
		}
		round := &planv1.Round{Actor: actor, CreateTime: timestamppb.Now()}
		if err := fn(tx, question, round); err != nil {
			return err
		}
		if err := taskOfWish(ctx, tx, question.GetWishId(), round.GetTaskId()); err != nil {
			return err
		}
		question.Rounds = append(question.Rounds, round)
		return tx.Put(question)
	})
	return question, err
}
