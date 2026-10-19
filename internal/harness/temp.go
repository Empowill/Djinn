package harness

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// sharedAge is how old a temporary file must be for sweepTemp to take it for a cut write, in a folder another Djinn
// may write in at the same time (agy's projects, a project's plan folder): a write lasts milliseconds.
const sharedAge = time.Minute

// sweepTemp removes what a kill of the last djinn up left of its writes: the temporary files of fsx.WriteAtomic cut
// before their rename, in each folder Djinn writes its own files in, and the staging folders of the tilasms. It
// removes only those names. Nothing writes in the data folder yet as Recover runs; in a folder that other Djinns
// share, it spares the files younger than sharedAge. It returns what it removed.
func (h *Harness) sweepTemp(ctx context.Context) []string {
	if h.home == "" {
		return nil
	}
	dirs := []string{h.home, filepath.Join(h.home, runDir), filepath.Join(h.home, "lead"), filepath.Dir(gitGuardFile(h.home))}
	var shared []string
	if agy, err := agyProjectsDir(); err == nil {
		shared = append(shared, agy)
	}
	if agy, err := plan.AgyProjectsDir(); err == nil && !slices.Contains(shared, agy) {
		shared = append(shared, agy)
	}
	// Pages, and per project its settings, its installed build and the setup stamps of its worktrees.
	for _, parent := range []string{filepath.Join(h.home, plan.PagesDir), filepath.Join(h.home, "projects")} {
		entries, _ := os.ReadDir(parent)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(parent, e.Name())
			dirs = append(dirs, dir, filepath.Join(dir, "worktrees"), filepath.Join(dir, "integration"), filepath.Join(dir, "push"))
		}
	}
	// The azimas' files, in each project's plan folder and its wish folders.
	if projects, err := store.List[*planv1.Project](ctx, h.store, nil); err == nil {
		for _, p := range projects {
			if p.GetDirectory() == "" {
				continue
			}
			dir := filepath.Join(p.GetDirectory(), plan.PlanDir)
			shared = append(shared, dir)
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if e.IsDir() {
					shared = append(shared, filepath.Join(dir, e.Name()))
				}
			}
		}
	}
	var removed []string
	for _, dir := range slices.Concat(dirs, shared) {
		age := time.Duration(0)
		if slices.Contains(shared, dir) {
			age = sharedAge
		}
		gone, err := fsx.Sweep(dir, age)
		if err != nil {
			logx.L().Warn("sweep the temporary files of cut writes", "dir", dir, "err", err)
		}
		removed = append(removed, gone...)
	}
	gone, kept, err := plan.SweepTilasmTemp(h.home)
	if err != nil {
		logx.L().Warn("sweep the tilasms' temporary folders", "err", err)
	}
	removed = append(removed, gone...)
	for _, dir := range kept {
		logx.L().Warn("a tilasm import was cut: its previous files stay aside", "dir", dir)
	}
	if len(removed) > 0 {
		logx.L().Info("temporary files of cut writes removed", "count", len(removed), "paths", removed)
	}
	return removed
}
