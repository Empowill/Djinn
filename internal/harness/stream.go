package harness

import (
	"context"
	"errors"
	"fmt"
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
	// queued is how many messages the agent still holds for later turns, when it says so; nil when it does not.
	// A message sent during a turn may be folded into that turn, and then gets no result of its own.
	queued *int
}

// Quiet is how long a worker that has messages without a result, after a result that did not say how many it
// still holds, may stay without a line on any output. Past it, those messages are taken as folded into the turn
// that ended: the input is closed, and the agent exits once done.
const Quiet = 10 * time.Minute

// streamAgent is what a stream worker needs from its provider.
type streamAgent struct {
	name   string // the program, for errors
	parser streamParser
	quiet  time.Duration // Quiet when 0
	// encode writes a user message as the agent reads it on its input, without the newline.
	encode func(text string) ([]byte, error)
}

// startStream starts command with args, writes the prompt as the first message, and reads what the agent says.
// The input stays open for more messages, and is closed once every message has its result: the agent then exits.
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
	p, err := startProcess(spec.Dir, command, args, spec.Env, spec.Scope, grace)
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

	mu      sync.Mutex
	pending int  // messages sent without a result yet
	closed  bool // input closed: no more messages
	failure string
	stopped atomic.Bool // asked to stop: a turn cut short is no failure of the agent
}

func (w *streamWorker) Events() <-chan Event { return w.events }

// Done is closed once the worker has ended and its events are all sent.
func (w *streamWorker) Done() <-chan struct{} { return w.done }

// PID is the process of the agent.
func (w *streamWorker) PID() int { return w.p.cmd.Process.Pid }

// Cgroup is the cgroup of the agent's scope.
func (w *streamWorker) Cgroup() string { return w.p.cgroup }

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
	return nil
}

// read turns the process's lines into events. Once every message sent has its result, the input is closed and
// the agent exits: a result that says how many messages are still queued sets how many wait, another counts one.
// A worker that said an error fails with it, even when every turn ended and the process exited 0.
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
	quiet := w.a.quiet
	if quiet == 0 {
		quiet = Quiet
	}
	// silence runs after a result that did not say how many messages are queued, while some still wait: nothing
	// said for quiet, and they are taken as folded into the turn that ended.
	silence := time.NewTimer(quiet)
	silence.Stop()
	waiting := false
	defer silence.Stop()
lines:
	for {
		var l line
		select {
		case next, ok := <-w.p.lines:
			if !ok {
				break lines
			}
			l = next
		case <-silence.C:
			waiting = false
			w.mu.Lock()
			if !w.closed {
				w.closed, w.pending = true, 0
				w.p.stdin.Close()
			}
			w.mu.Unlock()
			send([]Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
				Text: fmt.Sprintf("%s said nothing for %s after its result: the messages sent during its turn are taken as answered", w.a.name, quiet)}})
			continue
		}
		if waiting {
			silence.Reset(quiet)
		}
		if l.stderr {
			send(w.a.parser.stderr(l.text))
			continue
		}
		events, end := w.a.parser.stdout(l.text)
		send(events)
		if end == nil {
			continue
		}
		w.mu.Lock()
		if end.failure != "" {
			w.failure = end.failure
		}
		if end.queued != nil {
			w.pending = *end.queued
		} else {
			w.pending--
		}
		if w.pending <= 0 && !w.closed {
			w.closed = true
			w.p.stdin.Close()
		}
		waiting = end.queued == nil && w.pending > 0 && !w.closed
		w.mu.Unlock()
		if waiting {
			silence.Reset(quiet)
		} else {
			silence.Stop()
		}
	}
	send(w.a.parser.flush())
	<-w.p.done
	w.mu.Lock()
	if w.failure == "" {
		w.failure = lastError
	}
	cut := w.pending > 0 && w.failure == "" && !w.stopped.Load()
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
