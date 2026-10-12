package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestAgyProject: the agy project of a worker grants its folder and Git's folders for it, the listed commands in
// LISTED, every command in AUTO, and denies the denied commands, git push always in AUTO. Git's folders are found as git lays
// them out, without running git: a linked worktree's .git file names its folder in the repository, whose commondir
// names the repository's .git.
func TestAgyProject(t *testing.T) {
	t.Parallel()
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
}

// TestAgyProjectDjinnDirDenials tests that agyProject grants read access to the active task's worktree
// inside DJINN_HOME while recursively denying sibling worktrees, sibling projects, and data files.
// When the worktree is outside DJINN_HOME, the entire DJINN_HOME is denied.
func TestAgyProjectDjinnDirDenials(t *testing.T) {
	djinnHome := t.TempDir()
	t.Setenv("DJINN_HOME", djinnHome)

	// Create structure inside djinnHome:
	// djinn.db
	// tilasms/
	// projects/
	//   p1/
	//     worktrees/
	//       w1/ (active task's worktree)
	//       w2/ (sibling worktree)
	//   p2/ (sibling project)
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

	// Worktree w1 must be allowed for reading and writing.
	if !slices.Contains(g.Allow, "read_file("+w1+")") {
		t.Errorf("missing read_file grant for worktree %s", w1)
	}
	if !slices.Contains(g.Allow, "write_file("+w1+")") {
		t.Errorf("missing write_file grant for worktree %s", w1)
	}

	// Siblings must be denied:
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

	// Worktree w1 itself must NOT be denied.
	if slices.Contains(g.Deny, "read_file("+w1+")") {
		t.Errorf("worktree %s was denied!", w1)
	}

	// When worktree is outside DJINN_HOME, the entire DJINN_HOME is denied.
	outside := t.TempDir()
	fOutside := agyProject(Spec{Dir: outside, Permissions: &djinnv1.Permissions{Edit: true}})
	gOutside := fOutside.PermissionGrants.PermissionGrants
	if !slices.Contains(gOutside.Deny, "read_file("+djinnHome+")") {
		t.Errorf("outside worktree missing denial for %s; got %+v", djinnHome, gOutside.Deny)
	}
}

// TestAgyReadURLStep tests that a recorded read_url step from W371 is parsed by agyParser as a tool call,
// and not as a refusal or error.
func TestAgyReadURLStep(t *testing.T) {
	parser := &agyParser{}

	// Recorded ACTIVE step from W371: agy calls read_url_content.
	stepActive := `{"event":"step_update","step_update":{"conversation_id":"4326c014-f11e-4630-933f-bd6c2ec8b570","step_index":32,"state":"ACTIVE","step_type":"tool","tool_name":"read_url_content","tool_info":{"name":"read_url_content","parameters":{"Url":"https://www.anthropic.com/pricing"}}}}`
	events, end := parser.stdout(stepActive)
	if end != nil {
		t.Fatalf("unexpected turn end: %+v", end)
	}
	if len(events) != 1 || events[0].Kind != planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL {
		t.Fatalf("expected 1 TOOL_CALL event, got %+v", events)
	}
	wantCall := `read_url_content {"Url":"https://www.anthropic.com/pricing"}`
	if events[0].Text != wantCall {
		t.Errorf("got call %q, want %q", events[0].Text, wantCall)
	}

	// Successful DONE step when granted.
	stepDone := `{"event":"step_update","step_update":{"conversation_id":"4326c014-f11e-4630-933f-bd6c2ec8b570","step_index":32,"state":"DONE","step_type":"tool","tool_name":"read_url_content","tool_info":{"name":"read_url_content","parameters":{"Url":"https://www.anthropic.com/pricing"},"output":"# Anthropic Pricing\nClaude 3.5 Sonnet..."}}}`
	events, end = parser.stdout(stepDone)
	if end != nil {
		t.Fatalf("unexpected turn end: %+v", end)
	}
	if len(events) != 1 || events[0].Kind != planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT {
		t.Fatalf("expected 1 TOOL_RESULT event, got %+v", events)
	}
	if events[0].Text != "# Anthropic Pricing\nClaude 3.5 Sonnet..." {
		t.Errorf("got result text %q", events[0].Text)
	}

	// Successful result: turn ends without error or failure.
	resultLine := `{"event":"result","result":{"conversation_id":"4326c014-f11e-4630-933f-bd6c2ec8b570","status":"SUCCESS","response":"ok","num_turns":1}}`
	events, end = parser.stdout(resultLine)
	if end == nil || end.failure != "" {
		t.Fatalf("expected successful turn end, got %+v", end)
	}
	for _, ev := range events {
		if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_ERROR {
			t.Errorf("unexpected error event: %+v", ev)
		}
	}

	// In contrast, when headless agy refused read_url (due to missing grant in W371):
	refusalParser := &agyParser{}
	refusalParser.stdout(stepActive)
	refusalResult := `{"event":"result","result":{"conversation_id":"4326c014-f11e-4630-933f-bd6c2ec8b570","status":"SUCCESS","num_turns":1,"denied_actions":[{"action":"read_url","display_name":"ReadUrlContent"}]}}`
	events, end = refusalParser.stdout(refusalResult)
	if end == nil || !strings.Contains(end.failure, `headless, it cannot ask for the "read_url" permission`) {
		t.Fatalf("expected refusal turn failure, got %+v", end)
	}
	hasError := false
	for _, ev := range events {
		if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_ERROR {
			hasError = true
		}
	}
	if !hasError {
		t.Errorf("expected ERROR event on refusal result")
	}
}

// TestAgyIdleLoop tests that Antigravity's idle pause message:
// 1) ends the turn as done if the work is committed and clean (W392).
// 2) continues once if work is not yet committed.
// 3) fails if the idle pause happens again.
// 4) is ignored if background tasks of its own are still running.
func TestAgyIdleLoop(t *testing.T) {
	stepDoneWithPause := `{"event":"step_update","step_update":{"conversation_id":"c1","step_index":1,"state":"DONE","step_type":"system","tool_info":{"output":"There are no tools called. Execution paused. Waiting for task completion."}}}`

	// Case 1: work is committed.
	pCommitted := &agyParser{isWorkDone: func() bool { return true }}
	events, end := pCommitted.stdout(stepDoneWithPause)
	if end == nil || end.failure != "" || end.retry != "" {
		t.Fatalf("expected clean turn end for committed work, got %+v", end)
	}
	hasDone := false
	for _, ev := range events {
		if strings.Contains(ev.Text, "idle pause with work committed: done") {
			hasDone = true
			break
		}
	}
	if !hasDone {
		t.Errorf("missing done event: %+v", events)
	}

	// Case 2: work not committed, first time: continue once.
	pUncommitted := &agyParser{isWorkDone: func() bool { return false }}
	events, end = pUncommitted.stdout(stepDoneWithPause)
	if end == nil || end.retry != agyContinuePrompt {
		t.Fatalf("expected continuation prompt %q, got %+v", agyContinuePrompt, end)
	}
	hasContinued := false
	for _, ev := range events {
		if strings.Contains(ev.Text, "continued:") {
			hasContinued = true
			break
		}
	}
	if !hasContinued {
		t.Errorf("missing continued event: %+v", events)
	}

	// Case 3: idle pause occurs again: fails with idle loop error.
	pUncommitted.turnEnded = false // next turn started
	_, end = pUncommitted.stdout(stepDoneWithPause)
	if end == nil || !strings.Contains(end.failure, "idle loop:") {
		t.Fatalf("expected idle loop failure, got %+v", end)
	}

	// Case 4: background tasks still running: not treated as idle loop.
	pWithBg := &agyParser{
		bgTasks: map[string]bool{"task-bg-123": true},
	}
	_, end = pWithBg.stdout(stepDoneWithPause)
	if end != nil {
		t.Fatalf("expected no turn end when background task is running, got %+v", end)
	}
}
