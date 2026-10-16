package harness

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// A question worker is a small task Djinn starts by itself on a question of a wish, so that the lead stays informed
// without being in the loop: after "Enlighten me", an investigator reads, then revises the question. It reads and
// runs djinn's commands only (TASK_ACCESS_DJINN), in its project's folder, and takes no slot. The project's
// settings turn them off, and choose their model and budget.

// methodQuestion is what the journal records when Djinn starts a question worker; the request is the spawn.
const methodQuestion = "harness/question"

// WithEnlightenWorkers lets the harness start enlighten workers: true by default.
func WithEnlightenWorkers(on ...bool) Option {
	val := true
	if len(on) > 0 {
		val = on[0]
	}
	return func(h *Harness) { h.enlightenWorkers = &val }
}

// questionWorker tells whether Djinn started the task on a question.
func questionWorker(t *planv1.Task) bool { return dispatch.QuestionWorker(t) }

// light tells whether the task takes no slot: a watcher, or a question worker.
func light(t *planv1.Task) bool { return dispatch.Light(t) }

// WithEnlightenWait sets how long a request to investigate waits for its investigator to start before the lead is
// told instead: tests set a shorter one.
func WithEnlightenWait(d time.Duration) Option { return func(h *Harness) { h.enlightenWait = d } }

// defaultEnlightenWait is how long a request to investigate waits for its investigator to start: one starts in under
// a second.
const defaultEnlightenWait = 20 * time.Second

// errStartHangs says that an investigator did not start in time: what starts it hangs.
var errStartHangs = errors.New("the investigator did not start in time")

// Enlightened starts an investigator on q, which the developer wants to know more about before answering, with
// their note. The plan services call it once the request is stored; the lead is told next, when no investigator
// works on q (plan.Wishes.Enlightened). A start that hangs leaves q to the lead.
func (h *Harness) Enlightened(ctx context.Context, q *planv1.Question, note string) {
	if _, err := h.startInvestigator(ctx, q, note); err != nil {
		logx.L().Error("start an investigator", "loop", "question", "question", q.GetCode(), "err", err)
	}
}

// startInvestigator starts an investigator on q, with the developer's note, and returns it; nil when none starts. It
// waits for the start h.enlightenWait at most, then gives up with errStartHangs: the start of Q259 and Q262 waited
// behind one that a deadlock held (W549), and the lead never heard of them. The start goes on in the background: an
// investigator that starts late works beside the lead, which was told; Close waits for it.
func (h *Harness) startInvestigator(ctx context.Context, q *planv1.Question, note string) (*planv1.Task, error) {
	type started struct {
		task *planv1.Task
		err  error
	}
	done := make(chan started, 1)
	h.wg.Add(1)
	go func() {
		defer logx.Crash("question", "question", q.GetCode())
		defer h.wg.Done()
		h.questioning.Lock()
		defer h.questioning.Unlock()
		t, err := h.startQuestionWorker(ctx, q, planv1.TaskRole_TASK_ROLE_INVESTIGATOR, note, nil)
		done <- started{t, err}
	}()
	wait := cmp.Or(h.enlightenWait, defaultEnlightenWait)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case s := <-done:
		return s.task, s.err
	case <-timer.C:
		return nil, fmt.Errorf("%w (%s)", errStartHangs, wait)
	}
}

// ResumeInvestigations follows up each request to investigate that was left without one: djinn up stopped, or hung
// until it was made to quit, between storing the request and starting its investigator or telling the lead, and the
// question stayed under investigation with no one on it (W536). Each one gets its investigator now, or else the
// lead's event, once: a request that an investigator took on, or the lead heard of, is followed up already. djinn
// up calls it once the lead's events go out; it works in the background, and Close waits for it.
func (h *Harness) ResumeInvestigations() {
	h.wg.Add(1)
	go func() {
		defer logx.Crash("question")
		defer h.wg.Done()
		h.resumeInvestigations(h.ctx)
	}()
}

// resumeInvestigations is ResumeInvestigations, done now.
func (h *Harness) resumeInvestigations(ctx context.Context) {
	questions, err := store.List[*planv1.Question](ctx, h.store, nil)
	if err != nil {
		logx.L().Error("list the questions under investigation", "loop", "question", "err", err)
		return
	}
	for _, q := range questions {
		if !plan.Investigating(q) {
			continue
		}
		if err := h.resumeInvestigation(ctx, q); err != nil {
			logx.L().Error("follow up a request to investigate", "loop", "question", "wish", q.GetWishId(),
				"question", q.GetCode(), "err", err)
		}
	}
}

// resumeInvestigation follows up the last request to investigate q, unless it was: its investigator, or else the
// lead, is told.
func (h *Harness) resumeInvestigation(ctx context.Context, q *planv1.Question) error {
	rounds := q.GetRounds()
	asked := rounds[len(rounds)-1]
	wish, err := store.Get[*planv1.Wish](ctx, h.store, q.GetWishId())
	if err != nil || wish.GetState() == planv1.WishState_WISH_STATE_GRANTED {
		return err
	}
	if followed, err := h.investigationFollowed(ctx, q, asked); err != nil || followed {
		return err
	}
	t, err := h.startInvestigator(ctx, q, asked.GetNote())
	if t != nil {
		return nil
	}
	if err != nil {
		logx.L().Error("start an investigator", "loop", "question", "question", q.GetCode(), "err", err)
	}
	err = h.toLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_ENLIGHTEN, q.GetWishId(), q.GetCode(),
		plan.EnlightenLine(q, asked.GetNote(), ""))
	if errors.Is(err, plan.ErrNoLead) {
		return nil
	}
	return err
}

// investigationFollowed tells whether the request to investigate q, asked, was followed up: an investigator still
// works on q, or one was made since, or the lead was told of q since.
func (h *Harness) investigationFollowed(ctx context.Context, q *planv1.Question, asked *planv1.Round) (bool, error) {
	if t, err := plan.QuestionWorkerOf(ctx, h.store, q, planv1.TaskRole_TASK_ROLE_INVESTIGATOR); err != nil || t != nil {
		return t != nil, err
	}
	since := asked.GetCreateTime().AsTime()
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": q.GetWishId()})
	if err != nil {
		return false, err
	}
	if slices.ContainsFunc(tasks, func(t *planv1.Task) bool {
		return t.GetRole() == planv1.TaskRole_TASK_ROLE_INVESTIGATOR && t.GetQuestion() == q.GetCode() &&
			!t.GetCreateTime().AsTime().Before(since)
	}) {
		return true, nil
	}
	events, err := store.List[*planv1.LeadEvent](ctx, h.store, store.Where{"wish_id": q.GetWishId()})
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(events, func(e *planv1.LeadEvent) bool {
		return e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_ENLIGHTEN && e.GetCode() == q.GetCode() &&
			!e.GetCreateTime().AsTime().Before(since)
	}), nil
}

// startQuestionWorker starts a question worker of role on q, and returns it; nil when none starts. before is the
// worker it replaces, which gave up after a refusal: the new one is told what was refused, and a worker already at
// work on q is left alone. The caller holds h.questioning.
func (h *Harness) startQuestionWorker(
	ctx context.Context, q *planv1.Question, role planv1.TaskRole, note string, before *planv1.Task,
) (*planv1.Task, error) {
	if role != planv1.TaskRole_TASK_ROLE_INVESTIGATOR {
		return nil, nil
	}
	if h.enlightenWorkers != nil && !*h.enlightenWorkers {
		return nil, nil
	}
	wish, err := store.Get[*planv1.Wish](ctx, h.store, q.GetWishId())
	if err != nil || wish.GetState() == planv1.WishState_WISH_STATE_GRANTED {
		return nil, err
	}
	if t, err := plan.QuestionWorkerOf(ctx, h.store, q, role); err != nil || t != nil {
		if t != nil && before == nil {
			h.tellAgain(ctx, t, q, role, note)
		}
		return nil, err
	}
	var project *planv1.Project
	if ids := wish.GetProjectIds(); len(ids) > 0 {
		if project, err = store.Get[*planv1.Project](ctx, h.store, ids[0]); err != nil {
			return nil, err
		}
	}
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil {
		return nil, err
	}
	allow := settings.EnlightenWorkers
	if settings.EnlightenWorkersFrom == planv1.SettingSource_SETTING_SOURCE_DEFAULT && h.enlightenWorkers != nil {
		allow = *h.enlightenWorkers
	}
	if !allow {
		return nil, nil
	}
	brief, err := plan.BuildBrief(ctx, h.store, h.home, wish.GetId())
	if err != nil {
		return nil, err
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		return nil, err
	}
	title, prompt := investigatorPrompt(q, note, retryText(before), brief.Moving, tasks...)
	provider, model, _ := questionProvider(settings)
	task := &planv1.Task{Role: role, Question: q.GetCode()}
	return h.spawn(ctx, methodQuestion, &planv1.TaskServiceSpawnRequest{
		WishId: wish.GetId(), ProjectId: project.GetId(), Title: title, Prompt: prompt, Provider: provider,
		Model: model, MaxBudgetUsd: settings.QuestionBudgetUSD,
	}, task)
}

// questionProvider returns the provider and model for a question worker given the project's settings,
// and why it fell back to Claude if it did.
func questionProvider(s plan.Settings) (provider planv1.Provider, model, fallback string) {
	provider = s.QuestionProvider
	model = s.QuestionModel
	if provider != planv1.Provider_PROVIDER_UNSPECIFIED {
		if provider == s.Provider && model == "" {
			model = s.Model
		}
		if provider == planv1.Provider_PROVIDER_CLAUDE && model == "" {
			model = plan.DefaultQuestionModel
		}
		return provider, model, ""
	}
	if plan.CanReadOnly(s.Provider) {
		return s.Provider, cmp.Or(s.QuestionModel, s.Model), ""
	}
	return planv1.Provider_PROVIDER_CLAUDE, cmp.Or(s.QuestionModel, plan.DefaultQuestionModel),
		fmt.Sprintf("%s cannot run read-only: %s", short(s.Provider), short(planv1.Provider_PROVIDER_CLAUDE))
}

// tellAgain gives the question worker t, which still works on q, the developer's new answer or note: one worker per
// question and role at a time.
func (h *Harness) tellAgain(ctx context.Context, t *planv1.Task, q *planv1.Question, role planv1.TaskRole, note string) {
	text := fmt.Sprintf("The developer answered %s again: %s.", q.GetCode(), answerText(q))
	if n := q.GetAnswer().GetNote(); n != "" {
		text += " Their note: " + n
	}
	if role == planv1.TaskRole_TASK_ROLE_INVESTIGATOR {
		text = fmt.Sprintf("The developer asked again to know more about %s.", q.GetCode())
		if note != "" {
			text += " Their note: " + note
		}
	}
	_, err := h.Send(ctx, planv1connect.TaskServiceSendProcedure, &planv1.TaskServiceSendRequest{TaskId: t.GetId(), Text: text})
	if err != nil {
		h.Note(t.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_LOG, Text: "not told: " + text + " (" + err.Error() + ")"})
	}
}

// answerText is the answer to q: its letter and option, or yes.
func answerText(q *planv1.Question) string {
	c := q.GetAnswer().GetChoice()
	if c == planv1.Choice_CHOICE_YES {
		return "yes"
	}
	letter := strings.TrimPrefix(c.String(), "CHOICE_")
	if i := int(c - planv1.Choice_CHOICE_A); i >= 0 && i < len(q.GetOptions()) {
		return letter + ": " + q.GetOptions()[i]
	}
	return letter
}

// questionText writes q as a worker reads it: the question, its options, clean context, recommendation, linked/blocked
// tasks, and the full chronological history from Tour 0 through all rounds.
func questionText(b *strings.Builder, q *planv1.Question, tasks ...*planv1.Task) {
	fmt.Fprintf(b, "## The question\n\n%s: %s\n\n", q.GetCode(), q.GetText())
	for i, o := range q.GetOptions() {
		fmt.Fprintf(b, "- %c: %s\n", 'A'+i, o)
	}
	if len(q.GetOptions()) > 0 {
		b.WriteString("\n")
	}
	if c := strings.TrimSpace(plan.CleanContext(q.GetContext())); c != "" {
		b.WriteString("Context:\n\n" + c + "\n\n")
	}
	if r := strings.TrimSpace(q.GetRecommendation()); r != "" {
		b.WriteString("Recommended: " + r + "\n\n")
	}
	if before := strings.TrimSpace(q.GetBefore()); before != "" {
		b.WriteString("Before (--before): " + before + "\n\n")
	}
	writeLinkedTasks(b, q, tasks)
	writeQuestionHistory(b, q)
}

func writeLinkedTasks(b *strings.Builder, q *planv1.Question, tasks []*planv1.Task) {
	code, id, taskID, before := q.GetCode(), q.GetId(), q.GetTaskId(), q.GetBefore()
	var lines []string
	for _, t := range tasks {
		if t.GetRole() == planv1.TaskRole_TASK_ROLE_INVESTIGATOR {
			continue
		}
		var reasons []string
		if taskID != "" && t.GetId() == taskID {
			reasons = append(reasons, "asked "+code)
		}
		if code != "" && strings.EqualFold(t.GetDecision(), code) {
			reasons = append(reasons, "waiting on decision "+code)
		}
		if id != "" && t.GetEditQuestionId() == id {
			reasons = append(reasons, "waiting on edit question "+code)
		}
		if id != "" && t.GetIntegration().GetQuestionId() == id {
			reasons = append(reasons, "waiting on integration question "+code)
		}
		if len(reasons) == 0 && before != "" && t.GetCode() != "" && strings.Contains(before, t.GetCode()) {
			reasons = append(reasons, "named in --before")
		}
		if len(reasons) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s (%s) [%s]: %s",
			t.GetCode(), t.GetTitle(), short(t.GetStatus()), strings.Join(reasons, ", ")))
	}
	if len(lines) > 0 {
		b.WriteString("## Linked and blocked tasks\n\n" + strings.Join(lines, "\n") + "\n\n")
	}
}

func tourZero(q *planv1.Question) (options []string, context, recommendation string) {
	for _, r := range q.GetRounds() {
		if len(r.GetOptions()) > 0 || r.GetContext() != "" || r.GetRecommendation() != "" {
			return r.GetOptions(), plan.CleanContext(r.GetContext()), r.GetRecommendation()
		}
	}
	return q.GetOptions(), plan.CleanContext(q.GetContext()), q.GetRecommendation()
}

func revisionAfter(q *planv1.Question, rounds []*planv1.Round, idx int) (options []string, context, recommendation string) {
	for _, r := range rounds[idx+1:] {
		if r.GetKind() == planv1.RoundKind_ROUND_KIND_REVISE {
			return r.GetOptions(), plan.CleanContext(r.GetContext()), r.GetRecommendation()
		}
	}
	return q.GetOptions(), plan.CleanContext(q.GetContext()), q.GetRecommendation()
}

func writeStateLines(b *strings.Builder, options []string, context, recommendation string) {
	if len(options) > 0 {
		var formatted []string
		for i, o := range options {
			formatted = append(formatted, fmt.Sprintf("%c: %s", 'A'+i, o))
		}
		fmt.Fprintf(b, "- Options: %s\n", strings.Join(formatted, " | "))
	}
	if r := strings.TrimSpace(recommendation); r != "" {
		fmt.Fprintf(b, "- Recommendation: %s\n", r)
	}
	if c := strings.TrimSpace(context); c != "" {
		fmt.Fprintf(b, "- Context:\n\n%s\n", c)
	}
}

func writeQuestionHistory(b *strings.Builder, q *planv1.Question) {
	rounds := q.GetRounds()
	initOpts, initCtx, initRec := tourZero(q)
	b.WriteString("## Question history (from Tour 0)\n\n")
	fmt.Fprintf(b, "### Tour 0 (initial question state)\n- Subject: %s: %s\n", q.GetCode(), q.GetText())
	writeStateLines(b, initOpts, initCtx, initRec)
	b.WriteString("\n")
	revNum := 0
	for i, r := range rounds {
		switch r.GetKind() {
		case planv1.RoundKind_ROUND_KIND_ENLIGHTEN:
			note := strings.TrimSpace(r.GetNote())
			if note == "" {
				note = "(no note)"
			}
			fmt.Fprintf(b, "### Round %d — Enlighten request (Tour %d)\n- Note: %s\n\n", i+1, revNum, note)
		case planv1.RoundKind_ROUND_KIND_REVISE:
			revNum++
			opts, ctx, rec := revisionAfter(q, rounds, i)
			by := ""
			if r.GetTaskId() != "" {
				by = " (task " + r.GetTaskId() + ")"
			}
			fmt.Fprintf(b, "### Round %d — Revision %d (Tour %d)%s\n", i+1, revNum, revNum, by)
			writeStateLines(b, opts, ctx, rec)
			b.WriteString("\n")
		}
	}
}

// questionAccess tells a question worker how to work within its access (TASK_ACCESS_DJINN): its agent refuses a
// call that joins anything to a djinn command, since every part of a compound command must be allowed, and reads
// files with its own tools. Workers that took a refused form for a refused Bash gave up without doing anything.
const questionAccess = "## How to work within your access\n\n" +
	"You read files, search the web and fetch pages, and run djinn's inspection/revision commands (`djinn wish brief`, " +
	"`djinn task list`, `djinn task get`, `djinn question list`, `djinn block list`, `djinn tilasm list`, " +
	"`djinn tilasm get`, `djinn task spawn`, `djinn question revise`) and read-only git inspection commands " +
	"(`git log`, `git show`, `git diff`, `git status`, `git branch`, `git rev-parse`, `git ls-tree`): nothing else.\n\n" +
	"- Run each `djinn …` or `git …` command alone, in a Bash call of its own: no pipe (`|`), no `;`, no `&&` nor `||`, no " +
	"redirection (`>`, `2>`, `<<`), no `$(…)`, no `cd` before it. A call that joins anything to a command is " +
	"refused whole. Read a command's whole output rather than filtering it.\n" +
	"- Read files with your Read, Grep and Glob tools, and search or fetch the web with your web tools (WebSearch " +
	"and WebFetch, or search_web and read_url_content), never with cat, grep, sed, head, find or curl in Bash.\n" +
	"- Write the values of `--title`, `--prompt`, `--context`, `--recommendation`, `--options`… between double quotes, " +
	"with no backtick nor `$` inside: there they would run a command, and the call is refused. Never start a line " +
	"inside a quoted argument with `#` at column zero (indent `#` comments inside a `~~~` block with a leading space, " +
	"` # …`, or keep lines plain), as a newline followed immediately by `#` inside quotes is refused by the shell " +
	"filter; or pass `--context-file <path>` to read the context from a file when Markdown headers (`#`), arrows " +
	"(`->`) or parentheses would be rejected by shell static filters. Write an inner double quote `\\\"`.\n" +
	"- A refused call means that this form of the command is refused, not that Bash is: run the djinn command again, " +
	"alone and plain. Never end on a refusal: a question worker that did nothing after one has failed.\n\n"

// retryText tells the worker that starts again in the place of before, which gave up after a refusal, what was
// refused; nothing without before.
func retryText(before *planv1.Task) string {
	if before == nil {
		return ""
	}
	return fmt.Sprintf("## Before you\n\n%s had this job before you, and ended without doing anything after this call "+
		"was refused: %s. Only that form was refused, not Bash: work as the section above says, and do the job.\n\n",
		before.GetCode(), strings.TrimPrefix(before.GetError(), refusedPrefix))
}

// investigatorPrompt is the title and the prompt of the investigator of q, which the developer wants to know more
// about, with their note; retry says why it starts again, if it does.
func investigatorPrompt(q *planv1.Question, note, retry, brief string, tasks ...*planv1.Task) (title, prompt string) {
	code, wish := q.GetCode(), q.GetWishId()
	var b strings.Builder
	fmt.Fprintf(&b, "You are a question worker of Djinn. Before answering %s in wish %s, the developer wants to know "+
		"more. Your one job: find out, then revise the question so that it waits for the developer again. You write "+
		"no code, edit no file and run no gate: you read the codebase and search the web. The wish's lead is told "+
		"what you did when you end.\n\n",
		code, wish)
	questionText(&b, q, tasks...)
	if n := strings.TrimSpace(note); n != "" {
		b.WriteString("What the developer wants to know:\n\n" + n + "\n\n")
	}
	fmt.Fprintf(&b, "## What to do\n\n"+
		"1. Stay anchored to the original subject from Tour 0 (`%[1]s: %[3]s`): follow-up enlighten notes ask to "+
		"dig deeper into this question, never to replace its subject with an unrelated topic.\n"+
		"2. Read the code (with Read, Grep and Glob), inspect git history across branches when needed (`git log`, "+
		"`git show`, `git diff`, `git status`, `git branch`, `git rev-parse`, `git ls-tree`), search the web and "+
		"fetch external documentation or references (with WebSearch and WebFetch, or search_web and read_url_content) "+
		"when helpful, and read the brief below and Djinn's state (`djinn wish brief %[2]s`, "+
		"`djinn task list --wish-id %[2]s`, `djinn task get <task>`, `djinn question list --wish-id %[2]s`, "+
		"`djinn block list %[2]s`, `djinn tilasm list --wish %[2]s`, `djinn tilasm get <tilasm> --wish %[2]s`), to "+
		"answer what the developer asks.\n"+
		"3. Revise the question: `djinn question revise %[1]s --wish-id %[2]s --context \"…\" --recommendation \"…\"` "+
		"(or `--context-file <path>`), and `--options \"…\"` (each one, up to four) when the options change. The "+
		"context says what you found and what each option costs, in the shape below; Djinn keeps the former version "+
		"in the question's rounds, so write the current context only, never the earlier versions. Keep it short: what you found "+
		"to choose with full light, an analysis, a comparison, a mockup, goes in a tilasm that lays out the possible "+
		"choices, and the context links it (`djinn://tilasm/<id>`) instead of carrying it. You write no file: link a "+
		"tilasm of the wish (`djinn tilasm list --wish %[2]s`), or spawn a worker that makes one "+
		"(`djinn task spawn %[2]s --title \"…\" --prompt \"…\"`) and name it in your end line, so the lead links "+
		"it once made. Set `--before \"…\"` only when the answer is needed before something, `--before \"\"` when "+
		"it can wait: never to get it seen.\n"+
		"4. Revise it once, at the end, even if you found little: the question waits for you until you do.\n"+
		"5. End with one line: what you found.\n\n", code, wish, q.GetText())
	b.WriteString("## The shape of the context\n\n" + plan.QuestionShape + " Rewrite the former context into this " +
		"shape even where you found nothing new.\n\n")
	b.WriteString(questionAccess + retry + "## Where the wish stands\n\n" + brief)
	return code + ": enlighten", b.String()
}

// questionEnded tells the lead of the question worker t's wish what it did, now that it ended: the tasks it spawned
// from its question, what it asked, or that it revised the question; and what is left to the lead. One that gave up
// after a refusal is started again first, once, and the lead hears that Djinn did.
func (h *Harness) questionEnded(t *planv1.Task) {
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_STOPPED:
	default:
		return // Djinn resumes it, or it waits: it has not ended.
	}
	h.unpausePreempted(t)
	if !refusedFailure(t) {
		h.tellEnd(t, "")
		return
	}
	// In the background: an answer to its question may be waiting for this very run to end (tellAgain), holding
	// what starting a worker takes. The run counts in h.wg until it ends, so Close waits for this one too.
	t = proto.CloneOf(t)
	h.wg.Add(1)
	go func() {
		defer logx.Crash("question", "task", t.GetCode())
		defer h.wg.Done()
		h.tellEnd(t, h.retryRefused(t))
	}()
}

// tellEnd tells the lead of t's wish what the question worker t did; retry is the worker Djinn started again in its
// place, if any.
func (h *Harness) tellEnd(t *planv1.Task, retry string) {
	h.mu.Lock()
	told := h.tell != nil || h.notifyLead != nil
	h.mu.Unlock()
	if !told {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	line, err := h.questionEndLineOf(ctx, t, retry)
	if err == nil {
		err = h.toLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, t.GetWishId(), t.GetCode(), line)
	}
	if err != nil && !errors.Is(err, plan.ErrNoLead) {
		h.Note(t.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_LOG, Text: "the lead was not told: " + err.Error()})
	}
}

// refusedPrefix starts the error of a question worker that did nothing after its permissions refused a call.
const refusedPrefix = "its command was refused: "

// refusedError is the error of a question worker that did nothing after its permissions refused the call what, as
// its agent named it: for a command, the command, on one line.
func refusedError(what string) string {
	tool, input, _ := strings.Cut(what, " ")
	var call struct {
		Command string `json:"command"`
	}
	if (tool == "Bash" || tool == "PowerShell") && json.Unmarshal([]byte(input), &call) == nil && call.Command != "" {
		what = call.Command
	}
	return refusedPrefix + strings.Join(strings.Fields(what), " ")
}

// refusedFailure tells whether the question worker t failed for doing nothing after a refusal.
func refusedFailure(t *planv1.Task) bool {
	return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_FAILED && strings.HasPrefix(t.GetError(), refusedPrefix)
}

// questionDid tells whether the question worker t, which ends, spawned, asked or revised anything from its question.
// Not knowing, it says yes: a worker is never failed on a doubt.
func (h *Harness) questionDid(t *planv1.Task) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tasks, questions, err := h.wishWork(ctx, t.GetWishId())
	if err != nil {
		logx.L().Error("read what a question worker did", "loop", "question", "task", t.GetCode(), "err", err)
		return true
	}
	spawned, asked, revised := questionOutcome(t, tasks, questions)
	return len(spawned)+len(asked) > 0 || revised
}

// retryRefused starts a question worker again in the place of t, which gave up after a refusal, and returns its code;
// "" when none starts. Djinn retries once per question and role: never when another worker on it gave up so before,
// nor while another one works on it.
func (h *Harness) retryRefused(t *planv1.Task) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tasks, questions, err := h.wishWork(ctx, t.GetWishId())
	if err != nil {
		logx.L().Error("start a question worker again", "loop", "question", "task", t.GetCode(), "err", err)
		return ""
	}
	if slices.ContainsFunc(tasks, func(o *planv1.Task) bool {
		return o.GetId() != t.GetId() && o.GetRole() == t.GetRole() && o.GetQuestion() == t.GetQuestion() && refusedFailure(o)
	}) {
		return ""
	}
	i := slices.IndexFunc(questions, func(q *planv1.Question) bool { return q.GetCode() == t.GetQuestion() })
	if i < 0 {
		return ""
	}
	q, note := questions[i], ""
	if t.GetRole() == planv1.TaskRole_TASK_ROLE_INVESTIGATOR {
		for _, r := range q.GetRounds() {
			if r.GetKind() == planv1.RoundKind_ROUND_KIND_ENLIGHTEN {
				note = r.GetNote() // The last request is the one it worked on.
			}
		}
	}
	h.questioning.Lock()
	defer h.questioning.Unlock()
	again, err := h.startQuestionWorker(ctx, q, t.GetRole(), note, t)
	if err != nil {
		logx.L().Error("start a question worker again", "loop", "question", "task", t.GetCode(), "err", err)
	}
	return again.GetCode()
}

// wishWork are the tasks and the questions of the wish.
func (h *Harness) wishWork(ctx context.Context, wishID string) ([]*planv1.Task, []*planv1.Question, error) {
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wishID})
	if err != nil {
		return nil, nil, err
	}
	questions, err := store.List[*planv1.Question](ctx, h.store, store.Where{"wish_id": wishID})
	return tasks, questions, err
}

// QuestionEndLine is the line that tells a lead what the question worker t did, once it ended.
func (h *Harness) QuestionEndLine(ctx context.Context, t *planv1.Task) (string, error) {
	return h.questionEndLineOf(ctx, t, "")
}

func (h *Harness) questionEndLineOf(ctx context.Context, t *planv1.Task, retry string) (string, error) {
	tasks, questions, err := h.wishWork(ctx, t.GetWishId())
	if err != nil {
		return "", err
	}
	return questionEndLine(t, tasks, questions, retry), nil
}

// questionOutcome is what the question worker t did from its question: the tasks it spawned from it, the questions it
// asked, and whether it revised it.
func questionOutcome(t *planv1.Task, tasks []*planv1.Task, questions []*planv1.Question) (spawned, asked []string, revised bool) {
	code := t.GetQuestion()
	for _, o := range tasks {
		if strings.EqualFold(o.GetDecision(), code) && !o.GetCreateTime().AsTime().Before(t.GetCreateTime().AsTime()) {
			spawned = append(spawned, o.GetCode())
		}
	}
	for _, q := range questions {
		if q.GetTaskId() == t.GetId() {
			asked = append(asked, q.GetCode())
		}
		if q.GetCode() == code {
			for _, r := range q.GetRounds() {
				revised = revised || r.GetKind() == planv1.RoundKind_ROUND_KIND_REVISE && r.GetTaskId() == t.GetId()
			}
		}
	}
	return spawned, asked, revised
}

// questionEndLine is the line that tells a lead what the question worker t did; retry is the worker Djinn started
// again in its place, if any.
func questionEndLine(t *planv1.Task, tasks []*planv1.Task, questions []*planv1.Question, retry string) string {
	code, wish := t.GetQuestion(), t.GetWishId()
	spawned, asked, revised := questionOutcome(t, tasks, questions)
	var did []string
	if len(spawned) > 0 {
		did = append(did, "spawned "+strings.Join(spawned, ", ")+" from "+code)
	}
	if len(asked) > 0 {
		did = append(did, "asked "+strings.Join(asked, ", "))
	}
	if revised {
		did = append(did, "revised "+code+": it waits for the developer again")
	}
	ended := "ended"
	switch {
	case refusedFailure(t):
		ended = "failed: " + clipRunes(t.GetError(), 200)
	case t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE:
		ended = statusText(t)
	}
	line := fmt.Sprintf("Djinn: %s (%s) %s", t.GetCode(), t.GetTitle(), ended)
	if len(did) > 0 {
		line += ": " + strings.Join(did, "; ")
	}
	line += "."
	switch {
	case retry != "":
		line += fmt.Sprintf(" Djinn starts it again: %s, told to run each djinn command alone; you will hear when it ends.", retry)
	case t.GetRole() == planv1.TaskRole_TASK_ROLE_INVESTIGATOR && !revised:
		line += fmt.Sprintf(" %s still waits for a revision: djinn task get %s has what it found.", code, t.GetId())
	case t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE:
		line += fmt.Sprintf(" Check what is left of %s: djinn wish brief %s has the context.", code, wish)
	case len(spawned) > 0:
		line += " Check every task that ends: read its final note (djinn task watch <task>, or the line Djinn types): what it leaves to verify (something it did not verify, a failing test it calls unrelated) goes to a task or a watcher that verifies it, never to the developer; when it leaves an uncertainty (a choice it made in the developer's place, a partial result, a step it skipped, 'to validate'), ask the developer a question about it, with what the worker said, instead of letting it pass; a move only the developer can make is a question too. Withdraw the open questions its work made needless."
	}
	return line
}

// statusText says how a task that did not end done ended, with its error.
func statusText(t *planv1.Task) string {
	text := short(t.GetStatus())
	if e := t.GetError(); e != "" {
		text += " (" + clipRunes(e, 120) + ")"
	}
	return text
}

// questionStart is what a question worker's start event says it is.
func questionStart(t *planv1.Task) string {
	return "investigates " + t.GetQuestion() + ", then revises it"
}
