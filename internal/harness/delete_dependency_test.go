package harness

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// deleteEnv deliberately does not call Schedule: these tests take their own scheduler pass, so a background pass
// cannot add a wait event between an atomicity snapshot and Delete.
type deleteEnv struct {
	db *store.Store
	h  *Harness
}

func newDeleteEnv(t *testing.T, opts ...Option) *deleteEnv {
	t.Helper()
	db, err := store.Open(t.Context(), "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	h := New(db, t.TempDir(), testProviders(), opts...)
	e := &deleteEnv{db: db, h: h}
	t.Cleanup(func() {
		h.Close()
		db.Close()
	})
	return e
}

// TestDeleteRefusesReferencedTaskAtomically: deleting an imported task that still has incoming dependency edges is
// refused, naming every dependent in code order. The task, graph, event, retired codes and journal stay unchanged.
func TestDeleteRefusesReferencedTaskAtomically(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := newDeleteEnv(t, WithCapacity((&limit{slots: 0}).capacity))
	wishID := store.NewID()
	wish := &planv1.Wish{Id: wishID, Title: "Wish", State: planv1.WishState_WISH_STATE_ACTIVE, RetiredCodes: []string{"W0"}}
	dependency := &planv1.Task{
		Id: store.NewID(), WishId: wishID, Code: "W1", Title: "Imported dependency",
		Status: planv1.TaskStatus_TASK_STATUS_PENDING,
	}
	// Add W3 before W2 so the error must sort dependents by code rather than storage order.
	third := &planv1.Task{
		Id: store.NewID(), WishId: wishID, Code: "W3", Title: "Third dependent", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		Provider: planv1.Provider_PROVIDER_FAKE, Scheduled: true, DependsOn: []string{dependency.GetId()},
	}
	second := &planv1.Task{
		Id: store.NewID(), WishId: wishID, Code: "W2", Title: "Second dependent", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		Provider: planv1.Provider_PROVIDER_FAKE, Scheduled: true, DependsOn: []string{dependency.GetId()},
	}
	event := &planv1.TaskEvent{
		Id: store.NewID(), TaskId: dependency.GetId(), Seq: 1,
		Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "imported",
	}
	seedDeleteEntities(t, e, wish, dependency, third, second, event)

	beforeTasks := mustDeleteTasks(t, e, wishID)
	beforeEvents := mustDeleteEvents(t, e, dependency.GetId())
	beforeWish := mustDeleteWish(t, e, wishID)
	beforeCommands := mustDeleteCommands(t, e)
	_, err := e.h.Delete(t.Context(), "test", &planv1.TaskServiceDeleteRequest{TaskId: dependency.GetId()})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		if err == nil {
			// Keep the original failure diagnostic useful: before the guard, the next scheduler pass turns every
			// dependent into a terminal "dependency ... is gone" failure.
			e.h.schedule(t.Context())
			got := mustDeleteTask(t, e, second.GetId())
			t.Fatalf("delete of referenced task succeeded; scheduler marked %s %s: %q", got.GetCode(), got.GetStatus(), got.GetError())
		}
		t.Fatalf("delete referenced task: %v", err)
	}
	if !strings.Contains(err.Error(), "W2, W3") || !strings.Contains(err.Error(), "djinn task depend") || !strings.Contains(err.Error(), "then delete") {
		t.Errorf("referenced-task error = %v", err)
	}

	if got := mustDeleteTasks(t, e, wishID); !slices.EqualFunc(got, beforeTasks, func(a, b *planv1.Task) bool { return proto.Equal(a, b) }) {
		t.Errorf("tasks changed after refused delete: got %v, want %v", got, beforeTasks)
	}
	if got := mustDeleteEvents(t, e, dependency.GetId()); !slices.EqualFunc(got, beforeEvents, func(a, b *planv1.TaskEvent) bool { return proto.Equal(a, b) }) {
		t.Errorf("events changed after refused delete: got %v, want %v", got, beforeEvents)
	}
	if got := mustDeleteWish(t, e, wishID); !proto.Equal(got, beforeWish) {
		t.Errorf("wish changed after refused delete: got %v, want %v", got, beforeWish)
	}
	if got := mustDeleteCommands(t, e); !slices.EqualFunc(got, beforeCommands, sameDeleteCommand) {
		t.Errorf("journal changed after refused delete: got %v, want %v", got, beforeCommands)
	}
	e.h.schedule(t.Context())
	for _, task := range []*planv1.Task{second, third} {
		got := mustDeleteTask(t, e, task.GetId())
		if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || got.GetError() != "" ||
			!slices.Equal(got.GetDependsOn(), []string{dependency.GetId()}) {
			t.Errorf("dependent after refused delete and scheduler pass = %v", got)
		}
	}
}

// TestDeleteRefusesImportedCrossWishReference: imported data may contain an invalid edge across wishes; deletion
// still scans the complete task graph so it cannot erase the edge's target and leave a dangling dependency.
func TestDeleteRefusesImportedCrossWishReference(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := newDeleteEnv(t)
	targetWishID, foreignWishID := store.NewID(), store.NewID()
	targetWish := &planv1.Wish{Id: targetWishID, Title: "Target", State: planv1.WishState_WISH_STATE_ACTIVE}
	foreignWish := &planv1.Wish{Id: foreignWishID, Title: "Foreign", State: planv1.WishState_WISH_STATE_ACTIVE}
	target := &planv1.Task{
		Id: store.NewID(), WishId: targetWishID, Code: "W1", Title: "Imported target",
		Status: planv1.TaskStatus_TASK_STATUS_DONE,
	}
	foreign := &planv1.Task{
		Id: store.NewID(), WishId: foreignWishID, Code: "W2", Title: "Malformed imported dependent",
		Status: planv1.TaskStatus_TASK_STATUS_DONE, DependsOn: []string{target.GetId()},
	}
	seedDeleteEntities(t, e, targetWish, foreignWish, target, foreign)

	beforeTarget, beforeForeign := mustDeleteTask(t, e, target.GetId()), mustDeleteTask(t, e, foreign.GetId())
	beforeTargetWish := mustDeleteWish(t, e, targetWishID)
	beforeCommands := mustDeleteCommands(t, e)
	_, err := e.h.Delete(t.Context(), "test", &planv1.TaskServiceDeleteRequest{TaskId: target.GetId()})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "W2") {
		t.Fatalf("delete target with imported cross-wish reference: %v", err)
	}
	if got := mustDeleteTask(t, e, target.GetId()); !proto.Equal(got, beforeTarget) {
		t.Errorf("target changed after refused delete: got %v, want %v", got, beforeTarget)
	}
	if got := mustDeleteTask(t, e, foreign.GetId()); !proto.Equal(got, beforeForeign) {
		t.Errorf("foreign dependent changed after refused delete: got %v, want %v", got, beforeForeign)
	}
	if got := mustDeleteWish(t, e, targetWishID); !proto.Equal(got, beforeTargetWish) {
		t.Errorf("target wish changed after refused delete: got %v, want %v", got, beforeTargetWish)
	}
	if got := mustDeleteCommands(t, e); !slices.EqualFunc(got, beforeCommands, sameDeleteCommand) {
		t.Errorf("journal changed after refused delete: got %v, want %v", got, beforeCommands)
	}
}

// TestDeleteAfterAtomicDependRewire: Depend can replace the incoming edge on several dependents in one transaction,
// after which the referenced task can be deleted and a scheduler pass never reports a missing dependency.
func TestDeleteAfterAtomicDependRewire(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := newDeleteEnv(t, WithCapacity((&limit{slots: 0}).capacity))
	wishID := store.NewID()
	wish := &planv1.Wish{Id: wishID, Title: "Wish", State: planv1.WishState_WISH_STATE_ACTIVE}
	dependency := &planv1.Task{
		Id: store.NewID(), WishId: wishID, Code: "W1", Title: "Dependency", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
	}
	replacement := &planv1.Task{
		Id: store.NewID(), WishId: wishID, Code: "W4", Title: "Replacement", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
	}
	second := &planv1.Task{
		Id: store.NewID(), WishId: wishID, Code: "W2", Title: "Second", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		Provider: planv1.Provider_PROVIDER_FAKE, Scheduled: true, DependsOn: []string{dependency.GetId()},
	}
	third := &planv1.Task{
		Id: store.NewID(), WishId: wishID, Code: "W3", Title: "Third", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		Provider: planv1.Provider_PROVIDER_FAKE, Scheduled: true, DependsOn: []string{dependency.GetId()},
	}
	seedDeleteEntities(t, e, wish, dependency, replacement, second, third)

	set, err := e.h.Depend(t.Context(), "test", &planv1.TaskServiceDependRequest{
		TaskId: second.GetId(), DependsOn: []string{replacement.GetCode()},
		Also: []*planv1.TaskAfter{{Task: third.GetId(), After: []string{replacement.GetCode()}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(set) != 2 || !slices.Equal(set[0].GetDependsOn(), []string{replacement.GetId()}) ||
		!slices.Equal(set[1].GetDependsOn(), []string{replacement.GetId()}) {
		t.Fatalf("atomic rewire = %v", set)
	}
	if got := mustDeleteTask(t, e, second.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING ||
		!slices.Equal(got.GetDependsOn(), []string{replacement.GetId()}) {
		t.Fatalf("second after rewire = %v", got)
	}
	if got := mustDeleteTask(t, e, third.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING ||
		!slices.Equal(got.GetDependsOn(), []string{replacement.GetId()}) {
		t.Fatalf("third after rewire = %v", got)
	}
	if _, err := e.h.Delete(t.Context(), "test", &planv1.TaskServiceDeleteRequest{TaskId: dependency.GetId()}); err != nil {
		t.Fatalf("delete after rewire: %v", err)
	}
	e.h.schedule(t.Context())
	for _, id := range []string{second.GetId(), third.GetId()} {
		got := mustDeleteTask(t, e, id)
		if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || got.GetError() != "" ||
			!slices.Equal(got.GetDependsOn(), []string{replacement.GetId()}) || !strings.Contains(got.GetWaitReason(), replacement.GetCode()) {
			t.Errorf("%s after rewire/delete: %v", got.GetCode(), got)
		}
	}
}

// TestDeleteUnreferencedImportedTask: an imported task with events is still removable, and its code is retired.
func TestDeleteUnreferencedImportedTask(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := newDeleteEnv(t)
	wishID := store.NewID()
	wish := &planv1.Wish{Id: wishID, Title: "Wish", State: planv1.WishState_WISH_STATE_ACTIVE}
	task := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W1", Title: "Imported", Status: planv1.TaskStatus_TASK_STATUS_DONE}
	event := &planv1.TaskEvent{Id: store.NewID(), TaskId: task.GetId(), Seq: 1, Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "imported"}
	seedDeleteEntities(t, e, wish, task, event)
	if _, err := e.h.Delete(t.Context(), "test", &planv1.TaskServiceDeleteRequest{TaskId: task.GetId()}); err != nil {
		t.Fatalf("delete unreferenced imported task: %v", err)
	}
	if _, err := store.Get[*planv1.Task](t.Context(), e.db, task.GetId()); err == nil {
		t.Error("unreferenced imported task remains")
	}
	if got := mustDeleteEvents(t, e, task.GetId()); len(got) != 0 {
		t.Errorf("events remain after delete: %v", got)
	}
	if got := mustDeleteWish(t, e, wishID); !slices.Equal(got.GetRetiredCodes(), []string{"W1"}) {
		t.Errorf("retired codes = %v", got.GetRetiredCodes())
	}
}

// TestDeleteUsesCanonicalRunningTaskID: SQLite task identifiers compare case-insensitively, so the running check
// must use the freshly loaded task's canonical ID rather than the request spelling.
func TestDeleteUsesCanonicalRunningTaskID(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := newDeleteEnv(t)
	wishID := store.NewID()
	wish := &planv1.Wish{Id: wishID, Title: "Wish", State: planv1.WishState_WISH_STATE_ACTIVE}
	task := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W1", Title: "Imported", Status: planv1.TaskStatus_TASK_STATUS_DONE}
	seedDeleteEntities(t, e, wish, task)
	e.h.mu.Lock()
	e.h.runs[task.GetId()] = &run{}
	e.h.mu.Unlock()
	_, err := e.h.Delete(t.Context(), "test", &planv1.TaskServiceDeleteRequest{TaskId: strings.ToUpper(task.GetId())})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("delete a task with a running canonical ID: %v", err)
	}
	if _, err := store.Get[*planv1.Task](t.Context(), e.db, task.GetId()); err != nil {
		t.Fatalf("running task was deleted: %v", err)
	}
}

// TestDeleteSerializesWithSchedulerPass: a scheduler pass is pinned in its machine snapshot while a concurrent
// atomic rewire and delete queue behind the same lock. The pass then sees either the old graph or the rewired one,
// never a half-rewired graph.
func TestDeleteSerializesWithSchedulerPass(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce sync.Once
	capacity := func() (int, string, string) {
		enteredOnce.Do(func() { close(entered) })
		<-release
		return 0, "set by the test", ""
	}
	e := newDeleteEnv(t, WithCapacity(capacity))
	wishID := store.NewID()
	wish := &planv1.Wish{Id: wishID, Title: "Wish", State: planv1.WishState_WISH_STATE_ACTIVE}
	dependency := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W1", Title: "Dependency", Status: planv1.TaskStatus_TASK_STATUS_PENDING}
	replacement := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W4", Title: "Replacement", Status: planv1.TaskStatus_TASK_STATUS_PENDING}
	dependent := &planv1.Task{
		Id: store.NewID(), WishId: wishID, Code: "W2", Title: "Dependent", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		Provider: planv1.Provider_PROVIDER_FAKE, Scheduled: true, DependsOn: []string{dependency.GetId()},
	}
	seedDeleteEntities(t, e, wish, dependency, replacement, dependent)
	scheduleDone := make(chan struct{})
	go func() {
		e.h.schedule(t.Context())
		close(scheduleDone)
	}()
	<-entered // situation has read the graph and is inside the capacity callback while h.sched is held.
	rewireStarted, operationDone := make(chan struct{}), make(chan error, 1)
	go func() {
		close(rewireStarted)
		if _, err := e.h.Depend(t.Context(), "test", &planv1.TaskServiceDependRequest{
			TaskId: dependent.GetId(), DependsOn: []string{replacement.GetCode()},
		}); err != nil {
			operationDone <- err
			return
		}
		_, err := e.h.Delete(t.Context(), "test", &planv1.TaskServiceDeleteRequest{TaskId: dependency.GetId()})
		operationDone <- err
	}()
	<-rewireStarted
	close(release)
	if err := <-operationDone; err != nil {
		t.Fatalf("serialized rewire/delete: %v", err)
	}
	<-scheduleDone
	if _, err := store.Get[*planv1.Task](t.Context(), e.db, dependency.GetId()); err == nil {
		t.Error("serialized delete left the dependency")
	}
	got := mustDeleteTask(t, e, dependent.GetId())
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || got.GetError() != "" ||
		!slices.Equal(got.GetDependsOn(), []string{replacement.GetId()}) {
		t.Errorf("dependent after serialized scheduler/delete: %v", got)
	}
}

// TestDependRejectsCrossWishAtomically: a dependency must belong to the task's wish, and a refused cross-wish edge
// does not journal or mutate the task.
func TestDependRejectsCrossWishAtomically(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := newDeleteEnv(t, WithCapacity((&limit{slots: 0}).capacity))
	firstWishID, secondWishID := store.NewID(), store.NewID()
	firstWish := &planv1.Wish{Id: firstWishID, Title: "First", State: planv1.WishState_WISH_STATE_ACTIVE}
	secondWish := &planv1.Wish{Id: secondWishID, Title: "Second", State: planv1.WishState_WISH_STATE_ACTIVE}
	first := &planv1.Task{Id: store.NewID(), WishId: firstWishID, Code: "W1", Title: "First", Status: planv1.TaskStatus_TASK_STATUS_PENDING}
	other := &planv1.Task{Id: store.NewID(), WishId: secondWishID, Code: "W1", Title: "Other", Status: planv1.TaskStatus_TASK_STATUS_PENDING}
	seedDeleteEntities(t, e, firstWish, secondWish, first, other)
	before := mustDeleteTask(t, e, first.GetId())
	commands := mustDeleteCommands(t, e)
	_, err := e.h.Depend(t.Context(), "test", &planv1.TaskServiceDependRequest{
		TaskId: first.GetId(), DependsOn: []string{other.GetId()},
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "not a task of the wish") {
		t.Fatalf("cross-wish dependency: %v", err)
	}
	if got := mustDeleteTask(t, e, first.GetId()); !proto.Equal(got, before) {
		t.Errorf("task changed after refused cross-wish dependency: got %v, want %v", got, before)
	}
	if got := mustDeleteCommands(t, e); !slices.EqualFunc(got, commands, sameDeleteCommand) {
		t.Errorf("journal changed after refused cross-wish dependency: got %v, want %v", got, commands)
	}
}

func seedDeleteEntities(t *testing.T, e *deleteEnv, entities ...proto.Message) {
	t.Helper()
	if err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "seed", entities[0]); err != nil {
			return err
		}
		for _, entity := range entities {
			if err := tx.Put(entity); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func mustDeleteWish(t *testing.T, e *deleteEnv, id string) *planv1.Wish {
	t.Helper()
	wish, err := store.Get[*planv1.Wish](t.Context(), e.db, id)
	if err != nil {
		t.Fatal(err)
	}
	return wish
}

func mustDeleteTask(t *testing.T, e *deleteEnv, id string) *planv1.Task {
	t.Helper()
	task, err := store.Get[*planv1.Task](t.Context(), e.db, id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func mustDeleteTasks(t *testing.T, e *deleteEnv, wishID string) []*planv1.Task {
	t.Helper()
	tasks, err := store.List[*planv1.Task](t.Context(), e.db, store.Where{"wish_id": wishID})
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

func mustDeleteEvents(t *testing.T, e *deleteEnv, taskID string) []*planv1.TaskEvent {
	t.Helper()
	events, err := store.List[*planv1.TaskEvent](t.Context(), e.db, store.Where{"task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func mustDeleteCommands(t *testing.T, e *deleteEnv) []store.Command {
	t.Helper()
	commands, err := store.Commands(t.Context(), e.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	return commands
}

func sameDeleteCommand(a, b store.Command) bool {
	return a.Seq == b.Seq && a.ID == b.ID && a.Actor == b.Actor && a.At.Equal(b.At) && a.Method == b.Method && slices.Equal(a.Request, b.Request)
}
