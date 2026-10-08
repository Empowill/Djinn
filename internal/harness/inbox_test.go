package harness

import (
	"context"
	"slices"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

// TestSendToRunningWorker: a message sent while the worker works is an event of the task, journaled as the
// command that sent it, and the worker's next words come after a "received" event.
func TestSendToRunningWorker(t *testing.T) {
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())
	task := e.spawn(t, wishID, "text working\nsleep 500ms\ntext after")

	s, err := e.tasks.Watch(t.Context(), connect.NewRequest(&planv1.TaskServiceWatchRequest{TaskId: task.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var events []*planv1.TaskEvent
	for s.Receive() {
		events = append(events, s.Msg().GetEvent())
		if s.Msg().GetEvent().GetText() == "working" {
			break
		}
	}
	res, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{
		TaskId: task.GetId(), Text: "also update the docs",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if ev := res.Msg.GetEvent(); ev.GetKind() != planv1.TaskEventKind_TASK_EVENT_KIND_MESSAGE || ev.GetText() != "also update the docs" {
		t.Errorf("sent event = %v", ev)
	}
	for s.Receive() {
		events = append(events, s.Msg().GetEvent())
	}
	want := []string{"PROMPT", "STATUS", "STATUS", "TEXT", "MESSAGE", "RECEIVED", "TEXT", "TEXT", "STATUS"}
	if got := eventKinds(events); !slices.Equal(got, want) {
		t.Fatalf("events = %v\nwant %v", got, want)
	}
	checkSeqs(t, events, 1)
	if events[5].GetText() != "also update the docs" || events[6].GetText() != "after" ||
		events[7].GetText() != "received: also update the docs" {
		t.Errorf("received %q, then %q, %q", events[5].GetText(), events[6].GetText(), events[7].GetText())
	}

	// The journal holds the command as the user sent it.
	sent, err := store.Commands(t.Context(), e.db, func(c store.Command) bool {
		return c.Method == planv1connect.TaskServiceSendProcedure
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].Actor != actorLocal {
		t.Errorf("journal = %+v", sent)
	}

	// The worker has ended: it takes no more messages.
	if _, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{
		TaskId: task.GetId(), Text: "too late",
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("send to a finished task: %v", err)
	}
}

// TestSendUnacknowledged: a worker that says nothing after a message never acknowledges it.
func TestSendUnacknowledged(t *testing.T) {
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())
	task := e.spawn(t, wishID, "text working\nsleep 1h")
	watched := make(chan []*planv1.TaskEvent)
	go func() { watched <- e.watch(context.Background(), t, task.GetId(), 0) }()
	if _, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{
		TaskId: task.GetId(), Text: "stop soon",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
		t.Fatal(err)
	}
	events := <-watched
	kinds := eventKinds(events)
	if !slices.Contains(kinds, "MESSAGE") || slices.Contains(kinds, "RECEIVED") {
		t.Errorf("events = %v: want a message, never received", kinds)
	}
	checkSeqs(t, events, 1)
}

func TestSendRefused(t *testing.T) {
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())
	task := e.spawn(t, wishID, "text quick")
	e.watch(t.Context(), t, task.GetId(), 0)
	for _, tt := range []struct {
		name string
		req  *planv1.TaskServiceSendRequest
		code connect.Code
	}{
		{"no text", &planv1.TaskServiceSendRequest{TaskId: task.GetId()}, connect.CodeInvalidArgument},
		{"unknown task", &planv1.TaskServiceSendRequest{TaskId: store.NewID(), Text: "x"}, connect.CodeNotFound},
		{"finished task", &planv1.TaskServiceSendRequest{TaskId: task.GetId(), Text: "x"}, connect.CodeFailedPrecondition},
	} {
		if _, err := e.tasks.Send(t.Context(), connect.NewRequest(tt.req)); connect.CodeOf(err) != tt.code {
			t.Errorf("%s: %v, want %v", tt.name, err, tt.code)
		}
	}
}
