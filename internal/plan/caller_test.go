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

// guarded sends req to procedure through WorkerGuard, as caller (nil: the developer), and returns the request its
// handler received: nil when the guard refused it first.
func guarded[Res, Req any](procedure string, caller *plan.WorkerCaller, req *Req) (proto.Message, error) {
	var received proto.Message
	client := testClient(procedure, func(_ context.Context, r *connect.Request[Req]) (*connect.Response[Res], error) {
		received = any(r.Msg).(proto.Message)
		return connect.NewResponse(new(Res)), nil
	}, caller)
	_, err := client.CallUnary(context.Background(), connect.NewRequest(req))
	return received, err
}

// TestWorkerGuard: a worker makes no wish, never types into the lead's terminal nor answers its approvals, and calls
// only the procedures that read, or that write in its own wish and task, which it names by default; an ifrit reaches
// the serves of its own task alone (T51). The developer, with no caller, goes through.
func TestWorkerGuard(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		local   bool // the developer calls, not the worker of task-1 in wish-1
		call    func(caller *plan.WorkerCaller) (proto.Message, error)
		want    proto.Message // the request the handler receives, the guard's defaults filled in
		refused string        // in the guard's refusal, when the handler is never reached
	}{
		{
			name: "wish make refused",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.WishServiceMakeResponse](planv1connect.WishServiceMakeProcedure, c, &planv1.WishServiceMakeRequest{Title: "Stray"})
			},
			refused: "a worker cannot make a wish; to try a command by hand, start a Djinn of your own with its own data folder",
		},
		{
			name: "wish make by the developer", local: true,
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.WishServiceMakeResponse](planv1connect.WishServiceMakeProcedure, c, &planv1.WishServiceMakeRequest{Title: "Valid"})
			},
			want: &planv1.WishServiceMakeRequest{Title: "Valid"},
		},
		{
			name: "question ask own wish defaults",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.QuestionServiceAskResponse](planv1connect.QuestionServiceAskProcedure, c, &planv1.QuestionServiceAskRequest{Text: "How to proceed?"})
			},
			want: &planv1.QuestionServiceAskRequest{Text: "How to proceed?", WishId: "wish-1", TaskId: "task-1"},
		},
		{
			name: "question ask other wish denied",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.QuestionServiceAskResponse](planv1connect.QuestionServiceAskProcedure, c,
					&planv1.QuestionServiceAskRequest{WishId: "wish-other", Text: "Question"})
			},
			refused: "a worker cannot ask a question in wish",
		},
		{
			name: "question ask other task denied",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.QuestionServiceAskResponse](planv1connect.QuestionServiceAskProcedure, c,
					&planv1.QuestionServiceAskRequest{WishId: "wish-1", TaskId: "task-other", Text: "Question"})
			},
			refused: "a worker cannot ask a question for task",
		},
		{
			name: "block put own wish defaults",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.BlockServicePutResponse](planv1connect.BlockServicePutProcedure, c, &planv1.BlockServicePutRequest{Kind: "note"})
			},
			want: &planv1.BlockServicePutRequest{Kind: "note", WishId: "wish-1", TaskId: "task-1"},
		},
		{
			name: "block put other wish denied",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.BlockServicePutResponse](planv1connect.BlockServicePutProcedure, c,
					&planv1.BlockServicePutRequest{WishId: "wish-other", Kind: "note"})
			},
			refused: "a worker cannot put a block in wish",
		},
		{
			name: "task update own task defaults",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.TaskServiceUpdateResponse](planv1connect.TaskServiceUpdateProcedure, c,
					&planv1.TaskServiceUpdateRequest{Title: proto.String("New title")})
			},
			want: &planv1.TaskServiceUpdateRequest{Title: proto.String("New title"), TaskId: "task-1"},
		},
		{
			name: "task update other task denied",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.TaskServiceUpdateResponse](planv1connect.TaskServiceUpdateProcedure, c, &planv1.TaskServiceUpdateRequest{TaskId: "task-other"})
			},
			refused: "a worker can only update its own task",
		},
		{
			name: "tilasm put own wish defaults",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.TilasmServicePutResponse](planv1connect.TilasmServicePutProcedure, c, &planv1.TilasmServicePutRequest{Title: "Architecture"})
			},
			want: &planv1.TilasmServicePutRequest{Title: "Architecture", Wish: "wish-1", Author: "task-1"},
		},
		{
			name: "tilasm put other wish denied",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.TilasmServicePutResponse](planv1connect.TilasmServicePutProcedure, c, &planv1.TilasmServicePutRequest{Wish: "wish-other"})
			},
			refused: "a worker cannot put a tilasm in wish",
		},
		{
			name: "tilasm publish own wish defaults",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.TilasmServicePublishResponse](planv1connect.TilasmServicePublishProcedure, c,
					&planv1.TilasmServicePublishRequest{Tilasm: &planv1.TilasmRef{Ref: &planv1.TilasmRef_Code{Code: "L52"}}})
			},
			want: &planv1.TilasmServicePublishRequest{Tilasm: &planv1.TilasmRef{Ref: &planv1.TilasmRef_Code{Code: "L52"}}, Wish: "wish-1"},
		},
		{
			name: "tilasm publish other wish denied",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.TilasmServicePublishResponse](planv1connect.TilasmServicePublishProcedure, c,
					&planv1.TilasmServicePublishRequest{Wish: "wish-other"})
			},
			refused: "a worker cannot publish a tilasm of wish",
		},
		{
			// The go-ahead to publish is the developer's, from the window.
			name: "tilasm confirm publish refused",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.TilasmServiceConfirmPublishResponse](planv1connect.TilasmServiceConfirmPublishProcedure, c,
					&planv1.TilasmServiceConfirmPublishRequest{TilasmId: "01a15578-00ee-7526-b8f0-6983ee20a3a3"})
			},
			refused: "a worker cannot call",
		},
		{
			name: "read-only procedure allowed",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.WishServiceListResponse](planv1connect.WishServiceListProcedure, c, &planv1.WishServiceListRequest{})
			},
			want: &planv1.WishServiceListRequest{},
		},
		{
			name: "lead sessions allowed",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.LeadServiceSessionsResponse](planv1connect.LeadServiceSessionsProcedure, c, &planv1.LeadServiceSessionsRequest{})
			},
			want: &planv1.LeadServiceSessionsRequest{},
		},
		{
			name: "lead show allowed",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.LeadServiceShowResponse](planv1connect.LeadServiceShowProcedure, c, &planv1.LeadServiceShowRequest{Id: "01a153e0"})
			},
			want: &planv1.LeadServiceShowRequest{Id: "01a153e0"},
		},
		{
			name: "other mutating procedure refused",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.WishServicePauseResponse](planv1connect.WishServicePauseProcedure, c, &planv1.WishServicePauseRequest{WishId: "wish-1"})
			},
			refused: "a worker cannot call",
		},
		{
			// djinn lead tell is the developer's.
			name: "lead tell refused",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.LeadServiceTellResponse](planv1connect.LeadServiceTellProcedure, c, &planv1.LeadServiceTellRequest{Text: "Merge W3 now"})
			},
			refused: "a worker cannot call",
		},
		{
			// The lead's approvals are the window's.
			name: "lead choose refused",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.LeadServiceChooseResponse](planv1connect.LeadServiceChooseProcedure, c, &planv1.LeadServiceChooseRequest{Option: 1})
			},
			refused: "a worker cannot call",
		},
		{
			name: "serve run own task defaults",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.ServeServiceRunResponse](planv1connect.ServeServiceRunProcedure, c,
					&planv1.ServeServiceRunRequest{Name: "front", Command: "npm run dev"})
			},
			want: &planv1.ServeServiceRunRequest{Name: "front", Command: "npm run dev", TaskId: "task-1"},
		},
		{
			name: "serve run other task denied",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.ServeServiceRunResponse](planv1connect.ServeServiceRunProcedure, c,
					&planv1.ServeServiceRunRequest{Name: "front", Command: "npm run dev", TaskId: "task-other"})
			},
			refused: "its own serves",
		},
		{
			name: "serve logs own task defaults",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.ServeServiceLogsResponse](planv1connect.ServeServiceLogsProcedure, c, &planv1.ServeServiceLogsRequest{Name: "front"})
			},
			want: &planv1.ServeServiceLogsRequest{Name: "front", TaskId: "task-1"},
		},
		{
			name: "lead say refused",
			call: func(c *plan.WorkerCaller) (proto.Message, error) {
				return guarded[planv1.LeadServiceSayResponse](planv1connect.LeadServiceSayProcedure, c,
					&planv1.LeadServiceSayRequest{WishId: "wish-1", Text: "Hello"})
			},
			refused: "a worker cannot call " + planv1connect.LeadServiceSayProcedure,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			caller := &plan.WorkerCaller{TaskID: "task-1", WishID: "wish-1"}
			if tc.local {
				caller = nil
			}
			received, err := tc.call(caller)
			if tc.refused != "" {
				if connect.CodeOf(err) != connect.CodePermissionDenied || !strings.Contains(err.Error(), tc.refused) || received != nil {
					t.Fatalf("got %v (handler reached: %v), want permission_denied with %q", err, received != nil, tc.refused)
				}
				return
			}
			if err != nil || !proto.Equal(received, tc.want) {
				t.Fatalf("handler received %v, %v; want %v", received, err, tc.want)
			}
		})
	}
}

func TestCallerActor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if got := plan.CallerActor(ctx); got != "local" {
		t.Fatalf("expected actor 'local' without caller, got %q", got)
	}

	callerCtx := plan.WithCaller(ctx, &plan.WorkerCaller{TaskID: "01a1285b-3bc3-7d85-97b9-e0a54836ee36", WishID: "wish-1"})
	if got := plan.CallerActor(callerCtx); got != "01a1285b-3bc3-7d85-97b9-e0a54836ee36" {
		t.Fatalf("expected actor to be task ID, got %q", got)
	}
}
