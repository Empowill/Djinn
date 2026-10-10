package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// A question worker is a small task Djinn starts by itself on a question of a wish, so that the lead stays informed
// without being in the loop: after an answer, a converter turns the decision into tasks; after "Enlighten me", an
// investigator reads, then revises the question. It reads and runs djinn's commands only (TASK_ACCESS_DJINN), in its
// project's folder, and takes no slot. The project's settings turn them off, and choose their model and budget.

// methodQuestion is what the journal records when Djinn starts a question worker; the request is the spawn.
const methodQuestion = "harness/question"

// WithQuestionWorkers lets the harness start question workers: djinn up gives it unless told not to. Without it, the
// lead is told to act on each answer itself.
func WithQuestionWorkers() Option { return func(h *Harness) { h.questions = true } }

// questionWorker tells whether Djinn started the task on a question.
func questionWorker(t *planv1.Task) bool { return dispatch.QuestionWorker(t) }

// light tells whether the task takes no slot: a watcher, or a question worker.
func light(t *planv1.Task) bool { return dispatch.Light(t) }

// Enlightened starts an investigator on q, which the developer wants to know more about before answering, with
// their note. The plan services call it once the request is stored.
func (h *Harness) Enlightened(ctx context.Context, q *planv1.Question, note string) {
	h.askWorker(ctx, q, planv1.TaskRole_TASK_ROLE_INVESTIGATOR, note)
}

// askWorker starts a question worker of role on q, unless the project's settings turn them off, the wish is granted,
// or one of that role still works on q: that one is sent the new answer or note instead.
func (h *Harness) askWorker(ctx context.Context, q *planv1.Question, role planv1.TaskRole, note string) {
	h.questioning.Lock()
	defer h.questioning.Unlock()
	if _, err := h.startQuestionWorker(ctx, q, role, note, nil); err != nil {
		log.Printf("djinn: question %s: start its worker: %v", q.GetCode(), err)
	}
}

// startQuestionWorker starts a question worker of role on q, and returns it; nil when none starts. before is the
// worker it replaces, which gave up after a refusal: the new one is told what was refused, and a worker already at
// work on q is left alone. The caller holds h.questioning.
func (h *Harness) startQuestionWorker(
	ctx context.Context, q *planv1.Question, role planv1.TaskRole, note string, before *planv1.Task,
) (*planv1.Task, error) {
	if !h.questions {
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
	if !settings.QuestionWorkers {
		return nil, nil
	}
	brief, err := plan.BuildBrief(ctx, h.store, h.home, wish.GetId())
	if err != nil {
		return nil, err
	}
	title, prompt := converterPrompt(q, retryText(before), brief.Moving)
	if role == planv1.TaskRole_TASK_ROLE_INVESTIGATOR {
		title, prompt = investigatorPrompt(q, note, retryText(before), brief.Moving)
	}
	return h.spawn(ctx, methodQuestion, &planv1.TaskServiceSpawnRequest{
		WishId: wish.GetId(), ProjectId: project.GetId(), Title: title, Prompt: prompt, Provider: settings.Provider,
		Model: settings.QuestionModel, MaxBudgetUsd: settings.QuestionBudgetUSD,
	}, &planv1.Task{Role: role, Question: q.GetCode()})
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

// questionText writes q as a worker reads it: the question, its options, context and recommendation. The context
// keeps its lines as they are.
func questionText(b *strings.Builder, q *planv1.Question) {
	fmt.Fprintf(b, "## The question\n\n%s: %s\n\n", q.GetCode(), q.GetText())
	for i, o := range q.GetOptions() {
		fmt.Fprintf(b, "- %c: %s\n", 'A'+i, o)
	}
	if len(q.GetOptions()) > 0 {
		b.WriteString("\n")
	}
	if c := strings.TrimSpace(q.GetContext()); c != "" {
		b.WriteString("Context:\n\n" + c + "\n\n")
	}
	if r := strings.TrimSpace(q.GetRecommendation()); r != "" {
		b.WriteString("Recommended: " + r + "\n\n")
	}
}

// questionAccess tells a question worker how to work within its access (TASK_ACCESS_DJINN): its agent refuses a
// call that joins anything to a djinn command, since every part of a compound command must be allowed, and reads
// files with its own tools. Workers that took a refused form for a refused Bash gave up without doing anything.
const questionAccess = "## How to work within your access\n\n" +
	"You read files, and run djinn's commands (and `git log`, `git show`, `git diff`, `git status`): nothing else.\n\n" +
	"- Run each `djinn …` command alone, in a Bash call of its own: no pipe (`|`), no `;`, no `&&` nor `||`, no " +
	"redirection (`>`, `2>`), no `$(…)`, no `cd` before it. A call that joins anything to a djinn command is refused " +
	"whole. Read a command's whole output rather than filtering it.\n" +
	"- Read files with your Read, Grep and Glob tools, never with cat, grep, sed, head or find in Bash.\n" +
	"- Write the values of `--title`, `--prompt`, `--context`… between double quotes, with no backtick nor `$` inside: " +
	"there they would run a command, and the call is refused. Write an inner double quote `\\\"`.\n" +
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

// converterPrompt is the title and the prompt of the converter of the answered question q, in a wish whose brief's
// moving part is brief; retry says why it starts again, if it does.
func converterPrompt(q *planv1.Question, retry, brief string) (title, prompt string) {
	code, wish := q.GetCode(), q.GetWishId()
	var b strings.Builder
	fmt.Fprintf(&b, "You are a question worker of Djinn. The developer answered %s in wish %s. Your one job: turn "+
		"this decision into tasks for workers. You write no code, edit no file and run no gate: you only plan, with "+
		"Djinn's command line. The wish's lead is told what you did when you end.\n\n", code, wish)
	questionText(&b, q)
	fmt.Fprintf(&b, "## The answer\n\n%s\n\n", answerText(q))
	if n := strings.TrimSpace(q.GetAnswer().GetNote()); n != "" {
		b.WriteString("The developer's note:\n\n" + n + "\n\n")
	}
	fmt.Fprintf(&b, "## What to do\n\n"+
		"1. Read what you need: the brief below, `djinn task list --wish-id %[2]s`, the code (with Read, Grep and "+
		"Glob).\n"+
		"2. For each piece of work the decision calls for: `djinn task spawn %[2]s --title \"…\" --prompt \"…\" "+
		"--decision %[1]s`. A clear title; a prompt a worker can follow alone: what to do, in which files, how to "+
		"check it (the tests to run, through `djinn gate run`). `--part-of T07` puts it in the azima of the plan it "+
		"serves; `--after W3` only when it needs W3's result, so that tasks that do not need each other run side by "+
		"side; `--later` plans a task without starting it now. First look for the azima each task belongs to "+
		"(`djinn task list --wish-id %[2]s`): group what goes together, never mix what does not, and spawn no duplicate "+
		"of a task that exists. Open a new azima (`--kind azima`, one sentence a user reads as a feature) only for a "+
		"will no existing one carries. A task that extends an azima beyond its goal: say so in your last line, the "+
		"lead rephrases its goal.\n"+
		"3. If the answer leaves something open, do not guess: ask it, `djinn question ask \"…\" %[2]s --options \"…\" "+
		"--options \"…\" --recommendation \"…\" --context \"…\"`, its context naming %[1]s.\n"+
		"4. If the decision calls for no work, spawn nothing and say why in one sentence.\n"+
		"5. End with one line: what you spawned or asked.\n\n", code, wish)
	b.WriteString(questionAccess + retry + "## Where the wish stands\n\n" + brief)
	return code + " → tasks", b.String()
}

// investigatorPrompt is the title and the prompt of the investigator of q, which the developer wants to know more
// about, with their note; retry says why it starts again, if it does.
func investigatorPrompt(q *planv1.Question, note, retry, brief string) (title, prompt string) {
	code, wish := q.GetCode(), q.GetWishId()
	var b strings.Builder
	fmt.Fprintf(&b, "You are a question worker of Djinn. Before answering %s in wish %s, the developer wants to know "+
		"more. Your one job: find out, then revise the question so that it waits for the developer again. You write "+
		"no code, edit no file and run no gate: you read. The wish's lead is told what you did when you end.\n\n",
		code, wish)
	questionText(&b, q)
	if n := strings.TrimSpace(note); n != "" {
		b.WriteString("What the developer wants to know:\n\n" + n + "\n\n")
	}
	fmt.Fprintf(&b, "## What to do\n\n"+
		"1. Read the code (with Read, Grep and Glob), the brief below and `djinn task list --wish-id %[2]s`, to "+
		"answer what the developer asks.\n"+
		"2. Revise the question: `djinn question revise %[1]s --wish-id %[2]s --context \"…\" --recommendation \"…\"`, "+
		"and `--options \"…\"` (each one, up to four) when the options change. The context says what you found and "+
		"what each option costs, in a few lines; the recommendation starts with its option's letter (`B: …`).\n"+
		"3. Revise it once, at the end, even if you found little: the question waits for you until you do.\n"+
		"4. End with one line: what you found.\n\n", code, wish)
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
	if !refusedFailure(t) {
		h.tellEnd(t, "")
		return
	}
	// In the background: an answer to its question may be waiting for this very run to end (tellAgain), holding
	// what starting a worker takes. The run counts in h.wg until it ends, so Close waits for this one too.
	t = proto.CloneOf(t)
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.tellEnd(t, h.retryRefused(t))
	}()
}

// tellEnd tells the lead of t's wish what the question worker t did; retry is the worker Djinn started again in its
// place, if any.
func (h *Harness) tellEnd(t *planv1.Task, retry string) {
	h.mu.Lock()
	tell := h.tell
	h.mu.Unlock()
	if tell == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	line, err := h.questionEndLineOf(ctx, t, retry)
	if err == nil {
		err = tell(ctx, t.GetWishId(), line)
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
		log.Printf("djinn: task %s: read what it did: %v", t.GetCode(), err)
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
		log.Printf("djinn: task %s: start it again: %v", t.GetCode(), err)
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
		log.Printf("djinn: task %s: start it again: %v", t.GetCode(), err)
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
	case t.GetRole() == planv1.TaskRole_TASK_ROLE_CONVERTER && len(did) == 0:
		line += fmt.Sprintf(" Nothing came of %s: act on it, djinn wish brief %s has the context.", code, wish)
	case t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE:
		line += fmt.Sprintf(" Check what is left of %s: djinn wish brief %s has the context.", code, wish)
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
	if t.GetRole() == planv1.TaskRole_TASK_ROLE_INVESTIGATOR {
		return "investigates " + t.GetQuestion() + ", then revises it"
	}
	return "turns the decision " + t.GetQuestion() + " into tasks"
}
