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
	"time"

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
	key := Key(in.GetCommand(), in.GetDirectory())
	err = c.db.Tx(ctx, func(tx *store.Tx) error {
		all, err := store.List[*machinev1.CommandCost](ctx, tx, store.Where{"project_id": projectID})
		if err != nil {
			return err
		}
		if out = find(all, key); out == nil {
			out = &machinev1.CommandCost{Id: store.NewID(), ProjectId: projectID}
		}
		if !strings.EqualFold(out.GetCommand(), key) {
			out.Command = key
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

// find is the cost of the command whose key is key among all, case ignored: the one kept under key, else one kept
// under a command of that key, from before the keys were normalized (it then takes key, once counted again); nil when
// there is none.
func find(all []*machinev1.CommandCost, key string) *machinev1.CommandCost {
	if i := slices.IndexFunc(all, func(o *machinev1.CommandCost) bool { return strings.EqualFold(o.GetCommand(), key) }); i >= 0 {
		return all[i]
	}
	if i := slices.IndexFunc(all, func(o *machinev1.CommandCost) bool { return strings.EqualFold(Key(o.GetCommand()), key) }); i >= 0 {
		return all[i]
	}
	return nil
}

// byHead is what the commands of all with the head of key cost, key's own left out: the median of their highest peaks,
// and of their CPU times, with how many they are; zero when there is none.
func byHead(all []*machinev1.CommandCost, key string) (Cost, int) {
	head := Head(key)
	if head == "" {
		return Cost{}, 0
	}
	var peaks, cpus []float64
	for _, o := range all {
		if o.GetRuns() == 0 || strings.EqualFold(o.GetCommand(), key) {
			continue
		}
		if h := Head(Key(o.GetCommand())); !strings.EqualFold(h, head) {
			continue
		}
		peaks = append(peaks, float64(o.GetPeakMemoryBytes()))
		cpus = append(cpus, max(o.GetCpuSeconds(), o.GetLastCpuSeconds()))
	}
	if len(peaks) == 0 {
		return Cost{}, 0
	}
	return Cost{
		Peak: uint64(percentile(peaks, 0.5)), CPU: time.Duration(percentile(cpus, 0.5) * float64(time.Second)), Head: head,
	}, len(peaks)
}

// Peaks reads what each command was measured to cost, for the gates.
type Peaks struct{ db *store.Store }

// NewPeaks reads the costs in the store.
func NewPeaks(db *store.Store) *Peaks { return &Peaks{db: db} }

// Cost is what a command was measured to cost: its highest peak memory, in bytes, and its CPU time, user and system,
// the higher of its mean and its last run's; how long it usually holds its gate, the 95th percentile of its last runs.
// A command never measured is given the cost of the others of its head in its project (Head), their median: Head
// then names it, and Held is zero.
type Cost struct {
	Peak uint64
	CPU  time.Duration
	Held time.Duration
	Head string
}

// Cost is what the command was measured to cost in its project: the task's, else the one holding dir, found by its key
// (Key); never measured, what the others of its head cost. It is zero when neither was measured, or outside any
// project.
func (p *Peaks) Cost(ctx context.Context, taskID, dir, command string) Cost {
	c := &costs{db: p.db}
	projectID, err := c.project(ctx, taskID, dir)
	if err != nil || projectID == "" {
		return Cost{}
	}
	all, err := store.List[*machinev1.CommandCost](ctx, p.db, store.Where{"project_id": projectID})
	if err != nil {
		return Cost{}
	}
	key := Key(command, dir)
	found := find(all, key)
	if found.GetRuns() == 0 {
		guess, _ := byHead(all, key)
		return guess
	}
	cpu := max(found.GetCpuSeconds(), found.GetLastCpuSeconds())
	return Cost{
		Peak: found.GetPeakMemoryBytes(), CPU: time.Duration(cpu * float64(time.Second)),
		Held: time.Duration(found.GetP95HeldSeconds() * float64(time.Second)),
	}
}

// Record counts a run of a command that Djinn measured itself, as djinn gate run would send it: a worker's command
// admitted by the gates (djinn gate hook). Nothing is counted outside any project; a failure is logged.
func (p *Peaks) Record(ctx context.Context, run *machinev1.CommandServiceRecordRequest) {
	if _, err := (&costs{db: p.db}).Record(ctx, connect.NewRequest(run)); err != nil {
		logx.L().Warn("record an admitted command", "task", run.GetTaskId(), "command", logx.Cut(run.GetCommand(), 80), "err", err)
	}
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
		key := Key(command, dir)
		out := find(all, key)
		if out == nil {
			out = &machinev1.CommandCost{Id: store.NewID(), ProjectId: projectID}
		}
		if !strings.EqualFold(out.GetCommand(), key) {
			out.Command = key
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
