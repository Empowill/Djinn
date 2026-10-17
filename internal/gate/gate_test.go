package gate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/internal/harness"
	"github.com/empowill/djinn/internal/machine"
)

// TestTake covers Gates.Take: one holder at a time, CPU pressure, wish and azima rank order, merge priority, memory
// peak budgets, waiter cancellation, hold timeout, and lit ifrits.
func TestTake(t *testing.T) {
	t.Parallel()

	// A gate has one holder at a time; the next waiter gets it when it is given back, and the tasks' events say so.
	// Another gate is free meanwhile. Held says what a task holds.
	t.Run("one holder at a time", func(t *testing.T) {
		t.Parallel()
		f := &tasks{rank: map[string]int{"W1": 0, "W2": 0}}
		g := New(nil, nil, f)
		first := await(t, func() <-chan func() { c, _ := take(t, g, "Codegen", "W1"); return c }(), "first grant")
		second, waits := take(t, g, "codegen", "W2")
		if why := times(await(t, waits, "reason")); why != "held by W1, for Ns" {
			t.Errorf("second waits because %q", why)
		}
		notGranted(t, second, "a held gate")
		other, _ := take(t, g, "e2e", "")
		await(t, other, "another gate")
		if l := g.List(); len(l) != 2 || l[0].Name != "codegen" || l[0].HolderTaskID != "W1" || !slices.Equal(l[0].Waiting, []string{"W2"}) ||
			!slices.Equal(l[0].WaitingTaskIDs, []string{"W2"}) || l[1].WaitingTaskIDs != nil {
			t.Errorf("list = %+v", l)
		}
		// What a task holds, a worker that waits holding nothing; the holder outside any task is no task's.
		if !slices.Equal(g.Held("W1"), []string{"codegen"}) || g.Held("W2") != nil || g.Held("") != nil {
			t.Errorf("held: W1 %v, W2 %v, none %v", g.Held("W1"), g.Held("W2"), g.Held(""))
		}
		first()
		first() // Giving back twice does nothing.
		if g.Held("W1") != nil {
			t.Errorf("W1 still holds %v", g.Held("W1"))
		}
		await(t, second, "second grant")()
		want := []string{
			"W1 gate codegen: asked", "W1 gate codegen: taken",
			"W2 gate codegen: asked", "W2 gate codegen: waiting: held by W1, for Ns", "W1 gate codegen: given back after Ns",
			"W2 gate codegen: taken", "W2 gate codegen: given back after Ns",
		}
		var got []string
		for _, n := range f.noted() {
			got = append(got, times(n))
		}
		if !slices.Equal(got, want) {
			t.Errorf("notes = %q\nwant %q", got, want)
		}
		if _, err := g.Take(t.Context(), Request{Name: "codegen", TaskID: "W9"}, nil); err == nil {
			t.Error("took a gate for an unknown task")
		}
		if _, err := New(nil, nil, nil).Take(t.Context(), Request{Name: "codegen", TaskID: "W1"}, nil); err == nil {
			t.Error("took a gate with a task when no task store is configured")
		}
	})

	// No gate is granted while the machine is under pressure; it is once the pressure falls.
	t.Run("under CPU pressure", func(t *testing.T) {
		t.Parallel()
		p := calm()
		p.squeeze(true)
		g := New(p, nil, nil)
		g.tick = 10 * time.Millisecond
		granted, waits := take(t, g, "stack", "")
		if why := await(t, waits, "reason"); why != squeezed {
			t.Errorf("waits because %q", why)
		}
		notGranted(t, granted, "a gate under pressure")
		p.squeeze(false)
		await(t, granted, "grant after the pressure")()
	})

	// When a gate frees, the waiter of the first wish of the rank gets it, whoever came first.
	t.Run("wish rank order", func(t *testing.T) {
		t.Parallel()
		f := &tasks{rank: map[string]int{"holder": 0, "low": 2, "high": 1}}
		g := New(nil, nil, f)
		give := await(t, func() <-chan func() { c, _ := take(t, g, "paid", "holder"); return c }(), "first grant")
		low, lowWaits := take(t, g, "paid", "low")
		await(t, lowWaits, "low waits")
		high, highWaits := take(t, g, "paid", "high")
		await(t, highWaits, "high waits")
		outside, outsideWaits := take(t, g, "paid", "")
		await(t, outsideWaits, "outside waits")
		give()
		await(t, high, "the high wish's grant")()
		await(t, low, "the low wish's grant")()
		await(t, outside, "the grant outside any task")()
	})

	// Within a wish, the waiter whose azima is ranked first gets the gate, whoever came first; work of no azima after
	// the ranked azimas', a wish ranked first before them all, and Djinn's merges before every worker, whatever their
	// azimas (W134).
	t.Run("azima place order", func(t *testing.T) {
		t.Parallel()
		f := &tasks{
			rank:  map[string]int{"holder": 0, "loose": 1, "low": 1, "high": 1, "first": 0, "M": 1},
			place: map[string]int{"holder": 0, "low": 4, "high": 0, "first": 9},
		}
		g := New(nil, nil, f)
		give := await(t, func() <-chan func() { c, _ := take(t, g, "e2e", "holder"); return c }(), "first grant")
		var order []string
		granted := map[string]<-chan func(){}
		for _, id := range []string{"loose", "low", "high", "first"} {
			c, waits := take(t, g, "e2e", id)
			await(t, waits, id+" waits")
			granted[id] = c
		}
		m, mWaits := merge(t, g, "e2e", "M", "")
		await(t, mWaits, "M waits")
		granted["M"] = m
		give()
		for _, id := range []string{"M", "first", "high", "low", "loose"} {
			await(t, granted[id], "the grant of "+id)()
			order = append(order, id)
		}
		if want := []string{"M", "first", "high", "low", "loose"}; !slices.Equal(order, want) {
			t.Errorf("served %v, want %v", order, want)
		}
	})

	// Djinn's requests for a merge or a push go before the workers' that wait, whoever came first and whatever their
	// wish's rank, the first come first among them (Q26); the holder keeps its gate. Each waiter says which class it is
	// in and why it waits.
	t.Run("merge goes before workers", func(t *testing.T) {
		t.Parallel()
		f := &tasks{rank: map[string]int{"holder": 0, "W1": 0, "W2": 1, "M1": 3, "M2": 2}, works: map[string]bool{"holder": true, "W1": true, "W2": true}}
		g := New(nil, nil, f)
		give := await(t, func() <-chan func() { c, _ := take(t, g, "test", "holder"); return c }(), "first grant")
		w1, w1Waits := take(t, g, "test", "W1")
		if why := times(await(t, w1Waits, "W1 waits")); why != "held by holder, for Ns" {
			t.Errorf("W1 waits because %q", why)
		}
		w2, w2Waits := take(t, g, "test", "W2")
		await(t, w2Waits, "W2 waits")
		m1, m1Waits := merge(t, g, "test", "M1", "")
		if why := times(await(t, m1Waits, "M1 waits")); why != "a merge or a push, served before the workers: held by holder, for Ns" {
			t.Errorf("the merge waits because %q", why)
		}
		m2, m2Waits := merge(t, g, "test", "M2", "")
		await(t, m2Waits, "M2 waits")
		// Told of each merge as it comes, maybe of the second only.
		for why := ""; why != "held by holder, for Ns, then 2 merges or pushes go first"; {
			if why = times(await(t, w1Waits, "W1 told of the merges")); why != "held by holder, for Ns, then 1 merge or push goes first" &&
				why != "held by holder, for Ns, then 2 merges or pushes go first" {
				t.Fatalf("W1 waits behind the merges because %q", why)
			}
		}
		notGranted(t, m1, "a held gate, for a merge")

		give()
		order := make(chan string, 4)
		for _, c := range []struct {
			name    string
			granted <-chan func()
		}{{"M1", m1}, {"M2", m2}, {"W1", w1}, {"W2", w2}} {
			give := await(t, c.granted, "the grant of "+c.name)
			order <- c.name
			notGranted(t, w2, "a gate held")
			give()
		}
		close(order)
		var got []string
		for name := range order {
			got = append(got, name)
		}
		if want := []string{"M1", "M2", "W1", "W2"}; !slices.Equal(got, want) {
			t.Errorf("served %v, want %v", got, want)
		}
	})

	// A merge that waits for the memory keeps its free gate from a worker's lighter command, which says it waits for
	// the merge; on a machine full of workers holding their own gates, a merge gets another gate at once, and its gate
	// held outside the workers counts as a merge's (Outside).
	t.Run("merge keeps gate while waiting for memory", func(t *testing.T) {
		t.Parallel()
		f := &tasks{rank: map[string]int{"M": 0, "W1": 0, "W2": 0, "W3": 0}, works: map[string]bool{"W1": true, "W2": true, "W3": true}}
		m := calm()
		m.setFree(18 * machine.GiB / 10)
		g := New(m, peaks{"go tool task test": 5 * machine.GiB, "go tool task lint": 200 << 20}, f)
		g.tick = 10 * time.Millisecond

		heavy, heavyWaits := merge(t, g, "test", "M", "go tool task test")
		if why := await(t, heavyWaits, "the merge waits"); why != "a merge or a push, served before the workers: go tool task test peaks at 5.0 GiB, 1.8 GiB free" {
			t.Errorf("the merge waits because %q", why)
		}
		light, lightWaits := run(t, g, "test", "W1", "go tool task lint")
		if why := await(t, lightWaits, "the worker waits"); why != "a merge or a push goes first: go tool task test peaks at 5.0 GiB, 1.8 GiB free" {
			t.Errorf("the worker waits because %q", why)
		}
		notGranted(t, light, "a gate a merge waits for")
		m.setFree(8 * machine.GiB)
		giveTest := await(t, heavy, "the merge's grant once memory frees")
		notGranted(t, light, "a gate a merge holds")
		giveTest()
		await(t, light, "the worker's grant after the merge")()

		// The workers hold their gates in their own slots; the merge gets its own at once.
		var gives []func()
		for i, name := range []string{"e2e", "stack", "paid"} {
			c, _ := take(t, g, name, fmt.Sprintf("W%d", i+1))
			gives = append(gives, await(t, c, "the grant of "+name))
		}
		c, _ := merge(t, g, "lint", "M", "go tool task lint")
		gives = append(gives, await(t, c, "the merge's lint beside the workers' gates"))
		if n, merges := g.Outside(); n != 1 || merges != 1 {
			t.Errorf("outside the workers: %d gates, %d of merges", n, merges)
		}
		for _, give := range gives {
			give()
		}
	})

	// A gate goes to a measured command only when the machine holds its peak, the peaks of the commands holding a gate
	// counted as taken; a light command goes at once, and one never measured goes as it comes.
	t.Run("memory peak budget", func(t *testing.T) {
		t.Parallel()
		f := &tasks{rank: map[string]int{"W1": 0}}
		m := calm()
		m.setFree(18 * machine.GiB / 10)
		g := New(m, peaks{"go tool task e2e": 31 * machine.GiB / 10, "go tool task test": 5 * machine.GiB, "go tool task lint": 200 << 20}, f)
		g.tick = 10 * time.Millisecond

		heavy, waits := run(t, g, "e2e", "W1", "go tool task e2e")
		if why := await(t, waits, "reason"); why != "go tool task e2e peaks at 3.1 GiB, 1.8 GiB free" {
			t.Errorf("the heavy command waits because %q", why)
		}
		notGranted(t, heavy, "a command the machine cannot hold")
		if !slices.Contains(f.noted(), "W1 gate e2e: waiting: go tool task e2e peaks at 3.1 GiB, 1.8 GiB free") {
			t.Errorf("notes = %q", f.noted())
		}
		light, _ := run(t, g, "lint", "", "go tool task lint")
		await(t, light, "the light command's grant")()
		unmeasured, _ := run(t, g, "stack", "", "npm run everything")
		await(t, unmeasured, "the unmeasured command's grant")()

		m.setFree(8 * machine.GiB)
		giveE2E := await(t, heavy, "the heavy command's grant once memory frees")

		// The e2e run holds its gate: its peak counts as taken until it gives it back.
		test, waits := run(t, g, "test", "", "go tool task test")
		if why := await(t, waits, "reason"); why != "go tool task test peaks at 5.0 GiB, 8.0 GiB free, 3.1 GiB of it for the commands holding a gate" {
			t.Errorf("the second heavy command waits because %q", why)
		}
		notGranted(t, test, "a command beside a heavy holder")
		giveE2E()
		await(t, test, "the second heavy command's grant")()
	})

	// A waiter that leaves is forgotten; the gate goes to the next one.
	t.Run("waiter gives up on cancel", func(t *testing.T) {
		t.Parallel()
		g := New(nil, nil, nil)
		hold, err := g.Take(t.Context(), Request{Name: "e2e"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		left := make(chan error, 1)
		go func() { _, err := g.Take(ctx, Request{Name: "e2e"}, nil); left <- err }()
		time.Sleep(50 * time.Millisecond)
		cancel()
		if err := await(t, left, "the waiter leaving"); !errors.Is(err, context.Canceled) {
			t.Errorf("left with %v", err)
		}
		hold.Give()
		if l := g.List(); len(l) != 0 {
			t.Errorf("after all left: %+v", l)
		}
	})

	// A gate held past its timeout is taken back, its task told, and goes to the next waiter: a forgotten hold never
	// blocks the others.
	t.Run("timeout takes gate back", func(t *testing.T) {
		t.Parallel()
		f := &tasks{rank: map[string]int{"W1": 0, "W2": 0}}
		g := New(nil, nil, f)
		g.tick = 10 * time.Millisecond
		hold, err := g.Take(t.Context(), Request{Name: "e2e", TaskID: "W1", Timeout: 50 * time.Millisecond}, nil)
		if err != nil {
			t.Fatal(err)
		}
		next, _ := take(t, g, "e2e", "W2")
		await(t, hold.Done(), "the gate taken back")
		if why := hold.TakenBack(); why != "held past its timeout of 50ms" {
			t.Errorf("taken back because %q", why)
		}
		await(t, next, "the next waiter's grant")()
		hold.Give() // Taken back already: nothing to give.
		notes := f.noted()
		if !slices.Contains(notes, "W1 gate e2e: taken back: held past its timeout of 50ms") ||
			slices.ContainsFunc(notes, func(n string) bool { return strings.HasPrefix(n, "W1 gate e2e: given back") }) {
			t.Errorf("notes = %q", notes)
		}
	})

	// A lit ifrit holds its gate with no timeout: its expire time is its out time, pushed back by each action of the
	// developer, and djinn gate list says an ifrit holds it. Who waits is told it goes back when the developer leaves;
	// an ifrit that waits goes before no one, and Djinn's merge of its work, or a timeout it asks for, are what they
	// are for any task (Q11).
	t.Run("ifrit keeps gate until out time", func(t *testing.T) {
		t.Parallel()
		f := &tasks{rank: map[string]int{"W12": 0, "W13": 1, "W14": 0, "W15": 0}}
		out := time.Now().Add(30 * time.Minute).Truncate(time.Second)
		f.light("W12", out)
		f.light("W13", out)
		g := New(nil, nil, f)
		g.tick = 10 * time.Millisecond
		client := server(t, g)

		stack, _ := run(t, g, "stack", "W12", "task run-back")
		give := await(t, stack, "the ifrit's grant")
		first, waits := take(t, g, "stack", "W15")
		want := "held by ifrit W12: task run-back; released when you leave, at " + harness.ClockText(out, time.Now()) + " at the latest"
		if why := await(t, waits, "W15 waits"); why != want {
			t.Errorf("W15 waits because %q, want %q", why, want)
		}
		if !slices.Contains(f.noted(), "W15 gate stack: waiting: "+want) {
			t.Errorf("notes = %q", f.noted())
		}
		if iw := g.IfritWaits(); !slices.Equal(iw["W12"], []string{"W15, for stack"}) {
			t.Errorf("IfritWaits = %v", iw)
		}
		res, err := client.List(t.Context(), connect.NewRequest(&machinev1.GateServiceListRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		if gates := res.Msg.GetGates(); len(gates) != 1 || !gates[0].GetIfrit() || !gates[0].GetExpireTime().AsTime().Equal(out) ||
			gates[0].GetHolderTaskId() != "W12" {
			t.Errorf("djinn gate list: %v, want stack held by the ifrit W12 until %v", gates, out)
		}

		// The developer comes back: its gate is held until its new out time.
		later := out.Add(20 * time.Minute)
		f.light("W12", later)
		for deadline := time.Now().Add(5 * time.Second); !g.List()[0].Until.Equal(later); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("pushed back to %v: %+v", later, g.List())
			}
		}

		// The ifrit W13, of a lower wish, asked before W14: it is served after.
		ifrit, _ := take(t, g, "stack", "W13")
		notGranted(t, ifrit, "a held gate, for an ifrit")
		second, _ := take(t, g, "stack", "W14")
		for deadline := time.Now().Add(5 * time.Second); g.Waiting("W14") == nil; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("W14 does not wait")
			}
		}
		give()
		order := []string{}
		for _, c := range []struct {
			name    string
			granted <-chan func()
		}{{"W15", first}, {"W14", second}, {"W13", ifrit}} {
			give := await(t, c.granted, "the grant of "+c.name)
			order = append(order, c.name)
			give()
		}
		if want := []string{"W15", "W14", "W13"}; !slices.Equal(order, want) {
			t.Errorf("served %v, want %v", order, want)
		}

		// Djinn's merge of its work is no ifrit's gate; nor does a timeout it asks for go.
		lint := await(t, func() <-chan func() { c, _ := merge(t, g, "lint", "W12", "go tool task lint"); return c }(), "the merge's grant")
		if l := g.List(); len(l) != 1 || l[0].Ifrit || l[0].Until.Sub(l[0].Since) != DefaultTimeout {
			t.Errorf("the merge of an ifrit's work: %+v", l)
		}
		lint()
		hold, err := g.Take(t.Context(), Request{Name: "e2e", TaskID: "W12", Timeout: 50 * time.Millisecond}, nil)
		if err != nil {
			t.Fatal(err)
		}
		await(t, hold.Done(), "the gate taken back")
		if why := hold.TakenBack(); why != "held past its timeout of 50ms" {
			t.Errorf("taken back because %q", why)
		}
	})
}

// TestWaiting: Waiting says which gates a task waits for, held by another or kept by the pressure, until it gets
// them or stops waiting; a holder waits for nothing, and a waiter outside any task is no task's.
func TestWaiting(t *testing.T) {
	t.Parallel()
	f := &tasks{rank: map[string]int{"W1": 0, "W2": 0}}
	p := calm()
	g := New(p, nil, f)
	g.tick = 10 * time.Millisecond
	give := await(t, func() <-chan func() { c, _ := take(t, g, "test", "W1"); return c }(), "first grant")
	test, waits := take(t, g, "Test", "W2")
	await(t, waits, "reason")
	p.squeeze(true)
	ctx, cancel := context.WithCancel(t.Context())
	left := make(chan error, 1)
	go func() {
		_, err := g.Take(ctx, Request{Name: "e2e", TaskID: "W2"}, nil)
		left <- err
	}()
	_, outside := take(t, g, "e2e", "")
	await(t, outside, "reason outside any task")
	waiting := func() bool { return slices.Equal(g.Waiting("W2"), []string{"e2e", "test"}) }
	for deadline := time.Now().Add(5 * time.Second); !waiting() && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if !waiting() || g.Waiting("W1") != nil || g.Waiting("") != nil {
		t.Errorf("waiting: W1 %v, W2 %v, none %v", g.Waiting("W1"), g.Waiting("W2"), g.Waiting(""))
	}

	// Gone, it waits no more; granted, it holds instead.
	cancel()
	if err := await(t, left, "end of the wait"); !errors.Is(err, context.Canceled) {
		t.Errorf("the wait ended with %v", err)
	}
	if got := g.Waiting("W2"); !slices.Equal(got, []string{"test"}) {
		t.Errorf("W2 waits for %v once it left e2e", got)
	}
	p.squeeze(false)
	give()
	await(t, test, "second grant")()
	if got := g.Waiting("W2"); got != nil {
		t.Errorf("W2 still waits for %v once granted", got)
	}
}

// TestOutside: a gate held outside a running worker (no task, or a task whose worker does not run: a lead, a person)
// takes a slot; a running worker's gate is in the worker's own slot. Giving one back frees its slot (Freed), and the
// list shows which ones take a slot and until when they may be held.
func TestOutside(t *testing.T) {
	t.Parallel()
	f := &tasks{rank: map[string]int{"W1": 0, "W2": 0}, works: map[string]bool{"W1": true}}
	g := New(nil, nil, f)
	var freed atomic.Int32
	g.Freed(func() { freed.Add(1) })
	grant := func(name, taskID string) func() {
		c, _ := take(t, g, name, taskID)
		return await(t, c, "the grant of "+name)
	}
	worker := grant("test", "W1")
	if n, _ := g.Outside(); n != 0 {
		t.Errorf("a running worker's gate: %d outside", n)
	}
	direct, lead := grant("e2e", ""), grant("stack", "W2")
	if n, merges := g.Outside(); n != 2 || merges != 0 {
		t.Errorf("two gates held outside the workers: %d, %d of merges", n, merges)
	}
	l := g.List()
	if len(l) != 3 || !l[0].TakesSlot || !l[1].TakesSlot || l[2].TakesSlot || l[0].Until.Sub(l[0].Since) != DefaultTimeout {
		t.Errorf("list = %+v", l)
	}
	direct()
	if n, _ := g.Outside(); n != 1 || freed.Load() != 1 {
		t.Errorf("once given back: %d outside, freed %d times", n, freed.Load())
	}
	lead()
	worker()
}

// TestIntervals covers Gates.Intervals, Gates.AddInterval, and Gates.ProjectResolver.
func TestIntervals(t *testing.T) {
	t.Parallel()
	f := &tasks{rank: map[string]int{"W1": 0}}
	g := New(nil, nil, f)
	g.ProjectResolver(func(taskID, _ string) string { return "proj-" + taskID })

	old := time.Now().Add(-2 * time.Hour)
	g.AddInterval(&machinev1.GateInterval{
		Gate:        "old",
		ReleaseTime: timestamppb.New(old),
	})

	cutoff := time.Now().Add(-time.Minute)
	hold, err := g.Take(t.Context(), Request{Name: "test", TaskID: "W1", What: "go test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Intervals(cutoff); len(got) != 1 || got[0].GetGate() != "test" || got[0].GetProject() != "proj-W1" || got[0].GetReleaseTime() != nil {
		t.Errorf("while held: Intervals = %v", got)
	}
	hold.Give()
	if got := g.Intervals(cutoff); len(got) != 1 || got[0].GetGate() != "test" || got[0].GetProject() != "proj-W1" || got[0].GetReleaseTime() == nil {
		t.Errorf("once released: Intervals = %v", got)
	}
}
