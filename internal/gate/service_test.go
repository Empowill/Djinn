package gate

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
)

// TestService_Hold covers (*service).Hold and (*service).List: process holder measurement and take-back when the
// process ends, lit ifrit take-back when the ifrit goes out or ends, and worker process tracking.
func TestService_Hold(t *testing.T) {
	t.Parallel()

	// A gate held directly, by a process Djinn knows, is measured while it is held, and taken back once that process
	// ends: the stream says so and ends, and the slot frees.
	t.Run("process holder measured and taken back when process ends", func(t *testing.T) {
		t.Parallel()
		g := New(nil, nil, nil)
		g.tick = 10 * time.Millisecond
		read := Read(func(pid int) (machine.Group, error) { return machine.ReadWorker(pid, "") })
		if machine.NotMeasured != "" {
			read = func(int) (machine.Group, error) { return machine.Group{Processes: 1, Memory: 50 << 20}, nil }
		}
		g.Measure(10*time.Millisecond, read)
		freed := make(chan struct{}, 4)
		g.Freed(func() { freed <- struct{}{} })
		client := server(t, g)

		holder := exec.Command(os.Args[0])
		holder.Env = append(os.Environ(), helperEnv+"=sleep")
		if err := holder.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = holder.Process.Kill() })
		pid := holder.Process.Pid
		stream, err := client.Hold(t.Context(), connect.NewRequest(&machinev1.GateServiceHoldRequest{
			Name: "e2e", What: "by hand", Pid: int32(pid),
		}))
		if err != nil {
			t.Fatal(err)
		}
		if !stream.Receive() || stream.Msg().GetState() != machinev1.GateState_GATE_STATE_HELD {
			t.Fatalf("hold: %v, %v", stream.Msg(), stream.Err())
		}
		var gate *machinev1.Gate
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			res, err := client.List(t.Context(), connect.NewRequest(&machinev1.GateServiceListRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			if gate = res.Msg.GetGates()[0]; gate.GetResources().GetMemoryBytes() > 0 {
				break
			}
		}
		if n, _ := g.Outside(); !gate.GetTakesSlot() || gate.GetResources().GetProcesses() < 1 || gate.GetResources().GetMemoryBytes() == 0 ||
			gate.GetExpireTime().AsTime().Sub(gate.GetSince().AsTime()) != DefaultTimeout || n != 1 {
			t.Fatalf("held by process %d: %v", pid, gate)
		}

		_ = holder.Process.Kill()
		_ = holder.Wait()
		if !stream.Receive() || stream.Msg().GetState() != machinev1.GateState_GATE_STATE_TAKEN_BACK ||
			stream.Msg().GetReason() != fmt.Sprintf("gate e2e taken back: its process %d ended", pid) {
			t.Fatalf("once its holder ended: %v, %v", stream.Msg(), stream.Err())
		}
		if stream.Receive() {
			t.Errorf("the stream goes on: %v", stream.Msg())
		}
		waitFree(t, g)
		await(t, freed, "the slot freed")
		if n, _ := g.Outside(); n != 0 {
			t.Errorf("%d gates still held outside", n)
		}
	})

	// The gate a lit ifrit holds goes back once it goes out, the stream and its events saying why, and to the next
	// waiter; once it ends, as well.
	t.Run("ifrit gate taken back when ifrit goes out or ends", func(t *testing.T) {
		t.Parallel()
		const ifrit = "01a144ba-1ff6-73e6-8373-89464355150f"
		f := &tasks{rank: map[string]int{ifrit: 0, "W15": 0}}
		f.light(ifrit, time.Now().Add(30*time.Minute))
		g := New(nil, nil, f)
		g.tick = 10 * time.Millisecond
		client := server(t, g)

		stream, err := client.Hold(t.Context(), connect.NewRequest(&machinev1.GateServiceHoldRequest{
			Name: "stack", TaskId: ifrit, What: "task run-back",
		}))
		if err != nil {
			t.Fatal(err)
		}
		if !stream.Receive() || stream.Msg().GetState() != machinev1.GateState_GATE_STATE_HELD {
			t.Fatalf("hold: %v, %v", stream.Msg(), stream.Err())
		}
		next, _ := take(t, g, "stack", "W15")
		notGranted(t, next, "a gate a lit ifrit holds")
		f.putOut(ifrit, "went out: you left Djinn alone at 10:00")
		if !stream.Receive() || stream.Msg().GetState() != machinev1.GateState_GATE_STATE_TAKEN_BACK ||
			stream.Msg().GetReason() != "gate stack taken back: its ifrit went out: you left Djinn alone at 10:00" {
			t.Fatalf("once it went out: %v, %v", stream.Msg(), stream.Err())
		}
		await(t, next, "the next waiter's grant")()
		if !slices.Contains(f.noted(), ifrit+" gate stack: taken back: its ifrit went out: you left Djinn alone at 10:00") {
			t.Errorf("notes = %q", f.noted())
		}

		// Lit again, then ended.
		f.light(ifrit, time.Now().Add(30*time.Minute))
		hold, err := g.Take(t.Context(), Request{Name: "stack", TaskID: ifrit}, nil)
		if err != nil {
			t.Fatal(err)
		}
		f.putOut(ifrit, "ended")
		await(t, hold.Done(), "the gate taken back")
		if why := hold.TakenBack(); why != "its ifrit ended" {
			t.Errorf("taken back because %q", why)
		}
		waitFree(t, g)
	})

	// A worker's djinn gate run is told to djinn up's tracker while it holds the gate, and forgotten once it gives it
	// back, so that its command never outlives djinn up; a command run by hand is not (W514).
	t.Run("tracks worker command and not developer command", func(t *testing.T) {
		t.Parallel()
		g := New(calm(), nil, &tasks{rank: map[string]int{"t1": 0}})
		var mu sync.Mutex
		var tracked []string
		g.Track(func(pid int, taskID string) func() {
			mu.Lock()
			defer mu.Unlock()
			tracked = append(tracked, fmt.Sprintf("+%d %s", pid, taskID))
			return func() {
				mu.Lock()
				defer mu.Unlock()
				tracked = append(tracked, fmt.Sprintf("-%d", pid))
			}
		})
		seen := func() []string {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(tracked)
		}
		var worker atomic.Bool
		mux := http.NewServeMux()
		mux.Handle(Handler(g))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if worker.Load() {
				r = r.WithContext(plan.WithCaller(r.Context(), &plan.WorkerCaller{TaskID: "t1"}))
			}
			mux.ServeHTTP(w, r)
		}))
		t.Cleanup(srv.Close)
		client := machinev1connect.NewGateServiceClient(srv.Client(), srv.URL)
		pid := os.Getpid()
		for _, c := range []struct {
			worker bool
			want   []string
		}{
			{true, []string{fmt.Sprintf("+%d t1", pid), fmt.Sprintf("-%d", pid)}},
			// The developer's own djinn gate run: its command is theirs.
			{false, nil},
		} {
			worker.Store(c.worker)
			mu.Lock()
			tracked = nil
			mu.Unlock()
			ctx, cancel := context.WithCancel(t.Context())
			stream, err := client.Hold(ctx, connect.NewRequest(&machinev1.GateServiceHoldRequest{Name: "test", What: "go test", Pid: int32(pid)}))
			if err != nil {
				t.Fatal(err)
			}
			for stream.Receive() && stream.Msg().GetState() != machinev1.GateState_GATE_STATE_HELD {
			}
			if got := seen(); c.worker && len(got) != 1 || !c.worker && len(got) != 0 {
				t.Errorf("worker %v: tracked %v while held", c.worker, got)
			}
			cancel()
			stream.Close()
			waitFree(t, g)
			deadline := time.Now().Add(5 * time.Second)
			for len(seen()) != len(c.want) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if got := seen(); !slices.Equal(got, c.want) {
				t.Errorf("worker %v: tracked %v, want %v", c.worker, got, c.want)
			}
		}

		// A worker cannot hold a gate for another task.
		worker.Store(true)
		stream, err := client.Hold(t.Context(), connect.NewRequest(&machinev1.GateServiceHoldRequest{
			Name:   "test",
			TaskId: "01a144ba-1ff6-73e6-8373-89464355150f",
			What:   "go test",
		}))
		if err == nil {
			_ = stream.Receive()
			err = stream.Err()
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("worker holding gate for another task: %v", err)
		}
	})
}
