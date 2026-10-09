package harness

// Azimas are the tasks of kind AZIMA: the plan of a wish as a graph, which no worker runs and the scheduler never
// starts (internal/plan/azimas.go). The lead makes one with djinn task spawn --kind azima, or reads them from a
// project's plan files with djinn plan sync (PlanService).

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// spawnAzima makes an azima of the wish: a task of kind AZIMA, coded T1, T2… after the highest one, which no worker
// runs. What only a worker uses is refused.
func (h *Harness) spawnAzima(ctx context.Context, procedure string, req *planv1.TaskServiceSpawnRequest) (*planv1.Task, error) {
	if req.GetPrompt() != "" || req.GetProvider() != planv1.Provider_PROVIDER_UNSPECIFIED || req.GetModel() != "" ||
		req.GetMaxBudgetUsd() != 0 || len(req.GetWriteScopes()) > 0 || req.GetLater() || req.GetFork() != "" ||
		req.GetFromLead() || req.GetRestart() {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(
			"an azima has no worker: no prompt, provider, model, budget, write scope, --later, --fork, --from-lead or --restart"))
	}
	h.sched.Lock()
	defer h.sched.Unlock()
	wish, err := store.Get[*planv1.Wish](ctx, h.store, req.GetWishId())
	if err != nil {
		return nil, plan.Status(err)
	}
	project, err := pickProject(ctx, h.store, wish, req.GetProjectId())
	if err != nil {
		return nil, plan.Status(err)
	}
	task := &planv1.Task{
		Id: store.NewID(), WishId: wish.GetId(), ProjectId: project.GetId(), Title: req.GetTitle(),
		Status: planv1.TaskStatus_TASK_STATUS_PENDING, Kind: planv1.TaskKind_TASK_KIND_AZIMA, CreateTime: timestamppb.Now(),
	}
	if task.DependsOn, err = resolveDeps(ctx, h.store, wish.GetId(), req.GetDependsOn()); err != nil {
		return nil, plan.Status(err)
	}
	if task.PartOf, err = resolveAzima(ctx, h.store, wish.GetId(), req.GetPartOf()); err != nil {
		return nil, plan.Status(err)
	}
	if ref := req.GetDecision(); ref != "" {
		if task.Decision, err = plan.Decision(ctx, h.store, wish.GetId(), ref); err != nil {
			return nil, plan.Status(err)
		}
	}
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		tasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": wish.GetId()})
		if err != nil {
			return err
		}
		task.Code = nextAzimaCode(tasks)
		return tx.Put(task)
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.notify()
	return task, nil
}

// azimaCode is the code of an azima: T1, T07.
var azimaCode = regexp.MustCompile(`^[Tt][0-9]+$`)

// nextAzimaCode is the code of a new azima among tasks, the wish's: T1, T2…, after the highest one.
func nextAzimaCode(tasks []*planv1.Task) string {
	last := 0
	for _, t := range tasks {
		var n int
		if _, err := fmt.Sscanf(t.GetCode(), "T%d", &n); err == nil && n > last {
			last = n
		}
	}
	return fmt.Sprintf("T%d", last+1)
}

// storedAsWork tells whether a task of no kind is an azima stored before tasks had kinds: imported from a plan, never
// planned on this machine, never started, with no agent and no session.
func storedAsWork(t *planv1.Task) bool {
	return t.GetKind() == planv1.TaskKind_TASK_KIND_UNSPECIFIED && !t.GetScheduled() && t.GetStartTime() == nil &&
		t.GetSessionId() == "" && t.GetProvider() == planv1.Provider_PROVIDER_UNSPECIFIED
}

// migrateAzimas marks as azimas the tasks a wish holds from its plan files since before tasks had kinds: a code T1,
// T07…, stored as work that no worker ever ran (storedAsWork). Each one is journaled. A task Djinn ran stays work.
func (h *Harness) migrateAzimas(ctx context.Context, tasks []*planv1.Task) error {
	for _, t := range tasks {
		if !azimaCode.MatchString(t.GetCode()) || !storedAsWork(t) {
			continue
		}
		t.Kind = planv1.TaskKind_TASK_KIND_AZIMA
		err := h.store.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal(actorHarness, methodAzima, t); err != nil {
				return err
			}
			return tx.Put(t)
		})
		if err != nil {
			return fmt.Errorf("mark %s an azima: %w", t.GetCode(), err)
		}
	}
	return nil
}

// withAzimas is tasks with Task.azima set on their azimas, from every task of the azimas' wishes.
func (h *Harness) withAzimas(ctx context.Context, tasks []*planv1.Task) ([]*planv1.Task, error) {
	var wishes []string
	for _, t := range tasks {
		if plan.IsAzima(t) && !slices.Contains(wishes, t.GetWishId()) {
			wishes = append(wishes, t.GetWishId())
		}
	}
	if len(wishes) == 0 {
		return tasks, nil
	}
	filled := map[string]*planv1.Task{}
	for _, id := range wishes {
		all, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": id})
		if err != nil {
			return nil, err
		}
		for _, t := range plan.WithAzimas(all) {
			if plan.IsAzima(t) {
				filled[t.GetId()] = t
			}
		}
	}
	out := slices.Clone(tasks)
	for i, t := range out {
		if f := filled[t.GetId()]; f != nil {
			out[i] = f
		}
	}
	return out, nil
}

// PlanHandler returns the Connect handler of PlanService on h, and its path prefix.
func PlanHandler(h *Harness) (string, http.Handler) {
	return planv1connect.NewPlanServiceHandler(&Plans{h: h}, connect.WithInterceptors(plan.Validate))
}

// Plans implements PlanService.
type Plans struct {
	planv1connect.UnimplementedPlanServiceHandler
	h *Harness
}

func (s *Plans) Sync(
	ctx context.Context, req *connect.Request[planv1.PlanServiceSyncRequest],
) (*connect.Response[planv1.PlanServiceSyncResponse], error) {
	res, err := s.h.SyncPlan(ctx, req.Spec().Procedure, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

// planSource is the plan files of one project.
type planSource struct {
	project *planv1.Project
	files   []plan.AzimaFile
}

// SyncPlan reads the plan files of the wish's projects into its azimas, then writes what each azima depends on back
// into its file (PlanService.Sync). An azima is found by its file's id, else its code; one the files name and the
// store lacks is made, taking its file's after line once. What an azima depends on is the store's: the files only
// follow it.
func (h *Harness) SyncPlan(ctx context.Context, procedure string, req *planv1.PlanServiceSyncRequest) (*planv1.PlanServiceSyncResponse, error) {
	wish, err := store.Get[*planv1.Wish](ctx, h.store, req.GetWishId())
	if err != nil {
		return nil, plan.Status(err)
	}
	ids := wish.GetProjectIds()
	if id := req.GetProjectId(); id != "" {
		if !slices.Contains(ids, id) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("project %s is not one of the wish's projects", id))
		}
		ids = []string{id}
	}
	var sources []planSource
	for _, id := range ids {
		p, err := store.Get[*planv1.Project](ctx, h.store, id)
		if err != nil {
			return nil, plan.Status(err)
		}
		if p.GetDirectory() == "" {
			continue
		}
		files, err := plan.ReadAzimaFiles(p.GetDirectory())
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("project %s: %w", p.GetName(), err))
		}
		if len(files) > 0 {
			sources = append(sources, planSource{p, files})
		}
	}
	if len(sources) == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(
			"no project of the wish has plan files here: plan/*.md, each with a front matter that gives its code"))
	}

	h.sched.Lock()
	defer h.sched.Unlock()
	res := &planv1.PlanServiceSyncResponse{}
	var tasks []*planv1.Task
	// Each file's azima, in the order of the files.
	type read struct {
		file  plan.AzimaFile
		dir   string
		azima *planv1.Task
		after []string // for an azima made here
	}
	var reads []*read
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		if tasks, err = store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": wish.GetId()}); err != nil {
			return err
		}
		now := timestamppb.Now()
		codes := map[string]string{}
		var changed []*planv1.Task
		for _, src := range sources {
			for _, f := range src.files {
				key := strings.ToUpper(f.Code)
				if other, ok := codes[key]; ok {
					return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s and %s both give the code %s", other, f.Path, f.Code))
				}
				codes[key] = f.Path
				r := &read{file: f, dir: src.project.GetDirectory()}
				i := slices.IndexFunc(tasks, func(t *planv1.Task) bool { return f.ID != "" && strings.EqualFold(t.GetId(), f.ID) })
				if i < 0 {
					i = slices.IndexFunc(tasks, func(t *planv1.Task) bool { return strings.EqualFold(t.GetCode(), f.Code) })
				}
				var before *planv1.Task
				if i >= 0 {
					r.azima, before = tasks[i], proto.CloneOf(tasks[i])
					if !plan.IsAzima(r.azima) && !storedAsWork(r.azima) {
						return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
							"%s gives the code %s, which is work of the wish, not an azima", f.Path, r.azima.GetCode()))
					}
				} else {
					id, err := azimaID(ctx, tx, f.ID)
					if err != nil {
						return err
					}
					r.azima = &planv1.Task{
						Id: id, WishId: wish.GetId(), ProjectId: src.project.GetId(), Code: f.Code,
						Status: planv1.TaskStatus_TASK_STATUS_PENDING, CreateTime: now,
					}
					r.after = f.After
					tasks = append(tasks, r.azima)
				}
				t := r.azima
				t.Kind, t.Title, t.Phase, t.PlanFile = planv1.TaskKind_TASK_KIND_AZIMA, cmp.Or(f.Title, t.GetTitle(), f.Code), f.Phase, f.Path
				if t.GetProjectId() == "" {
					t.ProjectId = src.project.GetId()
				}
				switch {
				case f.Done() && t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE:
					t.Status, t.WaitReason, t.EndTime = planv1.TaskStatus_TASK_STATUS_DONE, "", now
					t.Closed = &planv1.Closure{Actor: planv1.Closer_CLOSER_PLAN_FILE, CreateTime: now, Note: f.Path + " says done"}
				case !f.Done() && t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE &&
					t.GetClosed().GetActor() == planv1.Closer_CLOSER_PLAN_FILE:
					t.Status, t.EndTime, t.Closed = planv1.TaskStatus_TASK_STATUS_PENDING, nil, nil
				}
				switch {
				case before == nil:
					res.Made = append(res.Made, t.GetCode())
					changed = append(changed, t)
				case !proto.Equal(before, t):
					res.Changed = append(res.Changed, t.GetCode())
					changed = append(changed, t)
				}
				reads = append(reads, r)
			}
		}
		// An azima made here takes its file's after line, once: the store holds the graph from then on. A code the
		// wish does not have is left out.
		for _, r := range reads {
			var deps []string
			for _, code := range r.after {
				if i := slices.IndexFunc(tasks, func(t *planv1.Task) bool { return strings.EqualFold(t.GetCode(), code) }); i >= 0 &&
					!slices.Contains(deps, tasks[i].GetId()) && tasks[i].GetId() != r.azima.GetId() {
					deps = append(deps, tasks[i].GetId())
				}
			}
			if len(deps) == 0 {
				continue
			}
			if cycle := closesCycle(r.azima, deps, r.azima.GetPartOf(), tasks); cycle != "" {
				return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
					"%s: the tasks of a wish form no cycle: %s", r.file.Path, cycle))
			}
			r.azima.DependsOn = deps
		}
		if len(changed) == 0 {
			return nil
		}
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		for _, t := range changed {
			if err := tx.Put(t); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	if len(res.Made)+len(res.Changed) > 0 {
		h.notify()
		h.wake() // A task waiting on an azima its file closed may start.
	}

	// The files follow the store: what each azima depends on, by code.
	codeOf := map[string]string{}
	for _, t := range tasks {
		codeOf[t.GetId()] = t.GetCode()
	}
	var errs []error
	for _, r := range reads {
		var after []string
		for _, id := range r.azima.GetDependsOn() {
			if c := codeOf[id]; c != "" {
				after = append(after, c)
			}
		}
		slices.SortFunc(after, plan.CompareCodes)
		wrote, err := plan.WriteAfter(filepath.Join(r.dir, filepath.FromSlash(r.file.Path)), after)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.file.Path, err))
		}
		if wrote {
			res.Written = append(res.Written, r.file.Path)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("the azimas are synced, but not every file: %w", err))
	}
	filled := map[string]*planv1.Task{}
	for _, t := range plan.WithAzimas(tasks) {
		filled[t.GetId()] = t
	}
	for _, r := range reads {
		res.Azimas = append(res.Azimas, filled[r.azima.GetId()])
	}
	slices.SortFunc(res.Azimas, func(a, b *planv1.Task) int { return plan.CompareCodes(a.GetCode(), b.GetCode()) })
	slices.SortFunc(res.Made, plan.CompareCodes)
	slices.SortFunc(res.Changed, plan.CompareCodes)
	return res, nil
}

// azimaID is the identifier of an azima made from a plan file: the file's own id, a UUID, unless a task of the store
// has it already (the same plan synced into another wish); else a new one.
func azimaID(ctx context.Context, r store.Reader, fileID string) (string, error) {
	u, err := uuid.Parse(fileID)
	if err != nil {
		return store.NewID(), nil
	}
	id := u.String()
	switch _, err := store.Get[*planv1.Task](ctx, r, id); {
	case errors.Is(err, store.ErrNotFound):
		return id, nil
	case err != nil:
		return "", err
	}
	return store.NewID(), nil
}
