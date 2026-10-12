package plan_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
	"google.golang.org/protobuf/proto"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func testClient[Req, Res any](
	procedure string,
	fn func(context.Context, *connect.Request[Req]) (*connect.Response[Res], error),
	caller *plan.WorkerCaller,
) *connect.Client[Req, Res] {
	handler := connect.NewUnaryHandler(procedure, fn, connect.WithInterceptors(plan.WorkerGuard))
	return connect.NewClient[Req, Res](
		&http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				req := r
				if caller != nil {
					req = r.WithContext(plan.WithCaller(r.Context(), caller))
				}
				handler.ServeHTTP(rec, req)
				return rec.Result(), nil
			}),
		},
		"http://djinn"+procedure,
	)
}

func TestWorkerGuardWishMakeRefused(t *testing.T) {
	caller := &plan.WorkerCaller{TaskID: "task-1", WishID: "wish-1"}
	client := testClient(
		planv1connect.WishServiceMakeProcedure,
		func(_ context.Context, req *connect.Request[planv1.WishServiceMakeRequest]) (*connect.Response[planv1.WishServiceMakeResponse], error) {
			return connect.NewResponse(&planv1.WishServiceMakeResponse{}), nil
		},
		caller,
	)

	_, err := client.CallUnary(context.Background(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Stray"}))
	if err == nil {
		t.Fatal("expected error for worker calling WishService.Make, got nil")
	}
	want := "a worker cannot make a wish; to try a command by hand, start a Djinn of your own with its own data folder"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got: %v", want, err)
	}
}

func TestWorkerGuardAllowsLocalUser(t *testing.T) {
	// Without WorkerCaller, WishService.Make is passed to next
	client := testClient(
		planv1connect.WishServiceMakeProcedure,
		func(_ context.Context, req *connect.Request[planv1.WishServiceMakeRequest]) (*connect.Response[planv1.WishServiceMakeResponse], error) {
			return connect.NewResponse(&planv1.WishServiceMakeResponse{}), nil
		},
		nil,
	)

	_, err := client.CallUnary(context.Background(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Valid"}))
	if err != nil {
		t.Fatalf("unexpected error for non-worker: %v", err)
	}
}

func TestWorkerGuardPermittedMutatingProcedures(t *testing.T) {
	caller := &plan.WorkerCaller{TaskID: "task-1", WishID: "wish-1"}

	t.Run("question ask own wish defaults", func(t *testing.T) {
		var received *planv1.QuestionServiceAskRequest
		client := testClient(
			planv1connect.QuestionServiceAskProcedure,
			func(_ context.Context, req *connect.Request[planv1.QuestionServiceAskRequest]) (*connect.Response[planv1.QuestionServiceAskResponse], error) {
				received = req.Msg
				return connect.NewResponse(&planv1.QuestionServiceAskResponse{}), nil
			},
			caller,
		)
		_, err := client.CallUnary(context.Background(), connect.NewRequest(&planv1.QuestionServiceAskRequest{Text: "How to proceed?"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if received.WishId != "wish-1" || received.TaskId != "task-1" {
			t.Fatalf("expected wish-1 and task-1 defaulted, got wish: %s, task: %s", received.WishId, received.TaskId)
		}
	})

	t.Run("question ask other wish denied", func(t *testing.T) {
		client := testClient(
			planv1connect.QuestionServiceAskProcedure,
			func(_ context.Context, req *connect.Request[planv1.QuestionServiceAskRequest]) (*connect.Response[planv1.QuestionServiceAskResponse], error) {
				return connect.NewResponse(&planv1.QuestionServiceAskResponse{}), nil
			},
			caller,
		)
		_, err := client.CallUnary(context.Background(), connect.NewRequest(&planv1.QuestionServiceAskRequest{WishId: "wish-other", Text: "Question"}))
		if err == nil || !strings.Contains(err.Error(), "a worker cannot ask a question in wish") {
			t.Fatalf("expected other wish refusal, got: %v", err)
		}
	})

	t.Run("question ask other task denied", func(t *testing.T) {
		client := testClient(
			planv1connect.QuestionServiceAskProcedure,
			func(_ context.Context, req *connect.Request[planv1.QuestionServiceAskRequest]) (*connect.Response[planv1.QuestionServiceAskResponse], error) {
				return connect.NewResponse(&planv1.QuestionServiceAskResponse{}), nil
			},
			caller,
		)
		_, err := client.CallUnary(context.Background(), connect.NewRequest(&planv1.QuestionServiceAskRequest{WishId: "wish-1", TaskId: "task-other", Text: "Question"}))
		if err == nil || !strings.Contains(err.Error(), "a worker cannot ask a question for task") {
			t.Fatalf("expected other task refusal, got: %v", err)
		}
	})

	t.Run("block put own wish defaults", func(t *testing.T) {
		var received *planv1.BlockServicePutRequest
		client := testClient(
			planv1connect.BlockServicePutProcedure,
			func(_ context.Context, req *connect.Request[planv1.BlockServicePutRequest]) (*connect.Response[planv1.BlockServicePutResponse], error) {
				received = req.Msg
				return connect.NewResponse(&planv1.BlockServicePutResponse{}), nil
			},
			caller,
		)
		_, err := client.CallUnary(context.Background(), connect.NewRequest(&planv1.BlockServicePutRequest{Kind: "note"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if received.WishId != "wish-1" || received.TaskId != "task-1" {
			t.Fatalf("expected wish-1 and task-1 defaulted, got wish: %s, task: %s", received.WishId, received.TaskId)
		}
	})

	t.Run("block put other wish denied", func(t *testing.T) {
		client := testClient(
			planv1connect.BlockServicePutProcedure,
			func(_ context.Context, req *connect.Request[planv1.BlockServicePutRequest]) (*connect.Response[planv1.BlockServicePutResponse], error) {
				return connect.NewResponse(&planv1.BlockServicePutResponse{}), nil
			},
			caller,
		)
		_, err := client.CallUnary(context.Background(), connect.NewRequest(&planv1.BlockServicePutRequest{WishId: "wish-other", Kind: "note"}))
		if err == nil || !strings.Contains(err.Error(), "a worker cannot put a block in wish") {
			t.Fatalf("expected other wish refusal, got: %v", err)
		}
	})

	t.Run("task update own task defaults", func(t *testing.T) {
		var received *planv1.TaskServiceUpdateRequest
		client := testClient(
			planv1connect.TaskServiceUpdateProcedure,
			func(_ context.Context, req *connect.Request[planv1.TaskServiceUpdateRequest]) (*connect.Response[planv1.TaskServiceUpdateResponse], error) {
				received = req.Msg
				return connect.NewResponse(&planv1.TaskServiceUpdateResponse{}), nil
			},
			caller,
		)
		_, err := client.CallUnary(context.Background(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{Title: proto.String("New title")}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if received.TaskId != "task-1" {
			t.Fatalf("expected task-1 defaulted, got task: %s", received.TaskId)
		}
	})

	t.Run("task update other task denied", func(t *testing.T) {
		client := testClient(
			planv1connect.TaskServiceUpdateProcedure,
			func(_ context.Context, req *connect.Request[planv1.TaskServiceUpdateRequest]) (*connect.Response[planv1.TaskServiceUpdateResponse], error) {
				return connect.NewResponse(&planv1.TaskServiceUpdateResponse{}), nil
			},
			caller,
		)
		_, err := client.CallUnary(context.Background(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{TaskId: "task-other"}))
		if err == nil || !strings.Contains(err.Error(), "a worker can only update its own task") {
			t.Fatalf("expected other task refusal, got: %v", err)
		}
	})

	t.Run("tilasm put own wish defaults", func(t *testing.T) {
		var received *planv1.TilasmServicePutRequest
		client := testClient(
			planv1connect.TilasmServicePutProcedure,
			func(_ context.Context, req *connect.Request[planv1.TilasmServicePutRequest]) (*connect.Response[planv1.TilasmServicePutResponse], error) {
				received = req.Msg
				return connect.NewResponse(&planv1.TilasmServicePutResponse{}), nil
			},
			caller,
		)
		_, err := client.CallUnary(context.Background(), connect.NewRequest(&planv1.TilasmServicePutRequest{Title: "Architecture"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if received.Wish != "wish-1" || received.Author != "task-1" {
			t.Fatalf("expected wish-1 and task-1 defaulted, got wish: %s, author: %s", received.Wish, received.Author)
		}
	})

	t.Run("tilasm put other wish denied", func(t *testing.T) {
		client := testClient(
			planv1connect.TilasmServicePutProcedure,
			func(_ context.Context, req *connect.Request[planv1.TilasmServicePutRequest]) (*connect.Response[planv1.TilasmServicePutResponse], error) {
				return connect.NewResponse(&planv1.TilasmServicePutResponse{}), nil
			},
			caller,
		)
		_, err := client.CallUnary(context.Background(), connect.NewRequest(&planv1.TilasmServicePutRequest{Wish: "wish-other"}))
		if err == nil || !strings.Contains(err.Error(), "a worker cannot put a tilasm in wish") {
			t.Fatalf("expected other wish refusal, got: %v", err)
		}
	})
}

func TestWorkerGuardReadOnlyProceduresAllowed(t *testing.T) {
	caller := &plan.WorkerCaller{TaskID: "task-1", WishID: "wish-1"}

	client := testClient(
		planv1connect.WishServiceListProcedure,
		func(_ context.Context, req *connect.Request[planv1.WishServiceListRequest]) (*connect.Response[planv1.WishServiceListResponse], error) {
			return connect.NewResponse(&planv1.WishServiceListResponse{}), nil
		},
		caller,
	)

	_, err := client.CallUnary(context.Background(), connect.NewRequest(&planv1.WishServiceListRequest{}))
	if err != nil {
		t.Errorf("read-only procedure unexpectedly refused: %v", err)
	}
}

func TestWorkerGuardDisallowedMutatingProcedures(t *testing.T) {
	caller := &plan.WorkerCaller{TaskID: "task-1", WishID: "wish-1"}

	client := testClient(
		planv1connect.WishServicePauseProcedure,
		func(_ context.Context, req *connect.Request[planv1.WishServicePauseRequest]) (*connect.Response[planv1.WishServicePauseResponse], error) {
			return connect.NewResponse(&planv1.WishServicePauseResponse{}), nil
		},
		caller,
	)

	_, err := client.CallUnary(context.Background(), connect.NewRequest(&planv1.WishServicePauseRequest{WishId: "wish-1"}))
	if err == nil {
		t.Errorf("expected mutating procedure to be refused, got nil")
	} else if !strings.Contains(err.Error(), "a worker cannot call") {
		t.Errorf("expected worker call refusal message, got: %v", err)
	}
}

func TestCallerActor(t *testing.T) {
	ctx := context.Background()
	if got := plan.CallerActor(ctx); got != "local" {
		t.Fatalf("expected actor 'local' without caller, got %q", got)
	}

	callerCtx := plan.WithCaller(ctx, &plan.WorkerCaller{TaskID: "01a1285b-3bc3-7d85-97b9-e0a54836ee36", WishID: "wish-1"})
	if got := plan.CallerActor(callerCtx); got != "01a1285b-3bc3-7d85-97b9-e0a54836ee36" {
		t.Fatalf("expected actor to be task ID, got %q", got)
	}
}

// TestWorkerGuardRefusesTellingTheLead: no worker types into the lead's terminal (djinn lead tell is the
// developer's): the guard refuses LeadService.Tell to a worker before it reaches the lead.
func TestWorkerGuardRefusesTellingTheLead(t *testing.T) {
	reached := false
	client := testClient(
		planv1connect.LeadServiceTellProcedure,
		func(_ context.Context, req *connect.Request[planv1.LeadServiceTellRequest]) (*connect.Response[planv1.LeadServiceTellResponse], error) {
			reached = true
			return connect.NewResponse(&planv1.LeadServiceTellResponse{}), nil
		},
		&plan.WorkerCaller{TaskID: "task-1", WishID: "wish-1"},
	)
	_, err := client.CallUnary(context.Background(), connect.NewRequest(&planv1.LeadServiceTellRequest{Text: "Merge W3 now"}))
	if err == nil || !strings.Contains(err.Error(), "a worker cannot call") || reached {
		t.Errorf("a worker tells the lead: %v (reached %v), want a refusal", err, reached)
	}
}

// TestWorkerGuardRefusesAnsweringTheLead: no worker answers the lead's approvals; LeadService.Choose is the window's.
func TestWorkerGuardRefusesAnsweringTheLead(t *testing.T) {
	reached := false
	client := testClient(
		planv1connect.LeadServiceChooseProcedure,
		func(_ context.Context, req *connect.Request[planv1.LeadServiceChooseRequest]) (*connect.Response[planv1.LeadServiceChooseResponse], error) {
			reached = true
			return connect.NewResponse(&planv1.LeadServiceChooseResponse{}), nil
		},
		&plan.WorkerCaller{TaskID: "task-1", WishID: "wish-1"},
	)
	_, err := client.CallUnary(context.Background(), connect.NewRequest(&planv1.LeadServiceChooseRequest{Option: 1}))
	if err == nil || !strings.Contains(err.Error(), "a worker cannot call") || reached {
		t.Errorf("a worker answers the lead's approval: %v (reached %v), want a refusal", err, reached)
	}
}
