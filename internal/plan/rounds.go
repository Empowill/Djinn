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

// HistoryHeader introduces the prior-versions section appended to a revised question's context.
const HistoryHeader = "## Historique des versions pr\u00e9c\u00e9dentes"

// CleanContext strips any auto-appended HistoryHeader section from a question's context so revisions never nest or
// duplicate prior history.
func CleanContext(context string) string {
	if i := strings.Index(context, "\n\n"+HistoryHeader); i >= 0 {
		return strings.TrimRight(context[:i], "\r\n ")
	}
	if i := strings.Index(context, "\n"+HistoryHeader); i >= 0 {
		return strings.TrimRight(context[:i], "\r\n ")
	}
	if strings.HasPrefix(context, HistoryHeader) {
		return ""
	}
	return context
}

// formatHistory formats the chronological history from Tour 0 through prior revisions so the question context
// preserves every prior version without nesting or duplicating.
func formatHistory(rounds []*planv1.Round) string {
	var b strings.Builder
	tour := 0
	for _, r := range rounds {
		switch r.GetKind() {
		case planv1.RoundKind_ROUND_KIND_ENLIGHTEN:
			if note := strings.TrimSpace(r.GetNote()); note != "" {
				fmt.Fprintf(&b, "- Enlighten (Tour %d): %s\n", tour, note)
			}
		case planv1.RoundKind_ROUND_KIND_REVISE:
			label := fmt.Sprintf("Tour %d", tour)
			if tour == 0 {
				label = "Tour 0 (initial)"
			}
			fmt.Fprintf(&b, "### %s\n", label)
			if opts := r.GetOptions(); len(opts) > 0 {
				var formatted []string
				for i, o := range opts {
					formatted = append(formatted, fmt.Sprintf("%c: %s", 'A'+i, o))
				}
				fmt.Fprintf(&b, "- Options: %s\n", strings.Join(formatted, " | "))
			}
			if rec := strings.TrimSpace(r.GetRecommendation()); rec != "" {
				fmt.Fprintf(&b, "- Recommendation: %s\n", rec)
			}
			if ctx := strings.TrimSpace(CleanContext(r.GetContext())); ctx != "" {
				fmt.Fprintf(&b, "\n%s\n", ctx)
			}
			b.WriteString("\n")
			tour++
		}
	}
	body := strings.TrimSpace(b.String())
	if body == "" {
		return ""
	}
	return HistoryHeader + "\n\n" + body
}

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
				CleanContext(question.GetContext()), slices.Clone(question.GetOptions()), question.GetRecommendation()
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
			prevContext := CleanContext(question.GetContext())
			round.Kind, round.TaskId = planv1.RoundKind_ROUND_KIND_REVISE, m.GetTaskId()
			round.Context, round.Options, round.Recommendation =
				prevContext, slices.Clone(question.GetOptions()), question.GetRecommendation()
			baseContext := prevContext
			if m.GetContext() != "" {
				baseContext = CleanContext(m.GetContext())
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
			history := formatHistory(append(slices.Clone(question.GetRounds()), round))
			switch {
			case baseContext != "" && history != "":
				question.Context = baseContext + "\n\n" + history
			case history != "":
				question.Context = history
			default:
				question.Context = baseContext
			}
			question.Revision++
			return nil
		})
	if err != nil {
		return nil, err
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
