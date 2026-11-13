package harness

// The quota of each sign-in (T33). Djinn knows the usage limits of every sign-in of an agent that
// tells them: the 5-hour session and the week, how much of each is used and when it resets. Two sources say them:
// a probe that asks the agent without a model turn (Claude Code's get_usage control request, every quotaEvery, for
// an agent with accounts only), and the stream of each worker running on the sign-in (claude's rate_limit_event, at
// each turn). The latest reading wins. Both tell, too, whether the sign-in may spend extra credits past its limits.
//
// Two modes pick a sign-in (decision Q339 C, Settings.account_mode). Dynamic, by default: among the free sign-ins of
// the agent with quota left, the one whose week resets first (decision Q258 B: what is left of a week is lost at its
// reset), two weeks within quotaTie going to the least used; then those whose quota is not known; then the
// pseudo-unlimited ones; each group in the developer's order. Manual, FirstInOrder (decision Q295 B): the first in the
// developer's order that is free and not known out of quota; a quota never read holds none back. Either way, a task
// that ran keeps its sign-in while that one may (its session and its prompt cache stay there), and with none such the
// task starts in overflow (decision L2 of 06/11): on the first sign-in not paused that may spend extra credits, as
// Djinn last read it, else on the first whose extra credits are unknown, else on the first not paused; if the agent
// refuses, the worker hits the limit, which holds that sign-in, and the task waits for the reset as before.
//
// A pseudo-unlimited sign-in (AccountOption.Unlimited) is one that gives no quota at all: its agent tells none (no
// UsageProber: Codex, Antigravity), or Djinn read it and it told no window (a Claude sign-in on an API key: the probe
// answers with no window while claude auth status says it is signed in on a pay-per-use key, claude_usage.go). A
// probe that fails, a Claude subscription that tells no window, or a Claude sign-in never read, is not: its quota is
// only unknown. The developer corrects the detection either way
// (djinn lead unlimited, Settings.unlimited_forced and unlimited_unforced).

import (
	"cmp"
	"context"
	"math"
	"slices"
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

// quotaTie is how close two weeks' resets are for the dynamic mode to take them as one: the least used goes first.
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
	// Extra tells whether the sign-in may spend extra credits past its limits (get_usage's extra_usage, a worker's
	// overageStatus); nil when not told.
	Extra *bool
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
		if q.Extra == nil && ok {
			q.Extra = old.Extra // A worker's turn under its limits does not say it: what the last reading said holds.
		}
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

// extra tells whether o may spend extra credits past its limits, as Djinn read it at most quotaStale ago: known, and
// yes or no.
func (o AccountOption) extra(now time.Time) (known, yes bool) {
	q := o.Quota
	if q == nil || q.Extra == nil || now.Sub(q.Read) > quotaStale {
		return false, false
	}
	return true, *q.Extra
}

// open tells whether a worker may start on o at now within its limits: free, and not known out of quota.
func (o AccountOption) open(now time.Time) bool {
	if !o.Free(now) {
		return false
	}
	known, left := o.room(now)
	return !known || left
}

// weekEnd is when o's week resets, for the dynamic mode: a week not started, or past, ends last.
func (o AccountOption) weekEnd(now time.Time) time.Time {
	if end := o.Quota.SevenDay.ResetsAt; end.After(now) {
		return end
	}
	return time.Unix(1<<62, 0)
}

// overflowRank ranks o for a session that starts past its limits, the lowest first: one that may spend extra credits,
// one whose extra credits are unknown, one that may not, then one a limit holds (a worker hit it: the agent refused it
// already, but a limit learnt from a message does not hold a task back alone, W409); -1 when paused.
func (o AccountOption) overflowRank(now time.Time) int {
	switch known, yes := o.extra(now); {
	case o.Paused:
		return -1
	case !o.Free(now):
		return 3
	case known && yes:
		return 0
	case !known:
		return 1
	}
	return 2
}

// FirstInOrder is the policy by default (decision Q295 B). A task that ran stays on its sign-in while it is free and
// not known to be out of quota. Otherwise the first such sign-in in the developer's order. With none, the task starts
// in overflow (Overflow), on the sign-in of the lowest overflowRank, the one it ran on first, then the first in the
// order. None when each is paused.
func FirstInOrder(c AccountChoice) (AccountPick, bool) {
	now := c.Now
	if c.Ran {
		for _, o := range c.Options {
			if o.Name == c.Current && o.open(now) {
				return AccountPick{Name: o.Name}, true
			}
		}
	}
	for _, o := range c.Options {
		if o.open(now) {
			return AccountPick{Name: o.Name}, true
		}
	}
	return overflow(c)
}

// Dynamic is the policy by default (decision Q339 C). A task that ran stays on its sign-in while it is free and not
// known out of quota, or pseudo-unlimited. Otherwise, among the free sign-ins of the agent with quota left, the one
// whose week resets first, two within quotaTie of each other going to the least used; then the first free one whose
// quota is unknown; then the first free pseudo-unlimited one; each in the developer's order. With none, the task
// starts in overflow, as with FirstInOrder.
func Dynamic(c AccountChoice) (AccountPick, bool) {
	now := c.Now
	open := func(o AccountOption) bool { return o.Free(now) && (o.Unlimited || o.open(now)) }
	if c.Ran {
		for _, o := range c.Options {
			if o.Name == c.Current && open(o) {
				return AccountPick{Name: o.Name}, true
			}
		}
	}
	var best, unknown, unlimited *AccountOption
	for i := range c.Options {
		o := &c.Options[i]
		if !open(*o) {
			continue
		}
		known, _ := o.room(now)
		switch {
		case o.Unlimited:
			if unlimited == nil {
				unlimited = o
			}
		case !known:
			if unknown == nil {
				unknown = o
			}
		case best == nil:
			best = o
		default:
			d := o.weekEnd(now).Sub(best.weekEnd(now))
			if d < -quotaTie || d.Abs() <= quotaTie && o.Quota.SevenDay.used(now) < best.Quota.SevenDay.used(now) {
				best = o
			}
		}
	}
	for _, o := range []*AccountOption{best, unknown, unlimited} {
		if o != nil {
			return AccountPick{Name: o.Name}, true
		}
	}
	return overflow(c)
}

// ByMode is the policy of the mode the developer chose (AccountChoice.Mode): Dynamic by default, FirstInOrder in the
// manual mode.
func ByMode(c AccountChoice) (AccountPick, bool) {
	if c.Mode == planv1.AccountMode_ACCOUNT_MODE_MANUAL {
		return FirstInOrder(c)
	}
	return Dynamic(c)
}

// overflow is the sign-in of a task that starts past its limits, each sign-in of its agent full, limited or paused:
// the one of the lowest overflowRank, the one it ran on first, then the first in the order. None when each is paused.
func overflow(c AccountChoice) (AccountPick, bool) {
	now := c.Now
	best := -1
	for _, o := range c.Options {
		if r := o.overflowRank(now); r >= 0 && (best < 0 || r < best) {
			best = r
		}
	}
	if best < 0 {
		return AccountPick{}, false
	}
	if c.Ran {
		for _, o := range c.Options {
			if o.Name == c.Current && o.overflowRank(now) == best {
				return AccountPick{Name: o.Name, Overflow: true}, true
			}
		}
	}
	for _, o := range c.Options {
		if o.overflowRank(now) == best {
			return AccountPick{Name: o.Name, Overflow: true}, true
		}
	}
	return AccountPick{}, false
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
		agent, _ := h.provider(provider)
		prober, ok := agent.(UsageProber)
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
// quota Djinn read or tried to, in the developer's order of priority, the one a new task would start on now marked next.
func (h *Harness) Usage() []*planv1.AccountUsage {
	if h.home == "" {
		return nil
	}
	now := h.now()
	overflowing := map[accountKey]bool{} // The sign-ins a worker runs on now past their limits (Overflowing).
	h.mu.Lock()
	for _, r := range h.runs {
		if r.worker != nil && r.overflow && r.shown != nil {
			overflowing[accountKey{agentOf(r.shown), r.shown.GetAccount()}] = true
		}
	}
	h.mu.Unlock()
	var out []*planv1.AccountUsage
	for _, provider := range []planv1.Provider{planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_CODEX,
		planv1.Provider_PROVIDER_ANTIGRAVITY} {
		options := h.accountOptions(provider)
		next, ok := h.choose(&planv1.Task{Provider: provider}, options, now)
		h.accountMu.Lock()
		for _, o := range options {
			k := accountKey{provider, o.Name}
			failed, tried := h.quota.failed[k]
			if o.Quota == nil && !tried && !overflowing[k] && !o.Unlimited &&
				o.SignIn.Unlimited == planv1.UnlimitedSetting_UNLIMITED_SETTING_UNSPECIFIED {
				continue // Nothing to say: no reading, and neither unlimited nor a word of the developer's.
			}
			u := &planv1.AccountUsage{Provider: provider, Account: o.Name, ProbeError: failed.why, Paused: o.Paused,
				Next: ok && o.Name == next.Name && len(options) > 1, Overflowing: overflowing[k],
				Unlimited: o.Unlimited, UnlimitedDetected: o.Detected, UnlimitedSetting: o.SignIn.Unlimited}
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
	order := plan.AccountOrder(h.home)
	rank := func(u *planv1.AccountUsage) int {
		return slices.IndexFunc(order, func(s plan.SignIn) bool { return s.Provider == u.GetProvider() && s.Name == u.GetAccount() })
	}
	slices.SortStableFunc(out, func(a, b *planv1.AccountUsage) int { return cmp.Compare(rank(a), rank(b)) })
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
