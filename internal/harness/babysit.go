package harness

// Babysitting: a pull or merge request a task of a wish opens is watched at once, in that wish. Once a work task ends
// done, Djinn looks for the request it opened: the address its gh pr create or glab mr create printed, else the one
// open for its branch, else the one open for the wish's branch. It starts the watcher a skill of the project declares
// (plan.Babysit), part of the task's azima, and tells the lead once. djinn task spawn --babysit does the same by hand,
// the request found as the watcher starts. A watcher spawned by hand on the project's command for the request is its
// babysit already: Djinn records it on that one, and starts none.

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// forgeLookTimeout is the most one look at the forge takes: gh or glab without network gives up.
const forgeLookTimeout = 30 * time.Second

// request is a pull or merge request open on its forge.
type request struct {
	Number int
	URL    string
}

// requestURL matches the web address of a pull request (…/pull/12) or of a merge request (…/-/merge_requests/12).
var requestURL = regexp.MustCompile(`https?://[^\s"'<>()\[\]\\]+?/(?:pull|-/merge_requests)/(\d+)`)

// forgeOf is the forge of the project, as a babysit declaration names it.
func forgeOf(ctx context.Context, project *planv1.Project) string {
	if detectForge(ctx, project.GetDirectory(), project) == "glab" {
		return plan.ForgeGitLab
	}
	return plan.ForgeGitHub
}

// requestName is how a person names the request: PR #12 on GitHub, MR !12 on GitLab.
func requestName(forge string, n int) string {
	if forge == plan.ForgeGitLab {
		return fmt.Sprintf("MR !%d", n)
	}
	return fmt.Sprintf("PR #%d", n)
}

// createsRequest tells whether a tool call opens a pull or merge request.
func createsRequest(call string) bool {
	return strings.Contains(call, "gh pr create") || strings.Contains(call, "glab mr create")
}

// openedIn is the address the last gh pr create or glab mr create of the task printed, and its number; 0 when none
// did.
func openedIn(ctx context.Context, r store.Reader, taskID string) (string, int) {
	events, err := store.List[*planv1.TaskEvent](ctx, r, store.Where{"task_id": taskID})
	if err != nil {
		return "", 0
	}
	slices.SortFunc(events, func(a, b *planv1.TaskEvent) int { return cmp.Compare(a.GetSeq(), b.GetSeq()) })
	url, number, creating := "", 0, false
	for _, ev := range events {
		switch ev.GetKind() {
		case planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL:
			creating = createsRequest(ev.GetText())
		case planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT:
			if !creating {
				continue
			}
			creating = false
			if all := requestURL.FindAllStringSubmatch(ev.GetText(), -1); len(all) > 0 {
				m := all[len(all)-1]
				if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
					url, number = m[0], n
				}
			}
		}
	}
	return url, number
}

// forgeJSON runs a gh or glab command in the project's folder, and decodes its JSON output into v; false when it
// failed, or printed no JSON.
func (h *Harness) forgeJSON(ctx context.Context, repo string, args []string, v any) bool {
	ctx, cancel := context.WithTimeout(ctx, forgeLookTimeout)
	defer cancel()
	out, code, err := h.runCmd(ctx, repo, args)
	if err != nil || code != 0 {
		return false
	}
	return json.Unmarshal([]byte(strings.TrimSpace(out)), v) == nil
}

// requestNumbered is the request number n in the project's repository, when it is open; nil otherwise.
func (h *Harness) requestNumbered(ctx context.Context, repo, forge string, n int) *request {
	var view struct {
		Number int    `json:"number"`
		IID    int    `json:"iid"`
		State  string `json:"state"`
		URL    string `json:"url"`
		WebURL string `json:"web_url"`
	}
	args := []string{"gh", "pr", "view", strconv.Itoa(n), "--json", "number,state,url"}
	if forge == plan.ForgeGitLab {
		args = []string{"glab", "mr", "view", strconv.Itoa(n), "--output", "json"}
	}
	if !h.forgeJSON(ctx, repo, args, &view) {
		return nil
	}
	if state := strings.ToUpper(view.State); state != "OPEN" && state != "OPENED" {
		return nil
	}
	return &request{Number: cmp.Or(view.Number, view.IID), URL: cmp.Or(view.URL, view.WebURL)}
}

// requestFor is the request open for branch in the project's repository; nil when none is.
func (h *Harness) requestFor(ctx context.Context, repo, forge, branch string) *request {
	var list []struct {
		Number int    `json:"number"`
		IID    int    `json:"iid"`
		URL    string `json:"url"`
		WebURL string `json:"web_url"`
	}
	args := []string{"gh", "pr", "list", "--head", branch, "--state", "open", "--json", "number,url"}
	if forge == plan.ForgeGitLab {
		args = []string{"glab", "mr", "list", "--source-branch", branch, "--output", "json"}
	}
	if branch == "" || !h.forgeJSON(ctx, repo, args, &list) || len(list) == 0 {
		return nil
	}
	return &request{Number: cmp.Or(list[0].Number, list[0].IID), URL: cmp.Or(list[0].URL, list[0].WebURL)}
}

// openedBy is the request task t opened, still open: the one its gh pr create or glab mr create printed, when it is
// of the project's repository, else the one open for its branch. nil when none is.
func (h *Harness) openedBy(ctx context.Context, repo, forge string, t *planv1.Task) *request {
	if url, n := openedIn(ctx, h.store, t.GetId()); n > 0 {
		if req := h.requestNumbered(ctx, repo, forge, n); req != nil && strings.EqualFold(strings.TrimSuffix(req.URL, "/"), url) {
			return req
		}
	}
	return h.requestFor(ctx, repo, forge, t.GetBranch())
}

// findRequest is the request the tasks opened, the first that did, else the one open for the wish's branch in the
// project; with the code of the task that opened it, empty for the wish's. nil when none is open.
func (h *Harness) findRequest(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, forge string, tasks []*planv1.Task,
) (*request, string) {
	repo := project.GetDirectory()
	for _, t := range tasks {
		if req := h.openedBy(ctx, repo, forge, t); req != nil {
			return req, t.GetCode()
		}
	}
	if req := h.requestFor(ctx, repo, forge, plan.IntegrationBranchOf(wish, project.GetId())); req != nil {
		return req, ""
	}
	return nil, ""
}

// babysitState is the file the watcher of task id keeps what it said in, for the request number of skill: under
// $DJINN_HOME/watchers/<task>, its folder made.
func (h *Harness) babysitState(id, skill string, number int) string {
	path := babysitStatePath(h.home, id, skill, number)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		logx.L().Warn("make a watcher's folder", "loop", "babysit", "task", id, "err", err)
	}
	return path
}

// babysitStatePath is the file babysitState gives, without making its folder.
func babysitStatePath(home, id, skill string, number int) string {
	return filepath.Join(home, "watchers", id, fmt.Sprintf("%s-%d", skill, number))
}

// methodBabysit journals a watcher Djinn found babysitting a request already, the request recorded on it; the request
// is the task.
const methodBabysit = "harness/babysit"

// babysat is the code of a task that babysits the request url already, or of the watcher of a wish made from a
// template that runs decl's command on it in the project and is not granted; empty when none does. A watcher of any
// wish that runs or resumes in the project on decl's command for the request, spawned by hand, babysits it too: it is
// given as well, for its babysit to be recorded.
func babysat(ctx context.Context, r store.Reader, projectID, url string, decl *plan.Babysit, number int) (string, *planv1.Task, error) {
	tasks, err := store.List[*planv1.Task](ctx, r, nil)
	if err != nil {
		return "", nil, err
	}
	for _, t := range tasks {
		if url != "" && strings.EqualFold(t.GetBabysit().GetUrl(), url) {
			return t.GetCode(), nil, nil
		}
	}
	for _, t := range tasks {
		if s := t.GetStatus(); !watching(t) || t.GetProjectId() != projectID || t.GetBabysit().GetUrl() != "" ||
			(s != planv1.TaskStatus_TASK_STATUS_RUNNING && s != planv1.TaskStatus_TASK_STATUS_RESUMING) {
			continue
		}
		if command, err := lastPrompt(ctx, r, t.GetId()); err == nil && decl.Watches(command, number) {
			return t.GetCode(), t, nil
		}
	}
	wishes, err := store.List[*planv1.Wish](ctx, r, nil)
	if err != nil {
		return "", nil, err
	}
	for _, w := range wishes {
		if t := w.GetTemplate(); t.GetProjectId() == projectID && decl.Watches(t.GetWatch(), number) &&
			w.GetState() != planv1.WishState_WISH_STATE_GRANTED {
			return w.GetTitle(), nil, nil
		}
	}
	return "", nil, nil
}

// adoptBabysit records babysit on the watcher id, which watches its request already: its run takes it at its next
// write (carry), its done line with it.
func (h *Harness) adoptBabysit(ctx context.Context, id string, babysit *planv1.TaskBabysit) error {
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		task, err := store.Get[*planv1.Task](ctx, tx, id)
		if err != nil {
			return err
		}
		task.Babysit = babysit
		if err := tx.Journal(actorHarness, methodBabysit, task); err != nil {
			return err
		}
		return tx.Put(task)
	})
	if err == nil {
		h.notify()
	}
	return err
}

// babysitOpened looks, once the work task t ended done, for the request it opened, and starts the project's babysit
// watcher on it, in the background: Close waits for it.
func (h *Harness) babysitOpened(t *planv1.Task) {
	if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || t.GetProjectId() == "" || watching(t) || questionWorker(t) {
		return
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.babysitting.Add(1)
	h.mu.Unlock()
	go func() {
		defer logx.Crash("babysit", "task", t.GetCode())
		defer h.babysitting.Done()
		if err := h.babysit(h.ctx, t); err != nil && h.ctx.Err() == nil {
			logx.L().Warn("babysit what a task opened", "loop", "babysit", "task", t.GetCode(), "err", err)
		}
	}()
}

// babysit starts the project's babysit watcher on the request t opened, in t's wish, part of its azima, and tells the
// lead once. Nothing when the project names no babysit, its settings turn it off, nothing is open, or the request is
// babysat already.
func (h *Harness) babysit(ctx context.Context, t *planv1.Task) error {
	project, err := store.Get[*planv1.Project](ctx, h.store, t.GetProjectId())
	if err != nil || !project.GetGit() {
		return err
	}
	if settings, err := plan.LoadSettings(h.home, project); err != nil || !settings.Babysit {
		return err
	}
	forge := forgeOf(ctx, project)
	decl := plan.ProjectBabysit(project, forge)
	if decl == nil {
		return nil
	}
	wish, err := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId())
	if err != nil {
		return err
	}
	req, by := h.findRequest(ctx, wish, project, forge, []*planv1.Task{t})
	if req == nil {
		return nil
	}
	// One look at a time from here: two tasks that end together never start two watchers on one request.
	h.babysitMu.Lock()
	defer h.babysitMu.Unlock()
	code, hand, err := babysat(ctx, h.store, project.GetId(), req.URL, decl, req.Number)
	if err != nil || (code != "" && hand == nil) {
		return err
	}
	name := requestName(forge, req.Number)
	babysit := &planv1.TaskBabysit{
		Url: req.URL, Number: int32(req.Number), Skill: decl.Skill, DoneWhen: decl.DoneWhen, OpenedBy: by,
	}
	babysits := code + " already babysits it"
	if hand != nil {
		// Spawned by hand on the request: it is its babysit, no second watcher polls the forge.
		if err := h.adoptBabysit(ctx, hand.GetId(), babysit); err != nil {
			return err
		}
	} else {
		id := store.NewID()
		watcher, err := h.spawn(ctx, planv1connect.TaskServiceSpawnProcedure, &planv1.TaskServiceSpawnRequest{
			WishId: wish.GetId(), ProjectId: project.GetId(), Title: "Babysit " + name,
			Prompt: decl.Fill(req.Number, h.babysitState(id, decl.Skill, req.Number)), Provider: planv1.Provider_PROVIDER_WATCH,
			Restart: decl.Restart, PartOf: t.GetPartOf(),
		}, &planv1.Task{Id: id, Babysit: babysit})
		if err != nil {
			return err
		}
		code, babysits = watcher.GetCode(), watcher.GetCode()+" babysits it"
	}
	opened := t.GetCode() + " opened " + name
	if by == "" {
		opened = name + " is open for " + plan.IntegrationBranchOf(wish, project.GetId())
	}
	line := fmt.Sprintf("Djinn: %s: %s; follow the skill %s each time it speaks. %s", opened, babysits, decl.Skill, req.URL)
	if err := h.toLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_WATCH, wish.GetId(), code, line); err != nil &&
		!errors.Is(err, plan.ErrNoLead) {
		return fmt.Errorf("tell the lead: %w", err)
	}
	return nil
}

// babysitSpawn makes the spawn of a task with --babysit a watcher of the project's babysit: its command, unless the
// request gives one, its restart and its done line. The request is found as it starts (resolveBabysit).
func babysitSpawn(ctx context.Context, req *planv1.TaskServiceSpawnRequest, project *planv1.Project, task *planv1.Task) (*planv1.TaskServiceSpawnRequest, error) {
	if !req.GetBabysit() {
		return req, nil
	}
	if p := req.GetProvider(); p != planv1.Provider_PROVIDER_UNSPECIFIED && p != planv1.Provider_PROVIDER_WATCH {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("--babysit starts a watcher: its provider is watch"))
	}
	if project == nil || !project.GetGit() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("--babysit watches a pull or merge request: the task's project is no Git repository"))
	}
	decl := plan.ProjectBabysit(project, forgeOf(ctx, project))
	if decl == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"project %s names no babysit: one of its skills declares it under metadata.djinn.babysit (docs/wish-templates.md)",
			project.GetName()))
	}
	req = proto.CloneOf(req)
	req.Provider, req.Restart = planv1.Provider_PROVIDER_WATCH, req.GetRestart() || decl.Restart
	req.Prompt = cmp.Or(req.GetPrompt(), decl.Watch)
	task.Babysit = &planv1.TaskBabysit{Skill: decl.Skill, DoneWhen: decl.DoneWhen}
	return req, nil
}

// resolveBabysit finds the request a babysit watcher spawned by hand watches, as it starts: the one a task it comes
// after opened, else the one open for the wish's branch. It records it on the task, and gives the watcher's command,
// prompt filled.
func (h *Harness) resolveBabysit(ctx context.Context, wish *planv1.Wish, project *planv1.Project, t *planv1.Task, prompt string) (string, error) {
	var after []*planv1.Task
	var codes []string
	for _, id := range t.GetDependsOn() {
		dep, err := store.Get[*planv1.Task](ctx, h.store, id)
		if err != nil {
			return "", err
		}
		after, codes = append(after, dep), append(codes, dep.GetCode())
	}
	forge := forgeOf(ctx, project)
	req, by := h.findRequest(ctx, wish, project, forge, after)
	if req == nil {
		why := "no pull or merge request is open"
		if len(codes) > 0 {
			why += " from " + strings.Join(codes, ", ")
		}
		if b := plan.IntegrationBranchOf(wish, project.GetId()); b != "" {
			why += " nor for " + b
		}
		return "", errors.New(why)
	}
	b := t.GetBabysit()
	b.Url, b.Number, b.OpenedBy = req.URL, int32(req.Number), by
	return plan.FillBabysit(prompt, req.Number, h.babysitState(t.GetId(), b.GetSkill(), req.Number)), nil
}

// babysitPrompt is resolveBabysit for a planned watcher, in t's wish: the command, or why it cannot start.
func (h *Harness) babysitPrompt(ctx context.Context, project *planv1.Project, t *planv1.Task, prompt string) (string, string) {
	wish, err := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId())
	if err == nil {
		prompt, err = h.resolveBabysit(ctx, wish, project, t, prompt)
	}
	if err != nil {
		return "", "--babysit: " + err.Error()
	}
	return prompt, ""
}
