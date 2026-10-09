package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// Claude runs Claude Code: `claude -p` reading and writing stream-json, one JSON message per line. The process
// stays open between turns, so Send can give it another message; it ends once it has answered every message.
type Claude struct {
	// Command is the claude program; "claude", found on the PATH, by default.
	Command string
	// Grace is how long a worker asked to stop has before it is killed; Grace by default.
	Grace time.Duration
	// Extra are arguments passed after Djinn's own: the workers' bench tries --bare and a system prompt file.
	Extra []string
	// Settle is how long claude may say nothing of a turn after a result before the messages sent during that turn
	// count as answered by it; ClaudeSettle by default.
	Settle time.Duration
}

// ClaudeSettle is how long a claude worker that took a message into its turn stays open after its result, in case
// the message opens a turn of its own instead. claude gives one result for a turn and the messages it took in on
// the way (real runs, claude 2.1.294, 2026-10-08): nothing in its output says which.
const ClaudeSettle = time.Minute

// claudeReadTools are the only tools of a read-only worker: reading files, finding them, searching them.
const claudeReadTools = "Read,Glob,Grep"

// claudeAgentsMd has Claude read the project's AGENTS.md as well as its CLAUDE.md, the import of one by the other
// read once. The built-in plugin that reads AGENTS.md takes this option from a --settings file, not from the
// project's settings (claude 2.1.285 and later).
var claudeAgentsMd = map[string]any{
	"cc-plugin-agents-md@builtin": map[string]any{"options": map[string]any{"instructionFiles": "claude-md-and-agents-md"}},
}

// args are the arguments of claude for spec. In a project, the project's and the user's settings decide what the
// worker may do, with the project's .agents permissions on top when spec has them; nobody answers a permission
// prompt, so what they do not allow is refused. A read-only worker gets the reading tools only, in plan mode,
// without the settings files nor MCP servers.
func (c Claude) args(spec Spec) []string {
	args := []string{
		"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--permission-prompts", "none",
	}
	if spec.ReadOnly {
		args = append(args, "--restricted", "--tools", claudeReadTools, "--permission-mode", "plan", "--strict-mcp-config")
	} else {
		settings := map[string]any{"pluginConfigs": claudeAgentsMd}
		if p := spec.Permissions; p != nil {
			perms := claudePermissions(p)
			settings["permissions"] = perms
			args = append(args, "--permission-mode", perms["defaultMode"].(string))
		}
		if allow, deny := claudeSkillRules(spec); len(allow)+len(deny) > 0 {
			perms, _ := settings["permissions"].(map[string]any)
			if perms == nil {
				perms = map[string]any{"allow": []string{}, "deny": []string{}}
				settings["permissions"] = perms
			}
			perms["allow"] = append(perms["allow"].([]string), allow...)
			perms["deny"] = append(perms["deny"].([]string), deny...)
		}
		b, _ := json.Marshal(settings) // Maps of strings and slices of strings: it cannot fail.
		args = append(args, "--settings", string(b))
	}
	if spec.SkillsDir != "" {
		// Claude loads the skills of .claude/skills in a folder given with --add-dir (code.claude.com/docs/en/skills).
		args = append(args, "--add-dir", spec.SkillsDir)
	}
	if spec.Resume != "" {
		args = append(args, "--resume", spec.Resume)
		if spec.Fork {
			args = append(args, "--fork-session")
		}
	}
	if spec.TaskID != "" && (spec.Resume == "" || spec.Fork) {
		// The session is named after the task, so it is known before the agent says it; a fork names its new
		// session so (claude takes --session-id with --resume only when it forks).
		args = append(args, "--session-id", spec.TaskID)
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(spec.MaxBudgetUSD, 'f', -1, 64))
	}
	return append(args, c.Extra...)
}

// claudePermissions are the permissions of Claude's settings for p: allow and deny rules, and the mode. Edit
// rules cover every tool that edits files; a command prefix p becomes Bash(p *), which matches p alone and p
// followed by arguments, not a longer word. LISTED is Claude's dontAsk mode: what no rule allows is refused.
// Network off denies Claude's web tools; a command reaching the network is not stopped by a rule.
func claudePermissions(p *djinnv1.Permissions) map[string]any {
	allow, deny := []string{}, []string{}
	add := func(ok bool, rules ...string) {
		if ok {
			allow = append(allow, rules...)
		} else {
			deny = append(deny, rules...)
		}
	}
	add(p.GetEdit(), "Edit", "Write", "NotebookEdit")
	add(p.GetNetwork(), "WebFetch", "WebSearch")
	for _, c := range p.GetCommands() {
		add(true, "Bash("+c+" *)", "PowerShell("+c+" *)")
	}
	for _, c := range p.GetDeniedCommands() {
		add(false, "Bash("+c+" *)", "PowerShell("+c+" *)")
	}
	mode := "dontAsk"
	if effectiveMode(p) == djinnv1.Mode_MODE_AUTO {
		mode = "auto"
	}
	return map[string]any{"allow": allow, "deny": deny, "defaultMode": mode}
}

// claudeSkillRules are the permission rules for the skills linked in spec.SkillsDir: reading each skill's folder
// in its source project, which a link from the added folder leads to (an allow rule must match both the link and
// its target); never editing either, whatever the mode (a deny rule matches either one).
func claudeSkillRules(spec Spec) (allow, deny []string) {
	if spec.SkillsDir == "" {
		return nil, nil
	}
	deny = append(deny, "Edit("+claudeAbsolute(spec.SkillsDir)+"/**)")
	for _, s := range spec.Skills {
		allow = append(allow, "Read("+claudeAbsolute(s.Dir)+"/**)")
		deny = append(deny, "Edit("+claudeAbsolute(s.Dir)+"/**)")
	}
	return allow, deny
}

// claudeAbsolute is an absolute path as Claude's permission rules write it: // then the path from the root, in
// POSIX form; on Windows, C:\Users becomes //c/Users.
func claudeAbsolute(path string) string {
	s := filepath.ToSlash(path)
	if vol := filepath.VolumeName(path); len(vol) == 2 && vol[1] == ':' {
		s = "/" + strings.ToLower(vol[:1]) + s[2:]
	}
	return "/" + s
}

func (c Claude) Start(ctx context.Context, spec Spec) (Worker, error) {
	command, grace := c.Command, c.Grace
	if command == "" {
		command = "claude"
	}
	if grace == 0 {
		grace = Grace
	}
	return startStream(ctx, spec, command, c.args(spec), grace, c.agent())
}

// Warm starts claude without a message: it loads, then waits on its input. The first Send is its first message.
func (c Claude) Warm(ctx context.Context, spec Spec) (Worker, error) {
	command, grace := c.Command, c.Grace
	if command == "" {
		command = "claude"
	}
	if grace == 0 {
		grace = Grace
	}
	return startStreamIdle(ctx, spec, command, c.args(spec), grace, c.agent())
}

func (c Claude) agent() streamAgent {
	settle := c.Settle
	if settle == 0 {
		settle = ClaudeSettle
	}
	return streamAgent{name: "claude", parser: claudeParser{}, encode: claudeMessageLine, settle: settle}
}

// claudeMessageLine is a user message as claude reads it in stream-json.
func claudeMessageLine(text string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}},
	})
}

// claudeParser reads claude's stream-json; it holds nothing between lines.
type claudeParser struct{}

func (claudeParser) stdout(raw string) ([]Event, *turnEnd) { return parseClaude(raw) }

// thinking tells a thinking_tokens progress line, or a message of thinking blocks: claude at work in a turn, though
// such a line gives no event, or an OTHER one.
func (claudeParser) thinking(raw string) bool {
	if !strings.Contains(raw, "thinking") {
		return false
	}
	var m claudeMessage
	if json.Unmarshal([]byte(raw), &m) != nil {
		return false
	}
	switch m.Type {
	case "system":
		return m.Subtype == "thinking_tokens"
	case "assistant":
		return slices.ContainsFunc(blocks(m.Message), func(b claudeBlock) bool {
			return b.Type == "thinking" || b.Type == "redacted_thinking"
		})
	}
	return false
}

func (claudeParser) stderr(raw string) []Event {
	return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_LOG, Text: raw, Raw: raw}}
}

func (claudeParser) flush() []Event { return nil }

// claudeMessage is a line of claude's stream-json output; only the fields Djinn reads.
type claudeMessage struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	Message   *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	// On a result.
	Result            *string  `json:"result"`
	IsError           bool     `json:"is_error"`
	Errors            []string `json:"errors"`
	NumTurns          int      `json:"num_turns"`
	TotalCostUSD      float64  `json:"total_cost_usd"`
	PermissionDenials []struct {
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
	} `json:"permission_denials"`
	Usage *struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	// ModelUsage is the usage per model. Seen on a real run (claude 2.1.293): the root usage may be all zeros
	// while the figures are here, so it is read first.
	ModelUsage map[string]struct {
		InputTokens              int64 `json:"inputTokens"`
		OutputTokens             int64 `json:"outputTokens"`
		CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
	} `json:"modelUsage"`
	// On a rate_limit_event.
	RateLimitInfo *struct {
		Status        string `json:"status"`
		RateLimitType string `json:"rateLimitType"`
		ResetsAt      int64  `json:"resetsAt"`
	} `json:"rate_limit_info"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	ToolUseID string          `json:"tool_use_id"`
}

// parseClaude turns one line of claude's output into events, and says what the turn's result was when the line
// is one. Nothing is lost: a line Djinn does not understand becomes an OTHER event that keeps it raw.
func parseClaude(raw string) ([]Event, *turnEnd) {
	var m claudeMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_OTHER, Text: raw, Raw: raw}}, nil
	}
	var events lineEvents
	add := events.add
	var res *turnEnd
	switch m.Type {
	case "system":
		if m.Subtype == "init" {
			text := "session " + m.SessionID
			if m.Model != "" {
				text += ", model " + m.Model
			}
			add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, text).SessionID = m.SessionID
		}
		if m.Subtype == "thinking_tokens" {
			return nil, nil // An estimate of the thinking under way; the result counts it.
		}
	case "rate_limit_event":
		info := m.RateLimitInfo
		if info == nil || info.Status == "allowed" {
			return nil, nil // Said on every turn while under the limit.
		}
		text := "rate limit " + info.Status
		if info.RateLimitType != "" {
			text += " (" + info.RateLimitType + ")"
		}
		if info.ResetsAt > 0 {
			text += ", resets at " + time.Unix(info.ResetsAt, 0).UTC().Format(time.RFC3339)
		}
		add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, text)
	case "assistant", "user":
		bs := blocks(m.Message)
		if len(bs) > 0 && !slices.ContainsFunc(bs, func(b claudeBlock) bool {
			return b.Type != "thinking" && b.Type != "redacted_thinking" || strings.TrimSpace(b.Thinking) != ""
		}) {
			return nil, nil // Thinking whose text is withheld: nothing to read.
		}
		for _, b := range bs {
			switch b.Type {
			case "thinking":
				if strings.TrimSpace(b.Thinking) != "" {
					add(planv1.TaskEventKind_TASK_EVENT_KIND_OTHER, "thinking: "+b.Thinking)
				}
			case "redacted_thinking":
			case "text":
				if strings.TrimSpace(b.Text) != "" {
					add(planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, b.Text)
				}
			case "tool_use":
				add(planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, strings.TrimSpace(b.Name+" "+compact(b.Input)))
			case "tool_result":
				text := contentText(b.Content)
				if b.IsError {
					text = "error: " + text
				}
				add(planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT, text)
			}
		}
	case "result":
		res = &turnEnd{}
		// An error subtype (error_max_turns, error_during_execution…) fails the turn even when is_error is false.
		if m.IsError || m.Subtype != "" && m.Subtype != "success" {
			res.failure = strings.Join(m.Errors, "; ")
			if res.failure == "" && m.Result != nil {
				res.failure = *m.Result
			}
			if res.failure == "" {
				res.failure = m.Subtype
			}
			add(planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, res.failure)
		}
		for _, d := range m.PermissionDenials {
			// Refused because the worker's permission mode does not allow it: the agent went on without it.
			add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, strings.TrimSpace("permission denied: "+d.ToolName+" "+compact(d.ToolInput)))
		}
		usage := &planv1.Usage{CostUsd: m.TotalCostUSD}
		for _, u := range m.ModelUsage {
			usage.InputTokens += u.InputTokens
			usage.OutputTokens += u.OutputTokens
			usage.CacheReadTokens += u.CacheReadInputTokens
			usage.CacheWriteTokens += u.CacheCreationInputTokens
		}
		if u := m.Usage; len(m.ModelUsage) == 0 && u != nil {
			usage.InputTokens, usage.OutputTokens = u.InputTokens, u.OutputTokens
			usage.CacheReadTokens, usage.CacheWriteTokens = u.CacheReadInputTokens, u.CacheCreationInputTokens
		}
		ev := add(planv1.TaskEventKind_TASK_EVENT_KIND_USAGE, fmt.Sprintf("%d turns, %d tokens in, %d out, $%.4f",
			m.NumTurns, usage.GetInputTokens(), usage.GetOutputTokens(), usage.GetCostUsd()))
		ev.Usage, ev.SessionID = usage, m.SessionID
	}
	what := m.Type
	if m.Subtype != "" {
		what += " " + m.Subtype
	}
	return events.done(raw, what), res
}

// blocks are the content blocks of a message; a plain string is one text block.
func blocks(msg *struct {
	Content json.RawMessage `json:"content"`
}) []claudeBlock {
	if msg == nil || len(msg.Content) == 0 {
		return nil
	}
	var out []claudeBlock
	if json.Unmarshal(msg.Content, &out) == nil {
		return out
	}
	var s string
	if json.Unmarshal(msg.Content, &s) == nil {
		return []claudeBlock{{Type: "text", Text: s}}
	}
	return nil
}

// contentText is the text of a tool result: a string, or the text of its blocks.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []claudeBlock
	if json.Unmarshal(raw, &parts) == nil {
		var texts []string
		for _, p := range parts {
			if p.Type == "text" {
				texts = append(texts, p.Text)
			} else {
				texts = append(texts, "["+p.Type+"]")
			}
		}
		return strings.Join(texts, "\n")
	}
	return compact(raw)
}

// compact writes JSON on one line.
func compact(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}
