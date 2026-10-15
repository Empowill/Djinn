package harness

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

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

// wakeDocument has the tilasm's document agent answer the developer in threads: told while it runs, nothing while it
// waits to start or resume (it reads every discussion that waits for it), a new turn of its session once it ended,
// else a new agent. Nothing for a granted wish, an HTML tilasm, or a project whose settings turn question workers
// off. The caller holds h.documenting.
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
		if plan.Ongoing(agent) {
			return nil // It waits to start or to resume: it reads the discussions that wait for it then.
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
	provider, model := plan.QuestionAgent(wish, settings)
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

// documentEnded wakes the document agent t again, once its run is over, when the developer wrote in a discussion
// after its turn started and it left that message unanswered: told too late, or interrupted. One turn more at most
// for each such message, since the next turn starts after it: no loop.
func (h *Harness) documentEnded(r *run, t *planv1.Task) {
	if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		return // Failed or stopped: the next message of the developer wakes it.
	}
	started := t.GetStartTime().AsTime()
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
		threads, err := store.List[*planv1.TilasmThread](ctx, h.store, store.Where{"tilasm_id": t.GetTilasmId()})
		if err != nil {
			log.Printf("djinn: task %s: read its discussions: %v", t.GetCode(), err)
			return
		}
		var late []*planv1.TilasmThread
		for _, thread := range threads {
			msgs := thread.GetMessages()
			if plan.AwaitsAgent(thread) && msgs[len(msgs)-1].GetCreateTime().AsTime().After(started) {
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

// documentTurn is what the document agent is told when the developer writes again: the discussions to answer.
func documentTurn(tilasm *planv1.Tilasm, threads []*planv1.TilasmThread) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The developer wrote in discussions of %s:\n\n", tilasm.GetCode())
	threadsText(&b, threads)
	fmt.Fprintf(&b, "\nRead each one (djinn doc context %s --wish %s --thread <id>), answer as your rules say, then end "+
		"your turn.", tilasm.GetCode(), tilasm.GetWishId())
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
	fmt.Fprintf(&b, "\nAnswer any other open discussion whose last message is the developer's too (`%s`).\n\n",
		fmt.Sprintf(doc, "discussions"))
	fmt.Fprintf(&b, "## How to answer\n\n"+
		"1. Read each discussion, its passage and the document around it: `%[1]s --thread <id>`. The whole "+
		"document: `%[2]s`.\n"+
		"2. A request: propose the change it asks for, `djinn doc propose %[3]s --wish %[4]s --thread <id> "+
		"--replacement \"…\" --note \"…\"` (`--delete` deletes the passage). The replacement is Markdown, markup "+
		"included, in place of the passage's source that the context shows; a discussion with an empty quote marks a "+
		"place, and the replacement is inserted there. Add a short message only if the proposal needs one: `djinn "+
		"doc reply <id> --text \"…\"`.\n"+
		"3. A comment: reply in it, `djinn doc reply <id> --text \"…\"`. Propose a change only when the discussion "+
		"calls for one, with --thread <id>.\n"+
		"4. Propose targeted changes, a passage at a time, never the document rewritten. Keep to what the developer "+
		"asked.\n"+
		"5. Rewording and questions about a sentence stay in the document. Tell the lead only when a decision affects "+
		"the rest of the wish, or when the developer asks you to: `djinn wish tell %[4]s \"%[3]s: …\"`, one line.\n"+
		"6. Answer every developer message you were given, then end your turn: Djinn wakes you when the developer "+
		"writes again, in this same conversation.\n\n",
		fmt.Sprintf(doc, "context"), fmt.Sprintf(doc, "get"), code, id)
	b.WriteString("## How to work within your access\n\n" +
		"You read files, and run djinn's commands to read the wish, discuss the document and tell the lead: nothing " +
		"else.\n\n" +
		"- Run each `djinn …` command alone, in a Bash call of its own: no pipe, no `;`, no `&&`, no redirection, no " +
		"`$(…)`, no `cd` before it. A call that joins anything to a djinn command is refused whole.\n" +
		"- Write the values of `--text`, `--replacement`, `--note` between double quotes, with no backtick nor `$` " +
		"inside; write an inner double quote `\\\"`.\n" +
		"- A refused call means that this form of the command is refused: run the djinn command again, alone and " +
		"plain.\n\n")
	b.WriteString("## Where the wish stands\n\n" + brief)
	return code + ": document agent", b.String()
}
