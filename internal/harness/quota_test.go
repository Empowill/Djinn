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

// TestEarliestWeek: a new task goes to the free sign-in whose week resets first among those with quota left; one out
// of quota (95 % of its 5 hours or of its week), paused or limited is skipped; a task that ran stays where it ran
// while it may; without a fresh quota the policy is the simple overflow, FirstFree.
func TestEarliestWeek(t *testing.T) {
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
	read := func(o AccountOption, at time.Time) AccountOption {
		q := *o.Quota
		q.Read = at
		o.Quota = &q
		return o
	}
	paused := func(o AccountOption) AccountOption { o.Paused = true; return o }
	limited := func(o AccountOption) AccountOption { o.LimitedUntil = now.Add(time.Hour); return o }
	for _, c := range []struct {
		name    string
		options []AccountOption
		current string
		ran     bool
		want    string
		ok      bool
	}{
		{"both agree: the week that ends first", []AccountOption{week(own, .6, day, .1), week(work, .2, 5*day, .1)}, "", false, "", true},
		{"the least used ends first: it goes first", []AccountOption{week(own, .8, 6*day, .1), week(work, .3, 10*time.Hour, .1)}, "", false, "work", true},
		{"out of quota for the week: skipped", []AccountOption{week(own, .5, 3*day, .1), week(work, .96, day, .1)}, "", false, "", true},
		{"out of quota for 5 hours: skipped", []AccountOption{week(own, .5, 3*day, .1), week(work, .2, day, .95)}, "", false, "", true},
		{"a week past counts as new", []AccountOption{week(own, .5, 3*day, .1), week(work, .99, -time.Minute, .1)}, "", false, "", true},
		{"paused: skipped", []AccountOption{week(own, .5, 3*day, .1), paused(week(work, .2, day, .1))}, "", false, "", true},
		{"limited: skipped", []AccountOption{week(own, .5, 3*day, .1), limited(week(work, .2, day, .1))}, "", false, "", true},
		{"two weeks within an hour: more left first", []AccountOption{week(own, .7, day, .1), week(work, .4, day+30*time.Minute, .1)}, "", false, "work", true},
		{"a week not started ends last", []AccountOption{week(own, 0, 0, 0), week(work, .4, 2*day, .1)}, "", false, "work", true},
		{"a known quota before an unknown one", []AccountOption{own, week(work, .4, 6*day, .1)}, "", false, "work", true},
		{"the probe silent: the first free", []AccountOption{own, work, perso}, "", false, "", true},
		{"the probe silent, its own paused: the next", []AccountOption{paused(own), work, perso}, "", false, "work", true},
		{"a stale quota is unknown", []AccountOption{own, read(week(work, .4, day, .1), now.Add(-quotaStale-time.Minute))}, "", false, "", true},
		{"a task that ran stays", []AccountOption{week(own, .2, day, .1), week(work, .6, 6*day, .1)}, "work", true, "work", true},
		{"a task that ran stays, its quota unknown", []AccountOption{week(own, .2, day, .1), work}, "work", true, "work", true},
		{"a task that ran, out of quota: moves", []AccountOption{week(own, .2, day, .1), week(work, .97, 6*day, .1)}, "work", true, "", true},
		{"each out of quota: the first, as FirstFree", []AccountOption{week(own, .97, day, .1), week(work, .99, 2*day, .1)}, "", false, "", true},
		{"each out of quota: the one it ran on", []AccountOption{week(own, .97, day, .1), week(work, .99, 2*day, .1)}, "work", true, "work", true},
		{"each paused: none", []AccountOption{paused(week(own, .1, day, .1)), paused(work)}, "", false, "", false},
	} {
		got, ok := EarliestWeek(AccountChoice{Provider: planv1.Provider_PROVIDER_CLAUDE, Options: c.options, Current: c.current, Ran: c.ran, Now: now})
		if got != c.want || ok != c.ok {
			t.Errorf("%s: %q, %v; want %q, %v", c.name, got, ok, c.want, c.ok)
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
		!week.ResetsAt.Equal(time.Date(2026, 11, 11, 11, 0, 0, 140303000, time.UTC)) || q.Source != planv1.UsageSource_USAGE_SOURCE_PROBE {
		t.Errorf("quota = %+v, %+v, %+v", q, five, week)
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
	if q, err := parseClaudeUsage([]byte(notStarted)); err != nil || !q.FiveHour.ResetsAt.IsZero() || q.SevenDay.Used != .03 {
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
		q.Source != planv1.UsageSource_USAGE_SOURCE_WORKER {
		t.Errorf("quota = %+v, %+v, %+v", q, q.FiveHour, q.SevenDay)
	}
	lines := fixtureLines(t, filepath.Join("testdata", "claude", "session-limit.jsonl"))
	evs, _ = parseClaude(lines[2])
	if len(evs) != 1 || evs[0].Kind != planv1.TaskEventKind_TASK_EVENT_KIND_STATUS || evs[0].Limit == nil ||
		evs[0].Quota == nil || evs[0].Quota.FiveHour.Used != 1 || evs[0].Quota.SevenDay.Used != .32 {
		t.Errorf("rejected: %+v", evs)
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
// account whose week resets first, the own sign-in's probe failure is shown, and LeadService's usage says it all.
func TestQuotaChoosesAccount(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	workDir, persoDir := filepath.Join(t.TempDir(), ".claude-work"), filepath.Join(t.TempDir(), ".claude-perso")
	signIns(t, home, &uiv1.Settings{Accounts: []*planv1.Account{
		{Name: "work", Provider: planv1.Provider_PROVIDER_CLAUDE, ConfigDirectory: workDir},
		{Name: "perso", Provider: planv1.Provider_PROVIDER_CLAUDE, ConfigDirectory: persoDir},
	}})
	now := time.Now()
	probe := &usageProbe{signInProbe: &signInProbe{}, asked: map[string]int{}, quotas: map[string]Quota{
		workDir:  {SevenDay: &UsageWindow{Used: .8, ResetsAt: now.Add(6 * 24 * time.Hour)}},
		persoDir: {SevenDay: &UsageWindow{Used: .3, ResetsAt: now.Add(10 * time.Hour)}},
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
	own, work, perso := usage[0], usage[1], usage[2]
	if own.GetAccount() != "" || own.GetProbeError() != errNoUsage.Error() || own.GetReadTime() != nil {
		t.Errorf("own = %v", own)
	}
	if work.GetAccount() != "work" || work.GetSevenDay().GetUsedPercent() != 80 || work.GetSource() != planv1.UsageSource_USAGE_SOURCE_PROBE || work.GetNext() {
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
