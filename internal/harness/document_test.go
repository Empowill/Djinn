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
	"github.com/empowill/djinn/internal/machine"
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

// TestDocumentAgentSkipsMemory: on a machine whose free memory holds no worker, a task waits for it, but a
// developer's request starts its document agent at once; under critical memory pressure, the next one waits.
func TestDocumentAgentSkipsMemory(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	lim := &limit{slots: 4}
	free := func() uint64 { return 1 * machine.GiB } // Less than a worker's 1 GiB and the 512 MiB kept.
	e := up(t, t.TempDir(), WithQuestionWorkers(), WithCapacity(lim.capacity), WithMemory(free, machine.DefaultPolicy()),
		WithTick(20*time.Millisecond))
	wishID, _ := e.wish(t, fakeProject(t, ""))
	waiting := e.mustSpawn(t, wishID, "Heavy", "text ok", nil)
	if waiting.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || !waiting.GetMachineWait() {
		t.Fatalf("the task should wait for the memory: %v", waiting)
	}
	d := e.markdownDoc(t, wishID)
	d.comment(t, "developer", "wait", planv1.TilasmThreadKind_TILASM_THREAD_KIND_REQUEST)
	agents := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)
	if len(agents) != 1 || agents[0].GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("the document agent should run beside the waiting task: %v", agents)
	}
	if got := e.get(t, waiting.GetId()); got.GetStartTime() != nil {
		t.Errorf("the task started on a tight machine: %v", got)
	}

	lim.set(4, "the system reports critical memory pressure")
	other := e.markdownDoc(t, wishID)
	other.comment(t, "developer", "wait", planv1.TilasmThreadKind_TILASM_THREAD_KIND_REQUEST)
	i := slices.IndexFunc(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT), func(a *planv1.Task) bool {
		return a.GetTilasmId() == other.tilasm.GetId()
	})
	if i < 0 {
		t.Fatal("no document agent on the second document")
	}
	if a := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)[i]; a.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING ||
		a.GetWaitReason() != "the machine is under pressure: the system reports critical memory pressure" {
		t.Errorf("under critical pressure: %v", a)
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

// TestDocumentAgentKeepsItsCommands: a document agent given a new turn of its session, by the developer's message once
// it ended or by djinn's restart while it ran, is started again with its own commands (documentOnly), not a question
// worker's: it may still reply and propose.
func TestDocumentAgentKeepsItsCommands(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	documentSpec := func(t *testing.T, spec Spec) {
		t.Helper()
		cmds := spec.Permissions.GetCommands()
		if spec.ReadOnly || spec.Permissions.GetEdit() || !slices.Contains(cmds, "djinn doc reply") ||
			!slices.Contains(cmds, "djinn doc propose") || slices.Contains(cmds, "djinn task spawn") {
			t.Errorf("document agent's spec (resume %q): read-only %v, permissions %v", spec.Resume, spec.ReadOnly, spec.Permissions)
		}
	}
	of := func(fake *recordingFake, id string) []Spec {
		var out []Spec
		for _, s := range fake.all() {
			if s.TaskID == id {
				out = append(out, s)
			}
		}
		return out
	}

	t.Run("continued", func(t *testing.T) {
		t.Parallel()
		fake := &recordingFake{}
		providers := testProviders()
		providers[planv1.Provider_PROVIDER_FAKE] = fake
		e := upWith(t, t.TempDir(), providers, WithQuestionWorkers())
		wishID, _ := e.wish(t, fakeProject(t, ""))
		d := e.markdownDoc(t, wishID)
		d.comment(t, "developer", "Why?", planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED)
		agents := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)
		if len(agents) != 1 {
			t.Fatalf("%d document agents, want 1", len(agents))
		}
		a := agents[0]
		// Once it ended, the developer's next message gives it a new turn of its session.
		e.settled(t, a.GetId(), func([]*planv1.TaskEvent) bool { return true })
		d.comment(t, "developer", "And now?", planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED)
		e.settled(t, a.GetId(), func(events []*planv1.TaskEvent) bool {
			return countText(events, planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "continued by Djinn") == 1
		})
		specs := of(fake, a.GetId())
		if len(specs) < 2 {
			t.Fatalf("%d workers started for the agent, want its launch and its new turn", len(specs))
		}
		for _, spec := range specs {
			documentSpec(t, spec)
		}
		if specs[len(specs)-1].Resume == "" {
			t.Error("the last turn does not resume the agent's session")
		}
	})

	t.Run("restarted", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		fake := &recordingFake{}
		providers := testProviders()
		providers[planv1.Provider_PROVIDER_FAKE] = fake
		e := upWith(t, home, providers, WithQuestionWorkers())
		wishID, _ := e.wish(t, fakeProject(t, ""))
		d := e.markdownDoc(t, wishID)
		d.comment(t, "developer", "wait", planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED)
		agents := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)
		if len(agents) != 1 {
			t.Fatalf("%d document agents, want 1", len(agents))
		}
		a := agents[0]
		e.workerUp(t, a.GetId())
		e.down() // It is cut short while it waits.

		again := &recordingFake{}
		providers = testProviders()
		providers[planv1.Provider_PROVIDER_FAKE] = again
		e = upWith(t, home, providers, WithQuestionWorkers())
		e.settled(t, a.GetId(), func(events []*planv1.TaskEvent) bool {
			return countText(events, planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "resumed "+byRestart) == 1
		})
		specs := of(again, a.GetId())
		if len(specs) == 0 {
			t.Fatal("the interrupted agent was not resumed")
		}
		if specs[0].Resume == "" {
			t.Errorf("the restart does not resume the agent's session: %+v", specs[0])
		}
		documentSpec(t, specs[0])
	})
}

// TestDocumentPromptRules: the document agent answers a comment in its thread, briefly, and a request by a proposal
// alone; its values hold no double quote, even escaped, which an agent's permission check refuses.
func TestDocumentPromptRules(t *testing.T) {
	t.Parallel()
	wish := &planv1.Wish{Id: "0199e5a0-0000-7000-8000-0000000000aa", Title: "Store"}
	tilasm := &planv1.Tilasm{Code: "L01", Title: "The store", WishId: wish.GetId()}
	_, prompt := documentPrompt(wish, tilasm, nil, "")
	for _, want := range []string{
		"A comment is a conversation: reply in its thread", "in 1 to 3 sentences, in the developer's language",
		"Do not propose a change unless the developer asks for one; you may offer to",
		"Never answer a comment with a proposal alone", "A request asks for a change: propose it directly",
		"one short sentence at most, 15 words or fewer", "Write no reply message, unless the request is ambiguous",
		"Put no double quote at all inside a value, not even escaped: quote with « » or ’ instead",
		"on ONE line between straight double quotes, with `\\n` for each line break",
		"give the command `--escapes` always", "Never put a real line break inside a command",
		`--escapes --replacement "## Options comparées\n\n- Option A : plus rapide\n- Option B : plus sûre"`,
		"djinn doc reply <id> --escapes --text", "--thread <id> --escapes --replacement",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt misses %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, `\"`) {
		t.Errorf("the prompt still shows an escaped double quote:\n%s", prompt)
	}
	// A continued agent, started before these rules, is told them again on every later turn.
	turn := documentTurn(tilasm, nil)
	for _, want := range []string{
		"## How to work within your access", "on ONE line between straight double quotes, with `\\n` for each line break",
		"give the command `--escapes` always", "Never put a real line break inside a command",
		"Put no double quote at all inside a value", `--escapes --replacement "## Options comparées\n`,
	} {
		if !strings.Contains(turn, want) {
			t.Errorf("a later turn misses %q:\n%s", want, turn)
		}
	}
}

// TestDocumentWake: a discussion left unanswered once its agent ended is woken again, without a new message, from
// the window (Wake), and by djinn's start (WakeDocuments) when it was left while question workers were off.
func TestDocumentWake(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	t.Run("window", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir(), WithQuestionWorkers())
		wishID, _ := e.wish(t, fakeProject(t, ""))
		d := e.markdownDoc(t, wishID)
		thread := d.comment(t, "developer", "Why?", planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED)
		agents := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)
		if len(agents) != 1 {
			t.Fatalf("%d document agents, want 1", len(agents))
		}
		a := agents[0]
		e.settled(t, a.GetId(), func([]*planv1.TaskEvent) bool { return true })
		if got := d.thread(t, thread.GetId()); got.GetAgentPending() || !plan.AwaitsAgent(got, nil) {
			t.Fatalf("the unanswered thread: pending %v, awaits %v", got.GetAgentPending(), plan.AwaitsAgent(got, nil))
		}
		res, err := d.Wake(t.Context(), connect.NewRequest(&planv1.TilasmServiceWakeRequest{Thread: thread.GetId()}))
		if err != nil {
			t.Fatal(err)
		}
		if n := len(res.Msg.GetThread().GetMessages()); n != 1 {
			t.Errorf("Wake added a message: %d messages", n)
		}
		e.settled(t, a.GetId(), func(events []*planv1.TaskEvent) bool {
			return countText(events, planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "continued by Djinn") == 1
		})
		if n := len(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)); n != 1 {
			t.Errorf("%d document agents, want the same one, continued", n)
		}
		// A discussion the agent answered waits for nothing.
		d.reply(t, thread.GetId(), plan.DocumentAuthor, "Because.")
		_, err = d.Wake(t.Context(), connect.NewRequest(&planv1.TilasmServiceWakeRequest{Thread: thread.GetId()}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("Wake on an answered discussion: %v, want failed precondition", err)
		}
	})
	t.Run("start", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		e := up(t, home)
		wishID, _ := e.wish(t, fakeProject(t, ""))
		d := e.markdownDoc(t, wishID)
		d.comment(t, "developer", "Why?", planv1.TilasmThreadKind_TILASM_THREAD_KIND_REQUEST)
		d.comment(t, plan.DocumentAuthor, "A note of the agent.", planv1.TilasmThreadKind_TILASM_THREAD_KIND_COMMENT)
		if n := len(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)); n != 0 {
			t.Fatalf("%d document agents without question workers", n)
		}
		e.down()

		e = up(t, home, WithQuestionWorkers())
		e.h.WakeDocuments(t.Context())
		deadline := time.Now().Add(10 * time.Second)
		for {
			agents := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)
			if len(agents) == 1 {
				break
			}
			if len(agents) > 1 || time.Now().After(deadline) {
				t.Fatalf("%d document agents after the start, want 1", len(agents))
			}
			time.Sleep(10 * time.Millisecond)
		}
		// A second start while it works wakes no other one.
		e.h.WakeDocuments(t.Context())
		time.Sleep(50 * time.Millisecond)
		if n := len(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)); n != 1 {
			t.Errorf("%d document agents, want 1", n)
		}
	})
}

// TestDocumentWakeAtStartEnds: a document agent djinn's start gives a new turn of its session ends done, and the
// developer's next message gives it another.
func TestDocumentWakeAtStartEnds(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	e := up(t, home, WithQuestionWorkers())
	wishID, _ := e.wish(t, fakeProject(t, ""))
	d := e.markdownDoc(t, wishID)
	d.comment(t, "developer", "Why?", planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED)
	agents := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)
	if len(agents) != 1 {
		t.Fatalf("%d document agents, want 1", len(agents))
	}
	a := agents[0]
	e.settled(t, a.GetId(), func([]*planv1.TaskEvent) bool { return true })
	e.down()

	e = up(t, home, WithQuestionWorkers())
	e.h.WakeDocuments(t.Context())
	e.settled(t, a.GetId(), func(events []*planv1.TaskEvent) bool {
		return countText(events, planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "continued by Djinn") == 1
	})
	d = docs{planv1connect.NewTilasmServiceClient(e.srv.Client(), e.srv.URL), wishID, d.tilasm}
	d.comment(t, "developer", "And now?", planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED)
	e.settled(t, a.GetId(), func(events []*planv1.TaskEvent) bool {
		return countText(events, planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "continued by Djinn") == 2
	})
}

// TestDocumentAnsweredByProposal: a request the agent answered with a proposal linked to it, made after the
// developer's message and without a reply, waits for no answer: djinn's start wakes no agent on it, and Wake refuses
// it. A message of the developer after that proposal waits again.
func TestDocumentAnsweredByProposal(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	e := up(t, home)
	wishID, _ := e.wish(t, fakeProject(t, ""))
	d := e.markdownDoc(t, wishID)
	thread := d.comment(t, "developer", "Say Postgres.", planv1.TilasmThreadKind_TILASM_THREAD_KIND_REQUEST)
	if _, err := d.Propose(t.Context(), connect.NewRequest(&planv1.TilasmServiceProposeRequest{
		Tilasm: &planv1.TilasmRef{Ref: &planv1.TilasmRef_Id{Id: d.tilasm.GetId()}}, Thread: thread.GetId(),
		Quote: "SQLite", Replacement: "Postgres", Author: plan.DocumentAuthor,
	})); err != nil {
		t.Fatal(err)
	}
	e.down()

	e = up(t, home, WithQuestionWorkers())
	d = docs{planv1connect.NewTilasmServiceClient(e.srv.Client(), e.srv.URL), wishID, d.tilasm}
	e.h.WakeDocuments(t.Context())
	time.Sleep(100 * time.Millisecond)
	if n := len(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)); n != 0 {
		t.Errorf("%d document agents woken on a request answered by a proposal, want none", n)
	}
	_, err := d.Wake(t.Context(), connect.NewRequest(&planv1.TilasmServiceWakeRequest{Thread: thread.GetId()}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("Wake on a request answered by a proposal: %v, want failed precondition", err)
	}
	d.reply(t, thread.GetId(), "developer", "Shorter, please.")
	if n := len(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)); n != 1 {
		t.Errorf("%d document agents once the developer wrote again, want 1", n)
	}
}

// TestDocumentStaleAgent: a document agent whose model is no longer the one its project's settings give is not
// continued: the developer's next message starts a new agent; and one djinn's restart cut short is closed by Djinn
// rather than resumed, and a new agent takes the discussion that waits.
func TestDocumentStaleAgent(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	t.Run("ended", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir(), WithQuestionWorkers())
		dir := fakeProject(t, "question_model: \"cheap\"\n")
		wishID, _ := e.wish(t, dir)
		d := e.markdownDoc(t, wishID)
		d.comment(t, "developer", "Why?", planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED)
		agents := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)
		if len(agents) != 1 {
			t.Fatalf("%d document agents, want 1", len(agents))
		}
		old := agents[0]
		e.settled(t, old.GetId(), func([]*planv1.TaskEvent) bool { return true })
		writeFile(t, dir, plan.SettingsFile, "provider: PROVIDER_FAKE\nquestion_model: \"other\"\n")
		d.comment(t, "developer", "And now?", planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED)
		agents = e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)
		if len(agents) != 2 {
			t.Fatalf("%d document agents, want the old one and a new one", len(agents))
		}
		for _, a := range agents {
			if a.GetId() != old.GetId() && a.GetModel() != "other" {
				t.Errorf("the new agent's model %q, want other", a.GetModel())
			}
		}
		if n := countText(e.events(t, old.GetId()), planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "continued"); n != 0 {
			t.Error("the stale agent was continued")
		}
	})
	t.Run("restarted", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		e := up(t, home, WithQuestionWorkers())
		dir := fakeProject(t, "question_model: \"cheap\"\n")
		wishID, _ := e.wish(t, dir)
		d := e.markdownDoc(t, wishID)
		d.comment(t, "developer", "wait", planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED)
		agents := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)
		if len(agents) != 1 {
			t.Fatalf("%d document agents, want 1", len(agents))
		}
		old := agents[0]
		e.workerUp(t, old.GetId())
		e.down() // Cut short: it would resume.
		writeFile(t, dir, plan.SettingsFile, "provider: PROVIDER_FAKE\nquestion_model: \"other\"\n")

		e = up(t, home, WithQuestionWorkers())
		got := e.get(t, old.GetId())
		if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Fatalf("the stale agent is %v after the restart, want done", got.GetStatus())
		}
		events := e.events(t, old.GetId())
		if countText(events, planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "closed by Djinn, was resuming") != 1 ||
			countText(events, planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "resumed") != 0 {
			t.Errorf("the stale agent's events: %v", eventKinds(events))
		}
		e.h.WakeDocuments(t.Context())
		deadline := time.Now().Add(10 * time.Second)
		for {
			agents = e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)
			if len(agents) == 2 {
				break
			}
			if len(agents) > 2 || time.Now().After(deadline) {
				t.Fatalf("%d document agents after the start, want the closed one and a new one", len(agents))
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

// TestDocumentToldWhileWaiting: a document agent continued while the machine holds it waits to resume; the
// developer's message meanwhile is not lost: the turn it takes once it starts names it.
func TestDocumentToldWhileWaiting(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	fake := &recordingFake{}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_FAKE] = fake
	lim := &limit{slots: 4}
	e := upWith(t, t.TempDir(), providers, WithQuestionWorkers(), WithCapacity(lim.capacity), WithTick(20*time.Millisecond))
	wishID, _ := e.wish(t, fakeProject(t, ""))
	d := e.markdownDoc(t, wishID)
	d.comment(t, "developer", "Why?", planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED)
	agents := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)
	if len(agents) != 1 {
		t.Fatalf("%d document agents, want 1", len(agents))
	}
	a := agents[0]
	e.settled(t, a.GetId(), func([]*planv1.TaskEvent) bool { return true })

	lim.set(4, "the test holds it")
	d.comment(t, "developer", "And now?", planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED)
	if got := e.get(t, a.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RESUMING {
		t.Fatalf("the continued agent is %v, want resuming while the machine holds it", got.GetStatus())
	}
	late := d.comment(t, "developer", "And this?", planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED)
	lim.set(4, "")
	e.settled(t, a.GetId(), func(events []*planv1.TaskEvent) bool {
		return countText(events, planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "continued: started") == 1
	})
	var prompt string
	for _, spec := range fake.all() {
		if spec.TaskID == a.GetId() {
			prompt = spec.Prompt
		}
	}
	if !strings.Contains(prompt, "- "+late.GetId()+": a comment") {
		t.Errorf("the turn does not name the message written while it waited:\n%s", prompt)
	}
	if n := len(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_DOCUMENT)); n != 1 {
		t.Errorf("%d document agents, want 1", n)
	}
}
