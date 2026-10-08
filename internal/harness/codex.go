package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// Codex runs OpenAI's Codex through its app-server: `codex app-server --listen stdio://`, JSON-RPC 2.0 with one
// JSON message per line on stdio (docs/providers.md). Djinn opens a thread in the worktree, and runs one turn per
// message: the prompt, then each message Send gives, in order. Once the last turn has completed, the input is
// closed and the app-server ends.
//
// Djinn passes its environment on and sets nothing about signing in: codex signs in by itself.
type Codex struct {
	// Command is the codex program; "codex", found on the PATH, by default.
	Command string
	// Grace is how long a worker asked to stop has before it is killed; Grace by default. It is also how long the
	// app-server has to end once its input is closed.
	Grace time.Duration
}

// args are the arguments of codex: the app-server, on stdio.
func (Codex) args() []string { return []string{"app-server", "--listen", "stdio://"} }

// codexVersion is the version Djinn gives the app-server as its client.
const codexVersion = "0.3.0"

// codexAccess is how codex receives what spec allows: the sandbox, who reviews an approval (nil: Djinn, by the
// rules of codexDecision), the approval policy, and the turns' sandbox policy. ok is false when codex's own
// configuration decides, and Djinn passes none of them.
//
// A read-only worker gets the read-only sandbox without network, and never asks. With permissions, editing
// opens the workspace-write sandbox, and the network follows the permissions. LISTED asks for every command
// codex does not trust by itself ("untrusted"), and Djinn approves the listed ones; AUTO lets codex ask when it
// needs to ("on-request") and routes the approvals to codex's own reviewer ("auto_review"). codex takes no list
// of commands at launch: in AUTO, the reviewer decides, denied commands included.
func codexAccess(spec Spec) (sandbox string, reviewer any, approval string, policy map[string]any, ok bool) {
	p := spec.Permissions
	switch {
	case spec.ReadOnly:
		return "read-only", nil, "never", map[string]any{"type": "readOnly", "networkAccess": false}, true
	case p == nil:
		return "", nil, "", nil, false
	}
	approval = "untrusted"
	if effectiveMode(p) == djinnv1.Mode_MODE_AUTO {
		approval, reviewer = "on-request", "auto_review"
	}
	if !p.GetEdit() {
		return "read-only", reviewer, approval, map[string]any{"type": "readOnly", "networkAccess": p.GetNetwork()}, true
	}
	return "workspace-write", reviewer, approval, map[string]any{
		"type": "workspaceWrite", "writableRoots": []any{}, "networkAccess": p.GetNetwork(),
		"excludeTmpdirEnvVar": false, "excludeSlashTmp": false,
	}, true
}

// threadRequest is the request opening the worker's thread, and its parameters: a new thread, the session to
// resume, or a new thread forked from it. In a project without permissions, codex's own configuration sets the
// sandbox and the approvals; an approval it asks for is declined, as nobody answers. Otherwise codexAccess.
func (c Codex) threadRequest(spec Spec) (string, map[string]any) {
	params := map[string]any{"cwd": spec.Dir}
	if sandbox, reviewer, approval, _, ok := codexAccess(spec); ok {
		params["sandbox"], params["approvalPolicy"] = sandbox, approval
		if reviewer != nil {
			params["approvalsReviewer"] = reviewer
		}
	}
	if spec.Model != "" {
		params["model"] = spec.Model
	}
	if text := skillsInstructions(spec.Skills); text != "" {
		// codex finds skills only in .agents/skills from the working folder up to the repository's root, and in
		// the user's folders: it is told where the summoned ones are instead.
		params["developerInstructions"] = text
	}
	if spec.Resume == "" {
		return "thread/start", params
	}
	params["threadId"] = spec.Resume
	// Codex keeps the history: sending it back can overflow a long thread's answer.
	params["excludeTurns"] = true
	if spec.Fork {
		return "thread/fork", params
	}
	return "thread/resume", params
}

// turnParams are the parameters of turn/start for a message.
func (c Codex) turnParams(spec Spec, thread, text string) map[string]any {
	params := map[string]any{
		"threadId": thread,
		"input":    []any{map[string]any{"type": "text", "text": text, "text_elements": []any{}}},
		"cwd":      spec.Dir,
	}
	if _, reviewer, approval, policy, ok := codexAccess(spec); ok {
		params["approvalPolicy"], params["sandboxPolicy"] = approval, policy
		if reviewer != nil {
			params["approvalsReviewer"] = reviewer
		}
	}
	if spec.Model != "" {
		params["model"] = spec.Model
	}
	return params
}

func (c Codex) Start(ctx context.Context, spec Spec) (Worker, error) {
	command, grace := c.Command, c.Grace
	if command == "" {
		command = "codex"
	}
	if grace == 0 {
		grace = Grace
	}
	p, err := startProcess(spec.Dir, command, c.args(), spec.Env, spec.Prefix, grace)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", command, err)
	}
	w := &codexWorker{c: c, spec: spec, p: p, grace: grace, events: make(chan Event, 64), done: make(chan struct{}),
		calls: map[int64]string{}, queue: []string{spec.Prompt}}
	go w.read()
	w.mu.Lock()
	err = w.call("initialize", map[string]any{
		"clientInfo": map[string]any{"name": "djinn", "title": "Djinn", "version": codexVersion},
	})
	w.mu.Unlock()
	if err != nil {
		w.Stop()
		go func() {
			for range w.events { // Drained so that the worker ends.
			}
		}()
		<-w.done
		return nil, err
	}
	go func() {
		select {
		case <-ctx.Done():
			w.Stop()
		case <-w.done:
		}
	}()
	return w, nil
}

type codexWorker struct {
	c      Codex
	spec   Spec
	p      *process
	grace  time.Duration
	events chan Event
	done   chan struct{}

	mu      sync.Mutex
	next    int64            // last request id
	calls   map[int64]string // requests waiting for their answer: their method
	thread  string           // the thread, once open
	turn    string           // the turn under way, if any
	queue   []string         // messages waiting for their turn
	closed  bool             // input closed: no more messages
	failure string
	lastErr string // the last error reported in the turn under way

	stopped atomic.Bool // asked to stop
	reaped  atomic.Bool // stopped by Djinn after its last turn, the app-server not ending by itself
	tools   map[string]bool
}

func (w *codexWorker) Events() <-chan Event { return w.events }

func (w *codexWorker) Pause() error  { return w.p.Pause() }
func (w *codexWorker) Resume() error { return w.p.Resume() }

func (w *codexWorker) Stop() {
	w.stopped.Store(true)
	w.p.Stop()
}

func (w *codexWorker) Wait() Result {
	<-w.done
	res := w.p.res
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.reaped.Load() && w.failure == "" {
		res = Result{} // Its work was done: Djinn only ended an app-server that did not end by itself.
	}
	if res.Err == nil && w.failure != "" {
		res.Err = errors.New(w.failure)
	}
	return res
}

// Send queues a message: it runs as a turn of its own once the turns before it have completed.
func (w *codexWorker) Send(text string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	w.queue = append(w.queue, text)
	if w.thread != "" && w.turn == "" && len(w.queue) == 1 {
		return w.startTurn()
	}
	return nil
}

// write sends a JSON-RPC message. The caller holds mu.
func (w *codexWorker) write(msg map[string]any) error {
	if w.closed {
		return ErrClosed
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := w.p.stdin.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write to codex: %w", err)
	}
	return nil
}

// call sends a request. The caller holds mu.
func (w *codexWorker) call(method string, params any) error {
	w.next++
	w.calls[w.next] = method
	return w.write(map[string]any{"id": w.next, "method": method, "params": params})
}

// startTurn starts a turn for the first message of the queue. The caller holds mu.
func (w *codexWorker) startTurn() error {
	w.turn = "starting"
	w.lastErr = ""
	return w.call("turn/start", w.c.turnParams(w.spec, w.thread, w.queue[0]))
}

// finish closes the input, so that the app-server ends; it is stopped if it has not after the grace delay. The
// caller holds mu.
func (w *codexWorker) finish() {
	if w.closed {
		return
	}
	w.closed = true
	w.p.stdin.Close()
	go func() {
		select {
		case <-w.p.done:
		case <-time.After(w.grace):
			w.reaped.Store(true)
			w.p.Stop()
		}
	}()
}

// fail records why the worker failed, and ends it: nothing more can run once a request Djinn needs fails. The
// caller holds mu.
func (w *codexWorker) fail(why string) {
	if w.failure == "" {
		w.failure = why
	}
	w.queue = nil
	w.finish()
}

// codexMessage is a JSON-RPC message from the app-server: an answer (id), a request (id and method) or a
// notification (method).
type codexMessage struct {
	ID     *json.RawMessage `json:"id"`
	Method string           `json:"method"`
	Params json.RawMessage  `json:"params"`
	Result json.RawMessage  `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (w *codexWorker) read() {
	defer close(w.done)
	defer close(w.events)
	for l := range w.p.lines {
		var events []Event
		if l.stderr {
			events = []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_LOG, Text: l.text, Raw: l.text}}
		} else {
			events = w.handle(l.text)
		}
		for _, ev := range events {
			w.events <- ev
		}
	}
	<-w.p.done
	w.mu.Lock()
	cut := (w.turn != "" || len(w.queue) > 0) && w.failure == "" && !w.stopped.Load()
	if cut {
		w.failure = "codex ended before the end of its turn"
	}
	w.closed = true
	failure := w.failure
	w.mu.Unlock()
	if cut {
		w.events <- Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, Text: failure}
	}
}

// handle reads one line of the app-server's output, answers what needs an answer, and says what it means.
func (w *codexWorker) handle(raw string) []Event {
	var m codexMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_OTHER, Text: raw, Raw: raw}}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var events lineEvents
	what := m.Method
	switch {
	case m.ID != nil && m.Method == "":
		what = w.answer(&events, &m)
	case m.ID != nil:
		w.request(&events, &m)
	default:
		w.notification(&events, m.Method, m.Params)
	}
	if len(events) == 0 && w.skipped(m.Method) {
		return nil
	}
	return events.done(raw, what)
}

// skipped are notifications Djinn reads and drops on purpose: streamed pieces whose whole comes later, and
// acknowledgements of what Djinn asked.
func (w *codexWorker) skipped(method string) bool {
	switch method {
	case "", "turn/started", "thread/started", "item/started", "item/completed", "turn/completed",
		"thread/tokenUsage/updated", "item/agentMessage/delta", "item/reasoning/summaryTextDelta",
		"item/reasoning/textDelta", "item/commandExecution/outputDelta", "item/fileChange/outputDelta",
		"item/plan/delta":
		return true
	}
	return false
}

// answer handles the answer to one of Djinn's requests, and says what it answered. The caller holds mu.
func (w *codexWorker) answer(events *lineEvents, m *codexMessage) string {
	var id int64
	_ = json.Unmarshal(*m.ID, &id)
	method, ok := w.calls[id]
	if !ok {
		return "answer to an unknown request"
	}
	delete(w.calls, id)
	if m.Error != nil {
		why := fmt.Sprintf("codex %s: %s", method, m.Error.Message)
		events.add(planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, why)
		w.fail(why)
		return method
	}
	switch method {
	case "initialize":
		_ = w.write(map[string]any{"method": "initialized"})
		name, params := w.c.threadRequest(w.spec)
		if err := w.call(name, params); err != nil {
			w.fail(err.Error())
		}
	case "thread/start", "thread/resume", "thread/fork":
		var r struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
			Model string `json:"model"`
		}
		_ = json.Unmarshal(m.Result, &r)
		w.thread = r.Thread.ID
		text := "session " + w.thread
		if r.Model != "" {
			text += ", model " + r.Model
		}
		events.add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, text).SessionID = w.thread
		if len(w.queue) > 0 {
			if err := w.startTurn(); err != nil {
				w.fail(err.Error())
			}
		}
	case "turn/start":
		var r struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(m.Result, &r)
		if w.turn == "starting" {
			w.turn = r.Turn.ID
		}
	}
	return method
}

// codexApproval is what Djinn reads of an approval the app-server asks for.
type codexApproval struct {
	Command                string          `json:"command"`
	Reason                 string          `json:"reason"`
	NetworkApprovalContext json.RawMessage `json:"networkApprovalContext"`
	GrantRoot              string          `json:"grantRoot"`
}

// codexDecision is Djinn's answer to an approval of method, under the worker's permissions: a command is accepted
// when it is listed and not denied, and asks for no network the permissions do not allow; a file change when the
// permissions allow editing, and only inside the workspace. Without permissions, everything is declined.
func codexDecision(p *djinnv1.Permissions, method string, a codexApproval) string {
	network := len(a.NetworkApprovalContext) > 0 && string(a.NetworkApprovalContext) != "null"
	switch {
	case p == nil:
	case method == "item/commandExecution/requestApproval":
		if commandAllowed(p, a.Command) && (!network || p.GetNetwork()) {
			return "accept"
		}
	case method == "item/fileChange/requestApproval":
		if p.GetEdit() && a.GrantRoot == "" {
			return "accept"
		}
	}
	return "decline"
}

// request answers a request of the app-server. Djinn grants no permission beyond the sandbox and the worker's
// permissions: an approval they do not cover is declined, and the agent goes on without it; any other request is
// refused as not supported. The caller holds mu.
func (w *codexWorker) request(events *lineEvents, m *codexMessage) {
	var p codexApproval
	_ = json.Unmarshal(m.Params, &p)
	switch m.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		perms := w.spec.Permissions
		if w.spec.ReadOnly {
			perms = nil
		}
		decision := codexDecision(perms, m.Method, p)
		_ = w.write(map[string]any{"id": m.ID, "result": map[string]any{"decision": decision}})
		if decision == "accept" {
			text := "permission granted: " + strings.TrimSuffix(strings.TrimPrefix(m.Method, "item/"), "/requestApproval")
			if p.Command != "" {
				text += " " + p.Command
			}
			events.add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, text)
			return
		}
		text := "permission denied: " + strings.TrimSuffix(strings.TrimPrefix(m.Method, "item/"), "/requestApproval")
		if p.Command != "" {
			text += " " + p.Command
		}
		if p.Reason != "" {
			text += " (" + p.Reason + ")"
		}
		events.add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, text)
	default:
		_ = w.write(map[string]any{"id": m.ID, "error": map[string]any{
			"code": -32601, "message": "Djinn does not support " + m.Method,
		}})
		events.add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "refused: "+m.Method)
	}
}

// codexItem is an item of a thread: what the agent said or did. Only the fields Djinn reads.
type codexItem struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	Text             string          `json:"text"`
	Summary          []string        `json:"summary"`
	Command          string          `json:"command"`
	Status           string          `json:"status"`
	AggregatedOutput *string         `json:"aggregatedOutput"`
	ExitCode         *int            `json:"exitCode"`
	Server           string          `json:"server"`
	Tool             string          `json:"tool"`
	Arguments        json.RawMessage `json:"arguments"`
	Query            string          `json:"query"`
	Changes          []struct {
		Path string `json:"path"`
		Kind any    `json:"kind"`
	} `json:"changes"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type codexTurnError struct {
	Message        string          `json:"message"`
	CodexErrorInfo json.RawMessage `json:"codexErrorInfo"`
}

// text is the error for a reader, with its kind when codex gives one: "usageLimitExceeded: …".
func (e *codexTurnError) text() string {
	if e == nil {
		return ""
	}
	var kind string
	if json.Unmarshal(e.CodexErrorInfo, &kind) != nil {
		var obj map[string]json.RawMessage
		if json.Unmarshal(e.CodexErrorInfo, &obj) == nil {
			for k := range obj {
				kind = k
			}
		}
	}
	if kind != "" && kind != "other" {
		return kind + ": " + e.Message
	}
	return e.Message
}

// notification reads a notification. The caller holds mu.
func (w *codexWorker) notification(events *lineEvents, method string, raw json.RawMessage) {
	var p struct {
		Item       *codexItem      `json:"item"`
		Error      *codexTurnError `json:"error"`
		WillRetry  bool            `json:"willRetry"`
		TokenUsage *struct {
			Total struct {
				InputTokens           int64 `json:"inputTokens"`
				CachedInputTokens     int64 `json:"cachedInputTokens"`
				CacheWriteInputTokens int64 `json:"cacheWriteInputTokens"`
				OutputTokens          int64 `json:"outputTokens"`
			} `json:"total"`
		} `json:"tokenUsage"`
		Turn *struct {
			ID     string          `json:"id"`
			Status string          `json:"status"`
			Error  *codexTurnError `json:"error"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(raw, &p)
	switch method {
	case "item/started":
		if p.Item != nil {
			w.itemStarted(events, p.Item)
		}
	case "item/completed":
		if p.Item != nil {
			w.itemCompleted(events, p.Item)
		}
	case "thread/tokenUsage/updated":
		if p.TokenUsage == nil {
			return
		}
		// What the thread spent so far. Codex counts cached tokens among the input ones, and gives no cost.
		t := p.TokenUsage.Total
		usage := &planv1.Usage{
			InputTokens: t.InputTokens - t.CachedInputTokens, OutputTokens: t.OutputTokens,
			CacheReadTokens: t.CachedInputTokens, CacheWriteTokens: t.CacheWriteInputTokens,
		}
		ev := events.add(planv1.TaskEventKind_TASK_EVENT_KIND_USAGE, fmt.Sprintf("%d tokens in, %d out, %d from the cache",
			usage.GetInputTokens(), usage.GetOutputTokens(), usage.GetCacheReadTokens()))
		ev.Usage, ev.SessionID = usage, w.thread
	case "error":
		text := p.Error.text()
		if p.WillRetry {
			events.add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "retrying: "+text)
			return
		}
		w.lastErr = text
		events.add(planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, text)
	case "turn/completed":
		if p.Turn == nil || (w.turn != "starting" && p.Turn.ID != w.turn) {
			return // A late notice of a turn that is not this worker's current one.
		}
		w.turn = ""
		if len(w.queue) > 0 {
			w.queue = w.queue[1:]
		}
		switch p.Turn.Status {
		case "completed":
		case "failed", "interrupted":
			why := p.Turn.Error.text()
			if why == "" {
				why = "codex turn " + p.Turn.Status
			}
			if why != w.lastErr {
				events.add(planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, why)
			}
			w.failure = why
		}
		events.add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "turn "+p.Turn.Status)
		if len(w.queue) > 0 && w.failure == "" {
			if err := w.startTurn(); err != nil {
				w.fail(err.Error())
			}
			return
		}
		w.finish()
	}
}

// toolItem says whether an item is a tool the agent called, and how to write the call.
func toolItem(it *codexItem) (string, bool) {
	switch it.Type {
	case "commandExecution":
		return "command " + it.Command, true
	case "fileChange":
		paths := make([]string, len(it.Changes))
		for i, c := range it.Changes {
			paths[i] = c.Path
		}
		return "fileChange " + strings.Join(paths, " "), true
	case "mcpToolCall":
		return strings.TrimSpace(it.Server + "." + it.Tool + " " + compactOrEmpty(it.Arguments)), true
	case "dynamicToolCall":
		return strings.TrimSpace(it.Tool + " " + compactOrEmpty(it.Arguments)), true
	case "webSearch":
		return strings.TrimSpace("webSearch " + it.Query), true
	case "imageView", "imageGeneration", "collabAgentToolCall":
		return it.Type, true
	}
	return "", false
}

func (w *codexWorker) itemStarted(events *lineEvents, it *codexItem) {
	call, ok := toolItem(it)
	if !ok {
		return // The agent's message and its reasoning are said once completed; the user's is Djinn's prompt.
	}
	if w.tools == nil {
		w.tools = map[string]bool{}
	}
	w.tools[it.ID] = true
	events.add(planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, call)
}

func (w *codexWorker) itemCompleted(events *lineEvents, it *codexItem) {
	switch it.Type {
	case "agentMessage":
		if strings.TrimSpace(it.Text) != "" {
			events.add(planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, it.Text)
		}
		return
	case "reasoning":
		if s := strings.Join(it.Summary, "\n"); s != "" {
			events.add(planv1.TaskEventKind_TASK_EVENT_KIND_OTHER, "reasoning: "+s)
		}
		return
	case "userMessage":
		return
	}
	call, ok := toolItem(it)
	if !ok {
		events.add(planv1.TaskEventKind_TASK_EVENT_KIND_OTHER, it.Type)
		return
	}
	if !w.tools[it.ID] {
		events.add(planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, call)
	}
	delete(w.tools, it.ID)
	var text string
	switch {
	case it.AggregatedOutput != nil:
		text = strings.TrimRight(*it.AggregatedOutput, "\r\n")
	case it.Error != nil:
		text = it.Error.Message
	case len(it.Result) > 0 && string(it.Result) != "null":
		text = compact(it.Result)
	}
	failed := it.Status == "failed" || it.Status == "declined" || it.Error != nil || (it.ExitCode != nil && *it.ExitCode != 0)
	switch {
	case failed:
		prefix := "error"
		if it.Status == "declined" {
			prefix = "declined"
		} else if it.ExitCode != nil && *it.ExitCode != 0 {
			prefix = fmt.Sprintf("exit %d", *it.ExitCode)
		}
		if text = prefix + ": " + text; strings.HasSuffix(text, ": ") {
			text = prefix
		}
	case text == "":
		text = it.Status
	}
	events.add(planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT, text)
}
