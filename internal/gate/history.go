package gate

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// HistoryMinutes is the window of the history of the gates when its request names none: the last hour.
const HistoryMinutes = 60

// Past lists the tasks the history of the gates joins: the kind and code of each interval's task, and the runs of the
// workers. djinn up lists them from the store.
type Past func(ctx context.Context) ([]*planv1.Task, error)

// HandlerOption sets an option of the GateService handler.
type HandlerOption func(*service)

// WithPast joins the tasks of past to the history of the gates (History). Without it, the history holds the
// intervals alone: no kinds, no runs.
func WithPast(past Past) HandlerOption { return func(s *service) { s.past = past } }

// StorePast lists the tasks of db, read again only after one changed: the Gates tab reads the history every few
// seconds, and some 3,300 tasks took 16 to 26 ms to read each time in the real store (W124), on its one connection.
// The tasks given are shared: never changed.
func StorePast(db *store.Store) Past {
	var (
		mu    sync.Mutex
		stale atomic.Bool
		tasks []*planv1.Task
	)
	stale.Store(true)
	db.OnCommit(func(ms []proto.Message) {
		for _, m := range ms {
			if _, ok := m.(*planv1.Task); ok {
				stale.Store(true)
				return
			}
		}
	})
	return func(ctx context.Context) ([]*planv1.Task, error) {
		mu.Lock()
		defer mu.Unlock()
		if stale.Swap(false) {
			read, err := store.List[*planv1.Task](ctx, db, nil)
			if err != nil {
				stale.Store(true)
				return nil, err
			}
			tasks = read
		}
		return tasks, nil
	}
}

// History gives the gate intervals released in the last minutes, then those held now, each with its task's kind and
// code, and the runs of the workers over the same window (Runs); asked after a time, only those released after it and
// the runs from it on. The intervals outlive a restart: the store keeps them for three months (machine.Intervals).
func (s *service) History(
	ctx context.Context, req *connect.Request[machinev1.GateServiceHistoryRequest],
) (*connect.Response[machinev1.GateServiceHistoryResponse], error) {
	minutes := req.Msg.GetMinutes()
	if minutes <= 0 {
		minutes = HistoryMinutes
	}
	now := time.Now()
	start := now.Add(-time.Duration(minutes) * time.Minute)
	// A read after a previous one gives only what changed since: the window keeps the rest.
	from := start
	if after := req.Msg.GetAfterTime(); after != nil && after.AsTime().After(start) {
		from = after.AsTime()
	}
	ivs := s.g.Intervals(from, req.Msg.GetAllAdmissions())
	var tasks []*planv1.Task
	if s.past != nil {
		var err error
		if tasks, err = s.past(ctx); err != nil {
			return nil, plan.Status(err)
		}
	}
	return connect.NewResponse(&machinev1.GateServiceHistoryResponse{
		StartTime: timestamppb.New(start), ReadTime: timestamppb.New(now),
		Intervals: Joined(ivs, tasks), Runs: Runs(tasks, from, now),
	}), nil
}

// Joined gives copies of ivs with the kind and code of their task among tasks; an interval of no task, or of one no
// longer there, has none. The intervals themselves are left as they are: the log in memory shares them.
func Joined(ivs []*machinev1.GateInterval, tasks []*planv1.Task) []*machinev1.GateInterval {
	byID := make(map[string]*planv1.Task, len(tasks))
	for _, t := range tasks {
		byID[t.GetId()] = t
	}
	out := make([]*machinev1.GateInterval, 0, len(ivs))
	for _, iv := range ivs {
		iv = proto.CloneOf(iv)
		if t, ok := byID[iv.GetTaskId()]; ok && iv.GetTaskId() != "" {
			iv.TaskKind, iv.TaskCode = plan.Kind(t), t.GetCode()
		}
		out = append(out, iv)
	}
	return out
}

// Runs gives the runs of the tasks' workers that overlap the window from start to now, the first started first. A run
// with no end counts only while its task runs, and only as its last run: an ended task can keep a run never closed
// (W5, W16 to W27 in October), which would draw a worker running ever since.
func Runs(tasks []*planv1.Task, start, now time.Time) []*machinev1.WorkerRun {
	var out []*machinev1.WorkerRun
	for _, t := range tasks {
		running := t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING
		for i, r := range t.GetRuns() {
			if r.GetStartTime() == nil || !r.GetStartTime().AsTime().Before(now) {
				continue
			}
			if r.GetEndTime() == nil {
				if !running || i != len(t.GetRuns())-1 {
					continue
				}
			} else if !r.GetEndTime().AsTime().After(start) {
				continue
			}
			out = append(out, &machinev1.WorkerRun{
				TaskId: t.GetId(), Code: t.GetCode(), Kind: plan.Kind(t),
				StartTime: r.GetStartTime(), EndTime: r.GetEndTime(),
			})
		}
	}
	slices.SortStableFunc(out, func(a, b *machinev1.WorkerRun) int {
		return a.GetStartTime().AsTime().Compare(b.GetStartTime().AsTime())
	})
	return out
}
