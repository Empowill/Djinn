package plan

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

func (c clients) make(t *testing.T, title string, paused bool) (*planv1.Wish, error) {
	t.Helper()
	res, err := c.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: title, Paused: paused}))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetWish(), nil
}

func (c clients) list(t *testing.T) []*planv1.Wish {
	t.Helper()
	res, err := c.wishes.List(t.Context(), connect.NewRequest(&planv1.WishServiceListRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetWishes()
}

// put writes entities straight into the store, as the harness would.
func (c clients) put(t *testing.T, ms ...proto.Message) {
	t.Helper()
	err := c.store.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "/test", &planv1.WishServiceListRequest{}); err != nil {
			return err
		}
		for _, m := range ms {
			if err := tx.Put(m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// order is "title:rank" for each wish, as listed.
func order(wishes []*planv1.Wish) string {
	parts := make([]string, len(wishes))
	for i, w := range wishes {
		parts[i] = w.GetTitle() + ":" + strings.TrimPrefix(w.GetState().String(), "WISH_STATE_")
		if w.GetRank() > 0 {
			parts[i] += string(rune('0' + w.GetRank()))
		}
	}
	return strings.Join(parts, " ")
}

// workers records what the wishes ask of their workers.
type workers struct {
	mu      sync.Mutex
	shelved []string
	stopped []string
	wakes   int
}

func (w *workers) Shelve(_ context.Context, wishID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.shelved = append(w.shelved, wishID)
}

func (w *workers) Wake() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.wakes++
}

func (w *workers) StopWish(_ context.Context, wishID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = append(w.stopped, wishID)
	return nil
}

// TestThreeWishes: there may be as many wishes as you like, and the three first are active. A fourth is made paused;
// activating one when three are active takes the third place, and pauses the third wish. A paused wish's workers
// stop.
func TestThreeWishes(t *testing.T) {
	ctx := t.Context()
	fake := &workers{}
	c := serve(t, WithWorkers(fake))
	var ids []string
	for _, title := range []string{"A", "B", "C"} {
		w, err := c.make(t, title, false)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, w.GetId())
	}
	d, err := c.make(t, "D", false)
	if err != nil || d.GetState() != planv1.WishState_WISH_STATE_PAUSED || d.GetRank() != 0 {
		t.Fatalf("a fourth wish, made paused: %v, %v", d, err)
	}
	if got := order(c.list(t)); got != "A:ACTIVE1 B:ACTIVE2 C:ACTIVE3 D:PAUSED" {
		t.Errorf("list = %s", got)
	}

	// Activating D when three are active: D takes the third place, C is paused and its workers stop.
	res, err := c.wishes.Activate(ctx, connect.NewRequest(&planv1.WishServiceActivateRequest{WishId: d.GetId()}))
	if err != nil || res.Msg.GetWish().GetRank() != 3 || len(res.Msg.GetPaused()) != 1 ||
		res.Msg.GetPaused()[0].GetId() != ids[2] {
		t.Fatalf("activate D: %v, %v", res, err)
	}
	if got := order(c.list(t)); got != "A:ACTIVE1 B:ACTIVE2 D:ACTIVE3 C:PAUSED" {
		t.Errorf("list = %s", got)
	}
	if !slices.Equal(fake.shelved, []string{ids[2]}) || fake.wakes == 0 {
		t.Errorf("shelved %v, %d wakes", fake.shelved, fake.wakes)
	}

	// Pausing B leaves its place, closes the gap in the ranks, and stops its workers.
	if _, err := c.wishes.Pause(ctx, connect.NewRequest(&planv1.WishServicePauseRequest{WishId: ids[1]})); err != nil {
		t.Fatal(err)
	}
	if got := order(c.list(t)); got != "A:ACTIVE1 D:ACTIVE2 B:PAUSED C:PAUSED" {
		t.Errorf("list = %s", got)
	}
	if !slices.Equal(fake.shelved, []string{ids[2], ids[1]}) {
		t.Errorf("shelved %v", fake.shelved)
	}
	// With a free place, an activated wish comes last, and nobody is paused.
	res, err = c.wishes.Activate(ctx, connect.NewRequest(&planv1.WishServiceActivateRequest{WishId: ids[2]}))
	if err != nil || res.Msg.GetWish().GetRank() != 3 || len(res.Msg.GetPaused()) != 0 {
		t.Fatalf("activate C: %v, %v", res, err)
	}
	// Granting A leaves its place too; a granted wish comes back with Activate.
	if _, err := c.wishes.Grant(ctx, connect.NewRequest(&planv1.WishServiceGrantRequest{WishId: ids[0]})); err != nil {
		t.Fatal(err)
	}
	if _, err := c.make(t, "E", false); err != nil {
		t.Fatal(err)
	}
	if got := order(c.list(t)); got != "D:ACTIVE1 C:ACTIVE2 E:ACTIVE3 B:PAUSED A:GRANTED" {
		t.Errorf("list = %s", got)
	}
	if _, err := c.wishes.Pause(ctx, connect.NewRequest(&planv1.WishServicePauseRequest{WishId: ids[0]})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("pause a granted wish: %v", err)
	}
	a, err := c.wishes.Activate(ctx, connect.NewRequest(&planv1.WishServiceActivateRequest{WishId: ids[0]}))
	if err != nil || a.Msg.GetWish().GetGrantTime() != nil || a.Msg.GetWish().GetRank() != 3 {
		t.Errorf("activate a granted wish: %v, %v", a, err)
	}
	if got := order(c.list(t)); got != "D:ACTIVE1 C:ACTIVE2 A:ACTIVE3 B:PAUSED E:PAUSED" {
		t.Errorf("list = %s", got)
	}
}

// TestDeleteWish: deleting a wish stops its workers, and takes its tasks, their events, its questions and its
// blocks with it; the other wishes keep theirs, and close the gap in the ranks.
func TestDeleteWish(t *testing.T) {
	ctx := t.Context()
	fake := &workers{}
	c := serve(t, WithWorkers(fake))
	a, err := c.make(t, "A", false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.make(t, "B", false)
	if err != nil {
		t.Fatal(err)
	}
	of := func(wishID string) (*planv1.Task, *planv1.TaskEvent, *planv1.Question, *planv1.Block) {
		task := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W1", Title: "work", Status: planv1.TaskStatus_TASK_STATUS_DONE,
			Worktree: "/somewhere", SessionId: "s"}
		return task, &planv1.TaskEvent{Id: store.NewID(), TaskId: task.GetId(), Seq: 1, Text: "hi"},
			&planv1.Question{Id: store.NewID(), WishId: wishID, Code: "Q01", Text: "why?"},
			&planv1.Block{Id: store.NewID(), WishId: wishID, Kind: "report", Content: "done"}
	}
	at, ae, aq, ab := of(a.GetId())
	bt, be, bq, bb := of(b.GetId())
	item := &planv1.InboxItem{Id: store.NewID(), WishId: a.GetId(), Text: "x", State: planv1.InboxState_INBOX_STATE_ROUTED}
	c.put(t, at, ae, aq, ab, bt, be, bq, bb, item)

	res, err := c.wishes.Delete(ctx, connect.NewRequest(&planv1.WishServiceDeleteRequest{WishId: a.GetId()}))
	if err != nil || res.Msg.GetTasks() != 1 || res.Msg.GetQuestions() != 1 || res.Msg.GetBlocks() != 1 {
		t.Fatalf("delete: %v, %v", res, err)
	}
	if !slices.Equal(fake.stopped, []string{a.GetId()}) || !slices.Equal(fake.shelved, []string{a.GetId()}) {
		t.Errorf("stopped %v, shelved %v", fake.stopped, fake.shelved)
	}
	gone := func(name string, get func() error) {
		t.Helper()
		if err := get(); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s still there: %v", name, err)
		}
	}
	gone("wish", func() error { _, err := store.Get[*planv1.Wish](ctx, c.store, a.GetId()); return err })
	gone("task", func() error { _, err := store.Get[*planv1.Task](ctx, c.store, at.GetId()); return err })
	gone("event", func() error { _, err := store.Get[*planv1.TaskEvent](ctx, c.store, ae.GetId()); return err })
	gone("question", func() error { _, err := store.Get[*planv1.Question](ctx, c.store, aq.GetId()); return err })
	gone("block", func() error { _, err := store.Get[*planv1.Block](ctx, c.store, ab.GetId()); return err })
	for _, m := range []string{bt.GetId(), be.GetId()} {
		if _, err := store.Get[*planv1.Task](ctx, c.store, m); m == bt.GetId() && err != nil {
			t.Errorf("B's task: %v", err)
		}
	}
	if _, err := store.Get[*planv1.TaskEvent](ctx, c.store, be.GetId()); err != nil {
		t.Errorf("B's event: %v", err)
	}
	if got, _ := store.Get[*planv1.InboxItem](ctx, c.store, item.GetId()); got.GetWishId() != "" {
		t.Errorf("the inbox item still names the wish: %v", got)
	}
	if got := order(c.list(t)); got != "B:ACTIVE1" {
		t.Errorf("list = %s", got)
	}
	if _, err := c.wishes.Delete(ctx, connect.NewRequest(&planv1.WishServiceDeleteRequest{WishId: a.GetId()})); code(err) != connect.CodeNotFound {
		t.Errorf("delete again: %v", err)
	}
}

// TestRank: the active wishes are ranked by hand, the first has priority.
func TestRank(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	var ids []string
	for _, title := range []string{"A", "B", "C"} {
		w, err := c.make(t, title, false)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, w.GetId())
	}
	move := func(id string, to int32) (string, error) {
		t.Helper()
		res, err := c.wishes.Move(ctx, connect.NewRequest(&planv1.WishServiceMoveRequest{WishId: id, To: to}))
		if err != nil {
			return "", err
		}
		return order(res.Msg.GetWishes()), nil
	}
	if got, err := move(ids[2], 1); err != nil || got != "C:ACTIVE1 A:ACTIVE2 B:ACTIVE3" {
		t.Errorf("move C to 1: %s, %v", got, err)
	}
	if got, err := move(ids[2], 9); err != nil || got != "A:ACTIVE1 B:ACTIVE2 C:ACTIVE3" {
		t.Errorf("move C beyond the last: %s, %v", got, err)
	}
	if got, err := move(ids[0], 2); err != nil || got != "B:ACTIVE1 A:ACTIVE2 C:ACTIVE3" {
		t.Errorf("move A to 2: %s, %v", got, err)
	}
	active, err := ActiveWishes(ctx, c.store)
	if err != nil || order(active) != "B:ACTIVE1 A:ACTIVE2 C:ACTIVE3" {
		t.Errorf("ActiveWishes = %s, %v", order(active), err)
	}
	if _, err := move(ids[0], 0); code(err) != connect.CodeInvalidArgument {
		t.Errorf("move to 0: %v", err)
	}
	if _, err := c.wishes.Pause(ctx, connect.NewRequest(&planv1.WishServicePauseRequest{WishId: ids[0]})); err != nil {
		t.Fatal(err)
	}
	// A paused wish moved among the three first becomes active there; the third is paused.
	if got, err := move(ids[0], 1); err != nil || got != "A:ACTIVE1 B:ACTIVE2 C:ACTIVE3" {
		t.Errorf("move a paused wish to 1: %s, %v", got, err)
	}
	if _, err := c.wishes.Pause(ctx, connect.NewRequest(&planv1.WishServicePauseRequest{WishId: ids[0]})); err != nil {
		t.Fatal(err)
	}

	// A wish stored before states and ranks is active, after the ranked ones; it counts in the three.
	legacy := &planv1.Wish{Id: store.NewID(), Title: "Old", CreateTime: timestamppb.Now()}
	c.put(t, legacy)
	if active, _ := ActiveWishes(ctx, c.store); order(active) != "B:ACTIVE1 C:ACTIVE2 Old:UNSPECIFIED" {
		t.Errorf("with an old wish: %s", order(active))
	}
	if d, err := c.make(t, "D", false); err != nil || d.GetState() != planv1.WishState_WISH_STATE_PAUSED {
		t.Errorf("a fourth beside an old wish: %v, %v", d, err)
	}
	if got, err := move(legacy.GetId(), 1); err != nil || got != "Old:ACTIVE1 B:ACTIVE2 C:ACTIVE3" {
		t.Errorf("move the old wish: %s, %v", got, err)
	}
	// D, paused, moved to the first place: active first, and C, pushed past the third place, paused.
	d := wishTitled(t, c, "D")
	res, err := c.wishes.Move(ctx, connect.NewRequest(&planv1.WishServiceMoveRequest{WishId: d.GetId(), To: 1}))
	if err != nil || order(res.Msg.GetWishes()) != "D:ACTIVE1 Old:ACTIVE2 B:ACTIVE3" || len(res.Msg.GetPaused()) != 1 ||
		res.Msg.GetPaused()[0].GetId() != ids[2] {
		t.Errorf("move D to 1: %v, %v", res, err)
	}
}

func TestReady(t *testing.T) {
	task := func(s planv1.TaskStatus) *planv1.Task { return &planv1.Task{Status: s} }
	done := task(planv1.TaskStatus_TASK_STATUS_DONE)
	open := &planv1.Question{}
	answered := &planv1.Question{Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_YES}}
	for _, tt := range []struct {
		name      string
		tasks     []*planv1.Task
		questions []*planv1.Question
		want      bool
	}{
		{"no task", nil, nil, false},
		{"all done", []*planv1.Task{done, task(planv1.TaskStatus_TASK_STATUS_STOPPED)}, []*planv1.Question{answered}, true},
		{"an open question", []*planv1.Task{done}, []*planv1.Question{answered, open}, false},
		{"a task waiting for an answer", []*planv1.Task{done, task(planv1.TaskStatus_TASK_STATUS_WAITING)}, nil, false},
		{"a failed task", []*planv1.Task{done, task(planv1.TaskStatus_TASK_STATUS_FAILED)}, nil, false},
		{"an interrupted task", []*planv1.Task{task(planv1.TaskStatus_TASK_STATUS_INTERRUPTED)}, nil, false},
		{"a running task", []*planv1.Task{task(planv1.TaskStatus_TASK_STATUS_RUNNING)}, nil, false},
		{"a planned task", []*planv1.Task{task(planv1.TaskStatus_TASK_STATUS_PENDING)}, nil, false},
		{"a task Djinn resumes", []*planv1.Task{done, task(planv1.TaskStatus_TASK_STATUS_RESUMING)}, nil, false},
		{"an interrupted task resumed as a fork, done", []*planv1.Task{
			{Id: "1", WishId: "w", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED},
			{Id: "5", WishId: "w", Code: "W5", ForkOf: "W1", Status: planv1.TaskStatus_TASK_STATUS_DONE},
		}, nil, true},
		{"an interrupted task resumed as a fork that runs", []*planv1.Task{
			{Id: "1", WishId: "w", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED},
			{Id: "5", WishId: "w", Code: "W5", ForkOf: "W1", Status: planv1.TaskStatus_TASK_STATUS_RUNNING},
		}, nil, false},
		{"an interrupted task and a fork of another", []*planv1.Task{
			{Id: "1", WishId: "w", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED},
			{Id: "5", WishId: "w", Code: "W5", ForkOf: "W2", Status: planv1.TaskStatus_TASK_STATUS_DONE},
		}, nil, false},
	} {
		if got := Ready(tt.tasks, tt.questions); got != tt.want {
			t.Errorf("%s: Ready = %v", tt.name, got)
		}
	}
}

// TestGrant: Djinn proposes a ready wish, never grants it; the user grants it, ready or not.
func TestGrant(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	w, err := c.make(t, "Ship", false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := c.make(t, "Other", false)
	if err != nil {
		t.Fatal(err)
	}
	ready := func() bool {
		t.Helper()
		i := slices.IndexFunc(c.list(t), func(l *planv1.Wish) bool { return l.GetId() == w.GetId() })
		snap, err := c.wishes.Snapshot(ctx, connect.NewRequest(&planv1.WishServiceSnapshotRequest{WishId: w.GetId()}))
		if err != nil {
			t.Fatal(err)
		}
		if listed := c.list(t)[i].GetReady(); listed != snap.Msg.GetExport().GetWish().GetReady() {
			t.Errorf("List says ready %v, Snapshot %v", listed, !listed)
		}
		return snap.Msg.GetExport().GetWish().GetReady()
	}
	if ready() {
		t.Error("a wish without task is ready")
	}
	task := &planv1.Task{Id: store.NewID(), WishId: w.GetId(), Code: "T01", Status: planv1.TaskStatus_TASK_STATUS_RUNNING}
	c.put(t, task)
	if ready() {
		t.Error("ready while a task runs")
	}
	task.Status = planv1.TaskStatus_TASK_STATUS_DONE
	c.put(t, task)
	if !ready() {
		t.Error("not ready once every task is done")
	}
	q := c.ask(t, w.GetId())
	if ready() {
		t.Error("ready with an open question")
	}
	if _, err := c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q.GetCode()}}, Choice: planv1.Choice_CHOICE_YES,
	})); err != nil {
		t.Fatal(err)
	}
	if !ready() {
		t.Error("not ready once the question is answered")
	}
	// Djinn never closes it: it stays active until the user grants it.
	if got := c.list(t)[0]; got.GetState() != planv1.WishState_WISH_STATE_ACTIVE {
		t.Errorf("a ready wish = %v", got)
	}
	res, err := c.wishes.Grant(ctx, connect.NewRequest(&planv1.WishServiceGrantRequest{WishId: w.GetId()}))
	if g := res.Msg.GetWish(); err != nil || g.GetState() != planv1.WishState_WISH_STATE_GRANTED || g.GetGrantTime() == nil ||
		g.GetRank() != 0 || g.GetReady() {
		t.Fatalf("grant: %v, %v", g, err)
	}
	if got := order(c.list(t)); got != "Other:ACTIVE1 Ship:GRANTED" {
		t.Errorf("after grant: %s", got)
	}
	again, err := c.wishes.Grant(ctx, connect.NewRequest(&planv1.WishServiceGrantRequest{WishId: w.GetId()}))
	if err != nil || !again.Msg.GetWish().GetGrantTime().AsTime().Equal(res.Msg.GetWish().GetGrantTime().AsTime()) {
		t.Errorf("grant twice: %v, %v", again, err)
	}
	// The user may grant a wish that is not ready.
	if g, err := c.wishes.Grant(ctx, connect.NewRequest(&planv1.WishServiceGrantRequest{WishId: other.GetId()})); err != nil ||
		g.Msg.GetWish().GetState() != planv1.WishState_WISH_STATE_GRANTED {
		t.Errorf("grant a wish not ready: %v, %v", g, err)
	}
	// Ready is computed, never stored.
	stored, err := store.Get[*planv1.Wish](ctx, c.store, w.GetId())
	if err != nil || stored.GetReady() {
		t.Errorf("stored = %v, %v", stored, err)
	}
}

// TestImportState: an active wish imported beyond the three is paused, with a note; one replaced keeps its rank.
func TestImportState(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	for _, title := range []string{"A", "B"} {
		if _, err := c.make(t, title, false); err != nil {
			t.Fatal(err)
		}
	}
	imported := func(title string, rank int32, replace bool, id string) *planv1.WishServiceImportDataResponse {
		t.Helper()
		data, err := proto.Marshal(&planv1.WishExport{Version: formatVersion, Wish: &planv1.Wish{
			Id: id, Title: title, State: planv1.WishState_WISH_STATE_ACTIVE, Rank: rank, Ready: true,
		}})
		if err != nil {
			t.Fatal(err)
		}
		res, err := c.wishes.ImportData(ctx, connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: data, Replace: replace}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}
	x := store.NewID()
	if res := imported("X", 1, false, x); res.GetWish().GetRank() != 3 || res.GetNote() != "" || res.GetWish().GetReady() {
		t.Errorf("third wish = %v", res)
	}
	if res := imported("Y", 1, false, store.NewID()); res.GetWish().GetState() != planv1.WishState_WISH_STATE_PAUSED ||
		!strings.Contains(res.GetNote(), "imported paused") {
		t.Errorf("fourth wish = %v", res)
	}
	if _, err := c.wishes.Move(ctx, connect.NewRequest(&planv1.WishServiceMoveRequest{WishId: x, To: 1})); err != nil {
		t.Fatal(err)
	}
	if res := imported("X again", 3, true, x); res.GetWish().GetRank() != 1 || res.GetNote() != "" {
		t.Errorf("replaced wish = %v", res)
	}
	if got := order(c.list(t)); got != "X again:ACTIVE1 A:ACTIVE2 B:ACTIVE3 Y:PAUSED" {
		t.Errorf("list = %s", got)
	}
}
