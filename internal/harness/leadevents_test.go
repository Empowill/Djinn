package harness

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// leadQueue records the events Djinn queues for the lead (NotifyLead).
type leadQueue struct {
	mu     sync.Mutex
	events []*planv1.LeadEvent
}

func (q *leadQueue) notify(_ context.Context, e *planv1.LeadEvent) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.events = append(q.events, e)
	return nil
}

func (q *leadQueue) all() []*planv1.LeadEvent {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.events)
}

// TestFailedWorkerQueuedForTheLead: with NotifyLead, a failed worker is an event of its own in the lead's queue, of
// kind failed, with its code and its line, and nothing is typed.
func TestFailedWorkerQueuedForTheLead(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir())
	typed, queue := &leadListener{}, &leadQueue{}
	e.h.TellLeads(typed.tell)
	e.h.NotifyLead(queue.notify)
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Build parser", Prompt: "fail syntax error on line 42",
		Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	task := res.Msg.GetTask()
	e.watch(t.Context(), t, task.GetId(), 0)
	waitFor(t, "the failure queued", func() bool { return len(queue.all()) > 0 })
	got := queue.all()[0]
	if got.GetKind() != planv1.LeadEventKind_LEAD_EVENT_KIND_FAILED || got.GetCode() != task.GetCode() ||
		got.GetWishId() != wishID || !strings.Contains(got.GetText(), "syntax error on line 42") {
		t.Errorf("queued %v", got)
	}
	if p := typed.all(); len(p) != 0 {
		t.Errorf("typed %q with a queue", p)
	}
}

// TestRestartQueuedForTheLead: djinn up queues the restart for the lead, with its version, the workers the last
// djinn up left running, and those Djinn resumes by itself.
func TestRestartQueuedForTheLead(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	e := up(t, home)
	running := &planv1.Task{Id: store.NewID(), WishId: store.NewID(), Code: "W7", Title: "cut short",
		Status: planv1.TaskStatus_TASK_STATUS_RUNNING, StartTime: timestamppb.Now()}
	putTask(t, e.db, running, "work")
	e.down()
	e = up(t, home, WithVersion("v9.9.9"))
	defer e.down()
	events, err := store.List[*planv1.LeadEvent](t.Context(), e.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	var restarts []string
	for _, ev := range events {
		if ev.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_RESTART && ev.GetAckSessionId() == "" {
			restarts = append(restarts, ev.GetText())
		}
	}
	want := "Djinn: restarted on version v9.9.9: 1 worker interrupted (W7); not resumed, the move is yours: W7."
	if len(restarts) != 2 || restarts[1] != want {
		t.Errorf("restart events %q, want the second %q", restarts, want)
	}
}

func TestRestartLine(t *testing.T) {
	for _, c := range []struct {
		version                        string
		interrupted, resumed, watchers []string
		want                           string
	}{
		{"v1.2.0", nil, nil, nil, "Djinn: restarted on version v1.2.0: no worker was running."},
		{"", []string{"W1"}, []string{"W1"}, nil, "Djinn: restarted on version unknown: 1 worker interrupted (W1), resumed by Djinn: W1."},
		{"v1.2.0", []string{"W1", "W2", "W3"}, []string{"W1", "W3"}, nil,
			"Djinn: restarted on version v1.2.0: 3 workers interrupted (W1, W2, W3), resumed by Djinn: W1, W3; " +
				"not resumed, the move is yours: W2."},
		// Babysit PR #2, 12/10/2026: W9, a watcher, came back, and the line said no worker was running.
		{"v1.2.0", nil, nil, []string{"W9"}, "Djinn: restarted on version v1.2.0: 1 watcher resumed: W9."},
		// Stopped with the last djinn up, a worker Djinn resumes was interrupted all the same.
		{"v1.2.0", nil, []string{"W4"}, []string{"W9", "W12"},
			"Djinn: restarted on version v1.2.0: 1 worker interrupted (W4), resumed by Djinn: W4; 2 watchers resumed: W9, W12."},
	} {
		if got := restartLine(c.version, c.interrupted, c.resumed, c.watchers); got != c.want {
			t.Errorf("restartLine(%q, %v, %v, %v)\n got %s\nwant %s", c.version, c.interrupted, c.resumed, c.watchers, got, c.want)
		}
	}
}
