package harness

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/testx"
)

// recordingFake is the fake agent, keeping the spec of each worker it starts.
type recordingFake struct {
	Fake
	mu    sync.Mutex
	specs []Spec
}

func (f *recordingFake) Start(ctx context.Context, spec Spec) (Worker, error) {
	f.mu.Lock()
	f.specs = append(f.specs, spec)
	f.mu.Unlock()
	return f.Fake.Start(ctx, spec)
}

func (f *recordingFake) all() []Spec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.specs)
}

// docs is the tilasm service of a test's djinn, on a Markdown tilasm of the wish.
type docs struct {
	planv1connect.TilasmServiceClient
	wishID string
	tilasm *planv1.Tilasm
}

func (e *env) markdownDoc(t *testing.T, wishID string) docs {
	t.Helper()
	c := planv1connect.NewTilasmServiceClient(e.srv.Client(), e.srv.URL)
	res, err := c.Create(t.Context(), connect.NewRequest(&planv1.TilasmServiceCreateRequest{
		Wish: wishID, Title: "The store", Markdown: "# The store\n\nWe keep the tasks in SQLite, one file.\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	return docs{c, wishID, res.Msg.GetTilasm()}
}

func (d docs) comment(t *testing.T, author, text string, kind planv1.TilasmThreadKind) *planv1.TilasmThread {
	t.Helper()
	res, err := d.Comment(t.Context(), connect.NewRequest(&planv1.TilasmServiceCommentRequest{
		Tilasm: &planv1.TilasmRef{Ref: &planv1.TilasmRef_Id{Id: d.tilasm.GetId()}}, Quote: "SQLite", Text: text,
		Author: author, Kind: kind,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetThread()
}

func (d docs) reply(t *testing.T, thread, author, text string) {
	t.Helper()
	if _, err := d.Reply(t.Context(), connect.NewRequest(&planv1.TilasmServiceReplyRequest{
		Thread: thread, Author: author, Text: text,
	})); err != nil {
		t.Fatal(err)
	}
}

// thread is the discussion as Discussions reads it, agent_pending computed.
func (d docs) thread(t *testing.T, id string) *planv1.TilasmThread {
	t.Helper()
	res, err := d.Discussions(t.Context(), connect.NewRequest(&planv1.TilasmServiceDiscussionsRequest{
		Tilasm: &planv1.TilasmRef{Ref: &planv1.TilasmRef_Id{Id: d.tilasm.GetId()}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, thread := range res.Msg.GetThreads() {
		if thread.GetId() == id {
			return thread
		}
	}
	t.Fatalf("no discussion %s", id)
	return nil
}

// settled waits until no worker runs the task, it is done, and its events say ok.
func (e *env) settled(t *testing.T, id string, ok func([]*planv1.TaskEvent) bool) []*planv1.TaskEvent {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		task := e.get(t, id)
		e.h.mu.Lock()
		running := e.h.runs[id] != nil
		e.h.mu.Unlock()
		if !running && task.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE {
			if events := e.events(t, id); ok(events) {
				return events
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s stays %v", task.GetCode(), task.GetStatus())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// events are the task's events so far.
func (e *env) events(t *testing.T, id string) []*planv1.TaskEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	return e.watch(ctx, t, id, 0)
}

// workerUp waits until the task's worker takes messages.
func (e *env) workerUp(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		e.h.mu.Lock()
		r := e.h.runs[id]
		up := r != nil && r.worker != nil
		e.h.mu.Unlock()
		if up {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s: no worker", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func countText(events []*planv1.TaskEvent, kind planv1.TaskEventKind, text string) int {
	n := 0
	for _, ev := range events {
		if ev.GetKind() == kind && strings.Contains(ev.GetText(), text) {
			n++
		}
	}
	return n
}

// TestDocumentAgent: a developer's request starts one document agent on the tilasm, light, with djinn's commands to
// discuss it and its author document; a reply while it runs is sent to it; a message it left unanswered when it ended
// gives it one more turn of its session, and so does the developer's next message: the same task each time. What the
// agent, the lead or a worker write wakes nothing. agent_pending follows.
func TestDocumentAgent(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	fake := &recordingFake{}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_FAKE] = fake
	lim := &limit{slots: 1}
	e := upWith(t, t.TempDir(), providers, WithQuestionWorkers(), WithCapacity(lim.capacity))
	dir := fakeProject(t, "question_model: \"cheap\"\nquestion_budget_usd: 0.5\n")
	wishID, _ := e.wish(t, dir)
	busy := e.spawn(t, wishID, "wait") // It takes the one slot.
	d := e.markdownDoc(t, wishID)

	thread := d.comment(t, "developer", "wait", planv1.TilasmThreadKind_TILASM_THREAD_KIND_REQUEST)
	if thread.GetKind() != planv1.TilasmThreadKind_TILASM_THREAD_KIND_REQUEST {
		t.Errorf("thread kind %v, want a request", thread.GetKind())
	}
	agents := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)
	if len(agents) != 1 {
		t.Fatalf("%d document agents, want 1", len(agents))
	}
	a := agents[0]
	if a.GetTilasmId() != d.tilasm.GetId() || a.GetTitle() != "L01: document agent" ||
		a.GetAccess() != planv1.TaskAccess_TASK_ACCESS_DJINN || a.GetWorktree() != "" || a.GetModel() != "cheap" ||
		a.GetMaxBudgetUsd() != 0.5 || a.GetProvider() != planv1.Provider_PROVIDER_FAKE {
		t.Errorf("document agent: %v", a)
	}
	if a.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("document agent %s, %s: it takes no slot", a.GetStatus(), a.GetWaitReason())
	}
	prompt, err := firstPrompt(e.db, a.GetId())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`You are the document agent of support L01 "The store" of wish ` + wishID,
		"- " + thread.GetId() + ": a request for a change: answer with a proposal linked to it. The developer wrote:\n\nwait\n",
		"djinn doc context L01 --wish " + wishID + " --thread <id>", "djinn doc propose L01 --wish " + wishID + " --thread <id>",
		"djinn doc discussions L01 --wish " + wishID, "djinn wish tell " + wishID, "you never do",
		"## Where the wish stands",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt misses %q:\n%s", want, prompt)
		}
	}
	t.Logf("a document agent's prompt: %d bytes, about %d tokens", len(prompt), len(prompt)/4)
	if !d.thread(t, thread.GetId()).GetAgentPending() {
		t.Error("agent_pending false while the agent works on the request")
	}
	e.workerUp(t, a.GetId())
	specs := fake.all()
	spec := specs[len(specs)-1]
	if spec.TaskID != a.GetId() || !slices.Contains(spec.Env, "DJINN_AUTHOR=document") || spec.ReadOnly ||
		spec.Permissions.GetEdit() || !slices.Contains(spec.Permissions.GetCommands(), "djinn doc propose") ||
		!slices.Contains(spec.Permissions.GetCommands(), "djinn wish tell") ||
		slices.Contains(spec.Permissions.GetCommands(), "djinn task spawn") {
		t.Errorf("document agent's spec: env %v, permissions %v", spec.Env, spec.Permissions)
	}

	// While it runs, the developer's reply goes to it: no second agent.
	d.reply(t, thread.GetId(), "developer", "go on")
	if n := len(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)); n != 1 {
		t.Fatalf("%d document agents, want 1", n)
	}
	// It ends without answering that reply: Djinn gives it one more turn of its session, once.
	events := e.settled(t, a.GetId(), func(events []*planv1.TaskEvent) bool {
		return countText(events, planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "continued by Djinn") == 1
	})
	if countText(events, planv1.TaskEventKind_TASK_EVENT_KIND_MESSAGE, "The developer wrote in discussions of L01") != 1 {
		t.Errorf("the running agent was not sent the reply: %v", eventKinds(events))
	}
	if countText(events, planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, "The developer wrote in discussions of L01") != 1 ||
		countText(events, planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "resumed") == 0 {
		t.Errorf("no turn of its session on the reply it left: %v", eventKinds(events))
	}

	// Its answer, and what the lead or a worker write, wake nothing.
	d.reply(t, thread.GetId(), plan.DocumentAuthor, "Proposed: Postgres.")
	if d.thread(t, thread.GetId()).GetAgentPending() {
		t.Error("agent_pending true once the agent answered")
	}
	d.reply(t, thread.GetId(), "", "The lead agrees.")
	d.comment(t, "W03", "A worker's note.", planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED)
	d.comment(t, plan.DocumentAuthor, "The agent's own note.", planv1.TilasmThreadKind_TILASM_THREAD_KIND_COMMENT)
	if got := e.events(t, a.GetId()); len(got) != len(events) {
		t.Errorf("messages by others woke the agent: %d events, then %d", len(events), len(got))
	}

	// Once it ended, the developer's next message is a new turn of the same task.
	other := d.comment(t, "developer", "Why one file?", planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED)
	if other.GetKind() != planv1.TilasmThreadKind_TILASM_THREAD_KIND_COMMENT {
		t.Errorf("a comment's kind %v", other.GetKind())
	}
	events = e.settled(t, a.GetId(), func(events []*planv1.TaskEvent) bool {
		return countText(events, planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "continued by Djinn") == 2
	})
	if n := len(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)); n != 1 {
		t.Errorf("%d document agents, want 1: the same one, continued", n)
	}
	if countText(events, planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, "- "+other.GetId()+": a comment: reply in it.") != 1 {
		t.Errorf("the new turn does not name the comment: %v", events)
	}
	if e.get(t, busy.GetId()).GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Error("the busy task should still run")
	}
}

// TestDocumentAgentOff: the project's settings, or a harness without question workers, start no document agent; nor
// does a discussion of an HTML tilasm, which Djinn refuses.
func TestDocumentAgentOff(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		opts     []Option
		settings string
	}{
		"settings": {opts: []Option{WithQuestionWorkers()}, settings: "question_workers: false\n"},
		"harness":  {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := up(t, t.TempDir(), c.opts...)
			wishID, _ := e.wish(t, fakeProject(t, c.settings))
			d := e.markdownDoc(t, wishID)
			thread := d.comment(t, "developer", "Why?", planv1.TilasmThreadKind_TILASM_THREAD_KIND_REQUEST)
			if n := len(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)); n != 0 {
				t.Errorf("%d document agents, want none", n)
			}
			if d.thread(t, thread.GetId()).GetAgentPending() {
				t.Error("agent_pending without an agent")
			}
		})
	}
	t.Run("html", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		e := up(t, home, WithQuestionWorkers())
		wishID, _ := e.wish(t, fakeProject(t, ""))
		tilasm := htmlTilasm(t, e, home, wishID)
		c := planv1connect.NewTilasmServiceClient(e.srv.Client(), e.srv.URL)
		_, err := c.Comment(t.Context(), connect.NewRequest(&planv1.TilasmServiceCommentRequest{
			Tilasm: &planv1.TilasmRef{Ref: &planv1.TilasmRef_Id{Id: tilasm.GetId()}}, Quote: "tasks", Text: "Why?",
			Author: "developer",
		}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("a discussion of an HTML tilasm: %v, want failed precondition", err)
		}
		// Even told of one, Djinn starts nothing on it.
		e.h.Discussed(t.Context(), &planv1.TilasmThread{
			Id: "0199e5a0-0000-7000-8000-000000000001", WishId: wishID, TilasmId: tilasm.GetId(),
			Messages: []*planv1.TilasmMessage{{Author: "developer", Text: "Why?"}},
		})
		if n := len(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)); n != 0 {
			t.Errorf("%d document agents on an HTML tilasm, want none", n)
		}
	})
}

// htmlTilasm puts an HTML page as a tilasm of the wish.
func htmlTilasm(t *testing.T, e *env, home, wishID string) *planv1.Tilasm {
	t.Helper()
	src := t.TempDir()
	writeFile(t, src, "index.html", "<html><body><p>A wish holds tasks.</p></body></html>")
	tilasms := &plan.Tilasms{Store: e.db, Home: home}
	put, err := tilasms.Put(t.Context(), connect.NewRequest(&planv1.TilasmServicePutRequest{Path: src, Wish: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	return put.Msg.GetTilasm()
}
