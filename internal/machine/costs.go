package machine

import (
	"cmp"
	"context"
	"errors"
	"math"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// CostsHandler returns the Connect handler of CommandService on the store, and its path prefix: what the commands
// of each project cost, as djinn gate run measures them.
func CostsHandler(db *store.Store) (string, http.Handler) {
	return machinev1connect.NewCommandServiceHandler(&costs{db: db}, connect.WithInterceptors(plan.Validate))
}

type costs struct {
	machinev1connect.UnimplementedCommandServiceHandler
	db *store.Store
}

func (c *costs) Record(
	ctx context.Context, req *connect.Request[machinev1.CommandServiceRecordRequest],
) (*connect.Response[machinev1.CommandServiceRecordResponse], error) {
	in := req.Msg
	projectID, err := c.project(ctx, in.GetTaskId(), in.GetDirectory())
	if err != nil || projectID == "" {
		return connect.NewResponse(&machinev1.CommandServiceRecordResponse{}), plan.Status(err)
	}
	var out *machinev1.CommandCost
	err = c.db.Tx(ctx, func(tx *store.Tx) error {
		all, err := store.List[*machinev1.CommandCost](ctx, tx, store.Where{"project_id": projectID})
		if err != nil {
			return err
		}
		if out = find(all, in.GetCommand()); out == nil {
			out = &machinev1.CommandCost{Id: store.NewID(), ProjectId: projectID, Command: in.GetCommand()}
		}
		Add(out, in)
		if err := tx.Journal("local", machinev1connect.CommandServiceRecordProcedure, in); err != nil {
			return err
		}
		return tx.Put(out)
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	return connect.NewResponse(&machinev1.CommandServiceRecordResponse{Cost: out}), nil
}

// find is the cost of the command among all, case ignored; nil when it is not there.
func find(all []*machinev1.CommandCost, command string) *machinev1.CommandCost {
	if i := slices.IndexFunc(all, func(o *machinev1.CommandCost) bool { return strings.EqualFold(o.GetCommand(), command) }); i >= 0 {
		return all[i]
	}
	return nil
}

// Peaks reads the highest peak memory measured of each command, for the gates.
type Peaks struct{ db *store.Store }

// NewPeaks reads the peaks in the store.
func NewPeaks(db *store.Store) *Peaks { return &Peaks{db: db} }

// Peak is the highest peak memory, in bytes, of the command in its project: the task's, else the one holding dir.
// It is 0 when the command was never measured, or outside any project.
func (p *Peaks) Peak(ctx context.Context, taskID, dir, command string) uint64 {
	c := &costs{db: p.db}
	projectID, err := c.project(ctx, taskID, dir)
	if err != nil || projectID == "" {
		return 0
	}
	all, err := store.List[*machinev1.CommandCost](ctx, p.db, store.Where{"project_id": projectID})
	if err != nil {
		return 0
	}
	return find(all, command).GetPeakMemoryBytes()
}

// Ask counts a run asked of the command in its project: the task's, else the one holding dir; the hold of gate asked
// is journaled. Nothing is counted outside any project, nor when the store fails: the gate is granted all the same.
func (p *Peaks) Ask(ctx context.Context, gate, taskID, dir, command string) {
	c := &costs{db: p.db}
	projectID, err := c.project(ctx, taskID, dir)
	if err != nil || projectID == "" {
		return
	}
	if err := p.db.Tx(ctx, func(tx *store.Tx) error {
		all, err := store.List[*machinev1.CommandCost](ctx, tx, store.Where{"project_id": projectID})
		if err != nil {
			return err
		}
		out := find(all, command)
		if out == nil {
			out = &machinev1.CommandCost{Id: store.NewID(), ProjectId: projectID, Command: command}
		}
		out.Asked = max(out.GetAsked(), out.GetRuns()) + 1
		hold := &machinev1.GateServiceHoldRequest{Name: gate, TaskId: taskID, What: command, Directory: dir}
		if err := tx.Journal("local", machinev1connect.GateServiceHoldProcedure, hold); err != nil {
			return err
		}
		return tx.Put(out)
	}); err != nil {
		logx.L().Warn("journal a gate hold", "gate", gate, "task", taskID, "command", logx.Cut(command, 80), "err", err)
	}
}

// percentile calculates the p-th percentile (0.0 to 1.0) using linear interpolation.
func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := slices.Clone(vals)
	slices.Sort(sorted)
	if len(sorted) == 1 {
		return sorted[0]
	}
	idx := p * float64(len(sorted)-1)
	low := int(math.Floor(idx))
	high := int(math.Ceil(idx))
	if low == high {
		return sorted[low]
	}
	weight := idx - float64(low)
	return sorted[low]*(1-weight) + sorted[high]*weight
}

// Add counts a run in what the command costs: the means move, the peak keeps the highest,
// wait and held times are tracked, percentiles computed, and recent runs recorded (up to 200).
func Add(c *machinev1.CommandCost, run *machinev1.CommandServiceRecordRequest) {
	c.Runs++
	// A cost from before runs were counted as asked: its runs were asked too.
	c.Asked = max(c.GetAsked(), c.GetRuns())
	n := float64(c.GetRuns())
	c.CpuSeconds += (run.GetCpuSeconds() - c.GetCpuSeconds()) / n
	c.Seconds += (run.GetSeconds() - c.GetSeconds()) / n
	c.PeakMemoryBytes = max(c.GetPeakMemoryBytes(), run.GetPeakMemoryBytes())
	c.LastCpuSeconds, c.LastSeconds, c.LastPeakMemoryBytes = run.GetCpuSeconds(), run.GetSeconds(), run.GetPeakMemoryBytes()
	c.LastExitCode, c.LastTime = run.GetExitCode(), timestamppb.Now()

	if run.GetGate() != "" {
		c.Gate = run.GetGate()
	}
	c.LastWaitSeconds = run.GetWaitSeconds()
	c.LastHeldSeconds = run.GetHeldSeconds()

	c.RecentRuns = append(c.RecentRuns, &machinev1.CommandRun{
		Time:            timestamppb.Now(),
		Seconds:         run.GetSeconds(),
		CpuSeconds:      run.GetCpuSeconds(),
		PeakMemoryBytes: run.GetPeakMemoryBytes(),
		ExitCode:        run.GetExitCode(),
		WaitSeconds:     run.GetWaitSeconds(),
		HeldSeconds:     run.GetHeldSeconds(),
		Gate:            run.GetGate(),
		TaskId:          run.GetTaskId(),
		Tree:            run.GetTree(),
	})
	const maxRecent = 200
	if len(c.RecentRuns) > maxRecent {
		c.RecentRuns = c.RecentRuns[len(c.RecentRuns)-maxRecent:]
	}

	recentHeld := make([]float64, len(c.RecentRuns))
	heldVals := make([]float64, len(c.RecentRuns))
	waitVals := make([]float64, len(c.RecentRuns))
	for i, r := range c.RecentRuns {
		recentHeld[i] = r.GetHeldSeconds()
		heldVals[i] = r.GetHeldSeconds()
		waitVals[i] = r.GetWaitSeconds()
	}
	c.RecentHeldSeconds = recentHeld

	c.P50HeldSeconds = percentile(heldVals, 0.50)
	c.P95HeldSeconds = percentile(heldVals, 0.95)
	c.P50WaitSeconds = percentile(waitVals, 0.50)
	c.P95WaitSeconds = percentile(waitVals, 0.95)
}

// Project gives the ID and name of the project the command ran in: the task's, or the one whose folder holds
// dir, the deepest first; "", "" outside any project.
func Project(ctx context.Context, db *store.Store, taskID, dir string) (id, name string, err error) {
	if db == nil {
		return "", "", nil
	}
	if taskID != "" {
		t, err := store.Get[*planv1.Task](ctx, db, taskID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return "", "", err
		}
		if pid := t.GetProjectId(); pid != "" {
			p, err := store.Get[*planv1.Project](ctx, db, pid)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return pid, "", err
			}
			if p != nil {
				return pid, p.GetName(), nil
			}
			return pid, "", nil
		}
	}
	if dir == "" || !filepath.IsAbs(dir) {
		return "", "", nil
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	projects, err := store.List[*planv1.Project](ctx, db, nil)
	if err != nil {
		return "", "", err
	}
	var best *planv1.Project
	depth := -1
	for _, p := range projects {
		if p.GetDirectory() == "" {
			continue
		}
		rel, err := filepath.Rel(p.GetDirectory(), dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if d := len(p.GetDirectory()); d > depth {
			best, depth = p, d
		}
	}
	if best != nil {
		return best.GetId(), best.GetName(), nil
	}
	return "", "", nil
}

// project is the project the command ran in: the task's, or the one whose folder holds dir, the deepest first; ""
// outside any project.
func (c *costs) project(ctx context.Context, taskID, dir string) (string, error) {
	id, _, err := Project(ctx, c.db, taskID, dir)
	return id, err
}

func (c *costs) List(
	ctx context.Context, req *connect.Request[machinev1.CommandServiceListRequest],
) (*connect.Response[machinev1.CommandServiceListResponse], error) {
	where := store.Where{}
	if name := req.Msg.GetProject(); name != "" {
		p, err := plan.ProjectNamed(ctx, c.db, name)
		if err != nil {
			return nil, plan.Status(err)
		}
		where["project_id"] = p.GetId()
	}
	all, err := store.List[*machinev1.CommandCost](ctx, c.db, where)
	if err != nil {
		return nil, plan.Status(err)
	}
	projects, _ := store.List[*planv1.Project](ctx, c.db, nil)
	projNames := make(map[string]string)
	for _, p := range projects {
		projNames[p.GetId()] = p.GetName()
	}
	for _, cost := range all {
		if name, ok := projNames[cost.GetProjectId()]; ok {
			cost.ProjectName = name
		}
	}
	slices.SortStableFunc(all, func(a, b *machinev1.CommandCost) int {
		return cmp.Or(
			cmp.Compare(b.GetP50HeldSeconds(), a.GetP50HeldSeconds()),
			cmp.Compare(b.GetLastHeldSeconds(), a.GetLastHeldSeconds()),
			cmp.Compare(b.GetSeconds(), a.GetSeconds()),
			cmp.Compare(b.GetRuns(), a.GetRuns()),
			strings.Compare(a.GetCommand(), b.GetCommand()),
		)
	})
	return connect.NewResponse(&machinev1.CommandServiceListResponse{Costs: all}), nil
}
