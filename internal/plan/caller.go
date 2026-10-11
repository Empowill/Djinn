package plan

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
)

// WorkerCaller identifies a worker making a request through its task and wish.
type WorkerCaller struct {
	TaskID string
	WishID string
}

type callerKey struct{}

// WithCaller returns a context carrying the worker caller identity.
func WithCaller(ctx context.Context, caller *WorkerCaller) context.Context {
	return context.WithValue(ctx, callerKey{}, caller)
}

// CallerFromContext returns the worker caller from ctx, or nil if none.
func CallerFromContext(ctx context.Context) *WorkerCaller {
	if c, ok := ctx.Value(callerKey{}).(*WorkerCaller); ok {
		return c
	}
	return nil
}

// CallerActor returns the actor name for the journal. If ctx has a WorkerCaller,
// its TaskID is returned; otherwise the local user actor is returned.
func CallerActor(ctx context.Context) string {
	if caller := CallerFromContext(ctx); caller != nil && caller.TaskID != "" {
		return caller.TaskID
	}
	return actor
}

// WorkerGuard intercepts Connect RPC calls to enforce permissions for workers.
// A worker cannot make a wish, or execute mutating commands on wishes, tasks, or plans
// other than those permitted for workers (question ask, question revise, block put,
// task update of own task, tilasm put).
var WorkerGuard = connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		caller := CallerFromContext(ctx)
		if caller == nil {
			return next(ctx, req)
		}
		procedure := req.Spec().Procedure

		// 1. Wish creation is strictly forbidden for workers.
		if procedure == planv1connect.WishServiceMakeProcedure {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New(
				"a worker cannot make a wish; to try a command by hand, start a Djinn of your own with its own data folder",
			))
		}

		// 2. Allowed mutating procedures for workers:
		switch procedure {
		case planv1connect.QuestionServiceAskProcedure:
			if r, ok := req.Any().(*planv1.QuestionServiceAskRequest); ok {
				if r.WishId == "" {
					r.WishId = caller.WishID
				} else if r.WishId != caller.WishID {
					return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf(
						"a worker cannot ask a question in wish %s (its task is in wish %s)", r.WishId, caller.WishID,
					))
				}
				if r.TaskId == "" {
					r.TaskId = caller.TaskID
				} else if r.TaskId != caller.TaskID {
					return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf(
						"a worker cannot ask a question for task %s (its own task is %s)", r.TaskId, caller.TaskID,
					))
				}
			}
			return next(ctx, req)

		case planv1connect.QuestionServiceReviseProcedure:
			if r, ok := req.Any().(*planv1.QuestionServiceReviseRequest); ok {
				if r.WishId == "" {
					r.WishId = caller.WishID
				} else if r.WishId != caller.WishID {
					return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf(
						"a worker cannot revise a question in wish %s (its task is in wish %s)", r.WishId, caller.WishID,
					))
				}
				if r.TaskId == "" {
					r.TaskId = caller.TaskID
				} else if r.TaskId != caller.TaskID {
					return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf(
						"a worker cannot revise a question for task %s (its own task is %s)", r.TaskId, caller.TaskID,
					))
				}
			}
			return next(ctx, req)

		case planv1connect.BlockServicePutProcedure:
			if r, ok := req.Any().(*planv1.BlockServicePutRequest); ok {
				if r.WishId == "" {
					r.WishId = caller.WishID
				} else if r.WishId != caller.WishID {
					return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf(
						"a worker cannot put a block in wish %s (its task is in wish %s)", r.WishId, caller.WishID,
					))
				}
				if r.TaskId == "" {
					r.TaskId = caller.TaskID
				} else if r.TaskId != caller.TaskID {
					return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf(
						"a worker cannot put a block for task %s (its own task is %s)", r.TaskId, caller.TaskID,
					))
				}
			}
			return next(ctx, req)

		case planv1connect.TaskServiceUpdateProcedure:
			if r, ok := req.Any().(*planv1.TaskServiceUpdateRequest); ok {
				if r.TaskId == "" {
					r.TaskId = caller.TaskID
				} else if r.TaskId != caller.TaskID {
					return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf(
						"a worker can only update its own task (%s)", caller.TaskID,
					))
				}
			}
			return next(ctx, req)

		case planv1connect.TilasmServicePutProcedure:
			if r, ok := req.Any().(*planv1.TilasmServicePutRequest); ok {
				if r.Wish == "" {
					r.Wish = caller.WishID
				} else if r.Wish != caller.WishID {
					return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf(
						"a worker cannot put a tilasm in wish %s (its task is in wish %s)", r.Wish, caller.WishID,
					))
				}
				if r.Author == "" {
					r.Author = caller.TaskID
				}
			}
			return next(ctx, req)
		}

		// 3. Check if procedure is read-only.
		if isReadOnlyProcedure(procedure) {
			return next(ctx, req)
		}

		// 4. Reject all other mutating calls.
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf(
			"a worker cannot call %s; to try a command by hand, start a Djinn of your own with its own data folder", procedure,
		))
	}
})

func isReadOnlyProcedure(procedure string) bool {
	idx := strings.LastIndexByte(procedure, '/')
	if idx < 0 {
		return false
	}
	method := procedure[idx+1:]
	for _, prefix := range []string{
		"List", "Get", "Watch", "Diff", "Brief", "Snapshot", "Files", "History", "Show", "Sources", "Check", "Render",
	} {
		if strings.HasPrefix(method, prefix) {
			return true
		}
	}
	return false
}
