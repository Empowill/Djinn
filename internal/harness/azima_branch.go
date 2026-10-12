package harness

// An azima's branch, under the per-azima push strategy, works like a wish's integration branch: cut from main when
// the azima is made (or when its first part starts, or its first work is merged, for an azima made before the wish
// was switched), recorded on the azima (Task.azima: its branch, the main it lands in, the commit it was cut from),
// its work merged into it, kept up with main by the same merge_main rules (main.go), and its pull request, opened at
// the developer's request, targeting main and following each push of it. It is never cut again nor rebased: a branch
// made before, stacked on the branch of the azima it depended on, keeps working as it is. No azima of such a wish
// waits for another (plan.RefuseAzimaDependencies): each one's branch holds main and its own work, nothing else.

import (
	"cmp"
	"context"
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

// methodAzimaBranch is how the journal records what Djinn records of an azima's branch: cut from main, where it
// stands against main, or where its pull request stands. The request is the azima.
const methodAzimaBranch = "harness/azima-branch"

// pushesPerAzima tells whether wish pushes per azima, as its settings or its own choice say.
func (h *Harness) pushesPerAzima(ctx context.Context, r store.Reader, wish *planv1.Wish) bool {
	strat, _, err := plan.EffectiveWishPushStrategy(ctx, r, h.home, wish)
	return err == nil && strat == planv1.PushStrategy_PUSH_STRATEGY_AZIMA
}

// refuseAzimaDependencies refuses a change to the tasks of wish, changed as it leaves them (new ones or stored ones),
// when the wish pushes per azima and the change makes an azima wait for another (plan.RefuseAzimaDependencies).
func (h *Harness) refuseAzimaDependencies(ctx context.Context, r store.Reader, wish *planv1.Wish, changed ...*planv1.Task) error {
	if !h.pushesPerAzima(ctx, r, wish) {
		return nil
	}
	tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		return err
	}
	graph := slices.Clone(tasks)
	for _, c := range changed {
		if i := slices.IndexFunc(graph, func(t *planv1.Task) bool { return t.GetId() == c.GetId() }); i >= 0 {
			graph[i] = c
		} else {
			graph = append(graph, c)
		}
	}
	return plan.RefuseAzimaDependencies(tasks, graph)
}

// azimaBranch is the branch of azima in project under the per-azima push strategy, cut from main's commit as Djinn
// knows it (resolveMainSha) when it does not exist yet, and recorded on the azima when project is the azima's own. A
// branch that exists stays as it is: never cut again, never rebased.
func (h *Harness) azimaBranch(ctx context.Context, project *planv1.Project, settings plan.Settings, azima *planv1.Task) (string, error) {
	branch := azimaBranchName(azima)
	repo := project.GetDirectory()
	if commitOf(ctx, repo, "refs/heads/"+branch) != "" {
		return branch, nil
	}
	main, sha, err := resolveMainSha(ctx, repo, settings)
	if err != nil {
		return "", err
	}
	if _, err := git(ctx, repo, "branch", branch, sha); err != nil {
		if commitOf(ctx, repo, "refs/heads/"+branch) != "" {
			return branch, nil // Cut meanwhile.
		}
		return "", fmt.Errorf("cut %s from %s: %w", branch, main, err)
	}
	logx.L().Info("azima branch cut", "azima", azima.GetCode(), "branch", branch, "from", main, "at", short8(sha))
	if p := azima.GetProjectId(); p == "" || p == project.GetId() {
		if main == "HEAD" {
			main = "" // No main branch: the checkout's commit, landing nowhere named.
		}
		h.editAzima(ctx, azima.GetId(), func(e *planv1.Azima) { e.Branch, e.BaseBranch, e.CutFrom = branch, main, sha })
	}
	return branch, nil
}

// azimaOwning is the azima among azimas whose own branch is branch: under the per-azima push strategy, one that
// adopted the wish's integration branch (nameAzimaBranch); nil when none. That branch is then the azima's: kept up with
// main, pushed and listed once, as the azima's.
func azimaOwning(azimas []*planv1.Task, branch string) *planv1.Task {
	for _, a := range azimas {
		if branch != "" && azimaBranchName(a) == branch {
			return a
		}
	}
	return nil
}

// cutAzimaBranch cuts the branch of azima, just made, from main in its project, when its wish pushes per azima and
// the project integrates; what fails is said in the log, the branch then cut when its first part starts.
func (h *Harness) cutAzimaBranch(ctx context.Context, wish *planv1.Wish, azima *planv1.Task) {
	ctx = context.WithoutCancel(ctx)
	if azima.GetProjectId() == "" || !h.pushesPerAzima(ctx, h.store, wish) {
		return
	}
	project, err := store.Get[*planv1.Project](ctx, h.store, azima.GetProjectId())
	if err != nil || !project.GetGit() {
		return
	}
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil || !settings.Integrates() {
		return
	}
	if _, err := h.azimaBranch(ctx, project, settings, azima); err != nil {
		logx.L().Warn("cut an azima's branch", "azima", azima.GetCode(), "err", err)
	}
}

// editAzima changes, with edit, what Djinn records of the azima id (Task.azima), journaled when it changes.
func (h *Harness) editAzima(ctx context.Context, id string, edit func(*planv1.Azima)) {
	ctx = context.WithoutCancel(ctx)
	if err := h.store.Tx(ctx, func(tx *store.Tx) error { return editAzima(ctx, tx, id, edit) }); err != nil {
		logx.L().Error("record an azima", "azima", short8(id), "err", err)
	}
	h.notify()
}

// editAzima changes, with edit, what Djinn records of the azima id, in tx.
func editAzima(ctx context.Context, tx *store.Tx, id string, edit func(*planv1.Azima)) error {
	t, err := store.Get[*planv1.Task](ctx, tx, id)
	if err != nil {
		return err
	}
	if t.Azima == nil {
		t.Azima = &planv1.Azima{}
	}
	before := proto.CloneOf(t.GetAzima())
	edit(t.Azima)
	if proto.Equal(before, t.GetAzima()) {
		return nil
	}
	if err := tx.Journal(actorHarness, methodAzimaBranch, t); err != nil {
		return err
	}
	return tx.Put(t)
}

// nameAzimaBranch names the own branch of azima, under the per-azima push strategy, as the developer asks
// (djinn task update <azima> --branch): a branch that exists is adopted as it is, recorded with the main it lands in
// and the commit of main it was cut from, their merge base; one that does not is cut from main as Djinn cuts an
// azima's, under that name. Nothing is cut again or moved. Refused while a merge runs into the azima's branch, for main
// itself, for another azima's branch, and when the azima's branch so far holds commits the named one lacks.
func (h *Harness) nameAzimaBranch(ctx context.Context, procedure string, req *planv1.TaskServiceUpdateRequest, azima *planv1.Task) (*planv1.Task, error) {
	refuse := func(format string, a ...any) error {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(format, a...))
	}
	wish, err := store.Get[*planv1.Wish](ctx, h.store, azima.GetWishId())
	if err != nil {
		return nil, plan.Status(err)
	}
	projectID := azima.GetProjectId()
	if projectID == "" && len(wish.GetProjectIds()) == 1 {
		projectID = wish.GetProjectIds()[0]
	}
	if projectID == "" {
		return nil, refuse("azima %s has no project, and its wish has %d: an azima's branch is named in its project", azima.GetCode(), len(wish.GetProjectIds()))
	}
	project, err := store.Get[*planv1.Project](ctx, h.store, projectID)
	if err != nil {
		return nil, plan.Status(err)
	}
	repo := project.GetDirectory()
	if !project.GetGit() || repo == "" {
		return nil, refuse("project %s is not in Git: azima %s has no branch", project.GetName(), azima.GetCode())
	}
	branch := strings.TrimSpace(req.GetBranch())
	if _, err := git(ctx, repo, "check-ref-format", "--branch", branch); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%q is no valid branch name", branch))
	}
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil {
		return nil, refuse("settings of %s unreadable: %v", project.GetName(), err)
	}
	main, mainSha, err := resolveMainSha(ctx, repo, settings)
	if err != nil {
		return nil, refuse("%v", err)
	}
	if strings.EqualFold(branch, main) {
		return nil, refuse("%s is main: an azima's branch lands in main", branch)
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		return nil, plan.Status(err)
	}
	for _, t := range tasks {
		if plan.IsAzima(t) && t.GetId() != azima.GetId() && strings.EqualFold(plan.AzimaBranchName(t), branch) {
			return nil, refuse("%s is the branch of azima %s already", branch, t.GetCode())
		}
	}
	current := plan.AzimaBranchName(azima)
	e := azima.GetAzima()
	if current == branch && e.GetNamed() && e.GetCutFrom() != "" {
		return h.withAzima(ctx, azima)
	}
	for _, b := range []string{current, branch} {
		if merge := plan.AzimaMergeRunning(azima, tasks, b); merge != "" {
			return nil, refuse("cannot name the branch of %s now: %s; name it once that merge has ended", azima.GetCode(), merge)
		}
	}
	if wishBranch := plan.IntegrationBranchOf(wish, projectID); strings.EqualFold(branch, wishBranch) {
		if merge := plan.MergeRunning(wish, tasks, projectID); merge != "" {
			return nil, refuse("cannot adopt %s for %s now: %s; adopt it once that merge has ended", branch, azima.GetCode(), merge)
		}
	}
	tip := commitOf(ctx, repo, "refs/heads/"+branch)
	// What the azima's branch so far holds stays reachable: from the branch adopted, or from main it is cut from.
	if old := commitOf(ctx, repo, "refs/heads/"+current); old != "" && current != branch && !isAncestor(ctx, repo, old, cmp.Or(tip, mainSha)) {
		into := main
		if tip != "" {
			into = branch
		}
		return nil, refuse("the branch of %s so far, %s, holds commits %s lacks: they would be left there; merge them into it first",
			azima.GetCode(), current, into)
	}
	cut := mainSha
	if tip != "" {
		if cut, err = git(ctx, repo, "merge-base", mainSha, tip); err != nil || cut == "" {
			return nil, refuse("%s shares no history with %s: an azima's branch lands in main", branch, main)
		}
	} else if _, err := git(ctx, repo, "branch", "--no-track", branch, mainSha); err != nil {
		return nil, refuse("cut %s from %s: %v", branch, main, err)
	}
	base := main
	if base == "HEAD" {
		base = "" // No main branch: the checkout's commit, landing nowhere named.
	}
	var named *planv1.Task
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(plan.CallerActor(ctx), procedure, req); err != nil {
			return err
		}
		t, err := store.Get[*planv1.Task](ctx, tx, azima.GetId())
		if err != nil {
			return err
		}
		// Where the branch stands against main and its pull request follow the branch: found again for the one named.
		t.Azima = &planv1.Azima{Branch: branch, BaseBranch: base, CutFrom: cut, Named: true}
		named = t
		return tx.Put(t)
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	if tip != "" {
		logx.L().Info("azima branch adopted", "azima", azima.GetCode(), "branch", branch, "from", main, "at", short8(cut))
	} else {
		logx.L().Info("azima branch cut", "azima", azima.GetCode(), "branch", branch, "from", main, "at", short8(cut))
	}
	h.notify()
	return h.withAzima(ctx, named)
}

// withAzima is azima as it is read: where it stands computed (withAzimas).
func (h *Harness) withAzima(ctx context.Context, azima *planv1.Task) (*planv1.Task, error) {
	filled, err := h.withAzimas(ctx, []*planv1.Task{azima})
	if err != nil {
		return nil, plan.Status(err)
	}
	return filled[0], nil
}
