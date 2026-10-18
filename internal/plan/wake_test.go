package plan

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// The lead's watch relies on its agent waking it up when a command it left running ends (decision Q191). The
// fixtures in testdata/wake are real sessions, cut to the steps that show it (see testdata/wake/README.md); these
// tests read them, and check that the brief's rule for that agent runs the watch the way the fixture was woken.

// readJSONL reads one JSON object per line.
func readJSONL(t *testing.T, name string) []map[string]any {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "wake", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rows []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var row map[string]any
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func at(t *testing.T, s any) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s.(string))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestLeadWatchHow: the brief's rule for each agent runs the lead's watch the way its fixture was woken.
func TestLeadWatchHow(t *testing.T) {
	t.Parallel()
	// Claude Code 2.1.296, interactive: a Bash command run with run_in_background, the turn ends, and when the command
	// exits a <task-notification> starts a new turn, without anyone typing.
	t.Run("claude", func(t *testing.T) {
		t.Parallel()
		rows := readJSONL(t, "claude.jsonl")
		if len(rows) != 5 {
			t.Fatalf("%d rows", len(rows))
		}
		message := func(i int) map[string]any { return rows[i]["message"].(map[string]any) }
		call := message(0)["content"].([]any)[0].(map[string]any)
		input := call["input"].(map[string]any)
		if call["name"] != "Bash" || input["run_in_background"] != true || rows[0]["entrypoint"] != "cli" {
			t.Fatalf("the command is not a background Bash of an interactive session: %v", call)
		}
		if message(2)["stop_reason"] != "end_turn" {
			t.Fatalf("the turn did not end before the command: %v", message(2))
		}
		notice, _ := message(3)["content"].(string)
		if rows[3]["type"] != "user" || !strings.HasPrefix(notice, "<task-notification>") ||
			!strings.Contains(notice, "<tool-use-id>"+call["id"].(string)+"</tool-use-id>") || !strings.Contains(notice, "<status>completed</status>") {
			t.Fatalf("no notification of the command's end: %q", notice)
		}
		ended, woken, acted := at(t, rows[2]["timestamp"]), at(t, rows[3]["timestamp"]), at(t, rows[4]["timestamp"])
		if woken.Sub(ended) < 10*time.Second || acted.Sub(woken) > 10*time.Second || rows[4]["type"] != "assistant" {
			t.Errorf("idle %s, then acted %s after the notification: want a wake-up of an idle session", woken.Sub(ended), acted.Sub(woken))
		}
		if rule := leadWatchHow[planv1.Provider_PROVIDER_CLAUDE]; !strings.Contains(rule, "`run_in_background: true`") ||
			!strings.Contains(rule, "<task-notification>") {
			t.Errorf("the brief's rule for Claude Code does not run the watch as the fixture was woken: %s", rule)
		}
	})
	// agy 1.3.3: a run_command that outlives WaitMsBeforeAsync goes to the background as a task, the turn ends, and when
	// it finishes a system message starts a new turn.
	t.Run("antigravity", func(t *testing.T) {
		t.Parallel()
		rows := readJSONL(t, "agy.jsonl")
		if len(rows) != 5 {
			t.Fatalf("%d rows", len(rows))
		}
		call := rows[0]["tool_calls"].([]any)[0].(map[string]any)
		if call["name"] != "run_command" || call["args"].(map[string]any)["WaitMsBeforeAsync"] == nil {
			t.Fatalf("not a run_command with WaitMsBeforeAsync: %v", call)
		}
		background, _ := rows[1]["content"].(string)
		_, task, ok := strings.Cut(background, "task id: ")
		if !ok || rows[1]["status"] != "RUNNING" {
			t.Fatalf("the command did not go to the background: %q", background)
		}
		task = strings.Fields(task)[0]
		if rows[2]["type"] != "PLANNER_RESPONSE" || rows[2]["tool_calls"] != nil {
			t.Fatalf("the turn did not end: %v", rows[2])
		}
		notice, _ := rows[3]["content"].(string)
		if rows[3]["type"] != "SYSTEM_MESSAGE" || !strings.Contains(notice, `Task id "`+task+`" finished with result`) {
			t.Fatalf("no system message of the task's end: %q", notice)
		}
		ended, woken, acted := at(t, rows[2]["created_at"]), at(t, rows[3]["created_at"]), at(t, rows[4]["created_at"])
		if woken.Sub(ended) < 10*time.Second || acted.Sub(woken) > 10*time.Second || rows[4]["source"] != "MODEL" {
			t.Errorf("idle %s, then acted %s after the message: want a wake-up of an idle session", woken.Sub(ended), acted.Sub(woken))
		}
		if rule := leadWatchHow[planv1.Provider_PROVIDER_ANTIGRAVITY]; !strings.Contains(rule, "`run_command`") ||
			!strings.Contains(rule, "`WaitMsBeforeAsync: 500`") || !strings.Contains(rule, "finished with result") {
			t.Errorf("the brief's rule for Antigravity does not run the watch as the fixture was woken: %s", rule)
		}
	})
	// Nothing wakes Codex up when a command ends (no fixture can show it: see testdata/wake/README.md), so its rule
	// leaves no watch running, and Djinn types its line even while a codex watch waits (TestLeadWatchFallbackLine).
	t.Run("codex", func(t *testing.T) {
		t.Parallel()
		rule := leadWatchHow[planv1.Provider_PROVIDER_CODEX]
		if !strings.Contains(rule, "leave no watch running") || !strings.Contains(rule, "--timeout 0s") ||
			!strings.Contains(rule, WaitingLine(2)[len("Djinn: 2"):]) {
			t.Errorf("the brief's rule for Codex: %s", rule)
		}
		brief := LeadWatchRule()
		for _, p := range []planv1.Provider{planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_ANTIGRAVITY, planv1.Provider_PROVIDER_CODEX} {
			if !strings.Contains(brief, leadWatchHow[p]) {
				t.Errorf("the brief lacks the rule of %s", p)
			}
		}
	})
}
