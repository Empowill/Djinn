package harness

// Before W63 the push of a wish in a project kept the hold of the branch held last in fields of its own (WishPush 6 to
// 12 and 15, reserved since: held, held_runs, unchecked, corrected_by, attempts, failed_sha, failure, held_branch),
// only the other branches' in holds, and a push question did not record the branch it asks about. A store written then
// still holds those fields, unknown to this build: Recover moves them (migratePushes).

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// The numbers of the WishPush fields that kept a hold before W63; 8, unchecked, between them, was dead since W59 and
// goes.
const (
	oldHeld        protowire.Number = 6
	oldHeldRuns    protowire.Number = 7
	oldCorrectedBy protowire.Number = 9
	oldAttempts    protowire.Number = 10
	oldFailedSha   protowire.Number = 11
	oldFailure     protowire.Number = 12
	oldHeldBranch  protowire.Number = 15
)

// oldHold reads the hold the fields of p kept before W63, and the branch they named (empty before W35), and takes them
// out of p. It tells whether p had any of them.
func oldHold(p *planv1.WishPush) (hold *planv1.PushHold, branch string, found bool) {
	hold = &planv1.PushHold{}
	var rest []byte
	b := p.ProtoReflect().GetUnknown()
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, "", false
		}
		m := protowire.ConsumeFieldValue(num, typ, b[n:])
		if m < 0 {
			return nil, "", false
		}
		field, value := b[:n+m], b[n:n+m]
		b = b[n+m:]
		if num < oldHeld || num > oldFailure && num != oldHeldBranch {
			rest = append(rest, field...)
			continue
		}
		found = true
		switch typ {
		case protowire.VarintType:
			v, _ := protowire.ConsumeVarint(value)
			switch num {
			case oldHeldRuns:
				hold.HeldRuns = int32(v)
			case oldAttempts:
				hold.Attempts = int32(v)
			}
		case protowire.BytesType:
			v, _ := protowire.ConsumeBytes(value)
			switch num {
			case oldHeld:
				hold.Held = string(v)
			case oldCorrectedBy:
				hold.CorrectedBy = string(v)
			case oldFailedSha:
				hold.FailedSha = string(v)
			case oldHeldBranch:
				branch = string(v)
			case oldFailure:
				f := &planv1.IntegrationFailure{}
				if proto.Unmarshal(v, f) == nil {
					hold.Failure = f
				}
			}
		}
	}
	if found {
		p.ProtoReflect().SetUnknown(rest)
	}
	if !plan.PushHeld(hold) {
		hold = nil
	}
	return hold, branch, found
}

// migratePushes moves, in each wish of the store, the hold a push kept in its own fields before W63 into
// WishPush.holds, on the branch it named or, before W35, the one heldBranchBefore finds; and gives a push question
// asked before W63 its branch: the wish's integration branch, or under the per-azima strategy, whose push questions
// name no branch Djinn can read, none: Djinn withdraws it, and asks again when the push is due. A wish with no
// integration branch in the push's project (the project gone from the store) pushes nothing there again: a hold found
// no branch goes, as no push run would read or clear it, and the push question is withdrawn. Each wish changed is
// journaled; a wish with nothing to move costs no read of its strategy.
func (h *Harness) migratePushes(ctx context.Context) error {
	wishes, err := store.List[*planv1.Wish](ctx, h.store, nil)
	if err != nil {
		return err
	}
	for _, wish := range wishes {
		changed, strategy := false, sync.OnceValue(func() bool { return h.pushesPerAzima(ctx, h.store, wish) })
		var withdraw []*planv1.WishPush
		for _, p := range wish.GetPushes() {
			hold, branch, found := oldHold(p)
			changed = changed || found
			if hold != nil {
				if branch == "" {
					branch = h.heldBranchBefore(ctx, wish, p, hold, strategy())
				}
				if branch != "" {
					if p.Holds == nil {
						p.Holds = map[string]*planv1.PushHold{}
					}
					p.Holds[branch] = hold
				}
			}
			if p.GetQuestionId() != "" && p.GetQuestionBranch() == "" {
				if about := plan.IntegrationBranchOf(wish, p.GetProjectId()); strategy() || about == "" {
					withdraw = append(withdraw, p)
				} else {
					p.QuestionBranch, changed = about, true
				}
			}
		}
		if changed {
			err := h.store.Tx(ctx, func(tx *store.Tx) error {
				return errors.Join(tx.Journal(actorHarness, methodIntegrate, wish), tx.Put(wish))
			})
			if err != nil {
				return fmt.Errorf("move the push holds of the wish %s: %w", wish.GetId(), err)
			}
		}
		for _, p := range withdraw {
			h.withdrawPushQuestion(ctx, wish.GetId(), p.GetProjectId(), p.GetQuestionId(),
				"Djinn: asked before it recorded which branch a push question is about; it asks again when the push is due.")
		}
	}
	return nil
}

// heldBranchBefore is the branch whose red push checks hold, as hold says, the push p of wish recorded before W35,
// which named none: the one its run held; else, per azima, the wish's branch whose tip is, or holds, the commit they
// were red at, an azima's first; else the wish's integration branch.
func (h *Harness) heldBranchBefore(ctx context.Context, wish *planv1.Wish, p *planv1.WishPush, hold *planv1.PushHold, perAzima bool) string {
	if run := p.GetRun(); run.GetStage() == planv1.PushStage_PUSH_STAGE_HELD && run.GetBranch() != "" {
		return run.GetBranch()
	}
	branch := plan.IntegrationBranchOf(wish, p.GetProjectId())
	project, err := store.Get[*planv1.Project](ctx, h.store, p.GetProjectId())
	if !perAzima || err != nil {
		return branch
	}
	repo := project.GetDirectory()
	var branches []string
	for _, a := range h.wishAzimas(ctx, wish) {
		branches = append(branches, azimaBranchName(a))
	}
	branches = append(branches, branch)
	tips := make([]string, len(branches))
	for i, b := range branches {
		tips[i] = commitOf(ctx, repo, "refs/heads/"+b)
	}
	red := slices.DeleteFunc([]string{hold.GetFailedSha(), hold.GetFailure().GetBase()}, func(sha string) bool { return sha == "" })
	for _, sha := range red {
		if i := slices.Index(tips, sha); i >= 0 {
			return branches[i]
		}
	}
	for i, tip := range tips { // The branch moved since: a commit of the developer's on it.
		for _, sha := range red {
			if tip != "" && isAncestor(ctx, repo, sha, tip) {
				return branches[i]
			}
		}
	}
	return branch
}
