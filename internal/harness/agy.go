package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// Antigravity runs Google's Antigravity CLI headless: `agy --input-format stream-json --output-format stream-json`,
// one NDJSON message per turn on its input, one event per line on its output (docs/providers.md). Like Claude, the
// process stays open between turns, and ends once every message has its result.
//
// Djinn passes its environment on and sets nothing about signing in: agy signs in by itself (decision Q20). Djinn
// never reads agy's configuration, its tokens, nor the Application Default Credentials file.
type Antigravity struct {
	// Command is the agy program; "agy", found on the PATH, by default.
	Command string
	// Grace is how long a worker asked to stop has before it is killed; Grace by default.
	Grace time.Duration
}

// args are the arguments of agy for spec. The prompt goes on the input, not on the command line: -p takes the
// prompt as its value, and stream-json input replaces it.
//
// In a project, agy's own settings decide; headless, agy cannot ask for a permission, so a tool that needs one
// is refused (soft-denied). Djinn never passes --dangerously-skip-permissions.
//
// A read-only task is refused: agy's plan mode asks before writing but is not read-only, and what --sandbox blocks is
// not documented. agy runs outside a project once a real capture proves that nothing is written or run. For the
// same reason, permissions without edit are refused.
//
// With permissions, edit becomes --mode accept-edits, and network off --sandbox, which only restricts more. agy
// takes no list of commands at launch, and its headless runs do not apply its allow rules: a command that needs
// an approval is soft-denied, listed or not. agy 1.3.0 has no auto mode on its command line (--mode takes
// accept-edits or plan): AUTO runs as accept-edits.
func (a Antigravity) args(spec Spec) ([]string, error) {
	args := []string{"--input-format", "stream-json", "--output-format", "stream-json"}
	if spec.ReadOnly {
		return nil, fmt.Errorf("agy cannot run a read-only task yet: %w; use claude or codex", ErrReadOnly)
	}
	if p := spec.Permissions; p != nil {
		if !p.GetEdit() {
			return nil, fmt.Errorf("agy cannot run without editing yet (the project's %s says edit: false): %w; use claude or codex",
				filepath.ToSlash(PermissionsFile), ErrReadOnly)
		}
		args = append(args, "--mode", "accept-edits")
		if !p.GetNetwork() {
			args = append(args, "--sandbox")
		}
	}
	if spec.SkillsDir != "" {
		// A workspace folder's .agents/skills is a customization root (the documentation in the agy 1.3.0 binary).
		args = append(args, "--add-dir", spec.SkillsDir)
	}
	if spec.Resume != "" {
		if spec.Fork {
			return nil, errors.New("agy cannot fork a conversation: resume it, or start a new one")
		}
		args = append(args, "--conversation", spec.Resume)
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	// agy has no spending cap: MaxBudgetUSD is not enforced.
	return args, nil
}

func (a Antigravity) Start(ctx context.Context, spec Spec) (Worker, error) {
	command, grace := a.Command, a.Grace
	if command == "" {
		command = "agy"
	}
	if grace == 0 {
		grace = Grace
	}
	args, err := a.args(spec)
	if err != nil {
		return nil, err
	}
	return startStream(ctx, spec, command, args, grace, streamAgent{name: "agy", parser: &agyParser{}, encode: agyMessageLine})
}

// agyMessageLine is a user message as agy reads it in stream-json.
func agyMessageLine(text string) ([]byte, error) {
	return json.Marshal(map[string]any{"event": "user", "message": map[string]any{"content": text}})
}

// agyLine is a line of agy's stream-json output; only the fields Djinn reads.
type agyLine struct {
	Event          string `json:"event"`
	ConversationID string `json:"conversation_id"`
	Init           *struct {
		Model string `json:"model"`
	} `json:"init"`
	StepUpdate *agyStep   `json:"step_update"`
	Result     *agyResult `json:"result"`
}

type agyStep struct {
	ConversationID string `json:"conversation_id"`
	StepIndex      int    `json:"step_index"`
	State          string `json:"state"` // ACTIVE, DONE
	StepType       string `json:"step_type"`
	ToolName       string `json:"tool_name"`
	TextDelta      string `json:"text_delta"`
	ToolInfo       *struct {
		Name       string          `json:"name"`
		Parameters json.RawMessage `json:"parameters"`
		Output     *string         `json:"output"`
	} `json:"tool_info"`
}

type agyResult struct {
	ConversationID string    `json:"conversation_id"`
	Status         string    `json:"status"`
	Error          string    `json:"error"`
	NumTurns       int       `json:"num_turns"`
	Usage          *agyUsage `json:"usage"`
}

type agyUsage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ThinkingTokens  int64 `json:"thinking_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
}

// agyParser reads agy's stream-json. The agent's text comes in deltas: it is gathered per step, and said once
// the step is done.
type agyParser struct {
	text    map[int]*strings.Builder // text of the agent_response steps under way, by step index
	order   []int                    // their indexes, in the order they started
	toolsIn map[int]bool             // tool steps whose call is said already
}

func (p *agyParser) stdout(raw string) ([]Event, *turnEnd) {
	var m agyLine
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_OTHER, Text: raw, Raw: raw}}, nil
	}
	var events lineEvents
	var end *turnEnd
	what, silent := m.Event, false
	switch {
	case m.Event == "init":
		text := "session " + m.ConversationID
		if m.Init != nil && m.Init.Model != "" {
			text += ", model " + m.Init.Model
		}
		events.add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, text).SessionID = m.ConversationID
	case m.Event == "step_update" && m.StepUpdate != nil:
		s := m.StepUpdate
		what = "step " + s.StepType + " " + s.State
		silent = p.step(&events, s)
	case m.Event == "result" && m.Result != nil:
		end = p.result(&events, m.Result)
	}
	if len(events) == 0 && silent {
		return nil, end // A piece of a whole said later, or the echo of Djinn's prompt.
	}
	return events.done(raw, what), end
}

// step reads a step update, and says whether Djinn knows its kind: a known step may say nothing yet.
func (p *agyParser) step(events *lineEvents, s *agyStep) bool {
	switch s.StepType {
	case "user_input", "checkpoint":
		// The prompt Djinn sent, and agy's own bookkeeping: nothing to say.
	case "agent_response":
		b := p.text[s.StepIndex]
		if b == nil {
			if p.text == nil {
				p.text = map[int]*strings.Builder{}
			}
			b = &strings.Builder{}
			p.text[s.StepIndex] = b
			p.order = append(p.order, s.StepIndex)
		}
		b.WriteString(s.TextDelta)
		if s.State == "DONE" {
			if text := b.String(); strings.TrimSpace(text) != "" {
				events.add(planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, strings.TrimRight(text, "\n"))
			}
			p.forget(s.StepIndex)
		}
	case "tool":
		name, params, output := s.ToolName, json.RawMessage(nil), (*string)(nil)
		if ti := s.ToolInfo; ti != nil {
			if ti.Name != "" {
				name = ti.Name
			}
			params, output = ti.Parameters, ti.Output
		}
		if !p.toolsIn[s.StepIndex] {
			if p.toolsIn == nil {
				p.toolsIn = map[int]bool{}
			}
			p.toolsIn[s.StepIndex] = true
			events.add(planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, strings.TrimSpace(name+" "+compactOrEmpty(params)))
		}
		if s.State == "DONE" {
			text := ""
			if output != nil {
				text = strings.TrimRight(*output, "\r\n")
			}
			events.add(planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT, text)
			delete(p.toolsIn, s.StepIndex)
		}
	default:
		return false
	}
	return true
}

func (p *agyParser) result(events *lineEvents, r *agyResult) *turnEnd {
	end := &turnEnd{}
	*events = append(*events, p.flush()...)
	// SUCCESS, or ERROR, CANCELED, INTERRUPTED, INVALID, WAITING (--print-timeout reached), RUNNING.
	if r.Status != "SUCCESS" {
		end.failure = r.Error
		if end.failure == "" {
			end.failure = "agy ended its turn with status " + r.Status
		}
		events.add(planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, end.failure)
	}
	// The usage of a result is what the whole conversation spent so far. agy gives no cost: tokens only. Its
	// thinking tokens are counted as written ones.
	usage := &planv1.Usage{}
	if u := r.Usage; u != nil {
		usage.InputTokens, usage.OutputTokens = u.InputTokens, u.OutputTokens+u.ThinkingTokens
		usage.CacheReadTokens = u.CacheReadTokens
	}
	ev := events.add(planv1.TaskEventKind_TASK_EVENT_KIND_USAGE, fmt.Sprintf("%d turns, %d tokens in, %d out",
		r.NumTurns, usage.GetInputTokens(), usage.GetOutputTokens()))
	ev.Usage, ev.SessionID = usage, r.ConversationID
	return end
}

func (p *agyParser) forget(step int) {
	delete(p.text, step)
	for i, s := range p.order {
		if s == step {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
}

// flush says the text of a response cut short by the end of the process.
func (p *agyParser) flush() []Event {
	var events []Event
	for _, step := range p.order {
		if text := p.text[step].String(); strings.TrimSpace(text) != "" {
			events = append(events, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, Text: strings.TrimRight(text, "\n") + " [cut]"})
		}
	}
	p.text, p.order = nil, nil
	return events
}

// agyErrorPrefix starts the line agy writes on its error output when a turn ends on a model or agent failure.
const agyErrorPrefix = "AGY_ERROR:"

// agyDenied is in the line agy writes on its error output when it refused tools it could not ask for.
const agyDenied = "headless mode cannot prompt for"

func (p *agyParser) stderr(raw string) []Event {
	switch {
	case strings.HasPrefix(raw, agyErrorPrefix):
		text := strings.TrimSpace(strings.TrimPrefix(raw, agyErrorPrefix))
		var e struct {
			ShortError string `json:"short_error"`
			Retryable  *bool  `json:"retryable"`
			HTTPStatus int    `json:"http_status"`
		}
		if json.Unmarshal([]byte(text), &e) == nil && e.ShortError != "" {
			text = e.ShortError
			if e.HTTPStatus != 0 {
				text += " (HTTP " + strconv.Itoa(e.HTTPStatus) + ")"
			}
			if e.Retryable != nil && *e.Retryable {
				text += ", retryable"
			}
		}
		return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, Text: text, Raw: raw}}
	case strings.Contains(raw, agyDenied):
		return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "permission denied: " + raw, Raw: raw}}
	}
	return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_LOG, Text: raw, Raw: raw}}
}

// compactOrEmpty writes JSON on one line; nothing for no JSON.
func compactOrEmpty(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return compact(raw)
}
