package harness

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/testx"
)

func watchStream(t *testing.T, c planv1connect.WishServiceClient, wishID string) *connect.ServerStreamForClient[planv1.WishServiceWatchResponse] {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	s, err := c.Watch(ctx, connect.NewRequest(&planv1.WishServiceWatchRequest{WishId: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	if first := nextWatchMsg(t, s); first.GetWishId() != "" {
		t.Fatalf("first message %v, want everything", first)
	}
	return s
}

func nextWatchMsg(t *testing.T, s *connect.ServerStreamForClient[planv1.WishServiceWatchResponse]) *planv1.WishServiceWatchResponse {
	t.Helper()
	got := make(chan *planv1.WishServiceWatchResponse, 1)
	go func() {
		if s.Receive() {
			got <- s.Msg()
		}
		close(got)
	}()
	select {
	case msg, ok := <-got:
		if !ok {
			t.Fatalf("the stream ended: %v", s.Err())
		}
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("no message received on watch stream")
		return nil
	}
}

func nextMatching(t *testing.T, s *connect.ServerStreamForClient[planv1.WishServiceWatchResponse], match func(msg *planv1.WishServiceWatchResponse) bool) *planv1.WishServiceWatchResponse {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for matching watch message")
			return nil
		}
		msg := nextWatchMsg(t, s)
		if match(msg) {
			return msg
		}
	}
}

// TestWatchTaskMethods verifies that every TaskService method publishes Change_CHANGE_TASK to the watch stream.
func TestWatchTaskMethods(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	restore := plan.SetWatchInterval(time.Millisecond)
	t.Cleanup(restore)

	repo := gitRepo(t)
	lim := &limit{slots: 0}
	e := up(t, t.TempDir(), WithCapacity(lim.capacity), WithTick(20*time.Millisecond))
	wish1, _ := e.wish(t, repo)
	s1 := watchStream(t, e.wishes, wish1)

	ctx := t.Context()

	// 1. Spawn (work task, planned)
	spawnRes, err := e.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wish1, Title: "Work Task 1", Later: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	t1 := spawnRes.Msg.GetTask()
	msg := nextMatching(t, s1, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
				return task.GetId() == t1.GetId()
			})
	})
	if msg == nil {
		t.Fatal("Spawn work: no message")
	}

	// 2. Spawn (azima)
	azimaRes, err := e.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wish1, Title: "Phase Alpha", Kind: planv1.TaskKind_TASK_KIND_AZIMA,
	}))
	if err != nil {
		t.Fatal(err)
	}
	azima := azimaRes.Msg.GetTask()
	msg = nextMatching(t, s1, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
				return task.GetId() == azima.GetId()
			})
	})
	if msg == nil {
		t.Fatal("Spawn azima: no message")
	}

	// 3. Group
	if _, err := e.tasks.Group(ctx, connect.NewRequest(&planv1.TaskServiceGroupRequest{
		TaskId: t1.GetId(), PartOf: azima.GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	msg = nextMatching(t, s1, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
				return task.GetId() == t1.GetId() && task.GetPartOf() == azima.GetId()
			})
	})
	if msg == nil {
		t.Fatal("Group: no message")
	}

	// 4. Depend
	spawnRes2, err := e.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wish1, Title: "Work Task 2", Later: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	t2 := spawnRes2.Msg.GetTask()
	_ = nextMatching(t, s1, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
			return task.GetId() == t2.GetId()
		})
	})

	if _, err := e.tasks.Depend(ctx, connect.NewRequest(&planv1.TaskServiceDependRequest{
		TaskId: t2.GetId(), DependsOn: []string{t1.GetId()},
	})); err != nil {
		t.Fatal(err)
	}
	msg = nextMatching(t, s1, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
				return task.GetId() == t2.GetId() && slices.Contains(task.GetDependsOn(), t1.GetId())
			})
	})
	if msg == nil {
		t.Fatal("Depend: no message")
	}

	// 5. Update (title)
	newTitle := "Work Task 1 Renamed"
	if _, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: t1.GetId(), Title: &newTitle,
	})); err != nil {
		t.Fatal(err)
	}
	msg = nextMatching(t, s1, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
				return task.GetId() == t1.GetId() && task.GetTitle() == newTitle
			})
	})
	if msg == nil {
		t.Fatal("Update: no message")
	}

	// 6. SetAgent (via Update provider/model)
	agy := planv1.Provider_PROVIDER_ANTIGRAVITY
	model := "gemini-3.8-flash-high"
	if _, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: t1.GetId(), Provider: &agy, Model: &model,
	})); err != nil {
		t.Fatal(err)
	}
	msg = nextMatching(t, s1, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
				return task.GetId() == t1.GetId() && task.GetProvider() == agy && task.GetModel() == model
			})
	})
	if msg == nil {
		t.Fatal("SetAgent: no message")
	}

	// 7. Describe (azima description)
	azimaDesc := "A clear description of Phase Alpha"
	if _, err := e.tasks.Describe(ctx, connect.NewRequest(&planv1.TaskServiceDescribeRequest{
		Azima: azima.GetId(), Text: azimaDesc,
	})); err != nil {
		t.Fatal(err)
	}
	msg = nextMatching(t, s1, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
				return task.GetId() == azima.GetId() && task.GetDescription() == azimaDesc
			})
	})
	if msg == nil {
		t.Fatal("Describe: no message")
	}

	// 8. Done
	if _, err := e.tasks.Done(ctx, connect.NewRequest(&planv1.TaskServiceDoneRequest{
		TaskId: t2.GetId(), By: planv1.Closer_CLOSER_LEAD,
	})); err != nil {
		t.Fatal(err)
	}
	msg = nextMatching(t, s1, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
				return task.GetId() == t2.GetId() && task.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE
			})
	})
	if msg == nil {
		t.Fatal("Done: no message")
	}

	// 9. Delete
	if _, err := e.tasks.Delete(ctx, connect.NewRequest(&planv1.TaskServiceDeleteRequest{
		TaskId: t2.GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	msg = nextMatching(t, s1, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.Contains(m.GetChanged().GetDeleted(), t2.GetId())
	})
	if msg == nil {
		t.Fatal("Delete: no message")
	}

	// 10. Open (draft azima)
	draftRes, err := e.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wish1, Title: "Draft Azima", Kind: planv1.TaskKind_TASK_KIND_AZIMA, Draft: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	draftAzima := draftRes.Msg.GetTask()
	_ = nextMatching(t, s1, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
			return task.GetId() == draftAzima.GetId()
		})
	})

	if _, err := e.tasks.Open(ctx, connect.NewRequest(&planv1.TaskServiceOpenRequest{
		Azima: draftAzima.GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	msg = nextMatching(t, s1, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
				return task.GetId() == draftAzima.GetId() && !task.GetDraft()
			})
	})
	if msg == nil {
		t.Fatal("Open: no message")
	}

	// 11. Move (azima move between wishes)
	repo2 := gitRepo(t)
	wish2, p2 := e.wish(t, repo2)
	s2 := watchStream(t, e.wishes, wish2)

	if _, err := e.tasks.Move(ctx, connect.NewRequest(&planv1.TaskServiceMoveRequest{
		Azima: azima.GetId(), Wish: wish2,
	})); err != nil {
		t.Fatal(err)
	}
	// wish1 receives deletion
	msg1 := nextMatching(t, s1, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.Contains(m.GetChanged().GetDeleted(), azima.GetId())
	})
	if msg1 == nil {
		t.Fatal("Move: wish1 did not receive deletion")
	}
	// wish2 receives addition
	msg2 := nextMatching(t, s2, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
				return task.GetId() == azima.GetId()
			})
	})
	if msg2 == nil {
		t.Fatal("Move: wish2 did not receive addition")
	}

	// 12. Stop, Pause, Resume, Continue, Clean
	// Spawn a worker task that runs a fake script with "wait"
	lim.set(5, "")
	runRes, err := e.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wish2, ProjectId: p2, Title: "Running Worker", Provider: planv1.Provider_PROVIDER_FAKE, Prompt: "wait",
	}))
	if err != nil {
		t.Fatal(err)
	}
	runTask := runRes.Msg.GetTask()

	// Wait for task to transition to RUNNING
	var runningTask *planv1.Task
	for range 50 {
		time.Sleep(10 * time.Millisecond)
		got := e.get(t, runTask.GetId())
		if got.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING {
			runningTask = got
			break
		}
	}
	if runningTask == nil {
		got := e.get(t, runTask.GetId())
		t.Fatalf("task did not start running: %v", got)
	}

	// Pause
	if _, err := e.tasks.Pause(ctx, connect.NewRequest(&planv1.TaskServicePauseRequest{
		TaskId: runTask.GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	msg = nextMatching(t, s2, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
				return task.GetId() == runTask.GetId() && task.GetStatus() == planv1.TaskStatus_TASK_STATUS_PAUSED
			})
	})
	if msg == nil {
		t.Fatal("Pause: no message")
	}

	// Resume
	if _, err := e.tasks.Resume(ctx, connect.NewRequest(&planv1.TaskServiceResumeRequest{
		TaskId: runTask.GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	msg = nextMatching(t, s2, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
				return task.GetId() == runTask.GetId() && task.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING
			})
	})
	if msg == nil {
		t.Fatal("Resume: no message")
	}

	// Stop
	if _, err := e.tasks.Stop(ctx, connect.NewRequest(&planv1.TaskServiceStopRequest{
		TaskId: runTask.GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	msg = nextMatching(t, s2, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
				return task.GetId() == runTask.GetId() && task.GetStatus() == planv1.TaskStatus_TASK_STATUS_STOPPED
			})
	})
	if msg == nil {
		t.Fatal("Stop: no message")
	}

	// Continue
	if _, err := e.tasks.Continue(ctx, connect.NewRequest(&planv1.TaskServiceContinueRequest{
		TaskId: runTask.GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	msg = nextMatching(t, s2, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
				return task.GetId() == runTask.GetId() &&
					(task.GetStatus() == planv1.TaskStatus_TASK_STATUS_PENDING || task.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING)
			})
	})
	if msg == nil {
		t.Fatal("Continue: no message")
	}

	// Clean (clean task worktree)
	for range 50 {
		time.Sleep(10 * time.Millisecond)
		got := e.get(t, runTask.GetId())
		if got.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING {
			break
		}
	}
	_, _ = e.tasks.Stop(ctx, connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: runTask.GetId()}))
	for range 50 {
		time.Sleep(10 * time.Millisecond)
		got := e.get(t, runTask.GetId())
		if got.GetStatus() == planv1.TaskStatus_TASK_STATUS_STOPPED {
			break
		}
	}

	cleanedRes, err := e.tasks.Clean(ctx, connect.NewRequest(&planv1.TaskServiceCleanRequest{
		TaskId: runTask.GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cleanedRes.Msg.GetTask().GetWorktree() != "" {
		t.Fatal("expected worktree to be cleaned")
	}
	msg = nextMatching(t, s2, func(m *planv1.WishServiceWatchResponse) bool {
		return slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) &&
			slices.ContainsFunc(m.GetChanged().GetTasks(), func(task *planv1.Task) bool {
				return task.GetId() == runTask.GetId() && task.GetWorktree() == ""
			})
	})
	if msg == nil {
		t.Fatal("Clean: no message")
	}
}
