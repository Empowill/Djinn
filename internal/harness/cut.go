package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/logx"
)

// What a stop cuts (W52): djinn up stopping lets the git steps that change a repository end (gitx.Uncut) and cuts the
// rest, the integration's checks, a push, a git that only reads. Close writes what it cut to cutFile; the next start
// names it in the lead's restart line (cutPart), and forgets it. A Djinn killed outright writes nothing: the restart
// line then names the tasks it left integrating, read from the store (recoverIntegrations).

// cutFile, in Djinn's data folder, holds what the last stop cut, until the next start reads it.
const cutFile = "cut.json"

// stopCuts is what the stop cut, for Close to write.
type stopCuts struct {
	doing atomic.Pointer[string] // what the integration does now, for a stop to name: "the push of main to origin"
	mu    sync.Mutex
	what  []string // guarded by mu
}

// setDoing says what the integration does now: the step a stop would cut.
func (h *Harness) setDoing(what string) {
	h.cuts.doing.Store(&what)
}

// cutPass records the integration's step as cut, when djinn up stops.
func (h *Harness) cutPass() {
	if what := h.cuts.doing.Load(); what != nil && *what != "" {
		h.cut(*what)
	}
}

// cutGit records what, a git run in dir, as cut, when djinn up stops: gitx tells each git a cancel stops (OnCut).
func (h *Harness) cutGit(what, dir string) {
	h.cut(what + " in " + h.worktreeName(dir))
}

// worktreeName is how the restart line names the folder dir: a worktree of Djinn's by its role, another by its path.
func (h *Harness) worktreeName(dir string) string {
	rel, err := filepath.Rel(filepath.Join(h.home, "projects"), dir)
	if h.home == "" || err != nil || strings.HasPrefix(rel, "..") {
		return dir
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	switch {
	case len(parts) >= 3 && parts[1] == "integration":
		return "the integration worktree of wish " + parts[2]
	case len(parts) >= 2 && parts[1] == "install":
		return "the install worktree"
	case len(parts) >= 3 && parts[1] == "worktrees":
		return "the worktree of task " + parts[2]
	}
	return dir
}

// cut records what, a git or a step, as cut, when djinn up stops.
func (h *Harness) cut(what string) {
	if h.ctx.Err() == nil {
		return // Not a stop: a request ended, or a step gave up on its own.
	}
	h.cuts.mu.Lock()
	defer h.cuts.mu.Unlock()
	if !slices.Contains(h.cuts.what, what) {
		h.cuts.what = append(h.cuts.what, what)
	}
}

// writeCuts writes what the stop cut for the next start, once every step has ended.
func (h *Harness) writeCuts() {
	h.cuts.mu.Lock()
	what := slices.Clone(h.cuts.what)
	h.cuts.mu.Unlock()
	if h.home == "" || len(what) == 0 {
		return
	}
	logx.L().Warn("djinn up stopped during work", "cut", strings.Join(what, "; "))
	b, err := json.Marshal(what)
	if err == nil {
		err = fsx.WriteAtomic(filepath.Join(h.home, cutFile), b)
	}
	if err != nil {
		logx.L().Error("record what the stop cut", "err", err)
	}
}

// readCuts reads what the last stop cut, and forgets it: it is told once.
func readCuts(home string) []string {
	if home == "" {
		return nil
	}
	path := filepath.Join(home, cutFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var what []string
	if err == nil {
		err = json.Unmarshal(b, &what)
	}
	if err != nil {
		logx.L().Warn("read what the last stop cut", "err", err)
	}
	if err := os.Remove(path); err != nil {
		logx.L().Warn("forget what the last stop cut", "err", err)
	}
	return what
}

// cutPart is the restart line's part naming what the last stop cut, cut, and the tasks it left integrating; "" for
// nothing.
func cutPart(cut, integrating []string) string {
	cut = slices.Clone(cut)
	if left := slices.DeleteFunc(slices.Clone(integrating), func(code string) bool {
		return slices.ContainsFunc(cut, func(c string) bool { return mentions(c, code) })
	}); len(left) > 0 {
		cut = append(cut, "the integration of "+strings.Join(left, ", "))
	}
	if len(cut) == 0 {
		return ""
	}
	return "the stop cut " + strings.Join(cut, ", ")
}

// mentions tells whether text names the task code as a word: W2, not W23.
func mentions(text, code string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], code)
		if j < 0 {
			return false
		}
		end := i + j + len(code)
		if end == len(text) || text[end] < '0' || text[end] > '9' {
			return true
		}
		i = end
	}
}

// lockedPart is the restart line's part naming the person's checkouts left with an index.lock, which Djinn leaves to
// them; "" for none.
func lockedPart(checkouts []string) string {
	switch len(checkouts) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("an index.lock is left in %s, not Djinn's to remove: once no git runs there, remove it", checkouts[0])
	}
	return fmt.Sprintf("index.lock files are left in %s, not Djinn's to remove: once no git runs there, remove them",
		strings.Join(checkouts, ", "))
}
