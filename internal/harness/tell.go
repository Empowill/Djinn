package harness

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// TellFunc types a line in the terminal of the lead of a wish: plan.Wishes.Tell.
type TellFunc func(ctx context.Context, wishID, line string) error

// NotifyFunc queues an event for the lead, which its watch hands out: plan.Wishes.Notify.
type NotifyFunc func(ctx context.Context, e *planv1.LeadEvent) error

// NotifyLead lets Djinn queue the lead's events (decision Q191) instead of typing lines in its terminal: djinn up
// gives it once its terminals run. Without it, the lines go to TellLeads' function, as they did.
func (h *Harness) NotifyLead(notify NotifyFunc) {
	h.mu.Lock()
	h.notifyLead = notify
	h.mu.Unlock()
	if notify != nil && h.batcher != nil {
		h.batcher.Flush(context.Background())
	}
}

// toLead hands an event of kind about code in wishID to the lead: queued with NotifyLead's function, or else typed
// with TellLeads'. ErrNoLead-like errors are the caller's to ignore; with neither function, nothing happens.
func (h *Harness) toLead(ctx context.Context, kind planv1.LeadEventKind, wishID, code, line string) error {
	return h.toLeadNote(ctx, kind, wishID, code, line, "")
}

// toLeadNote is toLead with the note the event carries whole (LeadEvent.note): a worker's final note. A line typed in
// the terminal goes without it.
func (h *Harness) toLeadNote(ctx context.Context, kind planv1.LeadEventKind, wishID, code, line, note string) error {
	return h.toLeadEvent(ctx, &planv1.LeadEvent{Kind: kind, WishId: wishID, Code: code, Text: line, Note: note})
}

// toLeadEvent queues e for its wish's lead. A line typed in the terminal goes with its detail after it, on the same
// line, without its note.
func (h *Harness) toLeadEvent(ctx context.Context, e *planv1.LeadEvent) error {
	h.mu.Lock()
	tell, notify := h.tell, h.notifyLead
	h.mu.Unlock()
	switch {
	case notify != nil:
		return notify(ctx, e)
	case tell != nil:
		line := e.GetText()
		if d := e.GetDetail(); d != "" {
			line += " " + strings.Join(strings.Fields(d), " ")
		}
		return tell(ctx, e.GetWishId(), line)
	}
	return nil
}

// WithTellDelay sets the delay before batched lines to a wish's lead are flushed: tests set a shorter one.
func WithTellDelay(d time.Duration) Option { return func(h *Harness) { h.tellDelay = d } }

// TellLeads lets Djinn type lines into the terminals of the leads of wishes. djinn up gives it once its terminals run.
// Buffered lines waiting to be told are flushed once it is set.
func (h *Harness) TellLeads(tell TellFunc) {
	h.mu.Lock()
	h.tell = tell
	h.mu.Unlock()
	if tell != nil && h.batcher != nil {
		h.batcher.Flush(context.Background())
	}
}

// defaultTellDelay is how long Djinn waits before sending a batch of worker lines to a lead as one paragraph.
const defaultTellDelay = 2 * time.Second

// tellBatcher groups lines that arrive within a few seconds for each wish into a single paragraph.
type tellBatcher struct {
	mu      sync.Mutex
	h       *Harness
	pending map[string][]string // wishID -> lines
	timers  map[string]*time.Timer
	closed  bool
}

func newTellBatcher(h *Harness) *tellBatcher {
	return &tellBatcher{
		h:       h,
		pending: make(map[string][]string),
		timers:  make(map[string]*time.Timer),
	}
}

func (b *tellBatcher) queue(wishID, line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.pending[wishID] = append(b.pending[wishID], line)
	delay := cmp.Or(b.h.tellDelay, defaultTellDelay)
	if _, ok := b.timers[wishID]; ok {
		return
	}
	b.timers[wishID] = time.AfterFunc(delay, func() {
		defer logx.Crash("tell", "wish", wishID)
		b.flushWish(wishID)
	})
}

func (b *tellBatcher) flushWish(wishID string) {
	b.mu.Lock()
	b.h.mu.Lock()
	tell := b.h.tell
	b.h.mu.Unlock()
	if tell == nil {
		if t, ok := b.timers[wishID]; ok {
			t.Stop()
			delete(b.timers, wishID)
		}
		b.mu.Unlock()
		return
	}
	if t, ok := b.timers[wishID]; ok {
		t.Stop()
		delete(b.timers, wishID)
	}
	lines := b.pending[wishID]
	delete(b.pending, wishID)
	b.mu.Unlock()

	if len(lines) == 0 {
		return
	}
	paragraph := strings.Join(lines, "\n")
	b.h.tellWish(wishID, paragraph)
}

func (b *tellBatcher) Flush(ctx context.Context) {
	b.mu.Lock()
	type item struct {
		wishID    string
		paragraph string
	}
	var items []item
	for wishID, lines := range b.pending {
		if t, ok := b.timers[wishID]; ok {
			t.Stop()
		}
		if len(lines) > 0 {
			items = append(items, item{wishID: wishID, paragraph: strings.Join(lines, "\n")})
		}
	}
	b.pending = make(map[string][]string)
	b.timers = make(map[string]*time.Timer)
	b.mu.Unlock()

	for _, it := range items {
		b.h.tellWish(it.wishID, it.paragraph)
	}
}

func (b *tellBatcher) Close() {
	b.mu.Lock()
	b.closed = true
	for _, t := range b.timers {
		t.Stop()
	}
	b.timers = make(map[string]*time.Timer)
	b.pending = make(map[string][]string)
	b.mu.Unlock()
}

// FlushTell sends any pending batched messages to the leads immediately.
func (h *Harness) FlushTell(ctx context.Context) {
	if h.batcher != nil {
		h.batcher.Flush(ctx)
	}
}

// tellWish types paragraph in the terminal of the lead of wishID.
func (h *Harness) tellWish(wishID, paragraph string) {
	h.mu.Lock()
	tell := h.tell
	h.mu.Unlock()
	if tell == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := tell(ctx, wishID, paragraph); err != nil && !errors.Is(err, plan.ErrNoLead) {
		logx.L().Warn("tell the lead", "wish", wishID, "err", err)
	}
}

// tellWorkerFailed queues a failure notification to the lead of t's wish.
func (h *Harness) tellWorkerFailed(t *planv1.Task) {
	if t.GetWishId() == "" || watching(t) || light(t) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	line := h.workerFailedLine(ctx, t)
	h.mu.Lock()
	queued := h.notifyLead != nil
	h.mu.Unlock()
	if queued {
		// The lead's queue batches by itself, in its watch.
		if err := h.toLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_FAILED, t.GetWishId(), t.GetCode(), line); err != nil {
			logx.L().Warn("tell the lead", "wish", t.GetWishId(), "task", t.GetCode(), "err", err)
		}
		return
	}
	h.batcher.queue(t.GetWishId(), line)
}

// workerFailedLine formats the line that tells a wish's lead what went wrong with the work worker t.
func (h *Harness) workerFailedLine(ctx context.Context, t *planv1.Task) string {
	code, title := t.GetCode(), t.GetTitle()
	var role string
	if c := t.GetCorrection(); c != nil {
		if served := taskCodes(ctx, h.store, c.GetFailure().GetTaskIds()); served != "" {
			role = fmt.Sprintf(", its correction worker (served %s),", served)
		} else {
			role = ", its correction worker,"
		}
	} else if r := t.GetReview(); r != nil {
		if served := taskCodes(ctx, h.store, r.GetTaskIds()); served != "" {
			role = fmt.Sprintf(", its review worker (served %s),", served)
		} else {
			role = ", its review worker,"
		}
	}

	agent := short(t.GetProvider())
	if t.GetProvider() == planv1.Provider_PROVIDER_UNSPECIFIED {
		agent = "claude"
	}
	if m := t.GetModel(); m != "" {
		agent += ", " + m
	}

	project, _ := store.Get[*planv1.Project](ctx, h.store, t.GetProjectId())
	var errLine string
	if project != nil && project.GetGit() && !light(t) && t.GetWorktree() != "" && isGone(t.GetWorktree()) {
		errLine = "its worktree is gone: it cannot be resumed"
	} else {
		errLine = firstLine(t.GetError())
	}
	if errLine == "" {
		if t.GetWaitReason() != "" {
			errLine = firstLine(t.GetWaitReason())
		} else {
			errLine = "unknown error"
		}
	}
	errLine = strings.TrimRight(errLine, ". ")

	action := "The move is yours."
	switch {
	case t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && t.GetResumeAfter() != nil:
		action = "A retry scheduled after a limit."
	case t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && t.GetContinuing():
		action = "A continuation scheduled."
	case t.GetCorrection() != nil:
		c := t.GetCorrection()
		attempts := c.GetAttempt()
		maxAttempts := plan.DefaultCorrectionAttempts
		if project != nil {
			if settings, err := plan.LoadSettings(h.home, project); err == nil {
				maxAttempts = settings.CorrectionAttempts
			}
		}
		if int(attempts) < maxAttempts {
			action = "A correction worker started."
		}
	case t.GetReview() != nil:
		r := t.GetReview()
		attempts := r.GetAttempt()
		maxAttempts := plan.DefaultCorrectionAttempts
		if project != nil {
			if settings, err := plan.LoadSettings(h.home, project); err == nil {
				maxAttempts = settings.CorrectionAttempts
			}
		}
		if int(attempts) < maxAttempts {
			action = "A review worker started."
		}
	}

	if wish, _ := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId()); t.GetRefused().GetCount() > 0 {
		action += " " + refusedClause(t, wish, refusedShown) + refusedEnd(t)
	}
	if role != "" {
		return fmt.Sprintf("Djinn: %s (%s)%s failed (%s): %s. %s", code, title, role, agent, errLine, action)
	}
	return fmt.Sprintf("Djinn: %s (%s) failed (%s): %s. %s", code, title, agent, errLine, action)
}

// taskCodes looks up the codes of the tasks given by IDs, joined by commas.
func taskCodes(ctx context.Context, r store.Reader, ids []string) string {
	tasks, err := tasksByID(ctx, r, ids)
	if err != nil || len(tasks) == 0 {
		return ""
	}
	codes := make([]string, len(tasks))
	for i, t := range tasks {
		codes[i] = t.GetCode()
	}
	return strings.Join(codes, ", ")
}
