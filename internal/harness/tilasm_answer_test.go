package harness

import (
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// commentsHead begins the part of a launched plan's prompt that gives the comments sent with the answer.
const commentsHead = "## The developer's comments, sent with the answer"

// draft leaves a comment in draft on L01, on the whole tilasm.
func (m *mockupEnv) draft(t *testing.T, text string) {
	t.Helper()
	if _, err := m.tilasms.PutComment(t.Context(), connect.NewRequest(&planv1.TilasmServicePutCommentRequest{
		TilasmId: m.tilasm.GetId(), Text: text,
	})); err != nil {
		t.Fatal(err)
	}
}

// recommend makes the question recommend rec, as a revision by the lead does.
func (m *mockupEnv) recommend(t *testing.T, rec string) {
	t.Helper()
	err := m.db.Tx(t.Context(), func(tx *store.Tx) error {
		q, err := store.Get[*planv1.Question](t.Context(), tx, m.q.GetId())
		if err != nil {
			return err
		}
		q.Recommendation = rec
		if err := tx.Journal("test", "test/recommend", q); err != nil {
			return err
		}
		return tx.Put(q)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// send sends L01's comments with choice.
func (m *mockupEnv) send(t *testing.T, choice planv1.SendChoice) *planv1.TilasmServiceSendCommentsResponse {
	t.Helper()
	res, err := m.tilasms.SendComments(t.Context(), connect.NewRequest(&planv1.TilasmServiceSendCommentsRequest{
		TilasmId: m.tilasm.GetId(), Choice: choice,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg
}

// question is the question as stored.
func (m *mockupEnv) question(t *testing.T) *planv1.Question {
	t.Helper()
	q, err := store.Get[*planv1.Question](t.Context(), m.db, m.q.GetId())
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// commentEvents are the comments events of the wish: what the lead would tie to the question by hand.
func (m *mockupEnv) commentEvents(t *testing.T) []*planv1.LeadEvent {
	t.Helper()
	events, err := store.List[*planv1.LeadEvent](t.Context(), m.db, store.Where{"wish_id": m.wishID})
	if err != nil {
		t.Fatal(err)
	}
	var out []*planv1.LeadEvent
	for _, e := range events {
		if e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_COMMENTS {
			out = append(out, e)
		}
	}
	return out
}

// launched is the prompt of the task the question's chosen plan launched.
func (m *mockupEnv) launched(t *testing.T, q *planv1.Question) string {
	t.Helper()
	id := plan.ChosenPlan(q).GetTaskId()
	if id == "" {
		t.Fatalf("plans %v; want the chosen one launched", q.GetPlans())
	}
	prompt, err := lastPrompt(t.Context(), m.db, id)
	if err != nil {
		t.Fatal(err)
	}
	return prompt
}

// TestTilasmQuestionSettle (W13, decision Q13 D): a tilasm that proposes to build or to iterate and the question that
// links it settle each other. Answering the question starts its flow with the tilasm's comments still in draft;
// sending the comments with Launch answers the question and launches its plan with them, no event left to the lead.
func TestTilasmQuestionSettle(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("answer_launches_with_drafts", func(t *testing.T) {
		t.Parallel()
		m := newMockupEnv(t)
		m.draft(t, "Bigger icons.")
		q := m.answer(t, planv1.Choice_CHOICE_B, "")
		prompt := m.launched(t, q)
		for _, want := range []string{"text built", commentsHead, "On L01 v1, send 1", "- C1, on the whole tilasm: Bigger icons."} {
			if !strings.Contains(prompt, want) {
				t.Errorf("build prompt lacks %q:\n%s", want, prompt)
			}
		}
		did := m.lastDid()
		if !strings.Contains(did, "launched as planned") ||
			!strings.Contains(did, "send 1 of L01 went with the answer, 1 comment: djinn tilasm comments L01") {
			t.Errorf("did %q", did)
		}
		if events := m.commentEvents(t); len(events) != 0 {
			t.Errorf("comments events %v; want none", events)
		}
	})

	t.Run("answer_iterates_with_drafts", func(t *testing.T) {
		t.Parallel()
		m := newMockupEnv(t)
		m.draft(t, "Darker.")
		q := m.answer(t, planv1.Choice_CHOICE_A, "bigger icons")
		if round := plan.Iterating(q); round == nil || round.GetSend() != 1 {
			t.Fatalf("rounds %v; want an iteration with send 1", q.GetRounds())
		}
		prompt, err := lastPrompt(t.Context(), m.db, m.mockupOf(t, q).GetId())
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"bigger icons", "- C1, on the whole tilasm: Darker."} {
			if !strings.Contains(prompt, want) {
				t.Errorf("mockup prompt lacks %q:\n%s", want, prompt)
			}
		}
	})

	t.Run("launch_send_answers", func(t *testing.T) {
		t.Parallel()
		m := newMockupEnv(t)
		m.recommend(t, "B: build it.")
		m.draft(t, "Keep the count in the button.")
		res := m.send(t, planv1.SendChoice_SEND_CHOICE_LAUNCH)
		if res.GetAnswered() != m.q.GetCode() {
			t.Fatalf("send %v; want %s answered", res, m.q.GetCode())
		}
		q := m.question(t)
		a := q.GetAnswer()
		if a.GetChoice() != planv1.Choice_CHOICE_B || a.GetSendId() != res.GetSend().GetId() ||
			!strings.HasPrefix(a.GetNote(), "Launched from L01 v1 with send 1 of its comments (1 comment)") {
			t.Errorf("answer %v; want B, its note citing send 1", a)
		}
		prompt := m.launched(t, q)
		for _, want := range []string{"text built", commentsHead, "- C1, on the whole tilasm: Keep the count in the button."} {
			if !strings.Contains(prompt, want) {
				t.Errorf("build prompt lacks %q:\n%s", want, prompt)
			}
		}
		if did := m.lastDid(); !strings.Contains(did, "launched as planned") || strings.Contains(did, "waits for you") {
			t.Errorf("did %q; want the plan launched, nothing for the lead", did)
		}
		if events := m.commentEvents(t); len(events) != 0 {
			t.Errorf("comments events %v; want none left to the lead", events)
		}
	})

	t.Run("launch_send_iterates", func(t *testing.T) {
		t.Parallel()
		m := newMockupEnv(t)
		m.recommend(t, "A: one more version.")
		m.draft(t, "Darker.")
		if res := m.send(t, planv1.SendChoice_SEND_CHOICE_LAUNCH); res.GetIterated() != m.q.GetCode() ||
			res.GetSend().GetChoice() != planv1.SendChoice_SEND_CHOICE_ITERATE {
			t.Fatalf("send %v; want %s iterated", res, m.q.GetCode())
		}
		q := m.question(t)
		prompt, err := lastPrompt(t.Context(), m.db, m.mockupOf(t, q).GetId())
		if err != nil || !strings.Contains(prompt, "- C1, on the whole tilasm: Darker.") {
			t.Errorf("mockup prompt: %v\n%s", err, prompt)
		}
		if events := m.commentEvents(t); len(events) != 0 {
			t.Errorf("comments events %v; want none", events)
		}
	})

	t.Run("no_comment", func(t *testing.T) {
		t.Parallel()
		m := newMockupEnv(t)
		m.recommend(t, "B: build it.")
		if res := m.send(t, planv1.SendChoice_SEND_CHOICE_LAUNCH); res.GetAnswered() != m.q.GetCode() {
			t.Fatalf("send %v; want %s answered", res, m.q.GetCode())
		}
		q := m.question(t)
		if a := q.GetAnswer(); a.GetChoice() != planv1.Choice_CHOICE_B || a.GetNote() != "" || a.GetSendId() != "" {
			t.Errorf("answer %v; want B approved, no note", a)
		}
		if prompt := m.launched(t, q); strings.Contains(prompt, commentsHead) {
			t.Errorf("a launch without a comment has a comments part:\n%s", prompt)
		}
		if did := m.lastDid(); strings.Contains(did, "went with the answer") {
			t.Errorf("did %q", did)
		}
	})
}
