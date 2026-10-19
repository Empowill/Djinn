package harness

import (
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// TestRankAzima (W134): the lead ranks the azimas of a wish (djinn task rank --top, --before, --after; djinn task
// update --rank), a new azima goes last, and on a full machine the waiting work of the higher azima starts first,
// whatever came first.
func TestRankAzima(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 1}).capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, nativeFolder(t))
	t1, t2 := e.azima(t, wishID, "Lay the ground"), e.azima(t, wishID, "Build on it")
	busy := e.mustSpawn(t, wishID, "Busy", "wait\ntext busy", nil)
	e.until(t, busy.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	low := e.mustSpawn(t, wishID, "Low", "wait\ntext low", &planv1.TaskServiceSpawnRequest{PartOf: "T1"})
	high := e.mustSpawn(t, wishID, "High", "wait\ntext high", &planv1.TaskServiceSpawnRequest{PartOf: "T2"})

	rank := func(req *planv1.TaskServiceRankRequest) ([]string, error) {
		res, err := e.tasks.Rank(t.Context(), connect.NewRequest(req))
		if err != nil {
			return nil, err
		}
		var codes []string
		for i, a := range res.Msg.GetAzimas() {
			if a.GetRank() != int32(i+1) {
				t.Errorf("%s ranked %d at place %d", a.GetCode(), a.GetRank(), i+1)
			}
			codes = append(codes, a.GetCode())
		}
		return codes, nil
	}
	stored := func() []string {
		tasks, err := store.List[*planv1.Task](t.Context(), e.h.store, store.Where{"wish_id": wishID})
		if err != nil {
			t.Fatal(err)
		}
		var codes []string
		for _, a := range plan.AzimaOrder(tasks) {
			codes = append(codes, a.GetCode())
		}
		return codes
	}
	if got, err := rank(&planv1.TaskServiceRankRequest{Azima: "t2", WishId: wishID, Top: proto.Bool(true)}); err != nil ||
		!slices.Equal(got, []string{"T2", "T1"}) {
		t.Fatalf("rank T2 --top = %v, %v", got, err)
	}

	// The slot frees: the higher azima's work starts, the older work of the lower one waits.
	e.release(t, busy.GetId())
	e.ended(t, busy.GetId())
	e.until(t, high.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	if got := e.get(t, low.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
		t.Errorf("low = %v; want it waiting behind the higher azima's work", got.GetStatus())
	}
	e.release(t, high.GetId())
	e.ended(t, high.GetId())
	e.release(t, low.GetId())
	e.ended(t, low.GetId())

	// A new azima goes last; --before and --after place it among the others; update --rank by number.
	e.azima(t, wishID, "Polish")
	if got := stored(); !slices.Equal(got, []string{"T2", "T1", "T3"}) {
		t.Errorf("with a new azima: %v, want it last", got)
	}
	if got, err := rank(&planv1.TaskServiceRankRequest{Azima: "T3", WishId: wishID, Before: "T1"}); err != nil ||
		!slices.Equal(got, []string{"T2", "T3", "T1"}) {
		t.Errorf("rank T3 --before T1 = %v, %v", got, err)
	}
	if got, err := rank(&planv1.TaskServiceRankRequest{Azima: t2.GetId(), After: t1.GetId()}); err != nil ||
		!slices.Equal(got, []string{"T3", "T1", "T2"}) {
		t.Errorf("rank T2 --after T1 = %v, %v", got, err)
	}
	update := func(id string, req *planv1.TaskServiceUpdateRequest) (*planv1.Task, error) {
		req.TaskId = id
		res, err := e.tasks.Update(t.Context(), connect.NewRequest(req))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetTask(), nil
	}
	if got, err := update(t1.GetId(), &planv1.TaskServiceUpdateRequest{Rank: proto.Int32(1)}); err != nil || got.GetRank() != 1 ||
		got.GetAzima() == nil {
		t.Errorf("update T1 --rank 1 = %v, %v", got, err)
	}
	if got := stored(); !slices.Equal(got, []string{"T1", "T3", "T2"}) {
		t.Errorf("after update --rank 1: %v", got)
	}
	if got, err := update(t1.GetId(), &planv1.TaskServiceUpdateRequest{Rank: proto.Int32(99)}); err != nil || got.GetRank() != 3 {
		t.Errorf("update T1 --rank 99 = %v, %v; want it last", got, err)
	}

	// What it refuses, saying why.
	for name, c := range map[string]struct {
		req  *planv1.TaskServiceRankRequest
		code connect.Code
		says string
	}{
		"no place":    {&planv1.TaskServiceRankRequest{Azima: "T1", WishId: wishID}, connect.CodeInvalidArgument, "one of --top"},
		"two places":  {&planv1.TaskServiceRankRequest{Azima: "T1", WishId: wishID, Top: proto.Bool(true), After: "T2"}, connect.CodeInvalidArgument, "one of --top"},
		"top false":   {&planv1.TaskServiceRankRequest{Azima: "T1", WishId: wishID, Top: proto.Bool(false)}, connect.CodeInvalidArgument, "--top=false"},
		"itself":      {&planv1.TaskServiceRankRequest{Azima: "T1", WishId: wishID, Before: "t1"}, connect.CodeInvalidArgument, "not itself"},
		"work":        {&planv1.TaskServiceRankRequest{Azima: low.GetCode(), WishId: wishID, Top: proto.Bool(true)}, connect.CodeInvalidArgument, "only azimas have a rank"},
		"before work": {&planv1.TaskServiceRankRequest{Azima: "T1", WishId: wishID, Before: low.GetCode()}, connect.CodeInvalidArgument, "is not an azima of the wish"},
	} {
		if _, err := rank(c.req); connect.CodeOf(err) != c.code || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v; want %v saying %q", name, err, c.code, c.says)
		}
	}
	if _, err := update(low.GetId(), &planv1.TaskServiceUpdateRequest{Rank: proto.Int32(1)}); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "only an azima has a rank") {
		t.Errorf("update --rank on work: %v", err)
	}
	if _, err := update(t1.GetId(), &planv1.TaskServiceUpdateRequest{Rank: proto.Int32(1), Title: proto.String("x")}); connect.CodeOf(err) != connect.CodeInvalidArgument ||
		!strings.Contains(err.Error(), "--rank goes alone") {
		t.Errorf("update --rank with a title: %v", err)
	}
}
