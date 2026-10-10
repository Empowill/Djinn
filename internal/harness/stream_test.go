package harness

import (
	"os"
	"strings"
	"testing"
	"time"
)

// unsaidQueue is claude's parser for a version whose result does not say how many messages are queued.
type unsaidQueue struct{ claudeParser }

func (unsaidQueue) stdout(raw string) ([]Event, *turnEnd) {
	events, end := parseClaude(raw)
	if end != nil {
		end.queued = nil
	}
	return events, end
}

// TestStreamQuietAfterResult: a message folded into the running turn gets no result of its own. When the result
// does not say so, the worker still ends once the agent has said nothing for a while, and that is no failure.
func TestStreamQuietAfterResult(t *testing.T) {
	t.Parallel()
	env, _, _ := fake{provider: "claude", fixture: "mid-turn-message"}.env(t)
	spec := Spec{TaskID: "t1", Dir: t.TempDir(), Prompt: "x", Env: env}
	a := streamAgent{name: "claude", parser: unsaidQueue{}, encode: claudeMessageLine, quiet: 200 * time.Millisecond}
	w, err := startStream(t.Context(), spec, os.Args[0], Claude{}.args(spec), Grace, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Send("Also check the docs."); err != nil {
		t.Fatal(err)
	}
	hung := time.AfterFunc(10*time.Second, func() {
		t.Errorf("the worker still runs after 10s")
		w.Stop()
	})
	events := collect(w)
	hung.Stop()
	if res := w.Wait(); res.Err != nil || res.ExitCode != 0 {
		t.Errorf("result = %+v", res)
	}
	statuses := texts(events, "STATUS")
	if len(statuses) != 2 || !strings.HasPrefix(statuses[1], "claude said nothing for 200ms after its result") {
		t.Errorf("statuses = %q", statuses)
	}
	if err := w.Send("late"); err == nil {
		t.Errorf("Send after the end took the message")
	}
}
