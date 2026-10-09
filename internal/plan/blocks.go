package plan

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

// gap is the distance between two blocks added one after the other: room for blocks slipped in between later,
// without moving the others. An integer reads and dictates better than a fractional key, and a wish holds few
// enough blocks that renumbering, the day a gap runs out, is a single command.
const gap = 1000

// Blocks implements BlockService.
type Blocks struct {
	planv1connect.UnimplementedBlockServiceHandler
	Store *store.Store
}

func (b *Blocks) Put(
	ctx context.Context, req *connect.Request[planv1.BlockServicePutRequest],
) (*connect.Response[planv1.BlockServicePutResponse], error) {
	var block *planv1.Block
	err := write(ctx, b.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		block, err = putBlock(ctx, tx, req.Msg)
		return err
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.BlockServicePutResponse{Block: block}), nil
}

// putBlock adds or changes the block m names, in tx. The caller journals the command.
func putBlock(ctx context.Context, tx *store.Tx, m *planv1.BlockServicePutRequest) (*planv1.Block, error) {
	var block *planv1.Block
	if err := checkIcon(m.GetIcon()); err != nil {
		return nil, err
	}
	if _, err := store.Get[*planv1.Wish](ctx, tx, m.GetWishId()); err != nil {
		return nil, err
	}
	if id := m.GetTaskId(); id != "" {
		task, err := store.Get[*planv1.Task](ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if task.GetWishId() != m.GetWishId() {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("task %s belongs to another wish", task.GetCode()))
		}
	}
	now := timestamppb.Now()
	if id := m.GetId(); id != "" {
		var err error
		if block, err = store.Get[*planv1.Block](ctx, tx, id); err != nil {
			return nil, err
		}
		if block.GetWishId() != m.GetWishId() {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("block %s belongs to another wish", id))
		}
	} else {
		block = &planv1.Block{Id: store.NewID(), WishId: m.GetWishId(), CreateTime: now}
		last, err := lastPosition(ctx, tx, m.GetWishId())
		if err != nil {
			return nil, err
		}
		block.Position = last + gap
	}
	// The fields given replace those of the block; the others keep their value.
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&block.Kind, m.GetKind())
	set(&block.Title, m.GetTitle())
	set(&block.Content, m.GetContent())
	set(&block.MediaType, m.GetMediaType())
	set(&block.TaskId, m.GetTaskId())
	set(&block.Icon, m.GetIcon())
	if m.GetPosition() != 0 {
		block.Position = m.GetPosition()
	}
	block.UpdateTime = now
	return block, tx.Put(block)
}

func lastPosition(ctx context.Context, r store.Reader, wishID string) (int64, error) {
	blocks, err := store.List[*planv1.Block](ctx, r, store.Where{"wish_id": wishID})
	if err != nil {
		return 0, err
	}
	var last int64
	for _, b := range blocks {
		last = max(last, b.GetPosition())
	}
	return last, nil
}

func (b *Blocks) List(
	ctx context.Context, req *connect.Request[planv1.BlockServiceListRequest],
) (*connect.Response[planv1.BlockServiceListResponse], error) {
	where := store.Where{"wish_id": req.Msg.GetWishId()}
	if id := req.Msg.GetTaskId(); id != "" {
		where["task_id"] = id
	}
	all, err := store.List[*planv1.Block](ctx, b.Store, where)
	if err != nil {
		return nil, Status(err)
	}
	res := &planv1.BlockServiceListResponse{}
	for _, block := range all {
		if kind := req.Msg.GetKind(); kind == "" || strings.EqualFold(kind, block.GetKind()) {
			res.Blocks = append(res.Blocks, block)
		}
	}
	sortBlocks(res.Blocks)
	return connect.NewResponse(res), nil
}

// sortBlocks puts blocks in their order: by position, then oldest first.
func sortBlocks(blocks []*planv1.Block) {
	slices.SortStableFunc(blocks, func(x, y *planv1.Block) int {
		return cmp.Or(cmp.Compare(x.GetPosition(), y.GetPosition()), strings.Compare(x.GetId(), y.GetId()))
	})
}

func (b *Blocks) Delete(
	ctx context.Context, req *connect.Request[planv1.BlockServiceDeleteRequest],
) (*connect.Response[planv1.BlockServiceDeleteResponse], error) {
	err := write(ctx, b.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		block, err := store.Get[*planv1.Block](ctx, tx, req.Msg.GetId())
		if err != nil {
			return err
		}
		return tx.Delete(block)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.BlockServiceDeleteResponse{}), nil
}
