package gate

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/internal/machine"
)

// TestPlain: a command that only reads or edits passes with no admission; any other, or one the hook cannot read
// through, is admitted.
func TestPlain(t *testing.T) {
	t.Parallel()
	for command, want := range map[string]bool{
		"ls -la":                                true,
		"rg -n 'foo|bar' internal/ | head -20":  true,
		"cat a.txt && sed -i 's/a/b/' x.go":     true,
		"git status --short":                    true,
		"git -C /src log --oneline -5":          true,
		"git diff main...HEAD -- internal/gate": true,
		"djinn task get W12":                    true,
		"/home/u/go/bin/djinn question list":    true,
		"export PATH=/x:$PATH; djinn gate run lint -- go tool task lint": true,
		"FOO=1 ls":                             true,
		"time wc -l *.go":                      true,
		"env":                                  true,
		"ls 2>&1 | grep x > out.txt":           true,
		"find . -name '*.go' | wc -l":          true,
		"echo 'a;b' # go test":                 true,
		"mkdir -p a/b && cp x a/b/ && rm -f y": true,
		"go test ./...":                        false,
		"cd internal && go build ./...":        false,
		"npm ci":                               false,
		"go tool task lint":                    false,
		"git commit -m 'gate: admit'":          false,
		"git push origin HEAD":                 false,
		"ls $(go env GOROOT)":                  false,
		"ls `pwd`":                             false,
		"find . -name x -delete":               false,
		"find . -name '*.tmp' -exec rm {} \\;": false,
		"cat <<EOF\nhi\nEOF":                   false,
		"python3 -I script.py":                 false,
		"while true; do sleep 1; done":         false,
		"env GOFLAGS=-count=1 go vet ./...":    false,
		"bash -c 'ls'":                         false,
		"(cd x && ls)":                         false,
		"sleep 30":                             false,
		"ls 'open quote":                       false,
		"ls -la\nmake build":                   false,
	} {
		if got := Plain(command); got != want {
			t.Errorf("Plain(%q) = %v, want %v", command, got, want)
		}
	}
}

// TestWhat: a command is kept by its words, one space apart, up to 200 characters.
func TestWhat(t *testing.T) {
	t.Parallel()
	if got := What("  go   test\n ./...  "); got != "go test ./..." {
		t.Errorf("What = %q", got)
	}
	long := What(strings.Repeat("word ", 100))
	if len(long) > 200 || !strings.HasPrefix(long, "word word") || strings.HasSuffix(long, " ") {
		t.Errorf("What of a long command = %q (%d)", long, len(long))
	}
	if got := What(strings.Repeat("\u00e9", 150)); len(got) > 200 || !strings.HasPrefix(got, "\u00e9") || strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("What of a long word = %q", got)
	}
}

// TestHook: Claude's hook lets a reading command go at once; any other, Bash or Monitor, waits for its admission and
// then goes unchanged, the hook telling only why it waited; with no Djinn to answer, the command goes on.
func TestHook(t *testing.T) {
	t.Parallel()
	hook := func(t *testing.T, call string, admit func(what, dir, match string) (string, error)) string {
		t.Helper()
		var out bytes.Buffer
		if err := Hook(strings.NewReader(call), &out, admit); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	never := func(string, string, string) (string, error) {
		t.Error("a reading command was admitted")
		return "", nil
	}

	t.Run("reading", func(t *testing.T) {
		t.Parallel()
		if got := hook(t, `{"tool_name":"Bash","tool_input":{"command":"git status && ls"},"cwd":"/src"}`, never); got != "" {
			t.Errorf("hook output for a reading command: %q", got)
		}
		if got := hook(t, `{"tool_name":"Monitor","tool_input":{"ws":{"url":"ws://x"}},"cwd":"/src"}`, never); got != "" {
			t.Errorf("hook output for a call with no command: %q", got)
		}
	})
	t.Run("admitted", func(t *testing.T) {
		t.Parallel()
		for _, tool := range []string{"Bash", "Monitor"} {
			var asked []string
			got := hook(t, `{"tool_name":"`+tool+`","tool_input":{"command":"go  test ./...\necho done","run_in_background":true},"cwd":"/src"}`,
				func(what, dir, match string) (string, error) {
					asked = append(asked, what+" in "+dir+" by "+match)
					return "", nil
				})
			if !slices.Equal(asked, []string{"go test ./... echo done in /src by gotest"}) || got != "" {
				t.Errorf("%s: asked %q, output %q", tool, asked, got)
			}
		}
		got := hook(t, `{"tool_name":"Bash","tool_input":{"command":"npm ci"}}`,
			func(string, string, string) (string, error) { return "waited 3s: the machine is under pressure", nil })
		var out map[string]map[string]any
		if err := json.Unmarshal([]byte(got), &out); err != nil {
			t.Fatalf("hook output %q: %v", got, err)
		}
		if o := out["hookSpecificOutput"]; o["hookEventName"] != "PreToolUse" || o["additionalContext"] != "waited 3s: the machine is under pressure" ||
			len(o) != 2 {
			t.Errorf("hook output after a wait: %v", o)
		}
	})
	t.Run("no Djinn", func(t *testing.T) {
		t.Parallel()
		if got := hook(t, `{"tool_name":"Bash","tool_input":{"command":"go test ./..."}}`,
			func(string, string, string) (string, error) { return "", errors.New("no djinn answers") }); got != "" {
			t.Errorf("hook output with no Djinn: %q", got)
		}
	})
	if err := Hook(strings.NewReader("not json"), &bytes.Buffer{}, never); err == nil {
		t.Error("read a call that is no JSON")
	}
}

// child starts a process that lives until the test kills it: what an admitted command's shell stands for.
func child(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperEnv+"=sleep")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

// end kills the process cmd and waits for it.
func end(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

// admit asks an admission in the background for the task taskID, its command what run by the process pid.
func admit(t *testing.T, g *Gates, taskID, what string, pid int) (<-chan func(), <-chan string) {
	t.Helper()
	return ask(t, g, Request{Shared: true, TaskID: taskID, What: what, PID: pid})
}

func TestAdmission(t *testing.T) {
	t.Parallel()

	// Admissions share: many at once, light ones of every task; each is in its task's Held, never in List, and takes
	// no slot outside a worker. An admission needs its command's process.
	t.Run("shared", func(t *testing.T) {
		t.Parallel()
		f := &tasks{rank: map[string]int{"W1": 0, "W2": 0}}
		g := New(calm(), nil, f)
		await(t, func() <-chan func() { c, _ := admit(t, g, "W1", "go vet ./...", os.Getpid()); return c }(), "W1's admission")
		await(t, func() <-chan func() { c, _ := admit(t, g, "W1", "go build ./...", os.Getpid()); return c }(), "W1's second")
		await(t, func() <-chan func() { c, _ := admit(t, g, "W2", "go vet ./...", os.Getpid()); return c }(), "W2's admission")
		await(t, func() <-chan func() { c, _ := admit(t, g, "", "npm ci", os.Getpid()); return c }(), "one outside any task")
		if !slices.Equal(g.Held("W1"), []string{Commands}) || !slices.Equal(g.Held("W2"), []string{Commands}) {
			t.Errorf("held: W1 %v, W2 %v", g.Held("W1"), g.Held("W2"))
		}
		if l := g.List(); len(l) != 0 {
			t.Errorf("admissions listed: %+v", l)
		}
		if n, merges := g.Outside(); n != 0 || merges != 0 {
			t.Errorf("outside = %d, %d", n, merges)
		}
		if _, err := g.Take(t.Context(), Request{Shared: true, What: "go test"}, nil); err == nil {
			t.Error("admitted a command with no process")
		}
	})

	// Under pressure an admission waits, in its task's Waiting so its worker is neither paused nor judged silent; once
	// the pressure falls, the wish ranked first is admitted first, the other waiting for the memory it leaves.
	t.Run("under pressure, in order", func(t *testing.T) {
		t.Parallel()
		p := calm()
		p.squeeze(true)
		f := &tasks{rank: map[string]int{"low": 2, "high": 1}}
		g := New(p, peaks{"go test ./...": machine.GiB}, f)
		g.tick = 10 * time.Millisecond
		low, lowWaits := admit(t, g, "low", "go test ./...", os.Getpid())
		if why := await(t, lowWaits, "low waits"); why != squeezed {
			t.Errorf("low waits because %q", why)
		}
		high, _ := admit(t, g, "high", "go test ./...", os.Getpid())
		notGranted(t, high, "an admission under pressure")
		if !slices.Equal(g.Waiting("low"), []string{Commands}) || g.Held("low") != nil {
			t.Errorf("low waits for %v, holds %v", g.Waiting("low"), g.Held("low"))
		}
		p.setFree(machine.GiB * 3 / 2)
		p.squeeze(false)
		await(t, high, "the high wish's admission")
		if why := await(t, lowWaits, "low waits for memory"); !strings.HasPrefix(why, "go test ./... peaks at ") {
			t.Errorf("low waits because %q", why)
		}
		notGranted(t, low, "an admission the memory cannot hold")
	})

	// A command measured heavy, by its memory or its CPU time, goes one at a time: another task's heavy admission
	// and heavy gate wait for it, while light admissions and the same task's heavy ones pass. Once its process ends,
	// the next heavy one goes.
	t.Run("heavy one at a time", func(t *testing.T) {
		t.Parallel()
		f := &tasks{rank: map[string]int{"W1": 0, "W2": 0, "W3": 0}, works: map[string]bool{"W1": true, "W2": true, "W3": true}}
		m := &measured{costs: map[string]machine.Cost{
			"go test ./...":     {Peak: 3 * machine.GiB},
			"go tool task lint": {Peak: 100 << 20, CPU: 90 * time.Second},
			"go vet ./...":      {Peak: 500 << 20, CPU: 10 * time.Second},
		}}
		g := New(calm(), m, f)
		g.tick = 10 * time.Millisecond
		shell := child(t)
		await(t, func() <-chan func() { c, _ := admit(t, g, "W1", "go test ./...", shell.Process.Pid); return c }(), "the first heavy")
		heavy, heavyWaits := admit(t, g, "W2", "go test ./...", os.Getpid())
		if why := times(await(t, heavyWaits, "W2 waits")); why != "heavy commands go one at a time: W1: go test ./... runs one, for Ns" {
			t.Errorf("W2 waits because %q", why)
		}
		lint, lintWaits := run(t, g, "lint", "W3", "go tool task lint")
		if why := times(await(t, lintWaits, "the heavy gate waits")); !strings.HasPrefix(why, "heavy commands go one at a time") {
			t.Errorf("the heavy gate waits because %q", why)
		}
		await(t, func() <-chan func() { c, _ := admit(t, g, "W3", "go vet ./...", os.Getpid()); return c }(), "a light admission")
		await(t, func() <-chan func() { c, _ := admit(t, g, "W1", "go test ./...", os.Getpid()); return c }(), "W1's own second heavy")
		notGranted(t, heavy, "a second task's heavy command")
		end(shell)
		// W1's second heavy command still runs: W2 and the lint wait for it, its process being this test's.
		notGranted(t, heavy, "a heavy command while another task's runs")
		notGranted(t, lint, "a heavy gate while another task's heavy command runs")
		// In its worker's slot; outside any worker, a heavy admission takes a slot as a gate does.
		if n, _ := g.Outside(); n != 0 {
			t.Errorf("outside = %d", n)
		}
		await(t, func() <-chan func() { c, _ := admit(t, g, "", "go vet ./...", os.Getpid()); return c }(), "a light one outside")
		if n, _ := g.Outside(); n != 0 {
			t.Errorf("outside with a light admission = %d", n)
		}
		f.mu.Lock()
		f.works["W1"] = false
		f.mu.Unlock()
		if n, _ := g.Outside(); n != 1 {
			t.Errorf("outside with a heavy admission no worker runs = %d", n)
		}
	})

	// Djinn's merge of heavy work, waiting for a heavy command, goes before the workers' once it ends.
	t.Run("heavy merge first", func(t *testing.T) {
		t.Parallel()
		f := &tasks{rank: map[string]int{"W1": 0, "W2": 0, "M": 1}}
		m := &measured{costs: map[string]machine.Cost{"go tool task test": {Peak: 4 * machine.GiB}}}
		g := New(calm(), m, f)
		g.tick = 10 * time.Millisecond
		shell := child(t)
		await(t, func() <-chan func() { c, _ := admit(t, g, "W1", "go tool task test", shell.Process.Pid); return c }(), "the heavy")
		worker, _ := admit(t, g, "W2", "go tool task test", os.Getpid())
		push, pushWaits := merge(t, g, "test", "M", "go tool task test")
		await(t, pushWaits, "the push waits")
		end(shell)
		await(t, push, "the push")()
		await(t, worker, "the worker after the push")
	})

	// An admission is held as long as its command's process lives: given back once it ends, the run Djinn read is
	// counted in what the command costs, exit code unknown.
	t.Run("held until its process ends", func(t *testing.T) {
		t.Parallel()
		f := &tasks{rank: map[string]int{"W1": 0}}
		m := &measured{costs: map[string]machine.Cost{}}
		g := New(calm(), m, f)
		g.tick = 10 * time.Millisecond
		g.Measure(10*time.Millisecond, func(int) (machine.Group, error) {
			return machine.Group{Processes: 2, CPU: 2 * time.Second, Memory: 300 << 20}, nil
		})
		shell := child(t)
		await(t, func() <-chan func() { c, _ := admit(t, g, "W1", "npm run build", shell.Process.Pid); return c }(), "the admission")
		time.Sleep(50 * time.Millisecond) // Read more than once.
		end(shell)
		deadline := time.Now().Add(5 * time.Second)
		for g.Held("W1") != nil && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if g.Held("W1") != nil {
			t.Fatal("the admission outlived its process")
		}
		runs := m.recorded()
		if len(runs) != 1 || runs[0].GetCommand() != "npm run build" || runs[0].GetGate() != Commands || runs[0].GetTaskId() != "W1" ||
			runs[0].GetPeakMemoryBytes() != 300<<20 || runs[0].GetCpuSeconds() != 2 || runs[0].GetExitCode() != -1 {
			t.Errorf("runs recorded: %v", runs)
		}
		if notes := f.noted(); !slices.Contains(notes, "W1 gate commands: taken") ||
			!slices.ContainsFunc(notes, func(n string) bool { return strings.HasPrefix(n, "W1 gate commands: given back after") }) {
			t.Errorf("notes = %q", notes)
		}
	})
}

// TestService_Admit: a worker's command is admitted, the stream saying so and ending; the admission is then held
// while the shell that runs the command, found among the agent's processes, lives, and given back once it ends.
// Once no shell is found, it is given back too.
func TestService_Admit(t *testing.T) {
	t.Parallel()
	const task = "01a1b696-f578-70e8-9e8d-9c1bef4f26f7"
	f := &tasks{rank: map[string]int{task: 0}}
	g := New(calm(), nil, f)
	g.tick, g.findBy = 10*time.Millisecond, 3*time.Second
	client := server(t, g)
	gone := func() bool {
		deadline := time.Now().Add(5 * time.Second)
		for g.Held(task) != nil && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		return g.Held(task) == nil
	}
	admitted := func(t *testing.T, req *machinev1.GateServiceAdmitRequest) {
		t.Helper()
		stream, err := client.Admit(t.Context(), connect.NewRequest(req))
		if err != nil {
			t.Fatal(err)
		}
		var states []machinev1.GateState
		for stream.Receive() {
			states = append(states, stream.Msg().GetState())
		}
		if err := stream.Err(); err != nil || !slices.Equal(states, []machinev1.GateState{machinev1.GateState_GATE_STATE_HELD}) {
			t.Fatalf("admission: %v, %v", states, err)
		}
	}

	if runtime.GOOS == "linux" {
		command := "sleep 30; echo admitted-by-the-gates"
		admitted(t, &machinev1.GateServiceAdmitRequest{
			TaskId: task, What: command, Directory: "/src", AgentPid: int32(os.Getpid()), Match: machine.Match(command),
		})
		// The agent starts the command once admitted.
		shell := exec.Command("sh", "-c", command)
		if err := shell.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { end(shell) })
		time.Sleep(time.Second) // Past a few looks: found, held.
		if !slices.Equal(g.Held(task), []string{Commands}) {
			t.Fatalf("held %v while the command runs", g.Held(task))
		}
		end(shell)
		if !gone() {
			t.Fatal("the admission outlived its command")
		}
		if notes := f.noted(); !slices.ContainsFunc(notes, func(n string) bool { return strings.Contains(n, "gate commands: given back after") }) {
			t.Errorf("notes = %q", notes)
		}
	}

	// A command that ran too short, or never ran: given back once it is not found.
	admitted(t, &machinev1.GateServiceAdmitRequest{TaskId: task, What: "true", AgentPid: int32(os.Getpid()), Match: "nosuchcommandrunsanywhere"})
	if !gone() {
		t.Error("an admission whose command is not found is still held")
	}
	if notes := f.noted(); !slices.Contains(notes, task+" gate commands: given back: its command ran too short to be found, or did not run") {
		t.Errorf("notes = %q", notes)
	}

	// No agent given: refused.
	stream, err := client.Admit(t.Context(), connect.NewRequest(&machinev1.GateServiceAdmitRequest{What: "go test"}))
	if err == nil {
		for stream.Receive() {
		}
		err = stream.Err()
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("admitted with no agent: %v", err)
	}
}
