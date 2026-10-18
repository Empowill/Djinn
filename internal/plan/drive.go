package plan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// The lead Djinn drives (decision Q31, W131): no terminal and no djinn lead watch. Djinn waits for the lead's events
// as a watch does, then starts a fresh headless session for the batch (harness.LeadDriver) whose prompt holds the
// short rules of the lead, the events whole, the briefs of their wishes and the lead's journal; the session acts with
// djinn's commands, ends its turn, and Djinn lets it go. What the developer writes to the Djinn is an event of the
// queue too. The batches, stored, are the lead's journal.

// methodLeadBatch is the journal's method for a batch of the driven lead stored or ended; the request is the batch.
const methodLeadBatch = "lead/batch"

const (
	// DrivenRulesVersion names the text of DrivenRules: every batch's session reads the version it got.
	DrivenRulesVersion = "4"
	// batchEventsMax is the most events one batch takes; the others go to the next batch, at once.
	batchEventsMax = 40
	// JournalMax is how many of the latest batches of its wishes a batch's prompt carries.
	JournalMax = 6
	// journalLineMax is how many characters of a command or of a batch's last text the journal keeps.
	journalLineMax = 200
	// JournalCommandsMax is how many of a batch's commands the journal keeps, the first ones.
	JournalCommandsMax = 20
	// journalEventsMax is how many of a batch's events the journal shows.
	journalEventsMax = 12
)

// LeadFolder is the folder a driven lead's sessions run in, in Djinn's data folder: no project's rules nor memory,
// and the plan files and tilasm folders the lead writes.
func LeadFolder(home string) string { return filepath.Join(home, "lead") }

// LeadDrive is what Djinn knows of the lead it drives, in memory: shared by every service of a store, kept by the
// driver (harness.LeadDriver).
type LeadDrive struct {
	mu      sync.Mutex
	batch   *planv1.LeadBatch // the batch that runs, or else the last one
	failure string            // why the last batch failed twice; empty once a batch ends its turn
	limit   *planv1.LeadLimit // the usage limit the last batch hit; nil once a batch ends its turn, or the lead moves
	retry   chan struct{}     // closed when the developer asks to try the waiting events again
	changed func()            // tells the window that the lead changed
}

// leadDrives holds the LeadDrive of each store whose lead Djinn drives.
var leadDrives sync.Map // *store.Store -> *LeadDrive

// DriveLead makes the lead of s's Djinn driven: LeadService runs no terminal for it. changed tells the window that
// the lead changed: a batch started, ended or failed.
func DriveLead(s *store.Store, changed func()) *LeadDrive {
	d, _ := leadDrives.LoadOrStore(s, &LeadDrive{retry: make(chan struct{})})
	drive := d.(*LeadDrive)
	drive.mu.Lock()
	drive.changed = changed
	drive.mu.Unlock()
	return drive
}

// drive is the lead Djinn drives on w's store; nil when its lead runs in a terminal.
func (w *Wishes) drive() *LeadDrive {
	if w == nil || w.Store == nil {
		return nil
	}
	d, ok := leadDrives.Load(w.Store)
	if !ok {
		return nil
	}
	return d.(*LeadDrive)
}

// SetBatch records b as the batch that runs, or the last one.
func (d *LeadDrive) SetBatch(b *planv1.LeadBatch) {
	d.mu.Lock()
	d.batch = proto.CloneOf(b)
	if b.GetState() == planv1.LeadBatchState_LEAD_BATCH_STATE_DONE {
		d.failure, d.limit = "", nil
	}
	changed := d.changed
	d.mu.Unlock()
	if changed != nil {
		changed()
	}
}

// Batch is the batch that runs, or else the last one; nil before the first.
func (d *LeadDrive) Batch() *planv1.LeadBatch {
	d.mu.Lock()
	defer d.mu.Unlock()
	return proto.CloneOf(d.batch)
}

// Fail records why a batch failed twice: the window shows it beside Lead until a batch ends its turn.
func (d *LeadDrive) Fail(why string) {
	d.mu.Lock()
	d.failure = why
	changed := d.changed
	d.mu.Unlock()
	if changed != nil {
		changed()
	}
}

// Failure is why the last batch failed twice; empty when none did since a batch ended its turn.
func (d *LeadDrive) Failure() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.failure
}

// Retried is closed when the developer asks to try the waiting events again (Lead in the window, djinn lead
// resume).
func (d *LeadDrive) Retried() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.retry
}

// askRetry wakes the driver on the events that wait, as Lead does after a failure.
func (d *LeadDrive) askRetry() {
	d.mu.Lock()
	close(d.retry)
	d.retry = make(chan struct{})
	d.mu.Unlock()
}

// WaitLeadEvents waits until events wait for the lead, then for them to settle as a watch does (2 seconds of quiet,
// 10 at most), and returns those waiting, oldest first.
func (w *Wishes) WaitLeadEvents(ctx context.Context) ([]*planv1.LeadEvent, error) {
	for {
		events, err := w.waitEvents(ctx, watchMax)
		if err != nil || len(events) > 0 {
			return events, err
		}
	}
}

// SetLeadSettle sets how long the lead's events of s settle before a watch or a batch takes them: quiet, max at
// most. Tests shorten it.
func SetLeadSettle(s *store.Store, quiet, most time.Duration) {
	l := (&Wishes{Store: s}).leadWatch()
	l.mu.Lock()
	l.timing.quiet, l.timing.settleMax = quiet, most
	l.mu.Unlock()
}

// LeadEventsChanged is closed at the next event queued for the lead.
func (w *Wishes) LeadEventsChanged() <-chan struct{} { return w.leadWatch().changed() }

// BatchEvents is the part of events one batch takes, the oldest.
func BatchEvents(events []*planv1.LeadEvent) []*planv1.LeadEvent {
	return events[:min(len(events), batchEventsMax)]
}

// PutLeadBatch stores b, journaled.
func PutLeadBatch(ctx context.Context, s *store.Store, b *planv1.LeadBatch) error {
	return s.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, methodLeadBatch, b); err != nil {
			return err
		}
		return tx.Put(b)
	})
}

// EndLeadBatch stores b as it ended. A batch done acknowledges its events still waiting for its session, as a watch
// would, in the same transaction; the events and batches older than leadEventsKept go.
func EndLeadBatch(ctx context.Context, s *store.Store, b *planv1.LeadBatch, now time.Time) error {
	return s.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, methodLeadBatch, b); err != nil {
			return err
		}
		if err := tx.Put(b); err != nil {
			return err
		}
		if b.GetState() != planv1.LeadBatchState_LEAD_BATCH_STATE_DONE {
			return nil
		}
		for _, id := range b.GetEventIds() {
			e, err := store.Get[*planv1.LeadEvent](ctx, tx, id)
			if errors.Is(err, store.ErrNotFound) {
				continue // Replaced meanwhile by a newer event of the same answer: the next batch takes that one.
			}
			if err != nil {
				return err
			}
			if e.GetAckSessionId() != "" {
				continue
			}
			e.AckSessionId, e.AckTime = b.GetSessionId(), timestamppb.New(now)
			if err := tx.Put(e); err != nil {
				return err
			}
			if e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER {
				if err := answerReached(ctx, tx, e.GetCode(), e.GetWishId()); err != nil {
					return err
				}
			}
		}
		events, err := store.List[*planv1.LeadEvent](ctx, tx, nil)
		if err != nil {
			return err
		}
		for _, e := range events {
			if e.GetAckTime() != nil && now.Sub(e.GetAckTime().AsTime()) > leadEventsKept {
				if err := tx.Delete(e); err != nil {
					return err
				}
			}
		}
		batches, err := store.List[*planv1.LeadBatch](ctx, tx, nil)
		if err != nil {
			return err
		}
		for _, old := range batches {
			if old.GetEndTime() != nil && now.Sub(old.GetEndTime().AsTime()) > leadEventsKept {
				if err := tx.Delete(old); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// LeadJournal is the latest batches, at most n, oldest first: those about one of wishIDs, or every one when wishIDs
// is empty. A batch about no wish (a restart alone) belongs to every journal.
func LeadJournal(ctx context.Context, r store.Reader, wishIDs []string, n int) ([]*planv1.LeadBatch, error) {
	all, err := store.List[*planv1.LeadBatch](ctx, r, nil)
	if err != nil {
		return nil, err
	}
	var out []*planv1.LeadBatch
	for _, b := range all {
		if len(wishIDs) == 0 || len(b.GetWishIds()) == 0 || slices.ContainsFunc(b.GetWishIds(), func(id string) bool {
			return slices.Contains(wishIDs, id)
		}) {
			out = append(out, b)
		}
	}
	slices.SortFunc(out, func(a, b *planv1.LeadBatch) int { return cmp.Compare(a.GetId(), b.GetId()) })
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
}

// EventWishes are the wishes events are about, in their order, each once.
func EventWishes(events []*planv1.LeadEvent) []string {
	var ids []string
	for _, e := range events {
		if id := e.GetWishId(); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// JournalEntry writes batch b of the lead's journal: when and who, the events it took, the commands it ran, the text
// that ended its turn and what it cost.
func JournalEntry(b *planv1.LeadBatch) string {
	var s strings.Builder
	who := providerName(b.GetProvider())
	if b.GetModel() != "" {
		who += " " + b.GetModel()
	}
	if b.GetAccount() != "" {
		who += " (account " + b.GetAccount() + ")"
	}
	state := strings.ToLower(strings.TrimPrefix(b.GetState().String(), "LEAD_BATCH_STATE_"))
	fmt.Fprintf(&s, "### %s · %s, session `%s`, attempt %d · %s", when(b.GetStartTime().AsTime()), who,
		b.GetSessionId(), max(b.GetAttempt(), 1), state)
	if u := b.GetUsage(); u.GetCostUsd() > 0 {
		fmt.Fprintf(&s, " · $%.2f", u.GetCostUsd())
	}
	s.WriteString("\n\n")
	if b.GetError() != "" {
		fmt.Fprintf(&s, "Failed: %s\n\n", clipRunes(oneLine(b.GetError()), journalLineMax))
	}
	if len(b.GetEvents()) > 0 {
		s.WriteString("Events:\n")
		events := b.GetEvents()
		for _, e := range events[:min(len(events), journalEventsMax)] {
			fmt.Fprintf(&s, "- %s\n", e)
		}
		if more := len(events) - journalEventsMax; more > 0 {
			fmt.Fprintf(&s, "- and %d more\n", more)
		}
		s.WriteString("\n")
	}
	if len(b.GetCommands()) > 0 {
		s.WriteString("Ran:\n")
		for _, c := range b.GetCommands() {
			fmt.Fprintf(&s, "- `%s`\n", strings.ReplaceAll(c, "`", "'"))
		}
		s.WriteString("\n")
	}
	if note := strings.TrimSpace(b.GetNote()); note != "" {
		fmt.Fprintf(&s, "Said at the end: %s\n\n", clipRunes(oneLine(note), journalLineMax))
	}
	return s.String()
}

// JournalCommand is a command of the lead as the journal keeps it: one line, cut.
func JournalCommand(command string) string { return clipRunes(oneLine(command), journalLineMax) }

// JournalNote is the text that ended a batch's turn, as the journal keeps it.
func JournalNote(text string) string { return clipRunes(strings.TrimSpace(text), 4*journalLineMax) }

// LeadPacket is the first message of a batch's session (decision Q31): its events whole, the briefs of the wishes
// they are about, the active wishes in a line each, and the lead's journal, the latest batches of those wishes.
// home is Djinn's data folder. The rules go apart, the same for every batch (DrivenRules).
func LeadPacket(
	ctx context.Context, r store.Reader, home string, events []*planv1.LeadEvent, journal []*planv1.LeadBatch,
	attempt int32,
) (string, error) {
	var b strings.Builder
	b.WriteString("# A batch for the Djinn\n\n")
	fmt.Fprintf(&b, "Djinn started this session for one batch of %d event(s), queued from %s to %s. Act on each, "+
		"then end your turn: the rules of the driven lead (%s) say how.", len(events),
		when(events[0].GetCreateTime().AsTime()), when(events[len(events)-1].GetCreateTime().AsTime()), DrivenRulesVersion)
	if attempt > 1 {
		b.WriteString(" The first session of this batch failed (the journal says what it ran before it did): do not " +
			"do again what it did.")
	}
	if tone := toneSection(ReadShownTheme(home)); tone != "" {
		b.WriteString("\n\n" + tone)
	}
	b.WriteString("\n\n## Events\n\n")
	titles := map[string]string{}
	for _, id := range EventWishes(events) {
		if wish, err := store.Get[*planv1.Wish](ctx, r, id); err == nil {
			titles[id] = oneLine(wish.GetTitle())
		}
	}
	for _, e := range events {
		b.WriteString(eventEntry(e, titles))
	}
	b.WriteString("## The lead's journal\n\n")
	if len(journal) == 0 {
		b.WriteString("No batch before this one for these wishes.\n\n")
	} else {
		b.WriteString("What the leads of the latest batches of these wishes did, the oldest first.\n\n")
		for _, j := range journal {
			b.WriteString(JournalEntry(j))
		}
	}
	for _, id := range EventWishes(events) {
		brief, err := BuildBrief(ctx, r, home, id)
		if errors.Is(err, store.ErrNotFound) {
			continue // Deleted since its event.
		}
		if err != nil {
			return "", err
		}
		b.WriteString(strings.TrimRight(brief.Moving, "\n") + "\n\n")
	}
	if err := activeWishesLines(ctx, r, &b); err != nil {
		return "", err
	}
	return b.String(), nil
}

// eventEntry is an event of a batch whole: its kind and code, its wish by title and identifier, its text, its note
// and its detail uncut.
func eventEntry(e *planv1.LeadEvent, titles map[string]string) string {
	var b strings.Builder
	kind := strings.ToLower(strings.TrimPrefix(e.GetKind().String(), "LEAD_EVENT_KIND_"))
	b.WriteString("### ")
	if e.GetCode() != "" {
		b.WriteString(e.GetCode() + " ")
	}
	b.WriteString(kind)
	if id := e.GetWishId(); id != "" {
		title := titles[id]
		if title == "" {
			title = "a wish deleted since"
		}
		fmt.Fprintf(&b, " · wish %s (`%s`)", title, id)
	}
	fmt.Fprintf(&b, " · %s\n\n", when(e.GetCreateTime().AsTime()))
	b.WriteString(strings.TrimSpace(strings.TrimPrefix(e.GetText(), "Djinn: ")) + "\n\n")
	if note := strings.TrimSpace(e.GetNote()); note != "" && !strings.Contains(oneLine(e.GetText()), oneLine(note)) {
		b.WriteString("The note, whole:\n\n")
		for line := range strings.SplitSeq(note, "\n") {
			b.WriteString(strings.TrimRight("> "+line, " ") + "\n")
		}
		b.WriteString("\n")
	}
	if detail := strings.TrimSpace(e.GetDetail()); detail != "" {
		b.WriteString(detail + "\n\n")
	}
	return b.String()
}

// activeWishesLines writes the active wishes in a line each: where a message about none of the batch's wishes goes.
func activeWishesLines(ctx context.Context, r store.Reader, b *strings.Builder) error {
	wishes, err := store.List[*planv1.Wish](ctx, r, nil)
	if err != nil {
		return err
	}
	slices.SortFunc(wishes, func(x, y *planv1.Wish) int { return cmp.Compare(x.GetRank(), y.GetRank()) })
	b.WriteString("## The active wishes\n\n")
	n := 0
	for _, w := range wishes {
		if w.GetState() != planv1.WishState_WISH_STATE_ACTIVE && w.GetState() != planv1.WishState_WISH_STATE_UNSPECIFIED {
			continue
		}
		fmt.Fprintf(b, "- %s (`%s`), rank %d\n", oneLine(w.GetTitle()), w.GetId(), w.GetRank())
		n++
	}
	if n == 0 {
		b.WriteString("None.\n")
	}
	return nil
}

// DrivenRules are the rules of the lead Djinn drives (decision Q31): the brief's rules made short for a session that
// takes one batch, then ends. Every batch's session reads them apart from its message, the same text each time, so
// that its cache serves the next session. A change here is a new DrivenRulesVersion.
func DrivenRules(projects []*planv1.Project, home string) string {
	return "# Leading in Djinn: the driven lead, rules " + DrivenRulesVersion + "\n\n" +
		"Djinn holds the active wishes and their plans: tasks, questions, decisions and blocks. You lead them: you " +
		"split the work into tasks for workers, talk with the developer through questions, and keep the plans true, " +
		"with the `djinn` command, never in its data folder. In Djinn the lead is called the Djinn: you are the " +
		"Djinn, the developer's single voice for every wish; workers are your hands, never Djinns. Speak of yourself " +
		"that way in questions and blocks (\"the Djinn recommends…\").\n\n" +
		"- **The developer reads none of your text.** No terminal shows this session: your last text reaches only " +
		"the next leads, through the journal. Every question, doubt or choice you have goes into a question " +
		"(`djinn question ask`) in the wish concerned, never only into your text; a fact or a decision for the next " +
		"leads and workers goes into a block. End your turn with one short line for the journal, what you did and " +
		"what you wait for (\"Spawned W12 for Q3's answer; waits for it\"), or with nothing.\n" +
		"- **Djinn drives you, one batch at a time.** Djinn started this session for one batch: its message holds the " +
		"events whole, the briefs of their wishes, the active wishes and the lead's journal, what the leads of the " +
		"previous batches did. Act on every event of the batch, then end your turn: Djinn lets the session go, and a " +
		"fresh one takes the next batch. Nothing carries over but the plan and the journal: what the next lead must " +
		"know goes in the plan (a question, a block, a task's prompt) or in the line that ends your turn. Never run " +
		"`djinn lead watch`, never wait nor poll for what comes later: it comes in a next batch.\n" +

		"- **A message is the developer's words.** A `message` event is what the developer wrote to the Djinn in the " +
		"window: a request like any other, about the wish it names or none. Act on it, and answer them in the window's " +
		"bubble with `djinn lead say <wish> \"<text>\"` (at most 600 characters; for a longer answer, write a report " +
		"block and name it there), never in your text; tell a decision you took alone that the developer would want to " +
		"know with `djinn lead say <wish> \"<text>\" --decided` instead of a question that asks nothing, and never say " +
		"that a worker or a task started. A decision the developer's message states gets a block of kind decision.\n" +
		"- **Ask, do not guess.** Everything for the developer is a question, `djinn question ask`, with its options " +
		"and your recommendation; the analysis behind it goes in its `--context`. Every move only the developer can " +
		"make (push a tag, merge or open a pull request, create a release, try something on a machine Djinn does not " +
		"have, " + installMoveRule(projects, home) + ") is a `--move` question whose title is a call to action, with " +
		"the links and the commands it needs, the moment it is due; withdraw it once done or superseded. An answered " +
		"question is a decision: do not bring it back; ask again only on a real doubt, not understanding the answer " +
		"is one. A task that follows from a decision names it: `--decision Q03`.\n" +
		"- **Every question's title starts with its emoji**, the same in `--icon`: 🚀 a merge or a release, 🔎 a " +
		"review, 🔐 a right, 🧪 a check, 📦 a build, 🌿 a branch, 🧭 an organisation choice, 🪵 logs, ⚠️ an incident.\n" +
		"- **A question reads at a glance.** " + QuestionShape + "\n" +
		"- **Your tone follows the window's theme.** A batch's message gives it under \"Your tone\", the tone of the " +
		"theme the window showed as the batch started: the solar Djinn in light, the mocking demon of the lamp in " +
		"dark; without it, stay plain. Only the tone changes: your proposals stay rigorous, and facts, numbers, " +
		"commands, links, options and risks stay exact and plain. Humour lives only in a question's title (a move's " +
		"keeps its verb first), the end of its recommendation, one aside of a dozen words at most, and a `djinn lead " +
		"say` bubble: a reader who skips it loses nothing. Never in facts and their bullets, a code block, security or " +
		"rights, an incident or bad news, what leaves Djinn (a commit, a PR, a message for a colleague or a customer), " +
		"blocks for agents, workers' prompts. Aim it at the situation, the machine or yourself, never at a person. In " +
		"doubt, stay plain.\n" +
		"- **Never ask the developer to check.** A check, a measure, a log, a CI result, a command to run goes to a " +
		"task or a watcher, never to the developer. A question is only for a decision or a move only a person can " +
		"make.\n" +
		"- **A worker's blocking question waits for the developer.** " + WaitingQuestionRule + "\n" +
		"- **Refused calls are yours, rights stay explicit.** An `ended` or `failed` event names the calls a worker's " +
		"permissions refused: continue the task otherwise, or close it as seen (`djinn task done <task> --note " +
		"\"…\"`) and spawn it again where a right already covers them. A new right is one `--move` question naming " +
		"exactly that right and why.\n" +
		"- **Run direct `djinn` commands.** " + DirectCommandsRule + " " + DirectFlags + " Write files only in your " +
		"working folder (an option's `--plan-file`, a tilasm's folder), with your file tools, never with shell " +
		"commands.\n" +
		"- **Delegate.** Code, investigations, fixes and checks go to tasks, even small ones, several side by side. " +
		"Never read nor search source code yourself. A worker starts from a short prompt, five to twenty lines of " +
		"Markdown: Gain, Scope (and the open tasks' files it must not touch), Validation (the commit checks and the " +
		"targeted tests, never the whole suite), References by link, never copied. To follow up " +
		"on a task, continue it (`djinn task continue <task> --prompt \"…\"`); fork it only to start another task " +
		"from its context. Djinn tells a worker started again to rebase onto its target first: never ask for it.\n" +
		"- **The plan is a graph of azimas.** An azima (`T07`) carries one clear goal; spawn each task `--part-of " +
		"<azima>`, `--after` only the tasks whose result it needs, `--blocks W5` to put it before a planned task. " +
		"You own the wish's DAG: before a spawn, read the open work (`djinn task list --wish-id <wish> --open --short`) " +
		"and its overlaps (`djinn task overlaps <wish>`). Work on files an open task touches goes to that task " +
		"(`djinn task update` while planned, `djinn task send` while it runs) or `--after` it, never beside it; give " +
		"`--write-scopes` when you know the paths; big work is a chain of small steps, not a fan. The others run side " +
		"by side. Look for the azima new work belongs to before opening one. Never spawn while the wish's integration " +
		"points at another branch.\n" +
		"- **Operational decisions are yours.** A merge still failing after its corrections, a held push, a conflict " +
		"come to you as events: retry a correction, drop the work (`djinn task done <task> --drop`, `djinn task " +
		"cancel`), re-sequence (`djinn task depend`), or leave it waiting.\n" +
		"- **Act on the answers.** An answer whose plan Djinn launched says so; turn each other answer into tasks " +
		"yourself, in one pass, placed in the graph, or launch its plan (`djinn question launch <question>`). Plan " +
		"each option's task when you ask (`--plan-file` per option). The least doubt on what the developer meant is " +
		"a new question that quotes it. \"Enlighten me\" starts an investigator: check what it revised, do not do it " +
		"again.\n" +
		"- **Check every task that ends.** Its event holds its final note: what it leaves to verify goes to a task or a " +
		"watcher; an uncertainty it leaves (a choice made in the developer's place, a partial result) is a question; " +
		"withdraw the open questions its work made needless.\n" +
		"- **Blocks are for agents**: a hand-off, a reference, a decision taken outside a question (kind decision). " +
		"**A tilasm explains a concept**: a folder with an `index.html`, `djinn tilasm put <folder> --wish <wish>`, " +
		"cited by its `djinn://tilasm/<id>` link, its diagrams in inline SVG, no network. **No secret, no local path** " +
		"in the plan: name the project.\n" +
		"- **Every request finds its wish**: one about no wish of the batch goes through `djinn wish route " +
		"\"<request>\" --wish-id <wish> --ask`. **Watch, do not poll**: to wait on something outside, spawn a watcher " +
		"(`--provider watch --prompt \"<command>\"`). **When Djinn misbehaves**, stop the damage (stop workers, " +
		"redirect an integration, withdraw useless questions) and tell the developer in a question.\n" +
		"- **Read the lead's notes before you act in their area**: `djinn lead notes <name>`, indexed below. A lesson " +
		"on leading goes to a task that edits `lead/notes/` in Djinn's repository, never to your agent's memory.\n\n" +
		leadCommandsBrief(true) + notesBrief()
}

// tellDriven queues what the developer writes to the Djinn as an event of the lead's queue: the next batch takes it.
func (w *Wishes) tellDriven(ctx context.Context, wishID, text string) error {
	e := &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_MESSAGE, WishId: wishID, Text: text}
	return Status(w.Notify(ctx, e))
}

// errDriven is why a call to the lead's terminal or watch is refused while Djinn drives the lead.
var errDriven = errors.New("the lead is driven by Djinn (decision Q31): each batch of its events goes to a fresh " +
	"session of its own, with no terminal and no watch; djinn lead journal shows what each batch did")

// resumeDriven is LeadService.Resume while Djinn drives the lead: no terminal opens; after a failure, the events
// that wait go to a new session at once.
func (s *LeadService) resumeDriven(
	ctx context.Context, req *connect.Request[planv1.LeadServiceResumeRequest], d *LeadDrive,
) (*connect.Response[planv1.LeadServiceResumeResponse], error) {
	lead, err := store.Get[*planv1.Lead](ctx, s.Wishes.Store, "lead")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, Status(err)
	}
	note := "Djinn drives the lead: each batch of its events, and what you write to the Djinn, goes to a fresh " +
		"session that ends with its turn. djinn lead journal shows what each batch did."
	if d.Failure() != "" {
		d.askRetry()
		note = "Djinn drives the lead: the events that wait go to a new session now. djinn lead journal shows what " +
			"each batch did."
	}
	if s.Wishes.Leads != nil {
		s.Wishes.Leads.Show(req.Msg.GetWishId(), "")
	}
	return connect.NewResponse(&planv1.LeadServiceResumeResponse{Lead: lead, Note: note}), nil
}

// Journal prints the lead's journal: the latest batches, of a wish or of all.
func (s *LeadService) Journal(
	ctx context.Context, req *connect.Request[planv1.LeadServiceJournalRequest],
) (*connect.Response[planv1.LeadServiceJournalResponse], error) {
	var wishes []string
	if id := req.Msg.GetWishId(); id != "" {
		wishes = []string{id}
	}
	n := int(req.Msg.GetLimit())
	if n == 0 {
		n = 10
	}
	batches, err := LeadJournal(ctx, s.Wishes.Store, wishes, n)
	if err != nil {
		return nil, Status(err)
	}
	var b strings.Builder
	if len(batches) == 0 {
		b.WriteString("The lead's journal is empty: Djinn writes a batch there each time it drives the lead.\n")
	}
	for _, batch := range batches {
		b.WriteString(JournalEntry(batch))
	}
	return connect.NewResponse(&planv1.LeadServiceJournalResponse{Text: b.String()}), nil
}
