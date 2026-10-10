package plan

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

// next receives the next message of a Watch stream, or fails after a few seconds.
func next(t *testing.T, s *connect.ServerStreamForClient[planv1.WishServiceWatchResponse]) *planv1.WishServiceWatchResponse {
	t.Helper()
	got := make(chan *planv1.WishServiceWatchResponse, 1)
	go func() {
		if s.Receive() {
			got <- s.Msg()
		}
		close(got)
	}()
	select {
	case msg, ok := <-got:
		if !ok {
			t.Fatalf("the stream ended: %v", s.Err())
		}
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("no message")
		return nil
	}
}

func watch(t *testing.T, c planv1connect.WishServiceClient, wishID string) *connect.ServerStreamForClient[planv1.WishServiceWatchResponse] {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	s, err := c.Watch(ctx, connect.NewRequest(&planv1.WishServiceWatchRequest{WishId: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	if first := next(t, s); first.GetWishId() != "" || len(first.GetChanges()) != len(everything) {
		t.Fatalf("first message %v, want everything", first)
	}
	return s
}

func TestWatchSaysWhatChanged(t *testing.T) {
	c := serve(t)
	s := watch(t, c.wishes, "")
	wish := c.wish(t)
	if msg := next(t, s); msg.GetWishId() != wish || !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_WISH) {
		t.Fatalf("after make: %v", msg)
	}

	// A question changes the wish too: whether Djinn proposes to grant it follows its questions.
	if _, err := c.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
		WishId: wish, Text: "Ship it?",
	})); err != nil {
		t.Fatal(err)
	}
	msg := next(t, s)
	if want := []planv1.Change{planv1.Change_CHANGE_WISH, planv1.Change_CHANGE_QUESTION}; msg.GetWishId() != wish ||
		!slices.Equal(msg.GetChanges(), want) {
		t.Fatalf("after ask: %v, want %v", msg, want)
	}

	put, err := c.blocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{
		WishId: wish, Kind: "section", Title: "Notes", Content: "Hi",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if msg := next(t, s); !slices.Equal(msg.GetChanges(), []planv1.Change{planv1.Change_CHANGE_BLOCK}) {
		t.Fatalf("after put: %v", msg)
	}
	// A deleted block names its wish too.
	if _, err := c.blocks.Delete(t.Context(), connect.NewRequest(&planv1.BlockServiceDeleteRequest{
		Id: put.Msg.GetBlock().GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	if msg := next(t, s); msg.GetWishId() != wish || !slices.Equal(msg.GetChanges(), []planv1.Change{planv1.Change_CHANGE_BLOCK}) {
		t.Fatalf("after delete: %v", msg)
	}

	if _, err := c.projects.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{
		Directory: t.TempDir(),
	})); err != nil {
		t.Fatal(err)
	}
	// The inbox's sources come from the projects' skills.
	want := []planv1.Change{planv1.Change_CHANGE_PROJECT, planv1.Change_CHANGE_INBOX}
	if msg := next(t, s); msg.GetWishId() != "" || !slices.Equal(msg.GetChanges(), want) {
		t.Fatalf("after project add: %v, want %v", msg, want)
	}
}

// TestWatchSaysPlugged: plugging a source in, or unplugging it, changes the inbox's sources.
func TestWatchSaysPlugged(t *testing.T) {
	c := serve(t)
	dir := t.TempDir()
	agentSkill(t, dir, "mentions", "metadata:\n  djinn:\n    source:\n      watch: mentions --new\n")
	if _, err := c.projects.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir})); err != nil {
		t.Fatal(err)
	}
	s := watch(t, c.wishes, "")
	if _, err := c.inbox.Plug(t.Context(), connect.NewRequest(&planv1.InboxServicePlugRequest{Source: "mentions"})); err != nil {
		t.Fatal(err)
	}
	if msg := next(t, s); !slices.Equal(msg.GetChanges(), []planv1.Change{planv1.Change_CHANGE_INBOX}) {
		t.Fatalf("after plug: %v", msg)
	}
	if _, err := c.inbox.Unplug(t.Context(), connect.NewRequest(&planv1.InboxServiceUnplugRequest{Source: "mentions"})); err != nil {
		t.Fatal(err)
	}
	if msg := next(t, s); !slices.Equal(msg.GetChanges(), []planv1.Change{planv1.Change_CHANGE_INBOX}) {
		t.Fatalf("after unplug: %v", msg)
	}
}

func TestWatchOneWish(t *testing.T) {
	c := serve(t)
	mine, other := c.wish(t), c.wish(t)
	s := watch(t, c.wishes, mine)
	for _, wish := range []string{other, mine} {
		if _, err := c.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
			WishId: wish, Text: "Which one?",
		})); err != nil {
			t.Fatal(err)
		}
	}
	// The other wish's question never comes.
	if msg := next(t, s); msg.GetWishId() != mine {
		t.Fatalf("got %v, want only wish %s", msg, mine)
	}
}

func TestWatchCoalesces(t *testing.T) {
	c := serve(t)
	wish := c.wish(t)
	s := watch(t, c.wishes, wish)
	// Many changes within one interval come as few messages, never one each.
	for range 20 {
		if _, err := c.blocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{
			WishId: wish, Kind: "log", Title: "Line", Content: "x",
		})); err != nil {
			t.Fatal(err)
		}
	}
	received := 0
	deadline := time.After(2 * watchInterval)
	got := make(chan struct{}, 64)
	go func() {
		for s.Receive() {
			got <- struct{}{}
		}
	}()
	for {
		select {
		case <-got:
			received++
			continue
		case <-deadline:
		}
		break
	}
	if received == 0 || received >= 20 {
		t.Fatalf("%d messages for 20 changes", received)
	}
}

func TestWatchRefusesABadWish(t *testing.T) {
	c := serve(t)
	s, err := c.wishes.Watch(t.Context(), connect.NewRequest(&planv1.WishServiceWatchRequest{WishId: "not-a-uuid"}))
	if err == nil {
		for s.Receive() {
		}
		err = s.Err()
	}
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("got %v, want invalid argument", err)
	}
}

// putAll stores messages as one command, as the harness and the services do.
func putAll(t *testing.T, c clients, ms ...proto.Message) {
	t.Helper()
	err := c.store.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/put", ms[0]); err != nil {
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

// ids are the ids of messages, in their order.
func ids[T interface{ GetId() string }](ms []T) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.GetId()
	}
	return out
}

// TestWatchSendsWhatChanged: a message carries the questions and blocks that changed, whole, and the ids of those
// deleted, so that the window replaces them alone.
func TestWatchSendsWhatChanged(t *testing.T) {
	c := serve(t)
	wish := c.wish(t)
	s := watch(t, c.wishes, wish)

	q := c.ask(t, wish)
	msg := next(t, s)
	if got := msg.GetChanged().GetQuestions(); len(got) != 1 || !proto.Equal(got[0], q) {
		t.Fatalf("after ask: %v, want the question %v", msg, q)
	}
	put, err := c.blocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{
		WishId: wish, Kind: "section", Title: "Notes", Content: "Hi",
	}))
	if err != nil {
		t.Fatal(err)
	}
	block := put.Msg.GetBlock()
	if msg := next(t, s); !slices.Equal(ids(msg.GetChanged().GetBlocks()), []string{block.GetId()}) ||
		msg.GetChanged().GetBlocks()[0].GetContent() != "Hi" {
		t.Fatalf("after put: %v", msg)
	}
	if _, err := c.blocks.Delete(t.Context(), connect.NewRequest(&planv1.BlockServiceDeleteRequest{
		Id: block.GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	msg = next(t, s)
	if !slices.Equal(msg.GetChanged().GetDeleted(), []string{block.GetId()}) || len(msg.GetChanged().GetBlocks()) != 0 {
		t.Fatalf("after delete: %v, want block %s deleted", msg, block.GetId())
	}
	if !slices.Equal(msg.GetChanges(), []planv1.Change{planv1.Change_CHANGE_BLOCK}) {
		t.Fatalf("after delete: changes %v", msg.GetChanges())
	}
}

// TestWatchSendsTheTasksThatChanged: a task comes as TaskService.List gives it, with its azima and its tilasms. Its
// progress changes nothing else; a change of its state brings its azima, and the wish, which may become ready.
func TestWatchSendsTheTasksThatChanged(t *testing.T) {
	c := serve(t)
	wish := c.wish(t)
	all := tasks(t, c, wish, "T1", "W1", "W2")
	azima, w1, w2 := all["T1"], all["W1"], all["W2"]
	w1.PartOf, w2.PartOf = azima.GetId(), azima.GetId()
	putAll(t, c, w1, w2, &planv1.Tilasm{Id: store.NewID(), WishId: wish, Code: "L01", Cites: []string{w1.GetId()}})
	s := watch(t, c.wishes, wish)

	w1.Status = planv1.TaskStatus_TASK_STATUS_RUNNING
	putAll(t, c, w1)
	msg := next(t, s)
	// The first change of the wish's tasks on this stream brings its azimas: Djinn cannot tell what the reader has.
	if got := ids(msg.GetChanged().GetTasks()); !slices.Equal(got, []string{azima.GetId(), w1.GetId()}) {
		t.Fatalf("first change: tasks %v, want T1 and W1", got)
	}
	if got := msg.GetChanged().GetTasks()[0].GetAzima(); got.GetState() != planv1.AzimaState_AZIMA_STATE_IN_PROGRESS ||
		got.GetPartsRunning() != 1 {
		t.Fatalf("azima %v, want in progress with a part running", got)
	}
	if got := msg.GetChanged().GetTasks()[1].GetTilasms(); !slices.Equal(got, []string{"L01"}) {
		t.Fatalf("W1's tilasms %v, want L01", got)
	}
	if !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_WISH) {
		t.Fatalf("first change: %v, want the wish too", msg.GetChanges())
	}

	// A worker's progress: the task alone, and the wish stays.
	w1.LastLine = "Reading the store"
	putAll(t, c, w1)
	msg = next(t, s)
	if got := msg.GetChanged().GetTasks(); len(got) != 1 || got[0].GetLastLine() != "Reading the store" {
		t.Fatalf("after progress: %v, want W1 alone", msg)
	}
	if !slices.Equal(msg.GetChanges(), []planv1.Change{planv1.Change_CHANGE_TASK}) {
		t.Fatalf("after progress: changes %v, want the tasks alone", msg.GetChanges())
	}

	// Its end changes its azima, and maybe the wish.
	w1.Status = planv1.TaskStatus_TASK_STATUS_DONE
	putAll(t, c, w1)
	msg = next(t, s)
	got := msg.GetChanged().GetTasks()
	if !slices.Equal(ids(got), []string{azima.GetId(), w1.GetId()}) || got[0].GetAzima().GetPartsDone() != 1 {
		t.Fatalf("after done: tasks %v, want T1 with a part done, and W1", got)
	}
	if want := []planv1.Change{planv1.Change_CHANGE_WISH, planv1.Change_CHANGE_TASK}; !slices.Equal(msg.GetChanges(), want) {
		t.Fatalf("after done: changes %v, want %v", msg.GetChanges(), want)
	}
}

// TestWatchSendsTooManyAsAReread: past maxChanged, a message names the kinds alone, for the reader to read them again.
func TestWatchSendsTooManyAsAReread(t *testing.T) {
	c := serve(t)
	wish := c.wish(t)
	s := watch(t, c.wishes, wish)
	var blocks []proto.Message
	for i := range maxChanged + 1 {
		blocks = append(blocks, &planv1.Block{Id: store.NewID(), WishId: wish, Kind: "log", Position: int64(i)})
	}
	putAll(t, c, blocks...)
	msg := next(t, s)
	if msg.GetChanged() != nil || !slices.Equal(msg.GetChanges(), []planv1.Change{planv1.Change_CHANGE_BLOCK}) {
		t.Fatalf("got %v, want the blocks to read again", msg)
	}
}
