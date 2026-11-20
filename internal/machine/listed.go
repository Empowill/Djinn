package machine

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// LongWait is the wait for a gate, in seconds, past which a worker notices it: the Gates tab counts those apart.
const LongWait = 5

// listed is every command as List reads it, with its project's name, in both orders, and each without its recent
// runs: the Gates tab reads a page every few seconds, and the commands change only when one ends (W124: 5,348 commands
// weighed 2 MB in the real store, read and sent whole each time).
type listed struct {
	held    []*machinev1.CommandCost // the longest held first
	byTotal []*machinev1.CommandCost // the most total time first
	brief   map[*machinev1.CommandCost]*machinev1.CommandCost
}

// committed drops the commands read when a command or a project changed: the next List reads them again.
func (c *costs) committed(ms []proto.Message) {
	for _, m := range ms {
		switch m.(type) {
		case *machinev1.CommandCost, *planv1.Project:
			c.stale.Store(true)
			return
		}
	}
}

// read gives the commands, read again from the store only after one changed. The messages are shared: never changed.
func (c *costs) read(ctx context.Context) (*listed, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.stale.Swap(false) && c.listed != nil {
		return c.listed, nil
	}
	all, err := store.List[*machinev1.CommandCost](ctx, c.db, nil)
	if err == nil {
		err = c.named(ctx, all...)
	}
	if err != nil {
		c.stale.Store(true)
		return nil, err
	}
	slices.SortStableFunc(all, byHeld)
	l := &listed{held: all, byTotal: slices.Clone(all), brief: make(map[*machinev1.CommandCost]*machinev1.CommandCost, len(all))}
	slices.SortStableFunc(l.byTotal, func(a, b *machinev1.CommandCost) int {
		return cmp.Or(cmp.Compare(total(b), total(a)), strings.Compare(a.GetCommand(), b.GetCommand()))
	})
	for _, cost := range all {
		l.brief[cost] = brief(cost)
	}
	c.listed = l
	return l, nil
}

// named gives each cost its project's short name.
func (c *costs) named(ctx context.Context, all ...*machinev1.CommandCost) error {
	projects, err := store.List[*planv1.Project](ctx, c.db, nil)
	if err != nil {
		return err
	}
	names := make(map[string]string, len(projects))
	for _, p := range projects {
		names[p.GetId()] = p.GetName()
	}
	for _, cost := range all {
		if name, ok := names[cost.GetProjectId()]; ok {
			cost.ProjectName = name
		}
	}
	return nil
}

// byHeld is List's order when not asked by total: the longest held first.
func byHeld(a, b *machinev1.CommandCost) int {
	return cmp.Or(
		cmp.Compare(b.GetP50HeldSeconds(), a.GetP50HeldSeconds()),
		cmp.Compare(b.GetLastHeldSeconds(), a.GetLastHeldSeconds()),
		cmp.Compare(b.GetSeconds(), a.GetSeconds()),
		cmp.Compare(b.GetRuns(), a.GetRuns()),
		strings.Compare(a.GetCommand(), b.GetCommand()),
	)
}

// total is the time a command's runs took: its runs times their mean duration.
func total(c *machinev1.CommandCost) float64 { return float64(c.GetRuns()) * c.GetSeconds() }

// brief is a copy of c without its recent runs.
func brief(c *machinev1.CommandCost) *machinev1.CommandCost {
	runs, held := c.RecentRuns, c.RecentHeldSeconds
	c.RecentRuns, c.RecentHeldSeconds = nil, nil
	out := proto.CloneOf(c)
	c.RecentRuns, c.RecentHeldSeconds = runs, held
	return out
}

func (c *costs) List(
	ctx context.Context, req *connect.Request[machinev1.CommandServiceListRequest],
) (*connect.Response[machinev1.CommandServiceListResponse], error) {
	in := req.Msg
	if id := in.GetId(); id != "" {
		cost, err := store.Get[*machinev1.CommandCost](ctx, c.db, id)
		if err == nil {
			err = c.named(ctx, cost)
		}
		if err != nil {
			return nil, plan.Status(err)
		}
		return connect.NewResponse(&machinev1.CommandServiceListResponse{Costs: []*machinev1.CommandCost{cost}, Total: 1}), nil
	}
	from := 0
	if token := in.GetPageToken(); token != "" {
		n, err := strconv.Atoi(token)
		if err != nil || n < 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("page_token: not a token List gave"))
		}
		from = n
	}
	projectID := ""
	if name := in.GetProject(); name != "" {
		p, err := plan.ProjectNamed(ctx, c.db, name)
		if err != nil {
			return nil, plan.Status(err)
		}
		projectID = p.GetId()
	}
	l, err := c.read(ctx)
	if err != nil {
		return nil, plan.Status(err)
	}
	all := l.held
	if in.GetByTotal() {
		all = l.byTotal
	}
	if command := in.GetCommand(); projectID != "" || command != "" {
		all = slices.DeleteFunc(slices.Clone(all), func(cost *machinev1.CommandCost) bool {
			return projectID != "" && cost.GetProjectId() != projectID ||
				command != "" && !strings.EqualFold(cost.GetCommand(), command)
		})
	}
	out := &machinev1.CommandServiceListResponse{Total: int32(len(all))}
	if in.GetSummary() {
		out.Summary = Summary(all)
		if p := out.GetSummary().GetPeak(); p != nil {
			out.Summary.Peak = l.brief[p]
		}
	}
	page := all[min(from, len(all)):]
	if n := int(in.GetPageSize()); n > 0 && n < len(page) {
		page = page[:n]
		out.NextPageToken = strconv.Itoa(from + n)
	}
	out.Costs = make([]*machinev1.CommandCost, len(page))
	for i, cost := range page {
		if in.GetBrief() {
			cost = l.brief[cost]
		}
		out.Costs[i] = cost
	}
	return connect.NewResponse(out), nil
}

// Summary is what the recent runs of costs say of the gates: the requests, the waits, the highest peak and the waits
// for each gate in each project (its command's). A run counts under its gate, else its command's; one under none is
// left out. Its peak is one of costs itself, its runs kept: List sends it brief.
func Summary(costs []*machinev1.CommandCost) *machinev1.GatesSummary {
	out := &machinev1.GatesSummary{}
	type gateRuns struct {
		waits []float64
		last  *machinev1.CommandRun
		cmd   string
	}
	type key struct{ gate, project string }
	byGate := map[key]*gateRuns{}
	var waits []float64
	for _, cost := range costs {
		out.Asked += max(cost.GetAsked(), cost.GetRuns())
		out.Ran += cost.GetRuns()
		if cost.GetPeakMemoryBytes() > out.GetPeak().GetPeakMemoryBytes() {
			out.Peak = cost
		}
		for _, run := range cost.GetRecentRuns() {
			gate := cmp.Or(run.GetGate(), cost.GetGate())
			if gate == "" {
				continue
			}
			k := key{gate, cost.GetProjectName()}
			g := byGate[k]
			if g == nil {
				g = &gateRuns{}
				byGate[k] = g
			}
			g.waits = append(g.waits, run.GetWaitSeconds())
			if g.last == nil || run.GetTime().AsTime().After(g.last.GetTime().AsTime()) {
				g.last, g.cmd = run, cost.GetCommand()
			}
			waits = append(waits, run.GetWaitSeconds())
		}
	}
	slices.Sort(waits)
	out.Holds = int32(len(waits))
	out.P50WaitSeconds, out.P90WaitSeconds = quantile(waits, 0.5), quantile(waits, 0.9)
	out.MaxWaitSeconds, out.LongWaits = last(waits), longOf(waits)
	for k, g := range byGate {
		slices.Sort(g.waits)
		out.Gates = append(out.Gates, &machinev1.GateWaits{
			Gate: k.gate, Project: k.project, Holds: int32(len(g.waits)),
			P50Seconds: quantile(g.waits, 0.5), P90Seconds: quantile(g.waits, 0.9),
			MaxSeconds: last(g.waits), Long: longOf(g.waits), Last: g.last, LastCommand: g.cmd,
		})
	}
	slices.SortFunc(out.Gates, func(a, b *machinev1.GateWaits) int {
		return cmp.Or(cmp.Compare(b.GetHolds(), a.GetHolds()), strings.Compare(a.GetGate(), b.GetGate()),
			strings.Compare(a.GetProject(), b.GetProject()))
	})
	return out
}

// quantile is the nearest-rank quantile of sorted, ascending; 0 for none.
func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[min(len(sorted)-1, max(0, int(math.Ceil(q*float64(len(sorted))))-1))]
}

func last(sorted []float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[len(sorted)-1]
}

// longOf counts the waits over LongWait among sorted.
func longOf(sorted []float64) int32 {
	i, _ := slices.BinarySearch(sorted, math.Nextafter(LongWait, math.Inf(1)))
	return int32(len(sorted) - i)
}
