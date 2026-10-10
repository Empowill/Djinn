package harness

// Azimas are the tasks of kind AZIMA: the plan of a wish as a graph, which no worker runs and the scheduler never
// starts (internal/plan/azimas.go). The lead makes one with djinn task spawn --kind azima, or reads them from a
// project's plan files with djinn plan sync (PlanService).

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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

// spawnAzima makes an azima of the wish: a task of kind AZIMA, coded T1, T2… after the highest one ever, which no worker
// runs. What only a worker uses is refused.
func (h *Harness) spawnAzima(ctx context.Context, procedure string, req *planv1.TaskServiceSpawnRequest) (*planv1.Task, error) {
	if req.GetPrompt() != "" || req.GetProvider() != planv1.Provider_PROVIDER_UNSPECIFIED || req.GetModel() != "" ||
		req.GetMaxBudgetUsd() != 0 || len(req.GetWriteScopes()) > 0 || req.GetLater() || req.GetFork() != "" ||
		req.GetFromLead() || req.GetRestart() || len(req.GetTilasm()) > 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(
			"an azima has no worker: no prompt, provider, model, budget, write scope, --later, --fork, --from-lead, "+
				"--restart or --tilasm"))
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
		Draft: req.GetDraft(), Description: req.GetDescription(),
	}
	if task.DependsOn, err = resolveDeps(ctx, h.store, wish.GetId(), after(req)); err != nil {
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
		var err error
		if task.Code, err = nextNumber(ctx, tx, wish.GetId(), "T"); err != nil {
			return err
		}
		if err := h.block(ctx, tx, task, req.GetBlocks()); err != nil {
			return err
		}
		return tx.Put(task)
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.notify()
	if len(req.GetBlocks()) > 0 {
		h.wake() // The tasks it blocks say what they wait for now.
	}
	return task, nil
}

// azimaCode is the code of an azima: T1, T07.
var azimaCode = regexp.MustCompile(`^[Tt][0-9]+$`)

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

// ungroupOrphans makes part of no azima each of tasks, every task of the store, that is part of an azima its wish no
// longer has (deleted before Delete refused it): journaled, said in the log and, for work, in its events, so that the
// window shows it where it shows work of no azima. Changed in place, for Recover to go on with.
func (h *Harness) ungroupOrphans(ctx context.Context, tasks []*planv1.Task) error {
	azimas := map[string]string{} // Each azima's wish.
	for _, t := range tasks {
		if plan.IsAzima(t) {
			azimas[t.GetId()] = t.GetWishId()
		}
	}
	for _, t := range tasks {
		id := t.GetPartOf()
		if id == "" || azimas[id] == t.GetWishId() {
			continue
		}
		t.PartOf = ""
		text := "part of no azima: its azima " + short8(id) + " is no longer one of the wish's"
		log.Printf("djinn: task %s: %s", t.GetCode(), text)
		err := h.store.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal(actorHarness, methodUngroup, t); err != nil {
				return err
			}
			if err := tx.Put(t); err != nil {
				return err
			}
			if plan.IsAzima(t) {
				return nil // An azima has no events.
			}
			seq, err := lastSeq(ctx, tx, t.GetId())
			if err != nil {
				return err
			}
			return tx.Put(newEvent(t.GetId(), seq+1, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text}))
		})
		if err != nil {
			return fmt.Errorf("take %s out of its azima: %w", t.GetCode(), err)
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
// into its file (PlanService.Sync). A file closes its azima when its status says done or every Done-when box is
// checked, the latter reported; its unchecked boxes that wait for a proof no worker can give go on the azima. An azima is found by its file's id, else its code; one the files name and the
// store lacks is made, taking its file's after line once, unless its code was a deleted task's: a code is never given
// twice. What an azima depends on is the store's: the files only follow it.
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
		stored, err := store.Get[*planv1.Wish](ctx, tx, wish.GetId())
		if err != nil {
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
					if slices.ContainsFunc(stored.GetRetiredCodes(), func(c string) bool { return strings.EqualFold(c, f.Code) }) {
						next, err := nextNumber(ctx, tx, wish.GetId(), "T")
						if err != nil {
							return err
						}
						return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
							"%s gives the code %s, which an azima of the wish had before it was deleted: a code is never "+
								"given twice; give the file a new one (%s is free)", f.Path, f.Code, next))
					}
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
				t.Draft, t.Description = f.Draft(), f.Description
				if t.GetProjectId() == "" {
					t.ProjectId = src.project.GetId()
				}
				// What the unchecked boxes wait for, when nothing else is left in them.
				t.ProofNeeds = f.DoneWhen.Needs
				if !f.Done() && f.DoneWhen.AllChecked() {
					res.AllChecked = append(res.AllChecked, f.Path)
				}
				switch {
				case f.Closes() && t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE:
					note := f.Path + " says done"
					if !f.Done() {
						note = "every Done-when box of " + f.Path + " is checked"
					}
					t.Status, t.WaitReason, t.EndTime = planv1.TaskStatus_TASK_STATUS_DONE, "", now
					t.Closed = &planv1.Closure{Actor: planv1.Closer_CLOSER_PLAN_FILE, CreateTime: now, Note: note}
				case !f.Closes() && t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE &&
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
		return store.NewID(), nil //nolint:nilerr // A file id that is not a UUID is not an error: the azima gets a new one.
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

// DescribeTask sets the description of an azima, and writes it back to its plan file if it has one.
func (h *Harness) DescribeTask(ctx context.Context, procedure string, req *planv1.TaskServiceDescribeRequest) (*planv1.Task, error) {
	h.sched.Lock()
	defer h.sched.Unlock()
	var task *planv1.Task
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		var err error
		task, err = findAzima(ctx, tx, req.GetAzima())
		if err != nil {
			return err
		}
		if task.GetPlanFile() != "" && task.GetProjectId() != "" {
			project, err := store.Get[*planv1.Project](ctx, tx, task.GetProjectId())
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if project != nil && project.GetDirectory() != "" {
				planPath := filepath.Join(project.GetDirectory(), filepath.FromSlash(task.GetPlanFile()))
				if _, err := plan.WriteDescription(planPath, req.GetText()); err != nil && !errors.Is(err, os.ErrNotExist) {
					return connect.NewError(connect.CodeInternal, fmt.Errorf("write description: %w", err))
				}
			}
		}
		task.Description = req.GetText()
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		return tx.Put(task)
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.notify()
	return task, nil
}

// Open turns a draft azima OPEN: its plan file front matter says status: open, and the lead is told to plan its tasks.
func (h *Harness) Open(ctx context.Context, procedure string, req *planv1.TaskServiceOpenRequest) (*planv1.Task, error) {
	h.sched.Lock()
	defer h.sched.Unlock()
	var task *planv1.Task
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		var err error
		task, err = findAzima(ctx, tx, req.GetAzima())
		if err != nil {
			return err
		}
		if !task.GetDraft() {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("azima %s is not a draft", task.GetCode()))
		}
		if task.GetPlanFile() != "" && task.GetProjectId() != "" {
			project, err := store.Get[*planv1.Project](ctx, tx, task.GetProjectId())
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if project != nil && project.GetDirectory() != "" {
				planPath := filepath.Join(project.GetDirectory(), filepath.FromSlash(task.GetPlanFile()))
				if _, err := plan.WriteStatus(planPath, "open"); err != nil && !errors.Is(err, os.ErrNotExist) {
					return connect.NewError(connect.CodeInternal, fmt.Errorf("write status: %w", err))
				}
			}
		}
		task.Draft = false
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		return tx.Put(task)
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.notify()
	h.wake()
	h.mu.Lock()
	tell := h.tell
	h.mu.Unlock()
	if tell != nil {
		_ = tell(ctx, task.GetWishId(), fmt.Sprintf("Djinn: azima %s opened: plan its tasks", task.GetCode()))
	}
	return task, nil
}

// Move moves an azima and its unstarted parts to another wish, with its plan file reference.
func (h *Harness) Move(ctx context.Context, procedure string, req *planv1.TaskServiceMoveRequest) (*planv1.Task, error) {
	h.sched.Lock()
	defer h.sched.Unlock()
	var movedAzima *planv1.Task
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		targetWish, err := resolveWish(ctx, tx, req.GetWish())
		if err != nil {
			return err
		}
		azima, err := findAzima(ctx, tx, req.GetAzima(), targetWish.GetId())
		if err != nil {
			return err
		}
		if targetWish.GetId() == azima.GetWishId() {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("azima is already in this wish"))
		}
		sourceWish, err := store.Get[*planv1.Wish](ctx, tx, azima.GetWishId())
		if err != nil {
			return err
		}

		sourceTasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": sourceWish.GetId()})
		if err != nil {
			return err
		}

		moving := []*planv1.Task{azima}
		movingIDs := map[string]bool{azima.GetId(): true}
		queue := []string{azima.GetId()}
		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			for _, t := range sourceTasks {
				if t.GetPartOf() == curr && !movingIDs[t.GetId()] {
					movingIDs[t.GetId()] = true
					moving = append(moving, t)
					queue = append(queue, t.GetId())
				}
			}
		}

		for _, t := range moving {
			if h.hasRun(t.GetId()) || t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING || t.GetStatus() == planv1.TaskStatus_TASK_STATUS_PAUSED {
				return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s runs: move it once it ended", t.GetCode()))
			}
			if !plan.IsAzima(t) {
				if where := started(t, false); where != "" {
					return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s has started (%s): move an azima before its parts start", t.GetCode(), where))
				}
			}
		}

		targetTasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": targetWish.GetId()})
		if err != nil {
			return err
		}

		targetCodes := map[string]bool{}
		for _, t := range targetTasks {
			targetCodes[strings.ToUpper(t.GetCode())] = true
		}
		for _, c := range targetWish.GetRetiredCodes() {
			targetCodes[strings.ToUpper(c)] = true
		}

		for _, t := range moving {
			var keptDeps []string
			for _, depID := range t.GetDependsOn() {
				if movingIDs[depID] {
					keptDeps = append(keptDeps, depID)
				}
			}
			t.DependsOn = keptDeps
		}

		allTarget := append(slices.Clone(targetTasks), moving...)
		for _, t := range moving {
			if cycle := closesCycle(t, t.GetDependsOn(), t.GetPartOf(), allTarget); cycle != "" {
				return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the tasks of a wish form no cycle: %s", cycle))
			}
		}

		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}

		for _, t := range sourceTasks {
			if movingIDs[t.GetId()] {
				continue
			}
			origLen := len(t.GetDependsOn())
			t.DependsOn = slices.DeleteFunc(t.DependsOn, func(id string) bool { return movingIDs[id] })
			if len(t.GetDependsOn()) != origLen {
				if err := tx.Put(t); err != nil {
					return err
				}
			}
		}

		for _, t := range moving {
			if pID := t.GetProjectId(); pID != "" && !slices.Contains(targetWish.GetProjectIds(), pID) {
				targetWish.ProjectIds = append(targetWish.ProjectIds, pID)
			}
		}

		for _, t := range moving {
			if !slices.Contains(sourceWish.GetRetiredCodes(), t.GetCode()) {
				sourceWish.RetiredCodes = append(sourceWish.RetiredCodes, t.GetCode())
			}
			t.WishId = targetWish.GetId()
			codeKey := strings.ToUpper(t.GetCode())
			if targetCodes[codeKey] {
				letter := "W"
				if plan.IsAzima(t) {
					letter = "T"
				}
				newCode, err := nextNumber(ctx, tx, targetWish.GetId(), letter)
				if err != nil {
					return err
				}
				t.Code = newCode
			}
			targetCodes[strings.ToUpper(t.GetCode())] = true
			if err := tx.Put(t); err != nil {
				return err
			}
		}

		if err := tx.Put(sourceWish); err != nil {
			return err
		}
		if err := tx.Put(targetWish); err != nil {
			return err
		}

		movedAzima = azima
		return nil
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.notify()
	h.wake()
	return movedAzima, nil
}

// findAzima finds an azima by identifier or code across tasks in the store.
func findAzima(ctx context.Context, r store.Reader, name string, notInWish ...string) (*planv1.Task, error) {
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("azima is required"))
	}
	if t, err := store.Get[*planv1.Task](ctx, r, name); err == nil {
		if !plan.IsAzima(t) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s is work, not an azima", t.GetCode()))
		}
		return t, nil
	}
	tasks, err := store.List[*planv1.Task](ctx, r, nil)
	if err != nil {
		return nil, err
	}
	var matches []*planv1.Task
	for _, t := range tasks {
		if plan.IsAzima(t) && strings.EqualFold(t.GetCode(), name) {
			matches = append(matches, t)
		}
	}
	if len(notInWish) > 0 && notInWish[0] != "" && len(matches) > 1 {
		var filtered []*planv1.Task
		for _, m := range matches {
			if m.GetWishId() != notInWish[0] {
				filtered = append(filtered, m)
			}
		}
		if len(filtered) > 0 {
			matches = filtered
		}
	}
	switch len(matches) {
	case 0:
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("azima %s not found", name))
	case 1:
		return matches[0], nil
	default:
		actives, err := plan.ActiveWishes(ctx, r)
		if err == nil {
			for _, w := range actives {
				for _, m := range matches {
					if m.GetWishId() == w.GetId() {
						return m, nil
					}
				}
			}
		}
		return matches[0], nil
	}
}

// resolveWish finds a wish by identifier, rank (e.g. W1, 1), or title.
func resolveWish(ctx context.Context, r store.Reader, name string) (*planv1.Wish, error) {
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("wish is required"))
	}
	if w, err := store.Get[*planv1.Wish](ctx, r, name); err == nil {
		return w, nil
	}
	all, err := store.List[*planv1.Wish](ctx, r, nil)
	if err != nil {
		return nil, err
	}
	norm := strings.TrimPrefix(strings.ToUpper(name), "W")
	if n, err := strconv.Atoi(norm); err == nil && n > 0 {
		for _, w := range all {
			if int(w.GetRank()) == n {
				return w, nil
			}
		}
	}
	for _, w := range all {
		if strings.EqualFold(w.GetId(), name) || strings.EqualFold(w.GetTitle(), name) {
			return w, nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("wish %s not found", name))
}
