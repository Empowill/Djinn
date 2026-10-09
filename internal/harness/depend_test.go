package harness

import (
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestDepend: a task's dependencies are set after it was made, by code; a cycle, a wait on itself or a task of
// another wish is refused; none clears them; a planned task waits for its new dependencies.
func TestDepend(t *testing.T) {
	ctx := t.Context()
	// No slot: the planned tasks wait, for their dependencies first.
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 0}).capacity))
	wishID, _ := e.wish(t, gitRepo(t))
	plan := func(title string) *planv1.Task {
		t.Helper()
		res, err := e.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: title, Prompt: "text " + title, Provider: planv1.Provider_PROVIDER_FAKE, Later: true,
		}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetTask()
	}
	depend := func(task *planv1.Task, on ...string) (*planv1.Task, error) {
		res, err := e.tasks.Depend(ctx, connect.NewRequest(&planv1.TaskServiceDependRequest{TaskId: task.GetId(), DependsOn: on}))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetTask(), nil
	}
	a, b, c := plan("A"), plan("B"), plan("C")

	// C waits for A and B; B for A: a graph without cycle.
	got, err := depend(c, a.GetCode(), b.GetCode())
	if err != nil || !slices.Equal(got.GetDependsOn(), []string{a.GetId(), b.GetId()}) {
		t.Fatalf("C on A and B: %v, %v", got, err)
	}
	if _, err := depend(b, a.GetCode()); err != nil {
		t.Fatal(err)
	}
	// A waiting for C would close A → C → A, and for B, A → B → A.
	_, err = depend(a, c.GetCode())
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "no cycle") ||
		!strings.Contains(err.Error(), a.GetCode()+" → "+c.GetCode()+" → "+a.GetCode()) {
		t.Errorf("A on C: %v", err)
	}
	if _, err := depend(a, a.GetCode()); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("A on itself: %v", err)
	}
	if _, err := depend(a, "W99"); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("A on a task of no wish: %v", err)
	}
	// The scheduler says what C waits for.
	waited := e.until(t, c.GetId(), func(task *planv1.Task) bool { return strings.Contains(task.GetWaitReason(), "waits for") })
	if !strings.Contains(waited.GetWaitReason(), a.GetCode()) && !strings.Contains(waited.GetWaitReason(), b.GetCode()) {
		t.Errorf("C waits: %q", waited.GetWaitReason())
	}
	// None clears them.
	if got, err := depend(c); err != nil || len(got.GetDependsOn()) != 0 {
		t.Errorf("C on nothing: %v, %v", got, err)
	}
}
