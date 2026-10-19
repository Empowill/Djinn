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
	DrivenRulesVersion = "5"
	// batchEventsMax is the most events one batch takes; the others go to the next batch, at once.
	batchEventsMax = 40
	// JournalMax is how many of the latest batches of its wishes a batch's prompt carries at most.
	JournalMax = 6
	// JournalMaxTokens caps the lead's journal section of a batch's prompt in tokens (at 4 runes per token).
	JournalMaxTokens = 1200
	// journalRunesPerToken is the conservative rune-per-token divisor for capping the journal section.
	journalRunesPerToken = 4
	// journalLineMax is how many characters of a command or of a batch's one-line hand-off note the journal keeps.
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

// JournalNote is the one line that ended a batch's turn, as the journal keeps it for the next leads.
func JournalNote(text string) string { return clipRunes(oneLine(text), journalLineMax) }

// JournalTokens estimates the tokens of a journal text at four runes per token, rounded up.
func JournalTokens(s string) int {
	return (len([]rune(s)) + journalRunesPerToken - 1) / journalRunesPerToken
}

// journalSection builds the lead's journal section of a batch's prompt, keeping the newest batches that fit under
// JournalMaxTokens (or the newest batch alone if even one exceeds the cap), in chronological order (oldest first).
func journalSection(journal []*planv1.LeadBatch) string {
	const head = "What the leads of the latest batches of these wishes did, the oldest first.\n\n"
	if len(journal) == 0 {
		return "No batch before this one for these wishes.\n\n"
	}
	entries := make([]string, len(journal))
	for i, j := range journal {
		entries[i] = JournalEntry(j)
	}
	used := JournalTokens(head)
	start := len(entries)
	for i := len(entries) - 1; i >= 0; i-- {
		t := JournalTokens(entries[i])
		if start < len(entries) && used+t > JournalMaxTokens {
			break
		}
		used += t
		start = i
	}
	var b strings.Builder
	b.WriteString(head)
	for _, e := range entries[start:] {
		b.WriteString(e)
	}
	return b.String()
}

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
	b.WriteString(journalSection(journal))
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

// DrivenRules are the rules of the lead Djinn drives (decision Q31): shared with briefRules through leadRules so both
// modes read from one source, made for a session that takes one batch then ends. Every batch's session reads them
// apart from its message, the same text each time, so that its cache serves the next session. A change here is a new
// DrivenRulesVersion.
func DrivenRules(projects []*planv1.Project, home string) string {
	return "# Leading in Djinn: the driven lead, rules " + DrivenRulesVersion + "\n\n" +
		"Djinn holds the active wishes and their plans: tasks, questions, decisions and blocks. You lead them: you " +
		"split the work into tasks for workers, talk with the developer through questions, and keep the plans true, " +
		"with the `djinn` command, never in its data folder.\n\n" +
		leadRules(projects, home, true) + "\n" +
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
	return connect.NewResponse(&planv1.LeadServiceJournalResponse{Text: b.String(), Batches: batches}), nil
}
