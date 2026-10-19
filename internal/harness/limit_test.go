package harness

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// TestLimitIn reads the providers' own messages: which limit, and when it resets.
func TestLimitIn(t *testing.T) {
	t.Parallel()
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no time zone database")
	}
	now := time.Date(2026, 10, 8, 4, 28, 19, 0, time.UTC)
	cases := []struct {
		reason string
		want   Limit
		ok     bool
	}{
		// Real, claude 2.1.293: 7:20am in Paris is 05:20 UTC.
		{"You've hit your session limit · resets 7:20am (Europe/Paris)",
			Limit{What: "the account's session limit", Until: time.Date(2026, 10, 8, 7, 20, 0, 0, paris)}, true},
		// A time already past today is tomorrow's.
		{"You've hit your weekly limit · resets 6am (Europe/Paris)",
			Limit{What: "the account's weekly limit", Until: time.Date(2026, 10, 9, 6, 0, 0, 0, paris)}, true},
		{"Claude AI usage limit reached|1791436800", Limit{What: "the account's usage limit", Until: time.Unix(1791436800, 0)}, true},
		// codex/limit.jsonl and antigravity/limit.jsonl: no reset time.
		{"usageLimitExceeded: You've hit your usage limit. Try again later.", Limit{What: "the account's usage limit"}, true},
		{"RESOURCE_EXHAUSTED: quota exceeded for aiplatform.googleapis.com/generate_content_requests_per_minute",
			Limit{What: "the provider's quota", PerMinute: true}, true},
		{"RESOURCE_EXHAUSTED: quota exceeded for model claude-opus-4-6",
			Limit{What: "the provider's quota", Model: "claude-opus-4-6", PerMinute: true}, true},
		{"API Error: 500 Internal server error", Limit{}, false},
		{"exit code 1", Limit{}, false},
	}
	for _, c := range cases {
		got, ok := limitIn(c.reason, now)
		if ok != c.ok || got.What != c.want.What || got.Model != c.want.Model || !got.Until.Equal(c.want.Until) || got.PerMinute != c.want.PerMinute {
			t.Errorf("limitIn(%q) = %+v, %v; want %+v, %v", c.reason, got, ok, c.want, c.ok)
		}
	}
}

func TestLimitBackoff(t *testing.T) {
	t.Parallel()
	for resumes, want := range []time.Duration{15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 2 * time.Hour} {
		if got := limitBackoff(int32(resumes)); got != want {
			t.Errorf("limitBackoff(%d) = %v, want %v", resumes, got, want)
		}
	}
}

type claudeFixture struct {
	events []Event
	res    Result
}

func (f claudeFixture) Start(context.Context, Spec) (Worker, error) {
	ch := make(chan Event, len(f.events))
	for _, ev := range f.events {
		ch <- ev
	}
	close(ch)
	return fixtureWorker{events: ch, res: f.res}, nil
}

type fixtureWorker struct {
	events <-chan Event
	res    Result
}

func (w fixtureWorker) Events() <-chan Event { return w.events }
func (w fixtureWorker) Send(string) error    { return ErrClosed }
func (w fixtureWorker) Stop()                {}
func (w fixtureWorker) Wait() Result         { return w.res }

// playClaudeFixture replays a recorded Claude JSONL fixture in-process through claudeParser.
func playClaudeFixture(t *testing.T, path string) Provider {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var p claudeParser
	var f claudeFixture
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		events, end := p.stdout(line)
		f.events = append(f.events, events...)
		if end != nil && end.failure != "" {
			f.res.Err = errors.New(end.failure)
		}
	}
	return f
}

// TestSessionLimitBounded: a task the limit stops each time it resumes waits after a backoff once the reset it was
// given is past (15 minutes, doubled each time), and after 3 resumes it fails, saying so.
func TestSessionLimitBounded(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = playClaudeFixture(t, "testdata/claude/session-limit.jsonl")
	clock := &fakeClock{at: time.Date(2026, 10, 8, 4, 28, 30, 0, time.UTC)}
	e := upWith(t, t.TempDir(), providers, WithClock(clock.now), WithTick(time.Hour))
	wishID, _ := e.wish(t, t.TempDir())
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Reopen the leads", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()
	got := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_RESUMING))
	for i := range int32(maxResumes) {
		now := got.GetResumeAfter().AsTime().Add(time.Second)
		clock.set(now)
		e.h.wake()
		if i == maxResumes-1 {
			break
		}
		got = e.until(t, id, func(t *planv1.Task) bool {
			return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && t.GetResumes() == i+1 && t.GetResumeAfter() != nil
		})
		// The reset the stream gives is past: a backoff, 30 minutes after the first resume, then an hour.
		if wait := got.GetResumeAfter().AsTime().Sub(now); wait != limitBackoff(i+1) || !strings.Contains(got.GetWaitReason(), "tries again at") {
			t.Errorf("resume %d: waits %v (%s), want %v", i+1, wait, got.GetWaitReason(), limitBackoff(i+1))
		}
	}
	got = e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_FAILED))
	if got.GetResumes() != maxResumes || got.GetError() != "resumed 3 times without finishing; the last time: You've hit your session limit · resets 7:20am (Europe/Paris)" {
		t.Errorf("after %d resumes: %v", maxResumes, got)
	}
}
