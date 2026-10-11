package plan

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

// oldWish is a wish as a Djinn of one lead per wish stored it: its lead in the field 6, reserved since.
func oldWish(t *testing.T, title string, state planv1.WishState, rank int32, lead *planv1.Lead) *planv1.Wish {
	t.Helper()
	w := &planv1.Wish{Id: store.NewID(), Title: title, State: state, Rank: rank}
	if lead != nil {
		b, err := proto.Marshal(lead)
		if err != nil {
			t.Fatal(err)
		}
		w.ProtoReflect().SetUnknown(protowire.AppendBytes(protowire.AppendTag(nil, wishLeadField, protowire.BytesType), b))
	}
	return w
}

// TestCarryLead: the first start after the update from one lead per wish records the lead of the first active wish
// by rank as the one lead, once; the store kept it as a field this build does not know.
func TestCarryLead(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	put := func(ws ...*planv1.Wish) {
		t.Helper()
		err := c.store.Tx(ctx, func(tx *store.Tx) error {
			errs := []error{tx.Journal("test", planv1connect.WishServiceMakeProcedure, &planv1.WishServiceMakeRequest{Title: "old"})}
			for _, w := range ws {
				errs = append(errs, tx.Put(w))
			}
			return errors.Join(errs...)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	lead := func(session string) *planv1.Lead {
		return &planv1.Lead{Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: session, Directory: "/srv/lamp"}
	}
	active := planv1.WishState_WISH_STATE_ACTIVE

	// No wish kept a lead: nothing is recorded.
	put(oldWish(t, "bare", active, 1, nil))
	if got, err := CarryLead(ctx, c.store); err != nil || got != nil {
		t.Fatalf("carry without a wish's lead = %v, %v", got, err)
	}

	// A granted wish, the most recent, kept one; so did the second and third active wishes. The first active wish
	// by rank that kept one wins.
	put(oldWish(t, "third", active, 3, lead("third")), oldWish(t, "second", active, 2, lead("second")),
		oldWish(t, "granted", planv1.WishState_WISH_STATE_GRANTED, 0, lead("granted")))
	got, err := CarryLead(ctx, c.store)
	if err != nil || got.GetSessionId() != "second" || got.GetId() != "lead" {
		t.Fatalf("carry = %v, %v; want the second wish's lead", got, err)
	}
	res, err := c.lead.Get(ctx, connect.NewRequest(&planv1.LeadServiceGetRequest{}))
	if err != nil || !proto.Equal(res.Msg.GetLead(), got) {
		t.Fatalf("djinn lead get = %v, %v; want %v", res, err, got)
	}

	// Once: a lead recorded is never replaced by a wish's.
	if again, err := CarryLead(ctx, c.store); err != nil || again != nil {
		t.Fatalf("second carry = %v, %v; want nothing", again, err)
	}

	// No active wish kept one: the most recent wish's lead.
	c = serve(t)
	put(oldWish(t, "old", planv1.WishState_WISH_STATE_PAUSED, 0, lead("old")),
		oldWish(t, "recent", planv1.WishState_WISH_STATE_GRANTED, 0, lead("recent")), oldWish(t, "bare", active, 1, nil))
	if got, err := CarryLead(ctx, c.store); err != nil || got.GetSessionId() != "recent" {
		t.Fatalf("carry from inactive wishes = %v, %v; want the most recent", got, err)
	}
}
