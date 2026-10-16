package harness

import (
	"net/http"

	"connectrpc.com/connect"

	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
)

// ServeHandler returns the Connect handler of ServeService on h, and its path prefix. Its methods answer
// unimplemented until Djinn runs an ifrit's serves (T51): the command line and the protos come first.
func ServeHandler(h *Harness) (string, http.Handler) {
	return planv1connect.NewServeServiceHandler(&Serves{h: h}, connect.WithInterceptors(plan.WorkerGuard, plan.Validate))
}

// Serves implements ServeService.
type Serves struct {
	planv1connect.UnimplementedServeServiceHandler
	h *Harness
}
