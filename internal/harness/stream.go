package harness

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
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
	// retry is a message Djinn sends on the agent's input to continue the conversation, when not empty (after backoff, when set).
	retry string
	// backoff is how long to wait before sending retry, when not 0.
	backoff time.Duration
}

// Quiet is how long a worker that has messages without a result, after a result that did not say how many it
// still holds, may stay without a line on any output. Past it, those messages are taken as folded into the turn
// that ended: the input is closed, and the agent exits once done.
const Quiet = 10 * time.Minute

// StreamIdleTimeout is how long a model call that returns nothing may wait before being cut and continued.
// Standard LLM streams produce chunks within seconds; 5 minutes without any stream event indicates a dead connection.
const StreamIdleTimeout = 5 * time.Minute

// streamAgent is what a stream worker needs from its provider.
type streamAgent struct {
	name   string // the program, for errors
	parser streamParser
	quiet  time.Duration // Quiet when 0
	idle   time.Duration // StreamIdleTimeout when 0; negative disables
	// encode writes a user message as the agent reads it on its input, without the newline.
	encode func(text string) ([]byte, error)
}

// toolTracker reports whether a local tool is currently running.
type toolTracker interface {
	toolRunning() bool
}

// startStream starts command with args, writes the prompt as the first message, and reads what the agent says.
// The input stays open for more messages, and is closed once every message has its result: the agent then exits.
// Started with Spec.Listen (an ifrit), it stays open until CloseInput: the agent listens between turns.
func startStream(ctx context.Context, spec Spec, command string, args []string, grace time.Duration, a streamAgent) (Worker, error) {
	p, err := startProcess(spec.Dir, command, args, spec.Env, spec.Scope, grace, spec.LowPriority, spec.Track)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", command, err)
	}
	w := &streamWorker{ctx: ctx, a: a, p: p, events: make(chan Event, 64), done: make(chan struct{}), wake: make(chan struct{}, 1),
		listen: spec.Listen}
	go func() {
		defer logx.Crash("stream read", "agent", command)
		w.read()
	}()
	if err := w.Send(spec.Prompt); err != nil {
		p.Stop()
		go func() {
			defer logx.Crash("stream drain", "agent", command)
			for range w.events { // Drained so that the worker ends.
			}
		}()
		<-w.done
		return nil, err
	}
	go func() {
		defer logx.Crash("stream stop", "agent", command)
		select {
		case <-ctx.Done():
			w.Stop()
		case <-w.done:
		}
	}()
	return w, nil
}

type streamWorker struct {
	ctx    context.Context
	a      streamAgent
	p      *process
	events chan Event
	done   chan struct{}
	wake   chan struct{}

	mu        sync.Mutex
	pending   int  // messages sent without a result yet
	closed    bool // input closed: no more messages
	listen    bool // the input stays open once every message has its result (Spec.Listen), until CloseInput
	listening bool // every message has its result, and the agent waits on its open input
	ending    bool // CloseInput was called: no more messages
	failure   string
	stopped   atomic.Bool // asked to stop: a turn cut short is no failure of the agent
}

func (w *streamWorker) Events() <-chan Event { return w.events }

// Done is closed once the worker has ended and its events are all sent.
func (w *streamWorker) Done() <-chan struct{} { return w.done }

// PID is the process of the agent.
func (w *streamWorker) PID() int { return w.p.cmd.Process.Pid }

// Cgroup is the cgroup of the agent's scope.
func (w *streamWorker) Cgroup() string { return w.p.cgroup }

func (w *streamWorker) Pause() error               { return w.p.Pause() }
func (w *streamWorker) Resume() error              { return w.p.Resume() }
func (w *streamWorker) SetPriority(low bool) error { return w.p.SetPriority(low) }

func (w *streamWorker) Stop() {
	w.stopped.Store(true)
	w.p.Stop()
}

func (w *streamWorker) Wait() Result {
	<-w.done
	res := w.p.res
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failure != "" && (res.Err == nil || isStreamIdleString(w.failure)) {
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
	if w.closed || w.ending {
		return ErrClosed
	}
	if _, err := w.p.stdin.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write to %s: %w", w.a.name, err)
	}
	w.pending++
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return nil
}

// CloseInput closes the input of a worker started with Spec.Listen once the turn under way, if any, has its result.
func (w *streamWorker) CloseInput() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.listen, w.ending = false, true
	if w.pending <= 0 && !w.closed {
		w.closed = true
		w.p.stdin.Close()
	}
}

// listened is the agent's turn ending while its input stays open (Spec.Listen): what it says once it listens, if it
// did not already. The caller holds mu.
func (w *streamWorker) listened() []Event {
	w.pending = 0
	if w.listening {
		return nil
	}
	w.listening = true
	return []Event{listeningEvent(w.a.name)}
}

// read turns the process's lines into events. Once every message sent has its result, the input is closed and
// the agent exits: a result that says how many messages are still queued sets how many wait, another counts one.
// A worker that said an error fails with it, even when every turn ended and the process exited 0. Started with
// Spec.Listen, the input stays open once every message has its result: the agent listens until a message starts
// its next turn, which forgets the errors of the turns before it.
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
	// working says the agent works again: a message came while it listened.
	working := func() {
		w.mu.Lock()
		flip := w.listening && w.pending > 0
		if flip {
			w.listening, w.failure = false, ""
			lastError = ""
		}
		w.mu.Unlock()
		if flip {
			send([]Event{workingEvent(w.a.name)})
		}
	}
	// silence runs after a result that did not say how many messages are queued, while some still wait: nothing
	// said for quiet, and they are taken as folded into the turn that ended.
	silence := time.NewTimer(quiet)
	silence.Stop()
	waiting := false
	defer silence.Stop()

	idleTimeout := w.a.idle
	if idleTimeout == 0 {
		idleTimeout = StreamIdleTimeout
	}
	var idleTimer *time.Timer
	var idleCh <-chan time.Time
	if idleTimeout > 0 {
		idleTimer = time.NewTimer(idleTimeout)
		idleTimer.Stop()
		defer idleTimer.Stop()
		idleCh = idleTimer.C
	}
	resetIdle := func() {
		if idleTimer == nil {
			return
		}
		w.mu.Lock()
		pending := w.pending
		closed := w.closed
		w.mu.Unlock()
		var toolRunning bool
		if tt, ok := w.a.parser.(toolTracker); ok {
			toolRunning = tt.toolRunning()
		}
		if pending > 0 && !closed && !toolRunning {
			idleTimer.Reset(idleTimeout)
		} else {
			idleTimer.Stop()
		}
	}
lines:
	for {
		var l line
		select {
		case next, ok := <-w.p.lines:
			if !ok {
				break lines
			}
			l = next
		case <-w.wake:
			working()
			resetIdle()
			continue
		case <-idleCh:
			w.mu.Lock()
			w.failure = formatStreamIdle(idleTimeout)
			w.closed = true
			w.p.stdin.Close()
			failure := w.failure
			w.mu.Unlock()
			send([]Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, Text: failure}})
			w.p.Stop()
			break lines
		case <-silence.C:
			waiting = false
			var listens []Event
			w.mu.Lock()
			switch {
			case w.closed:
			case w.listen:
				listens = w.listened()
			default:
				w.closed, w.pending = true, 0
				w.p.stdin.Close()
			}
			w.mu.Unlock()
			send([]Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
				Text: fmt.Sprintf("%s said nothing for %s after its result: the messages sent during its turn are taken as answered", w.a.name, quiet)}})
			send(listens)
			continue
		}
		working()
		if waiting {
			silence.Reset(quiet)
		}
		if l.stderr {
			send(w.a.parser.stderr(l.text))
			resetIdle()
			continue
		}
		events, end := w.a.parser.stdout(l.text)
		send(events)
		if end == nil {
			resetIdle()
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
		if end.retry != "" && !w.closed {
			if end.backoff > 0 {
				w.mu.Unlock()
				select {
				case <-time.After(end.backoff):
				case <-w.p.done:
				case <-w.ctx.Done():
				}
				w.mu.Lock()
			}
			if !w.closed {
				if b, err := w.a.encode(end.retry); err == nil {
					if _, err := w.p.stdin.Write(append(b, '\n')); err == nil {
						w.pending++
						lastError = ""
					}
				}
			}
		}
		var listens []Event
		if w.pending <= 0 && !w.closed {
			if w.listen {
				listens = w.listened()
			} else {
				w.closed = true
				w.p.stdin.Close()
			}
		}
		waiting = end.queued == nil && w.pending > 0 && !w.closed
		w.mu.Unlock()
		send(listens)
		if waiting {
			silence.Reset(quiet)
		} else {
			silence.Stop()
		}
		resetIdle()
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

// formatStreamIdle formats the idle duration as "stream idle for N min".
func formatStreamIdle(d time.Duration) string {
	mins := int(math.Ceil(d.Minutes()))
	if mins < 1 {
		mins = 1
	}
	return fmt.Sprintf("stream idle for %d min", mins)
}

// isStreamIdle reports whether err is a stream idle timeout error.
func isStreamIdle(err error) bool {
	return err != nil && isStreamIdleString(err.Error())
}

// isStreamIdleString reports whether s is a stream idle timeout error string.
func isStreamIdleString(s string) bool {
	return strings.HasPrefix(s, "stream idle for ")
}
