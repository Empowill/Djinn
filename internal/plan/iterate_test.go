package plan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// TestParsePlanRounds (W268): a mockup plan gives how many versions of its tilasm the developer reviews; rounds are a
// mockup's, from 1 to 9.
func TestParsePlanRounds(t *testing.T) {
	t.Parallel()
	p, err := ParsePlan("option: A\nkind: mockup\nrounds: 4\ntitle: Next version\ntilasm: L18\n\nRework it.")
	if err != nil {
		t.Fatal(err)
	}
	if !IsMockupPlan(p) || p.GetRounds() != 4 || MockupRounds(p) != 4 {
		t.Errorf("parsed %v", p)
	}
	if MockupRounds(&planv1.PlannedTask{Kind: planv1.TaskKind_TASK_KIND_MOCKUP}) != DefaultMockupRounds {
		t.Errorf("no rounds: %d, want %d", MockupRounds(&planv1.PlannedTask{}), DefaultMockupRounds)
	}
	for _, text := range []string{"rounds: 0", "rounds: ten", "rounds: 12"} {
		if _, err := ParsePlan(text); err == nil || !strings.Contains(err.Error(), "is not a number from 1 to 9") {
			t.Errorf("%q: %v", text, err)
		}
	}
	c := serve(t)
	wish := c.wish(t)
	_, err = c.askPlans(t, &planv1.QuestionServiceAskRequest{WishId: wish, Options: []string{"Build"},
		Plan: []string{"option: A\nrounds: 2\ntitle: Build it"}})
	if err == nil || !strings.Contains(err.Error(), "gives rounds, which are a mockup's") {
		t.Errorf("rounds on work: %v", err)
	}
}

// iterateFixture is a wish with a tilasm, L01, a question linking it whose A plans its mockup and B its build, and
// the hooks that see what an answer does.
type iterateFixture struct {
	sendFixture
	q        *planv1.Question
	mu       sync.Mutex
	rounds   []*planv1.Round // each iteration the hook saw
	answered int             // the answers the answer hooks saw
	code     string          // what the iteration hook says it started
}

func newIterateFixture(t *testing.T, code string) *iterateFixture {
	t.Helper()
	f := &iterateFixture{code: code}
	f.sendFixture = newSendFixture(t, WithLeads(&fakeLeads{}),
		WithIterated(func(_ context.Context, _ *planv1.Question, r *planv1.Round) string {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.rounds = append(f.rounds, r)
			return f.code
		}),
		WithAnswered(func(context.Context, *planv1.Question) string {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.answered++
			return ""
		}))
	res, err := f.c.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
		WishId: f.wish, Text: "Do the typed options suit you?", Options: []string{"Iterate", "Build", "Other"},
		Context: "The design: " + TilasmLink(f.tilasm.GetId()),
		Plan: []string{"option: A\nkind: mockup\ntitle: Next version of L01\ntilasm: L01\n\nRework L01.",
			"option: B\nkind: work\ntitle: Build L01\ntilasm: L01\n\nBuild the approved version."},
	}))
	if err != nil {
		t.Fatal(err)
	}
	f.q = res.Msg.GetQuestion()
	return f
}

func (f *iterateFixture) answer(t *testing.T, choice planv1.Choice, note string) *planv1.Question {
	t.Helper()
	res, err := f.c.questions.Answer(t.Context(), connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: f.q.GetCode()}}, WishId: f.wish,
		Choice: choice, Note: note,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetQuestion()
}

func (f *iterateFixture) revise(t *testing.T) {
	t.Helper()
	if _, err := f.c.questions.Revise(t.Context(), connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: f.q.GetCode()}}, WishId: f.wish,
		Context: "Version next: " + TilasmLink(f.tilasm.GetId()),
	})); err != nil {
		t.Fatal(err)
	}
}

// TestAnswerIterates (W268, T55): answering the option whose plan is a mockup stores an iteration round; the build
// option stores an answer; comments sent to iterate start the question's iteration round.
func TestAnswerIterates(t *testing.T) {
	t.Parallel()

	t.Run("answer iterates until last round", func(t *testing.T) {
		t.Parallel()
		f := newIterateFixture(t, "")
		stored, err := store.Get[*planv1.Question](t.Context(), f.c.store, f.q.GetId())
		if err != nil {
			t.Fatal(err)
		}
		q := f.answer(t, planv1.Choice_CHOICE_A, "bigger icons")
		if q.GetAnswer() != nil {
			t.Fatalf("answer stored: %v", q.GetAnswer())
		}
		round := Iterating(q)
		if round == nil || round.GetNote() != "bigger icons" || round.GetOption() != planv1.Choice_CHOICE_A ||
			round.GetTilasmId() != f.tilasm.GetId() || round.GetVersion() != 1 || round.GetContext() != stored.GetContext() {
			t.Fatalf("rounds = %v", q.GetRounds())
		}
		if !Investigating(q) || Tour(q) != 2 || f.answered != 0 || len(f.rounds) != 1 {
			t.Errorf("investigating %v, tour %d, answers %d, iterations %d", Investigating(q), Tour(q), f.answered, len(f.rounds))
		}
		// No mockup task started: the lead is told how to spawn one.
		events, err := store.List[*planv1.LeadEvent](t.Context(), f.c.store, store.Where{"wish_id": f.wish})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 || !strings.Contains(events[0].GetText(), "No mockup task started: spawn one with djinn task spawn "+
			f.wish+" --kind mockup --question "+q.GetCode()+" --tilasm L01") || events[0].GetNote() != "bigger icons" {
			t.Errorf("lead events: %v", events)
		}
		// The mockup task revises; the developer iterates once more, then at the last version the answer is one.
		f.revise(t)
		f.code = "W9"
		if q := f.answer(t, planv1.Choice_CHOICE_A, ""); q.GetAnswer() != nil || Tour(q) != 3 {
			t.Fatalf("second iteration: answer %v, tour %d", q.GetAnswer(), Tour(q))
		}
		f.revise(t)
		q = f.answer(t, planv1.Choice_CHOICE_A, "still not it")
		if q.GetAnswer().GetChoice() != planv1.Choice_CHOICE_A || f.answered != 1 || len(f.rounds) != 2 {
			t.Errorf("past the rounds: answer %v, answers %d, iterations %d", q.GetAnswer(), f.answered, len(f.rounds))
		}
	})

	t.Run("build option is an answer", func(t *testing.T) {
		t.Parallel()
		f := newIterateFixture(t, "W9")
		if q := f.answer(t, planv1.Choice_CHOICE_B, ""); q.GetAnswer().GetChoice() != planv1.Choice_CHOICE_B || len(f.rounds) != 0 ||
			f.answered != 1 {
			t.Errorf("build: answer %v, iterations %d, answers %d", q.GetAnswer(), len(f.rounds), f.answered)
		}
	})

	t.Run("send iterates", func(t *testing.T) {
		t.Parallel()
		f := newIterateFixture(t, "W9")
		f.draft(t, "Bigger", &planv1.TilasmAnchor{Exact: "Notices group."})
		res, err := f.send(t, planv1.SendChoice_SEND_CHOICE_ITERATE, "and darker")
		if err != nil {
			t.Fatal(err)
		}
		if res.GetIterated() != f.q.GetCode() || res.GetIterate() != "W9" || len(res.GetComments()) != 2 {
			t.Errorf("send: %v", res)
		}
		if len(f.rounds) != 1 || f.rounds[0].GetSend() != res.GetSend().GetNumber() || f.rounds[0].GetTilasmId() != f.tilasm.GetId() {
			t.Errorf("iterations: %v", f.rounds)
		}
		if events := commentEvents(t, f.c.store); len(events) != 0 {
			t.Errorf("comments events: %v", events)
		}
		task := f.taskOn(t, "W9", "Rework L01: djinn tilasm put mock --code L01", planv1.TaskStatus_TASK_STATUS_RUNNING)
		task.Kind = planv1.TaskKind_TASK_KIND_MOCKUP
		putTasks(t, f.c.store, task)
		if h := f.hold(t); h.GetReason() != planv1.TilasmHoldReason_TILASM_HOLD_REASON_REWORK || h.GetCode() != "W9" {
			t.Errorf("hold = %v, want W9 reworking L01", h)
		}
	})
}

// TestPinnedTilasm (W268): a tilasm named with a version, L01@1, gives that version to a worker and to a plan, the
// later ones aside; a version it lacks is refused.
func TestPinnedTilasm(t *testing.T) {
	t.Parallel()
	f := newSendFixture(t)
	home := f.c.tilasmHome(t)
	next := folder(t, map[string]string{"index.html": page("Notifications, second", "Notices apart.")})
	putTilasm(t, f.c, &planv1.TilasmServicePutRequest{Path: next, Wish: f.wish, Code: "L01"})
	for name, want := range map[string]string{"L01@1": "Notices group.", "l01@v1": "Notices group.", "L01": "Notices apart."} {
		text, err := TilasmContext(t.Context(), f.c.store, home, f.wish, []string{name})
		if err != nil || !strings.Contains(text, want) {
			t.Errorf("%s: %v\n%s", name, err, text)
		}
	}
	if _, err := TilasmContext(t.Context(), f.c.store, home, f.wish, []string{"L01@9"}); err == nil ||
		!strings.Contains(err.Error(), "has no version 9") {
		t.Errorf("L01@9: %v", err)
	}
	q, err := f.c.askPlans(t, &planv1.QuestionServiceAskRequest{WishId: f.wish, Options: []string{"Build"},
		Plan: []string{"option: A\ntitle: Build it\ntilasm: L01@1"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := q.GetPlans()[0].GetTilasm(); len(got) != 1 || got[0] != f.tilasm.GetId()+"@1" {
		t.Errorf("plan tilasm = %v", got)
	}
}

// tilasmHome is the data folder the services keep the tilasms in: where the first tilasm of a fixture lies.
func (c clients) tilasmHome(t *testing.T) string {
	t.Helper()
	tilasms, err := store.List[*planv1.Tilasm](t.Context(), c.store, nil)
	if err != nil || len(tilasms) == 0 {
		t.Fatal(tilasms, err)
	}
	res, err := c.tilasms.Get(t.Context(), connect.NewRequest(&planv1.TilasmServiceGetRequest{
		Tilasm: &planv1.TilasmRef{Ref: &planv1.TilasmRef_Id{Id: tilasms[0].GetId()}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	dir := res.Msg.GetDirectory() // <home>/tilasms/<id>/v<n>
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(dir)))
}
