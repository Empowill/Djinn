package gate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/store"
)

// TestService_History: the history of the gates reads the intervals the store keeps over the window asked, an hour by
// default, 6 hours or a day, each with its task's kind and code; and the runs of the workers over the window, an open
// run only while its task runs.
func TestService_History(t *testing.T) {
	t.Parallel()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "djinn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now()
	at := func(ago time.Duration) *timestamppb.Timestamp { return timestamppb.New(now.Add(-ago)) }
	log := machine.NewIntervals(db)
	for _, iv := range []struct {
		task string
		ago  time.Duration
	}{{"w1", 30 * time.Minute}, {"w2", 3 * time.Hour}, {"", 12 * time.Hour}, {"gone", 20 * time.Hour}, {"w1", 30 * time.Hour}} {
		log.Add(t.Context(), &machinev1.GateInterval{
			Gate: "e2e", Mode: machinev1.GateMode_GATE_MODE_EXCLUSIVE, TaskId: iv.task, Command: iv.ago.String(),
			AskedTime: at(iv.ago + time.Minute), GrantTime: at(iv.ago + 30*time.Second), ReleaseTime: at(iv.ago),
		})
	}
	g := New(nil, nil, &tasks{})
	g.Keep(log)

	run := func(from, to time.Duration) *planv1.TaskRun {
		r := &planv1.TaskRun{StartTime: at(from)}
		if to >= 0 {
			r.EndTime = at(to)
		}
		return r
	}
	running, done := planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_DONE
	past := []*planv1.Task{
		// Ran 2 hours ago, then since 10 minutes: it runs.
		{Id: "w1", Code: "W1", Kind: planv1.TaskKind_TASK_KIND_INVESTIGATION, Status: running,
			Runs: []*planv1.TaskRun{run(2*time.Hour, time.Hour), run(10*time.Minute, -1)}},
		// No kind: work. Ended, its last run never closed: left out.
		{Id: "w2", Code: "W2", Status: done, Runs: []*planv1.TaskRun{run(4*time.Hour, 3*time.Hour), run(time.Hour, -1)}},
		// Ended 25 hours ago: out of a day.
		{Id: "w3", Code: "W3", Kind: planv1.TaskKind_TASK_KIND_WATCHER, Status: done,
			Runs: []*planv1.TaskRun{run(26*time.Hour, 25*time.Hour)}},
		// Running, an older run left open: only its last counts.
		{Id: "w4", Code: "W4", Kind: planv1.TaskKind_TASK_KIND_MOCKUP, Status: running,
			Runs: []*planv1.TaskRun{run(5*time.Hour, -1), run(20*time.Minute, -1)}},
	}
	mux := http.NewServeMux()
	mux.Handle(Handler(g, WithPast(func(context.Context) ([]*planv1.Task, error) { return past, nil })))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := machinev1connect.NewGateServiceClient(srv.Client(), srv.URL)

	type runOf struct {
		code string
		kind planv1.TaskKind
		open bool
	}
	work, inv := planv1.TaskKind_TASK_KIND_WORK, planv1.TaskKind_TASK_KIND_INVESTIGATION
	mock := planv1.TaskKind_TASK_KIND_MOCKUP
	for _, tc := range []struct {
		minutes  int32
		commands []string
		runs     []runOf
	}{
		{0, []string{"30m0s"}, []runOf{{"W4", mock, true}, {"W1", inv, true}}},
		{60, []string{"30m0s"}, []runOf{{"W4", mock, true}, {"W1", inv, true}}},
		{360, []string{"3h0m0s", "30m0s"}, []runOf{{"W2", work, false}, {"W1", inv, false}, {"W4", mock, true}, {"W1", inv, true}}},
		{1440, []string{"20h0m0s", "12h0m0s", "3h0m0s", "30m0s"},
			[]runOf{{"W2", work, false}, {"W1", inv, false}, {"W4", mock, true}, {"W1", inv, true}}},
	} {
		res, err := client.History(t.Context(), connect.NewRequest(&machinev1.GateServiceHistoryRequest{Minutes: tc.minutes}))
		if err != nil {
			t.Fatalf("%d minutes: %v", tc.minutes, err)
		}
		want := tc.minutes
		if want == 0 {
			want = HistoryMinutes
		}
		if span := res.Msg.GetReadTime().AsTime().Sub(res.Msg.GetStartTime().AsTime()); span != time.Duration(want)*time.Minute {
			t.Errorf("%d minutes: a window of %v", tc.minutes, span)
		}
		var commands []string
		for _, iv := range res.Msg.GetIntervals() {
			commands = append(commands, iv.GetCommand())
			code, kind := "", planv1.TaskKind_TASK_KIND_UNSPECIFIED
			switch iv.GetTaskId() {
			case "w1":
				code, kind = "W1", inv
			case "w2":
				code, kind = "W2", work
			}
			if iv.GetTaskCode() != code || iv.GetTaskKind() != kind {
				t.Errorf("%d minutes: interval of %q joined %s %v, want %s %v", tc.minutes, iv.GetTaskId(),
					iv.GetTaskCode(), iv.GetTaskKind(), code, kind)
			}
		}
		if !slices.Equal(commands, tc.commands) {
			t.Errorf("%d minutes: intervals %v, want %v", tc.minutes, commands, tc.commands)
		}
		var runs []runOf
		for _, r := range res.Msg.GetRuns() {
			runs = append(runs, runOf{r.GetCode(), r.GetKind(), r.GetEndTime() == nil})
		}
		if !slices.Equal(runs, tc.runs) {
			t.Errorf("%d minutes: runs %v, want %v", tc.minutes, runs, tc.runs)
		}
	}

	// Asked after a time within the window, only what changed since: the intervals released after it, the runs from it
	// on; the window stays the one asked.
	after, err := client.History(t.Context(), connect.NewRequest(&machinev1.GateServiceHistoryRequest{
		Minutes: 1440, AfterTime: at(4 * time.Hour),
	}))
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	for _, iv := range after.Msg.GetIntervals() {
		commands = append(commands, iv.GetCommand())
	}
	var runs []string
	for _, r := range after.Msg.GetRuns() {
		runs = append(runs, r.GetCode())
	}
	if span := after.Msg.GetReadTime().AsTime().Sub(after.Msg.GetStartTime().AsTime()); span != 24*time.Hour ||
		!slices.Equal(commands, []string{"3h0m0s", "30m0s"}) || !slices.Equal(runs, []string{"W2", "W1", "W4", "W1"}) {
		t.Errorf("after 4 hours ago: a window of %v, intervals %v, runs %v", span, commands, runs)
	}
	// After a time before the window: the whole window.
	if before, err := client.History(t.Context(), connect.NewRequest(&machinev1.GateServiceHistoryRequest{
		Minutes: 60, AfterTime: at(3 * time.Hour),
	})); err != nil || len(before.Msg.GetIntervals()) != 1 {
		t.Errorf("after a time before the hour: %v, %v", before.Msg.GetIntervals(), err)
	}

	// The joins are copies: the intervals the log in memory shares are left as they are.
	kept := []*machinev1.GateInterval{{TaskId: "w1"}}
	if joined := Joined(kept, past); joined[0].GetTaskCode() != "W1" || kept[0].GetTaskCode() != "" {
		t.Errorf("joined %v, kept %v", joined, kept)
	}

	// Past a week, nothing is kept: the window is refused.
	_, err = client.History(t.Context(), connect.NewRequest(&machinev1.GateServiceHistoryRequest{Minutes: 7*24*60 + 1}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a window past a week: %v, want invalid_argument", err)
	}
}

// TestStorePast: the tasks the history joins are read once, then again only after a task changed.
func TestStorePast(t *testing.T) {
	t.Parallel()
	db, err := store.Open(t.Context(), "", &planv1.Task{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	put := func(task *planv1.Task) {
		t.Helper()
		if err := db.Tx(t.Context(), func(tx *store.Tx) error {
			if err := tx.Journal("test", "put", task); err != nil {
				return err
			}
			return tx.Put(task)
		}); err != nil {
			t.Fatal(err)
		}
	}
	put(&planv1.Task{Id: store.NewID(), Code: "W1"})
	past := StorePast(db)
	first, err := past(t.Context())
	if err != nil || len(first) != 1 {
		t.Fatalf("first read: %v, %v", first, err)
	}
	if again, _ := past(t.Context()); &again[0] != &first[0] {
		t.Error("read again with no task changed")
	}
	put(&planv1.Task{Id: store.NewID(), Code: "W2"})
	if after, err := past(t.Context()); err != nil || len(after) != 2 {
		t.Errorf("after a task was added: %v, %v", after, err)
	}
}
