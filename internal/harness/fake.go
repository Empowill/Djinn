package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// Fake is an agent that plays a script, to try Djinn and test it without a paid model. Each line of the prompt is
// a step:
//
//	text Hello            the agent writes Hello
//	tool Bash ls -la      it calls a tool
//	result total 8        the tool returns
//	usage 1200 300 0.02   it has spent so far 1200 tokens in, 300 out, $0.02
//	write notes.md Hi     it writes Hi in notes.md, in its folder, when it may edit (else: permission denied)
//	sleep 5s              it works for a while
//	fail no tests         it fails with this reason
//	exit 2                its process exits with this code
//
// Any other line is said back as text. It runs in Djinn's process: stopping it ends its script at once, pausing it
// holds its script before the next step until it is resumed.
type Fake struct{}

func (Fake) Start(ctx context.Context, spec Spec) (Worker, error) {
	ctx, cancel := context.WithCancel(ctx)
	w := &fakeWorker{events: make(chan Event), cancel: cancel, done: make(chan struct{})}
	go w.play(ctx, spec)
	return w, nil
}

type fakeWorker struct {
	events chan Event
	cancel context.CancelFunc
	done   chan struct{}
	res    Result

	mu    sync.Mutex
	inbox []string
	held  chan struct{} // while paused; closed when resumed
}

func (w *fakeWorker) Events() <-chan Event { return w.events }
func (w *fakeWorker) Stop()                { w.cancel() }
func (w *fakeWorker) Wait() Result         { <-w.done; return w.res }

// Send queues a message: the fake says it back once its script is played.
func (w *fakeWorker) Send(text string) error {
	select {
	case <-w.done:
		return ErrClosed
	default:
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.inbox = append(w.inbox, text)
	return nil
}

// Pause holds the script before its next step, as a process stopped where it is.
func (w *fakeWorker) Pause() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.held == nil {
		w.held = make(chan struct{})
	}
	return nil
}

// Resume lets the script go on.
func (w *fakeWorker) Resume() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.held != nil {
		close(w.held)
		w.held = nil
	}
	return nil
}

// hold waits while the worker is paused. It says false when the worker is stopped meanwhile.
func (w *fakeWorker) hold(ctx context.Context) bool {
	w.mu.Lock()
	held := w.held
	w.mu.Unlock()
	if held == nil {
		return true
	}
	select {
	case <-held:
		return true
	case <-ctx.Done():
		return false
	}
}

func (w *fakeWorker) play(ctx context.Context, spec Spec) {
	defer close(w.done)
	defer close(w.events)
	stopped := Result{ExitCode: -1, Err: context.Canceled}
	emit := func(ev Event) bool {
		if !w.hold(ctx) {
			return false
		}
		select {
		case w.events <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	session := "fake session " + spec.TaskID
	switch {
	case spec.Fork:
		session += ", forked from " + spec.Resume
	case spec.Resume != "":
		session += ", resumed"
	}
	if !emit(Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: session, SessionID: spec.TaskID}) {
		w.res = stopped
		return
	}
	for raw := range strings.Lines(spec.Prompt) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !w.hold(ctx) {
			w.res = stopped
			return
		}
		verb, rest, _ := strings.Cut(raw, " ")
		rest = strings.TrimSpace(rest)
		ev := Event{Raw: raw}
		switch verb {
		case "text":
			ev.Kind, ev.Text = planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, rest
		case "tool":
			ev.Kind, ev.Text = planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, rest
		case "result":
			ev.Kind, ev.Text = planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT, rest
		case "usage":
			u, err := fakeUsage(rest)
			if err != nil {
				ev.Kind, ev.Text = planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, err.Error()
				break
			}
			ev.Kind, ev.Usage = planv1.TaskEventKind_TASK_EVENT_KIND_USAGE, u
			ev.Text = fmt.Sprintf("%d tokens in, %d out, $%.4f", u.GetInputTokens(), u.GetOutputTokens(), u.GetCostUsd())
		case "write":
			ev = fakeWrite(spec, rest)
			ev.Raw = raw
		case "sleep":
			d, err := time.ParseDuration(rest)
			if err != nil {
				ev.Kind, ev.Text = planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, "sleep: "+err.Error()
				break
			}
			select {
			case <-time.After(d):
				continue
			case <-ctx.Done():
				w.res = stopped
				return
			}
		case "fail":
			emit(Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, Text: rest, Raw: raw})
			w.res = Result{ExitCode: 1, Err: errors.New(rest)}
			return
		case "exit":
			code, err := strconv.Atoi(rest)
			if err != nil {
				code = 1
			}
			w.res = Result{ExitCode: code}
			return
		default:
			ev.Kind, ev.Text = planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, raw
		}
		if !emit(ev) {
			w.res = stopped
			return
		}
	}
	w.mu.Lock()
	inbox := w.inbox
	w.inbox = nil
	w.mu.Unlock()
	for _, text := range inbox {
		if !emit(Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, Text: "received: " + text}) {
			w.res = stopped
			return
		}
	}
}

// fakeWrite writes "file text" in the worker's folder, as an agent that respects its rights: refused to a
// read-only worker, and to one whose permissions do not allow editing. Without permissions, the fake's own
// configuration allows it.
func fakeWrite(spec Spec, args string) Event {
	name, text, _ := strings.Cut(args, " ")
	call := Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, Text: "Write " + name}
	if spec.ReadOnly || spec.Permissions != nil && !spec.Permissions.GetEdit() {
		call.Kind, call.Text = planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "permission denied: Write "+name
		return call
	}
	path := filepath.Join(spec.Dir, filepath.FromSlash(name))
	if name == "" || !filepath.IsLocal(filepath.FromSlash(name)) {
		return Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, Text: "write: want a file inside the folder: " + name}
	}
	if err := os.WriteFile(path, []byte(text+"\n"), 0o600); err != nil {
		return Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, Text: "write: " + err.Error()}
	}
	return call
}

// fakeUsage reads "input output [cost]".
func fakeUsage(s string) (*planv1.Usage, error) {
	f := strings.Fields(s)
	if len(f) < 2 || len(f) > 3 {
		return nil, fmt.Errorf("usage: want input and output tokens, then the cost: %q", s)
	}
	in, err1 := strconv.ParseInt(f[0], 10, 64)
	out, err2 := strconv.ParseInt(f[1], 10, 64)
	var cost float64
	var err3 error
	if len(f) == 3 {
		cost, err3 = strconv.ParseFloat(f[2], 64)
	}
	if err := errors.Join(err1, err2, err3); err != nil {
		return nil, fmt.Errorf("usage %q: %w", s, err)
	}
	return &planv1.Usage{InputTokens: in, OutputTokens: out, CostUsd: cost}, nil
}
