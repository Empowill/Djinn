package plan

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// told records what Nudges tells the leads, by wish.
type told struct {
	mu   sync.Mutex
	got  map[string][]string
	sent chan struct{}
}

func (r *told) tell(wishID, text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got[wishID] = append(r.got[wishID], text)
	r.sent <- struct{}{}
}

// wait returns what the lead of wishID was told once n messages went, to all wishes.
func (r *told) wait(t *testing.T, n int, wishID string) []string {
	t.Helper()
	for range n {
		select {
		case <-r.sent:
		case <-time.After(5 * time.Second):
			t.Fatalf("%d messages expected; told %v", n, r.got)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got[wishID])
}

// TestNudges: the answers, approvals and task ends of a wish that come within a group reach its lead as one
// message; another wish's lead hears of its own; a read mark, a removed approval, a task already ended or one that
// only starts say nothing.
func TestNudges(t *testing.T) {
	ctx := t.Context()
	rec := &told{got: map[string][]string{}, sent: make(chan struct{}, 16)}
	nudges := &Nudges{Tell: rec.tell, Group: 200 * time.Millisecond}
	c := serve(t, WithNudges(nudges))
	wish, other := c.wish(t), c.wish(t)
	put := func(tasks ...*planv1.Task) {
		t.Helper()
		if err := c.store.Tx(ctx, func(tx *store.Tx) error {
			for _, task := range tasks {
				if err := tx.Journal(actor, "test/put", task); err != nil {
					return err
				}
				if err := tx.Put(task); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	task := func(code string, status planv1.TaskStatus) *planv1.Task {
		return &planv1.Task{Id: store.NewID(), WishId: wish, Code: code, Status: status, CreateTime: timestamppb.Now()}
	}
	old, w1, w2, w3 := task("W0", planv1.TaskStatus_TASK_STATUS_DONE), task("W1", planv1.TaskStatus_TASK_STATUS_RUNNING),
		task("W2", planv1.TaskStatus_TASK_STATUS_RUNNING), task("W3", planv1.TaskStatus_TASK_STATUS_PENDING)
	put(old, w1, w2)
	if err := nudges.Follow(ctx, c.store); err != nil {
		t.Fatal(err)
	}
	put(w3) // New: a task seen first says nothing, whatever its status.

	q1 := c.ask(t, wish, "Olive", "Paraffin")
	q2, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Ship?", WishId: wish, Recommendation: "Yes, today.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	q3 := c.ask(t, other, "sqlite", "files")
	block, err := c.blocks.Put(ctx, connect.NewRequest(&planv1.BlockServicePutRequest{
		WishId: wish, Kind: "decision", Title: "Ship\non Friday", Content: "Unless it rains.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	mark := func(ref *planv1.MarkTarget, kind planv1.MarkKind, remove bool) {
		t.Helper()
		if _, err := c.marks.Put(ctx, connect.NewRequest(&planv1.MarkServicePutRequest{
			Target: ref, Kind: kind, Remove: remove, WishId: wish,
		})); err != nil {
			t.Fatal(err)
		}
	}
	answer := func(q *planv1.Question, choice planv1.Choice, note string) {
		t.Helper()
		if _, err := c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
			Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q.GetId()}}, Choice: choice, Note: note,
		})); err != nil {
			t.Fatal(err)
		}
	}
	blockRef := &planv1.MarkTarget{Ref: &planv1.MarkTarget_Id{Id: block.Msg.GetBlock().GetId()}}

	answer(q1, planv1.Choice_CHOICE_B, "It burns\nbrighter.")
	mark(&planv1.MarkTarget{Ref: &planv1.MarkTarget_Code{Code: q2.Msg.GetQuestion().GetCode()}},
		planv1.MarkKind_MARK_KIND_APPROVED, false) // Answers Q02 with its recommendation: an answer, said once.
	mark(blockRef, planv1.MarkKind_MARK_KIND_READ, false)
	mark(blockRef, planv1.MarkKind_MARK_KIND_APPROVED, false)
	mark(blockRef, planv1.MarkKind_MARK_KIND_APPROVED, true)
	answer(q3, planv1.Choice_CHOICE_A, "")
	w1.Status = planv1.TaskStatus_TASK_STATUS_DONE
	w2.Status, w2.Error = planv1.TaskStatus_TASK_STATUS_FAILED, "exit code 1"
	old.Title = "Renamed, still done"
	put(w1, w2, old)
	put(w1) // Still done: said once.

	got := rec.wait(t, 2, wish)
	want := `Q01 answered: B (note: "It burns brighter."). Q02 answered: yes. ` +
		`Block ` + blockRef.GetId() + ` "Ship on Friday" approved. W1 ended (done). W2 ended (failed: exit code 1). Continue.`
	if len(got) != 1 || got[0] != want {
		t.Fatalf("the lead was told %q;\nwant one message %q", got, want)
	}
	if got := rec.wait(t, 0, other); len(got) != 1 || got[0] != "Q01 answered: A. Continue." {
		t.Fatalf("the other lead was told %q", got)
	}

	// Later news goes in a message of its own: the approval of a question already answered, a task that waits.
	mark(&planv1.MarkTarget{Ref: &planv1.MarkTarget_Code{Code: "Q01"}}, planv1.MarkKind_MARK_KIND_APPROVED, false)
	w3.Status, w3.Error = planv1.TaskStatus_TASK_STATUS_WAITING, "waiting for the answer to its edit question"
	put(w3)
	got = rec.wait(t, 1, wish)
	if len(got) != 2 || got[1] != "Q01 approved. W3 is waiting (waiting for the answer to its edit question). Continue." {
		t.Fatalf("the lead was told %q", got)
	}
	select {
	case <-rec.sent:
		t.Fatalf("told more: %v", rec.got)
	case <-time.After(300 * time.Millisecond):
	}
	if strings.Contains(strings.Join(got, ""), "\n") {
		t.Fatal("a message is one line")
	}
}
