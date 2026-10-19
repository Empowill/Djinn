package plan

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// TestQuestionAbout: the lead asks about a worker's work (djinn question ask --about W12), by its code or its
// identifier; a revision changes it, keeps it when not given, and clears it given empty. A task of another wish, or
// none, is refused (T51).
func TestQuestionAbout(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := serve(t)
	wish, other := c.wish(t), c.wish(t)
	ifrit := &planv1.Task{Id: store.NewID(), WishId: wish, Code: "W12", Title: "Front of the lamp", Kind: planv1.TaskKind_TASK_KIND_IFRIT}
	work := &planv1.Task{Id: store.NewID(), WishId: wish, Code: "W7", Title: "Wick"}
	stranger := &planv1.Task{Id: store.NewID(), WishId: other, Code: "W3"}
	for _, task := range []*planv1.Task{ifrit, work, stranger} {
		if err := c.store.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal("test", "put", task); err != nil {
				return err
			}
			return tx.Put(task)
		}); err != nil {
			t.Fatal(err)
		}
	}
	stored := func(code string) *planv1.Question {
		t.Helper()
		found, err := store.List[*planv1.Question](ctx, c.store, store.Where{"wish_id": wish, "code": code})
		if err != nil || len(found) != 1 {
			t.Fatalf("question %s: %v, %v", code, found, err)
		}
		return found[0]
	}

	asked, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Keep this margin on every card?", WishId: wish, About: "w12",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := asked.Msg.GetQuestion().GetAboutTaskId(); got != ifrit.GetId() {
		t.Errorf("asked about %q, want W12's %s", got, ifrit.GetId())
	}
	if got := stored("Q01").GetAboutTaskId(); got != ifrit.GetId() {
		t.Errorf("stored about %q, want W12's %s", got, ifrit.GetId())
	}
	byID, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Thicker wick?", WishId: wish, About: work.GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := byID.Msg.GetQuestion().GetAboutTaskId(); got != work.GetId() {
		t.Errorf("asked about %q by identifier, want W7's %s", got, work.GetId())
	}
	plain, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{Text: "Ship it?", WishId: wish}))
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.Msg.GetQuestion().GetAboutTaskId(); got != "" {
		t.Errorf("a question about no task is about %q", got)
	}

	ref := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q01"}}
	revise := func(about *string) *planv1.Question {
		t.Helper()
		res, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
			Question: ref, WishId: wish, Context: "Measured.", About: about,
		}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetQuestion()
	}
	if got := revise(nil).GetAboutTaskId(); got != ifrit.GetId() {
		t.Errorf("a revision without --about: about %q, want W12's still", got)
	}
	if got := revise(proto.String("W7")).GetAboutTaskId(); got != work.GetId() {
		t.Errorf("a revision --about W7: about %q, want W7's", got)
	}
	if got := stored("Q01").GetAboutTaskId(); got != work.GetId() {
		t.Errorf("stored after the revision: about %q, want W7's", got)
	}
	if got := revise(proto.String("")).GetAboutTaskId(); got != "" {
		t.Errorf("a revision --about \"\": about %q, want none", got)
	}

	for name, about := range map[string]string{"another wish's task": stranger.GetId(), "no such task": "W99"} {
		t.Run(name, func(t *testing.T) {
			_, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{Text: "Which?", WishId: wish, About: about}))
			if code := connect.CodeOf(err); code != connect.CodeInvalidArgument && code != connect.CodeNotFound {
				t.Errorf("a question about %s: %v, want invalid_argument or not_found", name, err)
			}
			if _, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
				Question: ref, WishId: wish, Context: "Again.", About: proto.String(about),
			})); err == nil {
				t.Errorf("a revision about %s was taken", name)
			}
		})
	}
}

// TestIfritStored: an ifrit's state, timers and serves are stored as given and read back whole; an export leaves out
// what only this machine knows of its serves, their processes and folders (T51).
func TestIfritStored(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := serve(t)
	wish := c.wish(t)
	out := time.Date(2026, 10, 16, 15, 42, 0, 0, time.UTC)
	ifrit := &planv1.Task{
		Id: store.NewID(), WishId: wish, Code: "W12", Title: "Front of the lamp", Kind: planv1.TaskKind_TASK_KIND_IFRIT,
		Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		Ifrit: &planv1.TaskIfrit{
			State: planv1.IfritState_IFRIT_STATE_LISTENING, OutTime: timestamppb.New(out),
			WarnTime: timestamppb.New(out.Add(-5 * time.Minute)), IdleMinutes: 60,
			Reply: "Primary button, 8 px margin. Reload /reviews.",
			Serves: []*planv1.Serve{{
				Name: "back", Command: "task run-back", Directory: "/home/me/lamp", Gate: "stack", Stop: "task stop-back",
				Pid: 4242, State: planv1.ServeState_SERVE_STATE_RUNNING, StartTime: timestamppb.New(out.Add(-time.Hour)),
				Ports: []*planv1.Port{{Number: 8080, Label: "api", Address: "127.0.0.1", Declared: true, Open: true}},
			}},
		},
	}
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("test", "put", ifrit); err != nil {
			return err
		}
		return tx.Put(ifrit)
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get[*planv1.Task](ctx, c.store, ifrit.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got.GetIfrit(), ifrit.GetIfrit()) || got.GetKind() != planv1.TaskKind_TASK_KIND_IFRIT {
		t.Errorf("stored ifrit %v, want %v", got.GetIfrit(), ifrit.GetIfrit())
	}

	exp, _, err := collect(ctx, c.store, wish, allEvents)
	if err != nil {
		t.Fatal(err)
	}
	exp = portable(exp, newScrubber(nil))
	if len(exp.GetTasks()) != 1 {
		t.Fatalf("exported tasks %v, want the ifrit", exp.GetTasks())
	}
	serve := exp.GetTasks()[0].GetIfrit().GetServes()[0]
	if serve.GetPid() != 0 || serve.GetDirectory() != "" {
		t.Errorf("exported serve keeps pid %d and folder %q of this machine", serve.GetPid(), serve.GetDirectory())
	}
	if serve.GetCommand() != "task run-back" || serve.GetPorts()[0].GetNumber() != 8080 {
		t.Errorf("exported serve lost what it runs: %v", serve)
	}
	if exp.GetTasks()[0].GetIfrit().GetIdleMinutes() != 60 {
		t.Errorf("exported ifrit lost its idle minutes: %v", exp.GetTasks()[0].GetIfrit())
	}
	if got.GetIfrit().GetServes()[0].GetPid() != 4242 {
		t.Error("the export changed the stored serve")
	}
}
