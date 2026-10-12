package harness

import (
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/testx"
)

// TestReviewMarkSurvivesRunEnd: the developer marks a task reviewed while its worker runs; the worker's end writes its
// copy of the task, older than the mark: the mark stays, with who put it.
func TestReviewMarkSurvivesRunEnd(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()

	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())
	task := e.spawn(t, wishID, "text working\nwait\ntext done")

	s, err := e.tasks.Watch(ctx, connect.NewRequest(&planv1.TaskServiceWatchRequest{TaskId: task.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for s.Receive() {
		if s.Msg().GetEvent().GetText() == "working" {
			break
		}
	}

	marks := planv1connect.NewMarkServiceClient(e.srv.Client(), e.srv.URL)
	if _, err := marks.Put(ctx, connect.NewRequest(&planv1.MarkServicePutRequest{
		Target: &planv1.MarkTarget{Ref: &planv1.MarkTarget_Id{Id: task.GetId()}},
		Kind:   planv1.MarkKind_MARK_KIND_REVIEWED,
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.Send(ctx, connect.NewRequest(&planv1.TaskServiceSendRequest{
		TaskId: task.GetId(), Text: "go on",
	})); err != nil {
		t.Fatal(err)
	}
	for s.Receive() {
	}

	got := e.ended(t, task.GetId()).GetMarks()
	if len(got) != 1 || got[0].GetKind() != planv1.MarkKind_MARK_KIND_REVIEWED || got[0].GetActor() != "local" {
		t.Errorf("marks after the worker ended = %v, want reviewed by local", got)
	}
}
