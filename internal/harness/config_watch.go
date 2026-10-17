package harness

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/gitx"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// WithConfigTick sets how often the watch of the configuration looks at each project's reference branch.
func WithConfigTick(d time.Duration) Option { return func(h *Harness) { h.configTick = d } }

// WatchConfig starts the watch of the projects' configuration on their reference branch (plan.LookAtConfig): a look
// at once, then every plan.ConfigLookEvery, and after each push of Djinn. djinn up calls it once; Close stops it.
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
			tick := time.NewTicker(cmp.Or(h.configTick, plan.ConfigLookEvery))
			defer tick.Stop()
			for {
				h.lookAtConfigs(h.ctx)
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

// kickConfig asks the watch of the configuration for a look: Djinn pushed.
func (h *Harness) kickConfig() {
	select {
	case h.configKick <- struct{}{}:
	default:
	}
}

// lookAtConfigs looks once at the reference branch of each project in Git.
func (h *Harness) lookAtConfigs(ctx context.Context) {
	projects, err := store.List[*planv1.Project](ctx, h.store, nil)
	if err != nil {
		logx.L().Error("read the projects", "loop", "config", "err", err)
		return
	}
	for _, p := range projects {
		if ctx.Err() != nil {
			return
		}
		if !p.GetGit() || p.GetDirectory() == "" {
			continue
		}
		if err := plan.LookAtConfig(ctx, h.store, h.home, p, fetchRef, h.now()); err != nil {
			logx.L().Warn("look at the configuration", "loop", "config", "project", p.GetName(), "err", err)
		}
	}
}

// fetchRef fetches ref, a remote ref (refs/remotes/origin/main), alone and without its tags, into the repository
// holding folder, with the person's own credentials and without ever prompting for them.
func fetchRef(ctx context.Context, folder, ref string) error {
	remoteBranch, ok := strings.CutPrefix(ref, "refs/remotes/")
	if !ok {
		return nil
	}
	remote, target, _ := strings.Cut(remoteBranch, "/")
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	cmd, cancelFetch := gitx.Command(ctx, folder, "fetch", "--no-tags", "--quiet", remote, "+refs/heads/"+target+":"+ref)
	defer cancelFetch()
	cmd.Env = append(cmd.Env, "LC_ALL=C")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("fetch %s from %s: %s", target, remote, cmp.Or(strings.TrimSpace(out.String()), err.Error()))
	}
	return nil
}
