package gate

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
)

// TestMatches: a pattern's words start a part of the command, past its variables and wrappers; * stands for zero or
// more words; the program goes by its base name; what split cannot read matches nothing.
func TestMatches(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		pattern, command string
		want             bool
	}{
		{"task run-back", "task run-back", true},
		{"task run-back", "task run-back --force", true},
		{"task run-back", "task run-backend", false},
		{"task run-back", "task", false},
		{"task run-back", "cd back && task run-back 2>&1", true},
		{"task run-back", "make && task run-back | tail -5", true},
		{"task run-back", "FOO=1 env BAR=2 task run-back", true},
		{"task run-back", "nohup time task run-back", true},
		{"task run-back", "/usr/local/bin/task run-back", true},
		{"task run-back", "'task' \"run-back\"", true},
		{"task run-back", "echo task run-back", false},
		{"task run-back", "task $(echo run-back)", false},
		{"docker compose * up", "docker compose up", true},
		{"docker compose * up", "docker compose -f a.yml -f b.yml up -d", true},
		{"docker compose * up", "docker compose down", false},
		{"docker compose * up", "docker compose", false},
		{"task", "task recette", true},
		{"go test *", "go test", true},
		{"", "task", false},
	} {
		if got := Matches(c.pattern, c.command); got != c.want {
			t.Errorf("Matches(%q, %q) = %v, want %v", c.pattern, c.command, got, c.want)
		}
	}
}

// TestDeclared (L17): a gate a project declares goes by name@project. An admitted command an alone pattern names holds
// it alone, and waits for those that share it too; one a beside pattern names shares it, and goes behind one that came
// first to hold it alone. djinn gate run with its name in the project takes the same gate; another project's gate of
// the same name is another gate; a command no pattern names is admitted as before. The reasons name the gate, its
// project and its file.
func TestDeclared(t *testing.T) {
	t.Parallel()
	f := &tasks{rank: map[string]int{"W1": 0, "W2": 0, "W3": 0, "W4": 0, "W5": 0, "W9": 0}}
	g := New(calm(), nil, f)
	g.tick = 10 * time.Millisecond
	stack := &djinnv1.ProjectGate{
		Name: "stack", Alone: []string{"task run-back", "docker compose * up"}, Beside: []string{"task recette", "task qa"},
	}
	g.Declare(func(taskID, _ string) Declared {
		if taskID == "W9" {
			return Declared{Project: "whakatuu", File: "djinn.yaml", Gates: []*djinnv1.ProjectGate{stack}}
		}
		return Declared{Project: "dolmen", File: ".agents/djinn.yaml", Gates: []*djinnv1.ProjectGate{stack}}
	})
	pid := os.Getpid()
	const label = "stack (dolmen, .agents/djinn.yaml): "

	recette := await(t, func() <-chan func() { c, _ := admit(t, g, "W1", "task recette", pid); return c }(), "W1's recette")
	if held := g.Held("W1"); !slices.Equal(held, []string{"stack@dolmen"}) {
		t.Errorf("W1 holds %v", held)
	}
	runBack, runBackWaits := admit(t, g, "W2", "cd back && task run-back", pid)
	if why := times(await(t, runBackWaits, "the run-back waits")); why != label+"shared by W1: task recette, for Ns" {
		t.Errorf("the run-back waits because %q", why)
	}
	qa, qaWaits := admit(t, g, "W3", "task qa", pid)
	if why := await(t, qaWaits, "the qa waits"); why != label+"behind W2: cd back && task run-back, which waits to hold it alone, shared by W1: task recette" {
		t.Errorf("the qa waits because %q", why)
	}
	notGranted(t, qa, "a beside command behind an alone one that waits")

	// Another project's stack is another gate; a command no pattern names is admitted as before.
	other := await(t, func() <-chan func() { c, _ := admit(t, g, "W9", "task run-back", pid); return c }(), "whakatuu's run-back")
	if held := g.Held("W9"); !slices.Equal(held, []string{"stack@whakatuu"}) {
		t.Errorf("W9 holds %v", held)
	}
	vet := await(t, func() <-chan func() { c, _ := admit(t, g, "W4", "go vet ./...", pid); return c }(), "W4's go vet")
	if held := g.Held("W4"); !slices.Equal(held, []string{Commands}) {
		t.Errorf("W4 holds %v", held)
	}

	recette()
	giveRunBack := await(t, runBack, "the run-back once the recette ends")
	if why := times(await(t, qaWaits, "the qa waits for the run-back")); why != label+"held by W2: cd back && task run-back, for Ns" {
		t.Errorf("the qa waits because %q", why)
	}
	// djinn gate run stack, typed in the project, takes the same gate, alone as its pattern says.
	typed, typedWaits := run(t, g, "Stack", "W4", "docker compose -f a.yml up")
	if why := times(await(t, typedWaits, "the typed gate waits")); why != label+"held by W2: cd back && task run-back, for Ns" {
		t.Errorf("the typed gate waits because %q", why)
	}
	if l := g.List(); len(l) != 2 || l[0].Name != "stack@dolmen" || l[0].HolderTaskID != "W2" || len(l[0].Waiting) != 2 ||
		l[1].Name != "stack@whakatuu" {
		t.Errorf("list = %+v", l)
	}
	giveRunBack()
	giveQA := await(t, qa, "the qa, first come, once the run-back ends")
	if why := times(await(t, typedWaits, "the typed gate waits for the qa")); why != label+"shared by W3: task qa, for Ns" {
		t.Errorf("the typed gate waits because %q", why)
	}
	giveQA()
	giveTyped := await(t, typed, "the typed gate once the qa ends")
	if held := g.Held("W4"); !slices.Equal(held, []string{Commands, "stack@dolmen"}) {
		t.Errorf("W4 holds %v", held)
	}

	// A task's own beside command keeps none of its alone ones waiting: one may run the other.
	giveTyped()
	own := await(t, func() <-chan func() { c, _ := admit(t, g, "W5", "task recette", pid); return c }(), "W5's recette")
	await(t, func() <-chan func() { c, _ := admit(t, g, "W5", "task run-back", pid); return c }(), "W5's run-back beside its recette")()
	own()

	// A gate's name the project does not declare keeps its name.
	e2e := await(t, func() <-chan func() { c, _ := run(t, g, "e2e", "W1", "go tool task e2e"); return c }(), "W1's e2e")
	if held := g.Held("W1"); !slices.Equal(held, []string{"e2e"}) {
		t.Errorf("W1 holds %v", held)
	}
	for _, give := range []func(){e2e, other, vet} {
		give()
	}
}

// TestDeclaredHeavy: a declared gate leaves the machine's bounds as they are: a beside command measured heavy shares
// its gate, and still waits for another task's heavy command.
func TestDeclaredHeavy(t *testing.T) {
	t.Parallel()
	f := &tasks{rank: map[string]int{"W1": 0, "W2": 0}}
	g := New(calm(), peaks{"task recette": 3 << 30, "go tool task e2e": 3 << 30}, f)
	g.tick = 10 * time.Millisecond
	g.Declare(func(string, string) Declared {
		return Declared{Project: "dolmen", Gates: []*djinnv1.ProjectGate{{Name: "stack", Beside: []string{"task recette"}}}}
	})
	e2e := await(t, func() <-chan func() { c, _ := admit(t, g, "W1", "go tool task e2e", os.Getpid()); return c }(), "W1's heavy e2e")
	recette, waits := admit(t, g, "W2", "task recette", os.Getpid())
	if why := times(await(t, waits, "the heavy recette waits")); why != "stack (dolmen): heavy commands go one at a time: W1: go tool task e2e runs one, for Ns" {
		t.Errorf("the heavy recette waits because %q", why)
	}
	e2e()
	await(t, recette, "the heavy recette once the e2e ends")()
}

// dolmenYAML is the .agents/djinn.yaml L17 proposes for dolmen, with what rebuilds or retags its images (the case of
// W402 below): everything that rebuilds, retags or restarts the shared stack holds it alone.
const dolmenYAML = `gates:
  - name: stack
    alone:
      - task run-back
      - task run-infra
      - task run-topology
      - task restart
      - task down
      - task docker-clean
      - docker compose * up
      - docker compose * restart
      - docker compose * down
      - docker compose * stop
      - docker compose * rm
      - docker compose * build
      - docker compose * create
      - docker compose * start
      - docker build
      - docker buildx build
      - docker image build
      - docker tag
      - docker image tag
      - docker rmi
      - docker image rm
    beside:
      - task recette
      - task recette-seed
      - task willers-showcase
      - task qa
      - task scenario-willy
      - task bench-willy-light
`

// TestDeclaredDolmenImage reproduces the case of Will's wish (17/11): while W402's case 24 used dolmen's stack, another
// worker rebuilt the image dolmen-empowill:latest and restarted the shared stack, taking no gate. With dolmen's
// declaration, read as Djinn reads it, each of those commands, as the hook gives it (machine.Key), waits for the
// recette, and runs once it ends.
func TestDeclaredDolmenImage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agents", "djinn.yaml"), []byte(dolmenYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := plan.ReadProjectConfig(dir)
	if err != nil || len(read.Dropped) > 0 || len(read.Config.GetGates()) != 1 {
		t.Fatalf("dolmen's file: %+v, %v", read, err)
	}
	f := &tasks{rank: map[string]int{"W402": 0, "W403": 0}}
	g := New(calm(), nil, f)
	g.tick = 10 * time.Millisecond
	g.Declare(func(string, string) Declared {
		return Declared{Project: "dolmen", File: read.File, Gates: read.Config.GetGates()}
	})
	pid := os.Getpid()
	worktree := "/home/dev/.config/djinn/projects/p/worktrees/w403"
	recette := await(t, func() <-chan func() {
		c, _ := admit(t, g, "W402", machine.Key(`CASE="24" task recette MR=3074`, worktree), pid)
		return c
	}(), "W402's recette")
	for _, command := range []string{
		"docker build -t dolmen-empowill:latest .",
		"cd " + worktree + " && docker compose -f docker-compose.back.yml build empowill",
		"docker tag dolmen-empowill:dev dolmen-empowill:latest",
		"docker compose -f docker-compose.back.yml up -d --force-recreate",
		"task restart",
		"COMPOSE_PROJECT_NAME=dolmen task run-back 2>&1 | tail -20",
		"bash -c 'docker image tag dolmen-empowill:dev dolmen-empowill:latest'",
	} {
		what := machine.Key(command, worktree)
		if _, alone, ok := (Declared{Project: "dolmen", Gates: read.Config.GetGates()}).Match(what); !ok || !alone {
			t.Errorf("%q (key %q) is not under stack alone", command, what)
			continue
		}
		ctx, cancel := context.WithCancel(t.Context())
		granted, waits := make(chan struct{}), make(chan string, 16)
		go func() {
			if hold, err := g.Take(ctx, Request{Shared: true, TaskID: "W403", What: what, PID: pid}, func(why string) { waits <- why }); err == nil {
				hold.Give()
				close(granted)
			}
		}()
		if why := times(await(t, waits, command+" waits")); !strings.HasPrefix(why, "stack (dolmen, .agents/djinn.yaml): shared by W402: CASE=* task recette MR=3074, for ") {
			t.Errorf("%q waits because %q", command, why)
		}
		select {
		case <-granted:
			t.Errorf("%q ran under W402's recette", command)
		case <-time.After(100 * time.Millisecond):
		}
		cancel() // It leaves the queue, as a hook that gives up does.
	}
	rebuild, waits := admit(t, g, "W403", machine.Key("docker build -t dolmen-empowill:latest .", worktree), pid)
	await(t, waits, "the rebuild waits")
	recette()
	await(t, rebuild, "the rebuild once the recette ends")()
}

// TestDeclaredDjinn reads Djinn's own .agents/djinn.yaml as Djinn reads it (PR #43 review: Djinn uses its gates daily):
// every field known, every gate kept, and each command, as the hook gives it (machine.Key), under the gate it means.
func TestDeclaredDjinn(t *testing.T) {
	t.Parallel()
	read, err := plan.ReadProjectConfig(filepath.Join("..", ".."))
	if err != nil || read.File != ".agents/djinn.yaml" || len(read.Unknown)+len(read.Dropped)+len(read.Broad) > 0 {
		t.Fatalf("Djinn's file: %+v, %v", read, err)
	}
	d := Declared{Project: "djinn", File: read.File, Gates: read.Config.GetGates()}
	worktree := "/home/dev/.config/djinn/projects/p/worktrees/w1"
	for _, c := range []struct {
		command, gate string
		alone         bool
	}{
		{"go tool task e2e -- --grep @budget", "e2e", true},
		{"go tool task e2e -- e2e/grimoire.spec.ts --grep @budget", "e2e", true},
		{"go tool task e2e -- e2e/wish-render.spec.ts", "e2e", false},
		{"go tool task e2e", "e2e", false},
		{"cd " + worktree + " && go tool task test 2>&1 | tail -30", "e2e", false},
		{"go tool task screenshots", "e2e", false},
		{"npx playwright test --config e2e/playwright.config.ts", "e2e", false},
		{"go tool task e2e-native -- -v", "window", true},
		{"go tool task check-window -- -path wails", "window", true},
		{"go test -count=1 ./e2e/native", "window", true},
		{"go tool task lint", "", false},
		{"go tool task test-go", "", false},
		{"go test ./internal/gate", "", false},
	} {
		what := machine.Key(c.command, worktree)
		gt, alone, ok := d.Match(what)
		if c.gate == "" {
			if ok {
				t.Errorf("%q (key %q) is under %s", c.command, what, gt.GetName())
			}
			continue
		}
		if !ok || gt.GetName() != c.gate || alone != c.alone {
			t.Errorf("%q (key %q): gate %q alone %v, want %q alone %v", c.command, what, gt.GetName(), alone, c.gate, c.alone)
		}
	}
}

// TestDeclaredUnnamedAlone (W1021, L106): djinn gate run stack with a script no pattern names holds the declared gate
// alone, light and unmeasured as it is: Djinn sees the script, not the run-back it runs. A second such script waits for
// it; a beside command waits too, and shares the gate with another once it is free.
func TestDeclaredUnnamedAlone(t *testing.T) {
	t.Parallel()
	f := &tasks{rank: map[string]int{"W574": 0, "W1021": 0, "W3": 0, "W4": 0}}
	g := New(calm(), nil, f)
	g.tick = 10 * time.Millisecond
	stack := &djinnv1.ProjectGate{Name: "stack", Alone: []string{"task * run-back"}, Beside: []string{"task * recette"}}
	g.Declare(func(string, string) Declared {
		return Declared{Project: "dolmen", File: ".agents/djinn.yaml", Gates: []*djinnv1.ProjectGate{stack}}
	})
	lot, _ := run(t, g, "stack", "W574", "$DJINN_SCRATCH/bin/lot.sh gemini-4-argon h2")
	giveLot := await(t, lot, "W574's lot")
	final, finalWaits := run(t, g, "stack", "W1021", "$DJINN_SCRATCH/final-run.sh")
	if why := times(await(t, finalWaits, "the final run waits")); why != "stack (dolmen, .agents/djinn.yaml): held by W574: $DJINN_SCRATCH/bin/lot.sh gemini-4-argon h2, for Ns" {
		t.Errorf("the final run waits because %q", why)
	}
	notGranted(t, final, "a second script no pattern names")
	recette, recetteWaits := run(t, g, "stack", "W3", "task recette")
	await(t, recetteWaits, "the recette waits")
	giveLot()
	giveFinal := await(t, final, "the final run once the lot ends")
	notGranted(t, recette, "a beside command under a script held alone")
	giveFinal()
	giveRecette := await(t, recette, "the recette once the final run ends")
	await(t, func() <-chan func() { c, _ := run(t, g, "stack", "W4", "task -d back recette"); return c }(), "a recette beside the other")()
	giveRecette()
}

// TestDeclaredBeside: djinn gate run --beside says the command only reads: a script no pattern names shares the
// declared gate with another, and with a beside command; an alone pattern still wins over the flag.
func TestDeclaredBeside(t *testing.T) {
	t.Parallel()
	f := &tasks{rank: map[string]int{"W1": 0, "W2": 0, "W3": 0, "W4": 0}}
	g := New(calm(), nil, f)
	g.tick = 10 * time.Millisecond
	stack := &djinnv1.ProjectGate{Name: "stack", Alone: []string{"task run-back"}, Beside: []string{"task recette"}}
	g.Declare(func(string, string) Declared {
		return Declared{Project: "dolmen", Gates: []*djinnv1.ProjectGate{stack}}
	})
	read := await(t, func() <-chan func() {
		c, _ := ask(t, g, Request{Name: "stack", TaskID: "W1", What: "./read-logs.sh", Beside: true})
		return c
	}(), "W1's script beside")
	other := await(t, func() <-chan func() {
		c, _ := ask(t, g, Request{Name: "stack", TaskID: "W2", What: "./read-db.sh", Beside: true})
		return c
	}(), "W2's script beside W1's")
	recette := await(t, func() <-chan func() { c, _ := run(t, g, "stack", "W3", "task recette"); return c }(), "W3's recette beside")
	runBack, waits := ask(t, g, Request{Name: "stack", TaskID: "W4", What: "task run-back", Beside: true})
	if why := times(await(t, waits, "the run-back waits")); !strings.HasPrefix(why, "stack (dolmen): shared by W1: ./read-logs.sh") {
		t.Errorf("the run-back waits because %q", why)
	}
	notGranted(t, runBack, "an alone pattern's command said beside")
	for _, give := range []func(){read, other, recette} {
		give()
	}
	await(t, runBack, "the run-back once the others end")()
}

// TestListSharers (L106): the list says how many share a gate, and names each of them, the first granted first.
func TestListSharers(t *testing.T) {
	t.Parallel()
	f := &tasks{rank: map[string]int{"W1": 0, "W2": 0, "W3": 0}}
	g := New(calm(), nil, f)
	g.tick = 10 * time.Millisecond
	g.Declare(func(string, string) Declared {
		return Declared{Project: "dolmen", Gates: []*djinnv1.ProjectGate{{Name: "stack", Beside: []string{"task recette"}}}}
	})
	// Each sharer says its project, as a waiter does (Q653 B: the window lays a shared gate under each sharer's).
	g.ProjectResolver(func(taskID, _ string) string { return map[string]string{"W1": "dolmen", "W3": "djinn"}[taskID] })
	var gives []func()
	for _, id := range []string{"W1", "W2", "W3"} {
		gives = append(gives, await(t, func() <-chan func() { c, _ := run(t, g, "stack", id, "task recette "+id); return c }(), id+"'s recette"))
	}
	l := g.List()
	want := []Sharer{
		{Who: "W1: task recette W1", TaskID: "W1", Project: "dolmen"},
		{Who: "W2: task recette W2", TaskID: "W2"},
		{Who: "W3: task recette W3", TaskID: "W3", Project: "djinn"},
	}
	if len(l) != 1 || l[0].HolderTaskID != "W1" || !slices.Equal(l[0].Sharers, want) {
		t.Errorf("list = %+v", l)
	}
	for _, give := range gives {
		give()
	}
	// Held alone, a gate names no sharer.
	alone := await(t, func() <-chan func() { c, _ := run(t, g, "stack", "W1", "./rebuild.sh"); return c }(), "W1's script alone")
	if l := g.List(); len(l) != 1 || len(l[0].Sharers) != 0 || l[0].HolderTaskID != "W1" {
		t.Errorf("list = %+v", l)
	}
	alone()
}
