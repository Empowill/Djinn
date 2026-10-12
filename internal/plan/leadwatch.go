package plan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/store"
)

// The lead's events (decision Q191): what the lead must act on waits in the store, in one queue for the one lead
// whatever wish it is about, until a watch of the lead's session acknowledges it. djinn lead watch blocks on the
// queue and returns one digest; while a watch waits, Djinn types nothing in the lead's terminal.

// methodLeadEvent is the journal's method for an event queued for the lead; the request is the event.
const methodLeadEvent = "lead/event"

// watchTiming is how long a watch and the reminder wait; tests shorten them.
type watchTiming struct {
	// quiet is how long the events must stop coming before a watch returns them: the lines of one integration
	// pass, or a failure and the correction Djinn starts for it, come within a second or two, and one digest is one
	// turn of the lead instead of several. A lead turn takes far longer than this wait.
	quiet time.Duration
	// settleMax caps the settling: a stream of events that never pauses is returned after this long anyway.
	settleMax time.Duration
	// grace is how long Djinn waits, after a watch returned, for the lead's next one before it types the line that
	// says events wait: the lead runs it again first thing, within seconds, when it read its digest.
	grace time.Duration
	// retype is how long Djinn waits before typing the line again when the lead ran no watch since the last one.
	retype time.Duration
	// remind is how long Djinn waits after an event before typing the line, so that a burst gets one line: quiet.
	remind time.Duration
}

// defaultWatchTiming is the timing of djinn up.
var defaultWatchTiming = watchTiming{
	quiet: 2 * time.Second, settleMax: 10 * time.Second, grace: time.Minute, retype: 10 * time.Minute, remind: 2 * time.Second,
}

const (
	// watchDefault is how long a watch waits for an event when the request names no timeout.
	watchDefault = time.Hour
	// watchMax is the longest a watch waits.
	watchMax = 6 * time.Hour
	// digestMax is the most events a digest prints; the others come with the next watch, at once.
	digestMax = 30
	// digestLineMax is how many characters of an event's text a digest line keeps.
	digestLineMax = 160
	// leadEventsKept is how long an acknowledged event stays in the store.
	leadEventsKept = 7 * 24 * time.Hour
)

// leadWatch is what Djinn knows of the watches of the lead, in memory: a restart forgets it, and the next watch
// takes it back.
type leadWatch struct {
	timing   watchTiming
	mu       sync.Mutex
	watching int           // watches waiting now
	returned time.Time     // when the last watch returned
	typed    time.Time     // when Djinn last typed the line that says events wait
	queued   chan struct{} // closed when an event is queued, then replaced
	timer    *time.Timer   // the check that types the line, when one is due
	due      time.Time     // when timer fires
}

// leadWatches holds the leadWatch of each store: djinn up makes more than one Wishes on its store, and the one that
// queues an event must wake the watch another one serves.
var leadWatches sync.Map // *store.Store -> *leadWatch

// leadWatch is the leadWatch of w's store.
func (w *Wishes) leadWatch() *leadWatch {
	l, _ := leadWatches.LoadOrStore(w.Store, &leadWatch{timing: defaultWatchTiming})
	return l.(*leadWatch)
}

// stop cancels the planned reminder.
func (l *leadWatch) stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
}

// changed is the channel closed at the next event queued.
func (l *leadWatch) changed() <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.queued == nil {
		l.queued = make(chan struct{})
	}
	return l.queued
}

// signal wakes the watches: an event was queued.
func (l *leadWatch) signal() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.queued != nil {
		close(l.queued)
		l.queued = nil
	}
}

// QueueLeadEvent stores e for the lead, with its id and its time: it waits there until a watch acknowledges it.
func QueueLeadEvent(ctx context.Context, s *store.Store, e *planv1.LeadEvent, now time.Time) error {
	e.Id, e.CreateTime = store.NewID(), timestamppb.New(now)
	e.AckSessionId, e.AckTime = "", nil
	return s.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, methodLeadEvent, e); err != nil {
			return err
		}
		return tx.Put(e)
	})
}

// waitingEvents are the events no session acknowledged, oldest first.
func waitingEvents(ctx context.Context, r store.Reader) ([]*planv1.LeadEvent, error) {
	return store.List[*planv1.LeadEvent](ctx, r, store.Where{"ack_session_id": ""})
}

// Notify queues e for the lead, and wakes its watch. With none waiting, Djinn types one short line in the lead's
// terminal once the events settle: "Djinn: N events waiting, run djinn lead watch".
func (w *Wishes) Notify(ctx context.Context, e *planv1.LeadEvent) error {
	if err := QueueLeadEvent(ctx, w.Store, e, w.now()); err != nil {
		return err
	}
	l := w.leadWatch()
	l.signal()
	w.remindLead(l.timing.remind)
	return nil
}

// RemindLead types the line that says events wait, after a grace for the lead's watch to come back, when events
// wait and no watch does: djinn up calls it once it runs, so that a lead whose watch died with the last djinn up
// hears of what happened meanwhile, the restart first.
func (w *Wishes) RemindLead() {
	if w.Store != nil {
		w.queueWaitingAnswers(context.Background())
	}
	w.remindLead(w.leadWatch().timing.grace)
}

// remindLead plans the check that types the line, in after at the soonest, or later when a watch returned within
// the grace.
func (w *Wishes) remindLead(after time.Duration) {
	if w.Leads == nil {
		return
	}
	l := w.leadWatch()
	l.mu.Lock()
	defer l.mu.Unlock()
	at := time.Now().Add(after)
	if grace := l.returned.Add(l.timing.grace); grace.After(at) {
		at = grace
	}
	if l.timer != nil && !l.due.After(at) {
		return // An earlier check is planned: it plans the next one if needed.
	}
	if l.timer != nil {
		l.timer.Stop()
	}
	l.due = at
	l.timer = time.AfterFunc(time.Until(at), func() {
		defer logx.Crash("leadwatch")
		w.checkLead()
	})
}

// checkLead types the line that says events wait, unless a watch waits for them, one returned within the grace,
// or the line was typed already since the lead last watched, less than timing.retype ago.
func (w *Wishes) checkLead() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	lead, err := store.Get[*planv1.Lead](ctx, w.Store, "lead")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		logx.L().Error("remind the lead", "loop", "leadwatch", "err", err)
		return
	}
	l := w.leadWatch()
	l.mu.Lock()
	l.timer = nil
	now := time.Now()
	// No finished command wakes a codex lead up: its watch, if one waits, tells it nothing.
	watched := l.watching > 0 && leadProvider(lead) != planv1.Provider_PROVIDER_CODEX
	recent := now.Before(l.returned.Add(l.timing.grace))
	retyped := !l.typed.IsZero() && now.Before(l.typed.Add(l.timing.retype))
	l.mu.Unlock()
	if watched || retyped {
		return
	}
	if recent {
		w.remindLead(0)
		return
	}
	events, err := waitingEvents(ctx, w.Store)
	if err != nil {
		logx.L().Error("remind the lead", "loop", "leadwatch", "err", err)
		return
	}
	if len(events) == 0 {
		return
	}
	if err := w.Tell(ctx, "", WaitingLine(len(events))); err != nil {
		if !errors.Is(err, ErrNoLead) {
			logx.L().Warn("remind the lead", "loop", "leadwatch", "err", err)
		}
		return
	}
	l.mu.Lock()
	l.typed = time.Now()
	l.mu.Unlock()
}

// WaitingLine is the one line Djinn types in the lead's terminal when events wait and no watch of the lead does.
func WaitingLine(n int) string {
	if n == 1 {
		return "Djinn: 1 event waiting, run djinn lead watch"
	}
	return fmt.Sprintf("Djinn: %d events waiting, run djinn lead watch", n)
}

func (s *LeadService) Watch(
	ctx context.Context, req *connect.Request[planv1.LeadServiceWatchRequest],
) (*connect.Response[planv1.LeadServiceWatchResponse], error) {
	w := s.Wishes
	if CallerFromContext(ctx) != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New(
			"a worker cannot watch the lead's events: they are the lead's"))
	}
	lead, err := store.Get[*planv1.Lead](ctx, w.Store, "lead")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, Status(err)
	}
	if !hasLead(lead) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(
			"no lead is recorded: djinn lead set records the session that watches"))
	}
	session := req.Msg.GetSession()
	switch {
	case session == "":
		session = lead.GetSessionId()
	case lead.GetSessionId() != "" && session != lead.GetSessionId():
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"session %s is not Djinn's lead (session %s): its events are the lead's; if this session leads now, "+
				"djinn lead set %s records it", session, lead.GetSessionId(), session))
	}
	timeout := watchDefault
	if t := req.Msg.GetTimeout(); t != nil {
		timeout = min(max(t.AsDuration(), 0), watchMax)
	}
	if after := req.Msg.GetAfter(); after != "" {
		if err := w.acknowledge(ctx, req, session, after); err != nil {
			return nil, err
		}
	}

	l := w.leadWatch()
	l.mu.Lock()
	l.watching++
	l.typed = time.Time{} // The lead watches: the next events that wait without a watch get their line.
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.watching--
		if timeout > 0 {
			// The lead runs its next watch within the grace. One that returns at once (a codex lead, a look) does
			// not promise one.
			l.returned = time.Now()
		}
		l.mu.Unlock()
		// Events that come before the next watch wait for it, or for the line once the grace ends.
		w.remindLead(l.timing.grace)
	}()

	events, err := w.waitEvents(ctx, timeout)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.LeadServiceWatchResponse{
		Text: w.digest(ctx, events, req.Msg.GetAfter(), timeout),
	}), nil
}

// acknowledge marks every waiting event up to the cursor after as acknowledged by session, and drops the events
// acknowledged more than leadEventsKept ago.
func (w *Wishes) acknowledge(
	ctx context.Context, req *connect.Request[planv1.LeadServiceWatchRequest], session, after string,
) error {
	events, err := waitingEvents(ctx, w.Store)
	if err != nil {
		return Status(err)
	}
	var acked []*planv1.LeadEvent
	for _, e := range events {
		if e.GetId() <= after {
			acked = append(acked, e)
		}
	}
	if len(acked) == 0 {
		return nil
	}
	now := w.now()
	return write(ctx, w.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		for _, e := range acked {
			e.AckSessionId, e.AckTime = session, timestamppb.New(now)
			if err := tx.Put(e); err != nil {
				return err
			}
			if e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER {
				if err := answerReached(ctx, tx, e.GetCode(), e.GetWishId()); err != nil {
					return err
				}
			}
		}
		old, err := store.List[*planv1.LeadEvent](ctx, tx, nil)
		if err != nil {
			return err
		}
		for _, e := range old {
			if e.GetAckTime() != nil && now.Sub(e.GetAckTime().AsTime()) > leadEventsKept {
				if err := tx.Delete(e); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// answerReached marks the answered question code of wishID no longer waiting for a lead: the lead acknowledged it.
func answerReached(ctx context.Context, tx *store.Tx, code, wishID string) error {
	if code == "" {
		return nil
	}
	questions, err := store.List[*planv1.Question](ctx, tx, store.Where{"wish_id": wishID})
	if err != nil {
		return err
	}
	for _, q := range questions {
		if q.GetCode() == code && q.GetWaitingLead() {
			q.WaitingLead = false
			return tx.Put(q)
		}
	}
	return nil
}

// waitEvents waits up to timeout for an event to wait, then for the events to settle, and returns those waiting;
// none when timeout passed first. A timeout of 0 returns at once what waits.
func (w *Wishes) waitEvents(ctx context.Context, timeout time.Duration) ([]*planv1.LeadEvent, error) {
	timing := w.leadWatch().timing
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		changed := w.leadWatch().changed() // Taken before reading, so that an event queued meanwhile is not missed.
		events, err := waitingEvents(ctx, w.Store)
		if err != nil {
			return nil, Status(err)
		}
		if len(events) > 0 {
			if timeout == 0 {
				return events, nil
			}
			break
		}
		if timeout == 0 {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, nil
		case <-changed:
		}
	}
	settled := time.Now().Add(timing.settleMax)
	for {
		changed := w.leadWatch().changed()
		wait := min(timing.quiet, time.Until(settled))
		if wait <= 0 {
			break
		}
		quiet := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			quiet.Stop()
			return nil, ctx.Err()
		case <-changed:
			quiet.Stop()
			continue
		case <-quiet.C:
		}
		break
	}
	events, err := waitingEvents(ctx, w.Store)
	return events, Status(err)
}

// digest is what a watch prints: a line per event, at most digestMax, then the cursor and what to run next.
func (w *Wishes) digest(ctx context.Context, events []*planv1.LeadEvent, after string, timeout time.Duration) string {
	var b strings.Builder
	if len(events) == 0 {
		next := "djinn lead watch"
		if after != "" {
			next += " --after " + after
		}
		fmt.Fprintf(&b, "No event for the lead in %s. Run %s again.\n", timeout, next)
		return b.String()
	}
	more := 0
	if len(events) > digestMax {
		more, events = len(events)-digestMax, events[:digestMax]
	}
	// The wish of each line, when the events are about more than one.
	titles := map[string]string{}
	for _, e := range events {
		if _, ok := titles[e.GetWishId()]; ok || e.GetWishId() == "" {
			continue
		}
		titles[e.GetWishId()] = e.GetWishId()
		if wish, err := store.Get[*planv1.Wish](ctx, w.Store, e.GetWishId()); err == nil {
			titles[e.GetWishId()] = clipRunes(wish.GetTitle(), 40)
		}
	}
	if len(events) == 1 {
		b.WriteString("1 event for the lead:\n")
	} else {
		fmt.Fprintf(&b, "%d events for the lead:\n", len(events))
	}
	for _, e := range events {
		b.WriteString("- ")
		b.WriteString(DigestLine(e))
		if title, ok := titles[e.GetWishId()]; ok && len(titles) > 1 {
			fmt.Fprintf(&b, " [%s]", title)
		}
		b.WriteString("\n")
	}
	if more > 0 {
		fmt.Fprintf(&b, "%d more wait: the next watch returns them at once.\n", more)
	}
	cursor := events[len(events)-1].GetId()
	b.WriteString("Details: djinn task watch <task>, djinn wish brief <wish>.\n")
	fmt.Fprintf(&b, "Cursor %s. Next: djinn lead watch --after %s (acknowledges these, waits for the next).\n",
		cursor, cursor)
	return b.String()
}

// DigestKeeps tells whether a digest's line keeps the whole text of e, uncut.
func DigestKeeps(e *planv1.LeadEvent) bool {
	return !strings.HasSuffix(DigestLine(e), "…") || strings.HasSuffix(oneLine(e.GetText()), "…")
}

// DigestLine is an event in a digest's line: its code, its kind, and its text in a few words.
func DigestLine(e *planv1.LeadEvent) string {
	kind := strings.ToLower(strings.TrimPrefix(e.GetKind().String(), "LEAD_EVENT_KIND_"))
	text := strings.TrimPrefix(oneLine(e.GetText()), "Djinn: ")
	head := kind
	if code := e.GetCode(); code != "" {
		head = code + " " + kind
		text = strings.TrimPrefix(text, code+" ")
	}
	return head + ": " + clipRunes(text, digestLineMax)
}
