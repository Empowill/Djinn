package harness

import (
	"context"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// A push records where it stands on its wish as it goes (WishPush.run): its push checks, then git push, then how it
// ended. The window shows it on the branch's sync line, the same whoever started the push: the window, djinn project
// push, or Djinn itself. A run Djinn stopped during, or crashed in, shows as none: Syncs only trusts a running stage
// while the harness runs that push (pushing).

// heldLines is how many lines of a red push check's output its sync line says on hover.
const heldLines = 6

// pushRun is one push as it goes, recorded on its wish.
type pushRun struct {
	h                         *Harness
	wishID, projectID, branch string
	start                     *timestamppb.Timestamp
}

// startPushRun notes that the push of branch, of the wish wishID in the project projectID, runs now; stop ends that.
func (h *Harness) startPushRun(wishID, projectID, branch string) *pushRun {
	h.pushingMu.Lock()
	h.pushing = pushKey(wishID, projectID, branch)
	h.pushingMu.Unlock()
	return &pushRun{h: h, wishID: wishID, projectID: projectID, branch: branch, start: timestamppb.New(h.now())}
}

// stop notes that the push no longer runs.
func (r *pushRun) stop() {
	r.h.pushingMu.Lock()
	r.h.pushing = ""
	r.h.pushingMu.Unlock()
}

// pushKey names the push of branch, of the wish wishID in the project projectID.
func pushKey(wishID, projectID, branch string) string {
	return strings.ToLower(wishID) + "\x00" + strings.ToLower(projectID) + "\x00" + branch
}

// pushRunning tells whether the push of branch, of the wish wishID in the project projectID, runs now.
func (h *Harness) pushRunning(wishID, projectID, branch string) bool {
	h.pushingMu.Lock()
	defer h.pushingMu.Unlock()
	return h.pushing != "" && h.pushing == pushKey(wishID, projectID, branch)
}

// ended is the run at stage: ended now, unless it runs still (CHECKING, PUSHING); note says why it ended so.
func (r *pushRun) ended(stage planv1.PushStage, note string) *planv1.PushRun {
	run := &planv1.PushRun{Branch: r.branch, Stage: stage, StartTime: r.start, Note: note}
	switch stage {
	case planv1.PushStage_PUSH_STAGE_CHECKING, planv1.PushStage_PUSH_STAGE_PUSHING:
	default:
		run.EndTime = timestamppb.New(r.h.now())
	}
	return run
}

// set records the run at stage, with the push check that runs (check) or why it ended so (note).
func (r *pushRun) set(ctx context.Context, stage planv1.PushStage, check, note string) {
	run := r.ended(stage, note)
	run.Check = check
	r.edit(ctx, func(p *planv1.WishPush) { p.Run = run })
}

// clear forgets the run: it neither runs nor ended in a way to show (Djinn stopped during it, or asks first).
func (r *pushRun) clear(ctx context.Context) {
	r.edit(ctx, func(p *planv1.WishPush) {
		if p.GetRun().GetBranch() == r.branch {
			p.Run = nil
		}
	})
}

func (r *pushRun) edit(ctx context.Context, edit func(*planv1.WishPush)) {
	err := r.h.store.Tx(ctx, func(tx *store.Tx) error { return editPush(ctx, tx, r.wishID, r.projectID, edit) })
	if err != nil {
		logx.L().Error("record the push run", "loop", "push", "wish", r.wishID, "project", r.projectID, "branch", r.branch, "err", err)
	}
}

// heldNote says why the push checks hold a push: the check that failed and why (held), then the first lines of what
// it printed last (out).
func heldNote(held, out string) string {
	lines := strings.Split(strings.TrimSpace(strings.TrimPrefix(tail(out), ":\n")), "\n")
	lines = lines[:min(heldLines, len(lines))]
	return strings.TrimSpace(held + "\n" + strings.Join(lines, "\n"))
}

// syncPush fills sync, a branch of wish in the project projectID, with where its last push stands, and the
// correction Djinn started while the push checks hold it.
func (h *Harness) syncPush(ctx context.Context, sync *planv1.ProjectSync, wish *planv1.Wish, projectID string) {
	state := plan.PushStateOf(wish, projectID)
	run := state.GetRun()
	if run.GetBranch() != sync.GetBranch() {
		return
	}
	switch run.GetStage() {
	case planv1.PushStage_PUSH_STAGE_CHECKING, planv1.PushStage_PUSH_STAGE_PUSHING:
		if !h.pushRunning(wish.GetId(), projectID, run.GetBranch()) {
			return // Djinn stopped during it.
		}
	case planv1.PushStage_PUSH_STAGE_HELD:
		if state.GetHeld() == "" {
			return // The hold lifted since.
		}
		if id := state.GetCorrectedBy(); id != "" {
			if c, err := store.Get[*planv1.Task](ctx, h.store, id); err == nil && !h.correctionOver(c) {
				sync.Correction = c.GetCode()
			}
		}
	}
	sync.Push = proto.CloneOf(run)
}
