package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestFirstInOrder: a new task goes to the first sign-in in the developer's order that is free and not known out of
// quota (95 % of its 5 hours or of its week); a quota never read holds none back; a task that ran stays where it ran
// while it may. With none such, the task starts in overflow: on the first sign-in that may spend extra credits, else
// the first whose extra credits are unknown, else the first not paused, a limited one last (W409); none when each is
// paused.
func TestFirstInOrder(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	own := AccountOption{}
	work := AccountOption{SignIn: plan.SignIn{Name: "work"}}
	perso := AccountOption{SignIn: plan.SignIn{Name: "perso"}}
	// week gives o a quota read now: its week used and resetting in, its 5 hours used.
	week := func(o AccountOption, used float64, in time.Duration, session float64) AccountOption {
		o.Quota = &Quota{Read: now, SevenDay: &UsageWindow{Used: used, ResetsAt: now.Add(in)},
			FiveHour: &UsageWindow{Used: session, ResetsAt: now.Add(2 * time.Hour)}}
		return o
	}
	full := func(o AccountOption) AccountOption { return week(o, .97, day, .5) }
	// extra says whether o may spend extra credits, read at.
	extra := func(o AccountOption, yes bool, at time.Time) AccountOption {
		if o.Quota == nil {
			o.Quota = &Quota{}
		}
		q := *o.Quota
		q.Extra, q.Read = &yes, at
		o.Quota = &q
		return o
	}
	read := func(o AccountOption, at time.Time) AccountOption {
		q := *o.Quota
		q.Read = at
		o.Quota = &q
		return o
	}
	paused := func(o AccountOption) AccountOption { o.Paused = true; return o }
	limited := func(o AccountOption) AccountOption { o.LimitedUntil = now.Add(time.Hour); return o }
	for _, c := range []struct {
		name     string
		options  []AccountOption
		current  string
		ran      bool
		want     string
		overflow bool
		ok       bool
	}{
		{"the order, whatever the weeks", []AccountOption{week(work, .8, 6*day, .1), week(own, .1, time.Hour, .1)}, "", false, "work", false, true},
		{"the first, its quota never read", []AccountOption{perso, week(own, .1, day, .1)}, "", false, "perso", false, true},
		{"out of quota for the week: skipped", []AccountOption{week(work, .96, day, .1), week(own, .5, 3*day, .1)}, "", false, "", false, true},
		{"out of quota for 5 hours: skipped", []AccountOption{week(work, .2, day, .95), week(own, .5, 3*day, .1)}, "", false, "", false, true},
		{"a week past counts as new", []AccountOption{week(work, .99, -time.Minute, .1), own}, "", false, "work", false, true},
		{"paused: skipped", []AccountOption{paused(work), own}, "", false, "", false, true},
		{"limited: skipped", []AccountOption{limited(week(work, .2, day, .1)), own}, "", false, "", false, true},
		{"a stale quota is unknown", []AccountOption{read(full(work), now.Add(-quotaStale-time.Minute)), own}, "", false, "work", false, true},
		{"a task that ran stays", []AccountOption{own, week(work, .6, 6*day, .1)}, "work", true, "work", false, true},
		{"a task that ran, out of quota: the first free", []AccountOption{own, full(work)}, "work", true, "", false, true},
		{"a task that ran, limited: the first free", []AccountOption{limited(own), work, perso}, "", true, "work", false, true},
		{"a forgotten account: the first free", []AccountOption{paused(own), work}, "gone", true, "work", false, true},
		{"each full: the first with extra credits", []AccountOption{full(own), extra(full(work), false, now), extra(full(perso), true, now)}, "", false, "perso", true, true},
		{"each full, extra credits unknown: the first", []AccountOption{full(own), extra(full(work), false, now), full(perso)}, "", false, "", true, true},
		{"each full, no extra credits: the first", []AccountOption{extra(full(own), false, now), extra(full(work), false, now)}, "", false, "", true, true},
		{"extra credits unknown before none", []AccountOption{extra(full(own), false, now), full(work)}, "", false, "work", true, true},
		{"each full: the one it ran on, with extra credits", []AccountOption{extra(full(own), true, now), extra(full(work), true, now)}, "work", true, "work", true, true},
		{"each full: extra credits before the one it ran on", []AccountOption{extra(full(own), true, now), extra(full(work), false, now)}, "work", true, "", true, true},
		{"full or paused: the one not paused", []AccountOption{paused(own), full(work)}, "", false, "work", true, true},
		{"a free one full before a limited one", []AccountOption{limited(own), extra(full(work), false, now)}, "", false, "work", true, true},
		{"each limited: the one it ran on (W409)", []AccountOption{limited(own), limited(work)}, "work", true, "work", true, true},
		{"each limited, a new task: the first", []AccountOption{limited(own), limited(extra(work, true, now))}, "", false, "", true, true},
		{"each paused: none", []AccountOption{paused(week(own, .1, day, .1)), paused(work)}, "work", true, "", false, false},
	} {
		got, ok := FirstInOrder(AccountChoice{Provider: planv1.Provider_PROVIDER_CLAUDE, Options: c.options, Current: c.current, Ran: c.ran, Now: now})
		if got.Name != c.want || got.Overflow != c.overflow || ok != c.ok {
			t.Errorf("%s: %+v, %v; want %q, overflow %v, %v", c.name, got, ok, c.want, c.overflow, c.ok)
		}
	}
}

// TestParseClaudeUsage reads claude's answer to get_usage: a real one, a signed-out folder, a refusal, another
// request's answer.
func TestParseClaudeUsage(t *testing.T) {
	t.Parallel()
	lines := fixtureLines(t, filepath.Join("testdata", "claude", "usage", "get-usage.jsonl"))
	q, err := parseClaudeUsage([]byte(lines[0]))
	if err != nil {
		t.Fatal(err)
	}
	five, week := q.FiveHour, q.SevenDay
	if five == nil || week == nil || five.Used != .44 || week.Used != .27 ||
		!five.ResetsAt.Equal(time.Date(2026, 11, 5, 9, 40, 0, 140285000, time.UTC)) ||
		!week.ResetsAt.Equal(time.Date(2026, 11, 11, 11, 0, 0, 140303000, time.UTC)) || q.Source != planv1.UsageSource_USAGE_SOURCE_PROBE ||
		q.Extra == nil || !*q.Extra {
		t.Errorf("quota = %+v, %+v, %+v", q, five, week)
	}
	spent := strings.Replace(lines[0], `"spend_limit_reached":false`, `"spend_limit_reached":true`, 1)
	if q, err := parseClaudeUsage([]byte(spent)); err != nil || q.Extra == nil || *q.Extra {
		t.Errorf("extra credits spent: %+v, %v", q.Extra, err)
	}
	// Seen on claude 2.1.316 in a folder signed in to nothing.
	signedOut := `{"type":"control_response","response":{"subtype":"success","request_id":"djinn-usage","response":{"session":{"total_cost_usd":0},"subscription_type":null,"rate_limits_available":false,"rate_limits":null,"behaviors":null}}}`
	if _, err := parseClaudeUsage([]byte(signedOut)); !errors.Is(err, errNoUsage) {
		t.Errorf("signed out: %v, want errNoUsage", err)
	}
	refused := `{"type":"control_response","response":{"subtype":"error","request_id":"djinn-usage","error":"Unsupported control request subtype: get_usage"}}`
	if _, err := parseClaudeUsage([]byte(refused)); err == nil || !strings.Contains(err.Error(), "Unsupported control request") {
		t.Errorf("refused: %v", err)
	}
	other := `{"type":"control_response","response":{"subtype":"success","request_id":"r9","response":{}}}`
	if _, err := parseClaudeUsage([]byte(other)); !errors.Is(err, errOtherResponse) {
		t.Errorf("another request: %v", err)
	}
	notStarted := `{"type":"control_response","response":{"subtype":"success","request_id":"djinn-usage","response":{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":0,"resets_at":null},"seven_day":{"utilization":3,"resets_at":"2026-11-11T11:00:00+00:00"}}}}}`
	if q, err := parseClaudeUsage([]byte(notStarted)); err != nil || !q.FiveHour.ResetsAt.IsZero() || q.SevenDay.Used != .03 || q.Extra != nil {
		t.Errorf("a window not started: %+v, %v", q, err)
	}
}

// TestClaudeUsageProbe asks the test binary playing claude for its usage through the real process path: it gets the
// request on its input and Djinn reads its answer; a claude that ends without one fails the probe.
func TestClaudeUsageProbe(t *testing.T) {
	t.Parallel()
	env, args, input := fake{provider: "claude", fixture: "usage/get-usage", end: "eof"}.env(t)
	q, err := Claude{Command: os.Args[0]}.Usage(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	if q.FiveHour.Used != .44 || q.SevenDay.Used != .27 {
		t.Errorf("quota = %+v, %+v", q.FiveHour, q.SevenDay)
	}
	if got, _ := os.ReadFile(input); strings.TrimSpace(string(got)) != claudeUsageRequest {
		t.Errorf("input = %q", got)
	}
	if got, _ := os.ReadFile(args); !strings.Contains(string(got), "--no-session-persistence\n--strict-mcp-config") {
		t.Errorf("args = %q", got)
	}

	env, _, _ = fake{provider: "claude", fixture: "usage/get-usage-silent", end: "eof"}.env(t)
	if _, err := (Claude{Command: os.Args[0]}).Usage(t.Context(), env); err == nil ||
		!strings.Contains(err.Error(), "claude ended without its usage") {
		t.Errorf("silent: %v", err)
	}
}

// TestParseClaudeWindows: a worker's rate_limit_event tells its sign-in's quota, alone in an event of no kind while
// under the limit, on the status event past a warning.
func TestParseClaudeWindows(t *testing.T) {
	t.Parallel()
	evs, _ := parseClaude(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1791418800,"rateLimitType":"five_hour","unifiedWindows":{"five_hour":{"utilization":0.58,"resetsAt":1791418800},"seven_day":{"utilization":0.14,"resetsAt":1791975600}}}}`)
	if len(evs) != 1 || evs[0].Kind != planv1.TaskEventKind_TASK_EVENT_KIND_UNSPECIFIED || evs[0].Quota == nil {
		t.Fatalf("allowed: %+v", evs)
	}
	q := evs[0].Quota
	if q.FiveHour.Used != .58 || q.SevenDay.Used != .14 || !q.SevenDay.ResetsAt.Equal(time.Unix(1791975600, 0)) ||
		q.Source != planv1.UsageSource_USAGE_SOURCE_WORKER || q.Extra != nil {
		t.Errorf("quota = %+v, %+v, %+v", q, q.FiveHour, q.SevenDay)
	}
	lines := fixtureLines(t, filepath.Join("testdata", "claude", "session-limit.jsonl"))
	evs, _ = parseClaude(lines[2])
	if len(evs) != 1 || evs[0].Kind != planv1.TaskEventKind_TASK_EVENT_KIND_STATUS || evs[0].Limit == nil ||
		evs[0].Quota == nil || evs[0].Quota.FiveHour.Used != 1 || evs[0].Quota.SevenDay.Used != .32 {
		t.Errorf("rejected: %+v", evs)
	} else if x := evs[0].Quota.Extra; x == nil || *x {
		t.Errorf("rejected: extra credits %v, want none (overageStatus rejected)", x)
	}
	evs, _ = parseClaude(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","overageStatus":"allowed","isUsingOverage":true,"unifiedWindows":{"five_hour":{"utilization":1,"resetsAt":1791418800}}}}`)
	if len(evs) != 1 || evs[0].Quota == nil || evs[0].Quota.Extra == nil || !*evs[0].Quota.Extra {
		t.Errorf("on extra credits: %+v", evs)
	}
}

// usageProbe is a claude worker as signInProbe, that tells each sign-in's quota by its folder, and counts the probes.
type usageProbe struct {
	*signInProbe
	mu     sync.Mutex
	quotas map[string]Quota // by CLAUDE_CONFIG_DIR, "" for its own
	asked  map[string]int
}

func (p *usageProbe) Usage(_ context.Context, env []string) (Quota, error) {
	dir := configDir(Spec{Env: env})
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked[dir]++
	q, ok := p.quotas[dir]
	if !ok {
		return Quota{}, errNoUsage
	}
	return q, nil
}

// TestQuotaChoosesAccount: with two accounts, the probe reads each sign-in at Djinn's start; a new task starts on the
// first account in the developer's order with quota left, the own sign-in's probe failure is shown, and LeadService's
// usage says it all, in that order.
func TestQuotaChoosesAccount(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	workDir, persoDir := filepath.Join(t.TempDir(), ".claude-work"), filepath.Join(t.TempDir(), ".claude-perso")
	signIns(t, home, &uiv1.Settings{Accounts: []*planv1.Account{
		{Name: "work", Provider: planv1.Provider_PROVIDER_CLAUDE, ConfigDirectory: workDir},
		{Name: "perso", Provider: planv1.Provider_PROVIDER_CLAUDE, ConfigDirectory: persoDir},
	}, AccountOrder: []*uiv1.SignInRef{
		{Provider: planv1.Provider_PROVIDER_CODEX},
		{Provider: planv1.Provider_PROVIDER_CLAUDE, Account: "work"},
		{Provider: planv1.Provider_PROVIDER_CLAUDE, Account: "perso"},
	}})
	now := time.Now()
	probe := &usageProbe{signInProbe: &signInProbe{}, asked: map[string]int{}, quotas: map[string]Quota{
		workDir:  {SevenDay: &UsageWindow{Used: .97, ResetsAt: now.Add(10 * time.Hour)}},
		persoDir: {SevenDay: &UsageWindow{Used: .3, ResetsAt: now.Add(6 * 24 * time.Hour)}},
	}}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = probe
	e := upWith(t, home, providers)
	wishID, _ := e.wish(t, nativeFolder(t))
	var usage []*planv1.AccountUsage
	deadline := time.Now().Add(10 * time.Second)
	for usage = e.h.Usage(); len(usage) < 3; usage = e.h.Usage() {
		if time.Now().After(deadline) {
			t.Fatalf("usage read: %v", usage)
		}
		time.Sleep(10 * time.Millisecond)
	}
	work, perso, own := usage[0], usage[1], usage[2]
	if own.GetAccount() != "" || own.GetProbeError() != errNoUsage.Error() || own.GetReadTime() != nil || own.GetNext() {
		t.Errorf("own = %v", own)
	}
	if work.GetAccount() != "work" || work.GetSevenDay().GetUsedPercent() != 97 || work.GetSource() != planv1.UsageSource_USAGE_SOURCE_PROBE || work.GetNext() {
		t.Errorf("work = %v", work)
	}
	if perso.GetAccount() != "perso" || !perso.GetNext() || perso.GetSevenDay().GetResetsTime() == nil {
		t.Errorf("perso = %v", perso)
	}

	task := e.spawnClaude(t, wishID)
	if got := configDir(probe.started(t, 0)); got != persoDir {
		t.Errorf("CLAUDE_CONFIG_DIR %q, want perso's %q", got, persoDir)
	}
	if done := e.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE)); done.GetAccount() != "perso" {
		t.Errorf("account %q, want perso", done.GetAccount())
	}
	// A reading fresh from the start, or a probe failed then, is not asked again before quotaEvery.
	e.h.probeQuotas(t.Context())
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if probe.asked[workDir] != 1 || probe.asked[persoDir] != 1 || probe.asked[""] != 1 {
		t.Errorf("asked %v: once each", probe.asked)
	}
}

// TestWorkerTellsQuota: the rate_limit_event of a claude worker on its own sign-in gives that sign-in's quota, and
// the event that only tells it is not written.
func TestWorkerTellsQuota(t *testing.T) {
	t.Parallel()
	claudeEnv, _, _ := fake{provider: "claude", fixture: "success-haiku", end: "eof"}.env(t)
	providers, _ := claudePlaying(claudeEnv)
	e := upWith(t, t.TempDir(), providers)
	wishID, _ := e.wish(t, gitRepo(t))
	task := e.spawnClaude(t, wishID)
	e.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	// The fixture's windows reset in October 2026: as Djinn says them now, none is used.
	usage := e.h.Usage()
	if len(usage) != 1 || usage[0].GetAccount() != "" || usage[0].GetSource() != planv1.UsageSource_USAGE_SOURCE_WORKER ||
		usage[0].GetSevenDay() == nil || usage[0].GetNext() {
		t.Errorf("usage = %v", usage)
	}
	e.h.accountMu.Lock()
	q, ok := e.h.quotaOf(accountKey{planv1.Provider_PROVIDER_CLAUDE, ""})
	e.h.accountMu.Unlock()
	if !ok || q.FiveHour.Used != .58 || q.SevenDay.Used != .14 || !q.SevenDay.ResetsAt.Equal(time.Unix(1791975600, 0)) {
		t.Errorf("quota kept = %+v", q)
	}
	events, err := store.List[*planv1.TaskEvent](t.Context(), e.db, store.Where{"task_id": task.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_UNSPECIFIED {
			t.Errorf("an event of no kind written: %v", ev)
		}
	}
}
