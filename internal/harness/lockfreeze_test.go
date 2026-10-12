package harness

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/testx"
)

// measuredFake is the fake agent measured as a process, as djinn up's agents are: its pump reads it every
// measureEvery.
type measuredFake struct{ Fake }

func (p measuredFake) Start(ctx context.Context, spec Spec) (Worker, error) {
	w, err := p.Fake.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	return measuredWorker{w.(*fakeWorker)}, nil
}

type measuredWorker struct{ *fakeWorker }

func (measuredWorker) PID() int       { return os.Getpid() }
func (measuredWorker) Cgroup() string { return "" }

// within runs f and panics with every goroutine's stack when it has not returned after d: a freeze would hang the
// test, and its cleanup, until go test's timeout.
func within(d time.Duration, what string, f func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(d):
		buf := make([]byte, 1<<22)
		buf = buf[:runtime.Stack(buf, true)]
		// From a goroutine of its own: the test's deferred calls would wait for the frozen lock.
		go panic(fmt.Sprintf("%s did not return in %s: frozen\n\n%s", what, d, buf))
		<-done
	}
}

// TestFreezeInvestigatorWithAGateHeld reproduces W549's freeze: an investigator starts while every slot is taken and a
// gate is held. slotsFull held h.mu while it asked the gates how many they hold outside a worker, and the gates ask the
// harness back whether each holder works (Works), which locks h.mu again: the spawn froze with h.mu held, every pump
// stopped at its next measure, each worker's stream reader at its next event, and every call behind them. The work
// task floods its stream, its pump measures it every millisecond, the scheduler passes and the machine reads what the
// workers use, all along.
func TestFreezeInvestigatorWithAGateHeld(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	providers := testProviders()
	providers[planv1.Provider_PROVIDER_FAKE] = measuredFake{}
	measure := func(int, string) (machine.Group, error) {
		return machine.Group{CPU: time.Duration(time.Now().UnixNano()), Memory: 300 << 20, Processes: 1}, nil
	}
	e := upWith(t, t.TempDir(), providers, WithEnlightenWorkers(), WithCapacity((&limit{slots: 1}).capacity),
		WithTick(5*time.Millisecond), WithMeasure(time.Millisecond, measure))
	wishID, _ := e.wish(t, fakeProject(t, ""))

	// The work task holds a gate (its lint), as djinn up's gates count it: one outside a worker unless its holder works.
	var holder atomic.Pointer[string]
	e.h.GatesOutside(func() int {
		if id := holder.Load(); id != nil && !e.h.Works(*id) {
			return 1
		}
		return 0
	})
	flood := strings.Repeat("text a line of the flood\n", 300) + "wait"
	w1 := e.mustSpawn(t, wishID, "Flood", flood, nil)
	id := w1.GetId()
	holder.Store(&id)
	e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	// The machine reads the workers all along: each machine snapshot (Resident), the window's load (Uses).
	ctx, stop := context.WithCancel(t.Context())
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for ctx.Err() == nil {
				e.h.Resident()
				e.h.Uses()
				time.Sleep(time.Millisecond)
			}
		})
	}
	defer readers.Wait()
	defer stop()

	q := e.asker(wishID)
	asked := q.ask(t, "wait")
	within(20*time.Second, "enlighten while a gate is held and every slot taken", func() {
		q.enlighten(t, asked.GetCode(), "Investigate the freeze")
	})
	investigators := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
	if len(investigators) != 1 {
		t.Fatalf("investigators = %d, want 1", len(investigators))
	}
	inv := investigators[0]
	e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_PAUSED))
	e.until(t, inv.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	within(5*time.Second, "Resident", func() { e.h.Resident() })
	if e.h.Resident() == 0 {
		t.Error("Resident = 0: the work task was never measured")
	}

	e.release(t, inv.GetId())
	e.ended(t, inv.GetId())
	e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	e.release(t, id)
	e.ended(t, id)
}
