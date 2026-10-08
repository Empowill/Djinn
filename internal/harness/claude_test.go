package harness

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

func collect(w Worker) []Event {
	var out []Event
	for ev := range w.Events() {
		out = append(out, ev)
	}
	return out
}

func kinds(events []Event) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = strings.TrimPrefix(ev.Kind.String(), "TASK_EVENT_KIND_")
	}
	return out
}

// TestParseClaude reads a hand-written stream in the format of claude -p --output-format stream-json --verbose:
// the format the Claude Agent SDK documents (system init, assistant and user messages with content blocks, a
// final result carrying the usage and total_cost_usd).
func TestParseClaude(t *testing.T) {
	var events []Event
	var results []*turnEnd
	for _, l := range fixtureLines(t, "testdata/claude/tool-call.jsonl") {
		evs, res := parseClaude(l)
		if evs[0].Raw != l {
			t.Errorf("first event of a line does not keep it raw")
		}
		events = append(events, evs...)
		if res != nil {
			results = append(results, res)
		}
	}
	want := []string{"STATUS", "TEXT", "TOOL_CALL", "TOOL_RESULT", "OTHER", "TOOL_CALL", "TOOL_RESULT", "TEXT", "USAGE"}
	if got := kinds(events); !slices.Equal(got, want) {
		t.Fatalf("kinds = %v\nwant %v", got, want)
	}
	const session = "0199c3a0-1b2c-7d3e-8f40-5a6b7c8d9e0f"
	if events[0].SessionID != session || !strings.Contains(events[0].Text, "claude-sonnet-4-5") {
		t.Errorf("init = %+v", events[0])
	}
	if events[2].Text != `Bash {"command":"ls","description":"List the files"}` {
		t.Errorf("tool call = %q", events[2].Text)
	}
	if events[3].Text != "README.md\ngo.mod" || events[6].Text != "     1\t# Project" {
		t.Errorf("tool results = %q, %q", events[3].Text, events[6].Text)
	}
	usage := events[8].Usage
	if usage.GetInputTokens() != 18 || usage.GetOutputTokens() != 95 || usage.GetCacheReadTokens() != 30600 ||
		usage.GetCacheWriteTokens() != 1500 || usage.GetCostUsd() != 0.0312 || events[8].SessionID != session {
		t.Errorf("usage = %v, session %q", usage, events[8].SessionID)
	}
	if len(results) != 1 || results[0].failure != "" {
		t.Errorf("results = %+v", results)
	}

	evs, res := parseClaude(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":null}`)
	if res == nil || res.failure != "error_during_execution" || evs[0].Kind != planv1.TaskEventKind_TASK_EVENT_KIND_ERROR {
		t.Errorf("error result: %+v, %+v", evs, res)
	}
	evs, _ = parseClaude(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","rateLimitType":"five_hour","resetsAt":1791418800}}`)
	if len(evs) != 1 || evs[0].Kind != planv1.TaskEventKind_TASK_EVENT_KIND_STATUS ||
		evs[0].Text != "rate limit allowed_warning (five_hour), resets at 2026-10-08T00:20:00Z" {
		t.Errorf("rate limit warning: %+v", evs)
	}
	for _, quiet := range []string{
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","rateLimitType":"five_hour"}}`,
		`{"type":"system","subtype":"thinking_tokens","estimated_tokens":50,"estimated_tokens_delta":50}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"","signature":"c2ln"}]}}`,
	} {
		if evs, res := parseClaude(quiet); evs != nil || res != nil {
			t.Errorf("%s: %+v, want nothing", quiet, evs)
		}
	}
	evs, _ = parseClaude("not json")
	if evs[0].Kind != planv1.TaskEventKind_TASK_EVENT_KIND_OTHER || evs[0].Text != "not json" {
		t.Errorf("plain line: %+v", evs)
	}
}

// TestClaudeArgs: in a project without permissions Djinn passes no permission setting, the project's settings
// decide; with the project's .agents permissions, Djinn passes them as settings; outside any project the worker
// only reads. A worker in a project reads AGENTS.md as well as CLAUDE.md.
func TestClaudeArgs(t *testing.T) {
	base := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--permission-prompts", "none"}
	const agentsMd = `"pluginConfigs":{"cc-plugin-agents-md@builtin":{"options":{"instructionFiles":"claude-md-and-agents-md"}}}`
	native := `{` + agentsMd + `}`
	tests := []struct {
		name string
		spec Spec
		want []string
	}{
		{"in a project", Spec{TaskID: "t1", Model: "opus", MaxBudgetUSD: 1.5},
			append(slices.Clone(base), "--settings", native, "--session-id", "t1", "--model", "opus", "--max-budget-usd", "1.5")},
		{"forked: the new session is named after the task", Spec{TaskID: "t1", Resume: "s0", Fork: true},
			append(slices.Clone(base), "--settings", native, "--resume", "s0", "--fork-session", "--session-id", "t1")},
		{"resumed: the session keeps its name", Spec{TaskID: "t1", Resume: "t1"},
			append(slices.Clone(base), "--settings", native, "--resume", "t1")},
		{"outside any project", Spec{TaskID: "t1", ReadOnly: true},
			append(slices.Clone(base), "--restricted", "--tools", "Read,Glob,Grep", "--permission-mode", "plan",
				"--strict-mcp-config", "--session-id", "t1")},
		{"read-only wins over permissions", Spec{TaskID: "t1", ReadOnly: true, Permissions: &djinnv1.Permissions{Edit: true}},
			append(slices.Clone(base), "--restricted", "--tools", "Read,Glob,Grep", "--permission-mode", "plan",
				"--strict-mcp-config", "--session-id", "t1")},
		{"listed permissions", Spec{TaskID: "t1", Permissions: &djinnv1.Permissions{
			Edit: true, Commands: []string{"go tool task test"}, DeniedCommands: []string{"git push"},
		}}, append(slices.Clone(base), "--permission-mode", "dontAsk", "--settings", `{"permissions":{`+
			`"allow":["Edit","Write","NotebookEdit","Bash(go tool task test *)","PowerShell(go tool task test *)"],`+
			`"defaultMode":"dontAsk",`+
			`"deny":["WebFetch","WebSearch","Bash(git push *)","PowerShell(git push *)"]},`+agentsMd+`}`, "--session-id", "t1")},
		{"auto mode", Spec{TaskID: "t1", Permissions: &djinnv1.Permissions{Edit: true, Network: true, Mode: djinnv1.Mode_MODE_AUTO}},
			append(slices.Clone(base), "--permission-mode", "auto", "--settings", `{"permissions":{`+
				`"allow":["Edit","Write","NotebookEdit","WebFetch","WebSearch"],"defaultMode":"auto","deny":[]},`+agentsMd+`}`,
				"--session-id", "t1")},
		{"auto mode without edit is listed", Spec{TaskID: "t1", Permissions: &djinnv1.Permissions{Mode: djinnv1.Mode_MODE_AUTO}},
			append(slices.Clone(base), "--permission-mode", "dontAsk", "--settings", `{"permissions":{`+
				`"allow":[],"defaultMode":"dontAsk","deny":["Edit","Write","NotebookEdit","WebFetch","WebSearch"]},`+agentsMd+`}`,
				"--session-id", "t1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Claude{}.args(tt.spec)
			if !slices.Equal(got, tt.want) {
				t.Errorf("args = %v\nwant %v", got, tt.want)
			}
			if !tt.spec.ReadOnly && tt.spec.Permissions == nil && slices.Contains(got, "--permission-mode") {
				t.Errorf("a worker without permissions gets a permission mode: %v", got)
			}
		})
	}
}

// TestPrefix: a worker runs under its prefix (djinn up --worker-cpu: a systemd scope), its own program found first.
func TestPrefix(t *testing.T) {
	env, args, _ := fake{provider: "claude", fixture: "success", end: "eof"}.env(t)
	record := filepath.Join(t.TempDir(), "prefix")
	env = append(env, "DJINN_FAKE_PREFIX="+record)
	prefix := []string{os.Args[0], "--scope", "--"}
	w, err := Claude{Command: os.Args[0]}.Start(t.Context(), Spec{Dir: t.TempDir(), Prompt: "x", Env: env, Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	events := collect(w)
	if res := w.Wait(); res.ExitCode != 0 || res.Err != nil || !slices.Contains(kinds(events), "TEXT") {
		t.Errorf("under the prefix: %+v, %v", res, kinds(events))
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	claudeArgs, err := os.ReadFile(args)
	if err != nil {
		t.Fatal("the agent did not run:", err)
	}
	if want := "--scope\n--\n" + os.Args[0] + "\n" + string(claudeArgs); string(got) != want {
		t.Errorf("the prefix ran %q, want %q", got, want)
	}
	if _, err := (Claude{Command: "djinn-no-such-agent"}).Start(t.Context(), Spec{Dir: t.TempDir(), Prompt: "x", Prefix: prefix}); err == nil ||
		!strings.Contains(err.Error(), "djinn-no-such-agent not found in PATH") {
		t.Errorf("a missing agent under a prefix: %v", err)
	}
}

func TestClaudeWorkerStops(t *testing.T) {
	env, _, _ := fake{provider: "claude", fixture: "process-dies", end: "wait"}.env(t)
	w, err := Claude{Command: os.Args[0], Grace: 5 * time.Second}.Start(t.Context(), Spec{Dir: t.TempDir(), Prompt: "x", Env: env})
	if err != nil {
		t.Fatal(err)
	}
	<-w.Events() // started
	start := time.Now()
	w.Stop()
	events := collect(w)
	res := w.Wait()
	if res.ExitCode == 0 || time.Since(start) > 3*time.Second {
		t.Errorf("stopped in %v with %+v; want a prompt end on SIGTERM", time.Since(start), res)
	}
	if slices.Contains(kinds(events), "ERROR") || res.Err != nil {
		t.Errorf("a stopped worker reports an error: %v, %v", kinds(events), res.Err)
	}
}

func TestClaudeWorkerKilledAfterGrace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows kills at once: no grace to test")
	}
	grace := 300 * time.Millisecond
	env, _, _ := fake{provider: "claude", fixture: "process-dies", end: "hang"}.env(t)
	w, err := Claude{Command: os.Args[0], Grace: grace}.Start(t.Context(), Spec{Dir: t.TempDir(), Prompt: "x", Env: env})
	if err != nil {
		t.Fatal(err)
	}
	<-w.Events()
	start := time.Now()
	w.Stop()
	collect(w)
	res := w.Wait()
	if elapsed := time.Since(start); res.ExitCode != -1 || elapsed < grace || elapsed > 5*time.Second {
		t.Errorf("ended after %v with %+v; want killed after %v", elapsed, res, grace)
	}
}
