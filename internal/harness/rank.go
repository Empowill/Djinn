package harness

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Rank places an azima among the azimas of its wish (Task.rank): first, or just before or after another one. The
// scheduler starts the waiting work of the first azimas first, and the gates serve it first after the merges and
// pushes. It returns the wish's azimas in their new order.
func (h *Harness) Rank(ctx context.Context, procedure string, req *planv1.TaskServiceRankRequest) ([]*planv1.Task, error) {
	given := 0
	for _, set := range []bool{req.Top != nil, req.GetBefore() != "", req.GetAfter() != ""} {
		if set {
			given++
		}
	}
	switch {
	case given != 1:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(
			"say where the azima goes, one of --top, --before <azima> or --after <azima>"))
	case req.Top != nil && !req.GetTop():
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(
			"--top=false places nothing: --before or --after another azima places it lower"))
	}
	logx.Lock(&h.sched, "scheduler")
	defer h.sched.Unlock()
	var order []*planv1.Task
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		azima, err := rankedAzima(ctx, tx, req.GetAzima(), req.GetWishId())
		if err != nil {
			return err
		}
		tasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": azima.GetWishId()})
		if err != nil {
			return err
		}
		others := slices.DeleteFunc(plan.AzimaOrder(tasks), func(t *planv1.Task) bool { return t.GetId() == azima.GetId() })
		at := 0
		if name := req.GetBefore() + req.GetAfter(); name != "" {
			i := slices.IndexFunc(others, func(t *planv1.Task) bool { return t.GetId() == name || strings.EqualFold(t.GetCode(), name) })
			switch {
			case i >= 0:
			case name == azima.GetId() || strings.EqualFold(name, azima.GetCode()):
				return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
					"azima %s is ranked against another azima, not itself", azima.GetCode()))
			default:
				return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
					"%s is not an azima of the wish of %s: rank it against one of its own", name, azima.GetCode()))
			}
			at = i
			if req.GetAfter() != "" {
				at++
			}
		}
		if err := tx.Journal(plan.CallerActor(ctx), procedure, req); err != nil {
			return err
		}
		order, err = rerankAzimas(tx, slices.Insert(others, at, azima))
		return err
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.notify()
	h.wake() // The waiting work starts in its new order.
	return h.withAzimas(ctx, order)
}

// rankAzima gives an azima the place at (from 1; beyond the last, last) among the azimas of its wish: djinn task
// update --rank.
func (h *Harness) rankAzima(ctx context.Context, procedure string, req *planv1.TaskServiceUpdateRequest, azima *planv1.Task) (*planv1.Task, error) {
	var ranked *planv1.Task
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		tasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": azima.GetWishId()})
		if err != nil {
			return err
		}
		order := slices.DeleteFunc(plan.AzimaOrder(tasks), func(t *planv1.Task) bool { return t.GetId() == azima.GetId() })
		at := min(int(req.GetRank())-1, len(order))
		if err := tx.Journal(plan.CallerActor(ctx), procedure, req); err != nil {
			return err
		}
		if order, err = rerankAzimas(tx, slices.Insert(order, at, azima)); err != nil {
			return err
		}
		ranked = order[at]
		return nil
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.notify()
	h.wake()
	return h.withAzima(ctx, ranked)
}

// rerankAzimas gives the azimas of a wish, in this order, the ranks 1 to n, and writes those that changed: the first
// ranking of a wish ranks all its azimas, so that a new one, unranked, goes last. It returns them, as written.
func rerankAzimas(tx *store.Tx, order []*planv1.Task) ([]*planv1.Task, error) {
	out := make([]*planv1.Task, len(order))
	for i, t := range order {
		out[i] = t
		if rank := int32(i + 1); t.GetRank() != rank {
			out[i] = proto.CloneOf(t)
			out[i].Rank = rank
			if err := tx.Put(out[i]); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// rankedAzima finds the azima to rank: by identifier, or by code within the wish when one is given, else as
// findAzima does.
func rankedAzima(ctx context.Context, r store.Reader, name, wishID string) (*planv1.Task, error) {
	if wishID == "" {
		return findAzima(ctx, r, name)
	}
	tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": wishID})
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(tasks, func(t *planv1.Task) bool { return t.GetId() == name || strings.EqualFold(t.GetCode(), name) })
	switch {
	case i < 0:
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("azima %s is not a task of the wish", name))
	case !plan.IsAzima(tasks[i]):
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"%s is work, not an azima: only azimas have a rank; its azima's rank orders it", tasks[i].GetCode()))
	}
	return tasks[i], nil
}
