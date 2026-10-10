package machine

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
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

// Add counts a run in what the command costs: the means move, the peak keeps the highest.
func Add(c *machinev1.CommandCost, run *machinev1.CommandServiceRecordRequest) {
	c.Runs++
	n := float64(c.GetRuns())
	c.CpuSeconds += (run.GetCpuSeconds() - c.GetCpuSeconds()) / n
	c.Seconds += (run.GetSeconds() - c.GetSeconds()) / n
	c.PeakMemoryBytes = max(c.GetPeakMemoryBytes(), run.GetPeakMemoryBytes())
	c.LastCpuSeconds, c.LastSeconds, c.LastPeakMemoryBytes = run.GetCpuSeconds(), run.GetSeconds(), run.GetPeakMemoryBytes()
	c.LastExitCode, c.LastTime = run.GetExitCode(), timestamppb.Now()
}

// project is the project the command ran in: the task's, or the one whose folder holds dir, the deepest first; ""
// outside any project.
func (c *costs) project(ctx context.Context, taskID, dir string) (string, error) {
	if taskID != "" {
		t, err := store.Get[*planv1.Task](ctx, c.db, taskID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return "", err
		}
		if id := t.GetProjectId(); id != "" {
			return id, nil
		}
	}
	if dir == "" || !filepath.IsAbs(dir) {
		return "", nil
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	projects, err := store.List[*planv1.Project](ctx, c.db, nil)
	if err != nil {
		return "", err
	}
	best, depth := "", -1
	for _, p := range projects {
		if p.GetDirectory() == "" {
			continue
		}
		rel, err := filepath.Rel(p.GetDirectory(), dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if d := len(p.GetDirectory()); d > depth {
			best, depth = p.GetId(), d
		}
	}
	return best, nil
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
	slices.SortStableFunc(all, func(a, b *machinev1.CommandCost) int {
		return cmp.Or(strings.Compare(a.GetProjectId(), b.GetProjectId()), cmp.Compare(b.GetRuns(), a.GetRuns()),
			strings.Compare(a.GetCommand(), b.GetCommand()))
	})
	return connect.NewResponse(&machinev1.CommandServiceListResponse{Costs: all}), nil
}
