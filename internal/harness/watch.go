package harness

// A watcher is a task without an agent: Djinn runs a command in the project's folder, and each paragraph it prints
// wakes the wish's lead with one line. No model reads its output, and it spends no token. It takes no slot of the
// machine (internal/dispatch): it sleeps until its command prints.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/plan"
)

// What a watcher waits for, by default.
const (
	// WatchQuiet is how long a command stays silent before what it printed makes a paragraph.
	WatchQuiet = time.Second
	// watchBackoff is the first wait before a command that failed starts again; it doubles up to watchMaxBackoff,
	// and starts over once the command exits 0 or ran longer than that.
	watchBackoff    = time.Second
	watchMaxBackoff = time.Minute
	// watchGap is the least time between two starts of a command that exits 0 at once: its loop never spins.
	watchGap = time.Second
	// watchLines is the most lines of a paragraph: a command that never pauses still wakes its lead.
	watchLines = 200
	// watchLineMax and watchCommandMax are the most characters of a paragraph's first line, and of the command,
	// in the line the lead is told.
	watchLineMax    = 200
	watchCommandMax = 120
)

// Watch runs a command instead of an agent: the task's prompt is its command line. It runs without a shell, in the
// worker's folder, under the worker's environment and prefix; quotes group words, as in a shell. Where the project
// lists the commands its workers may run (.agents/permissions.txtpb), the command must be one of them, and not
// denied: a watcher has no reviewer. It cannot run read-only.
type Watch struct {
	// Quiet is how long the command stays silent before what it printed makes a paragraph; 0 for WatchQuiet.
	Quiet time.Duration
	// Grace is how long the command has to stop before it is killed; 0 for Grace.
	Grace time.Duration
	// Backoff is the first wait before a command that failed starts again; 0 for one second.
	Backoff time.Duration
	// Gap is the least time between two starts of a command that exits 0 at once, and between two starts of one
	// that fails; 0 for one second.
	Gap time.Duration
	// Items ends a paragraph on an empty line too: each one is an item of an inbox source.
	Items bool
}

// Watched is a paragraph a watcher's command printed, on its event; or, without a first line, the end of a command
// whose last paragraph said it still watched.
type Watched struct {
	// First is its first line; empty on the end of the command.
	First string
	// Command is the watcher's command line.
	Command string
	// Watching says the command still watches: it runs, or starts again.
	Watching bool
	// Wake says the wish's lead is told: not when the paragraph says again what the last one said, nor when the
	// watcher was stopped.
	Wake bool
}

func (c Watch) Start(ctx context.Context, spec Spec) (Worker, error) {
	command := strings.TrimSpace(spec.Prompt)
	args, err := splitCommand(command)
	switch {
	case err != nil:
		return nil, err
	case len(args) == 0:
		return nil, errors.New("a watcher needs a command: its prompt is the command line")
	case spec.ReadOnly:
		return nil, errors.New("a watcher runs a command: it cannot run read-only")
	case spec.Permissions != nil && !commandAllowed(spec.Permissions, command):
		return nil, fmt.Errorf("the project's %s does not let its workers run %q without review, and a watcher has no reviewer",
			PermissionsFile, command)
	}
	ctx, cancel := context.WithCancel(ctx)
	w := &watchWorker{
		c: c.defaults(), spec: spec, command: command, args: args, events: make(chan Event), ctx: ctx, cancel: cancel,
		done: make(chan struct{}),
	}
	go w.loop()
	return w, nil
}

func (c Watch) defaults() Watch {
	if c.Quiet == 0 {
		c.Quiet = WatchQuiet
	}
	if c.Grace == 0 {
		c.Grace = Grace
	}
	if c.Backoff == 0 {
		c.Backoff = watchBackoff
	}
	if c.Gap == 0 {
		c.Gap = watchGap
	}
	return c
}

type watchWorker struct {
	c       Watch
	spec    Spec
	command string
	args    []string
	events  chan Event
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	res     Result
	last    string // the last paragraph, to say a new one only
	told    bool   // the lead was last told that the command still watches

	finished atomic.Bool // Finish ended it: done, not stopped

	mu   sync.Mutex
	proc *process      // the command while it runs
	held chan struct{} // while paused; closed when resumed
}

func (w *watchWorker) Events() <-chan Event { return w.events }
func (w *watchWorker) Stop()                { w.cancel() }

// Finish stops the command, as Stop does, and ends the watcher done: its work is over.
func (w *watchWorker) Finish() {
	w.finished.Store(true)
	w.cancel()
}
func (w *watchWorker) Wait() Result { <-w.done; return w.res }

// Send refuses: a watcher reads no message.
func (w *watchWorker) Send(string) error {
	return fmt.Errorf("%w: a watcher runs a command, and reads no message", ErrClosed)
}

// PID is the process of the command while it runs; 0 between two runs.
func (w *watchWorker) PID() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.proc == nil {
		return 0
	}
	return w.proc.cmd.Process.Pid
}

// Pause stops the command where it is; between two runs, the next one waits.
func (w *watchWorker) Pause() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.proc != nil {
		if err := w.proc.Pause(); err != nil {
			return err
		}
	}
	if w.held == nil {
		w.held = make(chan struct{})
	}
	return nil
}

// Resume lets the command go on.
func (w *watchWorker) Resume() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.proc != nil {
		if err := w.proc.Resume(); err != nil {
			return err
		}
	}
	if w.held != nil {
		close(w.held)
		w.held = nil
	}
	return nil
}

// loop runs the command, and again after each exit when the task restarts it, until it is stopped.
func (w *watchWorker) loop() {
	defer close(w.done)
	defer close(w.events)
	backoff := w.c.Backoff
	for {
		if !w.hold() {
			w.res = Result{}
			return
		}
		started := time.Now()
		p, err := startCommand(w.spec.Dir, w.args[0], w.args[1:], w.spec.Env, w.spec.Prefix, w.c.Grace, w.c.Items)
		if err != nil {
			w.res = Result{ExitCode: -1, Err: err}
			return
		}
		// A watcher reads no message: its command reads an empty input, and Djinn never writes to it.
		_ = p.stdin.Close()
		w.running(p)
		res := w.read(p)
		w.running(nil)
		if w.finished.Load() {
			w.res = Result{}
			return
		}
		if w.ctx.Err() != nil || !w.spec.Restart {
			w.res = res
			return
		}
		ran := time.Since(started)
		var wait time.Duration
		if res.Err != nil || res.ExitCode != 0 {
			if ran > watchMaxBackoff {
				backoff = w.c.Backoff
			}
			wait = max(backoff, w.c.Gap)
			backoff = min(2*backoff, watchMaxBackoff)
			why := fmt.Sprintf("exit code %d", res.ExitCode)
			if res.Err != nil {
				why = res.Err.Error()
			}
			w.events <- Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
				Text: fmt.Sprintf("the command failed (%s); it starts again in %s", why, wait.Round(time.Millisecond))}
		} else {
			// Its loop: it starts again without a word, its paragraphs say it lives.
			backoff = w.c.Backoff
			wait = max(0, w.c.Gap-ran)
		}
		select {
		case <-time.After(wait):
		case <-w.ctx.Done():
			w.res = res
			if w.finished.Load() {
				w.res = Result{}
			}
			return
		}
	}
}

// hold waits while the watcher is paused between two runs; false once it is stopped.
func (w *watchWorker) hold() bool {
	for {
		w.mu.Lock()
		held := w.held
		w.mu.Unlock()
		if held == nil {
			return w.ctx.Err() == nil
		}
		select {
		case <-held:
		case <-w.ctx.Done():
			return false
		}
	}
}

// running sets the command that runs now; one started while the watcher is paused holds still at once.
func (w *watchWorker) running(p *process) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.proc = p
	if p != nil && w.held != nil {
		_ = p.Pause()
	}
}

// read makes paragraphs of what the command prints, until it ends; stopping the watcher stops it.
func (w *watchWorker) read(p *process) Result {
	var lines, raw []string
	quiet := time.NewTimer(w.c.Quiet)
	quiet.Stop()
	stop := w.ctx.Done()
	flush := func(watching bool) {
		quiet.Stop()
		if len(lines) > 0 {
			w.paragraph(lines, raw, watching)
		}
		lines, raw = nil, nil
	}
	for {
		select {
		case l, ok := <-p.lines:
			if !ok {
				<-p.done
				watching := w.spec.Restart && w.ctx.Err() == nil
				flush(watching)
				if !watching && w.told && w.ctx.Err() == nil {
					// Its last paragraph came before its exit, and said it watched: the lead learns it ended.
					w.told = false
					w.events <- Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
						Text:    fmt.Sprintf("the command ended (exit code %d)", p.res.ExitCode),
						Watched: &Watched{Command: w.command, Wake: true}}
				}
				return p.res
			}
			text := cleanLine(l.text)
			if text == "" {
				if w.c.Items {
					flush(true)
				}
				continue
			}
			lines, raw = append(lines, text), append(raw, l.text)
			if len(lines) >= watchLines {
				flush(true)
				continue
			}
			quiet.Reset(w.c.Quiet)
		case <-quiet.C:
			flush(true)
		case <-stop:
			stop = nil
			p.Stop()
		}
	}
}

// paragraph says a paragraph of the command, as text, and whether it wakes the lead.
func (w *watchWorker) paragraph(lines, raw []string, watching bool) {
	text := strings.Join(lines, "\n")
	wake := text != w.last && w.ctx.Err() == nil
	w.last = text
	if wake {
		w.told = watching
	}
	// The harness reads every event until the channel closes: one of a stopped watcher still goes through.
	w.events <- Event{
		Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, Text: text, Raw: strings.Join(raw, "\n"),
		Watched: &Watched{First: lines[0], Command: w.command, Watching: watching, Wake: wake},
	}
}

// ansi matches the escape sequences of a terminal: colours, cursor moves, titles.
var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// cleanLine is a line of a command as a person reads it: without escape sequences nor control characters.
func cleanLine(s string) string {
	s = ansi.ReplaceAllString(s, "")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s))
}

// splitCommand splits a command line into words, as a shell does without running anything: spaces separate them,
// single quotes keep what they hold as it is, double quotes keep spaces, and outside single quotes a backslash
// keeps a quote, a backslash or a space that follows it; any other backslash stays, as in a Windows path. No pipe,
// redirection nor variable: run a shell for them.
func splitCommand(line string) ([]string, error) {
	var out []string
	var word strings.Builder
	inWord, quote, escaped := false, rune(0), false
	runes := []rune(line)
	for i, r := range runes {
		switch {
		case escaped:
			word.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'' && i+1 < len(runes) && strings.ContainsRune(`"'\ `, runes[i+1]):
			escaped, inWord = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case unicode.IsSpace(r):
			if inWord {
				out = append(out, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("the command %q ends inside a quote", line)
	}
	if inWord {
		out = append(out, word.String())
	}
	return out, nil
}

// watching tells whether the task is a watcher.
func watching(t *planv1.Task) bool { return dispatch.Watcher(t) }

// TellFunc types a line in the terminal of the lead of a wish: plan.Wishes.Tell.
type TellFunc func(ctx context.Context, wishID, line string) error

// WatchedFunc reads a new paragraph a watcher printed, and says whether it holds the done line of its wish's
// template: plan.Wishes.Watched, which then offers to grant the wish.
type WatchedFunc func(ctx context.Context, task *planv1.Task, text string) (done bool, err error)

// OnWatched gives each new paragraph of a watcher to f, after its lead is told. djinn up gives it once.
func (h *Harness) OnWatched(f WatchedFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.watched = f
}

// SpawnWatcher starts a watcher task in the wish wishID, in the project projectID, on the command line watch: the
// watcher of a wish made from a template (plan.SpawnWatcher). It gives the task's code.
func (h *Harness) SpawnWatcher(ctx context.Context, wishID, projectID, title, watch string, restart bool) (string, error) {
	task, err := h.Spawn(ctx, planv1connect.TaskServiceSpawnProcedure, &planv1.TaskServiceSpawnRequest{
		WishId: wishID, ProjectId: projectID, Title: title, Prompt: watch, Provider: planv1.Provider_PROVIDER_WATCH,
		Restart: restart,
	})
	return task.GetCode(), err
}

// TellLeads lets the watchers wake the leads of their wishes with tell. djinn up gives it once its terminals run;
// without it, a watcher only records what its command prints.
func (h *Harness) TellLeads(tell TellFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tell = tell
}

// wakeLead tells the lead of the run's wish what its watcher's command printed, then gives the paragraph text to
// OnWatched's function: on its template's done line, a watcher that restarts its command ends, done. A wish without
// a lead to tell keeps the paragraph in its task's events.
func (h *Harness) wakeLead(r *run, w *Watched, text string) {
	h.mu.Lock()
	tell, watched := h.tell, h.watched
	h.mu.Unlock()
	if !w.Wake {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if tell != nil {
		if err := tell(ctx, r.task.GetWishId(), WatchLine(r.task.GetCode(), w)); err != nil && !errors.Is(err, plan.ErrNoLead) {
			h.Note(r.id, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_LOG, Text: "the lead was not told: " + err.Error()})
		}
	}
	if watched == nil || w.First == "" {
		return
	}
	done, err := watched(ctx, r.task, text)
	if err != nil {
		h.Note(r.id, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_LOG, Text: "the paragraph was not read: " + err.Error()})
	}
	if f, ok := r.worker.(interface{ Finish() }); done && r.task.GetRestart() && ok {
		f.Finish() // Its work is done: the command would only say so again.
	}
}

// WatchLine is the line that tells a lead what its watcher's command printed: the first line of the paragraph, and
// whether the command still watches; or that the command ended, after a paragraph that said it still watched.
func WatchLine(code string, w *Watched) string {
	if w.First == "" {
		return fmt.Sprintf("Djinn: %s's watcher: %s has ended.", code, clipRunes(w.Command, watchCommandMax))
	}
	first := strings.TrimRight(clipRunes(w.First, watchLineMax), ". ")
	if !strings.HasSuffix(first, "…") {
		first += "."
	}
	tail := "is still watching"
	if !w.Watching {
		tail = "has ended"
	}
	return fmt.Sprintf("Djinn: %s's watcher says: %s %s %s.", code, first, clipRunes(w.Command, watchCommandMax), tail)
}

// clipRunes keeps at most n characters of s, an ellipsis replacing the rest.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// watchText says, in a watcher's start event, what it is, after where it runs.
func watchText(t *planv1.Task) string {
	if t.GetRestart() {
		return ": no agent, no model; its command starts again after each exit"
	}
	return ": no agent, no model"
}
