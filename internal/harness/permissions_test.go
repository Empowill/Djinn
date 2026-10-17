package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// writeFile writes content at the relative path name under dir, creating its folders.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPermissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if p, err := LoadPermissions(dir); p != nil || err != nil {
		t.Errorf("no file: %v, %v", p, err)
	}
	writeFile(t, dir, ".agents/permissions.txtpb", "# A comment\nedit: true\ncommands: \"go tool task test\"\nmode: MODE_AUTO\n")
	p, err := LoadPermissions(dir)
	if err != nil || !p.GetEdit() || p.GetCommands()[0] != "go tool task test" || p.GetMode() != djinnv1.Mode_MODE_AUTO {
		t.Errorf("valid file: %v, %v", p, err)
	}
	for _, bad := range []string{
		"edit: maybe\n",
		`commands: "go test *"` + "\n",
		`commands: "make && rm -rf /"` + "\n",
		`denied_commands: "git  push"` + "\n",
		`commands: ""` + "\n",
	} {
		writeFile(t, dir, ".agents/permissions.txtpb", bad)
		if p, err := LoadPermissions(dir); err == nil || !strings.Contains(err.Error(), "permissions.txtpb") {
			t.Errorf("%q: %v, %v; want an error naming the file", bad, p, err)
		}
	}

	writeFile(t, dir, ".agents/permissions.txtpb", "unknown_field: 1\nedit: true\n")
	p, err = LoadPermissions(dir)
	if err != nil || !p.GetEdit() {
		t.Errorf("unknown field: %v, %v", p, err)
	}
}

func TestCommandAllowed(t *testing.T) {
	t.Parallel()
	p := &djinnv1.Permissions{Commands: []string{"go tool task test", "git"}, DeniedCommands: []string{"git push"}}
	for command, want := range map[string]bool{
		"go tool task test":            true,
		"go tool task test -- -run X":  true,
		"  go  tool task   test  ":     true,
		"go tool task test-go":         false,
		"go tool task":                 false,
		"git status":                   true,
		"git push origin main":         false,
		"git push":                     false,
		"gitk":                         false,
		"bash -lc 'go tool task test'": false,
		"":                             false,
	} {
		if got := commandAllowed(p, command); got != want {
			t.Errorf("commandAllowed(%q) = %v, want %v", command, got, want)
		}
	}

	djinnPerms := djinnOnly()
	for command, want := range map[string]bool{
		`djinn question revise Q01 --wish-id w1 --context "SQLite is free"`:     true,
		`djinn question revise Q01 --wish-id w1 --context-file /tmp/context.md`: true,
		"djinn question revise Q01 --wish-id w1 --options \"A: Keep SQLite\" --options \"B: Move to Postgres\" " +
			"--recommendation \"A: Keep SQLite\" --context \"One-line why.\n\n~~~sh\n # check version\nsqlite3 --version\n~~~\n\n- Verified in go.mod.\"": true,
		"djinn question list --wish-id w1":         true,
		"djinn task get t1":                        true,
		"djinn task list --wish-id w1":             true,
		"djinn wish brief w1":                      true,
		"djinn block list w1":                      true,
		"djinn tilasm list w1":                     true,
		"djinn tilasm get w1 notes":                true,
		"git log -n 5 main..HEAD":                  true,
		"git show HEAD:internal/plan/rounds.go":    true,
		"git diff main...HEAD":                     true,
		"git status --short":                       true,
		"git branch -a":                            true,
		"git rev-parse HEAD":                       true,
		"git ls-tree -r main":                      true,
		"git checkout -b other":                    false,
		"git push origin main":                     false,
		"djinn gate run lint -- go tool task lint": false,
	} {
		if got := commandAllowed(djinnPerms, command); got != want {
			t.Errorf("commandAllowed(djinnOnly, %q) = %v, want %v", command, got, want)
		}
	}
}

// splitUnquoted splits command on shell operators (||, &&, |, ;, &, newline) outside single or double quotes.
func splitUnquoted(command string) []string {
	var parts []string
	var cur strings.Builder
	var quote byte
	for i := 0; i < len(command); i++ {
		c := command[i]
		if quote != 0 {
			if c == '\\' && quote == '"' && i+1 < len(command) {
				cur.WriteByte(c)
				i++
				cur.WriteByte(command[i])
				continue
			}
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			cur.WriteByte(c)
			continue
		}
		if (c == '|' || c == '&') && i+1 < len(command) && command[i+1] == c {
			parts = append(parts, cur.String())
			cur.Reset()
			i++
			continue
		}
		if c == '|' || c == ';' || c == '&' || c == '\n' {
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	return append(parts, cur.String())
}

// TestQuestionWorkerClaudeRules: what Claude gets for a question worker (TASK_ACCESS_DJINN), as its permission
// syntax reads it (code.claude.com/docs/en/permissions, read 2026-10-10): dontAsk, which still runs file reads in
// its working folder, the project's, with Read, Grep and Glob, and searches the web with WebFetch and WebSearch,
// none of them denied nor taken away; each djinn command allowed with its arguments by Bash(<command> *), the same
// as Bash(<command>:*); no edit.
func TestQuestionWorkerClaudeRules(t *testing.T) {
	t.Parallel()
	readOnly, perms := accessSpec(planv1.TaskAccess_TASK_ACCESS_DJINN, nil)
	args := Claude{}.args(Spec{TaskID: "t1", Dir: t.TempDir(), ReadOnly: readOnly, Permissions: perms})
	joined := strings.Join(args, " ")
	if readOnly || !strings.Contains(joined, "--permission-mode dontAsk") || strings.Contains(joined, "--tools") ||
		strings.Contains(joined, "--restricted") {
		t.Fatalf("args = %q", args)
	}
	var settings struct {
		Permissions struct{ Allow, Deny []string }
	}
	if err := json.Unmarshal([]byte(args[slices.Index(args, "--settings")+1]), &settings); err != nil {
		t.Fatal(err)
	}
	allow, deny := settings.Permissions.Allow, settings.Permissions.Deny
	for _, c := range slices.Concat(djinnCommands, gitRead) {
		if !slices.Contains(allow, "Bash("+c+" *)") {
			t.Errorf("%s is not allowed with its arguments: %v", c, allow)
		}
	}
	for _, w := range []string{"WebFetch", "WebSearch"} {
		if !slices.Contains(allow, w) {
			t.Errorf("%s is not allowed: %v", w, allow)
		}
	}
	for _, d := range deny {
		if tool, _, _ := strings.Cut(d, "("); slices.Contains([]string{"Read", "Grep", "Glob", "Bash", "WebFetch", "WebSearch"}, tool) {
			t.Errorf("%s denies what a question worker needs", d)
		}
	}
	for _, d := range []string{"Edit", "Write", "NotebookEdit"} {
		if !slices.Contains(deny, d) {
			t.Errorf("%s is not denied: %v", d, deny)
		}
	}
	// Claude splits a command on its operators outside quotes, and allows it only when a rule matches every part.
	allowed := func(command string) bool {
		for _, part := range splitUnquoted(command) {
			part = strings.TrimSpace(part)
			if strings.Contains(part, " > ") || strings.Contains(part, " >> ") {
				return false
			}
			if !slices.ContainsFunc(allow, func(rule string) bool {
				p, ok := strings.CutSuffix(strings.TrimPrefix(rule, "Bash("), " *)")
				return ok && strings.HasPrefix(rule, "Bash(") && (part == p || strings.HasPrefix(part, p+" "))
			}) {
				return false
			}
		}
		return true
	}
	for command, want := range map[string]bool{
		`djinn task spawn w1 --title "Move the store" --prompt "In internal/store." --decision Q01`: true,
		"djinn task list --wish-id w1":                                      true,
		`djinn question revise Q01 --wish-id w1 --context "SQLite is free"`: true,
		"djinn question revise Q01 --wish-id w1 --options \"A: First\" --options \"B: Second\" " +
			"--recommendation \"B: Second\" --context \"One-line why.\n\n- Fact one (verified).\n- Fact two (verified).\"": true,
		"djinn task list --wish-id w1 | grep Q01; sed -n 1,40p x.go": false,
		"djinn task list --wish-id w1 > tasks.txt && cat tasks.txt":  false,
		"djinn gate run test -- go tool task test":                   false,
	} {
		if got := allowed(command); got != want {
			t.Errorf("allowed(%q) = %v, want %v", command, got, want)
		}
	}
}

// TestDecideAccess: the order of docs/providers.md. Outside any project, read-only; .agents/permissions.txtpb
// decides when present, Git or not; then the agent's own configuration, in Git or when the folder holds an agent
// configuration file; otherwise read-only until the developer allows editing.
func TestDecideAccess(t *testing.T) {
	t.Parallel()
	const (
		readOnly = planv1.TaskAccess_TASK_ACCESS_READ_ONLY
		agents   = planv1.TaskAccess_TASK_ACCESS_AGENTS
		native   = planv1.TaskAccess_TASK_ACCESS_NATIVE
		asking   = planv1.TaskAccess_TASK_ACCESS_ASKING
	)
	claude, codex, agy, fake := planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_CODEX,
		planv1.Provider_PROVIDER_ANTIGRAVITY, planv1.Provider_PROVIDER_FAKE
	tests := []struct {
		name  string
		git   bool
		files []string
		kind  planv1.Provider
		want  planv1.TaskAccess
	}{
		{"empty folder outside Git", false, nil, claude, asking},
		{"Git repository without configuration", true, nil, claude, native},
		{".agents permissions outside Git", false, []string{".agents/permissions.txtpb"}, codex, agents},
		{".agents permissions win in Git", true, []string{".agents/permissions.txtpb", ".claude/settings.json"}, claude, agents},
		{"AGENTS.md is read by every agent", false, []string{"AGENTS.md"}, fake, native},
		{"agents.md, case aside", false, []string{"agents.md"}, codex, native},
		{"an .agents folder of skills", false, []string{".agents/skills/x/SKILL.md"}, agy, native},
		{"CLAUDE.md for claude", false, []string{"CLAUDE.md"}, claude, native},
		{"CLAUDE.md is not codex's", false, []string{"CLAUDE.md"}, codex, asking},
		{".claude settings for claude", false, []string{".claude/settings.local.json"}, claude, native},
		{".claude/CLAUDE.md for claude", false, []string{".Claude/claude.md"}, claude, native},
		{"an empty .claude folder is not a configuration", false, []string{".claude/notes.txt"}, claude, asking},
		{".codex for codex", false, []string{".codex/config.toml"}, codex, native},
		{"GEMINI.md for agy", false, []string{"GEMINI.md"}, agy, native},
		{"GEMINI.md is not claude's", false, []string{"GEMINI.md"}, claude, asking},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				content := ""
				if strings.HasSuffix(f, "permissions.txtpb") {
					content = "edit: true\n"
				}
				writeFile(t, dir, f, content)
			}
			got, perms, err := decideAccess("", &planv1.Project{Directory: dir, Git: tt.git}, tt.kind, planv1.Allowance_ALLOWANCE_NONE)
			if err != nil || got != tt.want || (got == agents) != (perms != nil) {
				t.Errorf("access = %v, %v, %v; want %v", got, perms, err, tt.want)
			}
		})
	}
	if got, _, err := decideAccess("", nil, claude, planv1.Allowance_ALLOWANCE_AUTO); got != readOnly || err != nil {
		t.Errorf("outside any project: %v, %v", got, err)
	}
	dir := t.TempDir()
	writeFile(t, dir, ".agents/permissions.txtpb", "edit: yes please\n")
	if _, _, err := decideAccess("", &planv1.Project{Directory: dir, Git: true}, claude, planv1.Allowance_ALLOWANCE_EDIT); err == nil {
		t.Error("an invalid permissions file: no error")
	}
}

// TestRepoPermissions: Djinn's own .agents/permissions.txtpb is valid, and .claude/settings.json, for a contributor
// who runs claude in the repository without Djinn, holds exactly what Djinn translates from it.
func TestRepoPermissions(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	p, err := LoadPermissions(root)
	if err != nil || p == nil {
		t.Fatalf("%s: %v, %v", PermissionsFile, p, err)
	}
	if !p.GetEdit() || p.GetNetwork() || effectiveMode(p) != djinnv1.Mode_MODE_AUTO {
		t.Errorf("Djinn's permissions: %v", p)
	}
	want, err := json.MarshalIndent(map[string]any{"permissions": claudePermissions(p)}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got, wantAny any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf(".claude/settings.json: %v", err)
	}
	_ = json.Unmarshal(want, &wantAny)
	if !reflect.DeepEqual(got, wantAny) {
		t.Errorf(".claude/settings.json does not follow %s; write it so:\n%s", PermissionsFile, want)
	}
}

func TestCodexDecision(t *testing.T) {
	t.Parallel()
	const command, file = "item/commandExecution/requestApproval", "item/fileChange/requestApproval"
	listed := &djinnv1.Permissions{Edit: true, Commands: []string{"go test"}, DeniedCommands: []string{"go test -exec"}}
	network := json.RawMessage(`{"host":"proxy.golang.org"}`)
	tests := []struct {
		name string
		p    *djinnv1.Permissions
		m    string
		a    codexApproval
		want string
	}{
		{"no permissions", nil, command, codexApproval{Command: "go test"}, "decline"},
		{"listed command", listed, command, codexApproval{Command: "go test ./..."}, "accept"},
		{"denied command", listed, command, codexApproval{Command: "go test -exec x ./..."}, "decline"},
		{"unlisted command", listed, command, codexApproval{Command: "rm -rf ."}, "decline"},
		{"listed command asking for the network", listed, command, codexApproval{Command: "go test", NetworkApprovalContext: network}, "decline"},
		{"network allowed", &djinnv1.Permissions{Commands: []string{"go test"}, Network: true}, command,
			codexApproval{Command: "go test", NetworkApprovalContext: network}, "accept"},
		{"null network context", listed, command, codexApproval{Command: "go test", NetworkApprovalContext: json.RawMessage("null")}, "accept"},
		{"file change with edit", listed, file, codexApproval{}, "accept"},
		{"file change outside the workspace", listed, file, codexApproval{GrantRoot: "/etc"}, "decline"},
		{"file change without edit", &djinnv1.Permissions{}, file, codexApproval{}, "decline"},
		{"another request", listed, "item/tool/requestUserInput", codexApproval{}, "decline"},
	}
	for _, tt := range tests {
		if got := codexDecision(tt.p, tt.m, tt.a); got != tt.want {
			t.Errorf("%s: %s, want %s", tt.name, got, tt.want)
		}
	}
}
