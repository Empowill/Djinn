package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// Antigravity runs Google's Antigravity CLI headless: `agy --input-format stream-json --output-format stream-json`,
// one NDJSON message per turn on its input, one event per line on its output (docs/providers.md). Like Claude, the
// process stays open between turns, and ends once every message has its result.
//
// Djinn passes its environment on and sets nothing about signing in: agy signs in by itself (decision Q20). Djinn
// never reads agy's configuration, its tokens, nor the Application Default Credentials file. It writes one thing of
// agy's: a project of its own per worktree, for a worker with permissions (agyProject).
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
// With permissions, edit becomes --mode accept-edits, and network off --sandbox, which only restricts more. The rest
// goes in an agy project of Djinn's own, given with --project (agyProject): agy takes no list of commands nor of
// writable folders at launch. agy 1.3.3 has no auto mode on its command line (--mode takes accept-edits or plan):
// AUTO runs as accept-edits.
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
		args = append(args, "--project", agyProjectID(spec.Dir))
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
	if spec.Permissions != nil {
		if err := writeAgyProject(spec); err != nil {
			return nil, err
		}
	}
	return startStream(ctx, spec, command, args, grace, streamAgent{name: "agy", parser: &agyParser{}, encode: agyMessageLine})
}

// agyMessageLine is a user message as agy reads it in stream-json.
func agyMessageLine(text string) ([]byte, error) {
	return json.Marshal(map[string]any{"event": "user", "message": map[string]any{"content": text}})
}

// agyProjectsDir is the folder of agy's projects, ~/.gemini/config/projects; a test points it elsewhere.
var agyProjectsDir = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini", "config", "projects"), nil
}

// agyProjectID is the id of the agy project of a worker in dir: the same for every worker of the folder, as agy
// resumes a conversation in the project it began in.
func agyProjectID(dir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(dir)))
	return "djinn-" + hex.EncodeToString(sum[:8])
}

// agyProjectFile is an agy project, as agy reads it in its projects folder (the descriptor of its project_pb, agy
// 1.3.3). Its other fields, settings among them, are left out: the command line does not apply a project's settings.
type agyProjectFile struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	PermissionGrants struct {
		PermissionGrants agyGrants `json:"permissionGrants"`
	} `json:"permissionGrants"`
}

type agyGrants struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny,omitempty"`
}

// agyCommitCommands are what an agy worker in AUTO may run besides what the project lists, in a Git worktree: Djinn
// asks it to commit its work, and agy has no auto mode to decide. The project's denied_commands still win.
var agyCommitCommands = []string{"git status", "git diff", "git log", "git show", "git add", "git commit"}

// agyProject is the agy project of a worker with permissions: what its permissions give, as agy's grants (real
// runs, agy 1.3.3, 2026-10-10, docs/providers.md).
//
// agy's sandbox (--sandbox) shows the workspace read-only to commands, and every .git folder read-only, whatever
// the workspace's trust or --add-dir; it hides the rest of the home folder, the repository of a linked worktree
// among them, so that git fails and agy asks to run it unsandboxed, which a headless run cannot. A write_file grant
// mounts its folder writable in the sandbox: edit grants the worker's folder and Git's folders for it (agyGitDirs).
// A command(<p>) grant lets a command starting with p run without approval; a deny grant wins over any allow one,
// the user's settings' included, and its command fails without ending the turn.
func agyProject(spec Spec) agyProjectFile {
	p := spec.Permissions
	f := agyProjectFile{ID: agyProjectID(spec.Dir), Name: "Djinn " + filepath.Base(spec.Dir)}
	g := agyGrants{Allow: []string{}}
	gitDirs := agyGitDirs(spec.Dir)
	if p.GetEdit() {
		for _, d := range append([]string{spec.Dir}, gitDirs...) {
			g.Allow = append(g.Allow, "write_file("+d+")")
		}
	}
	commands := slices.Clone(p.GetCommands())
	if effectiveMode(p) == djinnv1.Mode_MODE_AUTO && len(gitDirs) > 0 {
		for _, c := range agyCommitCommands {
			if !slices.Contains(commands, c) {
				commands = append(commands, c)
			}
		}
	}
	for _, c := range commands {
		g.Allow = append(g.Allow, "command("+c+")")
	}
	for _, c := range p.GetDeniedCommands() {
		g.Deny = append(g.Deny, "command("+c+")")
	}
	f.PermissionGrants.PermissionGrants = g
	return f
}

// agyGitDirs are Git's folders for the work tree holding dir: its .git folder; in a linked worktree, the folder
// its .git file names and the repository's common folder (the commondir file there), when not inside it. None
// outside Git.
func agyGitDirs(dir string) []string {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		dotGit := filepath.Join(d, ".git")
		info, err := os.Stat(dotGit)
		switch {
		case err == nil && info.IsDir():
			return []string{dotGit}
		case err == nil:
			b, err := os.ReadFile(dotGit)
			gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
			if err != nil || !ok {
				return nil
			}
			gitDir = absFrom(d, strings.TrimSpace(gitDir))
			b, err = os.ReadFile(filepath.Join(gitDir, "commondir"))
			if err != nil {
				return []string{gitDir}
			}
			common := absFrom(gitDir, strings.TrimSpace(string(b)))
			if rel, err := filepath.Rel(common, gitDir); err == nil && filepath.IsLocal(rel) {
				return []string{common}
			}
			return []string{common, gitDir}
		}
		if filepath.Dir(d) == d {
			return nil
		}
	}
}

// absFrom is path, absolute, relative to dir when it is not.
func absFrom(dir, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path)
}

// writeAgyProject writes the agy project of spec's worker in agy's projects folder, over the one an earlier worker
// of its folder wrote. Djinn writes nothing else of agy's, and reads nothing of it.
func writeAgyProject(spec Spec) error {
	dir, err := agyProjectsDir()
	if err != nil {
		return fmt.Errorf("agy's projects folder: %w", err)
	}
	f := agyProject(spec)
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("agy's projects folder: %w", err)
	}
	path := filepath.Join(dir, f.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("write agy's project: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write agy's project: %w", err)
	}
	return nil
}

// forgetAgyProject removes the agy project of the workers in dir, when there is one: its folder is gone.
func forgetAgyProject(dir string) {
	if projects, err := agyProjectsDir(); err == nil {
		_ = os.Remove(filepath.Join(projects, agyProjectID(dir)+".json"))
	}
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
	// DeniedActions are the permissions agy refused in the turn, headless mode unable to ask for them:
	// [{"action":"command","display_name":"RunCommand"}] (real runs, agy 1.3.0).
	DeniedActions []struct {
		Action      string `json:"action"`
		DisplayName string `json:"display_name"`
	} `json:"denied_actions"`
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

	// The last tool step started, and whether it gave an output: a denial ends the turn, and the denied step
	// ends without output, or does not end (real runs, agy 1.3.0).
	lastStep     int
	lastCall     string
	lastAnswered bool
	denied       string // the permission named by the denial notice on the error output, when one came
	failed       bool   // a result said the turn failed: the notice adds nothing
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
			p.lastStep, p.lastCall, p.lastAnswered = s.StepIndex, agyCall(name, params), false
			events.add(planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, strings.TrimSpace(name+" "+compactOrEmpty(params)))
		}
		if s.State == "DONE" {
			text := ""
			if output != nil {
				text = strings.TrimRight(*output, "\r\n")
				p.lastAnswered = p.lastAnswered || s.StepIndex == p.lastStep
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
	*events = append(*events, p.cut()...)
	// SUCCESS, or ERROR, CANCELED, INTERRUPTED, INVALID, WAITING (--print-timeout reached), RUNNING.
	switch {
	case r.Status != "SUCCESS":
		end.failure = r.Error
		if end.failure == "" {
			end.failure = "agy ended its turn with status " + r.Status
		}
	case len(r.DeniedActions) > 0:
		// A turn with a denial says SUCCESS, but the denial ended it: the work was not done.
		actions := make([]string, len(r.DeniedActions))
		for i, a := range r.DeniedActions {
			actions[i] = a.Action
		}
		end.failure = p.deniedReason(actions)
	}
	if end.failure != "" {
		p.failed = true
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

// flush says the text of a response cut short by the end of the process, and why agy stopped when its error
// output alone said it denied a permission: that notice may come after the result, the two outputs being read
// apart.
func (p *agyParser) flush() []Event {
	events := p.cut()
	if p.denied != "" && !p.failed {
		p.failed = true
		events = append(events, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, Text: p.deniedReason([]string{p.denied})})
	}
	return events
}

// deniedReason says, for a person, why agy stopped: it denied the actions (permissions) it could not ask for.
func (p *agyParser) deniedReason(actions []string) string {
	what := "it cannot run commands headless"
	if !slices.Contains(actions, "command") {
		what = `headless, it cannot ask for the "` + strings.Join(actions, `", "`) + `" permission`
	}
	if p.lastCall != "" && !p.lastAnswered {
		what += " (" + p.lastCall + ")"
	}
	return "agy stopped: " + what + ". Run this task with claude or codex."
}

// cut says the text of a response cut short.
func (p *agyParser) cut() []Event {
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

// agyDenied is in the line agy writes on its error output when it refused tools it could not ask for:
// `… a tool required the "command" permission that headless mode cannot prompt for, so it was auto-denied. …`
// (real runs, agy 1.3.0).
const agyDenied = "headless mode cannot prompt for"

// agyDeniedPermission finds the permission in the denial notice.
var agyDeniedPermission = regexp.MustCompile(`required the "([^"]+)" permission`)

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
		p.denied = "command"
		if m := agyDeniedPermission.FindStringSubmatch(raw); m != nil {
			p.denied = m[1]
		}
		return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: permissionDenied + raw, Raw: raw}}
	}
	return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_LOG, Text: raw, Raw: raw}}
}

// agyCall is what a tool step calls, for a person: the command line of run_command, else the tool and its
// parameters.
func agyCall(name string, params json.RawMessage) string {
	if name == "run_command" {
		var p struct {
			CommandLine string `json:"CommandLine"`
		}
		if json.Unmarshal(params, &p) == nil && p.CommandLine != "" {
			return p.CommandLine
		}
	}
	return strings.TrimSpace(name + " " + compactOrEmpty(params))
}

// compactOrEmpty writes JSON on one line; nothing for no JSON.
func compactOrEmpty(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return compact(raw)
}
