package harness

// Under the per-azima push strategy Djinn pushes the azimas' own branches, never the wish's integration branch: work of
// no azima merged there would live only in this clone, never pushed (decision Q268 B, review of PR #17). So work of no
// azima gets an azima of its own, made for it and titled as it: at its spawn (ownAzimaFor, putOwnAzima), its branch cut
// from main before its worker starts; or, for work spawned before or moved out of its azima since, when Djinn merges
// it (adoptOrphan). That azima is pushed, and may have its pull request, as any other.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// methodOwnAzima is how the journal records an azima Djinn made for work of no azima in a wish that pushes per azima;
// the request is the azima.
const methodOwnAzima = "harness/own-azima"

// needsOwnAzima tells whether task, of wish in project, is work of no azima that gets an azima of its own: its wish
// pushes per azima, its project is in Git, and Djinn merges its commits. A watcher, an investigation, a mockup or a
// question worker merges nothing; a correction or a review merges into the branch of the work it corrects or
// reviews, never one of its own.
func (h *Harness) needsOwnAzima(ctx context.Context, r store.Reader, wish *planv1.Wish, project *planv1.Project, task *planv1.Task) bool {
	if task.GetPartOf() != "" || plan.IsAzima(task) || dispatch.Watcher(task) || dispatch.ChangesNoCode(task) ||
		dispatch.QuestionWorker(task) || plan.IsMerge(task) || !project.GetGit() {
		return false
	}
	return h.pushesPerAzima(ctx, r, wish)
}

// ownAzimaFor is the azima made for task, work of no azima about to be spawned in wish from outside Djinn (made, the
// task Djinn spawns by itself, nil), when it needs one (needsOwnAzima): titled as task, in its project, not stored yet;
// task is made part of it. nil when task needs none, or Djinn spawns it: its own workers (a correction, a review, the
// opener of a pull request) are part of the azima of what they work for, or of none. Refused when task waits for work
// of another azima, or blocks work of one (blocked, as the spawn leaves them): no azima of a wish that pushes per azima
// waits for another.
func (h *Harness) ownAzimaFor(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, task, made *planv1.Task, blocked []*planv1.Task,
) (*planv1.Task, error) {
	if made != nil || !h.needsOwnAzima(ctx, h.store, wish, project, task) {
		return nil, nil
	}
	azima := ownAzima(task, h.now())
	task.PartOf = azima.GetId()
	// Its code as the refusal names it: putOwnAzima numbers it again, under the same scheduler lock.
	azima.Code, _ = nextNumber(ctx, h.store, wish.GetId(), "T")
	if err := h.refuseAzimaDependencies(ctx, h.store, wish, append(slices.Clone(blocked), task, azima)...); err != nil {
		task.PartOf = ""
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"work of no azima gets an azima of its own in a wish that pushes per azima, and that one would wait for "+
				"another, or hold it: spawn it --part-of that azima (%w)", errors.Unwrap(err)))
	}
	return azima, nil
}

// ownAzima is a new azima for task, work of no azima: titled as it, in its wish and project.
func ownAzima(task *planv1.Task, now time.Time) *planv1.Task {
	return &planv1.Task{
		Id: store.NewID(), WishId: task.GetWishId(), ProjectId: task.GetProjectId(), Title: task.GetTitle(),
		Status: planv1.TaskStatus_TASK_STATUS_PENDING, Kind: planv1.TaskKind_TASK_KIND_AZIMA,
		CreateTime: timestamppb.New(now),
	}
}

// putOwnAzima numbers and stores azima, made for task (ownAzimaFor), in tx, with an event that says why; task's code is
// given already.
func putOwnAzima(ctx context.Context, tx *store.Tx, azima, task *planv1.Task) error {
	var err error
	if azima.Code, err = nextNumber(ctx, tx, azima.GetWishId(), "T"); err != nil {
		return err
	}
	if err := tx.Journal(actorHarness, methodOwnAzima, azima); err != nil {
		return err
	}
	if err := tx.Put(azima); err != nil {
		return err
	}
	return putEvents(ctx, tx, newEvent(azima.GetId(), Event{
		Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
		Text: ownAzimaWhy(task),
	}))
}

// ownAzimaWhy is why Djinn made an azima for task.
func ownAzimaWhy(task *planv1.Task) string {
	return "made for " + task.GetCode() + ", work of no azima: the wish pushes per azima, its branch is pushed, never the wish's"
}

// adoptOrphan makes task, work of no azima that Djinn is about to merge in project, part of an azima made for it, when
// it needs one (needsOwnAzima): work spawned before Djinn made one at the spawn, or moved out of its azima since. It
// returns that azima, task changed in place; nil when task needs none.
func (h *Harness) adoptOrphan(ctx context.Context, wish *planv1.Wish, project *planv1.Project, task *planv1.Task) (*planv1.Task, error) {
	if !h.needsOwnAzima(ctx, h.store, wish, project, task) {
		return nil, nil
	}
	ctx = context.WithoutCancel(ctx)
	var azima *planv1.Task
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		t, err := store.Get[*planv1.Task](ctx, tx, task.GetId())
		if err != nil {
			return err
		}
		if t.GetPartOf() != "" {
			azima, err = store.Get[*planv1.Task](ctx, tx, t.GetPartOf()) // Grouped meanwhile.
			task.PartOf = t.GetPartOf()
			return err
		}
		azima = ownAzima(t, h.now())
		if err := putOwnAzima(ctx, tx, azima, t); err != nil {
			return err
		}
		t.PartOf = azima.GetId()
		if err := tx.Journal(actorHarness, methodOwnAzima, t); err != nil {
			return err
		}
		if err := tx.Put(t); err != nil {
			return err
		}
		task.PartOf = t.GetPartOf()
		return putEvents(ctx, tx, h.newEvent(t.GetId(), Event{
			Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
			Text: "part of " + azima.GetCode() + ", an azima made for it: work of no azima, in a wish that pushes per azima",
		}))
	})
	if err != nil {
		return nil, err
	}
	logx.L().Info("work of no azima given an azima of its own", "task", task.GetCode(), "azima", azima.GetCode())
	h.notify()
	return azima, nil
}
