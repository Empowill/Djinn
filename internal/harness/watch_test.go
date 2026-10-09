package harness

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// fakeWatchArg, as the test binary's first argument, makes it play a watcher's command: each next argument is a
// step.
//
//	say:hello      it prints hello
//	err:oops       it prints oops on its error output
//	sleep:300ms    it waits
//	count:<file>   it counts its runs in file, and prints "run N"
//	input:<file>   it reads its input to the end, and adds a line to file: how many bytes it read
//	exit:2         it exits with this code
//	wait           it waits until it is stopped
//
// wait sleeps, it does not block on an empty select: in a test binary without cgo, as on macOS and Windows, the
// runtime sees every goroutine asleep and ends the process with exit code 2, and a watcher that restarts its command
// runs it again.
const fakeWatchArg = "djinn-fake-watch"

func fakeWatch(steps []string) int {
	for _, step := range steps {
		verb, arg, _ := strings.Cut(step, ":")
		switch verb {
		case "say":
			fmt.Println(arg)
		case "err":
			fmt.Fprintln(os.Stderr, arg)
		case "sleep":
			d, _ := time.ParseDuration(arg)
			time.Sleep(d)
		case "count":
			b, _ := os.ReadFile(arg)
			n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			n++
			_ = os.WriteFile(arg, []byte(strconv.Itoa(n)), 0o600)
			fmt.Printf("run %d\n", n)
		case "input":
			b, _ := io.ReadAll(os.Stdin)
			f, _ := os.OpenFile(arg, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			fmt.Fprintf(f, "read %d bytes\n", len(b))
			f.Close()
		case "exit":
			n, _ := strconv.Atoi(arg)
			return n
		case "wait":
			time.Sleep(time.Hour)
		}
	}
	return 0
}

// watchCommand is the command line that plays steps: the test binary, then the steps, each quoted.
func watchCommand(steps ...string) string {
	return "'" + os.Args[0] + "' " + fakeWatchArg + " '" + strings.Join(steps, "' '") + "'"
}

// watchEvents reads the worker's events until it ends.
func watchEvents(t *testing.T, w Worker) ([]Event, Result) {
	t.Helper()
	var out []Event
	done := time.After(20 * time.Second)
	for {
		select {
		case ev, ok := <-w.Events():
			if !ok {
				return out, w.Wait()
			}
			out = append(out, ev)
		case <-done:
			w.Stop()
			t.Fatal("the watcher did not end")
		}
	}
}

// TestWatchParagraphs: what the command prints, then a pause, is one paragraph; each wakes the lead, with its first
// line, but one that says again what the last one said; and the lead learns when the command has ended, whether its
// last paragraph came with its exit or before it (a slow machine).
func TestWatchParagraphs(t *testing.T) {
	t.Parallel()
	quiet := 100 * time.Millisecond
	t.Run("paragraphs", func(t *testing.T) {
		t.Parallel()
		cmd := watchCommand("say:CI \x1b[31mred\x1b[0m on !12", "sleep:10ms", "err:job lint", "sleep:300ms", "say:CI",
			"sleep:300ms", "say:CI", "say:merged")
		w, err := Watch{Quiet: quiet}.Start(t.Context(), Spec{Dir: t.TempDir(), Prompt: cmd})
		if err != nil {
			t.Fatal(err)
		}
		events, res := watchEvents(t, w)
		if res.ExitCode != 0 || res.Err != nil {
			t.Fatalf("result = %+v", res)
		}
		var got, told []string
		for _, ev := range events {
			if ev.Watched == nil {
				t.Fatalf("event %+v", ev)
			}
			if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_TEXT {
				got = append(got, fmt.Sprintf("%q first=%q wake=%v", ev.Text, ev.Watched.First, ev.Watched.Wake))
			}
			if ev.Watched.Wake {
				told = append(told, WatchLine("W1", ev.Watched))
			}
		}
		want := []string{
			`"CI red on !12\njob lint" first="CI red on !12" wake=true`,
			`"CI" first="CI" wake=true`,
			`"CI\nmerged" first="CI" wake=true`,
		}
		if !slices.Equal(got, want) {
			t.Errorf("paragraphs:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		if len(told) < 3 || !strings.HasSuffix(told[0], "is still watching.") || !strings.HasSuffix(told[len(told)-1], "has ended.") {
			t.Errorf("the lead was told %q", told)
		}
		if !strings.Contains(events[0].Raw, "\x1b[31m") {
			t.Errorf("the raw line lost its colours: %q", events[0].Raw)
		}
	})

	// The same paragraph again does not wake the lead; its end does.
	t.Run("again", func(t *testing.T) {
		t.Parallel()
		cmd := watchCommand("say:nothing new", "sleep:300ms", "say:nothing new")
		w, err := Watch{Quiet: quiet}.Start(t.Context(), Spec{Dir: t.TempDir(), Prompt: cmd})
		if err != nil {
			t.Fatal(err)
		}
		events, _ := watchEvents(t, w)
		if len(events) != 3 || !events[0].Watched.Wake || events[1].Watched.Wake || !events[2].Watched.Wake || events[2].Watched.First != "" {
			t.Errorf("a paragraph said twice: %+v", events)
		}
	})
}

// TestWatchRefuses: no command, a quote left open, read-only, or a command the project's permissions do not list.
func TestWatchRefuses(t *testing.T) {
	t.Parallel()
	for _, spec := range []Spec{
		{Prompt: "  "},
		{Prompt: `mrwatch "-watch`},
		{Prompt: "mrwatch -watch", ReadOnly: true},
		{Prompt: "mrwatch -watch", Permissions: editOnly()},
	} {
		spec.Dir = t.TempDir()
		if _, err := (Watch{}).Start(t.Context(), spec); err == nil {
			t.Errorf("%+v started", spec)
		}
	}
	p := editOnly()
	p.Commands = []string{"mrwatch"}
	w, err := Watch{}.Start(t.Context(), Spec{Dir: t.TempDir(), Prompt: watchCommand("exit:0"), Permissions: p})
	if err == nil {
		w.Stop()
		t.Error("a command the permissions do not list started")
	}
}

func TestSplitCommand(t *testing.T) {
	t.Parallel()
	for line, want := range map[string][]string{
		`mrwatch -watch 41`:                {"mrwatch", "-watch", "41"},
		`  a   "b c"  'd "e"' f\ g`:        {"a", "b c", `d "e"`, "f g"},
		`"x\"y" 'z\'`:                      {`x"y`, `z\`},
		`C:\Tools\mrwatch.exe -watch`:      {`C:\Tools\mrwatch.exe`, "-watch"},
		`echo "" end`:                      {"echo", "", "end"},
		`mrwatch && rm -rf / ; echo $HOME`: {"mrwatch", "&&", "rm", "-rf", "/", ";", "echo", "$HOME"},
	} {
		got, err := splitCommand(line)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("splitCommand(%s) = %q, %v; want %q", line, got, err, want)
		}
	}
}

func TestWatchLine(t *testing.T) {
	t.Parallel()
	w := &Watched{First: "New thread on !41: please rename.", Command: "mrwatch -watch", Watching: true}
	if got, want := WatchLine("W70", w), "Djinn: W70's watcher says: New thread on !41: please rename. mrwatch -watch is still watching."; got != want {
		t.Errorf("line = %q\nwant %q", got, want)
	}
	if got := WatchLine("W70", &Watched{Command: "mrwatch -watch"}); got != "Djinn: W70's watcher: mrwatch -watch has ended." {
		t.Errorf("end line = %q", got)
	}
	w.Watching, w.First = false, strings.Repeat("\u00e9", 300)
	got := WatchLine("W70", w)
	if !strings.HasSuffix(got, "… mrwatch -watch has ended.") || strings.Count(got, "\u00e9") != watchLineMax-1 {
		t.Errorf("line = %q", got)
	}
	long := strings.Repeat("x", 300)
	if got := WatchLine("W70", &Watched{First: "ok", Command: long, Watching: true}); got !=
		"Djinn: W70's watcher says: ok. "+long[:watchCommandMax-1]+"… is still watching." {
		t.Errorf("line of a long command = %q", got)
	}
	if got := cleanLine("\x1b]0;title\x07\x1b[1;32mok\x1b[0m\tdone\x00\r"); got != "ok done" {
		t.Errorf("cleanLine = %q", got)
	}
}

// told records the lines the watchers tell the leads.
type told struct {
	mu    sync.Mutex
	lines []string
}

func (l *told) tell(_ context.Context, wishID, line string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, wishID+": "+line)
	return nil
}

func (l *told) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.lines)
}

// spawnWatch spawns a watcher of the wish on command.
func (e *env) spawnWatch(t *testing.T, wishID, command string, restart bool) (*planv1.Task, error) {
	t.Helper()
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Watch the MR", Prompt: command, Provider: planv1.Provider_PROVIDER_WATCH, Restart: restart,
	}))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetTask(), nil
}

// TestWatcher: a watcher starts on a full machine under pressure, takes no slot, runs its command again after each
// exit, tells the wish's lead each paragraph in one line, keeps the last one on its task, pauses, and stops.
func TestWatcher(t *testing.T) {
	t.Parallel()
	l := &limit{slots: 1, pressure: "simulated"}
	e := up(t, t.TempDir(), WithCapacity(l.capacity), WithTick(time.Hour))
	lead := &told{}
	e.h.TellLeads(lead.tell)
	repo := gitRepo(t)
	wishID, _ := e.wish(t, repo)
	counter := filepath.Join(t.TempDir(), "runs")
	cmd := watchCommand("count:"+counter, "exit:0")

	task, err := e.spawnWatch(t, wishID, cmd, true)
	if err != nil {
		t.Fatal(err)
	}
	if task.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || task.GetWorktree() != "" || task.GetBranch() != "" || !task.GetRestart() {
		t.Fatalf("watcher = %v", task)
	}
	task = e.until(t, task.GetId(), func(t *planv1.Task) bool { return t.GetLastLine() == "run 3" })
	if n := e.h.Running(); n != 0 {
		t.Errorf("Running() = %d with a watcher", n)
	}
	if task.GetUsage().GetCostUsd() != 0 || task.GetSessionId() != "" {
		t.Errorf("a watcher spent or has a session: %v", task)
	}
	lines := lead.all()
	if len(lines) < 3 {
		t.Fatalf("the lead was told %q", lines)
	}
	// The command is clipped in the line: the test binary's path is long on macOS and Windows.
	want := wishID + ": Djinn: W1's watcher says: run 1. " + clipRunes(cmd, watchCommandMax) + " is still watching."
	if lines[0] != want {
		t.Errorf("the lead was told %q\nwant %q", lines[0], want)
	}

	// The slot is free: once the pressure falls, an agent starts next to it.
	l.set(1, "")
	agent := e.mustSpawn(t, wishID, "Work", "text ok", nil)
	if agent.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("the agent waits: %v", agent)
	}

	// Paused, it holds still; resumed, it goes on. Windows cannot pause a worker yet (pause_test.go).
	if runtime.GOOS != "windows" {
		if _, err := e.tasks.Pause(t.Context(), connect.NewRequest(&planv1.TaskServicePauseRequest{TaskId: task.GetId()})); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond) // Runs start 50 ms apart (fastWatch): none while paused.
		paused := e.get(t, task.GetId())
		time.Sleep(200 * time.Millisecond)
		if again := e.get(t, task.GetId()); paused.GetStatus() != planv1.TaskStatus_TASK_STATUS_PAUSED || again.GetLastLine() != paused.GetLastLine() {
			t.Errorf("paused, it went on: %q then %q", paused.GetLastLine(), again.GetLastLine())
		}
		if _, err := e.tasks.Resume(t.Context(), connect.NewRequest(&planv1.TaskServiceResumeRequest{TaskId: task.GetId()})); err != nil {
			t.Fatal(err)
		}
		e.until(t, task.GetId(), func(t *planv1.Task) bool { return t.GetLastLine() != paused.GetLastLine() })
	}

	// A message is refused: no model reads it.
	if _, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{TaskId: task.GetId(), Text: "hi"})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a message to a watcher: %v", err)
	}

	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, task.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED {
		t.Errorf("stopped watcher = %v", got)
	}
	e.ended(t, agent.GetId())
}

// TestWatcherEnds: without --restart, the watcher ends with its command, and the lead learns it has ended; a failing
// command fails the task. --restart is for a watcher only, and a watcher needs a project.
func TestWatcherEnds(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir(), WithTick(time.Hour))
	lead := &told{}
	e.h.TellLeads(lead.tell)
	wishID, _ := e.wish(t, gitRepo(t))

	task, err := e.spawnWatch(t, wishID, watchCommand("say:merged"), false)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.ended(t, task.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetLastLine() != "merged" {
		t.Errorf("watcher = %v", got)
	}
	// "merged" came with its exit, or before it on a slow machine: either way the lead learns the end.
	if lines := lead.all(); len(lines) == 0 || !strings.Contains(lines[0], " says: merged. ") ||
		!strings.HasSuffix(lines[len(lines)-1], " has ended.") {
		t.Errorf("the lead was told %q", lines)
	}

	task, err = e.spawnWatch(t, wishID, watchCommand("err:no token", "exit:3"), false)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.ended(t, task.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED || got.GetError() != "exit code 3" {
		t.Errorf("failing watcher = %v", got)
	}

	if _, err := e.spawnReq(t, wishID, "Agent", "text hi", &planv1.TaskServiceSpawnRequest{Restart: true}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("--restart on an agent: %v", err)
	}
	bare, err := e.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Nowhere"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.spawnWatch(t, bare.Msg.GetWish().GetId(), watchCommand("say:x"), false); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a watcher outside any project: %v", err)
	}
}

// TestWatcherResumes: a watcher djinn up cut short is resumed on its command at the next start, however many times,
// in its project's folder, on a full machine under pressure.
func TestWatcherResumes(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	l := &limit{slots: 0, pressure: "simulated"}
	e := up(t, home, WithTick(20*time.Millisecond), WithCapacity(l.capacity))
	wishID, _ := e.wish(t, gitRepo(t))
	counter := filepath.Join(t.TempDir(), "runs")
	task, err := e.spawnWatch(t, wishID, watchCommand("count:"+counter, "wait"), true)
	if err != nil {
		t.Fatal(err)
	}
	e.until(t, task.GetId(), func(t *planv1.Task) bool { return t.GetLastLine() == "run 1" })
	for i := 2; i <= maxResumes+2; i++ {
		e.down()
		e = up(t, home, WithTick(20*time.Millisecond), WithCapacity(l.capacity))
		got := e.until(t, task.GetId(), func(t *planv1.Task) bool { return t.GetLastLine() == fmt.Sprintf("run %d", i) })
		if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || got.GetWorktree() != "" {
			t.Fatalf("after restart %d: %v", i-1, got)
		}
	}
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
		t.Fatal(err)
	}
}

// TestWatcherPermissions: where the project lists its commands, a watcher runs only one of them.
func TestWatcherPermissions(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir(), WithTick(time.Hour))
	repo := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, PermissionsFile), []byte(`commands: "mrwatch"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wishID, _ := e.wish(t, repo)
	if _, err := e.spawnWatch(t, wishID, watchCommand("say:x"), false); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "no reviewer") {
		t.Errorf("an unlisted command: %v", err)
	}
}

// TestWatcherDoneLine: the watcher of a wish made from a template starts at once in the skill's project; each new
// paragraph reaches plan.Wishes.Watched, and its done line asks the developer whether to grant the wish. Djinn
// grants nothing itself.
func TestWatcherDoneLine(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir(), WithTick(time.Hour))
	wishes := &plan.Wishes{Store: e.db, Language: "en"}
	var mu sync.Mutex
	var paragraphs []string
	e.h.OnWatched(func(ctx context.Context, task *planv1.Task, text string) (bool, error) {
		mu.Lock()
		paragraphs = append(paragraphs, task.GetCode()+": "+text)
		mu.Unlock()
		return wishes.Watched(ctx, task, text)
	})
	wishID, projectID := e.wish(t, gitRepo(t))
	err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
		wish, err := store.Get[*planv1.Wish](t.Context(), tx, wishID)
		if err != nil {
			return err
		}
		wish.Template = &planv1.WishTemplate{Skill: "babysit", ProjectId: projectID, DoneWhen: "MERGED"}
		if err := tx.Journal("test", "test/template", wish); err != nil {
			return err
		}
		return tx.Put(wish)
	})
	if err != nil {
		t.Fatal(err)
	}

	code, err := e.h.SpawnWatcher(t.Context(), wishID, projectID, "Watch, for the skill babysit",
		watchCommand("say:checks pending", "sleep:300ms", "say:MERGED: PR #12 is merged."), false)
	if err != nil || code != "W1" {
		t.Fatalf("SpawnWatcher = %q, %v", code, err)
	}
	tasks, err := store.List[*planv1.Task](t.Context(), e.db, store.Where{"wish_id": wishID})
	if err != nil || len(tasks) != 1 || tasks[0].GetProvider() != planv1.Provider_PROVIDER_WATCH || tasks[0].GetProjectId() != projectID {
		t.Fatalf("tasks = %v, %v", tasks, err)
	}
	e.ended(t, tasks[0].GetId())
	mu.Lock()
	got := slices.Clone(paragraphs)
	mu.Unlock()
	if want := []string{"W1: checks pending", "W1: MERGED: PR #12 is merged."}; !slices.Equal(got, want) {
		t.Errorf("paragraphs %q, want %q", got, want)
	}
	qs, err := store.List[*planv1.Question](t.Context(), e.db, store.Where{"wish_id": wishID})
	if err != nil || len(qs) != 1 || !qs[0].GetGrant() || qs[0].GetAnswer() != nil {
		t.Fatalf("questions = %v, %v", qs, err)
	}
	if wish, _ := store.Get[*planv1.Wish](t.Context(), e.db, wishID); wish.GetState() == planv1.WishState_WISH_STATE_GRANTED {
		t.Errorf("Djinn granted the wish by itself")
	}
}

// TestWatcherFinishes: a watcher that restarts its command ends, done, on its template's done line; one stopped on
// request still ends stopped.
func TestWatcherFinishes(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir(), WithTick(time.Hour))
	e.h.OnWatched(func(_ context.Context, _ *planv1.Task, text string) (bool, error) {
		return strings.Contains(text, "MERGED"), nil
	})
	wishID, projectID := e.wish(t, gitRepo(t))
	counter := filepath.Join(t.TempDir(), "runs")
	if _, err := e.h.SpawnWatcher(t.Context(), wishID, projectID, "Watch", watchCommand("count:"+counter, "say:MERGED", "exit:0"), true); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.List[*planv1.Task](t.Context(), e.db, store.Where{"wish_id": wishID})
	if err != nil || len(tasks) != 1 || !tasks[0].GetRestart() {
		t.Fatalf("tasks = %v, %v", tasks, err)
	}
	if got := e.ended(t, tasks[0].GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetError() != "" {
		t.Errorf("finished watcher = %v", got)
	}

	task, err := e.spawnWatch(t, wishID, watchCommand("say:checks pending", "wait"), true)
	if err != nil {
		t.Fatal(err)
	}
	e.until(t, task.GetId(), func(t *planv1.Task) bool { return t.GetLastLine() == "checks pending" })
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, task.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED {
		t.Errorf("stopped watcher = %v", got)
	}
}
