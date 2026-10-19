package harness

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// An azima is merged into main (Azima.merged) once all its work is in its project's main branch. Djinn finds it in two
// ways, and only once every work task part of the azima is finished, one at least done:
//   - its pull or merge request merged: a watcher that babysits it (part of the azima, on its request's address, or on
//     what one of its tasks opened) printed its done line after the last of its work ended; or the integration found
//     it merged on the forge (azimaPRPass), as it already looks. No forge is asked more than that.
//   - its commits: its own branch and the branch of each of its done tasks, with the commit each was merged as, are all
//     in the remote's main, fetched at most once each merge_main_minutes by this look, or by the merge of main.
// Work added to the azima clears it (reopenAzima); its end lets Djinn look again. The lead hears it once it turns on.

// fetchedKey keys a fetch of main made in a pass: by repository and ref.
func fetchedKey(repo, ref string) string { return repo + "\x00" + ref }

// lookMerged marks the azimas of wish whose work is all in main (Azima.merged), as the watchers and the commits say;
// now looks at main whatever the cadence, and fetched holds the fetches of main made in this pass (fetchedKey).
func (h *Harness) lookMerged(ctx context.Context, wish *planv1.Wish, now bool, fetched map[string]error) {
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		return
	}
	for _, azima := range tasks {
		if ctx.Err() != nil {
			return
		}
		if !plan.IsAzima(azima) || azima.GetDraft() || azima.GetAzima().GetMerged() != nil {
			continue
		}
		work, ok := mergeable(azima, tasks)
		if !ok {
			continue
		}
		m := requestMerged(azima, tasks, work)
		if m == nil {
			m = h.commitsInMain(ctx, wish, azima, work, now, fetched)
		}
		if m != nil {
			h.markMerged(ctx, azima.GetId(), m)
		}
	}
}

// azimaWork is the work part of an azima: its done tasks, the watchers of its requests, and when the last of its work
// ended.
type azimaWork struct {
	done, watchers []*planv1.Task
	ended          time.Time
}

// mergeable is the work part of azima among the tasks of its wish, through the azimas part of it, when Djinn may look
// whether it is all in main: every work task settled (plan.Settled), one at least done; a watcher is no work.
func mergeable(azima *planv1.Task, tasks []*planv1.Task) (azimaWork, bool) {
	var w azimaWork
	ids := map[string]bool{azima.GetId(): true}
	for grew := true; grew; {
		grew = false
		for _, t := range tasks {
			if plan.IsAzima(t) && ids[t.GetPartOf()] && !ids[t.GetId()] {
				ids[t.GetId()], grew = true, true
			}
		}
	}
	for _, t := range tasks {
		if plan.IsAzima(t) || !ids[t.GetPartOf()] {
			continue
		}
		if watching(t) {
			w.watchers = append(w.watchers, t)
			continue
		}
		if !plan.Settled(t) {
			return azimaWork{}, false
		}
		if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			continue
		}
		w.done = append(w.done, t)
		for _, at := range []*timestamppb.Timestamp{t.GetEndTime(), t.GetIntegration().GetUpdateTime()} {
			if at != nil && at.AsTime().After(w.ended) {
				w.ended = at.AsTime()
			}
		}
	}
	return w, len(w.done) > 0
}

// requestMerged is how the request of azima was merged after the last of its work ended, as a watcher that babysits
// it printed its done line: a watcher part of it, one on its request's address, or one on what a done task of it
// opened. Nil when none did: one closed without a merge never prints it.
func requestMerged(azima *planv1.Task, tasks []*planv1.Task, w azimaWork) *planv1.AzimaMerged {
	url := strings.TrimSuffix(azima.GetAzima().GetPr().GetUrl(), "/")
	opened := map[string]bool{}
	for _, t := range w.done {
		opened[t.GetCode()] = true
	}
	var found *planv1.AzimaMerged
	for _, t := range tasks {
		b := t.GetBabysit()
		if b.GetDoneTime() == nil || b.GetDoneTime().AsTime().Before(w.ended) {
			continue
		}
		ours := slices.Contains(w.watchers, t) ||
			url != "" && strings.EqualFold(strings.TrimSuffix(b.GetUrl(), "/"), url) ||
			opened[b.GetOpenedBy()]
		if ours && (found == nil || b.GetDoneTime().AsTime().After(found.GetTime().AsTime())) {
			found = &planv1.AzimaMerged{Time: b.GetDoneTime(), RequestUrl: b.GetUrl(), MainBranch: azima.GetAzima().GetBaseBranch()}
		}
	}
	return found
}

// commitsInMain is how the work of azima is in main by its commits: in each project of the wish, its own branch and
// the branch of each of its done tasks there, with the commit each was merged as, all in the remote's main, fetched.
// Nil when one is not, when there is none, or when main was not fetched in this pass and its look is not due (now: it
// is): a project that holds neither the azima nor a done task of it is not looked at.
func (h *Harness) commitsInMain(
	ctx context.Context, wish *planv1.Wish, azima *planv1.Task, w azimaWork, now bool, fetched map[string]error,
) *planv1.AzimaMerged {
	var found *planv1.AzimaMerged
	for _, projectID := range wish.GetProjectIds() {
		project, err := store.Get[*planv1.Project](ctx, h.store, projectID)
		if err != nil || !project.GetGit() {
			continue
		}
		if !strings.EqualFold(azima.GetProjectId(), projectID) &&
			!slices.ContainsFunc(w.done, func(t *planv1.Task) bool { return strings.EqualFold(t.GetProjectId(), projectID) }) {
			continue
		}
		// Main first: out of its cadence, the look runs no git.
		repo := project.GetDirectory()
		settings, err := plan.LoadSettings(h.home, project)
		if err != nil {
			return nil
		}
		main, ref, err := mainRef(ctx, repo, settings.MainBranch)
		if err != nil || !h.fetchedForMerged(ctx, wish, repo, ref, settings.MergeMainEvery, now, fetched) {
			return nil
		}
		var refs []string
		if commitOf(ctx, repo, "refs/heads/"+azimaBranchName(azima)) != "" {
			refs = append(refs, "refs/heads/"+azimaBranchName(azima))
		}
		for _, t := range w.done {
			if !strings.EqualFold(t.GetProjectId(), projectID) {
				continue
			}
			if b := t.GetBranch(); b != "" && commitOf(ctx, repo, "refs/heads/"+b) != "" {
				refs = append(refs, "refs/heads/"+b)
			}
			if in := t.GetIntegration(); in.GetState() == planv1.IntegrationState_INTEGRATION_STATE_COMMITTED &&
				in.GetSha() != "" && commitOf(ctx, repo, in.GetSha()) != "" {
				refs = append(refs, in.GetSha())
			}
		}
		if len(refs) == 0 {
			continue
		}
		sha := commitOf(ctx, repo, ref)
		if sha == "" {
			return nil
		}
		for _, r := range refs {
			if !isAncestor(ctx, repo, r, sha) {
				return nil
			}
		}
		if found == nil {
			found = &planv1.AzimaMerged{Time: timestamppb.New(h.now()), MainSha: sha, MainBranch: main}
		}
	}
	return found
}

// fetchedForMerged tells whether main, ref in repo, is fetched for this look: in this pass already, or now, when the
// look is due (now, or every each since the last fetch this look made there).
func (h *Harness) fetchedForMerged(
	ctx context.Context, wish *planv1.Wish, repo, ref string, every time.Duration, now bool, fetched map[string]error,
) bool {
	key := fetchedKey(repo, ref)
	err, ok := fetched[key]
	if !ok {
		h.mu.Lock()
		last, seen := h.mergedLooks[repo]
		h.mu.Unlock()
		if !now && seen && h.now().Sub(last) < every {
			return false
		}
		err = h.fetchMain(ctx, wish.GetId(), repo, ref)
		fetched[key] = err
		h.mu.Lock()
		h.mergedLooks[repo] = h.now()
		h.mu.Unlock()
	}
	return err == nil
}

// markMerged records that the work of the azima id is all in main, as m says, when its work is still all settled and
// it carries no mark yet; then tells the lead, once, and whether every azima of its wish is in main now.
func (h *Harness) markMerged(ctx context.Context, id string, m *planv1.AzimaMerged) {
	ctx = context.WithoutCancel(ctx)
	var azima *planv1.Task
	var tasks []*planv1.Task
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		a, err := store.Get[*planv1.Task](ctx, tx, id)
		if err != nil || a.GetAzima().GetMerged() != nil {
			return err
		}
		if tasks, err = store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": a.GetWishId()}); err != nil {
			return err
		}
		if _, ok := mergeable(a, tasks); !ok {
			return nil // Work was added meanwhile: it is not in main yet.
		}
		if err := editAzima(ctx, tx, id, func(e *planv1.Azima) { e.Merged = m }); err != nil {
			return err
		}
		if err := putEvents(ctx, tx, newEvent(id, Event{
			Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "merged into " + cmp.Or(m.GetMainBranch(), "main") + ": " + mergedHow(m),
		})); err != nil {
			return err
		}
		azima = a
		return nil
	})
	if err != nil {
		logx.L().Error("mark an azima merged into main", "azima", short8(id), "err", err)
		return
	}
	if azima == nil {
		return
	}
	h.notify()
	for i, t := range tasks {
		if t.GetId() == id {
			tasks[i].Azima = &planv1.Azima{Merged: m}
		}
	}
	h.tellLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_LANDED, azima.GetWishId(), azima.GetCode(), mergedLine(azima, m, tasks))
}

// mergedLine tells the lead that the work of azima is all in main, as m says: first whether every azima of its wish,
// among tasks, is too, so that it proposes to grant the wish; else which are not yet.
func mergedLine(azima *planv1.Task, m *planv1.AzimaMerged, tasks []*planv1.Task) string {
	main, how := cmp.Or(m.GetMainBranch(), "main"), mergedHow(m)
	if plan.AllMerged(tasks) {
		return fmt.Sprintf("Djinn: %s's work is in %s, as is every azima's: propose to grant the wish (%s).", azima.GetCode(), main, how)
	}
	var left []string
	for _, t := range tasks {
		if plan.IsAzima(t) && !t.GetDraft() && t.GetAzima().GetMerged() == nil {
			left = append(left, t.GetCode())
		}
	}
	slices.SortFunc(left, plan.CompareCodes)
	return fmt.Sprintf("Djinn: %s's work is in %s (%s); not in %s yet: %s.", azima.GetCode(), main, how, main, strings.Join(left, ", "))
}

// mergedHow says how Djinn found an azima's work in main, as m says.
func mergedHow(m *planv1.AzimaMerged) string {
	if m.GetRequestUrl() != "" {
		return "its request merged: " + m.GetRequestUrl()
	}
	return cmp.Or(m.GetMainBranch(), "main") + " at " + short8(m.GetMainSha()) + " holds every commit"
}

// unmergeAzima clears the mark of azima a in tx (Azima.merged): work was added to it, reason says which, and its
// events say so; Djinn looks again once that work is settled.
func unmergeAzima(ctx context.Context, tx *store.Tx, a *planv1.Task, reason string) error {
	if err := editAzima(ctx, tx, a.GetId(), func(e *planv1.Azima) { e.Merged = nil }); err != nil {
		return err
	}
	return putEvents(ctx, tx, newEvent(a.GetId(), Event{
		Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
		Text: "no longer merged into main: " + reason,
	}))
}
