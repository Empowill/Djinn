package plan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// MaxActive is how many wishes are active at once, at most. A djinn grants three wishes: the limit guards the
// user's attention, not the machine, and no option changes it. There may be as many wishes as you like: the three
// first are active, the others wait paused, their workers stopped.
const MaxActive = 3

// WishWorkers reaches the workers of the wishes: djinn up gives the harness's. A paused wish's workers stop, and
// wait to resume with it; a wish active again wakes the scheduler, which starts them.
type WishWorkers interface {
	Shelve(ctx context.Context, wishID string)
	Wake()
	// StopWish stops the wish's workers for good, and waits until they have ended.
	StopWish(ctx context.Context, wishID string) error
}

// shelve stops the workers of the wishes paused, and wakes the scheduler for the active ones; without workers,
// nothing.
func (w *Wishes) shelve(ctx context.Context, paused ...*planv1.Wish) {
	if w.Workers == nil {
		return
	}
	for _, wish := range paused {
		w.Workers.Shelve(ctx, wish.GetId())
	}
	w.Workers.Wake()
}

// Active tells whether wish is active. A wish made before Djinn recorded states is.
func Active(wish *planv1.Wish) bool {
	switch wish.GetState() {
	case planv1.WishState_WISH_STATE_UNSPECIFIED, planv1.WishState_WISH_STATE_ACTIVE:
		return true
	}
	return false
}

// ActiveWishes returns the active wishes by rank: the first one has priority, and the scheduler serves them in this
// order.
func ActiveWishes(ctx context.Context, r store.Reader) ([]*planv1.Wish, error) {
	all, err := store.List[*planv1.Wish](ctx, r, nil)
	if err != nil {
		return nil, err
	}
	return Ranked(all), nil
}

// Ranked returns the active wishes of all by rank. A wish without a rank, made before ranks, comes after the ranked
// ones, the oldest first.
func Ranked(all []*planv1.Wish) []*planv1.Wish {
	out := slices.DeleteFunc(slices.Clone(all), func(w *planv1.Wish) bool { return !Active(w) })
	rank := func(w *planv1.Wish) int32 {
		if w.GetRank() <= 0 {
			return math.MaxInt32
		}
		return w.GetRank()
	}
	// The store lists by identifier, a UUIDv7: oldest first, which the stable sort keeps between equal ranks.
	slices.SortStableFunc(out, func(a, b *planv1.Wish) int { return cmp.Compare(rank(a), rank(b)) })
	return out
}

// Ready tells whether Djinn proposes to grant a wish that has these tasks and questions: it has tasks, every one
// finished (done, stopped by the user, or cut short for good), and no question is open. A draft azima is not
// counted in the wish's progress: it waits to be opened. Djinn only proposes: the user grants.
func Ready(tasks []*planv1.Task, questions []*planv1.Question) bool {
	hasNonDraft := false
	for _, t := range tasks {
		if t.GetDraft() {
			continue
		}
		hasNonDraft = true
		// Cut short for good counts as finished: see Finished.
		if !Finished(t) {
			return false
		}
	}
	if !hasNonDraft {
		return false
	}
	for _, q := range questions {
		if q.GetAnswer() == nil {
			return false
		}
	}
	return true
}

// fill sets Wish.ready on wishes, as r holds their tasks and questions. A granted wish is not proposed again.
func fill(ctx context.Context, r store.Reader, wishes ...*planv1.Wish) error {
	for _, w := range wishes {
		if w == nil {
			continue
		}
		w.Ready = false
		if w.GetState() == planv1.WishState_WISH_STATE_GRANTED {
			continue
		}
		where := store.Where{"wish_id": w.GetId()}
		tasks, err := store.List[*planv1.Task](ctx, r, where)
		if err != nil {
			return err
		}
		questions, err := store.List[*planv1.Question](ctx, r, where)
		if err != nil {
			return err
		}
		w.Ready = Ready(tasks, questions)
	}
	return nil
}

// renumber gives the active wishes, in this order, the ranks 1 to n, and writes those that changed.
func renumber(tx *store.Tx, actives []*planv1.Wish) error {
	for i, w := range actives {
		rank := int32(i + 1)
		if w.GetRank() == rank && w.GetState() == planv1.WishState_WISH_STATE_ACTIVE {
			continue
		}
		w.Rank, w.State = rank, planv1.WishState_WISH_STATE_ACTIVE
		if err := tx.Put(w); err != nil {
			return err
		}
	}
	return nil
}

// wishRequest is a request that names one wish.
type wishRequest interface {
	proto.Message
	GetWishId() string
}

// change runs fn on the wish of the request in a transaction journaled with req, then fills the wish's readiness.
func change[R wishRequest](
	ctx context.Context, w *Wishes, spec connect.Spec, req R, fn func(*store.Tx, *planv1.Wish) error,
) (*planv1.Wish, error) {
	var wish *planv1.Wish
	err := write(ctx, w.Store, spec, req, func(tx *store.Tx) error {
		var err error
		if wish, err = store.Get[*planv1.Wish](ctx, tx, req.GetWishId()); err != nil {
			return err
		}
		return fn(tx, wish)
	})
	if err != nil {
		return nil, err
	}
	if err := fill(ctx, w.Store, wish); err != nil {
		return nil, Status(err)
	}
	return wish, nil
}

// Grant grants a wish: the user says it is done. Djinn proposes it when the wish is ready, and never grants it
// itself; the user may grant it at any time.
func (w *Wishes) Grant(
	ctx context.Context, req *connect.Request[planv1.WishServiceGrantRequest],
) (*connect.Response[planv1.WishServiceGrantResponse], error) {
	wish, err := change(ctx, w, req.Spec(), req.Msg, func(tx *store.Tx, wish *planv1.Wish) error {
		return grantWish(ctx, tx, wish)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.WishServiceGrantResponse{Wish: wish}), nil
}

// grantWish grants wish in tx, and closes the gap it leaves in the ranks. A granted wish stays as it is.
func grantWish(ctx context.Context, tx *store.Tx, wish *planv1.Wish) error {
	if wish.GetState() == planv1.WishState_WISH_STATE_GRANTED {
		return nil
	}
	wish.State, wish.Rank, wish.GrantTime = planv1.WishState_WISH_STATE_GRANTED, 0, timestamppb.Now()
	if err := tx.Put(wish); err != nil {
		return err
	}
	return rerank(ctx, tx)
}

// Pause sets an active wish aside: it keeps everything, its workers stop until it is active again, and it leaves
// its place to another one.
func (w *Wishes) Pause(
	ctx context.Context, req *connect.Request[planv1.WishServicePauseRequest],
) (*connect.Response[planv1.WishServicePauseResponse], error) {
	wish, err := change(ctx, w, req.Spec(), req.Msg, func(tx *store.Tx, wish *planv1.Wish) error {
		return pauseWish(ctx, tx, wish)
	})
	if err != nil {
		return nil, err
	}
	w.shelve(ctx, wish)
	return connect.NewResponse(&planv1.WishServicePauseResponse{Wish: wish}), nil
}

// pauseWish sets wish aside in tx, and closes the gap it leaves in the ranks. A paused wish stays as it is.
func pauseWish(ctx context.Context, tx *store.Tx, wish *planv1.Wish) error {
	switch wish.GetState() {
	case planv1.WishState_WISH_STATE_PAUSED:
		return nil
	case planv1.WishState_WISH_STATE_GRANTED:
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"wish %q is granted, not active: there is nothing to pause", wish.GetTitle()))
	}
	wish.State, wish.Rank = planv1.WishState_WISH_STATE_PAUSED, 0
	if err := tx.Put(wish); err != nil {
		return err
	}
	return rerank(ctx, tx)
}

// Activate makes a paused or granted wish active again, last by rank. Three wishes being active, it takes the third
// place, and the third wish is paused: only the three first wishes are active.
func (w *Wishes) Activate(
	ctx context.Context, req *connect.Request[planv1.WishServiceActivateRequest],
) (*connect.Response[planv1.WishServiceActivateResponse], error) {
	var paused []*planv1.Wish
	wish, err := change(ctx, w, req.Spec(), req.Msg, func(tx *store.Tx, wish *planv1.Wish) error {
		if Active(wish) {
			return nil
		}
		var err error
		paused, err = place(ctx, tx, wish, 0)
		return err
	})
	if err != nil {
		return nil, err
	}
	w.shelve(ctx, paused...)
	if err := fill(ctx, w.Store, paused...); err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.WishServiceActivateResponse{Wish: wish, Paused: paused}), nil
}

// place makes wish active at the rank to, from 1, among the active wishes; 0 or beyond the last, last. Never beyond
// the third place: the wishes pushed past it are paused, and returned.
func place(ctx context.Context, tx *store.Tx, wish *planv1.Wish, to int) ([]*planv1.Wish, error) {
	actives, err := ActiveWishes(ctx, tx)
	if err != nil {
		return nil, err
	}
	actives = slices.DeleteFunc(actives, func(a *planv1.Wish) bool { return a.GetId() == wish.GetId() })
	at := len(actives)
	if to > 0 {
		at = min(to-1, at)
	}
	actives = slices.Insert(actives, min(at, MaxActive-1), wish)
	var paused []*planv1.Wish
	for len(actives) > MaxActive {
		last := actives[len(actives)-1]
		actives = actives[:len(actives)-1]
		last.State, last.Rank = planv1.WishState_WISH_STATE_PAUSED, 0
		if err := tx.Put(last); err != nil {
			return nil, err
		}
		paused = append(paused, last)
	}
	wish.GrantTime = nil
	return paused, renumber(tx, actives)
}

// Delete deletes a wish and everything that belongs to it: its tasks and their events, its questions, its blocks.
// Its workers stop first: an active wish is paused, which the journal records, so that no task of it starts again.
// Once it is gone, its lead's terminal closes, and each task's worktree that holds no work is removed (cleanWorktree);
// the others stay, the response says why. Every branch stays. An inbox item routed to it forgets it.
func (w *Wishes) Delete(
	ctx context.Context, req *connect.Request[planv1.WishServiceDeleteRequest],
) (*connect.Response[planv1.WishServiceDeleteResponse], error) {
	id := req.Msg.GetWishId()
	wish, err := store.Get[*planv1.Wish](ctx, w.Store, id)
	if err != nil {
		return nil, Status(err)
	}
	if Active(wish) {
		pause := &planv1.WishServicePauseRequest{WishId: id}
		if _, err := w.Pause(ctx, connect.NewRequest(pause)); err != nil {
			return nil, err
		}
	}
	if w.Workers != nil {
		if err := w.Workers.StopWish(ctx, id); err != nil {
			return nil, Status(err)
		}
	}
	res := &planv1.WishServiceDeleteResponse{}
	var tasks []*planv1.Task
	var tilasms []*planv1.Tilasm
	err = write(ctx, w.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		where := store.Where{"wish_id": id}
		var err error
		if tasks, err = store.List[*planv1.Task](ctx, tx, where); err != nil {
			return err
		}
		for _, t := range tasks {
			events, err := store.List[*planv1.TaskEvent](ctx, tx, store.Where{"task_id": t.GetId()})
			if err != nil {
				return err
			}
			for _, ev := range events {
				if err := tx.Delete(ev); err != nil {
					return err
				}
			}
			if err := tx.Delete(t); err != nil {
				return err
			}
		}
		questions, err := store.List[*planv1.Question](ctx, tx, where)
		if err != nil {
			return err
		}
		for _, q := range questions {
			if err := tx.Delete(q); err != nil {
				return err
			}
		}
		blocks, err := store.List[*planv1.Block](ctx, tx, where)
		if err != nil {
			return err
		}
		for _, b := range blocks {
			if err := tx.Delete(b); err != nil {
				return err
			}
		}
		if tilasms, err = store.List[*planv1.Tilasm](ctx, tx, where); err != nil {
			return err
		}
		for _, t := range tilasms {
			if err := tx.Delete(t); err != nil {
				return err
			}
		}
		items, err := store.List[*planv1.InboxItem](ctx, tx, where)
		if err != nil {
			return err
		}
		for _, item := range items {
			item.WishId = ""
			if err := tx.Put(item); err != nil {
				return err
			}
		}
		if wish, err = store.Get[*planv1.Wish](ctx, tx, id); err != nil {
			return err
		}
		if err := tx.Delete(wish); err != nil {
			return err
		}
		res.Wish, res.Tasks, res.Questions, res.Blocks = wish, int32(len(tasks)), int32(len(questions)), int32(len(blocks))
		return rerank(ctx, tx)
	})
	if err != nil {
		return nil, err
	}
	if w.Leads != nil {
		w.Leads.Close(LeadTerminal(id))
	}
	for _, t := range tilasms {
		if w.Home != "" {
			os.RemoveAll(TilasmDir(w.Home, t.GetId()))
		}
	}
	res.Kept, res.WorktreesRemoved = cleanWorktrees(ctx, w.Store, tasks)
	return connect.NewResponse(res), nil
}

// cleanWorktrees removes the worktrees of tasks that hold no work, each from its project's repository, and returns
// those it keeps and how many it removed. The wish is gone already: what fails here keeps a worktree, and says so.
func cleanWorktrees(ctx context.Context, r store.Reader, tasks []*planv1.Task) ([]*planv1.KeptWorktree, int32) {
	var kept []*planv1.KeptWorktree
	var removed int32
	for _, t := range tasks {
		if t.GetWorktree() == "" {
			continue
		}
		project, err := store.Get[*planv1.Project](ctx, r, t.GetProjectId())
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			kept = append(kept, &planv1.KeptWorktree{TaskCode: t.GetCode(), Worktree: t.GetWorktree(),
				Branch: t.GetBranch(), Error: err.Error()})
			continue
		}
		gone, k := cleanWorktree(ctx, project.GetDirectory(), t)
		if gone {
			removed++
		}
		if k != nil {
			kept = append(kept, k)
		}
	}
	return kept, removed
}

// Move gives a wish another rank, and shifts the others. A wish that is not active becomes active at that rank, the
// third at most; the wish it pushes past the third place is paused.
func (w *Wishes) Move(
	ctx context.Context, req *connect.Request[planv1.WishServiceMoveRequest],
) (*connect.Response[planv1.WishServiceMoveResponse], error) {
	var actives, paused []*planv1.Wish
	_, err := change(ctx, w, req.Spec(), req.Msg, func(tx *store.Tx, wish *planv1.Wish) error {
		var err error
		if !Active(wish) {
			if paused, err = place(ctx, tx, wish, int(req.Msg.GetTo())); err != nil {
				return err
			}
			actives, err = ActiveWishes(ctx, tx)
			return err
		}
		if actives, err = ActiveWishes(ctx, tx); err != nil {
			return err
		}
		actives = slices.DeleteFunc(actives, func(a *planv1.Wish) bool { return a.GetId() == wish.GetId() })
		at := min(int(req.Msg.GetTo())-1, len(actives))
		actives = slices.Insert(actives, at, wish)
		return renumber(tx, actives)
	})
	if err != nil {
		return nil, err
	}
	w.shelve(ctx, paused...)
	if err := fill(ctx, w.Store, slices.Concat(actives, paused)...); err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.WishServiceMoveResponse{Wishes: actives, Paused: paused}), nil
}

// rerank closes the gaps in the ranks of the active wishes, after one left them.
func rerank(ctx context.Context, tx *store.Tx) error {
	actives, err := ActiveWishes(ctx, tx)
	if err != nil {
		return err
	}
	return renumber(tx, actives)
}

// sorted orders wishes as List gives them: the active ones by rank, then the paused ones, then the granted ones,
// each the oldest first.
func sorted(all []*planv1.Wish) []*planv1.Wish {
	out := Ranked(all)
	for _, state := range []planv1.WishState{planv1.WishState_WISH_STATE_PAUSED, planv1.WishState_WISH_STATE_GRANTED} {
		for _, w := range all {
			if w.GetState() == state {
				out = append(out, w)
			}
		}
	}
	return out
}
