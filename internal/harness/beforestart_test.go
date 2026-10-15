package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
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

// envRecorder is the fake agent, which records the environment its worker is given.
type envRecorder struct{ env *atomic.Value }

func (p envRecorder) Start(ctx context.Context, spec Spec) (Worker, error) {
	p.env.Store(append([]string(nil), spec.Env...))
	return Fake{}.Start(ctx, spec)
}

// A worker runs djinn (djinn doc context, djinn task done): the first one on its PATH is the djinn that started it,
// in its data folder, never none (a development build is bin/djinn-dev) nor another Djinn found on the PATH.
// Not portable: links and PATH differ on Windows.
func TestWorkerFindsThisDjinnFirst(t *testing.T) {
	home := t.TempDir()
	dir, err := machine.LinkSelf(home)
	if err != nil {
		t.Fatal(err)
	}
	// Another djinn on Djinn's own PATH: the developer's own.
	other := t.TempDir()
	name := "djinn"
	if filepath.Ext(os.Args[0]) == ".exe" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(other, name), []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", other+string(os.PathListSeparator)+os.Getenv("PATH"))
	env := &atomic.Value{}
	providers := Providers()
	providers[planv1.Provider_PROVIDER_FAKE] = envRecorder{env: env}
	e := upWith(t, home, providers, WithSelfDir(dir))
	wishID, _ := e.wish(t, gitRepo(t))
	task := e.spawn(t, wishID, "text hello")
	e.watch(t.Context(), t, task.GetId(), 0)
	got, _ := env.Load().([]string)
	path := ""
	for _, kv := range got {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v // the last one wins, as exec takes it
		}
	}
	if first := filepath.SplitList(path); len(first) == 0 || first[0] != dir {
		t.Fatalf("the worker's PATH is %q; want %s first", path, dir)
	}
	if !strings.Contains(path, other) {
		t.Errorf("the worker's PATH %q lost Djinn's own folders (%s)", path, other)
	}
	found, err := machine.LookPath("djinn", path)
	if err != nil {
		t.Fatalf("no djinn on the worker's PATH %q: %v", path, err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a, errA := os.Stat(found)
	b, errB := os.Stat(exe)
	if errA != nil || errB != nil || !os.SameFile(a, b) {
		t.Errorf("the worker's djinn is %s; want this executable, %s", found, exe)
	}
	if os.Getenv("PATH") == path {
		t.Errorf("Djinn's own PATH changed to the worker's: %q", path)
	}
}
