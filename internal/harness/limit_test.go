package harness

import (
	"testing"
	"time"
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
			Limit{"the account's session limit", time.Date(2026, 10, 8, 7, 20, 0, 0, paris)}, true},
		// A time already past today is tomorrow's.
		{"You've hit your weekly limit · resets 6am (Europe/Paris)",
			Limit{"the account's weekly limit", time.Date(2026, 10, 9, 6, 0, 0, 0, paris)}, true},
		{"Claude AI usage limit reached|1791436800", Limit{"the account's usage limit", time.Unix(1791436800, 0)}, true},
		// codex/limit.jsonl and antigravity/limit.jsonl: no reset time.
		{"usageLimitExceeded: You've hit your usage limit. Try again later.", Limit{What: "the account's usage limit"}, true},
		{"RESOURCE_EXHAUSTED: quota exceeded for aiplatform.googleapis.com/generate_content_requests_per_minute",
			Limit{What: "the provider's quota"}, true},
		{"API Error: 500 Internal server error", Limit{}, false},
		{"exit code 1", Limit{}, false},
	}
	for _, c := range cases {
		got, ok := limitIn(c.reason, now)
		if ok != c.ok || got.What != c.want.What || !got.Until.Equal(c.want.Until) {
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
