package harness

// A watcher is a task without an agent: Djinn runs a command in the project's folder, and each paragraph it prints
// wakes the wish's lead with one line. No model reads its output, and it spends no token. It takes no slot of the
// machine (internal/dispatch): it sleeps until its command prints.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
)

// What a watcher waits for, by default.
const (
	// WatchQuiet is how long a command stays silent before what it printed makes a paragraph.
	WatchQuiet = time.Second
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
// worker's folder, under the worker's environment and prefix; quotes group words, as in a shell, and the harness
// expanded Djinn's variables in it before (watchCommand). Where the worker's
// permissions list the commands it may run, the command must be one of them, and not denied: a watcher has no
// reviewer. The harness adds the command a skill of the project declares, or the developer allows (watch_allow.go).
// It cannot run read-only.
type Watch struct {
	// Quiet is how long the command stays silent before what it printed makes a paragraph; 0 for WatchQuiet.
	Quiet time.Duration
	// Grace is how long the command has to stop before it is killed; 0 for Grace.
	Grace time.Duration
	// Backoff is the first wait before a command that died starts again; 0 for watchBackoff.
	Backoff time.Duration
	// Gap is the least time between two starts of a command that exits 0 at once, and between two starts of one
	// that fails; 0 for one second.
	Gap time.Duration
	// Items ends a paragraph on an empty line too, and leaves the error output out of them: each one is an item of an
	// inbox source.
	Items bool
	// Endless starts the command again however many times it dies in a row: an inbox source never gives up.
	Endless bool
	// Runs says the end of each run of the command, by an event with Ran: an inbox source keeps its last run.
	Runs bool
}

// Ran is the end of a run of a watcher's command, on its event when Watch.Runs is set. The paragraphs of that run
// came before it.
type Ran struct {
	// End is when it ended, or could not start.
	End time.Time
	// Err is why it failed: its exit code and the last lines of its error output, or why it could not start; nil
	// when it exited 0.
	Err error
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
	// Wake says the wish's lead is told: not when the paragraph says again what the last one said, even before Djinn
	// started the watcher again (Spec.LastLine), nor when the watcher was stopped.
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
	go func() {
		defer logx.Crash("watch", "command", command)
		w.loop()
	}()
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
	last    string   // the last paragraph, to say a new one only
	told    bool     // the lead was last told that the command still watches
	errs    []string // the last lines of the command's error output, in its last run

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

// Cgroup is the cgroup of the command's scope while it runs: a scope of its own each run.
func (w *watchWorker) Cgroup() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.proc == nil {
		return ""
	}
	return w.proc.cgroup
}

// Pause stops the command where it is; between two runs, the next one waits. A command that just ended, not yet
// cleared (running), is between two runs.
func (w *watchWorker) Pause() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.proc != nil {
		if err := w.proc.Pause(); err != nil && !errors.Is(err, os.ErrProcessDone) {
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
		if err := w.proc.Resume(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
	}
	if w.held != nil {
		close(w.held)
		w.held = nil
	}
	return nil
}

// SetPriority sets the priority of the running command.
func (w *watchWorker) SetPriority(low bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.proc != nil {
		return w.proc.SetPriority(low)
	}
	return nil
}

// loop runs the command, and again as the Lamplighter says (watcherLife): after it dies, and after each exit when the
// task restarts it, until it is stopped, finished, or it died watchTries times in a row.
func (w *watchWorker) loop() {
	defer close(w.done)
	defer close(w.events)
	life := newWatcherLife(w.c.Backoff, w.c.Endless)
	for {
		if !w.hold() {
			w.res = Result{}
			return
		}
		started := time.Now()
		var res Result
		var wait time.Duration
		again := false
		next := func(r Result) bool {
			wait, again = life.ended(r, w.spec.Restart, time.Since(started))
			return again
		}
		w.errs = nil
		p, err := startCommand(w.spec.Dir, w.args[0], w.args[1:], w.spec.Env, w.spec.Scope, w.c.Grace, w.c.Items, w.spec.LowPriority, w.spec.Track)
		if err != nil {
			res = Result{ExitCode: -1, Err: err}
			next(res)
		} else {
			// A watcher reads no message: its command reads an empty input, and Djinn never writes to it.
			_ = p.stdin.Close()
			w.running(p)
			res = w.read(p, next)
			w.running(nil)
		}
		if w.finished.Load() {
			w.res = Result{}
			return
		}
		if w.c.Runs && w.ctx.Err() == nil {
			ran := &Ran{End: time.Now()}
			if died(res) {
				ran.Err = errors.New(death(res, w.errs))
			}
			w.events <- Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Ran: ran}
		}
		if w.ctx.Err() != nil || !again {
			w.res = res
			if w.ctx.Err() == nil && died(res) {
				w.res.Err = gaveUp(res, w.errs)
			}
			return
		}
		if died(res) {
			wait = max(wait, w.c.Gap)
			why := fmt.Sprintf("exit code %d", res.ExitCode)
			if res.Err != nil {
				why = res.Err.Error()
			}
			if !life.endless {
				why += fmt.Sprintf(", %d of %d in a row", life.deaths, watchTries)
			}
			w.events <- Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
				Text: fmt.Sprintf("the command failed (%s); it starts again in %s", why, wait.Round(time.Millisecond))}
		} else {
			// Its loop: it starts again without a word, its paragraphs say it lives.
			wait = max(0, w.c.Gap-time.Since(started))
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
		if err := p.Pause(); err != nil {
			logx.L().Warn("hold a watcher's command", "loop", "watch", "command", w.command, "err", err)
		}
	}
}

// read makes paragraphs of what the command prints, until it ends; stopping the watcher stops it. next says, at its
// end, whether the command starts again: its last paragraph says it still watches.
func (w *watchWorker) read(p *process, next func(Result) bool) Result {
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
				watching := next(p.res) && w.ctx.Err() == nil
				flush(watching)
				if !watching && w.told && w.ctx.Err() == nil && !died(p.res) {
					// Its last paragraph came before its exit, and said it watched: the lead learns it ended. A watcher
					// that died for good tells its lead why, once, as it fails (tellWatcherFailed).
					w.told = false
					w.events <- Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
						Text:    fmt.Sprintf("the command ended (exit code %d)", p.res.ExitCode),
						Watched: &Watched{Command: w.command, Wake: true}}
				}
				return p.res
			}
			text := cleanLine(l.text)
			if l.stderr && text != "" {
				w.errs = append(w.errs, clipRunes(text, watchLineMax))
				if len(w.errs) > watchErrLines {
					w.errs = w.errs[1:]
				}
			}
			if l.stderr && w.c.Items {
				// An inbox source's error output makes no item: its run keeps the last lines, as why it failed.
				continue
			}
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
	if w.last == "" && clipRunes(lines[0], watchLineMax) == w.spec.LastLine {
		// The first paragraph since Djinn started it again says what its task said last: the lead knows it already.
		wake = false
	}
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
// redirection nor variable (but Djinn's own, which expandWatch expanded before): run a shell for them.
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

// watchVars are the variables Djinn expands in a watcher's command line (expandWatch), in the order a refusal names
// them.
var watchVars = []string{"DJINN_HOME", "DJINN_TASK_ID", "DJINN_WISH_ID"}

// watchCommand is the command line the watcher t runs: its own, $DJINN_HOME, $DJINN_TASK_ID and $DJINN_WISH_ID
// expanded as a shell would (expandWatch). What its allowance judges, and what Watch.Start splits.
func (h *Harness) watchCommand(t *planv1.Task, command string) (string, error) {
	return expandWatch(strings.TrimSpace(command), map[string]string{
		"DJINN_HOME": h.home, "DJINN_TASK_ID": t.GetId(), "DJINN_WISH_ID": t.GetWishId(),
	})
}

// expandWatch expands in line the variables of vars, $NAME or ${NAME}, as a shell does, outside single quotes: a
// lead writes "$DJINN_HOME/watchers/$DJINN_TASK_ID/x" as it would in a terminal, and a watcher runs its command
// without a shell (W7 of wish Dew). Each value stays one word as splitCommand reads it, quoted when it must be;
// the rest of the line is kept as it is. Any other variable is refused, with how to write the command instead: no
// shell would expand it, and the command would read it literally. A $ before anything but a name, or after a
// backslash, stays as it is.
func expandWatch(line string, vars map[string]string) (string, error) {
	var out strings.Builder
	quote := rune(0)
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\\' && quote != '\'' && i+1 < len(runes):
			out.WriteRune(r)
			out.WriteRune(runes[i+1])
			i++
			continue
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '\'' || r == '"'):
			quote = r
		case r == '$' && quote != '\'':
			name, n := varName(runes[i+1:])
			if n == 0 {
				break
			}
			value, ok := vars[name]
			if !ok {
				return "", unknownWatchVar(line, name)
			}
			out.WriteString(watchWord(value, quote == '"'))
			i += n
			continue
		}
		out.WriteRune(r)
	}
	return out.String(), nil
}

// varName is the name of the variable rest starts with, after its $: NAME or {NAME}, and how many runes it takes;
// 0 when rest starts with no name.
func varName(rest []rune) (string, int) {
	braced := len(rest) > 0 && rest[0] == '{'
	start := 0
	if braced {
		start = 1
	}
	end := start
	for end < len(rest) && (rest[end] == '_' || rest[end] < unicode.MaxASCII &&
		(unicode.IsLetter(rest[end]) || end > start && unicode.IsDigit(rest[end]))) {
		end++
	}
	switch {
	case end == start:
		return "", 0
	case !braced:
		return string(rest[start:end]), end
	case end < len(rest) && rest[end] == '}':
		return string(rest[start:end]), end + 1
	}
	return "", 0
}

// watchWord writes value so that splitCommand reads it as it is, inside double quotes or outside any: as it is when
// nothing in it would be read otherwise, single-quoted when something would (a space outside quotes, on macOS's
// Application Support; a backslash before a quote, a space or another backslash, on Windows).
func watchWord(value string, double bool) string {
	runes := []rune(value)
	plain := true
	for i, r := range runes {
		switch {
		case r == '"', !double && (r == '\'' || unicode.IsSpace(r)):
			plain = false
		case r == '\\' && (i+1 == len(runes) || strings.ContainsRune(`"'\ `, runes[i+1])):
			plain = false
		}
	}
	if plain {
		return value
	}
	quoted := "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
	if double {
		return `"` + quoted + `"`
	}
	return quoted
}

// unknownWatchVar refuses the command line of a watcher that holds $name, a variable Djinn does not expand.
func unknownWatchVar(line, name string) error {
	known := make([]string, len(watchVars))
	for i, v := range watchVars {
		known[i] = "$" + v
	}
	return fmt.Errorf("the watcher's command %q holds $%s, which nothing would expand: a watcher runs its command "+
		"without a shell, and Djinn expands only %s in it. Write the value of $%s instead, or put it in single quotes "+
		"for the command to read it as it is", line, name, strings.Join(known, ", "), name)
}

// watching tells whether the task is a watcher.
func watching(t *planv1.Task) bool { return dispatch.Watcher(t) }

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

// wakeLead tells the lead of the run's wish what its watcher's command printed, then gives the paragraph text to
// OnWatched's function: on its template's done line, or its babysit's, the watcher ends, done. A wish without a lead to tell keeps the
// paragraph in its task's events.
func (h *Harness) wakeLead(r *run, w *Watched, text string) {
	h.mu.Lock()
	watched := h.watched
	h.mu.Unlock()
	if !w.Wake {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := h.toLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_WATCH, r.task.GetWishId(), r.task.GetCode(), WatchLine(r.task.GetCode(), w))
	if err != nil && !errors.Is(err, plan.ErrNoLead) {
		h.Note(r.id, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_LOG, Text: "the lead was not told: " + err.Error()})
	}
	if w.First == "" {
		return
	}
	// A babysit's done line ends it, in any wish; a template's asks whether to grant the wish too.
	done := plan.DoneLineOf(r.task.GetBabysit().GetDoneWhen(), text) != ""
	if watched != nil {
		grant, err := watched(ctx, r.task, text)
		if err != nil {
			h.Note(r.id, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_LOG, Text: "the paragraph was not read: " + err.Error()})
		}
		done = done || grant
	}
	if f, ok := r.worker.(interface{ Finish() }); done && ok {
		f.Finish() // Its work is done: the command would only say so again, or wait for nothing.
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
	return ": no agent, no model; its command starts again if it dies"
}
