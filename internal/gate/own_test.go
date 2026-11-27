package gate

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
)

// refusedAsk takes the gate r asks in the background, as ask does; the error channel gets why it was not granted.
func refusedAsk(t *testing.T, g *Gates, r Request) (<-chan func(), <-chan error) {
	t.Helper()
	granted, failed := make(chan func(), 1), make(chan error, 1)
	go func() {
		hold, err := g.Take(t.Context(), r, nil)
		if err != nil {
			failed <- err
			return
		}
		granted <- hold.Give
	}()
	return granted, failed
}

// dolmenStack is dolmen's stack as its .agents/djinn.yaml declares it, for the tests of a task waiting for itself.
func dolmenStack(g *Gates) {
	g.Declare(func(string, string) Declared {
		return Declared{Project: "dolmen", File: ".agents/djinn.yaml", Gates: []*djinnv1.ProjectGate{{
			Name: "stack", Alone: []string{"task run-back"}, Beside: []string{"task recette", "task recette-seed"},
		}}}
	})
}

// TestOwnHoldQ684 reproduces Q684 (27/11, 14:48 UTC): W1093 ran turbo run dev in the background, an admission measured
// heavy, held until it ended; W1101's proof.sh, heavy, asked stack@dolmen alone and waited for that heavy turn; then
// W1093's film.sh, djinn gate run stack, came to share the gate behind W1101, which waits to hold it alone: it waited
// for its own task's turbo, through W1101, and nothing passed for an hour. It is now refused at once, saying the hold,
// its command and what to do; W1101 still waits for the turbo, and goes once it ends.
func TestOwnHoldQ684(t *testing.T) {
	t.Parallel()
	const turbo, proof = "pnpm exec turbo run dev --parallel --filter=console", "bash proof.sh"
	f := &tasks{rank: map[string]int{"W1093": 0, "W1101": 0}}
	g := New(calm(), peaks{turbo: 3 << 30, proof: 3 << 30}, f)
	g.tick = 10 * time.Millisecond
	g.within = func(int, int) bool { return false }
	dolmenStack(g)

	front := await(t, func() <-chan func() { c, _ := admit(t, g, "W1093", turbo, os.Getpid()); return c }(), "W1093's turbo")
	proofed, proofWaits := run(t, g, "stack", "W1101", proof)
	if why := times(await(t, proofWaits, "W1101 waits")); !strings.Contains(why, "heavy commands go one at a time: W1093: "+turbo) {
		t.Fatalf("W1101 waits because %q", why)
	}

	began := time.Now()
	film, failed := refusedAsk(t, g, Request{Name: "stack", TaskID: "W1093", What: "film.sh", PID: os.Getpid()})
	var refused *RefusedError
	select {
	case err := <-failed:
		if !errors.As(err, &refused) {
			t.Fatalf("film.sh failed with %v", err)
		}
	case <-film:
		t.Fatal("film.sh was granted while W1101 waits for W1093's turbo")
	case <-time.After(5 * time.Second):
		t.Fatal("film.sh still waits behind its own task")
	}
	if d := time.Since(began); d > 2*time.Second {
		t.Errorf("refused after %s", d)
	}
	want := "stack (dolmen, .agents/djinn.yaml): refused rather than wait for its own task: W1093: " + turbo +
		" holds commands, heavy, for Ns, and W1101: " + proof + " waits for it before this request. Stop that command " +
		"first (one left running in the background keeps its gate until it ends), or run both in one command under one " +
		"gate: djinn gate run stack -- a script that starts the first, runs the second, then stops the first"
	if got := times(refused.Reason); got != want {
		t.Errorf("refused because\n%q, want\n%q", got, want)
	}
	if w := g.Waiting("W1093"); len(w) != 0 {
		t.Errorf("W1093 still waits for %v", w)
	}
	if !slices.ContainsFunc(f.noted(), func(n string) bool { return strings.HasPrefix(n, "W1093 gate stack@dolmen: refused: ") }) {
		t.Errorf("W1093's events: %q", f.noted())
	}

	front()
	await(t, proofed, "W1101's proof.sh once the turbo ends")()
}

// TestOwnHoldSameGate: a task that holds a gate, in the background, and asks it again, alone or shared, passes under
// its own hold rather than wait for it, directly or behind another task's request that waits for it; it takes no heavy
// turn again. The other task waits for both, and goes once they end.
func TestOwnHoldSameGate(t *testing.T) {
	t.Parallel()

	// W1 runs a heavy recette, sharing the stack, in the background; W2's run-back waits to hold it alone; W1's seed,
	// which would come behind W2, passes under W1's recette (Q684 as its lead read it).
	t.Run("shared", func(t *testing.T) {
		t.Parallel()
		f := &tasks{rank: map[string]int{"W1": 0, "W2": 0}}
		g := New(calm(), peaks{"task recette": 3 << 30, "task run-back": 3 << 30}, f)
		g.tick = 10 * time.Millisecond
		dolmenStack(g)
		recette := await(t, func() <-chan func() { c, _ := admit(t, g, "W1", "task recette", os.Getpid()); return c }(), "W1's recette")
		runBack, runBackWaits := admit(t, g, "W2", "task run-back", os.Getpid())
		await(t, runBackWaits, "W2's run-back waits")
		seed, failed := refusedAsk(t, g, Request{Shared: true, TaskID: "W1", What: "task recette-seed", PID: os.Getpid()})
		var giveSeed func()
		select {
		case giveSeed = <-seed:
		case err := <-failed:
			t.Fatalf("W1's seed failed: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("W1's seed waits behind its own task's recette")
		}
		g.mu.Lock()
		var held *waiter
		for h := range g.shared {
			if h.what == "task recette-seed" {
				held = h
			}
		}
		g.mu.Unlock()
		if held == nil || held.heavy || held.name != "stack@dolmen" {
			t.Fatalf("W1's seed held as %+v", held)
		}
		g.mu.Lock()
		waited := held.waited()
		g.mu.Unlock()
		if waited != "stack (dolmen, .agents/djinn.yaml): passed under its own task's hold: W1: task recette holds stack@dolmen" {
			t.Errorf("the seed's history says %q", waited)
		}
		if !slices.Contains(f.noted(), "W1 gate stack@dolmen: passes under its own task's hold, rather than wait for it: W1: task recette holds stack@dolmen") {
			t.Errorf("W1's events: %q", f.noted())
		}
		notGranted(t, runBack, "W2's run-back while W1 shares the stack")
		recette()
		notGranted(t, runBack, "W2's run-back while W1's seed shares the stack")
		giveSeed()
		await(t, runBack, "W2's run-back once W1's commands end")()
	})

	// W1 holds the stack alone with its hold-stack.sh, in the background (13:05, Q684); its next djinn gate run stack
	// passes under it, where it waited for itself.
	t.Run("alone", func(t *testing.T) {
		t.Parallel()
		f := &tasks{rank: map[string]int{"W1": 0, "W2": 0}}
		g := New(calm(), peaks{"hold-stack.sh": 3 << 30}, f)
		g.tick = 10 * time.Millisecond
		dolmenStack(g)
		hold := await(t, func() <-chan func() { c, _ := run(t, g, "stack", "W1", "hold-stack.sh"); return c }(), "W1's hold-stack.sh")
		other, otherWaits := run(t, g, "stack", "W2", "task run-back")
		await(t, otherWaits, "W2's run-back waits")
		film := await(t, func() <-chan func() { c, _ := run(t, g, "stack", "W1", "film.sh"); return c }(), "W1's film.sh under its own hold")
		if l := g.List(); len(l) != 1 || l[0].HolderTaskID != "W1" || len(l[0].Waiting) != 1 {
			t.Errorf("list = %+v", l)
		}
		notGranted(t, other, "W2's run-back while W1 holds the stack alone")
		hold()
		notGranted(t, other, "W2's run-back while W1's film.sh shares the stack")
		film()
		await(t, other, "W2's run-back once W1's commands end")()
	})
}

// TestOwnHoldRunsIt: a request the task's own hold runs, a djinn gate run inside its heavy go tool task check, passes
// where it would wait behind a merge that waits for that check's heavy turn: refusing it would fail the check, waiting
// would never end. Where the hold does not run it, it is refused.
func TestOwnHoldRunsIt(t *testing.T) {
	t.Parallel()
	const check, lint, push = "go tool task check", "go tool task lint", "go tool task test"
	for _, runs := range []bool{true, false} {
		f := &tasks{rank: map[string]int{"W1": 0, "W9": 0}}
		g := New(calm(), peaks{check: 3 << 30, lint: 3 << 30, push: 3 << 30}, f)
		g.tick = 10 * time.Millisecond
		const inside = 4242
		g.within = func(pid, ancestor int) bool { return runs && pid == inside && ancestor == os.Getpid() }
		checked := await(t, func() <-chan func() { c, _ := admit(t, g, "W1", check, os.Getpid()); return c }(), "W1's check")
		pushed, pushWaits := merge(t, g, "test", "W9", push)
		await(t, pushWaits, "the push waits for the heavy check")
		linted, failed := refusedAsk(t, g, Request{Name: "lint", TaskID: "W1", What: lint, PID: inside})
		select {
		case give := <-linted:
			if !runs {
				t.Error("granted a lint its task's check does not run")
			}
			give()
		case err := <-failed:
			if refused := new(RefusedError); runs || !errors.As(err, &refused) ||
				!strings.Contains(refused.Reason, "W1: "+check+" holds commands, heavy,") ||
				!strings.Contains(refused.Reason, "W9: "+push+" waits for it before this request") ||
				!strings.Contains(refused.Reason, "djinn gate run lint --") {
				t.Errorf("the lint inside (%v) failed: %v", runs, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the lint inside (%v) waits for its own check", runs)
		}
		checked()
		await(t, pushed, "the push once the check ends")()
	}
}

// TestOwnHoldOthers: a task's request that waits for another task's hold waits as before, its own holds elsewhere
// aside; Djinn's merge of a task's work waits for its task's hold, never refused for it.
func TestOwnHoldOthers(t *testing.T) {
	t.Parallel()
	f := &tasks{rank: map[string]int{"W1": 0, "W2": 0}}
	g := New(calm(), peaks{"task run-back": 3 << 30}, f)
	g.tick = 10 * time.Millisecond
	g.within = func(int, int) bool { return false }
	dolmenStack(g)
	vet := await(t, func() <-chan func() { c, _ := admit(t, g, "W1", "go vet ./...", os.Getpid()); return c }(), "W1's go vet")
	other := await(t, func() <-chan func() { c, _ := run(t, g, "stack", "W2", "task run-back"); return c }(), "W2's run-back")
	recette, failed := refusedAsk(t, g, Request{Name: "stack", TaskID: "W1", What: "task recette"})
	select {
	case err := <-failed:
		t.Fatalf("W1's recette, which waits for W2, failed: %v", err)
	case <-recette:
		t.Fatal("W1's recette was granted while W2 holds the stack alone")
	case <-time.After(150 * time.Millisecond):
	}
	other()
	await(t, recette, "W1's recette once W2's run-back ends")()
	vet()

	held := await(t, func() <-chan func() { c, _ := take(t, g, "e2e", "W1"); return c }(), "W1's e2e")
	merged, mergeFailed := refusedAsk(t, g, Request{Name: "e2e", TaskID: "W1", What: "go tool task e2e", First: true})
	select {
	case err := <-mergeFailed:
		t.Fatalf("W1's merge failed: %v", err)
	case <-merged:
		t.Fatal("W1's merge was granted while its task holds e2e alone")
	case <-time.After(150 * time.Millisecond):
	}
	held()
	await(t, merged, "W1's merge once its task's e2e ends")()
}

// TestOwnHoldService: djinn gate run, refused rather than wait for its own task, ends at once with a failed
// precondition that says why: its caller prints it.
func TestOwnHoldService(t *testing.T) {
	t.Parallel()
	const turbo, proof = "turbo run dev", "bash proof.sh"
	const w1, w2 = "01a21cf9-a332-71a0-b387-52a47f603c1f", "01a21d05-5bc5-707e-b574-8f2446a67fd4"
	f := &tasks{rank: map[string]int{w1: 0, w2: 0}}
	g := New(calm(), peaks{turbo: 3 << 30, proof: 3 << 30}, f)
	g.tick = 10 * time.Millisecond
	g.within = func(int, int) bool { return false }
	dolmenStack(g)
	front := await(t, func() <-chan func() { c, _ := admit(t, g, w1, turbo, os.Getpid()); return c }(), "W1's turbo")
	defer front()
	proofed, proofWaits := run(t, g, "stack", w2, proof)
	await(t, proofWaits, "W2 waits")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second) // Not refused, it would wait for good.
	defer cancel()
	stream, err := server(t, g).Hold(ctx, connect.NewRequest(&machinev1.GateServiceHoldRequest{
		Name: "stack", TaskId: w1, What: "film.sh", Directory: t.TempDir(), Pid: int32(os.Getpid()),
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for stream.Receive() {
		if stream.Msg().GetState() == machinev1.GateState_GATE_STATE_HELD {
			t.Fatal("film.sh was granted")
		}
	}
	if connect.CodeOf(stream.Err()) != connect.CodeFailedPrecondition ||
		!strings.Contains(stream.Err().Error(), "refused rather than wait for its own task: "+w1+": "+turbo+" holds commands") {
		t.Errorf("film.sh ended with %v", stream.Err())
	}
	front()
	await(t, proofed, "W2's proof.sh")()
}
