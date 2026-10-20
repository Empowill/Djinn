package harness

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// oldPush is a push as a store written before W63 keeps it: the hold of the branch held last in its own fields, those
// of hold and held_branch, with unchecked, dead since W59, and a field of a later build that must stay.
func oldPush(t *testing.T, p *planv1.WishPush, hold *planv1.PushHold, branch string) *planv1.WishPush {
	t.Helper()
	f, err := proto.Marshal(hold.GetFailure())
	if err != nil {
		t.Fatal(err)
	}
	b := protowire.AppendString(protowire.AppendTag(nil, oldHeld, protowire.BytesType), hold.GetHeld())
	b = protowire.AppendVarint(protowire.AppendTag(b, oldHeldRuns, protowire.VarintType), uint64(hold.GetHeldRuns()))
	b = protowire.AppendVarint(protowire.AppendTag(b, 8, protowire.VarintType), 1)
	b = protowire.AppendString(protowire.AppendTag(b, oldCorrectedBy, protowire.BytesType), hold.GetCorrectedBy())
	b = protowire.AppendVarint(protowire.AppendTag(b, oldAttempts, protowire.VarintType), uint64(hold.GetAttempts()))
	b = protowire.AppendString(protowire.AppendTag(b, oldFailedSha, protowire.BytesType), hold.GetFailedSha())
	b = protowire.AppendBytes(protowire.AppendTag(b, oldFailure, protowire.BytesType), f)
	if branch != "" {
		b = protowire.AppendString(protowire.AppendTag(b, oldHeldBranch, protowire.BytesType), branch)
	}
	b = protowire.AppendString(protowire.AppendTag(b, 99, protowire.BytesType), "later")
	p.ProtoReflect().SetUnknown(b)
	return p
}

// TestMigratePushes (W63): a store written before keeps a push's hold in the push's own fields, and its push question
// without its branch. Recover moves the hold into holds, on the branch it named, else the one its run held, else the
// wish's integration branch; a question gets the integration branch, or under the per-azima strategy, whose questions
// name no branch Djinn can read, is withdrawn, to be asked again. Nothing is left in the old fields, and a field of a
// later build stays.
func TestMigratePushes(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	hold := &planv1.PushHold{
		Held: "at 0f2e0240, test: exit status 1", HeldRuns: 2, CorrectedBy: "c", Attempts: 1, FailedSha: "0f2e02401234",
		Failure: &planv1.IntegrationFailure{Reason: "test: exit status 1", Base: "0f2e02401234"},
	}
	for _, c := range []struct {
		name, named, run string // The branch the hold named (W35), and the one its run held.
		perAzima         bool
		wantBranch       string // Where the hold goes; "" for the wish's integration branch.
	}{
		{name: "named", named: "djinn/T2-second", wantBranch: "djinn/T2-second"},
		{name: "its run held", run: "djinn/T1-first", wantBranch: "djinn/T1-first"},
		{name: "before W35"},
		{name: "per azima, named", named: "djinn/T2-second", perAzima: true, wantBranch: "djinn/T2-second"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			in := integrating(t)
			if c.perAzima {
				in.setAzimaStrategy(t)
			}
			q := &planv1.Question{Id: store.NewID(), WishId: in.wishID, Code: "Q1", Text: "Push it?", CreateTime: timestamppb.Now()}
			p := &planv1.WishPush{ProjectId: in.projectID, QuestionId: q.GetId(), Holds: map[string]*planv1.PushHold{
				"djinn/T3-third": {Held: "at 1, lint", HeldRuns: 1},
			}}
			if c.run != "" {
				p.Run = &planv1.PushRun{Branch: c.run, Stage: planv1.PushStage_PUSH_STAGE_HELD}
			}
			oldPush(t, p, hold, c.named)
			if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
				wish, err := store.Get[*planv1.Wish](t.Context(), tx, in.wishID)
				if err != nil {
					return err
				}
				wish.Pushes = []*planv1.WishPush{p}
				for _, m := range []proto.Message{q, wish} {
					if err := tx.Journal(actorLocal, "test/old", m); err != nil {
						return err
					}
					if err := tx.Put(m); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			if err := in.h.migratePushes(t.Context()); err != nil {
				t.Fatal(err)
			}
			got := in.pushState(t)
			want := c.wantBranch
			if want == "" {
				want = in.branch
			}
			if h := got.GetHolds()[want]; !proto.Equal(h, hold) {
				t.Errorf("the hold of %s %v; want %v", want, h, hold)
			}
			if h := got.GetHolds()["djinn/T3-third"]; h.GetHeld() != "at 1, lint" || len(got.GetHolds()) != 2 {
				t.Errorf("the holds %v; want djinn/T3-third's kept beside", got.GetHolds())
			}
			if rest := protowire.AppendString(protowire.AppendTag(nil, 99, protowire.BytesType), "later"); string(got.ProtoReflect().GetUnknown()) != string(rest) {
				t.Errorf("unknown fields %q left; want the later build's alone", got.ProtoReflect().GetUnknown())
			}
			asked, err := store.Get[*planv1.Question](t.Context(), in.db, q.GetId())
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case c.perAzima && (asked.GetWithdrawal() == nil || got.GetQuestionId() != ""):
				t.Errorf("per azima, %s withdrawal %v, the push waits on %q; want it withdrawn and let go", asked.GetCode(),
					asked.GetWithdrawal(), got.GetQuestionId())
			case !c.perAzima && (asked.GetWithdrawal() != nil || got.GetQuestionId() != q.GetId() || got.GetQuestionBranch() != in.branch):
				t.Errorf("%s withdrawal %v, the push waits on %q about %q; want it open, about %s", asked.GetCode(),
					asked.GetWithdrawal(), got.GetQuestionId(), got.GetQuestionBranch(), in.branch)
			}

			// Once moved, a second start finds nothing to move.
			before := in.pushState(t)
			if err := in.h.migratePushes(t.Context()); err != nil {
				t.Fatal(err)
			}
			if after := in.pushState(t); !proto.Equal(after, before) {
				t.Errorf("a second migration changed the push: %v; want %v", after, before)
			}
		})
	}
}
