package plan_test

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// TestPageReadsBounded pins how many statements the reads of a page of TaskService/List take, whatever its size: its
// tilasms, its times and its prompts, each in one. The prompts took one per task, over every event of each: a page
// of a wish of a thousand tasks waited past the window's 30 s for the database's one connection after a restart
// (L25, W123). Each task's last prompt stands, and a tool result whose text holds the bytes of a prompt's kind is none.
func TestPageReadsBounded(t *testing.T) {
	t.Parallel()
	s := open(t)
	t0 := time.Date(2026, 11, 18, 4, 0, 0, 0, time.UTC)
	prompt := planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT
	result := planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT
	var all []proto.Message
	for i := range 120 {
		id := store.NewID()
		all = append(all, &planv1.Task{
			Id: id, WishId: "wish", Code: fmt.Sprint("W", i+1), Status: planv1.TaskStatus_TASK_STATUS_DONE,
			StartTime: timestamppb.New(t0), EndTime: timestamppb.New(t0.Add(time.Hour)),
		}, &planv1.GateWait{Id: id},
			event(id, 1, prompt, fmt.Sprint("prompt ", i)), event(id, 2, result, "a \x20\x01 in a result"))
		if i%3 == 0 {
			all = append(all, event(id, 3, prompt, fmt.Sprint("continue ", i)), event(id, 4, result, "done"))
		}
	}
	put(t, s, true, all...)
	tasks, err := store.List[*planv1.Task](t.Context(), s, store.Where{"wish_id": "wish"})
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{10, 50, 120} {
		page := tasks[:size]
		for _, task := range page {
			task.Prompt = ""
		}
		var n atomic.Int64
		r := store.Counted(s, &n)
		if err := plan.FillTilasms(t.Context(), r, page); err != nil {
			t.Fatal(err)
		}
		if err := plan.ReadTimes(t.Context(), r, page, t0.Add(time.Hour), false); err != nil {
			t.Fatal(err)
		}
		if err := plan.FillPrompts(t.Context(), r, page); err != nil {
			t.Fatal(err)
		}
		if n.Load() != 3 {
			t.Errorf("a page of %d tasks read %d statements; want 3: its tilasms, times and prompts", size, n.Load())
		}
		for i, task := range page {
			want := fmt.Sprint("prompt ", i)
			if i%3 == 0 {
				want = fmt.Sprint("continue ", i)
			}
			if task.GetPrompt() != want {
				t.Errorf("page of %d: %s's prompt = %q; want %q", size, task.GetCode(), task.GetPrompt(), want)
			}
		}
	}
}
