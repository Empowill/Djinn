package harness

// Opening a request: a yes to Djinn's question "Open a pull request …?" opens it, as a draft. A project with a skill
// of its own for opening one (dolmen's open-mr-empowill) gets a worker that follows it; any other gets gh pr create or
// glab mr create from Djinn. When Djinn cannot, the lead hears that the developer must open it: never a yes noted and
// nothing opened (wish Dew, 13/10/2026: three merge requests answered, none opened).

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// opening is the request a pull request question proposed: from branch into base, with its draft title and body;
// azima is the azima whose branch it is, nil for a wish's integration branch.
type opening struct {
	wish                      *planv1.Wish
	project                   *planv1.Project
	azima                     *planv1.Task
	branch, base, title, body string
}

// openRequest opens the request Djinn's question q proposed, as a draft, and says what it did, for the lead: in a
// few words, a digest line keeps 160 characters.
func (h *Harness) openRequest(ctx context.Context, q *planv1.Question) string {
	o, err := h.proposedRequest(ctx, q)
	if err != nil {
		return "Djinn could not open the pull request (" + clipRunes(err.Error(), 50) + "): the developer must open it"
	}
	return h.openDraft(ctx, o)
}

// openDraft opens o as a draft, by the project's skill or the forge's command, and says what it did, as openRequest.
func (h *Harness) openDraft(ctx context.Context, o *opening) string {
	repo, forge := o.project.GetDirectory(), forgeOf(ctx, o.project)
	kind := requestKind(forge)
	if req := h.requestFor(ctx, repo, forge, o.branch); req != nil {
		h.openedRequest(ctx, o, req.URL)
		return fmt.Sprintf("%s is open already: %s", requestName(forge, req.Number), req.URL)
	}
	if skill := openSkill(h.home, o.project); skill != "" {
		task, err := h.spawn(ctx, planv1connect.TaskServiceSpawnProcedure, &planv1.TaskServiceSpawnRequest{
			WishId: o.wish.GetId(), ProjectId: o.project.GetId(), Title: openerTitle(kind, o.branch),
			Prompt: openPrompt(o, kind, skill),
		}, &planv1.Task{}) // Djinn spawns it: never a repair it claims.
		if err != nil {
			logx.L().Warn("spawn the opening of a request", "branch", o.branch, "err", err)
			return fmt.Sprintf("Djinn could not spawn a task: spawn one to open the %s of %s with the skill %s", kind, o.branch, skill)
		}
		return fmt.Sprintf("Djinn spawned %s: it opens the draft %s of %s with the skill %s", task.GetCode(), kind, o.branch, skill)
	}
	ctx, cancel := context.WithTimeout(ctx, forgeLookTimeout)
	defer cancel()
	out, code, err := h.runCmd(ctx, repo, createArgs(forge, o))
	url, n := createdURL(out)
	if err != nil || code != 0 || n == 0 {
		why := cmp.Or(firstLine(out), "exit "+strconv.Itoa(code))
		if err != nil {
			why = err.Error()
		}
		logx.L().Warn("open a request", "branch", o.branch, "exit", code, "out", why)
		return fmt.Sprintf("Djinn could not open the %s of %s (%s): the developer must open it", kind, o.branch, clipRunes(why, 32))
	}
	h.openedRequest(ctx, o, url)
	return fmt.Sprintf("Djinn opened draft %s: %s", requestName(forge, n), url)
}

// proposedRequest is the request Djinn's question q proposed: for its azima's branch when q is about an azima, else
// for the wish's integration branch in the project q names, or the wish's first. Its title and body are drafted again,
// as the question drafted them.
func (h *Harness) proposedRequest(ctx context.Context, q *planv1.Question) (*opening, error) {
	wish, err := store.Get[*planv1.Wish](ctx, h.store, q.GetWishId())
	if err != nil {
		return nil, err
	}
	o := &opening{wish: wish}
	if id := q.GetTaskId(); id != "" {
		task, err := store.Get[*planv1.Task](ctx, h.store, id)
		if err != nil {
			return nil, err
		}
		if plan.IsAzima(task) {
			o.azima = task
		}
	}
	if id := q.GetDjinnMove().GetProjectId(); id == "" && o.azima.GetProjectId() != "" {
		o.project, err = store.Get[*planv1.Project](ctx, h.store, o.azima.GetProjectId())
	} else {
		o.project, err = h.moveProject(ctx, q)
	}
	if err != nil {
		return nil, err
	}
	if !o.project.GetGit() || o.project.GetDirectory() == "" {
		return nil, errors.New("its project is no Git repository here")
	}
	repo := o.project.GetDirectory()
	settings, err := plan.LoadSettings(h.home, o.project)
	if err != nil {
		settings = plan.ResolveSettings(nil, nil)
	}
	if o.azima != nil {
		parts, _ := h.azimaTasks(ctx, wish.GetId(), o.azima.GetId())
		o.branch, o.base = azimaBranchName(o.azima), azimaPRBase(ctx, repo, settings, o.azima)
		if o.base == "" {
			return nil, errors.New("the project has no main branch: name it with djinn project set-branch, or main_branch in its settings")
		}
		o.title, o.body = h.draftAzimaPR(ctx, o.azima, parts)
		return o, nil
	}
	o.branch = cmp.Or(q.GetDjinnMove().GetRef(), plan.IntegrationBranchOf(wish, o.project.GetId()))
	if o.branch == "" {
		return nil, errors.New("the wish has no branch in " + o.project.GetName())
	}
	main, ref, err := mainRef(ctx, repo, settings.MainBranch)
	if err != nil {
		return nil, fmt.Errorf("no main branch: %w", err)
	}
	o.base = main
	o.title, o.body = h.draftPR(ctx, wish, o.project, ref, o.branch)
	return o, nil
}

// openedRequest records the request open at url on the azima o opens it for, for its card.
func (h *Harness) openedRequest(ctx context.Context, o *opening, url string) {
	if o.azima != nil {
		h.recordAzimaPR(ctx, o.azima, &planv1.AzimaPr{Url: url, State: planv1.AzimaPrState_AZIMA_PR_STATE_OPEN})
	}
}

// requestKind is how a person names a request on the forge: MR on GitLab, PR on GitHub.
func requestKind(forge string) string {
	if forge == plan.ForgeGitLab {
		return "MR"
	}
	return "PR"
}

// createArgs is the command that opens o as a draft on the forge.
func createArgs(forge string, o *opening) []string {
	if forge == plan.ForgeGitLab {
		return []string{"glab", "mr", "create", "--draft", "--source-branch", o.branch, "--target-branch", o.base,
			"--title", o.title, "--description", o.body, "--yes"}
	}
	return []string{"gh", "pr", "create", "--draft", "--head", o.branch, "--base", o.base, "--title", o.title, "--body", o.body}
}

// createdURL is the address of the request gh pr create or glab mr create printed, the last one, and its number; 0
// when it printed none.
func createdURL(out string) (string, int) {
	all := requestURL.FindAllStringSubmatch(out, -1)
	if len(all) == 0 {
		return "", 0
	}
	m := all[len(all)-1]
	n, _ := strconv.Atoi(m[1])
	return m[0], n
}

// openSkill is the project's own skill that opens a request: its name says open or create, and mr, pr or request
// (dolmen's open-mr-empowill). "" when the project has none.
func openSkill(home string, project *planv1.Project) string {
	for _, s := range plan.ProjectSkills(plan.ConfigDir(home, project)) {
		words := strings.FieldsFunc(strings.ToLower(s.Name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		has := func(names ...string) bool {
			return slices.ContainsFunc(words, func(w string) bool { return slices.Contains(names, w) })
		}
		if has("open", "create") && has("mr", "pr", "request") {
			return s.Name
		}
	}
	return ""
}

// openerTitle is the title of the task that opens the request of branch with the project's skill.
func openerTitle(kind, branch string) string {
	return "Open the draft " + kind + " of " + branch
}

// opening tells whether a task of wish opens the request of branch with the project's skill still (W623): while it
// works, the forge knows no request, and asking to open one would open a second. Done, its final note naming the
// request it opened or found, it counts still: the forge may list it a pass late (W624). Ended otherwise, its request
// is on the forge, or the question is the way back.
func (h *Harness) opening(ctx context.Context, wishID string, project *planv1.Project, branch string) bool {
	title := openerTitle(requestKind(forgeOf(ctx, project)), branch)
	tasks, _ := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wishID})
	return slices.ContainsFunc(tasks, func(t *planv1.Task) bool {
		if t.GetTitle() != title {
			return false
		}
		if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE {
			return requestURL.MatchString(h.finalNote(ctx, t.GetId()))
		}
		return !slices.Contains(openerEnded, t.GetStatus())
	})
}

// openerEnded are the statuses of an opener that opens no more; an interrupted one resumes.
var openerEnded = []planv1.TaskStatus{
	planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_FAILED,
	planv1.TaskStatus_TASK_STATUS_STOPPED, planv1.TaskStatus_TASK_STATUS_CANCELLED,
}

// openPrompt is the prompt of the worker that opens o with the project's skill.
func openPrompt(o *opening, kind, skill string) string {
	return fmt.Sprintf("Djinn opens the %[1]s of branch %[2]s into %[3]s. Open it as a draft, "+
		"following the project's skill %[4]s. The branch is on the remote already: push nothing, commit nothing, change "+
		"no file. If a %[1]s is open for %[2]s already, open none. Djinn drafted its title and description from the "+
		"work: keep what they say, in the skill's format.\n\nTitle: %[5]s\n\n%[6]s\n\nEnd with the %[1]s's address.",
		kind, o.branch, o.base, skill, o.title, o.body)
}

// openAzimaDraft opens the request of azima, under the per-azima strategy, as a draft from its branch's first push,
// with no question (decision Q375; wish Dew, 07/11/2026: four azimas pushed, none had its draft): once, while the azima
// records no request and its branch holds work main lacks. Opened, or found open, it is recorded open; handed to the
// project's skill, or failed, recorded proposed, and the lead hears what Djinn did. The question "Open a pull request
// for T07?" once the parts are done stays the way back when it could not, never while the skill's task opens it
// (W623). It tells whether it tried, the azima as read stale then.
func (h *Harness) openAzimaDraft(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, azima *planv1.Task, branch string,
) bool {
	if azima.GetAzima().GetPr() != nil || azima.GetAzima().GetMerged() != nil || azima.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE {
		return false
	}
	repo := project.GetDirectory()
	_, main, err := resolveMainSha(ctx, repo, settings)
	if err != nil {
		return false
	}
	if n, err := git(ctx, repo, "rev-list", "--count", main+"..refs/heads/"+branch); err != nil || strings.TrimSpace(n) == "0" {
		return false
	}
	o := &opening{wish: wish, project: project, azima: azima, branch: branch, base: azimaPRBase(ctx, repo, settings, azima)}
	parts, _ := h.azimaTasks(ctx, wish.GetId(), azima.GetId())
	o.title, o.body = h.draftAzimaPR(ctx, azima, parts)
	did := h.openDraft(ctx, o)
	if now, err := store.Get[*planv1.Task](ctx, h.store, azima.GetId()); err == nil && now.GetAzima().GetPr() == nil {
		h.recordAzimaPR(ctx, azima, &planv1.AzimaPr{State: planv1.AzimaPrState_AZIMA_PR_STATE_PROPOSED})
	}
	h.tellLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_MOVE, wish.GetId(), azima.GetCode(), azima.GetCode()+" pushed: "+did)
	return true
}
