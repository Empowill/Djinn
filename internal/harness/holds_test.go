package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/gate"
	"github.com/empowill/djinn/internal/harness"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// holdsWish is the wish whose lead hears the long holds of its tasks.
const holdsWish = "wish-holds"

// heard is the lead's queue in a test of long holds: every event Djinn queued for it.
type heard struct {
	mu     sync.Mutex
	events []*planv1.LeadEvent
}

func (q *heard) notify(_ context.Context, e *planv1.LeadEvent) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.events = append(q.events, e)
	return nil
}

func (q *heard) all() []*planv1.LeadEvent {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.events)
}

// held is fake costs: each command's usual hold, the 95th percentile measured; a command not in it was never measured.
type held map[string]time.Duration

func (c held) Cost(_ context.Context, _, _, command string) machine.Cost {
	return machine.Cost{Held: c[command]}
}

func (held) Ask(context.Context, string, string, string, string) {}

func (held) Record(context.Context, *machinev1.CommandServiceRecordRequest) {}

// holdsHarness is a harness on a store of its own, with the tasks given, whose leads' events go to the queue it
// returns. It runs no scheduler: the gates only ask it about its tasks.
func holdsHarness(t *testing.T, tasks ...*planv1.Task) (*harness.Harness, *heard) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Tx(t.Context(), func(tx *store.Tx) error {
		for _, task := range tasks {
			if err := tx.Journal("test", "put", task); err != nil {
				return err
			}
			if err := tx.Put(task); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := harness.New(db, "", nil)
	t.Cleanup(h.Close)
	q := &heard{}
	h.NotifyLead(q.notify)
	return h, q
}

// TestHoldsLong (G5 of L04, W14): a gate held with no timeout, past 3 times its command's p95 hold (10 minutes at
// least) or an hour when the command was never measured, has the lead of its task's wish told once, as a health
// event, and is never taken back. The gates and the harness are the real ones, on the real thresholds; time is the
// bubble's.
func TestHoldsLong(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		what  string
		usual time.Duration // the command's p95 hold; 0: never measured
		long  time.Duration // when the lead hears it
		why   string
	}{
		{"3 times its p95", "go tool task e2e", 5 * time.Minute, 15 * time.Minute,
			"past 3 times its usual hold of 5m0s"},
		{"10 minutes at least", "go tool task lint", time.Minute, gate.MinLongHold,
			"past 3 times its usual hold of 1m0s"},
		{"an hour unmeasured", "./bench.sh", 0, gate.UnmeasuredLong, "its command never measured"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, lead := holdsHarness(t, &planv1.Task{Id: "task-1", WishId: holdsWish, Code: "W1", Title: "Gates"})
			costs := held{}
			if tt.usual > 0 {
				costs[tt.what] = tt.usual
			}
			synctest.Test(t, func(t *testing.T) {
				g := gate.New(nil, costs, h)
				hold, err := g.Take(t.Context(), gate.Request{Name: "e2e", TaskID: "task-1", What: tt.what, PID: os.Getpid()}, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer hold.Give()

				time.Sleep(tt.long - time.Second)
				synctest.Wait()
				if got := lead.all(); len(got) != 0 {
					t.Fatalf("the lead heard %v a second before %s", got, tt.long)
				}

				time.Sleep(2 * time.Second)
				synctest.Wait()
				want := &planv1.LeadEvent{
					Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_HEALTH, WishId: holdsWish, Code: "W1",
					Text: "Djinn: W1 (Gates): " + tt.what + " has held gate e2e for " + tt.long.String() + ", " + tt.why +
						"; nothing is taken back: see whether it hangs.",
				}
				if got := lead.all(); len(got) != 1 || !sameEvent(got[0], want) {
					t.Fatalf("the lead heard %v; want %v", got, want)
				}

				// Hours later, the lead heard it once, and the holder still holds its gate.
				time.Sleep(3 * time.Hour)
				synctest.Wait()
				if got := lead.all(); len(got) != 1 {
					t.Errorf("the lead heard %d events: %v", len(got), got)
				}
				select {
				case <-hold.Done():
					t.Fatalf("a long hold taken back: %s", hold.TakenBack())
				default:
				}
				if l := g.List(); len(l) != 1 || l[0].Name != "e2e" || !l[0].Until.IsZero() {
					t.Errorf("held: %+v", l)
				}
			})
		})
	}

	// A hold with a timeout is taken back at its timeout, and the lead hears nothing of it.
	t.Run("timeout", func(t *testing.T) {
		t.Parallel()
		h, lead := holdsHarness(t, &planv1.Task{Id: "task-1", WishId: holdsWish, Code: "W1", Title: "Gates"})
		synctest.Test(t, func(t *testing.T) {
			g := gate.New(nil, held{}, h)
			hold, err := g.Take(t.Context(), gate.Request{
				Name: "e2e", TaskID: "task-1", What: "./bench.sh", PID: os.Getpid(), Timeout: 2 * time.Hour,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(3 * time.Hour)
			synctest.Wait()
			select {
			case <-hold.Done():
			default:
				t.Error("a hold past its timeout not taken back")
			}
			if got := lead.all(); len(got) != 0 {
				t.Errorf("the lead heard %v", got)
			}
		})
	})
}

// TestHoldsLongNoLead (W14): a long hold of a task part of no wish, or unknown, is logged; no lead hears it. The
// lead of a task's wish hears its line whole.
func TestHoldsLongNoLead(t *testing.T) {
	t.Parallel()
	h, lead := holdsHarness(t, &planv1.Task{Id: "task-1", Code: "W1"},
		&planv1.Task{Id: "task-3", WishId: holdsWish, Code: "W3"})
	synctest.Test(t, func(*testing.T) {
		h.HoldsLong("task-1", "Djinn: W1 has held gate e2e for 1h0m0s")
		h.HoldsLong("task-gone", "Djinn: W2 has held gate e2e for 1h0m0s")
		h.HoldsLong("task-3", "Djinn: W3 has held gate e2e for 1h0m0s")
		synctest.Wait()
	})
	want := &planv1.LeadEvent{
		Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_HEALTH, WishId: holdsWish, Code: "W3",
		Text: "Djinn: W3 has held gate e2e for 1h0m0s",
	}
	if got := lead.all(); len(got) != 1 || !sameEvent(got[0], want) {
		t.Errorf("the lead heard %v; want %v", got, want)
	}
}

// sameEvent says whether the lead event got is want in kind, wish, code and text.
func sameEvent(got, want *planv1.LeadEvent) bool {
	return got.GetKind() == want.GetKind() && got.GetWishId() == want.GetWishId() && got.GetCode() == want.GetCode() &&
		got.GetText() == want.GetText()
}
