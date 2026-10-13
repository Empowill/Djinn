package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// allowAuto gives the wish the auto allowance in its project: its workers' permissions list the project's commands,
// none without a permissions file.
func allowAuto(t *testing.T, e *env, wishID string) {
	t.Helper()
	if _, err := e.wishes.Allow(t.Context(), connect.NewRequest(&planv1.WishServiceAllowRequest{
		WishId: wishID, Mode: planv1.Allowance_ALLOWANCE_AUTO,
	})); err != nil {
		t.Fatal(err)
	}
}

// spawnWatchIn spawns a watcher on command in the wish's project.
func spawnWatchIn(t *testing.T, e *env, wishID, projectID, command string) (*planv1.Task, error) {
	t.Helper()
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, ProjectId: projectID, Title: "Watch it", Prompt: command, Provider: planv1.Provider_PROVIDER_WATCH,
		Restart: true,
	}))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetTask(), nil
}

// startedText is the text of the task's first event that says its worker started.
func startedText(t *testing.T, e *env, id string) string {
	t.Helper()
	events, err := store.List[*planv1.TaskEvent](t.Context(), e.db, store.Where{"task_id": id})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if strings.HasPrefix(ev.GetText(), "started watch ") {
			return ev.GetText()
		}
	}
	return ""
}

// stopTask stops the task, and waits for its end.
func stopTask(t *testing.T, e *env, id string) {
	t.Helper()
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: id})); err != nil {
		t.Fatal(err)
	}
	e.ended(t, id)
}

// projectName is the name Djinn gave the project.
func projectName(t *testing.T, e *env, projectID string) string {
	t.Helper()
	p, err := store.Get[*planv1.Project](t.Context(), e.db, projectID)
	if err != nil {
		t.Fatal(err)
	}
	return p.GetName()
}

// TestWatchDeclaredBabysit: in a project without .agents/permissions.txtpb, under a wish's auto allowance that lists
// no command, the babysit watcher Djinn starts on what a task opened runs: its command is the one the project's skill
// declares, as Djinn fills it. A watcher typed by hand on the same command is refused, and so is a --babysit whose
// own command merely starts like the declared one.
func TestWatchDeclaredBabysit(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	const url = "https://gitlab.com/acme/dolmen/-/merge_requests/123"
	forge := &fakeForge{
		views: map[string]string{"123": `{"iid":123,"state":"opened","web_url":"` + url + `"}`},
		lists: map[string]string{"": `[{"iid":123,"web_url":"` + url + `"}]`},
	}
	e, _, wishID, projectID := babysitWish(t, forge, babysitRepo(t, "git@gitlab.com:acme/dolmen.git", gitlabBabysit, "babysit: false\n"))
	allowAuto(t, e, wishID)

	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Babysit !123", Babysit: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	w := e.get(t, res.Msg.GetTask().GetId())
	if w.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || w.GetBabysit().GetNumber() != 123 {
		t.Fatalf("the declared babysit: %s %q, babysit %v; want running on !123", w.GetStatus(), w.GetError(), w.GetBabysit())
	}
	if w.GetAccess() != planv1.TaskAccess_TASK_ACCESS_WISH_AUTO {
		t.Errorf("access %s, want the wish's auto allowance", w.GetAccess())
	}
	if text := startedText(t, e, w.GetId()); !strings.HasSuffix(text, "; its command is the one the skill babysit-it declares") {
		t.Errorf("start event %q, want it to say the skill declares the command", text)
	}
	stopTask(t, e, w.GetId())

	state := filepath.Join(e.home, "watchers", w.GetId(), "babysit-it-123")
	for _, command := range []string{
		"sh .agents/skills/babysit-it/watch.sh 123 " + state, // the same command, by hand: no request found by Djinn
		"sh .agents/skills/babysit-it/watch.sh 123",
	} {
		if _, err := spawnWatchIn(t, e, wishID, projectID, command); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
			!strings.Contains(err.Error(), "a watcher has no reviewer") {
			t.Errorf("by hand %q: %v, want refused", command, err)
		}
	}
	_, err = e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Babysit !123 and more", Babysit: true,
		Prompt: "sh .agents/skills/babysit-it/watch.sh {mr} {state} --and-more",
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "this command is not one of them") {
		t.Errorf("--babysit on a command that starts like the declared one: %v, want refused", err)
	}
}

// TestWatchDeclaredTemplate: the watcher of a wish made from the template of a skill of the project runs its command
// without a permissions file; another command in that wish does not.
func TestWatchDeclaredTemplate(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	repo := gitRepoWith(t, map[string]string{".agents/skills/watch-it/SKILL.md": "---\nname: watch-it\ndescription: Watch.\n" +
		"metadata:\n  djinn:\n    wish:\n      title: \"Watch {what}\"\n      match: 'watch (?P<what>.+)'\n" +
		"      watch: \"sh -c 'sleep 30' {what}\"\n---\n"})
	wishID, projectID := e.wish(t, repo)
	allowAuto(t, e, wishID)
	tmpl, err := plan.ReadTemplate(filepath.Join(repo, ".agents", "skills", "watch-it"))
	if err != nil {
		t.Fatal(err)
	}
	_, watch, ok := tmpl.Fill("watch the queue")
	if !ok || watch != "sh -c 'sleep 30' 'the queue'" {
		t.Fatalf("Fill = %q, %v", watch, ok)
	}
	err = e.db.Tx(t.Context(), func(tx *store.Tx) error {
		wish, err := store.Get[*planv1.Wish](t.Context(), tx, wishID)
		if err != nil {
			return err
		}
		wish.Template = &planv1.WishTemplate{Skill: "watch-it", ProjectId: projectID, Watch: watch}
		if err := tx.Journal("test", "test/template", wish); err != nil {
			return err
		}
		return tx.Put(wish)
	})
	if err != nil {
		t.Fatal(err)
	}
	code, err := e.h.SpawnWatcher(t.Context(), wishID, projectID, "Watch, for the skill watch-it", watch, true)
	if err != nil {
		t.Fatalf("the template's watcher: %v", err)
	}
	tasks, err := store.List[*planv1.Task](t.Context(), e.db, store.Where{"wish_id": wishID})
	if err != nil || len(tasks) != 1 || tasks[0].GetCode() != code {
		t.Fatalf("tasks %v, %v", tasks, err)
	}
	if text := startedText(t, e, tasks[0].GetId()); !strings.HasSuffix(text, "the skill watch-it declares") {
		t.Errorf("start event %q", text)
	}
	stopTask(t, e, tasks[0].GetId())
	if _, err := e.h.SpawnWatcher(t.Context(), wishID, projectID, "Watch more", watch+" more", true); err == nil {
		t.Error("a command that adds a word to the template's started")
	}
}

// TestWatchPersonalAllowance: a command the developer allows in the project, or its start, runs in a watcher typed by
// hand; their file in Djinn's data folder says so, the repository is untouched. Taken back, it is refused again. The
// project's denied commands win over it.
func TestWatchPersonalAllowance(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	repo := gitRepo(t)
	wishID, projectID := e.wish(t, repo)
	allowAuto(t, e, wishID)
	command := "sh -c 'sleep 30' --label W6"
	if _, err := spawnWatchIn(t, e, wishID, projectID, command); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("before the allowance: %v, want refused", err)
	}
	if _, err := plan.AllowWatch(e.home, projectID, "sh  -c", false); err != nil {
		t.Fatal(err)
	}
	w, err := spawnWatchIn(t, e, wishID, projectID, command)
	if err != nil {
		t.Fatalf("allowed for themselves: %v", err)
	}
	if text := startedText(t, e, w.GetId()); !strings.Contains(text, "allowed by your own settings of "+projectName(t, e, projectID)) {
		t.Errorf("start event %q", text)
	}
	stopTask(t, e, w.GetId())
	if out, _ := git(t.Context(), repo, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Errorf("the repository changed: %s", out)
	}
	if _, err := os.Stat(plan.DeveloperSettingsFile(e.home, projectID)); err != nil {
		t.Errorf("the developer's file: %v", err)
	}

	if _, err := spawnWatchIn(t, e, wishID, projectID, "sh -cx 'sleep 30'"); err == nil {
		t.Error("a command that starts with the allowed one in the middle of a word started")
	}
	if _, err := plan.AllowWatch(e.home, projectID, "sh -c", true); err != nil {
		t.Fatal(err)
	}
	if _, err := spawnWatchIn(t, e, wishID, projectID, command); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("taken back: %v, want refused", err)
	}

	if _, err := plan.AllowWatch(e.home, projectID, "sh -c", false); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, ".agents/permissions.txtpb", "commands: \"ls\"\ndenied_commands: \"sh -c\"\n")
	_, err = spawnWatchIn(t, e, wishID, projectID, command)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "denies") {
		t.Errorf("denied by the project: %v, want refused", err)
	}
}

// TestWatchRefusal: the refusal says why, which skill declarations Djinn would run, and the exact command that allows
// it for the developer alone.
func TestWatchRefusal(t *testing.T) {
	t.Parallel()
	plain := t.TempDir()
	declares := gitRepoWith(t, map[string]string{".agents/skills/babysit-it/SKILL.md": "---\nname: babysit-it\n" +
		"description: Babysit.\nmetadata:\n  djinn:\n    babysit:\n      watch: \"sh watch.sh {mr}\"\n---\n"})
	listed := gitRepoWith(t, map[string]string{".agents/permissions.txtpb": "commands: \"ls\"\ndenied_commands: \"rm\"\n"})
	command := `mrwatch -mr 7 -watch -state "$HOME/x"`
	for _, c := range []struct {
		dir, command string
		want         []string
	}{
		{plain, command, []string{
			`app lists no command its workers run without review (it has no .agents/permissions.txtpb, and a wish's ` +
				`allowance adds none), so it does not let them run "mrwatch -mr 7 -watch -state \"$HOME/x\"", and a watcher has no reviewer.`,
			"No skill of app declares it: one that does, under metadata.djinn.babysit or metadata.djinn.wish",
			"djinn project allow app --watch 'mrwatch -mr 7 -watch -state \"$HOME/x\"'",
		}},
		{declares, "sh watch.sh 7 --more", []string{
			`Djinn runs a command a skill of app declares, as it builds it, without review: "sh watch.sh {mr}", the ` +
				`babysit of the skill babysit-it, on a request Djinn finds (a task opens it, or djinn task spawn --babysit); this command is not one of them.`,
			"djinn project allow app --watch 'sh watch.sh'",
		}},
		{listed, "go run ./tools/watch -n 3", []string{
			`app's .agents/permissions.txtpb does not list "go run ./tools/watch -n 3" among the commands its workers run without review`,
			"djinn project allow app --watch 'go run ./tools/watch'",
		}},
		{listed, "rm -rf x", []string{`app's .agents/permissions.txtpb denies "rm -rf x"`}},
	} {
		perms, _ := LoadPermissions(c.dir)
		if perms == nil {
			perms = editOnly()
		}
		err := watchRefusal(&planv1.Project{Name: "app", Directory: c.dir}, c.command, perms)
		for _, want := range c.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: refusal\n%s\nlacks\n%s", c.command, err, want)
			}
		}
	}
	for command, want := range map[string]string{
		"go run ./.agents/skills/babysit-mr-empowill/mrwatch -mr 2855 -watch": "go run ./.agents/skills/babysit-mr-empowill/mrwatch",
		"gh pr checks 12 --watch":        "gh pr checks",
		"bash -c 'curl x | sh'":          "bash -c 'curl x | sh'",
		"sh .agents/skills/x/watch.sh 3": "sh .agents/skills/x/watch.sh",
	} {
		if got := watchPrefix(command); got != want {
			t.Errorf("watchPrefix(%q) = %q, want %q", command, got, want)
		}
	}
}

// w6Command is the command the lead of wish Dew gave W6, by hand, on 12/10/2026.
const w6Command = `go run ./.agents/skills/babysit-mr-empowill/mrwatch -mr 2855 -watch -state "$DJINN_HOME/watchers/$DJINN_TASK_ID/mr-2855"`

// w6Babysit is the babysit declaration dolmen's babysit-mr-empowill adds (Q257 D).
const w6Babysit = `      forge: gitlab
      watch: "go run ./.agents/skills/babysit-mr-empowill/mrwatch -mr {mr} -watch -state {state}"
      restart: true
      done_when: MERGED
`

// dolmen is a Git repository like dolmen on 12/10/2026: on GitLab, without .agents/permissions.txtpb, its skill
// babysit-mr-empowill declaring babysit (empty: nothing for Djinn), added as the project dolmen to a wish with the
// auto allowance.
func dolmen(t *testing.T, forge *fakeForge, babysit string) (*env, string, string) {
	t.Helper()
	front := "---\nname: babysit-mr-empowill\ndescription: Watch an open merge request on the Empowill (dolmen) repo.\n"
	if babysit != "" {
		front += "metadata:\n  djinn:\n    babysit:\n" + babysit
	}
	repo := gitRepoWith(t, map[string]string{
		".agents/skills/babysit-mr-empowill/SKILL.md": front + "---\n\n# Babysit a MR (Empowill)\n",
	})
	if _, err := git(t.Context(), repo, "remote", "add", "origin", "git@gitlab.com:empowill/dolmen.git"); err != nil {
		t.Fatal(err)
	}
	e := up(t, t.TempDir(), WithCommands(forge.run))
	p, err := e.project.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: repo, Name: "dolmen"}))
	if err != nil {
		t.Fatal(err)
	}
	w, err := e.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Dew", ProjectIds: []string{p.Msg.GetProject().GetId()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	allowAuto(t, e, w.Msg.GetWish().GetId())
	return e, w.Msg.GetWish().GetId(), p.Msg.GetProject().GetId()
}

// TestW6BabysitWatcherInDolmen replays W6 of wish Dew (12/10/2026): in dolmen, without a permissions file, under the
// wish's auto allowance, the lead spawned a watcher on mrwatch for MR !2855 by hand; it failed at start, blaming a
// permissions file dolmen does not have, and no watcher could ever run there. Now the refusal says what dolmen lacks
// and gives the command that allows it; once the developer runs it, the same watcher starts. And once dolmen's skill
// declares its babysit, the watcher Djinn starts on the MR a task opens runs without any allowance.
func TestW6BabysitWatcherInDolmen(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	const url = "https://gitlab.com/empowill/dolmen/-/merge_requests/2855"
	forge := &fakeForge{views: map[string]string{"2855": `{"iid":2855,"state":"opened","web_url":"` + url + `"}`}}

	e, wishID, projectID := dolmen(t, forge, "")
	_, err := spawnWatchIn(t, e, wishID, projectID, w6Command)
	allow := "djinn project allow dolmen --watch 'go run ./.agents/skills/babysit-mr-empowill/mrwatch'"
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "dolmen lists no command") ||
		!strings.Contains(err.Error(), "it has no .agents/permissions.txtpb") || !strings.Contains(err.Error(), allow) {
		t.Fatalf("W6 by hand: %v\nwant the refusal to say dolmen has no permissions file, and %s", err, allow)
	}
	tasks, _ := store.List[*planv1.Task](t.Context(), e.db, store.Where{"wish_id": wishID})
	if len(tasks) != 1 || tasks[0].GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED ||
		strings.Contains(tasks[0].GetError(), "does not let its workers run") {
		t.Fatalf("W6: %v, want failed with the new refusal", tasks)
	}
	// The developer runs the command the refusal gives.
	if _, err := plan.AllowWatch(e.home, projectID, "go run ./.agents/skills/babysit-mr-empowill/mrwatch", false); err != nil {
		t.Fatal(err)
	}
	w, err := spawnWatchIn(t, e, wishID, projectID, w6Command)
	if err != nil {
		t.Fatalf("W6 once allowed: %v", err)
	}
	stopTask(t, e, w.GetId())

	// dolmen declares its babysit: a task opens !2855, and Djinn's watcher on it runs, without any allowance.
	e, wishID, projectID = dolmen(t, forge, w6Babysit)
	opener := endTask(t, e, wishID, projectID, strings.Join([]string{
		`tool Bash {"command":"glab mr create --draft --fill --yes"}`, "result " + url, "text Draft MR opened.",
	}, "\n"))
	if opener.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("the opener: %s %s", opener.GetStatus(), opener.GetError())
	}
	got := watchers(t, e, wishID)
	if len(got) != 1 {
		t.Fatalf("watchers %v, want the babysit of !2855", got)
	}
	if got[0].GetStatus() == planv1.TaskStatus_TASK_STATUS_FAILED || got[0].GetBabysit().GetNumber() != 2855 {
		t.Fatalf("the babysit: %s %q, babysit %v", got[0].GetStatus(), got[0].GetError(), got[0].GetBabysit())
	}
	state := filepath.Join(e.home, "watchers", got[0].GetId(), "babysit-mr-empowill-2855")
	prompt, _ := lastPrompt(t.Context(), e.db, got[0].GetId())
	if want := "go run ./.agents/skills/babysit-mr-empowill/mrwatch -mr 2855 -watch -state " + state; prompt != want {
		t.Errorf("command %q, want %q", prompt, want)
	}
	if dev, err := plan.ReadSettings(plan.DeveloperSettingsFile(e.home, projectID)); dev != nil || err != nil {
		t.Errorf("the developer's file: %v, %v; want none", dev, err)
	}
	stopTask(t, e, got[0].GetId())
}

// TestExpandWatch: Djinn's variables in a watcher's command line are expanded as a shell would, outside single
// quotes, each value one word whatever it holds; any other variable is refused, and the rest is kept as it is.
func TestExpandWatch(t *testing.T) {
	t.Parallel()
	vars := func(home string) map[string]string {
		return map[string]string{"DJINN_HOME": home, "DJINN_TASK_ID": "01a1", "DJINN_WISH_ID": "01b2"}
	}
	const linux, mac, windows = "/home/u/.config/djinn", "/Users/u/Library/Application Support/djinn", `C:\Users\u\AppData\Roaming\djinn`
	for _, c := range []struct {
		home, line, want string
		words            []string
	}{
		{linux, w6Command,
			`go run ./.agents/skills/babysit-mr-empowill/mrwatch -mr 2855 -watch -state "/home/u/.config/djinn/watchers/01a1/mr-2855"`,
			[]string{"go", "run", "./.agents/skills/babysit-mr-empowill/mrwatch", "-mr", "2855", "-watch", "-state",
				"/home/u/.config/djinn/watchers/01a1/mr-2855"}},
		{linux, `w ${DJINN_TASK_ID}-x $DJINN_WISH_ID '$DJINN_HOME' \$DJINN_HOME $ $1 "a$" "${DJINN_HOME"`,
			`w 01a1-x 01b2 '$DJINN_HOME' \$DJINN_HOME $ $1 "a$" "${DJINN_HOME"`,
			[]string{"w", "01a1-x", "01b2", "$DJINN_HOME", `\$DJINN_HOME`, "$", "$1", "a$", "${DJINN_HOME"}},
		{mac, `w "$DJINN_HOME/w" $DJINN_HOME/w`,
			`w "/Users/u/Library/Application Support/djinn/w" '/Users/u/Library/Application Support/djinn'/w`,
			[]string{"w", mac + "/w", mac + "/w"}},
		{windows, `w "$DJINN_HOME\w" $DJINN_HOME\w`, `w "C:\Users\u\AppData\Roaming\djinn\w" C:\Users\u\AppData\Roaming\djinn\w`,
			[]string{"w", windows + `\w`, windows + `\w`}},
		{`C:\it's "x"\`, `w "$DJINN_HOME" $DJINN_HOME`, `w ""'C:\it'\''s "x"\'"" 'C:\it'\''s "x"\'`,
			[]string{"w", `C:\it's "x"\`, `C:\it's "x"\`}},
	} {
		got, err := expandWatch(c.line, vars(c.home))
		if err != nil || got != c.want {
			t.Errorf("expandWatch(%s) = %s, %v\nwant %s", c.line, got, err, c.want)
			continue
		}
		if words, err := splitCommand(got); err != nil || !slices.Equal(words, c.words) {
			t.Errorf("splitCommand(%s) = %q, %v\nwant %q", got, words, err, c.words)
		}
	}
	for _, line := range []string{`mrwatch -state "$HOME/x"`, `mrwatch -state ${HOME}/x`, `w $DJINN_TASK`} {
		_, err := expandWatch(line, vars(linux))
		if err == nil || !strings.Contains(err.Error(), "without a shell") ||
			!strings.Contains(err.Error(), "$DJINN_HOME, $DJINN_TASK_ID, $DJINN_WISH_ID") ||
			!strings.Contains(err.Error(), "single quotes") {
			t.Errorf("expandWatch(%s): %v, want refused", line, err)
		}
	}
}

// TestW7WatcherCommandAsALeadWritesIt replays W7 of wish Dew (13/10/2026): the lead spawned a watcher on mrwatch with
// -state "$DJINN_HOME/watchers/$DJINN_TASK_ID/mr-2855", as W5 was told to; without a shell, the variables reached
// mrwatch literally, it could not open its state file, and it died at each start. Now Djinn expands them, the
// watcher's folder is made before its command starts, and the command that runs is the one its allowance judges:
// the developer's watch_commands, then the project's denied_commands. Any other variable is refused at spawn.
func TestW7WatcherCommandAsALeadWritesIt(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	repo := gitRepo(t)
	wishID, projectID := e.wish(t, repo)
	allowAuto(t, e, wishID)
	program := "'" + os.Args[0] + "' " + fakeWatchArg
	command := program + ` "remember:$DJINN_HOME/watchers/$DJINN_TASK_ID/mr-2855" wait`

	_, err := spawnWatchIn(t, e, wishID, projectID, command)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || strings.Contains(err.Error(), "$DJINN_HOME") {
		t.Fatalf("before the allowance: %v, want refused on the command that runs", err)
	}
	if _, err := plan.AllowWatch(e.home, projectID, program, false); err != nil {
		t.Fatal(err)
	}
	w, err := spawnWatchIn(t, e, wishID, projectID, command)
	if err != nil {
		t.Fatalf("W7 once allowed: %v", err)
	}
	state := e.home + "/watchers/" + w.GetId() + "/mr-2855"
	got := e.until(t, w.GetId(), func(t *planv1.Task) bool {
		return t.GetLastLine() != "" || t.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING
	})
	if got.GetLastLine() != "remembered "+state {
		t.Fatalf("W7: %s %q, last line %q; want %q", got.GetStatus(), got.GetError(), got.GetLastLine(), "remembered "+state)
	}
	if _, err := os.Stat(state); err != nil {
		t.Errorf("the state file: %v", err)
	}
	stopTask(t, e, w.GetId())
	if prompt, _ := lastPrompt(t.Context(), e.db, w.GetId()); prompt != command {
		t.Errorf("the task's prompt %q, want the lead's %q", prompt, command)
	}

	_, err = spawnWatchIn(t, e, wishID, projectID, program+` "remember:$HOME/mr-2855" wait`)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "holds $HOME") {
		t.Errorf("another variable: %v, want refused", err)
	}

	// The project lists mrwatch, and denies it on this wish once expanded: the variable does not hide it.
	writeFile(t, repo, ".agents/permissions.txtpb", "commands: \"mrwatch\"\ndenied_commands: \"mrwatch -wish "+wishID+"\"\n")
	_, err = spawnWatchIn(t, e, wishID, projectID, "mrwatch -wish $DJINN_WISH_ID -watch")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "denies") {
		t.Errorf("denied once expanded: %v, want refused", err)
	}
}
