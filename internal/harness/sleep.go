package harness

import (
	"context"
	"errors"
	"time"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/render"
	"github.com/empowill/djinn/internal/store"
)

// methodWake journals a question the scheduler woke, its conditions holding; the request is the question.
const methodWake = "harness/wake"

// methodNever journals a question the scheduler found waiting for what can never come (QuestionSleep.never), or
// waiting again for what may; the request is the question.
const methodNever = "harness/never"

// watchSleepers has the scheduler read the questions again at its next pass whenever one is put to sleep, and asks
// for that pass: a condition may hold already, or soon. Schedule calls it once.
func (h *Harness) watchSleepers() {
	h.sleepers.Store(true) // Questions put to sleep before this start.
	h.store.OnCommit(func(ms []proto.Message) {
		for _, m := range ms {
			if q, ok := m.(*planv1.Question); ok && render.Asleep(q) {
				h.sleepers.Store(true)
				h.wake()
				return
			}
		}
	})
}

// wakeQuestions wakes each asleep question whose conditions all hold now (plan.SleepAllHold), by tasks, every task
// stored: it shows at the top of its wish's open questions, and its lead is told to check its facts. One of whose
// conditions can never hold (plan.SleepNever) sleeps on, out of the developer's questions, and its lead is told why,
// once, to withdraw it or wake it (neverQuestion). It reads the questions only while one may sleep. The caller holds
// h.sched.
func (h *Harness) wakeQuestions(ctx context.Context, tasks []*planv1.Task) {
	if !h.sleepers.Swap(false) {
		return
	}
	questions, err := store.List[*planv1.Question](ctx, h.store, nil)
	if err != nil {
		h.sleepers.Store(true)
		if ctx.Err() == nil {
			logx.L().Error("list the questions asleep", "loop", "schedule", "err", err)
		}
		return
	}
	var byWish map[string][]*planv1.Task
	now := h.now()
	asleep := false
	for _, q := range questions {
		if !render.Asleep(q) {
			continue
		}
		if byWish == nil {
			byWish = map[string][]*planv1.Task{}
			for _, t := range plan.WithAzimas(tasks) {
				byWish[t.GetWishId()] = append(byWish[t.GetWishId()], t)
			}
		}
		never := plan.SleepNeverText(q, byWish[q.GetWishId()], now)
		if never != "" || q.GetSleep().GetNever() != "" {
			if never != q.GetSleep().GetNever() {
				if err := h.neverQuestion(ctx, q.GetId(), never); err != nil {
					logx.L().Error("tell the lead a question waits for what can never come", "loop", "schedule",
						"wish", q.GetWishId(), "question", q.GetCode(), "err", err)
				}
			}
			if never != "" {
				asleep = true // Until the lead withdraws it or wakes it.
				continue
			}
		}
		if !plan.SleepAllHold(q, byWish[q.GetWishId()], now) {
			asleep = true
			continue
		}
		if err := h.wakeQuestion(ctx, q.GetId(), now); err != nil {
			asleep = true // The next pass tries again.
			logx.L().Error("wake a question", "loop", "schedule", "wish", q.GetWishId(), "question", q.GetCode(), "err", err)
		}
	}
	if asleep {
		h.sleepers.Store(true)
	}
}

// neverQuestion records never on the question id, asleep: which of its conditions can never hold and why
// (plan.SleepNeverText), or "" once each may hold again (its task continued). A never set tells its lead
// (plan.NeverLine), who decides: withdraw it, or put it to the developer; the question sleeps on meanwhile. One that
// woke or closed meanwhile is left as it is.
func (h *Harness) neverQuestion(ctx context.Context, id, never string) error {
	var told *planv1.Question
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		q, err := store.Get[*planv1.Question](ctx, tx, id)
		if err != nil || !render.Asleep(q) || q.GetSleep().GetNever() == never {
			return err
		}
		q.Sleep = proto.CloneOf(q.GetSleep())
		q.Sleep.Never = never
		if err := tx.Journal(actorHarness, methodNever, q); err != nil {
			return err
		}
		told = q
		return tx.Put(q)
	})
	if err != nil || told == nil || never == "" {
		return err
	}
	logx.L().Info("question waits for what can never come", "wish", told.GetWishId(), "question", told.GetCode(),
		"until", plan.SleepTexts(told), "never", never)
	err = h.toLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_NEVER, told.GetWishId(), told.GetCode(),
		plan.NeverLine(told, never))
	if err != nil && !errors.Is(err, plan.ErrNoLead) {
		logx.L().Warn("tell the lead a question waits for what can never come", "wish", told.GetWishId(),
			"question", told.GetCode(), "err", err)
	}
	return nil
}

// wakeQuestion wakes the question id, asleep, at now, then tells its lead. One that woke or closed meanwhile is left
// as it is.
func (h *Harness) wakeQuestion(ctx context.Context, id string, now time.Time) error {
	var woken *planv1.Question
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		q, err := store.Get[*planv1.Question](ctx, tx, id)
		if err != nil || !render.Asleep(q) {
			return err
		}
		plan.Wake(q, now, false)
		if err := tx.Journal(actorHarness, methodWake, q); err != nil {
			return err
		}
		woken = q
		return tx.Put(q)
	})
	if err != nil || woken == nil {
		return err
	}
	logx.L().Info("question woke", "wish", woken.GetWishId(), "question", woken.GetCode(), "until", plan.SleepTexts(woken))
	err = h.toLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_WOKE, woken.GetWishId(), woken.GetCode(), plan.WokeLine(woken))
	if err != nil && !errors.Is(err, plan.ErrNoLead) {
		logx.L().Warn("tell the lead a question woke", "wish", woken.GetWishId(), "question", woken.GetCode(), "err", err)
	}
	return nil
}
