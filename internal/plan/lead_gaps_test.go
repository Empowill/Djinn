package plan

import (
	"context"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// The gaps of the lead's command line its session of 16/10/2026 measured (decision Q31 A, study L10): one answer's
// note read through a list of 113 KB, task codes that two wishes shared, notes cut at 160 characters, and a task
// watch that followed a running task.

// TestQuestionGet: djinn question get prints one question as it stands, its answer and note whole, without what its
// revisions replaced; a code two wishes share needs the wish.
func TestQuestionGet(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	w1, w2 := c.wish(t), c.wish(t)
	former := strings.Repeat("what the question said before. ", 500)
	note := "Take B, and keep the old page a week.\n\nThen drop it."
	q := &planv1.Question{
		Id: store.NewID(), Code: "Q01", WishId: w1, Text: "Which page?", Options: []string{"Old", "New"},
		Context: "The context now.", Recommendation: "B: the new one.",
		Rounds: []*planv1.Round{
			{Kind: planv1.RoundKind_ROUND_KIND_ENLIGHTEN, Note: "Which one loads faster?"},
			{Kind: planv1.RoundKind_ROUND_KIND_REVISE, Context: former, Options: []string{"Old", "New", "Both"},
				Recommendation: "C: both."},
		},
		Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_B, Note: note},
	}
	other := &planv1.Question{Id: store.NewID(), Code: "Q01", WishId: w2, Text: "Another one?"}
	c.put(t, q, other)

	get := func(req *planv1.QuestionServiceGetRequest) (*planv1.Question, error) {
		res, err := c.questions.Get(ctx, connect.NewRequest(req))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetQuestion(), nil
	}
	byCode := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q01"}}
	if _, err := get(&planv1.QuestionServiceGetRequest{Question: byCode}); code(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "--wish-id") {
		t.Errorf("a code of two wishes, no wish = %v, want failed_precondition naming --wish-id", err)
	}
	got, err := get(&planv1.QuestionServiceGetRequest{Question: byCode, WishId: w1})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetId() != q.GetId() || got.GetAnswer().GetNote() != note || got.GetContext() != "The context now." ||
		got.GetRecommendation() != "B: the new one." || len(got.GetOptions()) != 2 {
		t.Errorf("question get = %v, want Q01 of the first wish as it stands, its note whole", got)
	}
	if len(got.GetRounds()) != 2 || got.GetRounds()[0].GetNote() != "Which one loads faster?" {
		t.Fatalf("rounds = %v, want both, the request's note kept", got.GetRounds())
	}
	if r := got.GetRounds()[1]; r.GetContext() != "" || len(r.GetOptions()) != 0 || r.GetRecommendation() != "" {
		t.Errorf("a revision keeps what it replaced: %v", r)
	}
	stored, err := store.Get[*planv1.Question](ctx, c.store, q.GetId())
	if err != nil || stored.GetRounds()[1].GetContext() != former {
		t.Errorf("question get changed the stored rounds: %v", err)
	}
	if got, err := get(&planv1.QuestionServiceGetRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: other.GetId()}},
	}); err != nil || got.GetWishId() != w2 {
		t.Errorf("question get by identifier = %v, %v", got, err)
	}
	if _, err := get(&planv1.QuestionServiceGetRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q09"}}, WishId: w1,
	}); code(err) != connect.CodeNotFound {
		t.Errorf("an unknown code = %v, want not_found", err)
	}
}

// putWishes writes wishes of these titles in w's store, and returns their identifiers.
func putWishes(t *testing.T, w *Wishes, titles ...string) []string {
	t.Helper()
	var ids []string
	err := w.Store.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/wishes", &planv1.WishServiceListRequest{}); err != nil {
			return err
		}
		for _, title := range titles {
			wish := &planv1.Wish{Id: store.NewID(), Title: title, State: planv1.WishState_WISH_STATE_ACTIVE}
			ids = append(ids, wish.GetId())
			if err := tx.Put(wish); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// TestDigestNamesTheWish: every line of a digest names its wish beside its code, one wish or several, since codes
// repeat across wishes (W74 was in two of them), and the digest ends with the wishes' identifiers.
func TestDigestNamesTheWish(t *testing.T) {
	w, s, _ := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
	ids := putWishes(t, w, "Djinn QoL", "Release")
	for _, id := range ids {
		if err := w.Notify(t.Context(), &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED,
			WishId: id, Code: "W74", Text: "Djinn: W74 done: it works."}); err != nil {
			t.Fatal(err)
		}
	}
	notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_RESTART, "", "Djinn: restarted on v1.")
	got := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{Timeout: durationpb.New(0)})
	for _, want := range []string{
		"\n- W74 [Djinn QoL] ended: done: it works.\n", "\n- W74 [Release] ended: done: it works.\n",
		"\n- restart: restarted on v1.\n",
		"\nWishes: [Djinn QoL] " + ids[0] + ", [Release] " + ids[1] + ".\n",
		"djinn question get <question> --wish-id <wish>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the digest lacks %q:\n%s", want, got)
		}
	}

	if err := w.Notify(t.Context(), &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_MERGED,
		WishId: ids[0], Code: "W76", Text: "Djinn: W76 merged."}); err != nil {
		t.Fatal(err)
	}
	one := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{After: cursorOf(t, got), Timeout: durationpb.New(0)})
	if !strings.Contains(one, "\n- W76 [Djinn QoL] merged: merged.\n") {
		t.Errorf("a digest of one wish does not name it:\n%s", one)
	}
}

// TestDigestCarriesTheNote: the final note of a task that ended, or the note of an answer, comes whole under its
// line, where the line keeps 160 characters; past a screen or two, the digest names the command that prints it all,
// and that command does.
func TestDigestCarriesTheNote(t *testing.T) {
	w, s, _ := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
	wish := putWishes(t, w, "Djinn QoL")[0]
	note := "Added djinn question get.\n\nLeft to verify:\n- the Windows build"
	queue := func(code, text, note string) {
		t.Helper()
		if err := w.Notify(t.Context(), &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED,
			WishId: wish, Code: code, Text: text, Note: note}); err != nil {
			t.Fatal(err)
		}
	}
	queue("W01", "Djinn: W01 done: Added djinn question get.", note)
	queue("W02", "Djinn: W02 done: fine.", "fine.")
	long := strings.Repeat("A line of a long final note, one of many.\n", 100)
	queue("W03", "Djinn: W03 done: A line of a long final note, one of many.", long)
	got := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{Timeout: durationpb.New(0)})

	whole := "- W01 [Djinn QoL] ended: done: Added djinn question get.\n  Note:\n    Added djinn question get.\n" +
		"    Left to verify:\n    - the Windows build\n- W02"
	if !strings.Contains(got, whole) {
		t.Errorf("the digest lacks the whole note under its line:\n%s", got)
	}
	if !strings.Contains(got, "- W02 [Djinn QoL] ended: done: fine.\n- W03") {
		t.Errorf("a note the line holds comes again:\n%s", got)
	}
	_, w03, _ := strings.Cut(got, "- W03")
	if n := len([]rune(w03)); n > digestNoteMax+800 {
		t.Errorf("the long note takes %d characters of the digest:\n%s", n, got)
	}
	all, err := store.List[*planv1.LeadEvent](t.Context(), w.Store, nil)
	events := slices.DeleteFunc(all, func(e *planv1.LeadEvent) bool { return e.GetCode() != "W03" })
	if err != nil || len(events) != 1 {
		t.Fatalf("events of W03 = %v, %v", events, err)
	}
	command := "djinn lead event " + events[0].GetId() + " prints it whole."
	if !strings.Contains(w03, command) || !strings.Contains(w03, "The note goes on for ") {
		t.Errorf("the cut note does not name %q:\n%s", command, w03)
	}
	res, err := s.Event(t.Context(), connect.NewRequest(&planv1.LeadServiceEventRequest{Id: events[0].GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	if text := res.Msg.GetText(); strings.Count(text, "> A line of a long final note, one of many.") != 100 ||
		!strings.Contains(text, "### W03 ended · wish Djinn QoL (`"+wish+"`)") {
		t.Errorf("djinn lead event = %s", text)
	}
	if _, err := s.Event(context.Background(), connect.NewRequest(&planv1.LeadServiceEventRequest{Id: store.NewID()})); code(err) != connect.CodeNotFound {
		t.Errorf("an unknown event = %v, want not_found", err)
	}
}

// TestPacketCarriesTheNote: a driven lead's batch carries an event's note whole, apart from its line, once.
func TestPacketCarriesTheNote(t *testing.T) {
	e := &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER, Code: "Q03", WishId: "w1",
		Text: `Djinn: Q03 answered A. Note: "Yes, but…".`, Note: "Yes, but\nonly on Linux."}
	got := eventEntry(e, map[string]string{"w1": "Djinn QoL"})
	if !strings.Contains(got, "The note, whole:\n\n> Yes, but\n> only on Linux.\n") {
		t.Errorf("entry = %s", got)
	}
	e.Note = "Yes, but"
	if got := eventEntry(e, nil); strings.Contains(got, "The note, whole") {
		t.Errorf("a note its text holds comes twice: %s", got)
	}
}
