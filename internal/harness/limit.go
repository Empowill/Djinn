package harness

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Limit is a provider's usage limit that stopped a worker: the account's session or weekly limit, a quota, a rate
// limit. The worker's task waits until it resets, then Djinn resumes it.
type Limit struct {
	// What is the limit, for a reader: "the account's session limit".
	What string
	// Until is when it resets; zero when the provider did not say.
	Until time.Time
}

// limitKinds name the limits a provider's message speaks of, the most precise first. The messages are the
// providers' own: Claude's "You've hit your session limit · resets 7:20am (Europe/Paris)" (real, claude 2.1.293),
// its older "Claude AI usage limit reached|<unix time>", Codex's usageLimitExceeded, agy's RESOURCE_EXHAUSTED
// (supposed, docs/providers.md).
var limitKinds = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`(?i)\bsession limit\b|\bfive_hour\b`), "the account's session limit"},
	{regexp.MustCompile(`(?i)\bweekly limit\b|\bseven_day`), "the account's weekly limit"},
	{regexp.MustCompile(`(?i)usage limit|usageLimitExceeded|usage_limit_reached`), "the account's usage limit"},
	{regexp.MustCompile(`(?i)RESOURCE_EXHAUSTED|quota exceeded|quota exhausted`), "the provider's quota"},
	{regexp.MustCompile(`(?i)rate[ _]limit(ed)?\b|too many requests|\bHTTP 429\b`), "the provider's rate limit"},
}

// limitIn finds a usage limit in the reason a worker failed with, and when it resets when the message says it.
func limitIn(reason string, now time.Time) (Limit, bool) {
	for _, k := range limitKinds {
		if k.re.MatchString(reason) {
			return Limit{What: k.what, Until: resetIn(reason, now)}, true
		}
	}
	return Limit{}, false
}

// limitWhat names a limit by the kind Claude's rate_limit_event gives (five_hour, seven_day…).
func limitWhat(kind string) string {
	for _, k := range limitKinds {
		if k.re.MatchString(kind) {
			return k.what
		}
	}
	return "the account's usage limit"
}

var (
	// "resets 7:20am (Europe/Paris)", "resets at 19:05", "resets 7am".
	resetClock = regexp.MustCompile(`(?i)resets?\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*([ap]m)?(?:\s*\(([^)\s]+)\))?`)
	// "Claude AI usage limit reached|1791436800".
	resetUnix = regexp.MustCompile(`limit reached\|(\d{9,11})\b`)
)

// resetIn reads when a limit resets from its message: a time of day, the next one after now in the zone the
// message names (the machine's own when it names none, or one this system does not know), or a Unix time. Zero
// when it says neither.
func resetIn(reason string, now time.Time) time.Time {
	if m := resetUnix.FindStringSubmatch(reason); m != nil {
		if sec, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			return time.Unix(sec, 0)
		}
	}
	m := resetClock.FindStringSubmatch(reason)
	if m == nil {
		return time.Time{}
	}
	hour, _ := strconv.Atoi(m[1])
	minute, _ := strconv.Atoi(m[2])
	switch strings.ToLower(m[3]) {
	case "am":
		if hour == 12 {
			hour = 0
		}
	case "pm":
		if hour < 12 {
			hour += 12
		}
	}
	if hour > 23 || minute > 59 {
		return time.Time{}
	}
	zone := time.Local
	if m[4] != "" {
		if z, err := time.LoadLocation(m[4]); err == nil {
			zone = z
		}
	}
	local := now.In(zone)
	at := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, zone)
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at
}

// limitBackoff is how long a task waits for a limit that did not say when it resets: 15 minutes, doubled at each
// resume, at most 2 hours.
func limitBackoff(resumes int32) time.Duration {
	d := 15 * time.Minute
	for range resumes {
		if d *= 2; d >= 2*time.Hour {
			return 2 * time.Hour
		}
	}
	return d
}

// limitText says what a task waits for: "the account's session limit, resets at 07:20", in the machine's time.
func limitText(l Limit, after time.Time, now time.Time) string {
	if !l.Until.IsZero() {
		return l.What + ", resets at " + clockText(l.Until, now)
	}
	return l.What + ", tries again at " + clockText(after, now)
}

// clockText writes a time of the machine's day: 07:20, or with its date when it is not today.
func clockText(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	if y, m, d := t.Date(); y != now.Year() || m != now.Month() || d != now.Day() {
		return t.Format("2006-01-02 15:04")
	}
	return t.Format("15:04")
}
