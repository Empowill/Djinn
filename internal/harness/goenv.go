package harness

// The Go builds Djinn runs (W303, decision Q71). Without -trimpath, the Go build cache keys hold the folder of each
// package: every worktree compiled all of Djinn again, for each set of tags, and the cache grew ~10 GB an hour, 429 GB
// on 17/10/2026 (L23). The processes Djinn starts (workers in their worktrees, watchers, the lead, the commit and push
// checks, an install) get GOFLAGS=-trimpath: an unchanged package is compiled once for every worktree. Where Go is
// absent it changes nothing. The developer's own shells, the window's terminals included, keep their environment:
// a build by hand keeps its full paths for a profiler or a debugger.

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// trimmedGoFlags is env with -trimpath added to its GOFLAGS: appended to the flags already set, the last GOFLAGS
// entry, which is the one a child reads. A GOFLAGS that names -trimpath already, set either way (-trimpath=false for a
// full-path build), is left as it is.
func trimmedGoFlags(env []string) []string {
	flags, at := "", -1
	for i, e := range env {
		if v, ok := strings.CutPrefix(e, "GOFLAGS="); ok {
			flags, at = v, i
		}
	}
	for _, f := range strings.Fields(flags) {
		if name, _, _ := strings.Cut(strings.TrimLeft(f, "-"), "="); name == "trimpath" {
			return env
		}
	}
	entry := "GOFLAGS=" + strings.TrimSpace(flags+" -trimpath")
	if at < 0 {
		return append(slices.Clip(env), entry)
	}
	env = slices.Clone(env)
	env[at] = entry
	return env
}

// goCacheEvery is how long a GOCACHE Djinn found, or the lack of go, holds before it asks again: an install of Go
// meanwhile is seen.
const goCacheEvery = 10 * time.Minute

// goCache finds the Go build cache of this machine: go env GOCACHE, by the go on Djinn's PATH. No go, a cache turned
// off or a go that fails: "", with no error nor warning, Djinn may run where Go is not.
type goCache struct {
	mu    sync.Mutex
	bin   string // the go it asked
	dir   string
	asked time.Time
}

// find is the Go build cache's folder; "" for none. now is the harness's clock.
func (c *goCache) find(ctx context.Context, now time.Time) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	path, err := exec.LookPath("go")
	if err != nil {
		c.bin, c.dir = "", ""
		return ""
	}
	if path == c.bin && !c.asked.IsZero() && now.Sub(c.asked) < goCacheEvery {
		return c.dir
	}
	c.bin, c.dir, c.asked = path, "", now
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "env", "GOCACHE").Output()
	if dir := strings.TrimSpace(string(out)); err == nil && filepath.IsAbs(dir) {
		c.dir = dir
	}
	return c.dir
}
