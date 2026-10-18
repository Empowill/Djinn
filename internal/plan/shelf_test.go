package plan

import (
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// TestShelf: the paused wishes, and the granted ones, each keep an order of their own, which the person sets by hand.
// A wish paused or granted comes first in its list; reordering one moves it alone, and never makes it active.
func TestShelf(t *testing.T) {
	t.Parallel()

	t.Run("reorder and states", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		fake := &workers{}
		c := serve(t, WithWorkers(fake))
		ids := map[string]string{}
		for _, title := range []string{"A", "B", "C", "D", "E"} {
			w, err := c.make(t, title, false)
			if err != nil {
				t.Fatal(err)
			}
			ids[title] = w.GetId()
		}
		// D then E, made with three active, are paused: the newest first.
		if got := order(c.list(t)); got != "A:ACTIVE1 B:ACTIVE2 C:ACTIVE3 E:PAUSED D:PAUSED" {
			t.Errorf("list = %s", got)
		}
		reorder := func(title string, to int32) (string, error) {
			t.Helper()
			res, err := c.wishes.Reorder(ctx, connect.NewRequest(&planv1.WishServiceReorderRequest{WishId: ids[title], To: to}))
			if err != nil {
				return "", err
			}
			return order(res.Msg.GetWishes()), nil
		}
		if got, err := reorder("D", 1); err != nil || got != "D:PAUSED E:PAUSED" {
			t.Errorf("reorder D to 1: %s, %v", got, err)
		}
		if got := order(c.list(t)); got != "A:ACTIVE1 B:ACTIVE2 C:ACTIVE3 D:PAUSED E:PAUSED" {
			t.Errorf("list = %s", got)
		}
		if got, err := reorder("D", 9); err != nil || got != "E:PAUSED D:PAUSED" {
			t.Errorf("reorder D beyond the last: %s, %v", got, err)
		}
		if _, err := reorder("A", 1); code(err) != connect.CodeFailedPrecondition {
			t.Errorf("reorder an active wish: %v", err)
		}
		if _, err := reorder("D", 0); code(err) != connect.CodeInvalidArgument {
			t.Errorf("reorder to 0: %v", err)
		}
		// Reordering stops no worker and activates nothing.
		if len(fake.shelved) != 0 {
			t.Errorf("shelved %v", fake.shelved)
		}

		// D moved among the active ones takes rank 1; C, pushed past the third place, comes first among the paused.
		if _, err := c.wishes.Move(ctx, connect.NewRequest(&planv1.WishServiceMoveRequest{WishId: ids["D"], To: 1})); err != nil {
			t.Fatal(err)
		}
		if got := order(c.list(t)); got != "D:ACTIVE1 A:ACTIVE2 B:ACTIVE3 C:PAUSED E:PAUSED" {
			t.Errorf("after moving D to 1: %s", got)
		}
		// Paused by hand, B comes first too.
		if _, err := c.wishes.Pause(ctx, connect.NewRequest(&planv1.WishServicePauseRequest{WishId: ids["B"]})); err != nil {
			t.Fatal(err)
		}
		if got := order(c.list(t)); got != "D:ACTIVE1 A:ACTIVE2 B:PAUSED C:PAUSED E:PAUSED" {
			t.Errorf("after pausing B: %s", got)
		}

		// The granted wishes have their own list: the last granted first, a paused wish granted leaves its list.
		for _, title := range []string{"A", "E"} {
			if _, err := c.wishes.Grant(ctx, connect.NewRequest(&planv1.WishServiceGrantRequest{WishId: ids[title]})); err != nil {
				t.Fatal(err)
			}
		}
		if got := order(c.list(t)); got != "D:ACTIVE1 B:PAUSED C:PAUSED E:GRANTED A:GRANTED" {
			t.Errorf("after granting A then E: %s", got)
		}
		if got, err := reorder("A", 1); err != nil || got != "A:GRANTED E:GRANTED" {
			t.Errorf("reorder a granted wish: %s, %v", got, err)
		}
		// Active again, a wish leaves its list and has no place there.
		a, err := c.wishes.Activate(ctx, connect.NewRequest(&planv1.WishServiceActivateRequest{WishId: ids["A"]}))
		if err != nil || a.Msg.GetWish().GetPlace() != 0 {
			t.Fatalf("activate A: %v, %v", a, err)
		}
		if got := order(c.list(t)); got != "D:ACTIVE1 A:ACTIVE2 B:PAUSED C:PAUSED E:GRANTED" {
			t.Errorf("after activating A: %s", got)
		}
	})

	// Wishes set aside before Djinn recorded places come after the placed ones, the oldest first,
	// until a reorder places them all.
	t.Run("before places", func(t *testing.T) {
		t.Parallel()
		c := serve(t)
		paused := func(title string) *planv1.Wish {
			return &planv1.Wish{Id: store.NewID(), Title: title, State: planv1.WishState_WISH_STATE_PAUSED,
				CreateTime: timestamppb.Now()}
		}
		old1, old2 := paused("Old1"), paused("Old2")
		c.put(t, old1, old2)
		if _, err := c.make(t, "New", true); err != nil {
			t.Fatal(err)
		}
		if got := order(c.list(t)); got != "New:PAUSED Old1:PAUSED Old2:PAUSED" {
			t.Errorf("list = %s", got)
		}
		res, err := c.wishes.Reorder(t.Context(), connect.NewRequest(&planv1.WishServiceReorderRequest{WishId: old2.GetId(), To: 2}))
		if err != nil || order(res.Msg.GetWishes()) != "New:PAUSED Old2:PAUSED Old1:PAUSED" {
			t.Fatalf("reorder Old2 to 2: %v, %v", res, err)
		}
		for i, w := range res.Msg.GetWishes() {
			if w.GetPlace() != int32(i+1) {
				t.Errorf("%s at place %d, want %d", w.GetTitle(), w.GetPlace(), i+1)
			}
		}
	})

	// The order is stored with the wishes: a Djinn started again on the same data lists them in it.
	t.Run("survives restart", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "djinn.db")
		s, err := store.Open(ctx, path, Entities()...)
		if err != nil {
			t.Fatal(err)
		}
		w := &Wishes{Store: s}
		var ids []string
		for _, title := range []string{"A", "B", "C"} {
			res, err := w.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: title, Paused: true}))
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, res.Msg.GetWish().GetId())
		}
		if _, err := w.Reorder(ctx, connect.NewRequest(&planv1.WishServiceReorderRequest{WishId: ids[1], To: 3})); err != nil {
			t.Fatal(err)
		}
		s.Close()

		s, err = store.Open(ctx, path, Entities()...)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		res, err := (&Wishes{Store: s}).List(ctx, connect.NewRequest(&planv1.WishServiceListRequest{}))
		if err != nil || order(res.Msg.GetWishes()) != "C:PAUSED A:PAUSED B:PAUSED" {
			t.Errorf("after a restart: %v, %v", order(res.Msg.GetWishes()), err)
		}
	})

	// A wish imported paused comes first among the paused ones; one replaced keeps its place.
	t.Run("import", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		c := serve(t)
		for _, title := range []string{"A", "B"} {
			if _, err := c.make(t, title, true); err != nil {
				t.Fatal(err)
			}
		}
		imported := func(id, title string, replace bool) {
			t.Helper()
			data, err := proto.Marshal(&planv1.WishExport{Version: formatVersion, Wish: &planv1.Wish{
				Id: id, Title: title, State: planv1.WishState_WISH_STATE_PAUSED, Place: 7,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.wishes.ImportData(ctx, connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: data, Replace: replace})); err != nil {
				t.Fatal(err)
			}
		}
		x := store.NewID()
		imported(x, "X", false)
		if got := order(c.list(t)); got != "X:PAUSED B:PAUSED A:PAUSED" {
			t.Errorf("after an import: %s", got)
		}
		if _, err := c.wishes.Reorder(ctx, connect.NewRequest(&planv1.WishServiceReorderRequest{WishId: x, To: 2})); err != nil {
			t.Fatal(err)
		}
		imported(x, "X again", true)
		if got := order(c.list(t)); got != "B:PAUSED X again:PAUSED A:PAUSED" {
			t.Errorf("after a replace: %s", got)
		}
	})
}
