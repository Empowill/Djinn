package harness

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Integration (T07): a worker's work counts once it is in its wish's integration branch, tested. When a worker ends
// done in a project that names a check, its task's integration is pending; Djinn then commits the work at once,
// task by task, by itself, no model: a task whose worktree holds changes not committed goes to a review worker instead
// (review.go), and nothing commits them blindly; in a worktree of its own per wish and project, never the person's
// checkout, it merges the task's branch, makes the generated files again on a conflict only in them, runs the
// project's commit checks (checks.go) each through a gate, moves the integration branch when they pass, and removes the
// task's worktree when clean. Pushing the branch to its remote follows on a cadence (push.go).

// How the journal records the integration.
const (
	methodIntegrate = "harness/integrate" // a task's integration moved on, Djinn recorded a wish's integration branch, or asked what to do with work that failed; the request is the task, the wish, or the question
	methodCommit    = "harness/commit"    // a batch was committed into its wish's integration branch; the request is the commit (planv1.IntegrationCommit)
)

// The gates the integration's commands run under, as a worker's would: djinn gate run gen. Each check runs under
// the gate of its name, djinn gate run test, and the setup under djinn gate run setup.
const (
	genGate     = "gen"
	installGate = "install"
)

// TakeGate takes the gate name, for the task taskID, to run the command what in the folder dir, and returns how to
// give it back. It calls waiting, when not nil, each time the reason it waits changes. djinn up takes the gates of
// internal/gate, as djinn gate run does.
type TakeGate func(ctx context.Context, name, taskID, what, dir string, waiting func(why string)) (give func(), err error)

// RunCommand runs args in dir, and returns the end of its output and its exit code; an error when it could not run.
type RunCommand func(ctx context.Context, dir string, args []string) (out string, code int, err error)

// WithGates runs the integration's commands under the gates take gives; without it, they run at once.
func WithGates(take TakeGate) Option { return func(h *Harness) { h.gates = take } }

// WithCommands runs the integration's commands with run, instead of the processes they name. Tests give a fake.
func WithCommands(run RunCommand) Option { return func(h *Harness) { h.commands = run } }

// TaskSummary is what a task of the build brought, and its worker's final words.
type TaskSummary struct {
	Code    string
	Title   string
	Summary string
}

// Built is a wish's integration branch as Djinn pushed it, in a project whose settings name an install command: djinn
// up proposes to install it, and to restart on it (WithBuilt).
type Built struct {
	WishID, WishTitle, ProjectID, Project, Branch, Sha string
	// The tasks whose work the push brought, by code.
	Tasks []string
	// The titles of the commits the push brought, the latest first.
	Changes []string
	// What changed, one per task: its code and title, and its worker's summary.
	Summaries []TaskSummary
	// The install command.
	Install string
}

// WithBuilt calls built after each push of an integration branch in a project whose settings name an install command.
func WithBuilt(built func(Built)) Option { return func(h *Harness) { h.built = built } }

// WithIntegrateTick sets how often the integration looks at the work waiting without being woken: an hour passes
// without telling anyone.
func WithIntegrateTick(d time.Duration) Option { return func(h *Harness) { h.integrateTick = d } }

// Integrate starts committing the tasks' finished work into their wishes' integration branches, by itself, and
// pushing them: woken when a task ends done or a push is approved, and every minute. djinn up calls it once the
// harness has recovered; Close stops it, and the work it was on is integrated again at the next start.
func (h *Harness) Integrate() {
	h.integrating.Do(func() {
		h.integrateDone = make(chan struct{})
		go func() {
			defer close(h.integrateDone)
			tick := time.NewTicker(cmp.Or(h.integrateTick, time.Minute))
			defer tick.Stop()
			for {
				h.integratePass(h.ctx)
				select {
				case <-h.ctx.Done():
					return
				case <-h.integrateKick:
				case <-tick.C:
				}
			}
		}()
	})
}

// kickIntegrate asks the integration for a pass: a task's work waits.
func (h *Harness) kickIntegrate() {
	select {
	case h.integrateKick <- struct{}{}:
	default:
	}
}

// pendIntegration marks the work of t, a task its worker just finished, as waiting to be merged, when Djinn
// integrates it: a work task on a branch of its own, in a project whose settings name a check.
func (h *Harness) pendIntegration(t *planv1.Task) bool {
	if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || t.GetBranch() == "" || plan.IsAzima(t) {
		return false
	}
	project, err := store.Get[*planv1.Project](context.Background(), h.store, t.GetProjectId())
	if err != nil || !project.GetGit() {
		return false
	}
	if settings, err := plan.LoadSettings(h.home, project); err != nil || !settings.Integrates() {
		return false
	}
	t.Integration = &planv1.TaskIntegration{
		State: planv1.IntegrationState_INTEGRATION_STATE_PENDING, UpdateTime: timestamppb.New(h.now()),
	}
	return true
}

// recoverIntegrations puts back to pending the work a previous djinn up was integrating when it stopped: its batch
// is integrated again.
func (h *Harness) recoverIntegrations(ctx context.Context, tasks []*planv1.Task) {
	var cut []*planv1.Task
	for _, t := range tasks {
		if t.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING {
			cut = append(cut, t)
		}
	}
	if len(cut) > 0 {
		h.settleIntegration(ctx, cut, pending(cut[0].GetIntegration().GetBranch(), ""),
			"integration: djinn up stopped during it; Djinn integrates the work again", nil)
	}
}

// integratePass commits the finished work waiting, each task alone in the order they ended, pushes the integration
// branches whose push is due after each commit, and the pushes the person approved; then it merges main into the
// integration branches where it is due (main.go).
func (h *Harness) integratePass(ctx context.Context) {
	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil {
		log.Printf("djinn: integrate: %v", err)
		return
	}
	h.correctionsEnded(ctx, tasks)
	h.followStackedBranches(ctx, tasks)
	var waiting []*planv1.Task
	for _, t := range tasks {
		if t.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_PENDING &&
			t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE && !h.running(t.GetId()) {
			waiting = append(waiting, t)
		}
	}
	slices.SortStableFunc(waiting, func(a, b *planv1.Task) int {
		return a.GetEndTime().AsTime().Compare(b.GetEndTime().AsTime())
	})
	for _, t := range waiting {
		if ctx.Err() != nil {
			return
		}
		wish, err1 := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId())
		project, err2 := store.Get[*planv1.Project](ctx, h.store, t.GetProjectId())
		if err := errors.Join(err1, err2); err != nil {
			log.Printf("djinn: integrate: %v", err)
			continue
		}
		if h.integrateBatch(ctx, wish, project, []*planv1.Task{t}) {
			h.pushIfDue(ctx, wish.GetId(), project)
		}
	}
	h.pushApproved(ctx)
	h.mainPass(ctx)
	h.computedMovesPass(ctx)
}

// running tells whether a worker runs for the task id: a task continued after it was done.
func (h *Harness) running(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runs[id] != nil
}

// pending is the integration of work that waits to be merged, and why when it waits for something else.
func pending(branch, why string) *planv1.TaskIntegration {
	return &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_PENDING, Branch: branch, Reason: why}
}

// tested is a batch whose tests passed while its branch could not move, from old to sha: committing it again needs no
// new run.
type tested struct {
	old, sha string
	ids      []string
}

// integrateBatch commits the work of batch, a task of wish in project, with the work a review or a correction worker
// of it reviews or corrects, into the wish's integration branch, records where each task's work stands, and tells
// whether it is committed. A task whose worktree holds changes not committed is not merged: a review worker judges
// them. The worktrees of the work committed are removed when clean.
func (h *Harness) integrateBatch(ctx context.Context, wish *planv1.Wish, project *planv1.Project, batch []*planv1.Task) bool {
	h.integrateMu.Lock() // The push checks use the integration worktree too.
	defer h.integrateMu.Unlock()
	settings, err := plan.LoadSettings(h.home, project)
	var branch, why string
	var ok bool
	switch {
	case err != nil:
		why = err.Error()
	case !settings.Integrates():
		why = errNoChecks.Error()
		branch, _ = h.integrationBranch(ctx, wish, project)
	default:
		branch, why, ok = h.targetBranch(ctx, wish, project, settings, batch)
		if !ok {
			return false
		}
	}
	if why != "" {
		h.settleIntegration(ctx, batch, pending(branch, why), "integration: waiting: "+why, nil)
		return false
	}
	if batch = h.reviewUncommitted(ctx, wish, project, settings, branch, batch); len(batch) == 0 {
		return false
	}
	in, text, commit := h.commitBatch(ctx, wish, project, settings, branch, batch)
	if ctx.Err() != nil && in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
		in, text, commit = pending(branch, ""), "integration: djinn up stopped during it; Djinn integrates the work again", nil
	}
	in.Branch = branch
	if in.GetFailure() != nil {
		h.failed(ctx, wish, project, settings, batch, in, text)
		return false
	}
	h.settleIntegration(ctx, batch, in, text, commit)
	if in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
		return false
	}
	settled := append(slices.Clone(batch), h.settleReviewed(ctx, batch, in)...)
	settled = append(settled, h.settleCorrected(ctx, settled, in)...)
	h.settleMain(ctx, wish, project, settled, in) // A correction worker of main's merge brought main in.
	for _, t := range settled {
		h.dropWorktree(ctx, project, t)
	}
	oldSha := ""
	if commit != nil {
		oldSha = commit.GetOldSha()
	}
	moves := h.handleMergeMoves(ctx, wish, project, branch, oldSha, in.GetSha(), settled)
	leadLine := leadMergeLine(codes(batch), branch, in.GetSha(), moves)
	h.tellLead(ctx, wish.GetId(), leadLine)
	h.wake() // The tasks that waited for this work to be committed may start, from it.
	return true
}

// dropWorktree removes the worktree of t, whose work is committed, as djinn task clean does: its branch stays. A
// worktree with changes not committed stays, said; so does the worktree of a task a worker runs again.
func (h *Harness) dropWorktree(ctx context.Context, project *planv1.Project, t *planv1.Task) {
	ctx = context.WithoutCancel(ctx)
	cur, err := store.Get[*planv1.Task](ctx, h.store, t.GetId())
	if err != nil || cur.GetWorktree() == "" || h.running(t.GetId()) {
		return
	}
	text := "worktree removed, branch " + cur.GetBranch() + " kept"
	// A review worker works in the worktree of the task it reviews: the second of them finds it removed already.
	if _, err := os.Stat(cur.GetWorktree()); !errors.Is(err, os.ErrNotExist) {
		if changes, err := git(ctx, cur.GetWorktree(), "status", "--porcelain"); err == nil && changes != "" {
			text = "worktree kept: it holds changes not committed, " + cur.GetWorktree()
		} else if err := removeWorktree(ctx, project.GetDirectory(), cur.GetWorktree(), false); err != nil {
			text = "worktree kept: " + err.Error()
		}
	}
	kept := strings.HasPrefix(text, "worktree kept")
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		task, err := store.Get[*planv1.Task](ctx, tx, t.GetId())
		if err != nil {
			return err
		}
		if !kept {
			task.Worktree = ""
		}
		if err := tx.Journal(actorHarness, methodIntegrate, task); err != nil {
			return err
		}
		if err := tx.Put(task); err != nil {
			return err
		}
		seq, err := lastSeq(ctx, tx, task.GetId())
		if err != nil {
			return err
		}
		return tx.Put(newEvent(task.GetId(), seq+1, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text}))
	})
	if err != nil {
		log.Printf("djinn: integrate %s: %v", t.GetCode(), err)
	}
	h.notify()
}

// How long the lists of a build proposed are: the commits' titles, and a worker's last paragraph.
const (
	buildChanges = 30
	buildSummary = 400
)

// build is the integration branch as push pushed it, with the work of tasks, to propose for installing.
func (h *Harness) build(ctx context.Context, wish *planv1.Wish, project *planv1.Project, install string, push *planv1.IntegrationPush, tasks []*planv1.Task) Built {
	b := Built{
		WishID: wish.GetId(), WishTitle: wish.GetTitle(), ProjectID: project.GetId(), Project: project.GetName(),
		Branch: push.GetBranch(), Sha: push.GetNewSha(), Changes: push.GetCommits(), Install: install,
	}
	for _, t := range tasks {
		b.Tasks = append(b.Tasks, t.GetCode())
		b.Summaries = append(b.Summaries, TaskSummary{
			Code:    t.GetCode(),
			Title:   t.GetTitle(),
			Summary: h.lastWords(ctx, t.GetId()),
		})
	}
	return b
}

// lastWords is the last paragraph the worker of the task wrote, on one line, where it says what it did;
// "" when it wrote nothing.
func (h *Harness) lastWords(ctx context.Context, taskID string) string {
	var last *planv1.TaskEvent
	err := store.Latest(ctx, h.store, store.Where{"task_id": taskID}, func(e *planv1.TaskEvent) bool {
		if e.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_TEXT && strings.TrimSpace(e.GetText()) != "" {
			last = e
		}
		return last == nil
	})
	if err != nil || last == nil {
		return ""
	}
	paragraphs := strings.Split(strings.TrimSpace(last.GetText()), "\n\n")
	words := strings.Join(strings.Fields(paragraphs[len(paragraphs)-1]), " ")
	if r := []rune(words); len(r) > buildSummary {
		words = string(r[:buildSummary-1]) + "…"
	}
	return words
}

// InstallStep is where an install stands, as Install tells it.
type InstallStep int

const (
	// InstallWaiting: it waits for its turn, for what Install says with it.
	InstallWaiting InstallStep = iota + 1
	// InstallPreparing: it checks the build out in its worktree, and sets it up.
	InstallPreparing
	// InstallBuilding: it runs the install command.
	InstallBuilding
)

// Install runs the install command of the project's settings on sha, a build of the wish's integration branch there:
// in the project's install worktree, checked out at it, never the person's checkout nor an integration worktree, under
// the install gate. It never waits for an integration: the build is a commit already made, and its worktree is its
// own. One install runs at a time. progress, when not nil, is told each step as it comes, and what the install waits
// for when it waits ("" otherwise). It returns the end of the command's output, and an error when it did not end well.
func (h *Harness) Install(ctx context.Context, wishID, projectID, sha string, progress func(step InstallStep, waiting string)) (string, error) {
	if progress == nil {
		progress = func(InstallStep, string) {}
	}
	wish, err1 := store.Get[*planv1.Wish](ctx, h.store, wishID)
	project, err2 := store.Get[*planv1.Project](ctx, h.store, projectID)
	if err := errors.Join(err1, err2); err != nil {
		return "", err
	}
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil {
		return "", err
	}
	if settings.Install == "" {
		return "", errors.New("the project's settings name no install command")
	}
	branch := plan.IntegrationBranchOf(wish, projectID)
	branches := []string{branch}
	if strat, _ := plan.ResolveWishPushStrategy(wish, settings); strat == planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
		branches = append(branches, h.azimaBranchesOf(ctx, wish, project)...)
	}
	found := false
	for _, b := range branches {
		if b == "" {
			continue
		}
		if _, err := git(ctx, project.GetDirectory(), "merge-base", "--is-ancestor", sha, "refs/heads/"+b); err == nil {
			found = true
			branch = b
			break
		}
	}
	if !found {
		return "", fmt.Errorf("%s is not a commit of the wish's integration branch %s", short8(sha), branch)
	}
	if !h.installMu.TryLock() {
		h.mu.Lock()
		other := h.installing
		h.mu.Unlock()
		progress(InstallWaiting, "the install of "+short8(other)+" to end")
		h.installMu.Lock()
	}
	defer h.installMu.Unlock()
	h.mu.Lock()
	h.installing = sha
	h.mu.Unlock()
	step := InstallPreparing
	progress(step, "")
	ctx = context.WithValue(ctx, gateWaitKey{}, func(why string) {
		if why == "" {
			progress(step, "")
		} else {
			progress(InstallWaiting, why)
		}
	})
	wt := installDir(h.home, projectID)
	dir, _, err := integrationWorktree(ctx, project.GetDirectory(), wt, sha)
	if err != nil {
		return "", fmt.Errorf("prepare the install worktree: %w", err)
	}
	if why, out, _ := h.setUp(ctx, project, settings, wishID, "", wt, dir); why != "" {
		return out, fmt.Errorf("%s: %s", setupName, why)
	}
	step = InstallBuilding
	progress(step, "")
	out, code, err := h.command(ctx, installGate, "", settings.Install, dir)
	switch {
	case err != nil:
		return out, fmt.Errorf("%s could not run: %w", settings.Install, err)
	case code != 0:
		return out, fmt.Errorf("%s exited %d%s", settings.Install, code, tail(out))
	}
	h.mu.Lock()
	h.installed[projectID] = sha
	h.mu.Unlock()
	_ = os.MkdirAll(filepath.Dir(installedFile(h.home, projectID)), 0o755)
	_ = os.WriteFile(installedFile(h.home, projectID), []byte(sha+"\n"), 0o600)
	h.closeInstallQuestions(ctx, projectID, sha)
	return out, nil
}

// installDir is the worktree the builds of a project are installed from, whatever their wish: in Djinn's data folder,
// next to its integration worktrees, apart from them so that an install never waits for an integration.
func installDir(home, projectID string) string {
	return filepath.Join(home, "projects", projectID, "install")
}

// installedFile is the file that records the sha of the build installed last for projectID.
func installedFile(home, projectID string) string {
	return filepath.Join(home, "projects", projectID, "installed")
}

// installedSha returns the sha of the build installed last in projectID:
// checked from memory first, then installed file, then the install worktree's HEAD.
func (h *Harness) installedSha(ctx context.Context, projectID string) string {
	h.mu.Lock()
	sha, ok := h.installed[projectID]
	h.mu.Unlock()
	if ok && sha != "" {
		return sha
	}
	if data, err := os.ReadFile(installedFile(h.home, projectID)); err == nil {
		s := strings.TrimSpace(string(data))
		if s != "" {
			h.mu.Lock()
			h.installed[projectID] = s
			h.mu.Unlock()
			return s
		}
	}
	wt := installDir(h.home, projectID)
	if s := commitOf(ctx, wt, "HEAD"); s != "" {
		h.mu.Lock()
		h.installed[projectID] = s
		h.mu.Unlock()
		return s
	}
	return ""
}

// closeInstallQuestions closes open install questions for projectID when sha was installed:
// the question for sha is marked installed (Choice A); open questions for older shas are marked superseded (Choice B).
func (h *Harness) closeInstallQuestions(ctx context.Context, projectID, sha string) {
	questions, err := store.List[*planv1.Question](ctx, h.store, nil)
	if err != nil {
		return
	}
	wishesByID := make(map[string]*planv1.Wish)
	s := short8(sha)
	for _, q := range questions {
		if !q.GetMove() || q.GetAnswer() != nil {
			continue
		}
		if !strings.Contains(q.GetText(), "Install and restart") {
			continue
		}
		if !h.isQuestionForProject(ctx, q, projectID, wishesByID) {
			continue
		}
		if strings.Contains(q.GetText(), s) {
			_ = h.closeMove(ctx, q, planv1.Choice_CHOICE_A, "installed")
		} else {
			_ = h.closeMove(ctx, q, planv1.Choice_CHOICE_B, fmt.Sprintf("superseded by build %s", s))
		}
	}
}

// isQuestionForProject tells whether question q belongs to a wish that targets projectID.
func (h *Harness) isQuestionForProject(ctx context.Context, q *planv1.Question, projectID string, wishesByID map[string]*planv1.Wish) bool {
	if q.GetWishId() == "" {
		return false
	}
	w, ok := wishesByID[q.GetWishId()]
	if !ok {
		var err error
		w, err = store.Get[*planv1.Wish](ctx, h.store, q.GetWishId())
		if err != nil {
			return false
		}
		wishesByID[q.GetWishId()] = w
	}
	return slices.Contains(w.GetProjectIds(), projectID)
}

// closeMove records answer on an open move question q, journaled and notified.
func (h *Harness) closeMove(ctx context.Context, q *planv1.Question, choice planv1.Choice, note string) error {
	ctx = context.WithoutCancel(ctx)
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		cur, err := store.Get[*planv1.Question](ctx, tx, q.GetId())
		if err != nil {
			return err
		}
		if cur.GetAnswer() != nil {
			return nil
		}
		cur.Answer = &planv1.Answer{
			Choice:     choice,
			Note:       note,
			CreateTime: timestamppb.New(h.now()),
		}
		if err := tx.Journal(actorHarness, methodIntegrate, cur); err != nil {
			return err
		}
		return tx.Put(cur)
	})
	if err != nil {
		log.Printf("djinn: move: close: %v", err)
		return err
	}
	q.Answer = &planv1.Answer{
		Choice:     choice,
		Note:       note,
		CreateTime: timestamppb.New(h.now()),
	}
	h.notify()
	return nil
}

// integrationBranch is the branch wish integrates its work into in project, and why there is none. A wish made before
// Djinn recorded it takes the branch the project's checkout is on now, recorded on the wish.
func (h *Harness) integrationBranch(ctx context.Context, wish *planv1.Wish, project *planv1.Project) (string, string) {
	if b := plan.IntegrationBranchOf(wish, project.GetId()); b != "" {
		return b, ""
	}
	b := plan.CheckedOutBranch(ctx, project.GetDirectory())
	if b == "" {
		return "", "the project's checkout is on no branch: name the integration branch with djinn wish set-integration"
	}
	plan.SetIntegrationBranch(wish, project.GetId(), b)
	ctx = context.WithoutCancel(ctx)
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		cur, err := store.Get[*planv1.Wish](ctx, tx, wish.GetId())
		if err != nil {
			return err
		}
		plan.SetIntegrationBranch(cur, project.GetId(), b)
		if err := tx.Journal(actorHarness, methodIntegrate, cur); err != nil {
			return err
		}
		return tx.Put(cur)
	})
	if err != nil {
		return "", "record the integration branch: " + err.Error()
	}
	return b, ""
}

// commitBatch merges the branches of batch, tasks whose worktree holds nothing not committed, into the integration
// branch in the wish's integration worktree, checks the result, and moves the branch when the commit checks pass. It
// returns where the batch's work stands, the event that says it, and the commit to journal when the branch moved.
func (h *Harness) commitBatch(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, branch string, batch []*planv1.Task,
) (*planv1.TaskIntegration, string, *planv1.IntegrationCommit) {
	repo := project.GetDirectory()
	wait := func(why string) (*planv1.TaskIntegration, string, *planv1.IntegrationCommit) {
		return pending(branch, why), "integration: waiting: " + why, nil
	}
	old, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return wait("the branch " + branch + " is not in the project's repository")
	}
	taskIDs := ids(batch)
	key := wish.GetId() + "/" + project.GetId() + "/" + branch
	done, ok := h.tested[key]
	if !ok || done.old != old || !slices.Equal(done.ids, taskIDs) {
		h.settleIntegration(ctx, batch, &planv1.TaskIntegration{
			State: planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING, Branch: branch,
		}, "integration: integrating into "+branch+", with "+codes(batch), nil)
		var in *planv1.TaskIntegration
		var text string
		if done.sha, in, text = h.mergeAndTest(ctx, wish, project, settings, branch, old, batch); in != nil {
			return in, text, nil
		}
		done.old, done.ids = old, taskIDs
		h.tested[key] = done
	}
	sha := done.sha
	commit := &planv1.IntegrationCommit{
		WishId: wish.GetId(), ProjectId: project.GetId(), Branch: branch, OldSha: old, NewSha: sha,
		TaskIds: append(append(taskIDs, correctedIDs(batch)...), reviewedIDs(batch)...),
	}
	committed := &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, Sha: sha}
	if sha == old {
		delete(h.tested, key)
		return committed, "integration: committed: " + branch + " holds the work already, at " + short8(sha), nil
	}
	holder, why, moved := moveBranch(ctx, repo, branch, old, sha, "djinn: integrate "+codes(batch))
	if why != "" {
		if moved {
			delete(h.tested, key)
			why += ": Djinn integrates the work again"
		}
		return wait(why)
	}
	delete(h.tested, key)
	text := fmt.Sprintf("integration: committed into %s as %s, with %s", branch, short8(sha), codes(batch))
	if holder != "" {
		text += "; your checkout of it, " + holder + ", follows"
	}
	return committed, text, commit
}

// moveBranch moves branch from old to sha, tested green, in the repository holding repo, git checking it is still at
// old; the person's checkout of it, if any, follows when clean. It returns that checkout ("" for none); or why the
// branch stays at old, and whether it is because the branch moved meanwhile.
func moveBranch(ctx context.Context, repo, branch, old, sha, message string) (holder, why string, moved bool) {
	// The person's checkout of the branch, if any, follows it when clean. One with changes is left alone, and the
	// branch with it: moved under it, its next commit would undo the work.
	holder, err := checkoutOf(ctx, repo, branch)
	if err != nil {
		return "", err.Error(), false
	}
	if holder != "" {
		if changes, err := git(ctx, holder, "status", "--porcelain", "--untracked-files=no"); err != nil || changes != "" {
			return "", fmt.Sprintf("tested green as %s; %s stays at %s: your checkout of it, %s, has changes not committed "+
				"and is left as it is; Djinn moves the branch once they are committed or put aside", short8(sha), branch, short8(old), holder), false
		}
	}
	if _, err := git(ctx, repo, "update-ref", "-m", message, "refs/heads/"+branch, sha, old); err != nil {
		return "", branch + " moved during the integration", true
	}
	if holder != "" {
		// A two-way merge from the old tip to the new one: what a fast-forward does to the files, refused when one in
		// the way is not tracked.
		if _, err := git(ctx, holder, "read-tree", "-m", "-u", old, sha); err != nil {
			if _, back := git(ctx, repo, "update-ref", "refs/heads/"+branch, old, sha); back != nil {
				log.Printf("djinn: integrate: put %s back at %s: %v", branch, old, back)
			}
			return "", fmt.Sprintf("tested green as %s; %s stays at %s: your checkout of it, %s, cannot follow: %v",
				short8(sha), branch, short8(old), holder, err), false
		}
	}
	return holder, "", false
}

// mergeAndTest merges batch's branches from old in the wish's integration worktree, makes it ready with the setup,
// and runs the commit checks there. It returns the commit checked green; or where the batch stands when it is not,
// and the event that says it. A conflict in code, or a red check, says what failed as a correction worker starts from
// it (IntegrationFailure).
func (h *Harness) mergeAndTest(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, branch, old string, batch []*planv1.Task,
) (string, *planv1.TaskIntegration, string) {
	fail := func(state planv1.IntegrationState, why string, f *planv1.IntegrationFailure) (string, *planv1.TaskIntegration, string) {
		what := "conflict"
		if state == planv1.IntegrationState_INTEGRATION_STATE_RED {
			what = "red"
		}
		if f != nil {
			f.State, f.Reason = state, why
		}
		return "", &planv1.TaskIntegration{State: state, Reason: why, Failure: f},
			"integration: " + what + ": " + why + "; " + branch + " stays as it was"
	}
	wait := func(why string) (string, *planv1.TaskIntegration, string) {
		return "", pending(branch, why), "integration: waiting: " + why
	}
	repo := project.GetDirectory()
	wt := integrationDir(h.home, project.GetId(), wish.GetId())
	dir, prefix, err := integrationWorktree(ctx, repo, wt, old)
	if err != nil {
		return wait("prepare the integration worktree: " + err.Error())
	}
	for i, t := range batch {
		base, err := git(ctx, wt, "rev-parse", "HEAD")
		if err != nil {
			return wait(err.Error())
		}
		// What a correction starts from, when this merge conflicts: the merges before it, and this one again.
		conflict := func(files []string) *planv1.IntegrationFailure {
			return &planv1.IntegrationFailure{Base: base, MergeBranch: t.GetBranch(), Files: files, TaskIds: ids(batch[:i+1])}
		}
		msg := fmt.Sprintf("Merge branch '%s' into %s", t.GetBranch(), branch)
		_, err = git(ctx, wt, "merge", "--no-ff", "--no-edit", "--quiet", "-m", msg, t.GetBranch())
		if err == nil {
			h.integrationStep(ctx, t, "integration: merged "+t.GetBranch())
			continue
		}
		conflicts := conflicted(ctx, wt)
		var why string
		switch {
		case len(conflicts) == 0:
			_, _ = git(ctx, wt, "merge", "--abort")
			return fail(planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, fmt.Sprintf("merge %s: %v", t.GetCode(), err), nil)
		case !generatedOnly(conflicts, prefix, settings.Generated) || settings.Generate == "":
			why = fmt.Sprintf("%s conflicts with %s in %s", t.GetCode(), branch, strings.Join(conflicts, ", "))
		default:
			why = h.settleGenerated(ctx, t.GetCode(), t.GetId(), wt, dir, settings.Generate, conflicts, func() string {
				failed, _, _ := h.setUp(ctx, project, settings, wish.GetId(), t.GetId(), wt, dir)
				return failed
			})
		}
		if why != "" {
			_, _ = git(ctx, wt, "merge", "--abort")
			return fail(planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, why, conflict(conflicts))
		}
		h.integrationStep(ctx, t, fmt.Sprintf("integration: merged %s; its conflict, only in generated files (%s), settled by %s",
			t.GetBranch(), strings.Join(conflicts, ", "), settings.Generate))
	}
	sha, err := git(ctx, wt, "rev-parse", "HEAD")
	if err != nil {
		return wait(err.Error())
	}
	red := func(command, why, out string) (string, *planv1.TaskIntegration, string) {
		return fail(planv1.IntegrationState_INTEGRATION_STATE_RED, why,
			&planv1.IntegrationFailure{Base: sha, Command: command, Output: tail(out), TaskIds: ids(batch)})
	}
	command, why, out, stopped := h.commitChecks(ctx, wish, project, settings, batch[0].GetId(), sha, wt, dir, func(c *planv1.ProjectCheck) {
		h.integrationStep(ctx, batch[0], fmt.Sprintf("integration: checking %s: %s (%s)", codes(batch), c.GetName(), c.GetCommand()))
	})
	switch {
	case stopped != "":
		return wait(stopped)
	case why != "":
		return red(command, why, out)
	}
	return sha, nil, ""
}

// commitChecks makes the integration worktree wt ready with the setup, then runs the project's commit checks on sha
// there, in dir, for the task taskID ("" for none), each through its gate, saying each with step before it runs. It
// returns the command that failed, why, and the end of its output; or why it stopped, when djinn up stopped.
func (h *Harness) commitChecks(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, taskID, sha, wt, dir string,
	step func(*planv1.ProjectCheck),
) (command, why, out, stopped string) {
	checks := settings.ChecksAt(planv1.CheckWhen_CHECK_WHEN_COMMIT)
	if len(checks) == 0 {
		return "", "", "", ""
	}
	if why, out, halted := h.setUp(ctx, project, settings, wish.GetId(), taskID, wt, dir); halted {
		return "", "", "", "djinn up stopped during the setup"
	} else if why != "" {
		return settings.Setup, setupName + ": " + why, out, ""
	}
	for _, c := range checks {
		step(c)
		run := &planv1.CheckRun{
			Name: c.GetName(), Command: c.GetCommand(), When: planv1.CheckWhen_CHECK_WHEN_COMMIT, WishId: wish.GetId(),
			TaskId: taskID, Sha: sha,
		}
		if why, out, halted := h.check(ctx, project.GetId(), run, dir); halted {
			return "", "", "", "djinn up stopped during the check " + c.GetName()
		} else if why != "" {
			return c.GetCommand(), why, out, ""
		}
	}
	return "", "", "", ""
}

// settleGenerated settles a merge of what, a task's code or a branch, whose conflicts are all in generated files: it
// takes the side merged in, makes the worktree ready, makes them again with the command generate, in dir, under the
// gate gen for the task taskID ("" for none), and commits the merge in the worktree wt. It returns why it could not.
func (h *Harness) settleGenerated(
	ctx context.Context, what, taskID, wt, dir, generate string, conflicts []string, ready func() string,
) string {
	if _, err := git(ctx, wt, append([]string{"checkout", "--theirs", "--"}, conflicts...)...); err != nil {
		return err.Error()
	}
	if _, err := git(ctx, wt, append([]string{"add", "--"}, conflicts...)...); err != nil {
		return err.Error()
	}
	if why := ready(); why != "" {
		return fmt.Sprintf("%s conflicts in generated files (%s), and the setup failed: %s", what, strings.Join(conflicts, ", "), why)
	}
	out, code, err := h.command(ctx, genGate, taskID, generate, dir)
	switch {
	case err != nil:
		return fmt.Sprintf("%s conflicts in generated files (%s), and %s could not run: %v", what, strings.Join(conflicts, ", "), generate, err)
	case code != 0:
		return fmt.Sprintf("%s conflicts in generated files (%s), and %s exited %d%s", what, strings.Join(conflicts, ", "), generate, code, tail(out))
	}
	if _, err := git(ctx, wt, "add", "--all"); err != nil {
		return err.Error()
	}
	if _, err := git(ctx, wt, "commit", "--no-edit", "--quiet"); err != nil {
		return err.Error()
	}
	return ""
}

// integrationStep adds an event to the task t, saying a step of its integration.
func (h *Harness) integrationStep(ctx context.Context, t *planv1.Task, text string) {
	h.writeAlone(ctx, actorHarness, methodEvent, nil, t.GetId(), nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})
}

// settleIntegration records where the work of batch stands, with an event saying text on each task, and the commit
// when the branch moved, in one transaction. Each task is read again first: only its integration changes, whatever
// happened to it meanwhile. A task already where in says gets nothing new.
func (h *Harness) settleIntegration(
	ctx context.Context, batch []*planv1.Task, in *planv1.TaskIntegration, text string, commit *planv1.IntegrationCommit,
) {
	ctx = context.WithoutCancel(ctx)
	in.UpdateTime = timestamppb.New(h.now())
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		if commit != nil {
			if err := tx.Journal(actorHarness, methodCommit, commit); err != nil {
				return err
			}
		}
		for _, b := range batch {
			t, err := store.Get[*planv1.Task](ctx, tx, b.GetId())
			if err != nil {
				return err
			}
			b.Integration = in
			if sameIntegration(t.GetIntegration(), in) {
				continue
			}
			t.Integration = proto.CloneOf(in)
			if err := tx.Journal(actorHarness, methodIntegrate, t); err != nil {
				return err
			}
			if err := tx.Put(t); err != nil {
				return err
			}
			seq, err := lastSeq(ctx, tx, t.GetId())
			if err != nil {
				return err
			}
			if err := tx.Put(newEvent(t.GetId(), seq+1, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("djinn: integrate %s: %v", codes(batch), err)
	}
	h.notify()
}

// sameIntegration tells whether a and b say the same, whenever they said it.
func sameIntegration(a, b *planv1.TaskIntegration) bool {
	return a.GetState() == b.GetState() && a.GetBranch() == b.GetBranch() && a.GetSha() == b.GetSha() && a.GetReason() == b.GetReason() &&
		a.GetCorrectedBy() == b.GetCorrectedBy() && a.GetReviewedBy() == b.GetReviewedBy() && a.GetAttempts() == b.GetAttempts() &&
		a.GetQuestionId() == b.GetQuestionId() && proto.Equal(a.GetFailure(), b.GetFailure())
}

// ids lists the tasks' identifiers.
func ids(batch []*planv1.Task) []string {
	out := make([]string, len(batch))
	for i, t := range batch {
		out[i] = t.GetId()
	}
	return out
}

// codes lists the tasks' codes: "W2, W3".
func codes(batch []*planv1.Task) string {
	c := make([]string, len(batch))
	for i, t := range batch {
		c[i] = t.GetCode()
	}
	return strings.Join(c, ", ")
}

// short8 is a commit's first eight characters.
func short8(sha string) string { return sha[:min(8, len(sha))] }

// integrationDir is the worktree a wish integrates its work in, for one project: in Djinn's data folder, next to the
// tasks' worktrees.
func integrationDir(home, projectID, wishID string) string {
	return filepath.Join(home, "projects", projectID, "integration", wishID)
}

// integrationWorktree makes wt, a worktree of the repository holding repo, ready to merge into: detached at old,
// without changes. It returns the project's folder within it, and the project's folder in the repository ("app/"; ""
// at its root).
func integrationWorktree(ctx context.Context, repo, wt, old string) (string, string, error) {
	prefix, err := git(ctx, repo, "rev-parse", "--show-prefix")
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(wt, filepath.FromSlash(prefix))
	if top, err := git(ctx, wt, "rev-parse", "--show-toplevel"); err == nil && samePath(top, wt) {
		_, _ = git(ctx, wt, "merge", "--abort")
		_, _ = git(ctx, wt, "rebase", "--abort")
		if _, err := git(ctx, wt, "reset", "--quiet", "--hard", old); err != nil {
			return "", "", err
		}
		_, err = git(ctx, wt, "clean", "-fdq")
		return dir, prefix, err
	}
	// Not a worktree any more, if it ever was: it is Djinn's own folder, made again, and set up again.
	if err := os.RemoveAll(wt); err != nil {
		return "", "", err
	}
	if err := os.Remove(setupStamp(wt)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	if _, err := git(ctx, repo, "worktree", "prune"); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(wt), 0o700); err != nil {
		return "", "", err
	}
	_, err = git(ctx, repo, "worktree", "add", "--quiet", "--detach", wt, old)
	return dir, prefix, err
}

// samePath tells whether a and b are the same folder, symbolic links resolved.
func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && filepath.Clean(ra) == filepath.Clean(rb)
}

// conflicted lists the files a merge left in conflict in the worktree wt, as the repository names them.
func conflicted(ctx context.Context, wt string) []string {
	out, err := git(ctx, wt, "diff", "--name-only", "--diff-filter=U")
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// generatedOnly tells whether every file of files, as the repository names them, is a generated one: in the project's
// folder prefix, matching one of its globs.
func generatedOnly(files []string, prefix string, globs []string) bool {
	for _, f := range files {
		rel, ok := strings.CutPrefix(f, prefix)
		if !ok || !slices.ContainsFunc(globs, func(g string) bool { return matchGlob(g, rel) }) {
			return false
		}
	}
	return len(files) > 0
}

// matchGlob tells whether name, a path with slashes, matches pattern: "**" stands for any number of folders, other
// parts are path.Match patterns of one name.
func matchGlob(pattern, name string) bool {
	return matchParts(strings.Split(path.Clean(pattern), "/"), strings.Split(name, "/"))
}

func matchParts(pattern, name []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchParts(pattern[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pattern[0], name[0]); !ok {
			return false
		}
		pattern, name = pattern[1:], name[1:]
	}
	return len(name) == 0
}

// checkoutOf is the folder of the checkout that has branch checked out, "" when none has: the person's, or another
// worktree.
func checkoutOf(ctx context.Context, repo, branch string) (string, error) {
	out, err := git(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	dir := ""
	for line := range strings.Lines(out) {
		line = strings.TrimSpace(line)
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			dir = p
		}
		if line == "branch refs/heads/"+branch {
			return filepath.FromSlash(dir), nil
		}
	}
	return "", nil
}

// command runs the command line, its words split on spaces, in dir, under the gate name taken for the task taskID.
func (h *Harness) command(ctx context.Context, name, taskID, line, dir string) (string, int, error) {
	args := strings.Fields(line)
	if len(args) == 0 {
		return "", -1, errors.New("no command")
	}
	if h.gates != nil {
		waits, _ := ctx.Value(gateWaitKey{}).(func(why string))
		var waiting func(string)
		if waits != nil {
			waiting = func(why string) { waits("gate " + name + ": " + why) }
		}
		give, err := h.gates(ctx, name, taskID, line, dir, waiting)
		if err != nil {
			return "", -1, fmt.Errorf("gate %s: %w", name, err)
		}
		defer give()
		if waits != nil {
			waits("") // Taken: it runs.
		}
	}
	run := h.commands
	if run == nil {
		run = runCommand
	}
	return run(ctx, dir, args)
}

// gateWaitKey is the context key of a function told why a command waits for its gate ("gate install: held by …"), and
// "" once it is taken: Install says it to the window.
type gateWaitKey struct{}

// runCommand runs args in dir, and returns the end of what it wrote and its exit code.
func runCommand(ctx context.Context, dir string, args []string) (string, int, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	out := &tailBuffer{max: 16 << 10}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return out.String(), exit.ExitCode(), nil
	case err != nil:
		return out.String(), -1, err
	}
	return out.String(), 0, nil
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	bytes.Buffer
	max int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	n, _ := b.Buffer.Write(p)
	if over := b.Len() - b.max; over > 0 {
		b.Next(over)
	}
	return n, nil
}

// tail is the last lines of a command's output, for a reason: ": " and them, or "" for none.
func tail(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	lines = lines[max(0, len(lines)-10):]
	s := strings.TrimSpace(strings.Join(lines, "\n"))
	if s == "" {
		return ""
	}
	return ":\n" + s[max(0, len(s)-2000):]
}

// tellLead types a line in the terminal of the lead of wishID.
func (h *Harness) tellLead(ctx context.Context, wishID, line string) {
	h.mu.Lock()
	tell := h.tell
	h.mu.Unlock()
	if tell == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := tell(ctx, wishID, line); err != nil && !errors.Is(err, plan.ErrNoLead) {
		log.Printf("djinn: tell lead %s: %v", wishID, err)
	}
}

// askMove records q as a question for a move only the developer can make, journaled and notified.
func (h *Harness) askMove(ctx context.Context, q *planv1.Question) error {
	ctx = context.WithoutCancel(ctx)
	q.Id = store.NewID()
	q.CreateTime = timestamppb.New(h.now())
	q.Move = true
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actorHarness, methodIntegrate, q); err != nil {
			return err
		}
		return plan.Ask(ctx, tx, q)
	})
	if err != nil {
		log.Printf("djinn: move: ask: %v", err)
		return err
	}
	h.notify()
	return nil
}

// isWishSettled tells whether every task in wishID is settled green with no task running or integrating.
func (h *Harness) isWishSettled(ctx context.Context, wishID string) bool {
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wishID})
	if err != nil || len(tasks) == 0 {
		return false
	}
	for _, t := range tasks {
		if h.running(t.GetId()) {
			return false
		}
		st := t.GetIntegration().GetState()
		if st == planv1.IntegrationState_INTEGRATION_STATE_PENDING ||
			st == planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING ||
			st == planv1.IntegrationState_INTEGRATION_STATE_RED ||
			st == planv1.IntegrationState_INTEGRATION_STATE_CONFLICT {
			return false
		}
	}
	return true
}

// draftPR drafts a title and markdown body for a pull request from branch to ref in project.
func (h *Harness) draftPR(ctx context.Context, wish *planv1.Wish, project *planv1.Project, ref, branch string) (string, string) {
	repo := project.GetDirectory()
	title := wish.GetTitle()
	if title == "" {
		title = "Merge " + branch
	}
	var body strings.Builder
	if title != "" {
		body.WriteString("## Summary\n\n")
		body.WriteString(title)
		body.WriteString("\n\n")
	}
	logOut, err := git(ctx, repo, "log", "--no-merges", "--format=- %s", ref+"..refs/heads/"+branch)
	if err == nil && strings.TrimSpace(logOut) != "" {
		body.WriteString("### Commits\n\n")
		body.WriteString(strings.TrimSpace(logOut))
		body.WriteString("\n\n")
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err == nil {
		var azimas []string
		for _, t := range plan.WithAzimas(tasks) {
			if plan.IsAzima(t) {
				azimas = append(azimas, fmt.Sprintf("- **%s**: %s", t.GetCode(), t.GetTitle()))
			}
		}
		if len(azimas) > 0 {
			body.WriteString("### Azimas\n\n")
			body.WriteString(strings.Join(azimas, "\n"))
			body.WriteString("\n")
		}
	}
	return title, strings.TrimSpace(body.String())
}

// handleMergeMoves inspects the commits merged from oldSha to sha and the workers' final notes for moves only
// the developer can make: a tag created locally, release files changed, a needs: box added, or the integration
// branch worth a pull request. It raises a question for any move the worker did not ask itself, and returns
// the names of all moves detected.
func (h *Harness) handleMergeMoves(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, branch, oldSha, sha string, batch []*planv1.Task,
) []string {
	if sha == "" {
		return nil
	}
	repo := project.GetDirectory()
	existing, _ := store.List[*planv1.Question](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	hasQuestion := func(match func(q *planv1.Question) bool) bool {
		for _, q := range existing {
			if q.GetMove() && match(q) {
				return true
			}
		}
		return false
	}

	var moves []string

	// 1. Tag created locally
	var tags []string
	tagArgs := []string{"tag", "--merged", sha}
	if oldSha != "" && oldSha != sha {
		tagArgs = append(tagArgs, "--no-merged", oldSha)
	}
	if out, err := git(ctx, repo, tagArgs...); err == nil && strings.TrimSpace(out) != "" {
		for _, t := range strings.Fields(out) {
			if t != "" && !slices.Contains(tags, t) {
				tags = append(tags, t)
			}
		}
	}
	if out, err := git(ctx, repo, "tag", "--points-at", sha); err == nil && strings.TrimSpace(out) != "" {
		for _, t := range strings.Fields(out) {
			if t != "" && !slices.Contains(tags, t) {
				tags = append(tags, t)
			}
		}
	}
	for _, tag := range tags {
		moveName := tagMoveName(tag)
		if !slices.Contains(moves, moveName) {
			moves = append(moves, moveName)
			asked := hasQuestion(func(q *planv1.Question) bool {
				return strings.Contains(q.GetText(), tag)
			})
			if !asked {
				remote, _ := pushTarget(ctx, repo, branch)
				if remote == "" {
					remote = "origin"
				}
				q := tagMoveQuestion(wish.GetId(), tag, remote)
				if err := h.askMove(ctx, q); err == nil {
					existing = append(existing, q)
				}
			}
		}
	}

	// 2. Release files changed
	var files []string
	if oldSha != "" && oldSha != sha {
		if out, err := git(ctx, repo, "diff", "--name-only", oldSha, sha); err == nil {
			files = strings.Fields(out)
		}
	} else {
		if out, err := git(ctx, repo, "show", "--name-only", "--format=", sha); err == nil {
			files = strings.Fields(out)
		}
	}
	hasRelease := false
	for _, f := range files {
		if isReleaseFile(f) {
			hasRelease = true
			break
		}
	}
	if hasRelease {
		if !slices.Contains(moves, releaseMoveName) {
			moves = append(moves, releaseMoveName)
			asked := hasQuestion(func(q *planv1.Question) bool {
				return strings.Contains(strings.ToLower(q.GetText()), "release")
			})
			if !asked {
				q := releaseMoveQuestion(wish.GetId(), sha)
				if err := h.askMove(ctx, q); err == nil {
					existing = append(existing, q)
				}
			}
		}
	}

	// 3. needs: box added
	var diffOut string
	if oldSha != "" && oldSha != sha {
		diffOut, _ = git(ctx, repo, "diff", "-U0", oldSha, sha, "--", plan.PlanDir+"/*.md")
	} else {
		diffOut, _ = git(ctx, repo, "show", "-U0", "--format=", sha, "--", plan.PlanDir+"/*.md")
	}
	prefix, _ := git(ctx, repo, "rev-parse", "--show-prefix")
	prefix = strings.TrimSpace(prefix)
	curFile := ""
	for _, line := range strings.Split(diffOut, "\n") {
		if strings.HasPrefix(line, "+++ ") {
			curFile = strings.TrimPrefix(line, "+++ ")
			curFile = strings.TrimPrefix(curFile, "b/")
			if prefix != "" {
				curFile = strings.TrimPrefix(curFile, prefix)
			}
			continue
		}
		if !isPlanFile(curFile) {
			continue
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			added := strings.TrimPrefix(line, "+")
			box, machine, ok := plan.ReadTaskItemNeeds(added)
			if ok {
				moveName := needsMoveName(box, machine)
				if !slices.Contains(moves, moveName) {
					moves = append(moves, moveName)
					asked := hasQuestion(func(q *planv1.Question) bool {
						tl := strings.ToLower(q.GetText())
						return strings.Contains(tl, strings.ToLower(machine)) && (box == "" || strings.Contains(tl, strings.ToLower(box)))
					})
					if !asked {
						q := needsMoveQuestion(wish.GetId(), box, machine)
						if err := h.askMove(ctx, q); err == nil {
							existing = append(existing, q)
						}
					}
				}
			}
		}
	}

	// 4. Worth a pull request to main
	main, ref, ok := h.isPRWorthy(ctx, wish, project, branch, sha)
	if ok {
		allQuestions, _ := store.List[*planv1.Question](ctx, h.store, nil)
		wishes, _ := store.List[*planv1.Wish](ctx, h.store, nil)
		wishesByID := make(map[string]*planv1.Wish, len(wishes))
		for _, w := range wishes {
			wishesByID[w.GetId()] = w
		}
		if !h.hasPRQuestion(ctx, project.GetId(), allQuestions, wishesByID) {
			moveName := prMoveName(main)
			if !slices.Contains(moves, moveName) {
				moves = append(moves, moveName)
				title, body := h.draftPR(ctx, wish, project, ref, branch)
				q := prMoveQuestion(wish.GetId(), main, title, body)
				if err := h.askMove(ctx, q); err == nil {
					existing = append(existing, q)
				}
			}
		}
	}

	// 5. Worker asked moves: any moves the worker explicitly asked that were not already detected.
	for _, t := range batch {
		for _, q := range existing {
			if q.GetTaskId() == t.GetId() && q.GetMove() {
				already := false
				for _, tag := range tags {
					if strings.Contains(q.GetText(), tag) {
						already = true
						break
					}
				}
				if hasRelease && strings.Contains(strings.ToLower(q.GetText()), "release") {
					already = true
				}
				if strings.Contains(strings.ToLower(q.GetText()), "pull request") {
					already = true
				}
				if already {
					continue
				}
				name := strings.TrimSpace(strings.TrimSuffix(q.GetText(), "?"))
				found := false
				for _, m := range moves {
					if strings.EqualFold(m, name) {
						found = true
						break
					}
				}
				if !found && name != "" {
					moves = append(moves, name)
				}
			}
		}
	}

	return moves
}

// computedMovesPass checks for moves Djinn computes by itself across active wishes and projects:
// uninstalled builds, PRs to main, azimas awaiting proof, and release workflows.
func (h *Harness) computedMovesPass(ctx context.Context) {
	wishes, err := store.List[*planv1.Wish](ctx, h.store, nil)
	if err != nil {
		return
	}
	allQuestions, err := store.List[*planv1.Question](ctx, h.store, nil)
	if err != nil {
		return
	}
	wishesByID := make(map[string]*planv1.Wish, len(wishes))
	for _, w := range wishes {
		wishesByID[w.GetId()] = w
	}
	for _, wish := range wishes {
		if ctx.Err() != nil {
			return
		}

		// 1. Azima awaiting proof
		tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
		if err == nil {
			for _, t := range plan.WithAzimas(tasks) {
				if plan.IsAzima(t) {
					if t.GetAzima().GetState() == planv1.AzimaState_AZIMA_STATE_AWAITING_PROOF {
						asked := false
						for _, q := range allQuestions {
							if q.GetMove() && q.GetTaskId() == t.GetId() {
								asked = true
								break
							}
						}
						if !asked {
							needs := t.GetProofNeeds()
							if len(needs) == 0 {
								continue
							}
							needsWords := plan.NeedsWords(needs)
							var boxes []string
							for _, n := range needs {
								boxes = append(boxes, fmt.Sprintf("- [ ] %s (needs: %s)", n.GetBox(), n.GetNeeds()))
							}
							q := azimaProofMoveQuestion(wish.GetId(), t.GetId(), t.GetCode(), needsWords, boxes)
							if err := h.askMove(ctx, q); err == nil {
								allQuestions = append(allQuestions, q)
							}
						}
					} else {
						for _, q := range allQuestions {
							if q.GetMove() && q.GetTaskId() == t.GetId() && q.GetAnswer() == nil {
								_ = h.closeMove(ctx, q, planv1.Choice_CHOICE_A, "proof settled")
							}
						}
					}
				}
			}
		}

		// 2. Projects checks (uninstalled build and PR to main)
		for _, projectID := range wish.GetProjectIds() {
			project, err := store.Get[*planv1.Project](ctx, h.store, projectID)
			if err != nil {
				continue
			}
			repo := project.GetDirectory()
			branch, _ := h.integrationBranch(ctx, wish, project)
			if branch == "" {
				continue
			}
			settings, err := plan.LoadSettings(h.home, project)
			if err != nil {
				continue
			}

			// (a) Uninstalled build
			if settings.Install != "" {
				sha, _ := git(ctx, repo, "rev-parse", "refs/heads/"+branch)
				inst := h.installedSha(ctx, projectID)
				if sha != "" && inst == sha {
					h.closeInstallQuestions(ctx, projectID, sha)
				} else if sha != "" && inst != sha {
					s := short8(sha)
					// Supersede open install questions for older shas in this project
					for _, q := range allQuestions {
						if q.GetMove() && q.GetAnswer() == nil && strings.Contains(q.GetText(), "Install and restart") && !strings.Contains(q.GetText(), s) && h.isQuestionForProject(ctx, q, projectID, wishesByID) {
							_ = h.closeMove(ctx, q, planv1.Choice_CHOICE_B, fmt.Sprintf("superseded by build %s", s))
						}
					}
					tsStr, err := git(ctx, repo, "log", "-1", "--format=%ct", sha)
					if err == nil {
						ts, _ := strconv.ParseInt(strings.TrimSpace(tsStr), 10, 64)
						commitTime := time.Unix(ts, 0)
						delay := cmp.Or(h.uninstalledDelay, defaultUninstalledDelay)
						if h.now().Sub(commitTime) >= delay {
							asked := false
							for _, q := range allQuestions {
								if q.GetMove() && strings.Contains(q.GetText(), "Install and restart") && strings.Contains(q.GetText(), s) && h.isQuestionForProject(ctx, q, projectID, wishesByID) {
									asked = true
									break
								}
							}
							if !asked {
								q := installMoveQuestion(wish.GetId(), sha, branch, delay)
								if err := h.askMove(ctx, q); err == nil {
									allQuestions = append(allQuestions, q)
								}
							}
						}
					}
				}
			}

			// (b) PR to main: wish settled, pushed to remote, push checks green, ahead of remote default branch
			main, ref, ok := h.isPRWorthy(ctx, wish, project, branch, "")
			if ok {
				if !h.hasPRQuestion(ctx, projectID, allQuestions, wishesByID) {
					title, body := h.draftPR(ctx, wish, project, ref, branch)
					q := prMoveQuestion(wish.GetId(), main, title, body)
					if err := h.askMove(ctx, q); err == nil {
						allQuestions = append(allQuestions, q)
					}
				}
			} else {
				// If branch caught up to main (count == 0), close any open PR questions
				main, ref, err := mainRef(ctx, repo, settings.MainBranch)
				if err == nil && !strings.EqualFold(main, branch) {
					countStr, err := git(ctx, repo, "rev-list", "--count", ref+"..refs/heads/"+branch)
					count, _ := strconv.Atoi(strings.TrimSpace(countStr))
					if err == nil && count == 0 {
						for _, q := range allQuestions {
							if q.GetMove() && q.GetAnswer() == nil && strings.Contains(strings.ToLower(q.GetText()), "pull request") && h.isQuestionForProject(ctx, q, projectID, wishesByID) {
								_ = h.closeMove(ctx, q, planv1.Choice_CHOICE_A, "merged to "+main)
							}
						}
					}
				}
			}
		}
	}
}

// targetBranch determines the branch to merge the batch into:
// When the push strategy is azima, each azima gets its own integration branch (djinn/<azima code>-<slug of title>).
// A task with no azima goes to the wish's own integration branch.
// In per-wish mode, it returns the wish's integration branch as today.
func (h *Harness) targetBranch(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, batch []*planv1.Task,
) (branch, why string, ok bool) {
	strat, _ := plan.ResolveWishPushStrategy(wish, settings)
	if strat != planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
		b, why := h.integrationBranch(ctx, wish, project)
		return b, why, true
	}
	azima, err := h.taskAzima(ctx, batch[0])
	if err != nil {
		return "", "find azima: " + err.Error(), false
	}
	if azima == nil {
		b, why := h.integrationBranch(ctx, wish, project)
		return b, why, true
	}
	branch = azimaBranchName(azima)
	repo := project.GetDirectory()
	branchSha := commitOf(ctx, repo, "refs/heads/"+branch)
	if branchSha == "" {
		startSha, err := h.startShaForAzima(ctx, repo, settings, azima)
		if err != nil {
			return "", "find start commit for " + branch + ": " + err.Error(), false
		}
		if _, err := git(ctx, repo, "branch", branch, startSha); err != nil {
			return "", "create branch " + branch + ": " + err.Error(), false
		}
	} else {
		needs, mainSha, err := h.needsFollowMain(ctx, repo, settings, azima, branch, branchSha)
		if err != nil {
			return "", "check if " + branch + " follows main: " + err.Error(), false
		}
		if needs {
			if err := h.followMain(ctx, wish, project, settings, azima, branch, branchSha, mainSha, batch); err != nil {
				return "", "", false
			}
		}
	}
	return branch, "", true
}

// azimaBranchName is the integration branch name of an azima: djinn/<code-slug> or djinn/<code>.
func azimaBranchName(a *planv1.Task) string {
	s := slug(a.GetTitle(), 40)
	if s != "" {
		return fmt.Sprintf("djinn/%s-%s", a.GetCode(), s)
	}
	return fmt.Sprintf("djinn/%s", a.GetCode())
}

// taskAzima finds the azima task that t is part of, walking up the part_of chain if needed.
func (h *Harness) taskAzima(ctx context.Context, t *planv1.Task) (*planv1.Task, error) {
	if t == nil {
		return nil, nil
	}
	if plan.IsAzima(t) {
		return t, nil
	}
	curr := t
	visited := make(map[string]bool)
	for curr.GetPartOf() != "" && !visited[curr.GetId()] {
		visited[curr.GetId()] = true
		parent, err := store.Get[*planv1.Task](ctx, h.store, curr.GetPartOf())
		if err != nil {
			return nil, err
		}
		if plan.IsAzima(parent) {
			return parent, nil
		}
		curr = parent
	}
	return nil, nil
}

// azimaTasks returns all non-azima tasks that belong to azimaID under wishID.
func (h *Harness) azimaTasks(ctx context.Context, wishID, azimaID string) ([]*planv1.Task, error) {
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wishID})
	if err != nil {
		return nil, err
	}
	taskMap := make(map[string]*planv1.Task, len(tasks))
	for _, t := range tasks {
		taskMap[t.GetId()] = t
	}
	isUnder := func(t *planv1.Task) bool {
		curr := t
		visited := make(map[string]bool)
		for curr.GetPartOf() != "" && !visited[curr.GetId()] {
			visited[curr.GetId()] = true
			if curr.GetPartOf() == azimaID {
				return true
			}
			curr = taskMap[curr.GetPartOf()]
			if curr == nil {
				break
			}
		}
		return false
	}
	var out []*planv1.Task
	for _, t := range tasks {
		if !plan.IsAzima(t) && isUnder(t) {
			out = append(out, t)
		}
	}
	return out, nil
}

// wishAzimas returns all azima tasks in wish.
func (h *Harness) wishAzimas(ctx context.Context, wish *planv1.Wish) []*planv1.Task {
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		return nil
	}
	var azimas []*planv1.Task
	for _, t := range tasks {
		if plan.IsAzima(t) {
			azimas = append(azimas, t)
		}
	}
	return azimas
}

// dependencyAzimas finds all unique azimas that azima (or its work tasks) depends on in the same wish.
func (h *Harness) dependencyAzimas(ctx context.Context, azima *planv1.Task) []*planv1.Task {
	if azima == nil {
		return nil
	}
	wishID := azima.GetWishId()
	allTasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wishID})
	if err != nil {
		return nil
	}
	taskMap := make(map[string]*planv1.Task, len(allTasks))
	for _, t := range allTasks {
		taskMap[t.GetId()] = t
	}

	findAzima := func(taskID string) *planv1.Task {
		curr := taskMap[taskID]
		visited := make(map[string]bool)
		for curr != nil && !visited[curr.GetId()] {
			visited[curr.GetId()] = true
			if plan.IsAzima(curr) {
				return curr
			}
			if curr.GetPartOf() == "" {
				break
			}
			curr = taskMap[curr.GetPartOf()]
		}
		return nil
	}

	depAzimaMap := make(map[string]*planv1.Task)
	for _, depID := range azima.GetDependsOn() {
		if a := findAzima(depID); a != nil && a.GetId() != azima.GetId() {
			depAzimaMap[a.GetId()] = a
		}
	}
	for _, t := range allTasks {
		if a := findAzima(t.GetId()); a != nil && a.GetId() == azima.GetId() && !plan.IsAzima(t) {
			for _, depID := range t.GetDependsOn() {
				if da := findAzima(depID); da != nil && da.GetId() != azima.GetId() {
					depAzimaMap[da.GetId()] = da
				}
			}
		}
	}
	var res []*planv1.Task
	for _, da := range depAzimaMap {
		res = append(res, da)
	}
	slices.SortStableFunc(res, func(a, b *planv1.Task) int {
		return strings.Compare(a.GetCode(), b.GetCode())
	})
	return res
}

// azimaBranchesOf lists all existing azima branches for wish in project's repo.
func (h *Harness) azimaBranchesOf(ctx context.Context, wish *planv1.Wish, project *planv1.Project) []string {
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		return nil
	}
	repo := project.GetDirectory()
	var branches []string
	for _, t := range tasks {
		if plan.IsAzima(t) {
			b := azimaBranchName(t)
			if _, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
				branches = append(branches, b)
			}
		}
	}
	return branches
}

// resolveMainSha finds the project's main branch name and sha.
func resolveMainSha(ctx context.Context, repo string, settings plan.Settings) (name, sha string, err error) {
	mainName := settings.MainBranch
	if mainName == "" {
		if m, _, err := mainRef(ctx, repo, ""); err == nil {
			mainName = m
		} else {
			mainName = "main"
		}
	}
	localSha := commitOf(ctx, repo, "refs/heads/"+mainName)
	if localSha == "" && mainName != "master" {
		if mSha := commitOf(ctx, repo, "refs/heads/master"); mSha != "" {
			localSha = mSha
			mainName = "master"
		}
	}
	var remoteSha string
	remote, target := pushTarget(ctx, repo, mainName)
	if remote != "" && target != "" {
		remoteSha = commitOf(ctx, repo, "refs/remotes/"+remote+"/"+target)
	}
	switch {
	case localSha != "" && remoteSha != "":
		if isAncestor(ctx, repo, localSha, remoteSha) {
			return mainName, remoteSha, nil
		}
		return mainName, localSha, nil
	case localSha != "":
		return mainName, localSha, nil
	case remoteSha != "":
		return mainName, remoteSha, nil
	}
	if headSha := commitOf(ctx, repo, "HEAD"); headSha != "" {
		return "HEAD", headSha, nil
	}
	return "", "", errors.New("the project has no main branch: name it with main_branch in its settings")
}

// startShaForAzima determines the commit an azima's branch starts from:
// It starts from an unmerged dependency's branch; or once dependencies are merged into main, from main.
func (h *Harness) startShaForAzima(ctx context.Context, repo string, settings plan.Settings, azima *planv1.Task) (string, error) {
	_, mainSha, err := resolveMainSha(ctx, repo, settings)
	if err != nil {
		return "", err
	}
	deps := h.dependencyAzimas(ctx, azima)
	var unmerged []string
	for _, dep := range deps {
		depBranch := azimaBranchName(dep)
		if depSha := commitOf(ctx, repo, "refs/heads/"+depBranch); depSha != "" {
			if !isAncestor(ctx, repo, depSha, mainSha) {
				unmerged = append(unmerged, depSha)
			}
		}
	}
	if len(unmerged) == 0 {
		return mainSha, nil
	}
	leaf := unmerged[0]
	for _, cand := range unmerged {
		isAnc := false
		for _, other := range unmerged {
			if cand != other && isAncestor(ctx, repo, cand, other) {
				isAnc = true
				break
			}
		}
		if !isAnc {
			leaf = cand
			break
		}
	}
	return leaf, nil
}

// needsFollowMain tells whether branch needs to follow main (base azima merged into main).
func (h *Harness) needsFollowMain(ctx context.Context, repo string, settings plan.Settings, azima *planv1.Task, branch, branchSha string) (bool, string, error) {
	_, mainSha, err := resolveMainSha(ctx, repo, settings)
	if err != nil {
		return false, "", err
	}
	if mainSha == "" {
		return false, "", nil
	}
	if isAncestor(ctx, repo, mainSha, branchSha) {
		return false, mainSha, nil
	}
	deps := h.dependencyAzimas(ctx, azima)
	for _, dep := range deps {
		depBranch := azimaBranchName(dep)
		depSha := commitOf(ctx, repo, "refs/heads/"+depBranch)
		if depSha != "" && isAncestor(ctx, repo, depSha, mainSha) && isAncestor(ctx, repo, depSha, branchSha) {
			return true, mainSha, nil
		}
		depTasks, _ := h.azimaTasks(ctx, azima.GetWishId(), dep.GetId())
		for _, dt := range depTasks {
			if tSha := dt.GetIntegration().GetSha(); tSha != "" {
				if isAncestor(ctx, repo, tSha, mainSha) && isAncestor(ctx, repo, tSha, branchSha) {
					return true, mainSha, nil
				}
			}
		}
	}
	return false, mainSha, nil
}

// followMain brings main into branch when base azima was merged into main:
// rebases if never pushed; merges main into it if pushed; on conflict raises a question or spawns correction.
func (h *Harness) followMain(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings,
	azima *planv1.Task, branch, branchSha, mainSha string, batch []*planv1.Task,
) error {
	repo := project.GetDirectory()
	shownMain, _, _ := mainRef(ctx, repo, settings.MainBranch)
	if shownMain == "" {
		shownMain = "main"
	}
	pushed := h.isBranchPushed(ctx, wish, project, branch)
	wt := integrationDir(h.home, project.GetId(), wish.GetId())
	dir, prefix, err := integrationWorktree(ctx, repo, wt, branchSha)
	if err != nil {
		return fmt.Errorf("prepare integration worktree: %w", err)
	}

	if !pushed {
		// Never pushed: rebase onto main
		if _, err := git(ctx, wt, "rebase", "--rebase-merges", mainSha); err != nil {
			conflicts := conflicted(ctx, wt)
			_, _ = git(ctx, wt, "rebase", "--abort")
			why := fmt.Sprintf("%s conflicts with %s in %s", branch, shownMain, strings.Join(conflicts, ", "))
			f := &planv1.IntegrationFailure{
				State: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, Base: branchSha,
				MergeBranch: mainSha, Files: conflicts, Reason: why, TaskIds: ids(batch),
			}
			in := &planv1.TaskIntegration{
				State: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, Reason: why, Failure: f, Branch: branch,
			}
			h.failed(ctx, wish, project, settings, batch, in, "integration: conflict: "+why+"; "+branch+" stays as it was")
			return errors.New(why)
		}
		newSha, err := git(ctx, wt, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		command, why, out, stopped := h.commitChecks(ctx, wish, project, settings, batch[0].GetId(), newSha, wt, dir, func(*planv1.ProjectCheck) {})
		switch {
		case stopped != "":
			return errors.New(stopped)
		case why != "":
			f := &planv1.IntegrationFailure{
				State: planv1.IntegrationState_INTEGRATION_STATE_RED, Base: newSha,
				Command: command, Output: tail(out), Reason: why, TaskIds: ids(batch),
			}
			in := &planv1.TaskIntegration{
				State: planv1.IntegrationState_INTEGRATION_STATE_RED, Reason: why, Failure: f, Branch: branch,
			}
			h.failed(ctx, wish, project, settings, batch, in, "integration: red: "+why+"; "+branch+" stays as it was")
			return errors.New(why)
		}
		holder, why, _ := moveBranch(ctx, repo, branch, branchSha, newSha, "djinn: rebase on "+shownMain)
		if why != "" {
			return errors.New(why)
		}
		_ = holder
		h.updateRebasedTasks(ctx, repo, batch, branch, newSha)
		azTasks, _ := h.azimaTasks(ctx, wish.GetId(), azima.GetId())
		h.updateRebasedTasks(ctx, repo, azTasks, branch, newSha)
		return nil
	}

	// Already pushed: merge main into branch without force
	msg := fmt.Sprintf("Merge branch '%s' into %s", shownMain, branch)
	if _, err := git(ctx, wt, "merge", "--no-ff", "--no-edit", "--quiet", "-m", msg, mainSha); err != nil {
		conflicts := conflicted(ctx, wt)
		var why string
		switch {
		case len(conflicts) == 0:
			_, _ = git(ctx, wt, "merge", "--abort")
			why = fmt.Sprintf("merge %s: %v", shownMain, err)
		case !generatedOnly(conflicts, prefix, settings.Generated) || settings.Generate == "":
			why = fmt.Sprintf("%s conflicts with %s in %s", shownMain, branch, strings.Join(conflicts, ", "))
		default:
			why = h.settleGenerated(ctx, shownMain, batch[0].GetId(), wt, dir, settings.Generate, conflicts, func() string {
				failed, _, _ := h.setUp(ctx, project, settings, wish.GetId(), batch[0].GetId(), wt, dir)
				return failed
			})
		}
		if why != "" {
			_, _ = git(ctx, wt, "merge", "--abort")
			f := &planv1.IntegrationFailure{
				State: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, Base: branchSha,
				MergeBranch: mainSha, Files: conflicts, Reason: why, TaskIds: ids(batch),
			}
			in := &planv1.TaskIntegration{
				State: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, Reason: why, Failure: f, Branch: branch,
			}
			h.failed(ctx, wish, project, settings, batch, in, "integration: conflict: "+why+"; "+branch+" stays as it was")
			return errors.New(why)
		}
	}
	newSha, err := git(ctx, wt, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	command, why, out, stopped := h.commitChecks(ctx, wish, project, settings, batch[0].GetId(), newSha, wt, dir, func(*planv1.ProjectCheck) {})
	switch {
	case stopped != "":
		return errors.New(stopped)
	case why != "":
		f := &planv1.IntegrationFailure{
			State: planv1.IntegrationState_INTEGRATION_STATE_RED, Base: newSha,
			Command: command, Output: tail(out), Reason: why, TaskIds: ids(batch),
		}
		in := &planv1.TaskIntegration{
			State: planv1.IntegrationState_INTEGRATION_STATE_RED, Reason: why, Failure: f, Branch: branch,
		}
		h.failed(ctx, wish, project, settings, batch, in, "integration: red: "+why+"; "+branch+" stays as it was")
		return errors.New(why)
	}
	holder, why, _ := moveBranch(ctx, repo, branch, branchSha, newSha, "djinn: merge "+shownMain+" into "+branch)
	if why != "" {
		return errors.New(why)
	}
	_ = holder
	return nil
}

// updateRebasedTasks updates the recorded integration sha for committed tasks of azima after rebase.
func (h *Harness) updateRebasedTasks(ctx context.Context, repo string, tasks []*planv1.Task, branch, newSha string) {
	ctx = context.WithoutCancel(ctx)
	for _, t := range tasks {
		if t.GetIntegration().GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
			continue
		}
		sha := newSha
		if out, err := git(ctx, repo, "log", "-n", "1", "--format=%H", "-i", "--grep="+t.GetBranch(), newSha); err == nil && strings.TrimSpace(out) != "" {
			sha = strings.TrimSpace(out)
		} else if out, err := git(ctx, repo, "log", "-n", "1", "--format=%H", "-i", "--grep="+t.GetCode(), newSha); err == nil && strings.TrimSpace(out) != "" {
			sha = strings.TrimSpace(out)
		}
		_ = h.store.Tx(ctx, func(tx *store.Tx) error {
			cur, err := store.Get[*planv1.Task](ctx, tx, t.GetId())
			if err != nil {
				return err
			}
			if cur.Integration == nil {
				return nil
			}
			cur.Integration.Sha = sha
			cur.Integration.Branch = branch
			cur.Integration.UpdateTime = timestamppb.New(h.now())
			if err := tx.Journal(actorHarness, methodIntegrate, cur); err != nil {
				return err
			}
			return tx.Put(cur)
		})
	}
}

// followStackedBranches checks active wishes under azima push strategy and rebases/merges main into branches when base was merged into main.
func (h *Harness) followStackedBranches(ctx context.Context, tasks []*planv1.Task) {
	wishes, err := store.List[*planv1.Wish](ctx, h.store, nil)
	if err != nil {
		return
	}
	for _, wish := range wishes {
		if s := wish.GetState(); s == planv1.WishState_WISH_STATE_PAUSED || s == planv1.WishState_WISH_STATE_GRANTED {
			continue
		}
		for _, projectID := range wish.GetProjectIds() {
			if ctx.Err() != nil {
				return
			}
			project, err := store.Get[*planv1.Project](ctx, h.store, projectID)
			if err != nil || !project.GetGit() {
				continue
			}
			settings, err := plan.LoadSettings(h.home, project)
			if err != nil || !settings.Integrates() {
				continue
			}
			strat, _ := plan.ResolveWishPushStrategy(wish, settings)
			if strat != planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
				continue
			}
			repo := project.GetDirectory()
			for _, azima := range h.wishAzimas(ctx, wish) {
				branch := azimaBranchName(azima)
				branchSha := commitOf(ctx, repo, "refs/heads/"+branch)
				if branchSha == "" {
					continue
				}
				needs, _, err := h.needsFollowMain(ctx, repo, settings, azima, branch, branchSha)
				if err != nil || !needs {
					continue
				}
				h.integrateMu.Lock()
				branchSha = commitOf(ctx, repo, "refs/heads/"+branch)
				if branchSha != "" {
					if n, mSha, _ := h.needsFollowMain(ctx, repo, settings, azima, branch, branchSha); n {
						azTasks, _ := h.azimaTasks(ctx, wish.GetId(), azima.GetId())
						batch := committedTasks(azTasks)
						if len(batch) == 0 {
							batch = azTasks
						}
						if len(batch) == 0 {
							batch = []*planv1.Task{azima}
						}
						_ = h.followMain(ctx, wish, project, settings, azima, branch, branchSha, mSha, batch)
					}
				}
				h.integrateMu.Unlock()
			}
		}
	}
}

// committedTasks returns the tasks among tasks whose work is committed.
func committedTasks(tasks []*planv1.Task) []*planv1.Task {
	var out []*planv1.Task
	for _, t := range tasks {
		if t.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
			out = append(out, t)
		}
	}
	return out
}

// workerStartBranch determines the branch or ref a worker starts from.
func (h *Harness) workerStartBranch(ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, t *planv1.Task) string {
	strat, _ := plan.ResolveWishPushStrategy(wish, settings)
	if strat != planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
		return plan.IntegrationBranchOf(wish, project.GetId())
	}
	azima, err := h.taskAzima(ctx, t)
	if err != nil || azima == nil {
		return plan.IntegrationBranchOf(wish, project.GetId())
	}
	repo := project.GetDirectory()
	azimaBranch := azimaBranchName(azima)
	if _, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+azimaBranch); err == nil {
		return azimaBranch
	}
	deps := h.dependencyAzimas(ctx, azima)
	mainName, mainSha, _ := resolveMainSha(ctx, repo, settings)
	for _, dep := range deps {
		depBranch := azimaBranchName(dep)
		if depSha := commitOf(ctx, repo, "refs/heads/"+depBranch); depSha != "" {
			if mainSha == "" || !isAncestor(ctx, repo, depSha, mainSha) {
				return depBranch
			}
		}
	}
	if mainName != "" {
		return mainName
	}
	return plan.IntegrationBranchOf(wish, project.GetId())
}
