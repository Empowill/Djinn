package harness

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// A document agent answers the developer in the discussions of one Markdown tilasm (TASK_ROLE_DOCUMENT, Task.tilasm_id).
// Djinn wakes it when the developer writes in a discussion: one agent per tilasm, sent the new discussions while it
// runs, given a new turn of its own session once it ended, so that it keeps the memory of the document's discussions;
// a new one only when that session cannot go on. Like a question worker it reads in its project's folder, takes no
// slot, and its project's question worker settings turn it off and choose its model and budget. It runs djinn's
// commands to read, reply, propose and tell the lead (documentCommands), as the author document ($DJINN_AUTHOR).

// methodDocument is what the journal records when Djinn starts a document agent, or gives it a new turn.
const methodDocument = "harness/document"

// documentAgent tells whether Djinn started the task as a tilasm's document agent.
func documentAgent(t *planv1.Task) bool { return t.GetRole() == planv1.TaskRole_TASK_ROLE_DOCUMENT }

// Discussed wakes the document agent of thread's tilasm, in which the developer just wrote. The plan services call it
// once the message is stored (plan.WithDiscussed).
func (h *Harness) Discussed(ctx context.Context, thread *planv1.TilasmThread) {
	if !h.questions {
		return
	}
	ctx = context.WithoutCancel(ctx) // The developer's message is stored: its agent wakes even if the request goes.
	h.documenting.Lock()
	defer h.documenting.Unlock()
	if err := h.wakeDocument(ctx, thread.GetWishId(), thread.GetTilasmId(), []*planv1.TilasmThread{thread}); err != nil {
		log.Printf("djinn: tilasm %s: wake its document agent: %v", thread.GetTilasmId(), err)
	}
}

// WakeDocuments wakes, once as djinn up starts and after Recover, the document agent of each Markdown tilasm with
// discussions that wait for it (plan.AwaitsAgent) and that no worker runs: left unanswered when djinn stopped, or
// while question workers were off. It works in the background.
func (h *Harness) WakeDocuments(ctx context.Context) {
	if !h.questions {
		return
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		ctx := context.WithoutCancel(ctx)
		if err := h.wakeDocuments(ctx); err != nil {
			log.Printf("djinn: wake the document agents: %v", err)
		}
	}()
}

func (h *Harness) wakeDocuments(ctx context.Context) error {
	threads, err := store.List[*planv1.TilasmThread](ctx, h.store, store.Where{})
	if err != nil {
		return err
	}
	proposals, err := store.List[*planv1.TilasmProposal](ctx, h.store, store.Where{})
	if err != nil {
		return err
	}
	type key struct{ wish, tilasm string }
	var order []key
	waiting := map[key][]*planv1.TilasmThread{}
	for _, thread := range threads {
		if !plan.AwaitsAgent(thread, proposals) {
			continue
		}
		k := key{thread.GetWishId(), thread.GetTilasmId()}
		if waiting[k] == nil {
			order = append(order, k)
		}
		waiting[k] = append(waiting[k], thread)
	}
	h.documenting.Lock()
	defer h.documenting.Unlock()
	for _, k := range order {
		if h.ctx.Err() != nil {
			return nil
		}
		agent, err := plan.DocumentAgentOf(ctx, h.store, k.wish, k.tilasm)
		if err != nil {
			return err
		}
		if agent != nil && h.running(agent.GetId()) {
			continue // It runs, resumed: it reads the discussions that wait for it.
		}
		if err := h.wakeDocument(ctx, k.wish, k.tilasm, waiting[k]); err != nil {
			log.Printf("djinn: tilasm %s: wake its document agent: %v", k.tilasm, err)
		}
	}
	return nil
}

// wakeDocument has the tilasm's document agent answer the developer in threads: told while it runs; while it waits to
// start or resume, its pending turn is told the discussions that wait for it now (retellDocument); a new turn of its
// session once it ended; else a new agent. An agent whose provider or model is no longer the one the wish's question
// workers get (plan.QuestionAgent), or whose provider is not available, is not continued: Djinn closes it and starts
// a new one (staleDocument). Nothing for a granted wish, an HTML tilasm, or a project whose settings turn question
// workers off. The caller holds h.documenting.
func (h *Harness) wakeDocument(ctx context.Context, wishID, tilasmID string, threads []*planv1.TilasmThread) error {
	wish, err := store.Get[*planv1.Wish](ctx, h.store, wishID)
	if err != nil || wish.GetState() == planv1.WishState_WISH_STATE_GRANTED {
		return err
	}
	tilasm, err := store.Get[*planv1.Tilasm](ctx, h.store, tilasmID)
	if err != nil || tilasm.GetFormat() != planv1.TilasmFormat_TILASM_FORMAT_MARKDOWN {
		return err
	}
	agent, err := plan.DocumentAgentOf(ctx, h.store, wishID, tilasmID)
	if err != nil {
		return err
	}
	if agent != nil {
		if told, err := h.tellDocument(ctx, agent, tilasm, threads); told || err != nil {
			return err
		}
		if agent, err = store.Get[*planv1.Task](ctx, h.store, agent.GetId()); err != nil {
			return err
		}
	}
	var project *planv1.Project
	if ids := wish.GetProjectIds(); len(ids) > 0 {
		if project, err = store.Get[*planv1.Project](ctx, h.store, ids[0]); err != nil {
			return err
		}
	}
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil || !settings.QuestionWorkers {
		return err
	}
	provider, model := plan.QuestionAgent(wish, settings)
	if agent != nil {
		if why := h.staleDocument(agent, provider, model); why != "" {
			over, err := h.closeDocument(ctx, agent.GetId(), why)
			if err != nil || !over {
				return err // A worker took it meanwhile: it reads the discussions that wait for it.
			}
			agent = nil
		} else if plan.Ongoing(agent) {
			return h.retellDocument(ctx, agent.GetId(), tilasm)
		}
	}
	if agent != nil {
		_, err := h.continueTask(ctx, methodDocument, &planv1.TaskServiceContinueRequest{
			TaskId: agent.GetId(), Prompt: documentTurn(tilasm, threads),
		}, actorHarness, "Djinn")
		if err == nil {
			return nil
		}
		// Its session cannot go on (its budget is spent, its agent cannot resume one): another agent starts.
		h.Note(agent.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_LOG, Text: "not continued: " + err.Error()})
	}
	brief, err := plan.BuildBrief(ctx, h.store, h.home, wish.GetId())
	if err != nil {
		return err
	}
	title, prompt := documentPrompt(wish, tilasm, threads, brief.Moving)
	_, err = h.spawn(ctx, methodDocument, &planv1.TaskServiceSpawnRequest{
		WishId: wish.GetId(), ProjectId: project.GetId(), Title: title, Prompt: prompt, Provider: provider,
		Model: model, MaxBudgetUsd: settings.QuestionBudgetUSD,
	}, &planv1.Task{Role: planv1.TaskRole_TASK_ROLE_DOCUMENT, TilasmId: tilasm.GetId()})
	return err
}

// staleDocument says why the document agent t, which no worker runs, must not be continued nor resumed, "" when it
// may: its provider or its model is no longer the one its wish's question workers get (provider, model), or its
// provider is not available here.
func (h *Harness) staleDocument(t *planv1.Task, provider planv1.Provider, model string) string {
	switch {
	case t.GetProvider() != provider:
		return fmt.Sprintf("its agent %s is no longer the wish's, %s", short(t.GetProvider()), short(provider))
	case t.GetModel() != model:
		return fmt.Sprintf("its model %q is no longer the one its project's settings give, %q", t.GetModel(), model)
	}
	if _, ok := h.providers[t.GetProvider()]; !ok {
		return fmt.Sprintf("its agent %s is not available", short(t.GetProvider()))
	}
	return ""
}

// closeDocument closes, as Djinn, the document agent id that must not go on (why, staleDocument): one that waits to
// start or to resume is done, with an event that says why, so that Djinn neither resumes nor continues it. It tells
// whether the agent is over, closed now or ended already, so that a new agent takes its discussions; false while a
// worker runs it.
func (h *Harness) closeDocument(ctx context.Context, id, why string) (bool, error) {
	h.answering.Lock()
	defer h.answering.Unlock()
	h.sched.Lock()
	defer h.sched.Unlock()
	t, err := store.Get[*planv1.Task](ctx, h.store, id)
	if err != nil {
		return false, err
	}
	switch s := t.GetStatus(); {
	case h.running(id) || s == planv1.TaskStatus_TASK_STATUS_RUNNING || s == planv1.TaskStatus_TASK_STATUS_PAUSED:
		return false, nil
	case !plan.Ongoing(t):
		return true, nil
	}
	text := fmt.Sprintf("closed by Djinn, was %s: %s; a new document agent takes its discussions", short(t.GetStatus()), why)
	t.Status, t.Error, t.WaitReason, t.ResumeAfter, t.MachineWait, t.Continuing =
		planv1.TaskStatus_TASK_STATUS_DONE, "", "", nil, false, false
	if t.GetEndTime() == nil {
		t.EndTime = timestamppb.Now()
	}
	h.writeAlone(ctx, actorHarness, methodDocument, t, id, t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})
	h.wake()
	return true, nil
}

// closeStaleDocuments closes, as djinn up starts, each document agent left to resume or to start that must not go on
// (staleDocument): Recover calls it before the scheduler runs, so that none waits for a provider that no longer
// applies, nor holds the machine for the others. WakeDocuments then starts a new agent where discussions wait.
func (h *Harness) closeStaleDocuments(ctx context.Context, tasks []*planv1.Task) error {
	for _, t := range tasks {
		if !documentAgent(t) || !plan.Ongoing(t) {
			continue
		}
		wish, err := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId())
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return err
		}
		var project *planv1.Project
		if ids := wish.GetProjectIds(); len(ids) > 0 {
			if project, err = store.Get[*planv1.Project](ctx, h.store, ids[0]); err != nil {
				return err
			}
		}
		settings, err := plan.LoadSettings(h.home, project)
		if err != nil {
			log.Printf("djinn: task %s: read its project's settings: %v", t.GetCode(), err)
			continue
		}
		provider, model := plan.QuestionAgent(wish, settings)
		if why := h.staleDocument(t, provider, model); why != "" {
			if _, err := h.closeDocument(ctx, t.GetId(), why); err != nil {
				return err
			}
		}
	}
	return nil
}

// retellDocument tells the document agent id, which waits to be continued, every discussion of tilasm that waits for
// it now: the prompt its worker takes when it starts (lastPrompt) becomes that one, so that a message the developer
// wrote while it waited is answered in the same turn. An agent Djinn resumes otherwise (cut short by a restart or a
// limit) is told nothing: it ends, and is woken again on what it left (documentEnded).
func (h *Harness) retellDocument(ctx context.Context, id string, tilasm *planv1.Tilasm) error {
	h.answering.Lock()
	defer h.answering.Unlock()
	h.sched.Lock()
	defer h.sched.Unlock()
	t, err := store.Get[*planv1.Task](ctx, h.store, id)
	if err != nil || t.GetStatus() != planv1.TaskStatus_TASK_STATUS_RESUMING || !t.GetContinuing() || h.running(id) {
		return err
	}
	threads, err := plan.AwaitingAgent(ctx, h.store, tilasm.GetId())
	if err != nil || len(threads) == 0 {
		return err
	}
	h.writeAlone(ctx, actorHarness, methodDocument, nil, id, nil,
		Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, Text: documentTurn(tilasm, threads)})
	return nil
}

// tellDocument sends the document agent the threads while its worker runs, and tells whether it did. A worker that
// is ending is waited for, so that it is given a new turn rather than a second agent started beside it.
func (h *Harness) tellDocument(
	ctx context.Context, agent *planv1.Task, tilasm *planv1.Tilasm, threads []*planv1.TilasmThread,
) (bool, error) {
	h.mu.Lock()
	r := h.runs[agent.GetId()]
	var ending bool
	if r != nil {
		ending = r.final || r.stopping
	}
	h.mu.Unlock()
	if r == nil {
		return false, nil
	}
	if !ending {
		_, err := h.Send(ctx, planv1connect.TaskServiceSendProcedure, &planv1.TaskServiceSendRequest{
			TaskId: agent.GetId(), Text: documentTurn(tilasm, threads),
		})
		if err == nil {
			return true, nil
		}
		h.mu.Lock()
		ending = r.final || r.stopping
		h.mu.Unlock()
		if !ending {
			return true, nil // Its worker has not started yet: it reads the discussions that wait for it.
		}
	}
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case <-r.done:
	case <-wait.Done():
		return false, fmt.Errorf("task %s did not end: %w", agent.GetCode(), wait.Err())
	}
	return false, nil
}

// documentEnded wakes the document agent t again, once its run is over, when a discussion still waits for it whose
// developer's last message came after its last prompt: told while it ran, too late, or interrupted. One turn more at
// most for each such message, since the next turn's prompt comes after it: no loop.
func (h *Harness) documentEnded(r *run, t *planv1.Task) {
	if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		return // Failed or stopped: the next message of the developer wakes it.
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		select {
		case <-r.done:
		case <-h.ctx.Done():
			return
		}
		ctx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
		defer cancel()
		h.documenting.Lock()
		defer h.documenting.Unlock()
		told, err := lastPromptTime(ctx, h.store, t.GetId())
		if err != nil {
			log.Printf("djinn: task %s: read its last prompt: %v", t.GetCode(), err)
			return
		}
		threads, err := plan.AwaitingAgent(ctx, h.store, t.GetTilasmId())
		if err != nil {
			log.Printf("djinn: task %s: read its discussions: %v", t.GetCode(), err)
			return
		}
		var late []*planv1.TilasmThread
		for _, thread := range threads {
			msgs := thread.GetMessages()
			if msgs[len(msgs)-1].GetCreateTime().AsTime().After(told) {
				late = append(late, thread)
			}
		}
		if len(late) == 0 {
			return
		}
		if agent, err := plan.DocumentAgentOf(ctx, h.store, t.GetWishId(), t.GetTilasmId()); err != nil || plan.Ongoing(agent) {
			return // Woken already.
		}
		if err := h.wakeDocument(ctx, t.GetWishId(), t.GetTilasmId(), late); err != nil {
			log.Printf("djinn: task %s: wake it again: %v", t.GetCode(), err)
		}
	}()
}

// lastPromptTime is when the task was given its last prompt: its first, or the last turn Djinn continued it with.
func lastPromptTime(ctx context.Context, s *store.Store, taskID string) (time.Time, error) {
	var last *planv1.TaskEvent
	err := store.Latest(ctx, s, store.Where{"task_id": taskID}, func(ev *planv1.TaskEvent) bool {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT {
			last = ev
		}
		return last == nil
	})
	if err != nil || last == nil {
		return time.Time{}, err
	}
	return last.GetCreateTime().AsTime(), nil
}

// threadsText lists the discussions to answer, each with what the developer opened it for and their last message,
// as they wrote it.
func threadsText(b *strings.Builder, threads []*planv1.TilasmThread) {
	for _, thread := range threads {
		what := "a comment: reply in it"
		if plan.ThreadKind(thread) == planv1.TilasmThreadKind_TILASM_THREAD_KIND_REQUEST {
			what = "a request for a change: answer with a proposal linked to it"
		}
		fmt.Fprintf(b, "- %s: %s.", thread.GetId(), what)
		if msgs := thread.GetMessages(); len(msgs) > 0 {
			if text := strings.TrimSpace(msgs[len(msgs)-1].GetText()); text != "" {
				b.WriteString(" The developer wrote:\n\n" + text + "\n")
			}
		}
		b.WriteString("\n")
	}
}

// documentAccess is how a document agent works within its access: the rules its commands must follow to be allowed.
// Every turn carries them, so an agent continued in an older session learns them too.
func documentAccess() string {
	return "## How to work within your access\n\n" +
		"You read files, and run djinn's commands to read the wish, discuss the document and tell the lead: nothing " +
		"else.\n\n" +
		"- Run each `djinn …` command alone, in a Bash call of its own: no pipe, no `;`, no `&&`, no redirection, no " +
		"`$(…)`, no `cd` before it. A call that joins anything to a djinn command is refused whole.\n" +
		"- Write each value of `--text`, `--replacement`, `--note` on ONE line between straight double quotes, with " +
		"`\\n` for each line break (`\\n\\n` between paragraphs, `\\t` for a tab, no other backslash), and give " +
		"the command `--escapes` always. Never put a real line break inside a command: a line that starts with `#` " +
		"is read as a comment, and the call is refused. Put no double quote at all inside a value, not even " +
		"escaped: quote with « » or ’ instead, as French typography does, and write an apostrophe as ’. No " +
		"backtick, no `$` inside a value either. For instance, a heading and a list: `djinn doc propose L01 --wish " +
		"<wish> --thread <id> --escapes --replacement \"## Options comparées\\n\\n- Option A : plus rapide\\n- Option " +
		"B : plus sûre\" --note \"Ajoute la comparaison\"`.\n" +
		"- A refused call means that this form of the command is refused: run the djinn command again, alone and " +
		"plain, its values rewritten by the rule above.\n\n"
}

// documentTurn is what the document agent is told when the developer writes again: the discussions to answer.
func documentTurn(tilasm *planv1.Tilasm, threads []*planv1.TilasmThread) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The developer wrote in discussions of %s:\n\n", tilasm.GetCode())
	threadsText(&b, threads)
	fmt.Fprintf(&b, "\nRead each one (djinn doc context %s --wish %s --thread <id>), answer as your rules say, then end "+
		"your turn.\n\n", tilasm.GetCode(), tilasm.GetWishId())
	b.WriteString(documentAccess())
	return b.String()
}

// documentPrompt is the title and the first prompt of the document agent of tilasm, woken on threads, in a wish whose
// brief's moving part is brief.
func documentPrompt(wish *planv1.Wish, tilasm *planv1.Tilasm, threads []*planv1.TilasmThread, brief string) (title, prompt string) {
	code, id := tilasm.GetCode(), wish.GetId()
	doc := fmt.Sprintf("djinn doc %%s %s --wish %s", code, id)
	var b strings.Builder
	fmt.Fprintf(&b, "You are the document agent of support %s %q of wish %s (%s). The developer writes to you in the "+
		"discussions of this Markdown document, in Djinn's window; you answer there. You edit no file and run no gate: "+
		"you read, reply and propose changes, with Djinn's command line. The developer accepts or rejects each "+
		"proposal; you never do. Djinn signs your messages and proposals \"document\".\n\n",
		code, tilasm.GetTitle(), id, wish.GetTitle())
	b.WriteString("## Now\n\n")
	threadsText(&b, threads)
	fmt.Fprintf(&b, "\nAnswer too any other open discussion in which nothing answered the developer's last message yet, "+
		"neither a message by someone else nor a proposal linked to it after it (`%s`).\n\n",
		fmt.Sprintf(doc, "discussions"))
	fmt.Fprintf(&b, "## How to answer\n\n"+
		"1. Read each discussion, its passage and the document around it: `%[1]s --thread <id>`. The whole "+
		"document: `%[2]s`.\n"+
		"2. A comment is a conversation: reply in its thread, `djinn doc reply <id> --escapes --text \"…\"`. Answer the "+
		"developer's question, or give your view, in 1 to 3 sentences, in the developer's language. Do not propose a "+
		"change unless the developer asks for one; you may offer to. Never answer a comment with a proposal alone.\n"+
		"3. A request asks for a change: propose it directly, `djinn doc propose %[3]s --wish %[4]s --thread <id> "+
		"--escapes --replacement \"…\"` (`--delete` deletes the passage). The replacement is Markdown, markup included, in place "+
		"of the passage's source that the context shows; a discussion with an empty quote marks a place, and the "+
		"replacement is inserted there. `--note \"…\"` is optional: one short sentence at most, 15 words or fewer, no "+
		"justification. Write no reply message, unless the request is ambiguous and you must ask a question instead "+
		"of proposing.\n"+
		"4. Propose targeted changes, a passage at a time, never the document rewritten. Keep to what the developer "+
		"asked.\n"+
		"5. Rewording and questions about a sentence stay in the document. Tell the lead only when a decision affects "+
		"the rest of the wish, or when the developer asks you to: `djinn wish tell %[4]s \"%[3]s: …\"`, one line.\n"+
		"6. Answer every developer message you were given, then end your turn: Djinn wakes you when the developer "+
		"writes again, in this same conversation.\n\n",
		fmt.Sprintf(doc, "context"), fmt.Sprintf(doc, "get"), code, id)
	b.WriteString(documentAccess())
	b.WriteString("## Where the wish stands\n\n" + brief)
	return code + ": document agent", b.String()
}
