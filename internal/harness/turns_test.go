package harness

import (
	"fmt"
	"math"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestClaudeTurns: claude's parser counts one model turn per assistant message of the worker's own, on the first
// event of the message, as the lines come: lines of one message share its id, a subagent's line is no turn of the
// worker, and a message that opens on withheld thinking says its turn on its next event. The count is the result's
// num_turns.
func TestClaudeTurns(t *testing.T) {
	t.Parallel()
	for fixture, want := range map[string][]int{
		"turns-mid-run": {1, 2, 3}, // tool-call.jsonl's three, its subagent line aside
		"success-haiku": {1, 2},    // num_turns 2, both messages open on withheld thinking (a real run)
	} {
		p := &claudeParser{}
		var got []int
		for _, l := range fixtureLines(t, "testdata/claude/"+fixture+".jsonl") {
			evs, _ := p.stdout(l)
			for _, ev := range evs {
				if ev.Turns > 0 {
					got = append(got, ev.Turns)
				}
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: turns %v, want %v", fixture, got, want)
		}
	}
}

// TestTurnsAcrossRuns: an ended task keeps the turns of its run, the limit, and the total; a continue counts its
// run again from 1, as the seal's limit applies to a run, and adds it to the total.
func TestTurnsAcrossRuns(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir(), WithTaskMaxTurns(40))
	wishID, _ := e.wish(t, nativeFolder(t))
	first := e.ended(t, e.mustSpawn(t, wishID, "Count turns", "turns 7\ntext first", nil).GetId())
	if got := first.GetTurns(); got.GetRun() != 7 || got.GetLimit() != 40 || got.GetTotal() != 7 {
		t.Fatalf("first run: turns %v, want 7 of 40, 7 in all", got)
	}
	if _, err := e.continueTask(t, first.GetId(), "turns 2\ntext again"); err != nil {
		t.Fatal(err)
	}
	done := e.ended(t, first.GetId())
	if got := done.GetTurns(); got.GetRun() != 2 || got.GetLimit() != 40 || got.GetTotal() != 9 {
		t.Errorf("continued: turns %v, want 2 of 40, 9 in all", got)
	}
}

// TestTurnsWrittenWithResources: a running worker's turns reach the store as they grow, with the sampler's write,
// though no event of theirs changes the task. The sampler's write keeps a comment stored on the task while it runs.
func TestTurnsWrittenWithResources(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	env, _, _ := fake{provider: "claude", fixture: "turns-mid-run", end: "wait"}.env(t)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = envProvider{Claude{Command: os.Args[0]}, env}
	var readings atomic.Int32
	read := func(int, string) (machine.Group, error) {
		// A process more or less at each reading: each one is worth writing.
		return machine.Group{Processes: 1 + int(readings.Add(1)%2), Memory: 100 << 20}, nil
	}
	e := upWith(t, t.TempDir(), providers, WithMeasure(10*time.Millisecond, read), WithTaskMaxTurns(40))
	wishID, _ := e.wish(t, nativeFolder(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Count turns", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()
	stored := func() *planv1.Task {
		task, err := store.Get[*planv1.Task](t.Context(), e.db, id)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	waitFor(t, "three turns stored", func() bool { return stored().GetTurns().GetRun() == 3 })
	if got := stored(); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING ||
		got.GetTurns().GetLimit() != 40 || got.GetTurns().GetTotal() != 3 {
		t.Fatalf("task %s, turns %v: want running, 3 of 40", got.GetStatus(), got.GetTurns())
	}

	// A comment comes while it runs (TaskService.Comment), between two readings.
	if err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actorLocal, "test", &planv1.Task{}); err != nil {
			return err
		}
		task, err := store.Get[*planv1.Task](t.Context(), tx, id)
		if err != nil {
			return err
		}
		task.Comments = append(task.Comments, &planv1.TaskComment{
			Id: store.NewID(), File: "README.md", Line: 1, EndLine: 1, Text: "Say what it is",
		})
		return tx.Put(task)
	}); err != nil {
		t.Fatal(err)
	}
	after := readings.Load() + 3
	waitFor(t, "three readings more", func() bool { return readings.Load() >= after })
	if got := stored().GetComments(); len(got) != 1 {
		t.Errorf("comments after the sampler wrote: %v, want the one", got)
	}
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: id})); err != nil {
		t.Fatal(err)
	}
	if got := e.ended(t, id); len(got.GetComments()) != 1 || got.GetTurns().GetRun() != 3 {
		t.Errorf("stopped: comments %v, turns %v; want the comment, 3 turns", got.GetComments(), got.GetTurns())
	}
}

// TestClaudeSpent (W213): claude's parser estimates what the run spent as the assistant lines come, at the prices
// Djinn knows, each message once at its last line (its usage grows from line to line), on the first event of each
// line: what an investigation's budget is weighed against before claude says its cost at a result.
func TestClaudeSpent(t *testing.T) {
	t.Parallel()
	line := func(id string, out, cacheWrite int) string {
		return fmt.Sprintf(`{"type":"assistant","message":{"id":%q,"model":"claude-sonnet-4-5","content":[{"type":"text",`+
			`"text":"step"}],"usage":{"input_tokens":4,"cache_creation_input_tokens":%d,"cache_read_input_tokens":9000,`+
			`"output_tokens":%d}},"parent_tool_use_id":null}`, id, cacheWrite, out)
	}
	p := &claudeParser{}
	var got []float64
	for _, l := range []string{line("msg_01", 20, 0), line("msg_01", 50, 0), line("msg_02", 100, 1000)} {
		evs, _ := p.stdout(l)
		got = append(got, evs[0].Spent)
	}
	// $3 in, $15 out, $0.30 cache read, $3.75 cache write per million tokens.
	want := []float64{0.003012, 0.003462, 0.003462 + 0.007962}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Errorf("spent %v, want %v", got, want)
			break
		}
	}
}
