package plan

// Before the one lead (W338), each wish kept its own lead in the field 6 of Wish, reserved since. A store written then
// still holds it, as a field this build does not know: the first start after the update makes it the one lead.

import (
	"context"
	"errors"
	"slices"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

// wishLeadField is the number of the lead a wish kept before the one lead.
const wishLeadField protowire.Number = 6

// WishLead is the lead a wish kept before the one lead, nil when it kept none.
func WishLead(w *planv1.Wish) *planv1.Lead {
	var lead *planv1.Lead
	b := w.ProtoReflect().GetUnknown()
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil
		}
		b = b[n:]
		n = protowire.ConsumeFieldValue(num, typ, b)
		if n < 0 {
			return nil
		}
		if num == wishLeadField && typ == protowire.BytesType {
			v, _ := protowire.ConsumeBytes(b[:n])
			l := &planv1.Lead{}
			if proto.Unmarshal(v, l) == nil {
				lead = l
			}
		}
		b = b[n:]
	}
	if !hasLead(lead) {
		return nil
	}
	return lead
}

// CarryLead records the one lead from the leads the wishes kept before it, once: only while no lead is recorded.
// The lead of the first active wish by rank wins, else that of the most recent wish. It returns the lead recorded,
// nil when there was nothing to carry.
func CarryLead(ctx context.Context, s *store.Store) (*planv1.Lead, error) {
	if _, err := store.Get[*planv1.Lead](ctx, s, "lead"); !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	all, err := store.List[*planv1.Wish](ctx, s, nil)
	if err != nil {
		return nil, err
	}
	// The store lists by identifier, a UUIDv7: the most recent wish is the last.
	recent := slices.Clone(all)
	slices.Reverse(recent)
	var carried *planv1.Lead
	for _, w := range append(Ranked(all), recent...) {
		if carried = WishLead(w); carried != nil {
			break
		}
	}
	if carried == nil {
		return nil, nil
	}
	carried.Id = "lead"
	if carried.GetProvider() == planv1.Provider_PROVIDER_UNSPECIFIED {
		carried.Provider = planv1.Provider_PROVIDER_CLAUDE
	}
	req := &planv1.LeadServiceSetRequest{
		SessionId: carried.GetSessionId(), Provider: carried.GetProvider(), Directory: carried.GetDirectory(),
	}
	err = s.Tx(ctx, func(tx *store.Tx) error {
		return errors.Join(tx.Journal(actor, planv1connect.LeadServiceSetProcedure, req), tx.Put(carried))
	})
	if err != nil {
		return nil, err
	}
	return carried, nil
}
