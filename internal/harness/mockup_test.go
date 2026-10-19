package harness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// mockupEnv is a wish on a project in Git with a tilasm, L01, and a question linking it whose A plans its mockup and
// B its build, served with Djinn's data folder for the tilasms (W268).
type mockupEnv struct {
	*env
	wishID    string
	told      *toldLines
	questions planv1connect.QuestionServiceClient
	tilasms   planv1connect.TilasmServiceClient
	tilasm    *planv1.Tilasm
	q         *planv1.Question
}

func newMockupEnv(t *testing.T, opts ...Option) *mockupEnv {
	t.Helper()
	m := &mockupEnv{told: &toldLines{}}
	m.env = up(t, t.TempDir(), append([]Option{WithTellDelay(10 * time.Millisecond)}, opts...)...)
	m.h.TellLeads(m.told.tell)
	m.wishID, _ = m.wish(t, gitRepoWith(t, nil))
	answered := func(ctx context.Context, q *planv1.Question) string {
		did := m.h.Answered(ctx, q)
		m.mu.Lock()
		m.did = append(m.did, did)
		m.mu.Unlock()
		return did
	}
	mux := http.NewServeMux()
	for prefix, handler := range plan.Handlers(m.db, plan.WithHome(m.home), plan.WithAnswered(answered),
		plan.WithIterated(m.h.Iterated), plan.WithWorkers(m.h), plan.WithLaunch(m.h.LaunchPlan)) {
		mux.Handle(prefix, handler)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	m.questions = planv1connect.NewQuestionServiceClient(srv.Client(), srv.URL)
	m.tilasms = planv1connect.NewTilasmServiceClient(srv.Client(), srv.URL)
	m.tilasm = m.put(t, "Notices group.", "")
	res, err := m.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
		WishId: m.wishID, Text: "Do the typed options suit you?", Options: []string{"Iterate", "Build", "Other"},
		Context: "The design: " + plan.TilasmLink(m.tilasm.GetId()),
		Plan: []string{"option: A\nkind: mockup\nprovider: fake\ntitle: Next version of L01\ntilasm: L01\n\nwait\ntext mocked",
			"option: B\nprovider: fake\ntitle: Build L01\ntilasm: L01\n\ntext built"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	m.q = res.Msg.GetQuestion()
	return m
}

// put puts a version of L01 that says text, by author; the first one without code.
func (m *mockupEnv) put(t *testing.T, text, author string) *planv1.Tilasm {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "index.html", "<!doctype html><html><head><title>Notices</title></head><body><p>"+text+"</p></body></html>")
	req := &planv1.TilasmServicePutRequest{Path: dir, Wish: m.wishID, Author: author}
	if m.tilasm != nil {
		req.Code = m.tilasm.GetCode()
	}
	res, err := m.tilasms.Put(t.Context(), connect.NewRequest(req))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetTilasm()
}

func (m *mockupEnv) answer(t *testing.T, choice planv1.Choice, note string) *planv1.Question {
	t.Helper()
	res, err := m.questions.Answer(t.Context(), connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: m.q.GetId()}}, Choice: choice, Note: note,
		By: planv1.Closer_CLOSER_DEVELOPER,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetQuestion()
}

// revise revises the question as the mockup task's worker does.
func (m *mockupEnv) revise(t *testing.T, by *planv1.Task) {
	t.Helper()
	if _, err := m.questions.Revise(t.Context(), connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: m.q.GetId()}}, TaskId: by.GetId(),
		Context: "Next version: " + plan.TilasmLink(m.tilasm.GetId()),
	})); err != nil {
		t.Fatal(err)
	}
}

// mockupOf is the mockup task the last iteration round of the question names.
func (m *mockupEnv) mockupOf(t *testing.T, q *planv1.Question) *planv1.Task {
	t.Helper()
	round := plan.Iterating(q)
	if round == nil || round.GetTaskId() == "" {
		t.Fatalf("rounds %v; want an iteration that names its mockup task", q.GetRounds())
	}
	return m.get(t, round.GetTaskId())
}

// TestMockup verifies mockup iteration on a question, ending without revision, spawn rules,
// bypassing branch holds, and resuming stored iterations on djinn up (W268).
func TestMockup(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("iterate_starts_mockup", func(t *testing.T) {
		t.Parallel()
		m := newMockupEnv(t)
		q := m.answer(t, planv1.Choice_CHOICE_A, "bigger icons")
		if q.GetAnswer() != nil || !plan.Investigating(q) {
			t.Fatalf("answer %v, investigating %v; want an iteration", q.GetAnswer(), plan.Investigating(q))
		}
		mock := m.until(t, m.mockupOf(t, q).GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		if mock.GetKind() != planv1.TaskKind_TASK_KIND_MOCKUP || mock.GetQuestion() != q.GetCode() ||
			mock.GetMaxBudgetUsd() != DefaultInvestigationBudgetUSD || mock.GetWorktree() == "" {
			t.Errorf("mockup task %v", mock)
		}
		prompt, err := lastPrompt(t.Context(), m.db, mock.GetId())
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"You are the mockup task of question " + q.GetCode(), "you make version 2 of 3, from L01 v1",
			"bigger icons", "--code L01", "Files, version 1", "djinn question revise " + q.GetCode()} {
			if !strings.Contains(prompt, want) {
				t.Errorf("prompt lacks %q:\n%s", want, prompt)
			}
		}

		// Its tilasm is held: comments wait for the next version.
		if _, err := m.tilasms.PutComment(t.Context(), connect.NewRequest(&planv1.TilasmServicePutCommentRequest{
			TilasmId: m.tilasm.GetId(), Text: "darker",
		})); err != nil {
			t.Fatal(err)
		}
		if _, err := m.tilasms.SendComments(t.Context(), connect.NewRequest(&planv1.TilasmServiceSendCommentsRequest{
			TilasmId: m.tilasm.GetId(), Choice: planv1.SendChoice_SEND_CHOICE_ITERATE,
		})); err == nil || !strings.Contains(err.Error(), mock.GetCode()+" puts the next version of L01") {
			t.Errorf("a send while %s works: %v", mock.GetCode(), err)
		}

		// It puts v2 and revises; done, nothing merges, and the lead reads what it did.
		m.put(t, "Notices apart.", mock.GetCode())
		m.revise(t, mock)
		m.release(t, mock.GetId())
		if done := m.ended(t, mock.GetId()); done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || done.GetIntegration() != nil {
			t.Fatalf("mockup ended %v, integration %v", done.GetStatus(), done.GetIntegration())
		}
		want := "put L01 v2; revised " + q.GetCode() + ": it waits for the developer again."
		waitFor(t, "the mockup's end line", func() bool { m.h.FlushTell(t.Context()); return m.told.with(want) != "" })
		if line := m.told.with(mock.GetCode() + " done"); line != "" {
			t.Errorf("a done line beside the mockup's: %q", line)
		}

		// Build: launched on v2, approved; a later version does not reach it.
		q = m.answer(t, planv1.Choice_CHOICE_B, "")
		m.put(t, "Notices later.", "")
		if did := m.lastDid(); !strings.Contains(did, "launched as planned, L01 v2 approved") {
			t.Errorf("did %q", did)
		}
		build := plan.ChosenPlan(q)
		if got := build.GetTilasm(); len(got) != 1 || got[0] != m.tilasm.GetId()+"@2" {
			t.Fatalf("launched plan tilasm %v; want L01 pinned at v2", got)
		}
		if prompt, err := lastPrompt(t.Context(), m.db, build.GetTaskId()); err != nil || !strings.Contains(prompt, "Files, version 2") {
			t.Errorf("build prompt: %v\n%s", err, prompt)
		}
	})

	t.Run("end_without_revision", func(t *testing.T) {
		t.Parallel()
		m := newMockupEnv(t)
		q := m.answer(t, planv1.Choice_CHOICE_A, "")
		mock := m.until(t, m.mockupOf(t, q).GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		m.release(t, mock.GetId())
		m.ended(t, mock.GetId())
		want := q.GetCode() + " still waits for its revision: djinn task get " + mock.GetId()
		waitFor(t, "the mockup's end line", func() bool { m.h.FlushTell(t.Context()); return m.told.with(want) != "" })
	})

	t.Run("spawn", func(t *testing.T) {
		t.Parallel()
		m := newMockupEnv(t)
		mockup := func(req *planv1.TaskServiceSpawnRequest) *planv1.TaskServiceSpawnRequest {
			req.Kind = planv1.TaskKind_TASK_KIND_MOCKUP
			return req
		}
		task := m.mustSpawn(t, m.wishID, "Mock the panel", "text mocked",
			mockup(&planv1.TaskServiceSpawnRequest{Question: strings.ToLower(m.q.GetCode()), Feedback: true}))
		if task.GetKind() != planv1.TaskKind_TASK_KIND_MOCKUP || task.GetQuestion() != m.q.GetCode() || !task.GetFeedback() ||
			task.GetInvestigation() == nil || task.GetMaxBudgetUsd() != DefaultInvestigationBudgetUSD {
			t.Errorf("mockup %v", task)
		}
		for why, req := range map[string]*planv1.TaskServiceSpawnRequest{
			"--question is for a mockup":     {Question: m.q.GetCode()},
			"the wish has no question Q99":   mockup(&planv1.TaskServiceSpawnRequest{Question: "Q99"}),
			"a mockup is an agent":           mockup(&planv1.TaskServiceSpawnRequest{Provider: planv1.Provider_PROVIDER_WATCH}),
			"--minutes is for an investigat": {Minutes: 10},
		} {
			if _, err := m.spawnReq(t, m.wishID, "Mock", "text x", req); err == nil || !strings.Contains(err.Error(), why) {
				t.Errorf("%s: %v", why, err)
			}
		}
		outside, _ := m.wish(t, t.TempDir())
		if _, err := m.spawnReq(t, outside, "Mock", "text x", mockup(&planv1.TaskServiceSpawnRequest{})); err == nil ||
			!strings.Contains(err.Error(), "a mockup works in a worktree of a project in Git") {
			t.Errorf("outside Git: %v", err)
		}
	})

	t.Run("not_held", func(t *testing.T) {
		t.Parallel()
		for name, hold := range map[string]func(*testing.T, *integration) string{
			"red branch": func(t *testing.T, in *integration) string {
				in.redAgain(t, in.tip(t))
				return "to be green again"
			},
			"conflict hold": func(t *testing.T, in *integration) string {
				in.settleReadme(t)
				in.finished(t, "W1", map[string]string{"app/README.md": "# One\n"})
				w2 := in.finished(t, "W2", map[string]string{"app/README.md": "# Two\n"})
				in.pass(t, 0)
				got, _ := in.integration(t, w2)
				in.until(t, got.GetCorrectedBy(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
				return "to settle the conflict in " + in.branch
			},
			"repair running": func(t *testing.T, in *integration) string {
				in.checking(t, lintAtCommit, map[string]int{"lint": 1})
				in.correctWith(func(_, prompt string) string {
					if strings.Contains(prompt, "the work of W1") {
						return "wait\ntext fixed"
					}
					return ""
				})
				w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
				in.pass(t, 0)
				got, _ := in.integration(t, w1)
				in.until(t, got.GetCorrectedBy(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
				return "to repair the red checks of " + in.branch
			},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				in := integrating(t)
				want := hold(t, in)
				in.pass(t, time.Second)
				work := in.mustSpawn(t, in.wishID, "Work", "text work", nil)
				if got := in.get(t, work.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING ||
					!strings.Contains(got.GetWaitReason(), want) {
					t.Fatalf("work %v; want it held %q", got, want)
				}
				mock := in.mustSpawn(t, in.wishID, "Mock", "text mocked",
					&planv1.TaskServiceSpawnRequest{Kind: planv1.TaskKind_TASK_KIND_MOCKUP})
				if got := in.ended(t, mock.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
					t.Errorf("mockup %v while the branch is held; want it run", got)
				}
				if !slices.ContainsFunc(in.storedEvents(t, mock.GetId()), func(ev *planv1.TaskEvent) bool {
					return ev.GetText() == "integration: none: "+mockupLeft
				}) {
					t.Error("no event says its integration is none")
				}
				in.pass(t, time.Second)
				if got := in.get(t, work.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
					t.Errorf("work %v after the mockup; want it still held", got.GetStatus())
				}
			})
		}
	})

	t.Run("resume_iteration", func(t *testing.T) {
		t.Parallel()
		m := newMockupEnv(t)
		err := m.db.Tx(t.Context(), func(tx *store.Tx) error {
			q, err := store.Get[*planv1.Question](t.Context(), tx, m.q.GetId())
			if err != nil {
				return err
			}
			q.Rounds = append(q.Rounds, &planv1.Round{Kind: planv1.RoundKind_ROUND_KIND_ITERATE, Option: planv1.Choice_CHOICE_A,
				TilasmId: m.tilasm.GetId(), Version: 1, Note: "bigger icons", CreateTime: timestamppb.Now()})
			if err := tx.Journal("test", "test/iterate", q); err != nil {
				return err
			}
			return tx.Put(q)
		})
		if err != nil {
			t.Fatal(err)
		}
		m.h.resumeInvestigations(t.Context())
		q, err := store.Get[*planv1.Question](t.Context(), m.db, m.q.GetId())
		if err != nil {
			t.Fatal(err)
		}
		if mock := m.mockupOf(t, q); mock.GetKind() != planv1.TaskKind_TASK_KIND_MOCKUP || mock.GetQuestion() != q.GetCode() {
			t.Errorf("resumed mockup %v", mock)
		}
		// Followed up, it is not started twice.
		m.h.resumeInvestigations(t.Context())
		tasks, err := store.List[*planv1.Task](t.Context(), m.db, store.Where{"wish_id": m.wishID})
		if err != nil || len(tasks) != 1 {
			t.Errorf("tasks after a second follow-up: %d, %v", len(tasks), err)
		}
	})
}

// TestMockupLines (W268): a mockup task is asked to publish its next version and its revision; its end line warns
// when it revised its question without a new version of the mockup.
func TestMockupLines(t *testing.T) {
	t.Parallel()
	mock := &planv1.Task{Id: "m", Code: "W9", Title: "Next version", Kind: planv1.TaskKind_TASK_KIND_MOCKUP, Question: "Q61",
		Status: planv1.TaskStatus_TASK_STATUS_DONE}
	if text := publishText(mock, "its time limit, 25m0s"); !strings.Contains(text, "your tilasm's next version, as it stands, and your revision of Q61") {
		t.Errorf("publish text %q", text)
	}
	if rule := mockupRule(mock); !strings.Contains(rule, "then revise Q61") || !strings.Contains(rule, "never the project's code") {
		t.Errorf("rule %q", rule)
	}
	q := &planv1.Question{Code: "Q61", Rounds: []*planv1.Round{{Kind: planv1.RoundKind_ROUND_KIND_REVISE, TaskId: "m"}}}
	l18 := &planv1.Tilasm{Code: "L18", Versions: []*planv1.TilasmVersion{{Number: 1, Author: "W266"}}}
	line := mockupLine(mock, nil, []*planv1.Question{q}, []*planv1.Tilasm{l18})
	if !strings.Contains(line, "revised Q61") || !strings.Contains(line, "Warning: it put no new version of its mockup") {
		t.Errorf("line %q", line)
	}
	l18.Versions = append(l18.Versions, &planv1.TilasmVersion{Number: 2, Author: "W9"})
	if line := mockupLine(mock, nil, []*planv1.Question{q}, []*planv1.Tilasm{l18}); line !=
		"Djinn: W9 (Next version) ended: put L18 v2; revised Q61: it waits for the developer again." {
		t.Errorf("line %q", line)
	}
}
