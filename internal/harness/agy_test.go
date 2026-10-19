package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
)

// TestAgyProject: the agy project of a worker grants its folder and Git's folders for it, the listed commands in
// LISTED, every command in AUTO, and denies the denied commands, git push always in AUTO, plus sibling worktrees,
// sibling projects, and data files inside DJINN_HOME.
func TestAgyProject(t *testing.T) {
	t.Run("grants_and_git_dirs", func(t *testing.T) {
		root := t.TempDir()
		repo, wt := filepath.Join(root, "repo"), filepath.Join(root, "worktrees", "W1")
		common := filepath.Join(repo, ".git")
		writeFile(t, common, filepath.Join("worktrees", "W1", "commondir"), "../..\n")
		writeFile(t, wt, ".git", "gitdir: "+filepath.Join(common, "worktrees", "W1")+"\n")
		// A worktree git writes with relative paths (worktree.useRelativePaths).
		rel := filepath.Join(root, "worktrees", "W2")
		writeFile(t, common, filepath.Join("worktrees", "W2", "commondir"), "../..\n")
		writeFile(t, rel, ".git", "gitdir: ../../repo/.git/worktrees/W2\n")
		// A worktree whose folder in the repository lies elsewhere: both are granted.
		apart := filepath.Join(root, "apart")
		writeFile(t, root, filepath.Join("elsewhere", "W3", "commondir"), common+"\n")
		writeFile(t, apart, ".git", "gitdir: "+filepath.Join(root, "elsewhere", "W3")+"\n")
		plain := filepath.Join(root, "plain")
		if err := os.MkdirAll(filepath.Join(plain, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		gitDirs := []struct {
			dir  string
			want []string
		}{
			{wt, []string{common}},
			{filepath.Join(wt, "sub"), []string{common}},
			{rel, []string{common}},
			{apart, []string{common, filepath.Join(root, "elsewhere", "W3")}},
			{plain, []string{filepath.Join(plain, ".git")}},
		}
		for _, tt := range gitDirs {
			if got := agyGitDirs(tt.dir); !slices.Equal(got, tt.want) {
				t.Errorf("agyGitDirs(%s) = %q, want %q", tt.dir, got, tt.want)
			}
		}
		auto := &djinnv1.Permissions{Edit: true, Mode: djinnv1.Mode_MODE_AUTO, Commands: []string{"go tool task lint", "git status"},
			DeniedCommands: []string{"git push"}}
		listed := &djinnv1.Permissions{Edit: true, Commands: []string{"go tool task lint"}}
		autoOwn := &djinnv1.Permissions{Edit: true, Mode: djinnv1.Mode_MODE_AUTO, DeniedCommands: []string{"rm -rf"}}
		projects := []struct {
			spec        Spec
			allow, deny []string
		}{
			{Spec{Dir: wt, Permissions: auto}, []string{"write_file(" + wt + ")", "write_file(" + common + ")", "command(*)", "read_url(*)"},
				[]string{"command(git push)"}},
			// AUTO denies git push even when the project does not; outside Git, every command all the same.
			{Spec{Dir: wt, Permissions: autoOwn}, []string{"write_file(" + wt + ")", "write_file(" + common + ")", "command(*)", "read_url(*)"},
				[]string{"command(rm -rf)", "command(git push)"}},
			{Spec{Dir: filepath.Join(root, "nogit"), Permissions: autoOwn}, []string{"write_file(" + filepath.Join(root, "nogit") + ")", "command(*)", "read_url(*)"},
				[]string{"command(rm -rf)", "command(git push)"}},
			// LISTED: only what is listed, Git's folders all the same.
			{Spec{Dir: wt, Permissions: listed}, []string{"write_file(" + wt + ")", "write_file(" + common + ")", "command(go tool task lint)"}, nil},
			// Question worker (djinnOnly): web reads and djinn commands in LISTED mode, no write_file.
			{Spec{Dir: wt, Permissions: djinnOnly()}, []string{"read_url(*)", "command(djinn question revise)"}, nil},
		}
		for _, tt := range projects {
			f := agyProject(tt.spec)
			g := f.PermissionGrants.PermissionGrants
			if f.ID != agyProjectID(tt.spec.Dir) {
				t.Errorf("agyProject(%v) ID = %q, want %q", tt.spec.Permissions, f.ID, agyProjectID(tt.spec.Dir))
			}
			for _, a := range tt.allow {
				if !slices.Contains(g.Allow, a) {
					t.Errorf("agyProject(%v) missing allow %q; got %+v", tt.spec.Permissions, a, g.Allow)
				}
			}
			for _, d := range tt.deny {
				if !slices.Contains(g.Deny, d) {
					t.Errorf("agyProject(%v) missing deny %q; got %+v", tt.spec.Permissions, d, g.Deny)
				}
			}
		}
		home, _ := os.UserHomeDir()
		goRoot, modCache, buildCache, npmCache := agyCaches()
		fAuto := agyProject(Spec{Dir: wt, Permissions: auto})
		gAuto := fAuto.PermissionGrants.PermissionGrants
		for _, want := range []string{
			"read_file(" + wt + ")",
			"read_file(" + common + ")",
			"read_file(" + repo + ")",
			"read_file(" + goRoot + ")",
			"read_file(" + modCache + ")",
			"read_file(" + buildCache + ")",
			"read_file(" + npmCache + ")",
			"read_file(" + home + ")",
		} {
			if !slices.Contains(gAuto.Allow, want) {
				t.Errorf("missing read grant %q", want)
			}
		}
		for _, s := range []string{".ssh", ".gnupg", filepath.Join(".config", "gh"), filepath.Join(".config", "gcloud"), ".aws", ".netrc", ".git-credentials"} {
			want := "read_file(" + filepath.Join(home, s) + ")"
			if !slices.Contains(gAuto.Deny, want) {
				t.Errorf("missing secret denial %q", want)
			}
		}
		if agyProjectID(wt) == agyProjectID(rel) || agyProjectID(wt) != agyProjectID(wt+string(filepath.Separator)) {
			t.Errorf("project ids: %s %s", agyProjectID(wt), agyProjectID(rel))
		}

		// Written in agy's projects folder, over an earlier one; removed with the worktree.
		spec := Spec{Dir: wt, Permissions: listed}
		if err := writeAgyProject(Spec{Dir: wt, Permissions: auto}); err != nil {
			t.Fatal(err)
		}
		dir, _ := agyProjectsDir()
		path := filepath.Join(dir, agyProjectID(wt)+".json")
		if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), `"command(*)"`) || !strings.Contains(string(b), `"read_url(*)"`) {
			t.Errorf("project file in AUTO = %s, %v", b, err)
		}
		if err := writeAgyProject(spec); err != nil {
			t.Fatal(err)
		}
		if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), `"command(go tool task lint)"`) || strings.Contains(string(b), "command(*)") || strings.Contains(string(b), "read_url(*)") {
			t.Errorf("project file = %s, %v", b, err)
		}
		forgetAgyProject(wt)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("project file after the worktree is gone: %v", err)
		}
	})

	t.Run("djinn_dir_denials", func(t *testing.T) {
		djinnHome := t.TempDir()
		t.Setenv("DJINN_HOME", djinnHome)

		if err := os.WriteFile(filepath.Join(djinnHome, "djinn.db"), []byte("db"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(djinnHome, "tilasms"), 0o755); err != nil {
			t.Fatal(err)
		}
		w1 := filepath.Join(djinnHome, "projects", "p1", "worktrees", "w1")
		w2 := filepath.Join(djinnHome, "projects", "p1", "worktrees", "w2")
		p2 := filepath.Join(djinnHome, "projects", "p2")
		for _, d := range []string{w1, w2, p2} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}

		spec := Spec{Dir: w1, Permissions: &djinnv1.Permissions{Edit: true}}
		f := agyProject(spec)
		g := f.PermissionGrants.PermissionGrants

		if !slices.Contains(g.Allow, "read_file("+w1+")") {
			t.Errorf("missing read_file grant for worktree %s", w1)
		}
		if !slices.Contains(g.Allow, "write_file("+w1+")") {
			t.Errorf("missing write_file grant for worktree %s", w1)
		}

		for _, denied := range []string{
			"read_file(" + filepath.Join(djinnHome, "djinn.db") + ")",
			"read_file(" + filepath.Join(djinnHome, "tilasms") + ")",
			"read_file(" + p2 + ")",
			"read_file(" + w2 + ")",
		} {
			if !slices.Contains(g.Deny, denied) {
				t.Errorf("missing denial %q in %+v", denied, g.Deny)
			}
		}

		if slices.Contains(g.Deny, "read_file("+w1+")") {
			t.Errorf("worktree %s was denied!", w1)
		}

		// The version folder of a tilasm given to the task (Spec.ReadDirs, W360) is granted, and no denial of an ancestor
		// shadows it; the tilasm's other versions and the other tilasms stay denied.
		given := filepath.Join(djinnHome, "tilasms", "l1", "v2")
		other, otherTilasm := filepath.Join(djinnHome, "tilasms", "l1", "v1"), filepath.Join(djinnHome, "tilasms", "l2")
		for _, d := range []string{given, other, otherTilasm} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		gTilasm := agyProject(Spec{Dir: w1, Permissions: &djinnv1.Permissions{Edit: true}, ReadDirs: []string{given}}).PermissionGrants.PermissionGrants
		if !slices.Contains(gTilasm.Allow, "read_file("+given+")") {
			t.Errorf("missing read_file grant for the tilasm %s in %+v", given, gTilasm.Allow)
		}
		for _, d := range []string{filepath.Join(djinnHome, "tilasms"), filepath.Dir(given), given} {
			if slices.Contains(gTilasm.Deny, "read_file("+d+")") {
				t.Errorf("%s is denied: %+v", d, gTilasm.Deny)
			}
		}
		for _, d := range []string{other, otherTilasm, w2} {
			if !slices.Contains(gTilasm.Deny, "read_file("+d+")") {
				t.Errorf("missing denial of %s in %+v", d, gTilasm.Deny)
			}
		}

		// When worktree is outside DJINN_HOME, the entire DJINN_HOME is denied.
		outside := t.TempDir()
		fOutside := agyProject(Spec{Dir: outside, Permissions: &djinnv1.Permissions{Edit: true}})
		gOutside := fOutside.PermissionGrants.PermissionGrants
		if !slices.Contains(gOutside.Deny, "read_file("+djinnHome+")") {
			t.Errorf("outside worktree missing denial for %s; got %+v", djinnHome, gOutside.Deny)
		}
	})
}

// TestAgyStdout: agyParser.stdout parses read_url steps and refusal results (W371), idle pause transitions (W392),
// and background task notices, system messages, and manage_task status/kill updates (W360).
func TestAgyStdout(t *testing.T) {
	t.Parallel()
	const conv = "a7b0d8e7-4941-4195-bd26-dbf86d65ef8d"
	step := func(i int, state, tool, params, output string) string {
		out := ""
		if output != "" {
			b, _ := json.Marshal(output)
			out = `,"output":` + string(b)
		}
		return fmt.Sprintf(`{"event":"step_update","step_update":{"conversation_id":%q,"step_index":%d,"state":%q,"step_type":"tool",`+
			`"tool_name":%q,"tool_info":{"name":%q,"parameters":%s%s}}}`, conv, i, state, tool, tool, params, out)
	}
	notice := func(n int) string {
		return fmt.Sprintf("Created At: 2026-10-17T23:49:15+02:00\nTool is running as a background task with task id: %s/task-%d\n"+
			"Task Description: go test ./internal/dispatch\n", conv, n)
	}
	sysmsg := func(i int, content string) string {
		c := ""
		if content != "" {
			b, _ := json.Marshal(content)
			c = `,"content":` + string(b)
		}
		return fmt.Sprintf(`{"event":"step_update","step_update":{"conversation_id":%q,"step_index":%d,"state":"DONE","step_type":"system_message"%s}}`,
			conv, i, c)
	}
	run := `{"CommandLine":"go test ./internal/dispatch"}`
	gitLog := "dbab85af Merge branch 'w334-the-seat-counts-every-waiting-task-of-8e0eb518' into feat/qol\r\n" +
		"e2e/task-continue.spec.ts | 4 ++\r\n"

	readURLActive := `{"event":"step_update","step_update":{"conversation_id":"4326c014-f11e-4630-933f-bd6c2ec8b570","step_index":32,"state":"ACTIVE","step_type":"tool","tool_name":"read_url_content","tool_info":{"name":"read_url_content","parameters":{"Url":"https://www.anthropic.com/pricing"}}}}`
	readURLDone := `{"event":"step_update","step_update":{"conversation_id":"4326c014-f11e-4630-933f-bd6c2ec8b570","step_index":32,"state":"DONE","step_type":"tool","tool_name":"read_url_content","tool_info":{"name":"read_url_content","parameters":{"Url":"https://www.anthropic.com/pricing"},"output":"# Anthropic Pricing\nClaude 3.5 Sonnet..."}}}`
	readURLResult := `{"event":"result","result":{"conversation_id":"4326c014-f11e-4630-933f-bd6c2ec8b570","status":"SUCCESS","response":"ok","num_turns":1}}`
	readURLRefusal := `{"event":"result","result":{"conversation_id":"4326c014-f11e-4630-933f-bd6c2ec8b570","status":"SUCCESS","num_turns":1,"denied_actions":[{"action":"read_url","display_name":"ReadUrlContent"}]}}`
	idlePause := `{"event":"step_update","step_update":{"conversation_id":"c1","step_index":1,"state":"DONE","step_type":"system","tool_info":{"output":"There are no tools called. Execution paused. Waiting for task completion."}}}`

	for _, c := range []struct {
		name     string
		workDone bool
		bgTasks  []agyBgTask
		lines    []string
		want     []string // background tasks still running
		kinds    []string
		texts    []string // substrings expected in emitted events
		ended    bool
		retry    string
		failure  string
	}{
		{
			name:  "read_url granted",
			lines: []string{readURLActive, readURLDone, readURLResult},
			kinds: []string{"TOOL_CALL", "TOOL_RESULT", "USAGE"},
			texts: []string{`read_url_content {"Url":"https://www.anthropic.com/pricing"}`, "# Anthropic Pricing\nClaude 3.5 Sonnet..."},
			ended: true,
		},
		{
			name:    "read_url refused",
			lines:   []string{readURLActive, readURLRefusal},
			kinds:   []string{"TOOL_CALL", "ERROR", "USAGE"},
			ended:   true,
			failure: `headless, it cannot ask for the "read_url" permission`,
		},
		{
			name:     "idle pause with committed work ends clean",
			workDone: true,
			lines:    []string{idlePause},
			kinds:    []string{"STATUS"},
			texts:    []string{"idle pause with work committed: done"},
			ended:    true,
		},
		{
			name:  "idle pause uncommitted continues once",
			lines: []string{idlePause},
			kinds: []string{"STATUS"},
			texts: []string{"continued:"},
			ended: true,
			retry: agyContinuePrompt,
		},
		{
			name:    "idle pause twice fails",
			lines:   []string{idlePause, idlePause},
			kinds:   []string{"STATUS", "ERROR"},
			ended:   true,
			failure: "idle loop:",
		},
		{
			name:    "idle pause ignored while background task runs",
			bgTasks: []agyBgTask{{id: "c1/task-9", step: 9}},
			lines:   []string{idlePause},
			want:    []string{"c1/task-9"},
		},
		{name: "task- in an output is no task", lines: []string{step(30, "ACTIVE", "run_command", `{"CommandLine":"git log"}`, ""),
			step(30, "DONE", "run_command", `{"CommandLine":"git log"}`, gitLog)},
			kinds: []string{"TOOL_CALL", "TOOL_RESULT"}},
		{name: "the notice records it", lines: []string{step(19, "ACTIVE", "run_command", run, ""), step(19, "RUNNING", "run_command", run, notice(19))},
			want: []string{conv + "/task-19"}, kinds: []string{"TOOL_CALL", "TOOL_RESULT"}},
		{name: "its step ends it", lines: []string{step(19, "ACTIVE", "run_command", run, ""), step(19, "RUNNING", "run_command", run, notice(19)),
			step(19, "DONE", "run_command", run, "ok\r\n")},
			kinds: []string{"TOOL_CALL", "TOOL_RESULT", "TOOL_RESULT"}},
		{name: "a system message without text ends the oldest", lines: []string{step(19, "DONE", "run_command", run, notice(19)),
			step(20, "DONE", "run_command", run, notice(20)), sysmsg(21, "")},
			want: []string{conv + "/task-20"}, kinds: []string{"TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT"}},
		{name: "a system message names the one that ended", lines: []string{step(19, "DONE", "run_command", run, notice(19)),
			step(20, "DONE", "run_command", run, notice(20)), sysmsg(21, `Task id "`+conv+`/task-20" finished with result:`)},
			want: []string{conv + "/task-19"}, kinds: []string{"TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT"}},
		{name: "a restart stops them all", lines: []string{step(19, "DONE", "run_command", run, notice(19)),
			step(20, "DONE", "run_command", run, notice(20)),
			sysmsg(21, "[Notice] All your subagents and background tasks have been stopped due to server restart.")},
			kinds: []string{"TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT"}},
		{name: "manage_task kills it", lines: []string{step(19, "DONE", "run_command", run, notice(19)),
			step(25, "DONE", "manage_task", `{"Action":"kill","TaskId":"`+conv+`/task-19"}`, `Task "`+conv+`/task-19" cancelled.`)},
			kinds: []string{"TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT"}},
		{name: "manage_task sees it running", lines: []string{step(19, "DONE", "run_command", run, notice(19)),
			step(25, "DONE", "manage_task", `{"Action":"status","TaskId":"`+conv+`/task-19"}`, "Task: "+conv+"/task-19\nStatus: RUNNING\n")},
			want: []string{conv + "/task-19"}, kinds: []string{"TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT"}},
		{name: "manage_task sees it done", lines: []string{step(19, "DONE", "run_command", run, notice(19)),
			step(25, "DONE", "manage_task", `{"Action":"status","TaskId":"`+conv+`/task-19"}`, "Task: "+conv+"/task-19\nStatus: DONE\n")},
			kinds: []string{"TOOL_CALL", "TOOL_RESULT", "TOOL_CALL", "TOOL_RESULT"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			workDone := c.workDone
			p := &agyParser{
				toolIdle:   AgyToolIdleTimeout,
				isWorkDone: func() bool { return workDone },
				bgTasks:    slices.Clone(c.bgTasks),
			}
			var events []Event
			var lastEnd *turnEnd
			for i, l := range c.lines {
				p.turnEnded = false
				evs, end := p.stdout(l)
				if i < len(c.lines)-1 && end != nil && end.retry == "" {
					t.Fatalf("unexpected early turn end on line %d: %+v", i, end)
				}
				events = append(events, evs...)
				lastEnd = end
			}
			if (lastEnd != nil) != c.ended {
				t.Fatalf("turn end = %+v, want ended = %v", lastEnd, c.ended)
			}
			if lastEnd != nil {
				if lastEnd.retry != c.retry {
					t.Errorf("retry = %q, want %q", lastEnd.retry, c.retry)
				}
				if c.failure == "" && lastEnd.failure != "" {
					t.Errorf("unexpected failure = %q", lastEnd.failure)
				} else if c.failure != "" && !strings.Contains(lastEnd.failure, c.failure) {
					t.Errorf("failure = %q, want substring %q", lastEnd.failure, c.failure)
				}
			}
			var ids []string
			for _, bg := range p.bgTasks {
				ids = append(ids, bg.id)
			}
			if !slices.Equal(ids, c.want) {
				t.Errorf("background tasks = %q, want %q", ids, c.want)
			}
			if got := kinds(events); !slices.Equal(got, c.kinds) {
				t.Errorf("kinds = %v, want %v", got, c.kinds)
			}
			for _, sub := range c.texts {
				if !slices.ContainsFunc(events, func(ev Event) bool { return strings.Contains(ev.Text, sub) }) {
					t.Errorf("events %+v missing text containing %q", events, sub)
				}
			}
			if !c.ended && (p.toolRunning() != (len(c.want) > 0) || p.idleCap() != AgyToolIdleTimeout) {
				t.Errorf("toolRunning = %v, idleCap = %v", p.toolRunning(), p.idleCap())
			}
		})
	}
}
