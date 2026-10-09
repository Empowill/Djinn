package plan

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/render"
	"github.com/empowill/djinn/internal/store"
)

// MaxActive is how many wishes are active at once, at most. A djinn grants three wishes: the limit guards the
// user's attention, not the machine, and no option changes it.
const MaxActive = 3

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
// finished (done, or stopped by the user), and no question is open. A task waiting for an answer, failed,
// interrupted, resuming, planned or running keeps it from being ready; an interrupted task resumed as a fork does
// not, its fork does. Djinn only proposes: the user grants.
func Ready(tasks []*planv1.Task, questions []*planv1.Question) bool {
	if len(tasks) == 0 {
		return false
	}
	for _, t := range tasks {
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_STOPPED:
		case planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
			if render.ForkedAs(t, tasks) == "" {
				return false
			}
		default:
			return false
		}
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

// full is the refusal of a wish that would be active beyond the limit. how says what else the caller may do.
func full(actives []*planv1.Wish, how string) error {
	names := make([]string, len(actives))
	for i, w := range actives {
		names[i] = fmt.Sprintf("%d. %s (%s)", i+1, w.GetTitle(), w.GetId())
	}
	return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
		"%d wishes are active already, and a djinn grants %d at a time: pause one (djinn wish pause <wish>) or "+
			"grant one (djinn wish grant <wish>)%s. Active: %s",
		len(actives), MaxActive, how, strings.Join(names, "; ")))
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
		if wish.GetState() == planv1.WishState_WISH_STATE_GRANTED {
			return nil
		}
		wish.State, wish.Rank, wish.GrantTime = planv1.WishState_WISH_STATE_GRANTED, 0, timestamppb.Now()
		if err := tx.Put(wish); err != nil {
			return err
		}
		return rerank(ctx, tx)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.WishServiceGrantResponse{Wish: wish}), nil
}

// Pause sets an active wish aside: it keeps everything, and leaves its place to another one.
func (w *Wishes) Pause(
	ctx context.Context, req *connect.Request[planv1.WishServicePauseRequest],
) (*connect.Response[planv1.WishServicePauseResponse], error) {
	wish, err := change(ctx, w, req.Spec(), req.Msg, func(tx *store.Tx, wish *planv1.Wish) error {
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
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.WishServicePauseResponse{Wish: wish}), nil
}

// Activate makes a paused or granted wish active again, last by rank, within the limit of three.
func (w *Wishes) Activate(
	ctx context.Context, req *connect.Request[planv1.WishServiceActivateRequest],
) (*connect.Response[planv1.WishServiceActivateResponse], error) {
	wish, err := change(ctx, w, req.Spec(), req.Msg, func(tx *store.Tx, wish *planv1.Wish) error {
		if Active(wish) {
			return nil
		}
		actives, err := ActiveWishes(ctx, tx)
		if err != nil {
			return err
		}
		if len(actives) >= MaxActive {
			return full(actives, ", then activate this one")
		}
		if err := renumber(tx, actives); err != nil {
			return err
		}
		wish.State, wish.Rank, wish.GrantTime = planv1.WishState_WISH_STATE_ACTIVE, int32(len(actives)+1), nil
		return tx.Put(wish)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.WishServiceActivateResponse{Wish: wish}), nil
}

// Move gives an active wish another rank, and shifts the others.
func (w *Wishes) Move(
	ctx context.Context, req *connect.Request[planv1.WishServiceMoveRequest],
) (*connect.Response[planv1.WishServiceMoveResponse], error) {
	var actives []*planv1.Wish
	_, err := change(ctx, w, req.Spec(), req.Msg, func(tx *store.Tx, wish *planv1.Wish) error {
		if !Active(wish) {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"wish %q is not active: activate it first (djinn wish activate %s)", wish.GetTitle(), wish.GetId()))
		}
		var err error
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
	if err := fill(ctx, w.Store, actives...); err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.WishServiceMoveResponse{Wishes: actives}), nil
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
