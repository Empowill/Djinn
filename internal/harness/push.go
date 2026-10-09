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

// Pushing (T30): each task's work is committed into its wish's integration branch as soon as it ends; pushing that
// branch to its remote is the orchestrator's, never an agent's (.agents/ denies git push). Djinn checks it each time
// a task's merge ends: a push is due when an azima ends, its last part committed, or once enough tasks are committed
// since the last push and more than an hour has passed since it. In auto mode, the default, Djinn pushes, through git
// with the person's own credentials, never forcing; in ask mode it asks first. The project's push checks run first:
// red, they hold the push (checks.go). A push the remote refuses is said, and asked about. Once pushed, a project that
// names an install command proposes the build.

// methodPush is how the journal records a push: the request is the push (planv1.IntegrationPush).
const methodPush = "harness/push"

// When a push is due when no azima ends: three tasks committed since the last push, and more than an hour since it.
// A wish sets its own (Wish.push_after_minutes, Wish.push_after_tasks).
const (
	DefaultPushAfter = time.Hour
	DefaultPushTasks = 3
)

// How long a push may take before Djinn gives up on it, and how many commits' titles a push records.
const (
	pushTimeout = 5 * time.Minute
	pushTitles  = 30
)

// The options of the questions Djinn asks about a push, answered by their letter: A pushes.
const (
	pushOption      = "Push it"
	notNowOption    = "Not now: Djinn asks again when the next push is due"
	pushAgainOption = "Push again, without forcing: once you have brought the remote's commits into %s, or allowed the push"
	leavePushOption = "Leave it: Djinn tries again when the next push is due"
)

// pushIfDue pushes the integration branch of the wish wishID in project, or asks to, when a push is due: checked once a
// task's work is committed there.
func (h *Harness) pushIfDue(ctx context.Context, wishID string, project *planv1.Project) {
	wish, err := store.Get[*planv1.Wish](ctx, h.store, wishID)
	if err != nil {
		log.Printf("djinn: push: %v", err)
		return
	}
	state := pushState(wish, project.GetId())
	switch {
	case state.GetQuestionId() != "":
		return // Djinn asked: the answer says when.
	case state.GetApproved():
		return // The person said to push: pushApproved pushes, at the end of this pass.
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wishID})
	if err != nil {
		log.Printf("djinn: push: %v", err)
		return
	}
	since := lastPush(wish, state)
	committed := committedSince(tasks, project.GetId(), since)
	every := time.Duration(wish.GetPushAfterMinutes()) * time.Minute
	why := pushDue(committed, tasks, since, h.now(), cmp.Or(every, DefaultPushAfter), int(cmp.Or(wish.GetPushAfterTasks(), DefaultPushTasks)))
	if why != "" {
		h.push(ctx, wish, project, committed, why, false)
	}
}

// pushApproved pushes the integration branches whose push the person approved, answering a question.
func (h *Harness) pushApproved(ctx context.Context) {
	wishes, err := store.List[*planv1.Wish](ctx, h.store, nil)
	if err != nil {
		log.Printf("djinn: push: %v", err)
		return
	}
	for _, wish := range wishes {
		for _, p := range wish.GetPushes() {
			if !p.GetApproved() || ctx.Err() != nil {
				continue
			}
			project, err1 := store.Get[*planv1.Project](ctx, h.store, p.GetProjectId())
			tasks, err2 := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
			if err := errors.Join(err1, err2); err != nil {
				log.Printf("djinn: push: %v", err)
				continue
			}
			h.push(ctx, wish, project, committedSince(tasks, p.GetProjectId(), lastPush(wish, p)), "you said to push it", true)
		}
	}
}

// pushState is where the push of wish's integration branch stands in the project projectID; nil before anything.
func pushState(wish *planv1.Wish, projectID string) *planv1.WishPush {
	for _, p := range wish.GetPushes() {
		if strings.EqualFold(p.GetProjectId(), projectID) {
			return p
		}
	}
	return nil
}

// lastPush is when wish last pushed its integration branch, as state says: when it was made, before any push.
func lastPush(wish *planv1.Wish, state *planv1.WishPush) time.Time {
	if at := state.GetLast().GetPushTime(); at != nil {
		return at.AsTime()
	}
	return wish.GetCreateTime().AsTime()
}

// committedSince are the tasks of tasks, in the project projectID, whose work was committed after since, in the order
// it was.
func committedSince(tasks []*planv1.Task, projectID string, since time.Time) []*planv1.Task {
	var out []*planv1.Task
	for _, t := range tasks {
		in := t.GetIntegration()
		if t.GetProjectId() == projectID && in.GetState() == planv1.IntegrationState_INTEGRATION_STATE_COMMITTED &&
			in.GetUpdateTime().AsTime().After(since) {
			out = append(out, t)
		}
	}
	slices.SortStableFunc(out, func(a, b *planv1.Task) int {
		return a.GetIntegration().GetUpdateTime().AsTime().Compare(b.GetIntegration().GetUpdateTime().AsTime())
	})
	return out
}

// pushDue says why a push is due, "" when it is not: an azima that a task of committed, committed since the last push,
// is part of has ended; or n tasks are committed since the last push, at since, and more than every has passed since.
// tasks are all the wish's.
func pushDue(committed, tasks []*planv1.Task, since, now time.Time, every time.Duration, n int) string {
	for _, t := range slices.Backward(committed) {
		if a := t.GetPartOf(); a != "" && azimaEnded(a, tasks) {
			return "the azima " + codeOf(a, tasks) + " ends"
		}
	}
	if len(committed) >= n && now.Sub(since) > every {
		return fmt.Sprintf("%d tasks committed, and more than %s since the last push", len(committed), duration(every))
	}
	return ""
}

// azimaEnded tells whether the azima id has ended: every work part of it has finished, done or not, and none waits
// to be committed.
func azimaEnded(id string, tasks []*planv1.Task) bool {
	for _, t := range tasks {
		if t.GetPartOf() != id || plan.IsAzima(t) {
			continue
		}
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_STOPPED:
		default:
			return false
		}
		switch t.GetIntegration().GetState() {
		case planv1.IntegrationState_INTEGRATION_STATE_PENDING, planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING:
			return false
		}
	}
	return true
}

// codeOf is the code of the task id among tasks; id itself when it is not there.
func codeOf(id string, tasks []*planv1.Task) string {
	if i := slices.IndexFunc(tasks, func(t *planv1.Task) bool { return t.GetId() == id }); i >= 0 {
		return tasks[i].GetCode()
	}
	return id
}

// duration says d as a person reads it: "an hour", "90 minutes".
func duration(d time.Duration) string {
	switch {
	case d == time.Hour:
		return "an hour"
	case d%time.Hour == 0:
		return fmt.Sprintf("%d hours", d/time.Hour)
	}
	return fmt.Sprintf("%d minutes", d/time.Minute)
}

// push pushes the integration branch of wish in project to its remote, why saying why it is due, or asks the person
// first in ask mode unless approved. committed are the tasks committed since the last push, whose work it brings:
// each one's events say it. A branch with nothing the remote lacks, or a repository without a remote, pushes nothing.
func (h *Harness) push(ctx context.Context, wish *planv1.Wish, project *planv1.Project, committed []*planv1.Task, why string, approved bool) {
	pushing := ctx // git push stops with djinn up: an approval waits for the next start.
	ctx = context.WithoutCancel(ctx)
	h.pushMu.Lock()
	defer h.pushMu.Unlock()
	branch := plan.IntegrationBranchOf(wish, project.GetId())
	repo := project.GetDirectory()
	remote, target := pushTarget(ctx, repo, branch)
	if branch == "" || remote == "" {
		h.clearApproved(ctx, wish.GetId(), project.GetId(), approved)
		return
	}
	titles, count, ahead, err := unpushed(ctx, repo, branch, remote)
	if err != nil {
		log.Printf("djinn: push %s: %v", branch, err)
		return
	}
	if ahead == 0 {
		h.clearApproved(ctx, wish.GetId(), project.GetId(), approved)
		return
	}
	sha, err := git(ctx, repo, "rev-parse", "refs/heads/"+branch)
	if err != nil {
		log.Printf("djinn: push %s: %v", branch, err)
		return
	}
	state := pushState(wish, project.GetId())
	unchecked := approved && state.GetUnchecked()
	if !unchecked {
		var held, out string
		var stopped bool
		if settings, err := plan.LoadSettings(h.home, project); err != nil {
			held = err.Error()
		} else {
			held, out, stopped = h.pushChecks(pushing, wish, project, settings, branch, sha, committed)
		}
		switch {
		case stopped || pushing.Err() != nil:
			return
		case held != "":
			h.holdPush(ctx, wish, project, committed, branch, sha, held, out)
			return
		case state.GetHeld() != "" || state.GetHeldRuns() > 0:
			err := h.store.Tx(ctx, func(tx *store.Tx) error {
				return editPush(ctx, tx, wish.GetId(), project.GetId(), func(p *planv1.WishPush) { p.Held, p.HeldRuns = "", 0 })
			})
			if err != nil {
				log.Printf("djinn: push %s: %v", branch, err)
			}
		}
	}
	if wish.GetPushMode() == planv1.PushMode_PUSH_MODE_ASK && !approved {
		q := h.pushQuestion(wish, fmt.Sprintf("Push %s to %s? (%s)", branch, remote, commitsText(count, titles)),
			fmt.Sprintf("**Why now.** %s.\n\n**What it pushes.** %s\n\nDjinn never forces a push; agents never push.", capital(why),
				bulleted(titles, count)), []string{pushOption, notNowOption})
		q.Recommendation = "A: each commit passed the project's tests before it went into " + branch + "."
		if h.askPush(ctx, wish.GetId(), project.GetId(), q, "") == nil {
			h.pushSteps(ctx, committed, fmt.Sprintf("integration: a push of %s to %s is due (%s); Djinn asks you %s",
				branch, remote, why, q.GetCode()))
		}
		return
	}
	old, out, err := gitPush(pushing, repo, remote, branch, target)
	if pushing.Err() != nil {
		return
	}
	if err != nil {
		reason := refusal(out, err)
		q := h.pushQuestion(wish, fmt.Sprintf("%s refused the push of %s: %s. Djinn never forces a push: what should it do?",
			remote, branch, headline(reason)), fmt.Sprintf("**What git said.**\n\n```\n%s\n```\n\n**What it would push.** %s",
			strings.TrimSpace(out), bulleted(titles, count)), []string{fmt.Sprintf(pushAgainOption, branch), leavePushOption})
		if h.askPush(ctx, wish.GetId(), project.GetId(), q, reason) == nil {
			h.pushSteps(ctx, committed, fmt.Sprintf("integration: %s refused the push of %s: %s; Djinn asks you %s",
				remote, branch, headline(reason), q.GetCode()))
		}
		return
	}
	record := &planv1.IntegrationPush{
		WishId: wish.GetId(), ProjectId: project.GetId(), Branch: branch, Remote: remote, OldSha: old, NewSha: sha,
		Count: int32(count), Commits: titles, TaskIds: ids(committed), PushTime: timestamppb.New(h.now()),
	}
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actorHarness, methodPush, record); err != nil {
			return err
		}
		return editPush(ctx, tx, wish.GetId(), project.GetId(), func(p *planv1.WishPush) {
			p.Last, p.QuestionId, p.Refused, p.Approved = record, "", "", false
			p.Held, p.HeldRuns, p.Unchecked = "", 0, false
		})
	})
	if err != nil {
		log.Printf("djinn: push %s: %v", branch, err)
	}
	if unchecked {
		why += "; without the push checks, as you said"
	}
	h.pushSteps(ctx, committed, fmt.Sprintf("integration: pushed %s to %s as %s, %s (%s)", branch, remote, short8(sha),
		commitsText(count, nil), why))
	h.notify()
	if settings, err := plan.LoadSettings(h.home, project); err == nil && settings.Install != "" && h.built != nil {
		h.built(h.build(ctx, wish, project, settings.Install, record, committed))
	}
}

// clearApproved forgets an approval that found nothing to push.
func (h *Harness) clearApproved(ctx context.Context, wishID, projectID string, approved bool) {
	if !approved {
		return
	}
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		return editPush(ctx, tx, wishID, projectID, func(p *planv1.WishPush) { p.Approved = false })
	})
	if err != nil {
		log.Printf("djinn: push: %v", err)
	}
}

// pushSteps adds an event saying text to each task of tasks.
func (h *Harness) pushSteps(ctx context.Context, tasks []*planv1.Task, text string) {
	for _, t := range tasks {
		h.integrationStep(ctx, t, text)
	}
}

// pushQuestion is a question about pushing wish's integration branch, with its options; askPush asks it.
func (h *Harness) pushQuestion(wish *planv1.Wish, text, ctxt string, options []string) *planv1.Question {
	return &planv1.Question{
		Id: store.NewID(), WishId: wish.GetId(), CreateTime: timestamppb.New(h.now()), Icon: "🚀",
		Text: text, Context: ctxt, Options: options,
	}
}

// askPush asks q on the wish wishID, recorded as the question about the push of its integration branch in the project
// projectID, with refused, why the remote refused it, if it did.
func (h *Harness) askPush(ctx context.Context, wishID, projectID string, q *planv1.Question, refused string) error {
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actorHarness, methodIntegrate, q); err != nil {
			return err
		}
		if err := plan.Ask(ctx, tx, q); err != nil {
			return err
		}
		return editPush(ctx, tx, wishID, projectID, func(p *planv1.WishPush) {
			p.QuestionId, p.Approved = q.GetId(), false
			if refused != "" {
				p.Refused = refused
			}
		})
	})
	if err != nil {
		log.Printf("djinn: push: ask: %v", err)
		return err
	}
	h.notify()
	return nil
}

// editPush changes, with edit, where the push of the wish wishID stands in the project projectID, in tx.
func editPush(ctx context.Context, tx *store.Tx, wishID, projectID string, edit func(*planv1.WishPush)) error {
	wish, err := store.Get[*planv1.Wish](ctx, tx, wishID)
	if err != nil {
		return err
	}
	p := pushState(wish, projectID)
	if p == nil {
		p = &planv1.WishPush{ProjectId: projectID}
		wish.Pushes = append(wish.Pushes, p)
	}
	before := proto.CloneOf(p)
	edit(p)
	if proto.Equal(before, p) {
		return nil
	}
	if err := tx.Journal(actorHarness, methodIntegrate, wish); err != nil {
		return err
	}
	return tx.Put(wish)
}

// answerPush takes the person's answer to a question Djinn asked about a push: A pushes at the integration's next
// pass, B waits for the next push due; once the push checks stay red, B pushes without them and C waits. It tells
// whether q was such a question.
func (h *Harness) answerPush(ctx context.Context, q *planv1.Question) bool {
	wish, err := store.Get[*planv1.Wish](ctx, h.store, q.GetWishId())
	if err != nil {
		return false
	}
	i := slices.IndexFunc(wish.GetPushes(), func(p *planv1.WishPush) bool { return p.GetQuestionId() == q.GetId() })
	if i < 0 {
		return false
	}
	// Once the push checks stay red: A checks again and pushes if they pass, B pushes without them, C waits for the next
	// push due. Otherwise A pushes, B waits.
	choice, held := q.GetAnswer().GetChoice(), wish.GetPushes()[i].GetHeld() != ""
	push := choice == planv1.Choice_CHOICE_A || held && choice == planv1.Choice_CHOICE_B
	ctx = context.WithoutCancel(ctx)
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		return editPush(ctx, tx, wish.GetId(), wish.GetPushes()[i].GetProjectId(), func(p *planv1.WishPush) {
			p.QuestionId, p.Approved, p.Unchecked = "", push, held && choice == planv1.Choice_CHOICE_B
			if held && !push {
				p.HeldRuns = 0
			}
		})
	})
	if err != nil {
		log.Printf("djinn: question %s: %v", q.GetCode(), err)
		return true
	}
	h.notify()
	if push {
		h.kickIntegrate()
	}
	return true
}

// pushTarget is the remote the integration branch goes to, and its name there: the branch's upstream when it has one,
// else origin, else the repository's only remote; "" when it has none.
func pushTarget(ctx context.Context, repo, branch string) (remote, target string) {
	if branch == "" {
		return "", ""
	}
	target = branch
	if merge, err := git(ctx, repo, "config", "--get", "branch."+branch+".merge"); err == nil {
		target = cmp.Or(strings.TrimPrefix(merge, "refs/heads/"), branch)
	}
	if r, err := git(ctx, repo, "config", "--get", "branch."+branch+".remote"); err == nil && r != "" && r != "." {
		return r, target
	}
	out, err := git(ctx, repo, "remote")
	if err != nil || out == "" {
		return "", ""
	}
	remotes := strings.Fields(out)
	switch {
	case slices.Contains(remotes, "origin"):
		return "origin", target
	case len(remotes) == 1:
		return remotes[0], target
	}
	return "", ""
}

// unpushed are the commits of branch that no branch of remote holds, as this repository last saw it: the titles of
// the latest of them that are no merge, the latest first, how many such there are, and how many commits in all.
func unpushed(ctx context.Context, repo, branch, remote string) (titles []string, count, ahead int, err error) {
	tip, not := "refs/heads/"+branch, "--remotes="+remote
	n, err := git(ctx, repo, "rev-list", "--count", tip, "--not", not)
	if err != nil {
		return nil, 0, 0, err
	}
	if ahead, err = strconv.Atoi(n); err != nil || ahead == 0 {
		return nil, 0, ahead, err
	}
	if n, err = git(ctx, repo, "rev-list", "--count", "--no-merges", tip, "--not", not); err != nil {
		return nil, 0, 0, err
	}
	if count, err = strconv.Atoi(n); err != nil {
		return nil, 0, 0, err
	}
	out, err := git(ctx, repo, "log", "--no-merges", "--format=%s", "-n", strconv.Itoa(pushTitles), tip, "--not", not)
	if err != nil {
		return nil, 0, 0, err
	}
	if out != "" {
		titles = strings.Split(out, "\n")
	}
	return titles, count, ahead, nil
}

// gitPush pushes branch to target on remote from repo, never forcing, with the person's own credentials and without
// ever prompting for them. It returns the commit the remote's branch was at ("" for a new branch), and what git said.
func gitPush(ctx context.Context, repo, remote, branch, target string) (old, out string, err error) {
	ctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	ref := "refs/heads/" + branch + ":refs/heads/" + target
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "push", "--porcelain", remote, ref)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	out = strings.TrimSpace(stderr.String() + "\n" + stdout.String())
	if err != nil {
		return "", out, err
	}
	// A pushed ref is "<flag>\t<from>:<to>\t<old>..<new>": a space for a fast-forward, * for a new branch.
	for line := range strings.Lines(stdout.String()) {
		fields := strings.Split(strings.TrimRight(line, "\n"), "\t")
		if len(fields) < 3 || fields[1] != ref {
			continue
		}
		if from, _, ok := strings.Cut(fields[2], ".."); ok {
			if full, err := git(ctx, repo, "rev-parse", from); err == nil {
				return full, out, nil
			}
			return from, out, nil
		}
	}
	return "", out, nil
}

// refusal says why git refused a push, from what it said: the rejected ref's reason, and what the remote said.
func refusal(out string, err error) string {
	var why, remote []string
	for line := range strings.Lines(out) {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "!"):
			if f := strings.Split(line, "\t"); len(f) >= 3 {
				why = append(why, f[2])
			}
		case strings.HasPrefix(line, "remote:") && strings.TrimSpace(strings.TrimPrefix(line, "remote:")) != "":
			remote = append(remote, strings.TrimSpace(strings.TrimPrefix(line, "remote:")))
		}
	}
	s := strings.Join(why, "; ")
	switch {
	case strings.Contains(s, "fetch first") || strings.Contains(s, "non-fast-forward"):
		s = "its branch has commits that this one does not (" + s + ")"
	case s == "" && len(remote) == 0:
		s = cmp.Or(lastLine(out), err.Error())
	}
	if len(remote) > 0 {
		s = strings.TrimPrefix(s+"; the remote said: "+strings.Join(remote, " "), "; ")
	}
	return s
}

// lastLine is the last line of out that says something.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// commitsText says how many commits a push brings, with the titles of the first three: "3 commits: a, b, c".
func commitsText(count int, titles []string) string {
	s := fmt.Sprintf("%d commits", count)
	if count == 1 {
		s = "1 commit"
	}
	if len(titles) == 0 {
		return s
	}
	shown := titles[:min(3, len(titles))]
	s += ": " + strings.Join(shown, ", ")
	if count > len(shown) {
		s += ", …"
	}
	return s
}

// bulleted lists the titles of a push's commits in Markdown, saying how many more there are.
func bulleted(titles []string, count int) string {
	if len(titles) == 0 {
		return "Merges only."
	}
	var b strings.Builder
	for _, t := range titles {
		b.WriteString("\n- " + t)
	}
	if more := count - len(titles); more > 0 {
		fmt.Fprintf(&b, "\n- and %d more", more)
	}
	return strings.TrimPrefix(b.String(), "\n")
}

// capital is s with its first letter in capital.
func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
