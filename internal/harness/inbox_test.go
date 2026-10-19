package harness

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestSend verifies sending messages to workers, acknowledgement rules, and concurrent event ordering.
func TestSend(t *testing.T) {
	t.Parallel()

	// A message sent while the worker works is an event of the task, journaled as the command that sent it, and the
	// worker's next words come after a "received" event.
	t.Run("running_worker", func(t *testing.T) {
		testx.Portable(t)
		t.Parallel()
		e := up(t, t.TempDir())
		wishID, _ := e.wish(t, t.TempDir())
		task := e.spawn(t, wishID, "text working\nwait\ntext after")

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

		sent, err := store.Commands(t.Context(), e.db, func(c store.Command) bool {
			return c.Method == planv1connect.TaskServiceSendProcedure
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(sent) != 1 || sent[0].Actor != actorLocal {
			t.Errorf("journal = %+v", sent)
		}

		if _, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{
			TaskId: task.GetId(), Text: "too late",
		})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("send to a finished task: %v", err)
		}
	})

	// A worker that says nothing after a message never acknowledges it.
	t.Run("unacknowledged", func(t *testing.T) {
		testx.Portable(t)
		t.Parallel()
		e := up(t, t.TempDir())
		wishID, _ := e.wish(t, t.TempDir())
		task := e.spawn(t, wishID, "text working\nsleep 1h")
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
		if _, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{
			TaskId: task.GetId(), Text: "stop soon",
		})); err != nil {
			t.Fatal(err)
		}
		if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
			t.Fatal(err)
		}
		for s.Receive() {
			events = append(events, s.Msg().GetEvent())
		}
		kinds := eventKinds(events)
		if !slices.Contains(kinds, "MESSAGE") || slices.Contains(kinds, "RECEIVED") {
			t.Errorf("events = %v: want a message, never received", kinds)
		}
		checkSeqs(t, events, 1)
	})

	// Events written outside a worker's run while it streams and a message sent meanwhile are all stored, each at its own position.
	t.Run("events_beside_run", func(t *testing.T) {
		testx.Portable(t)
		t.Parallel()
		e := up(t, t.TempDir())
		wishID, _ := e.wish(t, t.TempDir())
		const lines, beside = 150, 60
		var script strings.Builder
		for i := range lines {
			fmt.Fprintf(&script, "text a%d\n", i)
		}
		script.WriteString("wait\n")
		for i := range lines {
			fmt.Fprintf(&script, "text b%d\n", i)
		}
		task := e.spawn(t, wishID, script.String())
		watched := make(chan []*planv1.TaskEvent)
		go func() { watched <- e.watch(context.Background(), t, task.GetId(), 0) }()
		var wg sync.WaitGroup
		wg.Go(func() {
			for i := range beside {
				e.h.writeAlone(context.Background(), actorHarness, methodEvent, nil, task.GetId(), nil,
					Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: fmt.Sprintf("beside %d", i)})
			}
		})
		if _, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{
			TaskId: task.GetId(), Text: "go on",
		})); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		<-watched

		stored, err := store.List[*planv1.TaskEvent](t.Context(), e.db, store.Where{"task_id": task.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		slices.SortFunc(stored, func(a, b *planv1.TaskEvent) int { return cmp.Compare(a.GetSeq(), b.GetSeq()) })
		checkSeqs(t, stored, 1)
		texts := map[string]int{}
		for _, ev := range stored {
			texts[ev.GetKind().String()+" "+ev.GetText()]++
		}
		var want []string
		for i := range lines {
			want = append(want, fmt.Sprintf("TASK_EVENT_KIND_TEXT a%d", i), fmt.Sprintf("TASK_EVENT_KIND_TEXT b%d", i))
		}
		for i := range beside {
			want = append(want, fmt.Sprintf("TASK_EVENT_KIND_STATUS beside %d", i))
		}
		want = append(want, "TASK_EVENT_KIND_MESSAGE go on", "TASK_EVENT_KIND_RECEIVED go on")
		for _, w := range want {
			if texts[w] != 1 {
				t.Errorf("%q stored %d times, want once", w, texts[w])
			}
		}
	})

	t.Run("refused", func(t *testing.T) {
		testx.Portable(t)
		t.Parallel()
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
	})

	// On Antigravity, a message delivered while a turn is under way is marked RECEIVED only after TASK_EVENT_KIND_USAGE
	// closes the turn and the next turn emits text or a tool call (W405).
	t.Run("agy_acknowledge_next_turn", func(t *testing.T) {
		testx.Portable(t)
		t.Parallel()
		providers := testProviders()
		providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = Fake{}
		e := upWith(t, t.TempDir(), providers)
		wishID, _ := e.wish(t, t.TempDir())
		task := e.mustSpawn(t, wishID, "Agy turn ack", "text step1\nwait\ntext step2\nusage 100 50 0.01\ntext turn2",
			&planv1.TaskServiceSpawnRequest{Provider: planv1.Provider_PROVIDER_ANTIGRAVITY})

		s, err := e.tasks.Watch(t.Context(), connect.NewRequest(&planv1.TaskServiceWatchRequest{TaskId: task.GetId()}))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		var events []*planv1.TaskEvent
		for s.Receive() {
			events = append(events, s.Msg().GetEvent())
			if s.Msg().GetEvent().GetText() == "step1" {
				break
			}
		}
		if _, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{
			TaskId: task.GetId(), Text: "update docs",
		})); err != nil {
			t.Fatal(err)
		}
		for s.Receive() {
			events = append(events, s.Msg().GetEvent())
		}
		want := []string{"PROMPT", "STATUS", "STATUS", "TEXT", "MESSAGE", "TEXT", "USAGE", "RECEIVED", "TEXT", "TEXT", "STATUS"}
		if got := eventKinds(events); !slices.Equal(got, want) {
			t.Fatalf("events = %v\nwant %v", got, want)
		}
		if events[5].GetText() != "step2" || events[7].GetText() != "update docs" || events[8].GetText() != "turn2" {
			t.Errorf("got step2=%q, received=%q, turn2=%q", events[5].GetText(), events[7].GetText(), events[8].GetText())
		}
	})
}
