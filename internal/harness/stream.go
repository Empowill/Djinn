package harness

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// streamParser reads the output of an agent that writes one JSON line per event and ends each turn with a result
// line: claude -p and agy, both in stream-json.
type streamParser interface {
	// stdout turns a line of the output into events, and says how the turn ended when the line ends one.
	stdout(raw string) ([]Event, *turnEnd)
	// stderr turns a line of the error output into events.
	stderr(raw string) []Event
	// flush gives what the parser still holds once the output has ended: a message cut short.
	flush() []Event
}

// turnEnd is what the result line of a turn says.
type turnEnd struct {
	failure string // why the turn failed, when it did
}

// streamAgent is what a stream worker needs from its provider.
type streamAgent struct {
	name   string // the program, for errors
	parser streamParser
	// encode writes a user message as the agent reads it on its input, without the newline.
	encode func(text string) ([]byte, error)
	// settle, when set, says the agent may take a message sent during a turn into that turn, and give one result
	// for both: after a result that leaves messages without one, the agent has finished once it says nothing of a
	// turn for settle. Unset, every message waits for a result of its own.
	settle time.Duration
}

// startStream starts command with args, writes the prompt as the first message, and reads what the agent says.
// The input stays open for more messages, and is closed once the agent has answered them all: the agent then exits.
func startStream(ctx context.Context, spec Spec, command string, args []string, grace time.Duration, a streamAgent) (Worker, error) {
	return startStreamWith(ctx, spec, command, args, grace, a, true)
}

// startStreamIdle starts command with args and lets the agent load, without a message: the first Send is its first
// message. A warm worker.
func startStreamIdle(ctx context.Context, spec Spec, command string, args []string, grace time.Duration, a streamAgent) (Worker, error) {
	return startStreamWith(ctx, spec, command, args, grace, a, false)
}

func startStreamWith(
	ctx context.Context, spec Spec, command string, args []string, grace time.Duration, a streamAgent, prompt bool,
) (Worker, error) {
	p, err := startProcess(spec.Dir, command, args, spec.Env, spec.Prefix, grace)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", command, err)
	}
	w := &streamWorker{a: a, p: p, events: make(chan Event, 64), done: make(chan struct{})}
	go w.read()
	if !prompt {
		go func() {
			select {
			case <-ctx.Done():
				w.Stop()
			case <-w.done:
			}
		}()
		return w, nil
	}
	if err := w.Send(spec.Prompt); err != nil {
		p.Stop()
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

type streamWorker struct {
	a      streamAgent
	p      *process
	events chan Event
	done   chan struct{}

	mu       sync.Mutex
	pending  int  // messages sent without a result yet
	closed   bool // input closed: no more messages
	active   bool // the agent said something of a turn since the last result
	settling int  // the settle period under way, 0 for none
	settles  int  // settle periods started, to number them
	failure  string
	stopped  atomic.Bool // asked to stop: a turn cut short is no failure of the agent
}

func (w *streamWorker) Events() <-chan Event { return w.events }

// Done is closed once the worker has ended and its events are all sent.
func (w *streamWorker) Done() <-chan struct{} { return w.done }

// PID is the process of the agent.
func (w *streamWorker) PID() int { return w.p.cmd.Process.Pid }

func (w *streamWorker) Pause() error  { return w.p.Pause() }
func (w *streamWorker) Resume() error { return w.p.Resume() }

func (w *streamWorker) Stop() {
	w.stopped.Store(true)
	w.p.Stop()
}

func (w *streamWorker) Wait() Result {
	<-w.done
	res := w.p.res
	w.mu.Lock()
	defer w.mu.Unlock()
	if res.Err == nil && w.failure != "" {
		res.Err = errors.New(w.failure)
	}
	return res
}

// Send writes a user message on the agent's input.
func (w *streamWorker) Send(text string) error {
	b, err := w.a.encode(text)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	if _, err := w.p.stdin.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write to %s: %w", w.a.name, err)
	}
	w.pending++
	w.settling = 0 // A message sent after the last result has a turn of its own.
	return nil
}

// closeInput closes the agent's input: it ends once it has answered what it read. The caller holds mu.
func (w *streamWorker) closeInput() {
	if !w.closed {
		w.closed = true
		w.p.stdin.Close()
	}
}

// settle waits for a turn after a result that left messages without one. Claude takes a message sent during a
// turn into that turn and gives a single result: if nothing of a turn follows for the agent's settle period, the
// messages were answered, and the input is closed. A message, or a turn under way, cancels it. The caller holds mu.
func (w *streamWorker) settle() {
	w.settles++
	w.settling = w.settles
	n := w.settling
	time.AfterFunc(w.a.settle, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.settling == n {
			w.settling, w.pending = 0, 0
			w.closeInput()
		}
	})
}

// ofTurn says whether events show the agent at work in a turn: text, a tool it calls, a tool's result. Its system
// lines and usage may come after a result.
func ofTurn(events []Event) bool {
	return slices.ContainsFunc(events, func(ev Event) bool {
		switch ev.Kind {
		case planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL,
			planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT:
			return true
		}
		return false
	})
}

// read turns the process's lines into events. Once every message sent has its result, or the agent settled after
// one (settle), the input is closed and the agent exits. A worker that said an error fails with it, even when every turn ended and the process exited 0.
func (w *streamWorker) read() {
	defer close(w.done)
	defer close(w.events)
	var lastError string
	send := func(events []Event) {
		for _, ev := range events {
			if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_ERROR {
				lastError = ev.Text
			}
			w.events <- ev
		}
	}
	for l := range w.p.lines {
		if l.stderr {
			send(w.a.parser.stderr(l.text))
			continue
		}
		events, end := w.a.parser.stdout(l.text)
		send(events)
		if end == nil {
			if ofTurn(events) {
				w.mu.Lock()
				w.active, w.settling = true, 0
				w.mu.Unlock()
			}
			continue
		}
		w.mu.Lock()
		if end.failure != "" {
			w.failure = end.failure
		}
		w.active, w.settling = false, 0
		w.pending--
		switch {
		case w.pending <= 0:
			w.closeInput()
		case w.a.settle > 0 && !w.closed:
			w.settle()
		}
		w.mu.Unlock()
	}
	send(w.a.parser.flush())
	<-w.p.done
	w.mu.Lock()
	if w.failure == "" {
		w.failure = lastError
	}
	if w.settling != 0 {
		// The agent ended while settling after a result: the messages left were taken into its last turn.
		w.settling, w.pending = 0, 0
	}
	cut := (w.pending > 0 || w.active) && w.failure == "" && !w.stopped.Load()
	if cut {
		// The process ended in the middle of a turn: whatever its exit code, the turn did not finish.
		w.failure = w.a.name + " ended before the end of its turn"
	}
	w.closed = true
	failure := w.failure
	w.mu.Unlock()
	if cut {
		w.events <- Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, Text: failure}
	}
}

// lineEvents is a parser's helper: the events of one line, the first one keeping the line raw.
type lineEvents []Event

func (l *lineEvents) add(kind planv1.TaskEventKind, text string) *Event {
	*l = append(*l, Event{Kind: kind, Text: text})
	return &(*l)[len(*l)-1]
}

// done gives the events, the first keeping raw; with none, an OTHER event holding the line and what it is.
func (l lineEvents) done(raw, what string) []Event {
	if len(l) == 0 {
		l = lineEvents{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_OTHER, Text: what}}
	}
	l[0].Raw = raw
	return l
}
