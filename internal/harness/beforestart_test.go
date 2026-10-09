package harness

import (
	"context"
	"os"
	"sync/atomic"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// pathProvider is the fake agent, which records the PATH its worker starts with.
type pathProvider struct{ path *atomic.Value }

func (p pathProvider) Start(ctx context.Context, spec Spec) (Worker, error) {
	p.path.Store(os.Getenv("PATH"))
	return Fake{}.Start(ctx, spec)
}

// djinn up brings its PATH up to date before each worker starts (machine.ExtendPath): a worker started after an
// agent was installed from the window finds it, without a restart.
func TestBeforeStartRunsBeforeTheWorker(t *testing.T) {
	repo := gitRepo(t)
	installed := t.TempDir()
	path := &atomic.Value{}
	t.Setenv("PATH", os.Getenv("PATH"))
	providers := Providers()
	providers[planv1.Provider_PROVIDER_FAKE] = pathProvider{path: path}
	e := upWith(t, t.TempDir(), providers, WithBeforeStart(func() {
		_ = os.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+installed)
	}))
	wishID, _ := e.wish(t, repo)
	task := e.spawn(t, wishID, "text hello")
	e.watch(t.Context(), t, task.GetId(), 0)
	if got, _ := path.Load().(string); got == "" || got[len(got)-len(installed):] != installed {
		t.Errorf("the worker started with PATH %q; want it to end with %s, added before it started", got, installed)
	}
}
