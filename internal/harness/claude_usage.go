package harness

// The usage probe of Claude Code (quota.go): claude -p in stream-json answers a get_usage control request with the
// sign-in's usage limits, without a model turn and at no cost. Undocumented: tried on claude 2.1.316 (W374, W380),
// as T3 Code does (apps/server/src/provider/claudeUsageLimits.ts). Djinn reads the 5-hour and the weekly windows, and
// whether the sign-in may spend extra credits past them; the rest of the answer (the credits spent and their limit,
// behaviours, an email) is never kept nor logged.

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// claudeUsageRequest is the control request that asks claude for its sign-in's usage.
const claudeUsageRequest = `{"type":"control_request","request_id":"djinn-usage","request":{"subtype":"get_usage"}}`

// claudeUsageArgs start claude to answer control requests: no session kept, no MCP server, no slash command.
var claudeUsageArgs = []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
	"--no-session-persistence", "--strict-mcp-config", "--disable-slash-commands"}

// errNoUsage is a sign-in that tells no usage limits: signed out, on an API key, or a subscription claude could not
// read.
var errNoUsage = errors.New("no usage limits told: signed out, on an API key, or a subscription not read")

// Usage implements UsageProber: one claude process, in the folder env names, asked once.
func (c Claude) Usage(ctx context.Context, env []string) (Quota, error) {
	command := c.Command
	if command == "" {
		command = "claude"
	}
	cmd := exec.CommandContext(ctx, command, claudeUsageArgs...)
	cmd.Dir = os.TempDir() // No project: its settings and instructions are not read.
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(claudeUsageRequest + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		return Quota{}, err
	}
	if err := cmd.Start(); err != nil {
		return Quota{}, err
	}
	q, found, perr := Quota{}, false, error(nil)
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 64*1024), 4<<20)
	for scan.Scan() {
		line := scan.Bytes()
		if !bytes.Contains(line, []byte(`"control_response"`)) {
			continue
		}
		if q, perr = parseClaudeUsage(line); !errors.Is(perr, errOtherResponse) {
			found = true
			break
		}
	}
	_ = cmd.Process.Kill() // It answered, or said all it would.
	werr := cmd.Wait()
	switch {
	case found && errors.Is(perr, errNoUsage) && claudeOnAPIKey(ctx, command, env):
		// No window told, on an API key: a reading with no window, that makes the sign-in pseudo-unlimited
		// (quota.go). Signed out, or a subscription whose usage claude could not read, it stays a failure, and its
		// quota only unknown.
		return Quota{Source: planv1.UsageSource_USAGE_SOURCE_PROBE}, nil
	case found:
		return q, perr
	case ctx.Err() != nil:
		return Quota{}, fmt.Errorf("no answer: %w", ctx.Err())
	}
	why := strings.TrimSpace(stderr.String())
	if i := strings.LastIndexByte(why, '\n'); i >= 0 {
		why = why[i+1:]
	}
	return Quota{}, fmt.Errorf("claude ended without its usage: %s", clipRunes(cmp.Or(why, fmt.Sprint(werr)), 200))
}

// claudePayPerUse are the authMethod values of claude auth status that bill each request, with no usage limit to
// tell: an API key (ANTHROPIC_API_KEY, a Console sign-in), one given by apiKeyHelper, or a third-party provider
// (Bedrock, Vertex, Foundry). A subscription (claude.ai, oauth_token) is not one, even when get_usage tells no window.
var claudePayPerUse = []string{"api_key", "api_key_helper", "third_party"}

// claudeOnAPIKey tells whether claude auth status, in the folder env names, says it is signed in on a pay-per-use
// key (claudePayPerUse). Its exit status alone does not tell: it exits 0 on a subscription too, and does not check
// the key. Djinn reads only loggedIn and authMethod of what it prints, never its email or organization.
func claudeOnAPIKey(ctx context.Context, command string, env []string) bool {
	cmd := exec.CommandContext(ctx, command, "auth", "status", "--json")
	cmd.Dir = os.TempDir()
	cmd.Env = append(os.Environ(), env...)
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	var status struct {
		LoggedIn   bool   `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
	}
	if json.Unmarshal(out, &status) != nil {
		return false
	}
	return status.LoggedIn && slices.Contains(claudePayPerUse, status.AuthMethod)
}

// errOtherResponse is a control response to another request than Djinn's.
var errOtherResponse = errors.New("another control response")

// claudeUsageWindow is a window of get_usage's rate_limits: utilization in percent, resets_at in RFC 3339.
type claudeUsageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

// parseClaudeUsage reads claude's answer to claudeUsageRequest: its 5-hour and weekly windows.
func parseClaudeUsage(line []byte) (Quota, error) {
	var m struct {
		Type     string `json:"type"`
		Response struct {
			Subtype   string `json:"subtype"`
			RequestID string `json:"request_id"`
			Error     string `json:"error"`
			Response  *struct {
				RateLimitsAvailable bool `json:"rate_limits_available"`
				RateLimits          *struct {
					FiveHour *claudeUsageWindow `json:"five_hour"`
					SevenDay *claudeUsageWindow `json:"seven_day"`
					// ExtraUsage tells whether the sign-in may spend extra credits past its windows.
					ExtraUsage *claudeExtraUsage `json:"extra_usage"`
				} `json:"rate_limits"`
			} `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return Quota{}, fmt.Errorf("read claude's usage: %w", err)
	}
	r := m.Response
	if m.Type != "control_response" || r.RequestID != "djinn-usage" {
		return Quota{}, errOtherResponse
	}
	if r.Subtype != "success" {
		return Quota{}, fmt.Errorf("claude refused the usage request: %s", clipRunes(cmp.Or(r.Error, r.Subtype), 200))
	}
	if r.Response == nil || !r.Response.RateLimitsAvailable || r.Response.RateLimits == nil {
		return Quota{}, errNoUsage
	}
	five, err := r.Response.RateLimits.FiveHour.window()
	if err != nil {
		return Quota{}, err
	}
	week, err := r.Response.RateLimits.SevenDay.window()
	if err != nil {
		return Quota{}, err
	}
	if five == nil && week == nil {
		return Quota{}, errNoUsage
	}
	return Quota{FiveHour: five, SevenDay: week, Source: planv1.UsageSource_USAGE_SOURCE_PROBE,
		Extra: r.Response.RateLimits.ExtraUsage.usable()}, nil
}

// claudeExtraUsage is get_usage's extra_usage: whether the sign-in may spend extra credits past its limits.
type claudeExtraUsage struct {
	IsEnabled         bool    `json:"is_enabled"`
	UserDisabled      bool    `json:"user_disabled"`
	SpendLimitReached bool    `json:"spend_limit_reached"`
	DisabledReason    *string `json:"disabled_reason"`
}

// usable tells whether e lets the sign-in spend extra credits now; nil when not told.
func (e *claudeExtraUsage) usable() *bool {
	if e == nil {
		return nil
	}
	yes := e.IsEnabled && !e.UserDisabled && !e.SpendLimitReached && (e.DisabledReason == nil || *e.DisabledReason == "")
	return &yes
}

// overageUsable tells whether a rate_limit_event's overageStatus lets the sign-in spend extra credits: allowed, or
// allowed with a warning; nil when not told.
func overageUsable(status string) *bool {
	if status == "" {
		return nil
	}
	yes := strings.HasPrefix(status, "allowed")
	return &yes
}

// window is w as Djinn keeps it; nil when not told.
func (w *claudeUsageWindow) window() (*UsageWindow, error) {
	if w == nil || w.Utilization == nil {
		return nil, nil
	}
	out := &UsageWindow{Used: *w.Utilization / 100}
	if w.ResetsAt != nil && *w.ResetsAt != "" {
		t, err := time.Parse(time.RFC3339Nano, *w.ResetsAt)
		if err != nil {
			return nil, fmt.Errorf("read claude's usage reset: %w", err)
		}
		out.ResetsAt = t
	}
	return out, nil
}

// claudeWindows are the unifiedWindows of a rate_limit_event: utilization from 0 to 1, resetsAt in Unix seconds.
type claudeWindows map[string]struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    int64    `json:"resetsAt"`
}

// quota is the sign-in's quota a worker's rate_limit_event tells; nil when it tells no window.
func (ws claudeWindows) quota() *Quota {
	window := func(name string) *UsageWindow {
		w, ok := ws[name]
		if !ok || w.Utilization == nil {
			return nil
		}
		out := &UsageWindow{Used: *w.Utilization}
		if w.ResetsAt > 0 {
			out.ResetsAt = time.Unix(w.ResetsAt, 0)
		}
		return out
	}
	q := Quota{FiveHour: window("five_hour"), SevenDay: window("seven_day"), Source: planv1.UsageSource_USAGE_SOURCE_WORKER}
	if q.FiveHour == nil && q.SevenDay == nil {
		return nil
	}
	return &q
}
