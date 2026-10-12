package harness

// An azima's branch, under the per-azima push strategy, works like a wish's integration branch: cut from main when
// the azima is made (or when its first part starts, or its first work is merged, for an azima made before the wish
// was switched), recorded on the azima (Task.azima: its branch, the main it lands in, the commit it was cut from),
// its work merged into it, kept up with main by the same merge_main rules (main.go), and its pull request, opened at
// the developer's request, targeting main and following each push of it. It is never cut again nor rebased: a branch
// made before, stacked on the branch of the azima it depended on, keeps working as it is. No azima of such a wish
// waits for another (plan.RefuseAzimaDependencies): each one's branch holds main and its own work, nothing else.

import (
	"context"
	"fmt"
	"log"
	"slices"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
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
	log.Printf("djinn: azima %s: cut %s from %s at %s", azima.GetCode(), branch, main, short8(sha))
	if p := azima.GetProjectId(); p == "" || p == project.GetId() {
		if main == "HEAD" {
			main = "" // No main branch: the checkout's commit, landing nowhere named.
		}
		h.editAzima(ctx, azima.GetId(), func(e *planv1.Azima) { e.Branch, e.BaseBranch, e.CutFrom = branch, main, sha })
	}
	return branch, nil
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
		log.Printf("djinn: azima %s: %v", azima.GetCode(), err)
	}
}

// editAzima changes, with edit, what Djinn records of the azima id (Task.azima), journaled when it changes.
func (h *Harness) editAzima(ctx context.Context, id string, edit func(*planv1.Azima)) {
	ctx = context.WithoutCancel(ctx)
	if err := h.store.Tx(ctx, func(tx *store.Tx) error { return editAzima(ctx, tx, id, edit) }); err != nil {
		log.Printf("djinn: azima %s: %v", short8(id), err)
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
