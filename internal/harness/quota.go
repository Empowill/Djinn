package harness

// The quota of each sign-in (T33, decision Q258 B). Djinn knows the usage limits of every sign-in of an agent that
// tells them: the 5-hour session and the week, how much of each is used and when it resets. Two sources say them:
// a probe that asks the agent without a model turn (Claude Code's get_usage control request, every quotaEvery, for
// an agent with accounts only), and the stream of each worker running on the sign-in (claude's rate_limit_event, at
// each turn). The latest reading wins.
//
// EarliestWeek, the policy by default, gives a new task the sign-in whose week resets first among those with quota
// left: what is left of a week is lost at its reset, so the week that ends first is spent first (L43, reading 1). A
// task that ran keeps its sign-in while that one is free and has quota left (its session and its prompt cache stay
// there). Without a fresh reading of any sign-in (the probe silent, no worker running), it is FirstFree, the simple
// overflow.

import (
	"cmp"
	"context"
	"math"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
)

// quotaEvery is how often the probe reads each sign-in's usage, unless a worker told it since.
const quotaEvery = 5 * time.Minute

// quotaLook is how often the probe looks for a sign-in to read: an account added is read within it.
const quotaLook = time.Minute

// quotaStale is how old a reading may be for the policy to trust it: three probes missed, and it is unknown.
const quotaStale = 3 * quotaEvery

// quotaFull is the share of a window past which a sign-in has no quota left for a new task: the tilasm's 95 %.
const quotaFull = 0.95

// quotaTie is how close two weeks' resets are for the policy to take them as one: the one with more left to lose
// goes first.
const quotaTie = time.Hour

// usageTimeout bounds one usage probe: a start of the agent and one request, measured at 6 to 36 seconds, the most
// under a heavy load (W380).
const usageTimeout = time.Minute

// UsageWindow is one window of a sign-in's usage limit.
type UsageWindow struct {
	// Used is the share used, from 0 to 1.
	Used float64
	// ResetsAt is when the window starts again; zero when not told (a window not started).
	ResetsAt time.Time
}

// used is the share of w used at now: none once it reset.
func (w *UsageWindow) used(now time.Time) float64 {
	if w == nil || !w.ResetsAt.IsZero() && !w.ResetsAt.After(now) {
		return 0
	}
	return w.Used
}

// Quota is what an agent tells of a sign-in's usage limits.
type Quota struct {
	// FiveHour is the session's window, SevenDay the week's; nil when not told.
	FiveHour, SevenDay *UsageWindow
	// Read is when Djinn read it; the harness stamps a worker's.
	Read time.Time
	// Source is where it was read.
	Source planv1.UsageSource
}

// UsageProber is an agent that tells a sign-in's usage limits without a model turn. env is what the sign-in adds to
// the agent's environment (plan.AccountEnv): nil for its own folder.
type UsageProber interface {
	Usage(ctx context.Context, env []string) (Quota, error)
}

// quotas are the sign-ins' quotas, guarded by Harness.accountMu, and the probe's goroutine.
type quotas struct {
	known  map[accountKey]Quota
	failed map[accountKey]probeFailure // the last probe of a sign-in, when it failed; gone once one succeeds
	wg     sync.WaitGroup              // the probe's loop; Close waits for it
}

// probeFailure is why a probe of a sign-in failed, and when.
type probeFailure struct {
	why string
	at  time.Time
}

// noteQuota keeps q as the quota of provider's sign-in name, unless a later reading is known.
func (h *Harness) noteQuota(provider planv1.Provider, name string, q Quota) {
	if q.Read.IsZero() {
		q.Read = h.now()
	}
	k := accountKey{cmp.Or(provider, planv1.Provider_PROVIDER_CLAUDE), name}
	h.accountMu.Lock()
	defer h.accountMu.Unlock()
	if h.quota.known == nil {
		h.quota.known = map[accountKey]Quota{}
	}
	if old, ok := h.quota.known[k]; !ok || !q.Read.Before(old.Read) {
		h.quota.known[k] = q
	}
	if q.Source == planv1.UsageSource_USAGE_SOURCE_PROBE {
		delete(h.quota.failed, k)
	}
}

// quotaOf is the quota known of k, and whether one is; the caller holds accountMu.
func (h *Harness) quotaOf(k accountKey) (Quota, bool) {
	q, ok := h.quota.known[k]
	return q, ok
}

// room tells whether Djinn knows o's quota at now (a reading fresher than quotaStale that tells the week), and
// whether it has some left: neither window past quotaFull.
func (o AccountOption) room(now time.Time) (known, left bool) {
	q := o.Quota
	if q == nil || q.SevenDay == nil || now.Sub(q.Read) > quotaStale {
		return false, false
	}
	return true, q.FiveHour.used(now) < quotaFull && q.SevenDay.used(now) < quotaFull
}

// weekEnd is when o's week resets, for the policy: a week not started, or past, ends last.
func (o AccountOption) weekEnd(now time.Time) time.Time {
	if end := o.Quota.SevenDay.ResetsAt; end.After(now) {
		return end
	}
	return time.Unix(1<<62, 0)
}

// EarliestWeek is the policy by default (Q258 B). A task that ran stays on its sign-in while it is free and not known
// to be out of quota. Otherwise, among the free sign-ins with quota left, the one whose week resets first; two weeks
// that reset within quotaTie of each other go to the one with more left to lose, then the developer's order. With
// none known, the first free sign-in whose quota is unknown, in the developer's order. With each free one out of
// quota, or none free, FirstFree: a quota spent does not hold a task back alone, as a limit does not (W409).
func EarliestWeek(c AccountChoice) (string, bool) {
	now := c.Now
	if c.Ran {
		for _, o := range c.Options {
			if o.Name != c.Current || !o.Free(now) {
				continue
			}
			if known, left := o.room(now); !known || left {
				return o.Name, true
			}
		}
	}
	var withRoom, unknown []AccountOption
	for _, o := range c.Options {
		if !o.Free(now) {
			continue
		}
		switch known, left := o.room(now); {
		case !known:
			unknown = append(unknown, o)
		case left:
			withRoom = append(withRoom, o)
		}
	}
	if len(withRoom) > 0 {
		best := withRoom[0]
		for _, o := range withRoom[1:] {
			a, b := o.weekEnd(now), best.weekEnd(now)
			d := a.Sub(b)
			if d < -quotaTie || d.Abs() <= quotaTie && o.Quota.SevenDay.used(now) < best.Quota.SevenDay.used(now) {
				best = o
			}
		}
		return best.Name, true
	}
	if len(unknown) > 0 {
		return unknown[0].Name, true
	}
	return FirstFree(c)
}

// watchQuotas starts the probe: each sign-in of an agent that tells its usage limits, for an agent with accounts,
// read at once and every quotaEvery after, unless a worker told it since; looked for every quotaLook, so that an
// account added is read within a minute. Close stops it.
func (h *Harness) watchQuotas() {
	h.quota.wg.Add(1)
	go func() {
		defer h.quota.wg.Done()
		defer logx.Crash("quota")
		tick := time.NewTicker(quotaLook)
		defer tick.Stop()
		for {
			h.probeQuotas(h.ctx)
			select {
			case <-h.ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}

// probeQuotas reads, one after the other, the usage of each sign-in of an agent with accounts whose agent tells it,
// when it was never read, or last read or tried more than quotaEvery ago, less half a quotaLook (a look late by a few
// seconds still reads it).
func (h *Harness) probeQuotas(ctx context.Context) {
	if h.home == "" {
		return
	}
	for _, provider := range []planv1.Provider{planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_CODEX,
		planv1.Provider_PROVIDER_ANTIGRAVITY} {
		prober, ok := h.providers[provider].(UsageProber)
		if !ok {
			continue
		}
		signIns := plan.SignIns(h.home, provider)
		if len(signIns) < 2 {
			continue // One sign-in: nothing to choose, and its workers tell its quota.
		}
		for _, s := range signIns {
			if ctx.Err() != nil {
				return
			}
			k := accountKey{provider, s.Name}
			h.accountMu.Lock()
			q, known := h.quotaOf(k)
			failed, tried := h.quota.failed[k]
			h.accountMu.Unlock()
			last := q.Read
			if tried && failed.at.After(last) {
				last = failed.at
			}
			if (known || tried) && h.now().Sub(last) < quotaEvery-quotaLook/2 {
				continue
			}
			h.probe(ctx, prober, k, plan.AccountEnv(s.Account))
		}
	}
}

// probe reads the usage of sign-in k by prober, and keeps it; a failure is kept to be shown, and logged once.
func (h *Harness) probe(ctx context.Context, prober UsageProber, k accountKey, env []string) {
	ctx, cancel := context.WithTimeout(ctx, usageTimeout)
	defer cancel()
	started := time.Now()
	q, err := prober.Usage(ctx, env)
	took := time.Since(started).Round(time.Millisecond)
	if err != nil {
		if ctx.Err() != nil && h.ctx.Err() != nil {
			return // Djinn stops.
		}
		h.accountMu.Lock()
		if h.quota.failed == nil {
			h.quota.failed = map[accountKey]probeFailure{}
		}
		said := h.quota.failed[k].why == err.Error()
		h.quota.failed[k] = probeFailure{why: err.Error(), at: h.now()}
		h.accountMu.Unlock()
		if !said {
			logx.L().Info("usage probe failed", "agent", short(k.provider), "account", k.name, "took", took, "err", err)
		}
		return
	}
	q.Read, q.Source = h.now(), planv1.UsageSource_USAGE_SOURCE_PROBE
	h.noteQuota(k.provider, k.name, q)
	logx.L().Debug("usage probed", "agent", short(k.provider), "account", k.name, "took", took)
}

// Usage is what Djinn knows of each sign-in's usage limits, for LeadService.Accounts: every sign-in of an agent whose
// quota Djinn read or tried to, in the developer's order, the one a new task would start on now marked next.
func (h *Harness) Usage() []*planv1.AccountUsage {
	if h.home == "" {
		return nil
	}
	now := h.now()
	var out []*planv1.AccountUsage
	for _, provider := range []planv1.Provider{planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_CODEX,
		planv1.Provider_PROVIDER_ANTIGRAVITY} {
		options := h.accountOptions(provider)
		next, ok := h.choose(&planv1.Task{Provider: provider}, options, now)
		h.accountMu.Lock()
		for _, o := range options {
			k := accountKey{provider, o.Name}
			failed, tried := h.quota.failed[k]
			if o.Quota == nil && !tried {
				continue
			}
			u := &planv1.AccountUsage{Provider: provider, Account: o.Name, ProbeError: failed.why, Paused: o.Paused,
				Next: ok && o.Name == next && len(options) > 1}
			if !o.LimitedUntil.IsZero() && o.LimitedUntil.After(now) {
				u.LimitedUntil = timestamppb.New(o.LimitedUntil)
			}
			if q := o.Quota; q != nil {
				u.FiveHour, u.SevenDay = usageWindow(q.FiveHour, now), usageWindow(q.SevenDay, now)
				u.ReadTime, u.Source = timestamppb.New(q.Read), q.Source
			}
			out = append(out, u)
		}
		h.accountMu.Unlock()
	}
	return out
}

// usageWindow is w as the proto says it: none used once it reset.
func usageWindow(w *UsageWindow, now time.Time) *planv1.UsageWindow {
	if w == nil {
		return nil
	}
	out := &planv1.UsageWindow{UsedPercent: math.Round(w.used(now)*10000) / 100}
	if !w.ResetsAt.IsZero() && w.ResetsAt.After(now) {
		out.ResetsTime = timestamppb.New(w.ResetsAt)
	}
	return out
}

// copyQuota is a copy of q, that a policy may keep.
func copyQuota(q Quota) *Quota {
	c := q
	for _, w := range []**UsageWindow{&c.FiveHour, &c.SevenDay} {
		if *w != nil {
			v := **w
			*w = &v
		}
	}
	return &c
}
