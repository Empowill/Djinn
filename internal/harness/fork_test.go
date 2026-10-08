package harness

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

func connectCode(err error) connect.Code {
	var cerr *connect.Error
	if errors.As(err, &cerr) {
		return cerr.Code()
	}
	return 0
}

// TestForkArguments: each agent is asked for a fork its own way, or refuses it.
func TestForkArguments(t *testing.T) {
	spec := Spec{TaskID: "t2", Dir: "/w", Resume: "s1", Fork: true}
	args := strings.Join(Claude{}.args(spec), " ")
	if !strings.HasSuffix(args, "--resume s1 --fork-session --session-id t2") {
		t.Errorf("claude: %s", args)
	}
	if method, params := (Codex{}).threadRequest(spec); method != "thread/fork" || params["threadId"] != "s1" {
		t.Errorf("codex: %s %v", method, params)
	}
	if _, err := (Antigravity{Command: "djinn-no-such-agy"}).Start(t.Context(), spec); err == nil ||
		!strings.Contains(err.Error(), "cannot fork") {
		t.Errorf("agy: %v, want a refusal", err)
	}
}

// TestSpawnFork: a task starts from a copy of another task's session, or of the lead's, with a session of its own;
// the default stays a worker that starts from its prompt alone.
func TestSpawnFork(t *testing.T) {
	ctx := t.Context()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, nativeFolder(t))

	first := e.ended(t, e.mustSpawn(t, wishID, "Map the code", "text mapped", nil).GetId())
	if first.GetForkSession() != "" || first.GetSessionId() != first.GetId() {
		t.Fatalf("a plain task: %v", first)
	}
	forked := e.mustSpawn(t, wishID, "Go on from the map", "text on", &planv1.TaskServiceSpawnRequest{Fork: "w1"})
	if forked.GetForkSession() != first.GetSessionId() || forked.GetForkOf() != "W1" {
		t.Errorf("forked task = %v", forked)
	}
	events := e.watch(ctx, t, forked.GetId(), 0)
	var texts []string
	for _, ev := range events {
		texts = append(texts, ev.GetText())
	}
	all := strings.Join(texts, "\n")
	if !strings.Contains(all, "forked from W1's session") || !strings.Contains(all, "fake session "+forked.GetId()+", forked from "+first.GetId()) {
		t.Errorf("events of the fork:\n%s", all)
	}
	if done := e.get(t, forked.GetId()); done.GetSessionId() != forked.GetId() {
		t.Errorf("the fork's own session = %q, want %q", done.GetSessionId(), forked.GetId())
	}
	// A planned fork keeps its source until it starts.
	later := e.ended(t, e.mustSpawn(t, wishID, "Later", "text later", &planv1.TaskServiceSpawnRequest{Fork: "W1", Later: true}).GetId())
	if later.GetForkSession() != first.GetSessionId() || later.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("planned fork = %v", later)
	}

	// From the lead: its session and its agent.
	err := e.db.Tx(ctx, func(tx *store.Tx) error {
		wish, err := store.Get[*planv1.Wish](ctx, tx, wishID)
		if err != nil {
			return err
		}
		wish.Lead = &planv1.Lead{Provider: planv1.Provider_PROVIDER_FAKE, SessionId: "lead-session"}
		if err := tx.Journal("test", "test/lead", wish); err != nil {
			return err
		}
		return tx.Put(wish)
	})
	if err != nil {
		t.Fatal(err)
	}
	fromLead := e.mustSpawn(t, wishID, "From the lead", "text hi", &planv1.TaskServiceSpawnRequest{FromLead: true})
	if fromLead.GetForkSession() != "lead-session" || fromLead.GetForkOf() != "lead" {
		t.Errorf("from the lead = %v", fromLead)
	}
	e.ended(t, fromLead.GetId())

	// What cannot fork is refused before any task exists.
	agy := &planv1.Task{
		Id: store.NewID(), WishId: wishID, Code: "W9", Title: "Elsewhere", Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
		SessionId: "conv-1", Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: timestamppb.Now(),
	}
	pending := &planv1.Task{
		Id: store.NewID(), WishId: wishID, Code: "W10", Title: "Not started", Provider: planv1.Provider_PROVIDER_FAKE,
		Status: planv1.TaskStatus_TASK_STATUS_PENDING, CreateTime: timestamppb.Now(),
	}
	if err := e.db.Tx(ctx, func(tx *store.Tx) error {
		for _, task := range []*planv1.Task{agy, pending} {
			if err := tx.Journal("test", "test/task", task); err != nil {
				return err
			}
			if err := tx.Put(task); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		req  *planv1.TaskServiceSpawnRequest
		code connect.Code
		says string
	}{
		"both sources":            {&planv1.TaskServiceSpawnRequest{Fork: "W1", FromLead: true}, connect.CodeInvalidArgument, "exclude"},
		"an unknown task":         {&planv1.TaskServiceSpawnRequest{Fork: "W42"}, connect.CodeInvalidArgument, "not a task"},
		"antigravity":             {&planv1.TaskServiceSpawnRequest{Fork: "w9"}, connect.CodeFailedPrecondition, "cannot fork"},
		"a task without session":  {&planv1.TaskServiceSpawnRequest{Fork: "W10"}, connect.CodeFailedPrecondition, "no session"},
		"another agent than W1's": {&planv1.TaskServiceSpawnRequest{Fork: "W1", Provider: planv1.Provider_PROVIDER_CLAUDE}, connect.CodeInvalidArgument, "same agent"},
	} {
		req := tc.req
		req.WishId, req.Title, req.Prompt = wishID, name, "text x"
		_, err := e.tasks.Spawn(ctx, connect.NewRequest(req))
		if connectCode(err) != tc.code || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: %v, want %v saying %q", name, err, tc.code, tc.says)
		}
	}
	// A wish without a lead session cannot fork it.
	bare, _ := e.wish(t, nativeFolder(t))
	_, err = e.tasks.Spawn(context.WithoutCancel(ctx), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: bare, Title: "x", FromLead: true, Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if connectCode(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "set-lead") {
		t.Errorf("no lead: %v", err)
	}
}
