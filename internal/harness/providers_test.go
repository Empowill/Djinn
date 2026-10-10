package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// catalogCase is a row of the case catalog of docs/providers.md: a fixture replayed by the fake provider, and what
// Djinn must make of it.
type catalogCase struct {
	provider string // claude, antigravity, codex: the folder of testdata
	fixture  string
	spec     Spec     // Dir, Prompt and Env are filled in
	send     []string // messages sent once the worker has started
	end      string   // what the fake does once the fixture is played; eof by default
	want     []string // kinds of the events from the output, in order; those from the error output aside
	exit     int
	err      string // in Result.Err; "" for none
	check    func(t *testing.T, events []Event, args, input string)
}

// event finds the first event of a kind.
func event(t *testing.T, events []Event, kind string) Event {
	t.Helper()
	for _, ev := range events {
		if strings.TrimPrefix(ev.Kind.String(), "TASK_EVENT_KIND_") == kind {
			return ev
		}
	}
	t.Fatalf("no %s event in %v", kind, kinds(events))
	return Event{}
}

func texts(events []Event, kind string) []string {
	var out []string
	for _, ev := range events {
		if strings.TrimPrefix(ev.Kind.String(), "TASK_EVENT_KIND_") == kind {
			out = append(out, ev.Text)
		}
	}
	return out
}

func catalog() []catalogCase {
	const claudeSession = "0199c3a0-1b2c-7d3e-8f40-5a6b7c8d9e0f"
	const agyConversation = "9ec58bfd-4d67-4f5e-83a5-9d907e9c6b1f"
	const codexThread = "0199d1e2-3f40-7a51-8b62-7c83d94ea5f6"
	return []catalogCase{
		// Claude.
		{provider: "claude", fixture: "success", want: []string{"STATUS", "TEXT", "USAGE"},
			check: func(t *testing.T, events []Event, args, _ string) {
				if ev := event(t, events, "STATUS"); ev.SessionID != claudeSession {
					t.Errorf("session = %q", ev.SessionID)
				}
				if !strings.Contains(args, "--session-id\nt1") || strings.Contains(args, "--permission-mode") {
					t.Errorf("args = %q", args)
				}
			}},
		{provider: "claude", fixture: "tool-call",
			want: []string{"STATUS", "TEXT", "TOOL_CALL", "TOOL_RESULT", "OTHER", "TOOL_CALL", "TOOL_RESULT", "TEXT", "USAGE"}},
		{provider: "claude", fixture: "error", end: "eof:1", want: []string{"STATUS", "TEXT", "ERROR", "USAGE"},
			exit: 1, err: "API Error: 500"},
		{provider: "claude", fixture: "budget-reached", end: "eof:1", spec: Spec{MaxBudgetUSD: 0.5},
			want: []string{"STATUS", "TOOL_CALL", "TOOL_RESULT", "ERROR", "USAGE"}, exit: 1, err: "Reached maximum budget",
			check: func(t *testing.T, events []Event, args, _ string) {
				if u := event(t, events, "USAGE").Usage; u.GetCostUsd() != 0.51 {
					t.Errorf("usage = %v", u)
				}
				if !strings.Contains(args, "--max-budget-usd\n0.5") {
					t.Errorf("args = %q", args)
				}
			}},
		{provider: "claude", fixture: "permission-denied",
			want: []string{"STATUS", "TEXT", "TOOL_CALL", "TOOL_RESULT", "TEXT", "STATUS", "USAGE"},
			check: func(t *testing.T, events []Event, _, _ string) {
				if got := texts(events, "STATUS")[1]; got != `permission denied: Bash {"command":"go test ./..."}` {
					t.Errorf("denial = %q", got)
				}
				if got := event(t, events, "TOOL_RESULT").Text; !strings.HasPrefix(got, "error: ") {
					t.Errorf("tool result = %q", got)
				}
			}},
		{provider: "claude", fixture: "resume", spec: Spec{Resume: "0199c3a0-0000-7000-8000-00000000a001"},
			want: []string{"STATUS", "TEXT", "USAGE"},
			check: func(t *testing.T, events []Event, args, _ string) {
				if !strings.Contains(args, "--resume\n0199c3a0-0000-7000-8000-00000000a001") || strings.Contains(args, "--session-id") {
					t.Errorf("args = %q", args)
				}
				if ev := event(t, events, "STATUS"); ev.SessionID != "0199c3a0-0000-7000-8000-00000000a001" {
					t.Errorf("session = %q", ev.SessionID)
				}
			}},
		{provider: "claude", fixture: "session-limit", want: []string{"STATUS", "STATUS", "STATUS", "TEXT", "ERROR", "USAGE"},
			err: "You've hit your session limit · resets 7:20am (Europe/Paris)",
			check: func(t *testing.T, events []Event, _, _ string) {
				// The rejected rate limit says which limit, and when it resets; the warning before it says nothing.
				var limits []Limit
				for _, ev := range events {
					if ev.Limit != nil {
						limits = append(limits, *ev.Limit)
					}
				}
				if want := (Limit{What: "the account's session limit", Until: time.Unix(1791436800, 0)}); len(limits) != 1 ||
					limits[0].What != want.What || !limits[0].Until.Equal(want.Until) {
					t.Errorf("limits = %+v, want %+v", limits, want)
				}
			}},
		{provider: "claude", fixture: "unknown-line", want: []string{"STATUS", "OTHER", "OTHER", "OTHER", "TEXT", "USAGE"},
			check: func(t *testing.T, events []Event, _, _ string) {
				want := []string{"system compact_boundary", "prompt_suggestion", "Warning: a plain line on the output"}
				if got := texts(events, "OTHER"); !slices.Equal(got, want) {
					t.Errorf("others = %q", got)
				}
			}},
		{provider: "claude", fixture: "process-dies", end: "exit:2", want: []string{"STATUS", "TOOL_CALL", "ERROR"},
			exit: 2, err: "claude ended before the end of its turn"},
		{provider: "claude", fixture: "two-turns", send: []string{"And then?"},
			want: []string{"STATUS", "TEXT", "USAGE", "TEXT", "USAGE"},
			check: func(t *testing.T, _ []Event, _, input string) {
				if n := strings.Count(input, `"type":"user"`); n != 2 || !strings.Contains(input, "And then?") {
					t.Errorf("input = %q", input)
				}
			}},
		// A question worker: its compound command refused, the djinn command alone runs; the refusal is said at the end.
		{provider: "claude", fixture: "question-refused-then-spawned", send: []string{"go on"},
			want: []string{"STATUS", "TEXT", "TOOL_CALL", "TOOL_RESULT", "TEXT", "TOOL_CALL", "TOOL_RESULT", "TEXT", "STATUS", "USAGE"},
			check: func(t *testing.T, events []Event, _, _ string) {
				if got := texts(events, "STATUS")[1]; !strings.HasPrefix(got, `permission denied: Bash {"command":"djinn task list`) {
					t.Errorf("denial = %q", got)
				}
			}},
		// A question worker that takes its refused compound command for Bash refused, and ends its turn.
		{provider: "claude", fixture: "question-gives-up",
			want: []string{"STATUS", "TEXT", "TOOL_CALL", "TOOL_RESULT", "TEXT", "STATUS", "USAGE"}},
		// A message sent during the turn is folded into it: one result, queued_turn_count 0, and the worker ends.
		{provider: "claude", fixture: "mid-turn-message", send: []string{"Also check the docs."},
			want: []string{"STATUS", "TEXT", "TEXT", "USAGE"},
			check: func(t *testing.T, _ []Event, _, input string) {
				if n := strings.Count(input, `"type":"user"`); n != 2 || !strings.Contains(input, "Also check the docs.") {
					t.Errorf("input = %q", input)
				}
			}},
		{provider: "claude", fixture: "budget-first-call", end: "eof:1", spec: Spec{MaxBudgetUSD: 0.1},
			want: []string{"STATUS", "TOOL_CALL", "ERROR", "USAGE"}, exit: 1, err: "Reached maximum budget ($0.1)",
			check: func(t *testing.T, events []Event, _, _ string) {
				// The root usage is all zeros: the figures come from modelUsage.
				u := event(t, events, "USAGE").Usage
				if u.GetInputTokens() != 2 || u.GetOutputTokens() != 123 || u.GetCacheWriteTokens() != 22160 || u.GetCostUsd() != 0.179748 {
					t.Errorf("usage = %v", u)
				}
			}},
		{provider: "claude", fixture: "max-turns", want: []string{"STATUS", "TOOL_CALL", "TOOL_RESULT", "ERROR", "USAGE"},
			err: "error_max_turns"},
		{provider: "claude", fixture: "success-haiku", want: []string{"STATUS", "TOOL_CALL", "TOOL_RESULT", "TEXT", "USAGE"},
			check: func(t *testing.T, events []Event, _, _ string) {
				u := event(t, events, "USAGE").Usage
				if u.GetInputTokens() != 4 || u.GetOutputTokens() != 526 || u.GetCacheReadTokens() != 22943 || u.GetCacheWriteTokens() != 27155 {
					t.Errorf("usage = %v", u)
				}
				if got := event(t, events, "TOOL_CALL").Text; got != `Read {"file_path":"/work/project/SETUP.md"}` {
					t.Errorf("call = %q", got)
				}
			}},
		{provider: "claude", fixture: "success", spec: Spec{ReadOnly: true}, want: []string{"STATUS", "TEXT", "USAGE"},
			check: func(t *testing.T, _ []Event, args, _ string) {
				if !strings.Contains(args, "--tools\nRead,Glob,Grep\n--permission-mode\nplan") {
					t.Errorf("args = %q", args)
				}
			}},

		// Antigravity.
		{provider: "antigravity", fixture: "success", want: []string{"STATUS", "TEXT", "USAGE"},
			check: func(t *testing.T, events []Event, args, input string) {
				if ev := event(t, events, "STATUS"); ev.SessionID != agyConversation || !strings.Contains(ev.Text, "gemini") {
					t.Errorf("init = %+v", ev)
				}
				if got := event(t, events, "TEXT").Text; got != "Hello, the worktree is ready." {
					t.Errorf("text = %q", got)
				}
				u := event(t, events, "USAGE").Usage
				if u.GetInputTokens() != 30384 || u.GetOutputTokens() != 9 || u.GetCostUsd() != 0 {
					t.Errorf("usage = %v", u)
				}
				if args != "--input-format\nstream-json\n--output-format\nstream-json" {
					t.Errorf("args = %q", args)
				}
				if !strings.Contains(input, `{"event":"user","message":{"content":"x"}}`) {
					t.Errorf("input = %q", input)
				}
			}},
		{provider: "antigravity", fixture: "tool-call",
			want: []string{"STATUS", "TEXT", "TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT", "TEXT", "USAGE"},
			check: func(t *testing.T, events []Event, _, _ string) {
				calls := texts(events, "TOOL_CALL")
				if calls[0] != `run_command {"CommandLine":"ls"}` || !strings.HasPrefix(calls[1], "view_file ") {
					t.Errorf("calls = %q", calls)
				}
				if got := texts(events, "TOOL_RESULT")[0]; got != "README.md\r\ngo.mod" {
					t.Errorf("result = %q", got)
				}
				if u := event(t, events, "USAGE").Usage; u.GetOutputTokens() != 125 {
					t.Errorf("thinking tokens are not counted as written: %v", u)
				}
			}},
		{provider: "antigravity", fixture: "error", end: "eof:3", want: []string{"STATUS", "TEXT", "ERROR", "USAGE"},
			exit: 3, err: "INTERNAL: the model returned an error",
			check: func(t *testing.T, events []Event, _, _ string) {
				if got := event(t, events, "TEXT").Text; got != "Let me check [cut]" {
					t.Errorf("cut text = %q", got)
				}
				if !slices.Contains(texts(events, "ERROR"), "the model returned an error") {
					t.Errorf("AGY_ERROR not read: %q", texts(events, "ERROR"))
				}
			}},
		{provider: "antigravity", fixture: "limit", end: "eof:3", want: []string{"STATUS", "ERROR", "USAGE"},
			exit: 3, err: "RESOURCE_EXHAUSTED",
			check: func(t *testing.T, events []Event, _, _ string) {
				if !slices.Contains(texts(events, "ERROR"), "Resource exhausted: quota exceeded (HTTP 429), retryable") {
					t.Errorf("errors = %q", texts(events, "ERROR"))
				}
			}},
		// A denied command ends agy's turn with SUCCESS: the worker fails, naming the command (real runs).
		{provider: "antigravity", fixture: "permission-denied",
			want: []string{"STATUS", "TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT",
				"TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT", "ERROR", "USAGE"},
			err: "agy stopped: it cannot run commands headless (go tool task --list). Run this task with claude or codex.",
			check: func(t *testing.T, events []Event, _, _ string) {
				statuses := texts(events, "STATUS")
				if !slices.ContainsFunc(statuses, func(s string) bool {
					return strings.HasPrefix(s, `permission denied: jetski: no output produced — a tool required the "command" permission`)
				}) {
					t.Errorf("statuses = %q", statuses)
				}
			}},
		{provider: "antigravity", fixture: "permission-denied-first",
			want: []string{"STATUS", "TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "ERROR", "USAGE"},
			err:  `agy stopped: it cannot run commands headless (git grep -n "RestartFile"). Run this task with claude or codex.`},
		// The same run, the denial said on the error output only: it may come after the result, the worker fails all the same.
		{provider: "antigravity", fixture: "permission-denied-notice",
			want: []string{"STATUS", "TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "USAGE", "ERROR"},
			err:  `agy stopped: it cannot run commands headless (git grep -n "RestartFile"). Run this task with claude or codex.`},
		{provider: "antigravity", fixture: "resume", spec: Spec{Resume: "9ec58bfd-4d67-4f5e-83a5-9d907e9c6b1f"},
			want: []string{"STATUS", "TEXT", "USAGE"},
			check: func(t *testing.T, events []Event, args, _ string) {
				if !strings.HasSuffix(args, "--conversation\n9ec58bfd-4d67-4f5e-83a5-9d907e9c6b1f") {
					t.Errorf("args = %q", args)
				}
				if ev := event(t, events, "STATUS"); ev.SessionID != "9ec58bfd-4d67-4f5e-83a5-9d907e9c6b1f" {
					t.Errorf("session = %q", ev.SessionID)
				}
			}},
		{provider: "antigravity", fixture: "unknown-line", want: []string{"STATUS", "OTHER", "OTHER", "OTHER", "TEXT", "USAGE"},
			check: func(t *testing.T, events []Event, _, _ string) {
				want := []string{"heartbeat", "step subagent DONE", "warning: falling back to the default model"}
				if got := texts(events, "OTHER"); !slices.Equal(got, want) {
					t.Errorf("others = %q", got)
				}
			}},
		{provider: "antigravity", fixture: "process-dies", end: "exit:1", want: []string{"STATUS", "TEXT", "ERROR"},
			exit: 1, err: "agy ended before the end of its turn",
			check: func(t *testing.T, events []Event, _, _ string) {
				if got := event(t, events, "TEXT").Text; got != "Running the tests [cut]" {
					t.Errorf("cut text = %q", got)
				}
			}},
		// A worker with permissions commits in a linked worktree, its repository outside agy's workspace: Djinn's agy
		// project grants the folders and the commands (real run, agy 1.3.3).
		{provider: "antigravity", fixture: "commit",
			spec: Spec{Permissions: &djinnv1.Permissions{Edit: true, Commands: []string{"git add", "git commit"}, DeniedCommands: []string{"git push"}}},
			want: []string{"STATUS", "TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT",
				"TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT", "TEXT", "USAGE"},
			check: func(t *testing.T, events []Event, args, _ string) {
				if got := texts(events, "TOOL_RESULT"); !slices.Contains(got, "[w1 fea0a4e] b\r\n 1 file changed, 1 insertion(+)\r\n create mode 100644 b.txt") {
					t.Errorf("results = %q", got)
				}
				list := strings.Split(args, "\n")
				i := slices.Index(list, "--project")
				if i < 0 || slices.Contains(list, "--sandbox") {
					t.Fatalf("args = %q", args)
				}
				dir, _ := agyProjectsDir()
				b, err := os.ReadFile(filepath.Join(dir, list[i+1]+".json"))
				if err != nil {
					t.Fatal(err)
				}
				var f agyProjectFile
				if err := json.Unmarshal(b, &f); err != nil || f.ID != list[i+1] {
					t.Fatalf("project %s: %v", b, err)
				}
				g := f.PermissionGrants.PermissionGrants
				if len(g.Allow) != 3 || !strings.HasPrefix(g.Allow[0], "write_file(") || g.Allow[2] != "command(git commit)" ||
					!slices.Equal(g.Deny, []string{"command(git push)"}) {
					t.Errorf("grants = %+v", g)
				}
			}},
		// AUTO: agy runs any command, compound ones included, in its sandbox; a deny grant refuses git push, and the
		// turn goes on (real run, agy 1.3.3).
		{provider: "antigravity", fixture: "auto",
			spec: Spec{Permissions: &djinnv1.Permissions{Edit: true, Mode: djinnv1.Mode_MODE_AUTO}},
			want: []string{"STATUS", "TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT", "TOOL_CALL",
				"TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "STATUS", "TOOL_CALL", "TOOL_RESULT", "TEXT", "USAGE"},
			check: func(t *testing.T, events []Event, args, _ string) {
				if got := texts(events, "TOOL_RESULT"); !slices.Equal(got, []string{"6", "one\r\ntwo", "ok", "bar", "baz", "after-push"}) {
					t.Errorf("results = %q", got)
				}
				if got := texts(events, "STATUS"); len(got) != 2 || got[1] != "permission denied: git push origin w1" {
					t.Errorf("statuses = %q", got)
				}
				list := strings.Split(args, "\n")
				i := slices.Index(list, "--project")
				dir, _ := agyProjectsDir()
				b, err := os.ReadFile(filepath.Join(dir, list[i+1]+".json"))
				var f agyProjectFile
				if err != nil || json.Unmarshal(b, &f) != nil {
					t.Fatalf("project %s: %v", b, err)
				}
				if g := f.PermissionGrants.PermissionGrants; !slices.Contains(g.Allow, "command(*)") || !slices.Equal(g.Deny, []string{"command(git push)"}) {
					t.Errorf("grants = %+v", g)
				}
			}},
		{provider: "antigravity", fixture: "two-turns", send: []string{"And then?"},
			want: []string{"STATUS", "TEXT", "USAGE", "TEXT", "USAGE"},
			check: func(t *testing.T, _ []Event, _, input string) {
				if n := strings.Count(input, `"event":"user"`); n != 2 {
					t.Errorf("input = %q", input)
				}
			}},

		// Codex.
		{provider: "codex", fixture: "success", want: []string{"STATUS", "TEXT", "USAGE", "STATUS"},
			check: func(t *testing.T, events []Event, args, input string) {
				if args != "app-server\n--listen\nstdio://" {
					t.Errorf("args = %q", args)
				}
				if ev := event(t, events, "STATUS"); ev.SessionID != codexThread || !strings.Contains(ev.Text, "gpt-5.1-codex") {
					t.Errorf("thread = %+v", ev)
				}
				u := event(t, events, "USAGE").Usage
				if u.GetInputTokens() != 3000 || u.GetCacheReadTokens() != 9000 || u.GetOutputTokens() != 40 {
					t.Errorf("usage = %v", u)
				}
				for _, want := range []string{`"method":"initialize"`, `"method":"initialized"`, `"method":"thread/start"`, `"method":"turn/start"`} {
					if !strings.Contains(input, want) {
						t.Errorf("input lacks %s: %q", want, input)
					}
				}
				// In a project, codex's own configuration decides: Djinn sets neither the sandbox nor the approvals.
				if strings.Contains(input, "sandbox") || strings.Contains(input, "approvalPolicy") {
					t.Errorf("a worker in a project gets a permission setting: %q", input)
				}
			}},
		{provider: "codex", fixture: "tool-call",
			want: []string{"STATUS", "TEXT", "TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT", "OTHER", "TEXT", "USAGE", "STATUS"},
			check: func(t *testing.T, events []Event, _, _ string) {
				want := []string{"command ls", "fileChange README.md"}
				if got := texts(events, "TOOL_CALL"); !slices.Equal(got, want) {
					t.Errorf("calls = %q", got)
				}
				want = []string{"README.md\ngo.mod", "completed"}
				if got := texts(events, "TOOL_RESULT"); !slices.Equal(got, want) {
					t.Errorf("results = %q", got)
				}
			}},
		{provider: "codex", fixture: "error", want: []string{"STATUS", "STATUS", "ERROR", "STATUS"},
			err: "responseStreamDisconnected: stream disconnected before completion",
			check: func(t *testing.T, events []Event, _, _ string) {
				if got := texts(events, "STATUS"); !strings.HasPrefix(got[1], "retrying: ") || got[2] != "turn failed" {
					t.Errorf("statuses = %q", got)
				}
			}},
		{provider: "codex", fixture: "limit", want: []string{"STATUS", "ERROR", "STATUS"},
			err: "usageLimitExceeded: You've hit your usage limit."},
		{provider: "codex", fixture: "error-completed", want: []string{"STATUS", "ERROR", "STATUS"},
			err: "internalServerError: the model returned an error"},
		{provider: "codex", fixture: "unknown-status", want: []string{"STATUS", "ERROR", "STATUS"}, err: "codex turn cancelled"},
		{provider: "codex", fixture: "permission-denied",
			want: []string{"STATUS", "TOOL_CALL", "STATUS", "TOOL_RESULT", "TEXT", "STATUS"},
			check: func(t *testing.T, events []Event, _, input string) {
				if got := texts(events, "STATUS")[1]; got != "permission denied: commandExecution go test ./... (needs network to download modules)" {
					t.Errorf("denial = %q", got)
				}
				if got := event(t, events, "TOOL_RESULT").Text; got != "declined" {
					t.Errorf("result = %q", got)
				}
				if !strings.Contains(input, `{"id":0,"result":{"decision":"decline"}}`) {
					t.Errorf("input = %q", input)
				}
			}},
		// The same approval under the project's permissions listing the command: Djinn accepts it.
		{provider: "codex", fixture: "permission-denied", spec: Spec{Permissions: &djinnv1.Permissions{Edit: true, Commands: []string{"go test"}}},
			want: []string{"STATUS", "TOOL_CALL", "STATUS", "TOOL_RESULT", "TEXT", "STATUS"},
			check: func(t *testing.T, events []Event, _, input string) {
				if got := texts(events, "STATUS")[1]; got != "permission granted: commandExecution go test ./..." {
					t.Errorf("approval = %q", got)
				}
				if !strings.Contains(input, `{"id":0,"result":{"decision":"accept"}}`) ||
					!strings.Contains(input, `"approvalPolicy":"untrusted"`) || !strings.Contains(input, `"sandbox":"workspace-write"`) {
					t.Errorf("input = %q", input)
				}
			}},
		{provider: "codex", fixture: "resume", spec: Spec{Resume: codexThread}, want: []string{"STATUS", "TEXT", "USAGE", "STATUS"},
			check: func(t *testing.T, events []Event, _, input string) {
				if !strings.Contains(input, `"method":"thread/resume","params":{"cwd":`) ||
					!strings.Contains(input, `"excludeTurns":true`) || !strings.Contains(input, `"threadId":"`+codexThread+`"`) {
					t.Errorf("input = %q", input)
				}
				if ev := event(t, events, "STATUS"); ev.SessionID != codexThread {
					t.Errorf("session = %q", ev.SessionID)
				}
			}},
		{provider: "codex", fixture: "resume-missing", spec: Spec{Resume: codexThread}, want: []string{"ERROR"},
			err: "codex thread/resume: thread not found"},
		{provider: "codex", fixture: "unknown-line", want: []string{"STATUS", "OTHER", "OTHER", "OTHER", "STATUS", "TEXT", "STATUS"},
			check: func(t *testing.T, events []Event, _, input string) {
				want := []string{"account/rateLimits/updated", "WARNING: proceeding, even though we could not update PATH", "contextCompaction"}
				if got := texts(events, "OTHER"); !slices.Equal(got, want) {
					t.Errorf("others = %q", got)
				}
				if !strings.Contains(input, `{"error":{"code":-32601,`) {
					t.Errorf("the unknown request is not refused: %q", input)
				}
			}},
		{provider: "codex", fixture: "process-dies", end: "exit:1", want: []string{"STATUS", "TOOL_CALL", "ERROR"},
			exit: 1, err: "codex ended before the end of its turn"},
		{provider: "codex", fixture: "two-turns", send: []string{"And then?"},
			want: []string{"STATUS", "TEXT", "STATUS", "TEXT", "USAGE", "STATUS"},
			check: func(t *testing.T, _ []Event, _, input string) {
				if n := strings.Count(input, `"method":"turn/start"`); n != 2 || !strings.Contains(input, "And then?") {
					t.Errorf("input = %q", input)
				}
			}},
		{provider: "codex", fixture: "success", spec: Spec{ReadOnly: true}, want: []string{"STATUS", "TEXT", "USAGE", "STATUS"},
			check: func(t *testing.T, _ []Event, _, input string) {
				for _, want := range []string{`"approvalPolicy":"never"`, `"sandbox":"read-only"`, `"sandboxPolicy":{"networkAccess":false,"type":"readOnly"}`} {
					if !strings.Contains(input, want) {
						t.Errorf("input lacks %s: %q", want, input)
					}
				}
			}},
	}
}

func provider(name string) Provider {
	switch name {
	case "claude":
		return Claude{Command: os.Args[0]}
	case "antigravity":
		return Antigravity{Command: os.Args[0]}
	}
	return Codex{Command: os.Args[0]}
}

// stderrEvents are the events the provider makes of the lines of its error output.
func stderrEvents(name string, lines []string) []Event {
	var out []Event
	for _, l := range lines {
		switch name {
		case "claude", "codex":
			out = append(out, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_LOG, Text: l})
		case "antigravity":
			out = append(out, (&agyParser{}).stderr(l)...)
		}
	}
	return out
}

// TestCatalog replays every case of the catalog through the real process path, the test binary playing the
// provider, and checks the events Djinn records and how the worker ends.
func TestCatalog(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, c := range catalog() {
		name := c.provider + "/" + c.fixture
		if c.spec.ReadOnly {
			name += "/read-only"
		}
		seen[filepath.Join("testdata", c.provider, c.fixture+".jsonl")] = true
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env, argsFile, inputFile := fake{provider: c.provider, fixture: c.fixture, end: c.end}.env(t)
			spec := c.spec
			spec.TaskID, spec.Dir, spec.Prompt, spec.Env = "t1", t.TempDir(), "x", env
			w, err := provider(c.provider).Start(t.Context(), spec)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range c.send {
				if err := w.Send(m); err != nil {
					t.Fatal(err)
				}
			}
			// A worker left waiting on its input would hang the test: it is stopped, and that fails the case.
			hung := time.AfterFunc(10*time.Second, func() {
				t.Errorf("the worker still runs after 10s")
				w.Stop()
			})
			events := collect(w)
			hung.Stop()
			res := w.Wait()

			// The error output is read apart from the output: its events come in any order among the others.
			var stderr []string
			if b, err := os.ReadFile(filepath.Join("testdata", c.provider, c.fixture+".stderr")); err == nil {
				stderr = strings.Split(strings.TrimRight(string(b), "\n"), "\n")
			}
			rest := slices.Clone(events)
			for _, want := range stderrEvents(c.provider, stderr) {
				i := slices.IndexFunc(rest, func(ev Event) bool { return ev.Kind == want.Kind && ev.Text == want.Text })
				if i < 0 {
					t.Errorf("no %s event %q from the error output", want.Kind, want.Text)
					continue
				}
				rest = slices.Delete(rest, i, i+1)
			}
			if got := kinds(rest); !slices.Equal(got, c.want) {
				t.Errorf("kinds = %v\nwant %v\nevents: %+v", got, c.want, events)
			}
			if res.ExitCode != c.exit {
				t.Errorf("exit code = %d, want %d", res.ExitCode, c.exit)
			}
			switch {
			case c.err == "" && res.Err != nil:
				t.Errorf("error = %v, want none", res.Err)
			case c.err != "" && (res.Err == nil || !strings.Contains(res.Err.Error(), c.err)):
				t.Errorf("error = %v, want %q", res.Err, c.err)
			}
			if err := w.Send("late"); err == nil {
				t.Errorf("Send after the end took the message")
			}
			if c.check != nil {
				args, _ := os.ReadFile(argsFile)
				input, _ := os.ReadFile(inputFile)
				c.check(t, events, string(args), string(input))
			}
		})
	}
	// Every fixture of the catalog is replayed.
	files, _ := filepath.Glob(filepath.Join("testdata", "*", "*.jsonl"))
	for _, f := range files {
		if !seen[f] {
			t.Errorf("%s is in no case of the catalog", f)
		}
	}
}

func TestProviderArgs(t *testing.T) {
	t.Parallel()
	agy := []struct {
		spec Spec
		want []string
		err  bool
	}{
		{Spec{}, []string{"--input-format", "stream-json", "--output-format", "stream-json"}, false},
		{Spec{Model: "gemini-3.8-flash-medium", Resume: "c1"},
			[]string{"--input-format", "stream-json", "--output-format", "stream-json", "--conversation", "c1", "--model", "gemini-3.8-flash-medium"}, false},
		{Spec{ReadOnly: true}, nil, true},
		{Spec{Resume: "c1", Fork: true}, nil, true},
		{Spec{Dir: "/w", Permissions: &djinnv1.Permissions{Edit: true, Mode: djinnv1.Mode_MODE_AUTO}},
			[]string{"--input-format", "stream-json", "--output-format", "stream-json", "--mode", "accept-edits",
				"--project", agyProjectID("/w")}, false},
		{Spec{Dir: "/w", Permissions: &djinnv1.Permissions{Edit: true, Network: true}},
			[]string{"--input-format", "stream-json", "--output-format", "stream-json", "--mode", "accept-edits", "--project", agyProjectID("/w")}, false},
		{Spec{Permissions: &djinnv1.Permissions{Commands: []string{"git status"}}}, nil, true},
	}
	for _, tt := range agy {
		got, err := Antigravity{}.args(tt.spec)
		if (err != nil) != tt.err || !slices.Equal(got, tt.want) {
			t.Errorf("agy args(%+v) = %v, %v; want %v", tt.spec, got, err, tt.want)
		}
		if tt.err && tt.spec.Fork == false && !errors.Is(err, ErrReadOnly) {
			t.Errorf("agy args(%+v): %v, want ErrReadOnly", tt.spec, err)
		}
	}

	codex := []struct {
		spec   Spec
		method string
		params map[string]any
	}{
		{Spec{Dir: "/w"}, "thread/start", map[string]any{"cwd": "/w"}},
		{Spec{Dir: "/w", Model: "gpt-5.1-codex"}, "thread/start", map[string]any{"cwd": "/w", "model": "gpt-5.1-codex"}},
		{Spec{Dir: "/w", Resume: "th"}, "thread/resume", map[string]any{"cwd": "/w", "threadId": "th", "excludeTurns": true}},
		{Spec{Dir: "/w", Resume: "th", Fork: true}, "thread/fork", map[string]any{"cwd": "/w", "threadId": "th", "excludeTurns": true}},
		{Spec{Dir: "/w", ReadOnly: true}, "thread/start", map[string]any{"cwd": "/w", "sandbox": "read-only", "approvalPolicy": "never"}},
		{Spec{Dir: "/w", ReadOnly: true, Permissions: &djinnv1.Permissions{Edit: true}}, "thread/start",
			map[string]any{"cwd": "/w", "sandbox": "read-only", "approvalPolicy": "never"}},
		{Spec{Dir: "/w", Permissions: &djinnv1.Permissions{Edit: true}}, "thread/start",
			map[string]any{"cwd": "/w", "sandbox": "workspace-write", "approvalPolicy": "untrusted"}},
		{Spec{Dir: "/w", Permissions: &djinnv1.Permissions{Edit: true, Mode: djinnv1.Mode_MODE_AUTO}}, "thread/start",
			map[string]any{"cwd": "/w", "sandbox": "workspace-write", "approvalPolicy": "on-request", "approvalsReviewer": "auto_review"}},
		{Spec{Dir: "/w", Permissions: &djinnv1.Permissions{Mode: djinnv1.Mode_MODE_AUTO}}, "thread/start",
			map[string]any{"cwd": "/w", "sandbox": "read-only", "approvalPolicy": "untrusted"}},
	}
	for _, tt := range codex {
		method, params := Codex{}.threadRequest(tt.spec)
		if method != tt.method || len(params) != len(tt.params) {
			t.Errorf("codex thread(%+v) = %s %v; want %s %v", tt.spec, method, params, tt.method, tt.params)
			continue
		}
		for k, v := range tt.params {
			if params[k] != v {
				t.Errorf("codex thread(%+v)[%s] = %v; want %v", tt.spec, k, params[k], v)
			}
		}
	}
	if p := (Codex{}).turnParams(Spec{Dir: "/w"}, "th", "hi"); p["sandboxPolicy"] != nil || p["approvalPolicy"] != nil {
		t.Errorf("a turn in a project gets a permission setting: %v", p)
	}
	turn := (Codex{}).turnParams(Spec{Dir: "/w", Permissions: &djinnv1.Permissions{Edit: true, Network: true, Mode: djinnv1.Mode_MODE_AUTO}}, "th", "hi")
	if b, _ := json.Marshal(turn); !strings.Contains(string(b), `"approvalPolicy":"on-request","approvalsReviewer":"auto_review"`) ||
		!strings.Contains(string(b), `"sandboxPolicy":{"excludeSlashTmp":false,"excludeTmpdirEnvVar":false,"networkAccess":true,"type":"workspaceWrite","writableRoots":[]}`) {
		t.Errorf("an auto turn with edit and network: %s", b)
	}
	turn = (Codex{}).turnParams(Spec{Dir: "/w", Permissions: &djinnv1.Permissions{}}, "th", "hi")
	if b, _ := json.Marshal(turn); !strings.Contains(string(b), `"approvalPolicy":"untrusted"`) ||
		!strings.Contains(string(b), `"sandboxPolicy":{"networkAccess":false,"type":"readOnly"}`) || strings.Contains(string(b), "approvalsReviewer") {
		t.Errorf("a listed turn without edit: %s", b)
	}
}

// TestProviderMissing: a provider whose program is not installed fails to start with a clear error.
func TestProviderMissing(t *testing.T) {
	t.Parallel()
	for _, p := range []Provider{
		Claude{Command: "djinn-no-such-claude"}, Codex{Command: "djinn-no-such-codex"}, Antigravity{Command: "djinn-no-such-agy"},
	} {
		_, err := p.Start(t.Context(), Spec{Dir: t.TempDir(), Prompt: "x"})
		if err == nil || !strings.Contains(err.Error(), "not found in PATH") {
			t.Errorf("%T: error = %v", p, err)
		}
	}
}

// TestSpawnMissingProvider: djinn task spawn --provider codex, codex not installed, fails with an error that names
// it, and the task records why.
func TestSpawnMissingProvider(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir())
	e.h.providers[planv1.Provider_PROVIDER_CODEX] = Codex{Command: "codex-djinn-missing"}
	wishID, _ := e.wish(t, t.TempDir())
	_, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Try codex", Provider: planv1.Provider_PROVIDER_CODEX,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "codex-djinn-missing not found in PATH") {
		t.Fatalf("spawn = %v", err)
	}
	list, err := e.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	task := list.Msg.GetTasks()[0]
	if task.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED || task.GetProvider() != planv1.Provider_PROVIDER_CODEX ||
		!strings.Contains(task.GetError(), "not found in PATH") {
		t.Errorf("task = %v", task)
	}
}

// recorder is a provider that records the spec it starts a worker with, and plays a fake.
type recorder struct{ specs chan Spec }

func (r recorder) Start(ctx context.Context, spec Spec) (Worker, error) {
	r.specs <- spec
	spec.Prompt = "text read"
	return Fake{}.Start(ctx, spec)
}

// TestSpawnOutsideProject: a task of a wish without any project runs read-only, in an empty folder of its own
// under Djinn's data folder.
func TestSpawnOutsideProject(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	e := up(t, home)
	rec := recorder{specs: make(chan Spec, 1)}
	e.h.providers[planv1.Provider_PROVIDER_CLAUDE] = rec
	wish, err := e.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Read a document"}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wish.Msg.GetWish().GetId(), Title: "Summarize the spec",
	}))
	if err != nil {
		t.Fatal(err)
	}
	task := res.Msg.GetTask()
	spec := <-rec.specs
	if !spec.ReadOnly || spec.Dir != filepath.Join(home, "tasks", task.GetId()) || task.GetProjectId() != "" {
		t.Errorf("spec = %+v, task = %v", spec, task)
	}
	if entries, err := os.ReadDir(spec.Dir); err != nil || len(entries) != 0 {
		t.Errorf("folder = %v, %v; want an empty folder", entries, err)
	}
	events := e.watch(t.Context(), t, task.GetId(), 0)
	if !strings.Contains(events[1].GetText(), "read-only: outside any project") {
		t.Errorf("start = %q", events[1].GetText())
	}
}
