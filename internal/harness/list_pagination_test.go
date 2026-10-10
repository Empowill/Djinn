package harness

import (
	"fmt"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

func TestTaskListPagination(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 0}).capacity))
	wishID, _ := e.wish(t, fakeProject(t, ""))

	var taskIDs []string
	for i := range 55 {
		res, err := e.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId:   wishID,
			Title:    fmt.Sprintf("Task %02d", i),
			Prompt:   "do work",
			Provider: planv1.Provider_PROVIDER_FAKE,
			Later:    true,
		}))
		if err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
		taskIDs = append(taskIDs, res.Msg.GetTask().GetId())
	}

	// First page (default size 50)
	res1, err := e.tasks.List(ctx, connect.NewRequest(&planv1.TaskServiceListRequest{
		WishId: wishID,
	}))
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if len(res1.Msg.GetTasks()) != 50 {
		t.Fatalf("got %d tasks, want 50", len(res1.Msg.GetTasks()))
	}
	if res1.Msg.GetTotal() != 55 {
		t.Errorf("got total %d, want 55", res1.Msg.GetTotal())
	}
	if res1.Msg.GetNextPageToken() == "" {
		t.Fatal("expected non-empty next_page_token on page 1")
	}

	// Verify order of first page
	for i, task := range res1.Msg.GetTasks() {
		if task.GetId() != taskIDs[i] {
			t.Errorf("task %d: got id %s, want %s", i, task.GetId(), taskIDs[i])
		}
	}

	// Second page
	res2, err := e.tasks.List(ctx, connect.NewRequest(&planv1.TaskServiceListRequest{
		WishId:    wishID,
		PageToken: res1.Msg.GetNextPageToken(),
	}))
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if len(res2.Msg.GetTasks()) != 5 {
		t.Fatalf("got %d tasks, want 5", len(res2.Msg.GetTasks()))
	}
	if res2.Msg.GetTotal() != 55 {
		t.Errorf("got total %d, want 55", res2.Msg.GetTotal())
	}
	if res2.Msg.GetNextPageToken() != "" {
		t.Errorf("expected empty next_page_token on last page, got %q", res2.Msg.GetNextPageToken())
	}

	// Verify order of second page
	for i, task := range res2.Msg.GetTasks() {
		if task.GetId() != taskIDs[50+i] {
			t.Errorf("task %d: got id %s, want %s", 50+i, task.GetId(), taskIDs[50+i])
		}
	}

	// Invalid page token
	_, err = e.tasks.List(ctx, connect.NewRequest(&planv1.TaskServiceListRequest{
		WishId:    wishID,
		PageToken: "invalid-token-!@#$",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("invalid token: got %v, want CodeInvalidArgument", err)
	}
}
