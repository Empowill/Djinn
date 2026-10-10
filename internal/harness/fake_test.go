package harness

import (
	"slices"
	"testing"
	"time"
)

func TestFakeScript(t *testing.T) {
	t.Parallel()
	w, err := Fake{}.Start(t.Context(), Spec{TaskID: "t1", Prompt: "text hello\ntool Bash ls\nresult a b\nusage 100 20 0.5\nsleep 1ms\njust words\n"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Send("again"); err != nil {
		t.Fatal(err)
	}
	events := collect(w)
	want := []string{"STATUS", "TEXT", "TOOL_CALL", "TOOL_RESULT", "USAGE", "TEXT", "TEXT"}
	if got := kinds(events); !slices.Equal(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	if events[0].SessionID != "t1" || events[4].Usage.GetCostUsd() != 0.5 || events[5].Text != "just words" ||
		events[6].Text != "received: again" {
		t.Errorf("events = %+v", events)
	}
	if res := w.Wait(); res.ExitCode != 0 || res.Err != nil {
		t.Errorf("result = %+v", res)
	}
}

func TestFakeEnds(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		script string
		code   int
		failed bool
	}{
		{"fail no tests", 1, true},
		{"exit 3", 3, false},
	} {
		w, _ := Fake{}.Start(t.Context(), Spec{Prompt: tt.script})
		collect(w)
		if res := w.Wait(); res.ExitCode != tt.code || (res.Err != nil) != tt.failed {
			t.Errorf("%q: result = %+v", tt.script, res)
		}
	}
}

func TestFakeStops(t *testing.T) {
	t.Parallel()
	w, _ := Fake{}.Start(t.Context(), Spec{Prompt: "sleep 1h"})
	<-w.Events()
	start := time.Now()
	w.Stop()
	collect(w)
	if res := w.Wait(); res.ExitCode != -1 || time.Since(start) > time.Second {
		t.Errorf("result = %+v after %v", res, time.Since(start))
	}
}

// TestFakeWaits: wait holds the script until a message comes, then the script goes on and says the message back; a
// worker stopped while it waits ends stopped.
func TestFakeWaits(t *testing.T) {
	t.Parallel()
	w, _ := Fake{}.Start(t.Context(), Spec{Prompt: "text before\nwait\ntext after"})
	<-w.Events() // its session
	if ev := <-w.Events(); ev.Text != "before" {
		t.Fatalf("first words %+v", ev)
	}
	if err := w.Send("go on"); err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, ev := range collect(w) {
		texts = append(texts, ev.Text)
	}
	if want := []string{"after", "received: go on"}; !slices.Equal(texts, want) || w.Wait().ExitCode != 0 {
		t.Errorf("after the message: %q, %+v", texts, w.Wait())
	}

	w, _ = Fake{}.Start(t.Context(), Spec{Prompt: "wait"})
	<-w.Events()
	w.Stop()
	collect(w)
	if res := w.Wait(); res.ExitCode != -1 {
		t.Errorf("stopped while it waits: %+v", res)
	}
}
