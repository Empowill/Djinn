package harness

import (
	"cmp"
	"context"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// WithConfigTick sets how often the watch of the configuration looks at each project's reference branch.
func WithConfigTick(d time.Duration) Option { return func(h *Harness) { h.configTick = d } }

// configPoll is how often the watch of the configuration asks itself whether a look is due (configPass): its pace
// changes with the developer's presence (fetch_pace.go).
const configPoll = time.Minute

// WatchConfig starts the watch of the projects' configuration on their reference branch (plan.LookAtConfig): a look
// at once, then every configEvery, and after each push of Djinn at the pushed project. djinn up calls it once; Close
// stops it.
func (h *Harness) WatchConfig() {
	h.configWatching.Do(func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.closed {
			return
		}
		h.wg.Add(1)
		go func() {
			defer logx.Crash("config")
			defer h.wg.Done()
			tick := time.NewTicker(min(cmp.Or(h.configTick, plan.ConfigLookEvery), configPoll))
			defer tick.Stop()
			for {
				h.configPass(h.ctx)
				select {
				case <-h.ctx.Done():
					return
				case <-h.configKick:
				case <-tick.C:
				}
			}
		}()
	})
}

// kickConfig asks the watch of the configuration for a look at once: at the projects named, Djinn pushed there; at
// every project when none is.
func (h *Harness) kickConfig(projectIDs ...string) {
	h.pace.mu.Lock()
	if len(projectIDs) == 0 {
		h.pace.configAll = true
	}
	for _, id := range projectIDs {
		if h.pace.configAsk == nil {
			h.pace.configAsk = map[string]bool{}
		}
		h.pace.configAsk[id] = true
	}
	h.pace.mu.Unlock()
	select {
	case h.configKick <- struct{}{}:
	default:
	}
}

// configPass is one turn of the watch of the configuration: a look at every project when it is due (configEvery) or
// asked, else at the projects asked (kickConfig), else nothing.
func (h *Harness) configPass(ctx context.Context) {
	every, now := h.configEvery(), h.now()
	h.pace.mu.Lock()
	asked, ask := h.pace.configAll, h.pace.configAsk
	h.pace.configAll, h.pace.configAsk = false, nil
	all := asked || h.pace.configLast.IsZero() || now.Sub(h.pace.configLast) >= every
	if all {
		h.pace.configLast = now
	}
	h.pace.mu.Unlock()
	switch {
	case all && !asked:
		// The branch as fetched by any loop within half the pace: a look never reads it older than the pace.
		h.lookAtConfigs(ctx, nil, every/2)
	case all:
		h.lookAtConfigs(ctx, nil, 0) // Asked: the developer is back, a push moved a branch; fetched whatever.
	case len(ask) > 0:
		h.lookAtConfigs(ctx, ask, 0)
	}
}

// lookAtConfigs looks once at the reference branch of each project in Git, or only of those only names: fetched
// unless it was within fresh (fetchFresh).
func (h *Harness) lookAtConfigs(ctx context.Context, only map[string]bool, fresh time.Duration) {
	projects, err := store.List[*planv1.Project](ctx, h.store, nil)
	if err != nil {
		logx.L().Error("read the projects", "loop", "config", "err", err)
		return
	}
	for _, p := range projects {
		if ctx.Err() != nil {
			return
		}
		if !p.GetGit() || p.GetDirectory() == "" || only != nil && !only[p.GetId()] {
			continue
		}
		fetch := func(ctx context.Context, folder, ref string) error {
			return h.fetchMainFresh(ctx, "", folder, ref, fresh)
		}
		if err := plan.LookAtConfig(ctx, h.store, h.home, p, fetch, h.now()); err != nil {
			logx.L().Warn("look at the configuration", "loop", "config", "project", p.GetName(), "err", err)
		}
	}
}
