package harness

// The hourglass Disk tab's measures (W435, T65, W122). Two passes walk the disk, each on demand, never on its own:
//
//   - the regular one measures Djinn's folders (each project's worktrees and scratch, split between live and ended
//     tasks, its logs and tilasms, its data folder, the projects' node_modules) and the known caches (Go, Docker,
//     golangci-lint, npm, pip, uv, pnpm, JetBrains), each walk within its own diskWalkTimeout: on 18/11/2026 the walk
//     of 800 worktrees on an eCryptfs home ate a shared two minutes, and the folders after it came back at 0 and left
//     the list (L26). A walk that runs out of time keeps the last whole measure, or says its size is a floor. A pass
//     holds diskEveryFactor times what it took, so that a walk of minutes runs a few times a day, not each 5 minutes;
//   - the slow one measures, every diskRestEvery, what no row counts: each entry of the home folder (those of
//     ~/.cache, ~/.config, ~/.local, ~/.local/share and ~/go one by one) and /var, /usr, /opt when on the same disk,
//     leaving out the folders the regular pass counts. It runs once the regular pass has measured, and its measure,
//     kept in diskRestFile, holds across restarts.
//
// A file counts by the blocks it takes (onDisk), not its length: on eCryptfs a header and the rounding up to a block
// double a small file, and a cache of small files takes far more than its length says.

import (
	"cmp"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/store"
)

const (
	// diskWalkTimeout bounds the walk of one folder or cache of the regular pass, each its own.
	diskWalkTimeout = 10 * time.Minute
	// diskEveryFactor is how many times what a regular pass took it holds before it walks again, between
	// diskFoldersEvery and diskRestEvery.
	diskEveryFactor = 10
	// diskRestEvery is how long the slow pass's measure holds.
	diskRestEvery = 6 * time.Hour
	// diskRestTimeout bounds the slow pass's walk of one entry.
	diskRestTimeout = 30 * time.Minute
	// diskRestMin is the size under which an entry of the home joins the "~" row.
	diskRestMin = GB
	// diskRestFile, in the data folder, keeps the slow pass's measure across restarts.
	diskRestFile = "disk-rest.json"
)

// The kinds of a DiskFolder.
const (
	folderWorktrees   = "worktrees"
	folderScratch     = "scratch"
	folderData        = "data"
	folderLogs        = "logs"
	folderTilasms     = "tilasms"
	folderNodeModules = "node_modules"
	folderGoBuild     = "go-build"
	folderNpm         = "npm"
	folderPlaywright  = "playwright"
	folderTmp         = "tmp"
	folderHome        = "home"
	folderSystem      = "system"
)

// restExpand are the folders of the home the slow pass splits into their entries: caches, settings and data of many
// tools, and the Go path, whose sum names nothing.
var restExpand = []string{".cache", ".config", ".local", ".local/share", "go"}

// diskDir is a folder the regular pass measures: its path, its kind, and the project whose worktrees or scratch it is.
type diskDir struct {
	path    string
	kind    string
	project string // the project's id
}

// walked is the measure of one folder: what its files take on disk, whether its walk ran out of time, and what each
// of its entries takes, by name.
type walked struct {
	bytes    uint64
	partial  bool
	children map[string]uint64
}

// walk sums what the files under dir take on disk, within timeout, on dir's file system only. A folder of known
// other than dir is not walked: with add, its size counts; without, it is left out, as another row counts it.
func walk(ctx context.Context, dir string, timeout time.Duration, known map[string]uint64, add bool) walked {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	root := filepath.Clean(dir)
	w := walked{children: map[string]uint64{}}
	var rootDev uint64
	oneFS := false
	if info, err := os.Lstat(root); err == nil {
		rootDev, oneFS = device(info)
	}
	_ = filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil //nolint:nilerr // A folder it cannot read is left out of its size.
		}
		clean := filepath.Clean(path)
		child := firstPart(root, clean)
		if e.IsDir() {
			if clean == root {
				return nil
			}
			if sub, ok := known[clean]; ok {
				if add {
					w.bytes += sub
					w.children[child] += sub
				}
				return filepath.SkipDir
			}
			if oneFS {
				if info, err := e.Info(); err == nil {
					if dev, ok := device(info); ok && dev != rootDev {
						return filepath.SkipDir // A mount inside: another disk's, or one of its own counted twice.
					}
				}
			}
			return nil
		}
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			n := onDisk(info)
			w.bytes += n
			w.children[child] += n
		}
		return nil
	})
	w.partial = ctx.Err() != nil
	return w
}

// firstPart is the name of the entry of root that path is in; "" for root itself.
func firstPart(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return ""
	}
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	return first
}

// isSubfolder says whether child is inside parent.
func isSubfolder(child, parent string) bool {
	c, p := filepath.Clean(child), filepath.Clean(parent)
	return len(c) > len(p) && strings.HasPrefix(c, p+string(filepath.Separator))
}

// measureFolders walks each of dirs within its own timeout, a folder inside another one first, so that the other
// counts it without walking it again. onUpdate, when set, hears the measures so far after each walk.
func measureFolders(
	ctx context.Context, dirs []diskDir, timeout time.Duration, onUpdate func(map[string]walked),
) map[string]walked {
	order := make([]string, 0, len(dirs))
	for _, d := range dirs {
		order = append(order, filepath.Clean(d.path))
	}
	// Deepest first: a folder is walked after every folder inside it.
	slices.SortStableFunc(order, func(a, b string) int {
		return cmp.Compare(strings.Count(b, string(filepath.Separator)), strings.Count(a, string(filepath.Separator)))
	})
	known := make(map[string]uint64, len(order))
	out := make(map[string]walked, len(order))
	for _, dir := range order {
		if _, done := out[dir]; done || ctx.Err() != nil {
			continue
		}
		w := walk(ctx, dir, timeout, known, true)
		for sub, got := range out {
			if got.partial && isSubfolder(sub, dir) {
				w.partial = true // It counts a floor.
			}
		}
		known[dir] = w.bytes
		out[dir] = w
		if onUpdate != nil {
			onUpdate(out)
		}
	}
	return out
}

// passEvery is how long a regular pass that took took holds.
func passEvery(took time.Duration) time.Duration {
	return min(max(diskEveryFactor*took, diskFoldersEvery), diskRestEvery)
}

// DiskFolders returns the folders on disk the hourglass Disk tab lists: Djinn's, biggest first, then the slow pass's
// home and system ones. It measures Djinn's on the server when the last measure no longer holds (passEvery), without
// blocking the caller when a large folder takes longer, and starts the slow pass when its measure is older than
// diskRestEvery.
func (h *Harness) DiskFolders() []*machinev1.DiskFolder {
	d := &h.disk
	now := h.now()
	d.mu.Lock()
	h.loadRest()
	fresh := !d.measuredAt.IsZero() && now.Sub(d.measuredAt) < cmp.Or(d.foldersEvery, diskFoldersEvery)
	if fresh || d.measuring || h.ctx.Err() != nil {
		out := d.foldersOut()
		d.mu.Unlock()
		h.diskRest()
		return out
	}
	first := len(d.measured) == 0
	previous := d.measured
	d.measuring = true
	done := make(chan struct{})
	d.wg.Add(1)
	d.mu.Unlock()

	go func() {
		defer logx.Crash("disk folders")
		defer d.wg.Done()
		defer close(done)
		var cache string
		if d.cacheDir != nil {
			cache = d.cacheDir()
		} else if d.guard.Free != nil {
			cache = d.cache.find(h.ctx, h.now())
		}
		dirs := h.diskFolders(h.ctx, cache)
		var onUpdate func(map[string]walked)
		if first {
			onUpdate = func(partial map[string]walked) {
				folders := h.toDiskFolders(h.ctx, false, dirs, partial, nil, time.Time{})
				d.mu.Lock()
				d.measured = folders
				d.mu.Unlock()
			}
		}
		start := time.Now()
		walks := measureFolders(h.ctx, dirs, diskWalkTimeout, onUpdate)
		took := time.Since(start)
		folders := h.toDiskFolders(h.ctx, true, dirs, walks, previous, h.now())
		d.mu.Lock()
		d.measured = folders
		d.measuring = false
		if h.ctx.Err() == nil {
			d.measuredAt = h.now()
			d.foldersEvery = passEvery(took)
		}
		d.mu.Unlock()
		logx.L().Debug("disk folders measured", "folders", len(folders), "took", took.Round(time.Millisecond))
		h.diskRest()
	}()

	select {
	case <-done:
	case <-time.After(cmp.Or(d.wait, diskFoldersWait)):
	case <-h.ctx.Done():
	}
	d.mu.Lock()
	out := d.foldersOut()
	d.mu.Unlock()
	return out
}

// foldersOut is Djinn's folders, then the slow pass's, each biggest first. d.mu is held.
func (d *disk) foldersOut() []*machinev1.DiskFolder {
	return append(slices.Clone(d.measured), d.rest...)
}

// toDiskFolders makes the rows of the measured folders, biggest first. A folder whose walk ran out of time keeps its
// previous whole size when that was bigger, said partial; one that measured nothing is left out. With ctx, the
// worktrees and scratch are split between live and ended tasks, read in the store, when split.
func (h *Harness) toDiskFolders(
	ctx context.Context, split bool, dirs []diskDir, walks map[string]walked, previous []*machinev1.DiskFolder, at time.Time,
) []*machinev1.DiskFolder {
	before := make(map[string]*machinev1.DiskFolder, len(previous))
	for _, f := range previous {
		before[filepath.Clean(f.GetPath())] = f
	}
	var names map[string]string
	var tasks map[string]*planv1.Task
	if split && h.store != nil {
		names, tasks = h.diskTasks(ctx, dirs, walks)
	}
	var out []*machinev1.DiskFolder
	for _, dir := range dirs {
		clean := filepath.Clean(dir.path)
		w, ok := walks[clean]
		if !ok {
			continue
		}
		f := &machinev1.DiskFolder{
			Path: dir.path, SizeBytes: w.bytes, Partial: w.partial, Kind: dir.kind, Project: names[dir.project],
		}
		if !at.IsZero() {
			f.MeasureTime = timestamppb.New(at)
		}
		if p := before[clean]; w.partial && p != nil && p.GetSizeBytes() > w.bytes {
			f.SizeBytes = p.GetSizeBytes()
		}
		if f.GetSizeBytes() == 0 {
			continue
		}
		if tasks != nil && (dir.kind == folderWorktrees || dir.kind == folderScratch) {
			for id, bytes := range w.children {
				f.Count++
				t := tasks[id]
				if t == nil || t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE ||
					t.GetStatus() == planv1.TaskStatus_TASK_STATUS_CANCELLED {
					f.EndedCount++
					f.EndedBytes += bytes
				}
			}
		}
		out = append(out, f)
	}
	slices.SortStableFunc(out, func(a, b *machinev1.DiskFolder) int { return cmp.Compare(b.GetSizeBytes(), a.GetSizeBytes()) })
	return out
}

// diskTasks reads the projects' names by id, and the tasks whose folders the worktrees and scratch hold, by id.
func (h *Harness) diskTasks(
	ctx context.Context, dirs []diskDir, walks map[string]walked,
) (names map[string]string, tasks map[string]*planv1.Task) {
	names = map[string]string{}
	if projects, err := store.List[*planv1.Project](ctx, h.store, nil); err == nil {
		for _, p := range projects {
			names[p.GetId()] = p.GetName()
		}
	}
	var ids []string
	for _, dir := range dirs {
		if dir.kind != folderWorktrees && dir.kind != folderScratch {
			continue
		}
		for id := range walks[filepath.Clean(dir.path)].children {
			ids = append(ids, id)
		}
	}
	tasks, err := store.GetMany[*planv1.Task](ctx, h.store, ids)
	if err != nil {
		logx.L().Debug("read the tasks of the worktrees", "err", err)
		return names, nil
	}
	return names, tasks
}

// diskFolders are the folders the regular pass measures: each project's worktrees and scratch, Djinn's logs, tilasms
// and data folder, each project's node_modules, and the folders that fill a disk Djinn works on (knownFolders).
func (h *Harness) diskFolders(ctx context.Context, cache string) []diskDir {
	var raw []diskDir
	if h.home != "" {
		raw = append(raw, h.projectFolders()...)
		raw = append(raw,
			diskDir{path: filepath.Join(h.home, "logs"), kind: folderLogs},
			diskDir{path: filepath.Join(h.home, "tilasms"), kind: folderTilasms},
			diskDir{path: h.home, kind: folderData})
	}
	if h.store != nil {
		if projects, err := store.List[*planv1.Project](ctx, h.store, nil); err == nil {
			for _, p := range projects {
				if dir := p.GetDirectory(); dir != "" {
					raw = append(raw, diskDir{path: filepath.Join(dir, "node_modules"), kind: folderNodeModules})
				}
			}
		}
	}
	if h.disk.folders != nil {
		raw = append(raw, h.disk.folders(cache)...)
	} else if h.disk.guard.Free != nil || h.disk.cacheDir != nil {
		raw = append(raw, h.knownFolders(cache)...)
	}
	seen := make(map[string]bool, len(raw))
	var dirs []diskDir
	for _, dir := range raw {
		if dir.path == "" {
			continue
		}
		clean := filepath.Clean(dir.path)
		if !seen[clean] {
			seen[clean] = true
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// projectFolders are each project's worktrees and scratch folders in the data folder.
func (h *Harness) projectFolders() []diskDir {
	var dirs []diskDir
	if found, err := filepath.Glob(filepath.Join(h.home, "projects", "*", "worktrees")); err == nil {
		for _, dir := range found {
			dirs = append(dirs, diskDir{path: dir, kind: folderWorktrees, project: filepath.Base(filepath.Dir(dir))})
		}
	}
	if found, err := filepath.Glob(filepath.Join(h.home, "scratch", "*")); err == nil {
		for _, dir := range found {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				dirs = append(dirs, diskDir{path: dir, kind: folderScratch, project: filepath.Base(dir)})
			}
		}
	}
	return dirs
}

// knownFolders are the folders that fill a disk Djinn works on: the Go build cache, each project's worktrees and
// scratch, Djinn's logs and tilasms, the npm and Playwright caches, the temporary folder.
func (h *Harness) knownFolders(cache string) []diskDir {
	var dirs []diskDir
	if cache != "" {
		dirs = append(dirs, diskDir{path: cache, kind: folderGoBuild})
	}
	if h.home != "" {
		dirs = append(dirs, h.projectFolders()...)
		dirs = append(dirs,
			diskDir{path: filepath.Join(h.home, "logs"), kind: folderLogs},
			diskDir{path: filepath.Join(h.home, "tilasms"), kind: folderTilasms})
	}
	if user, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			diskDir{path: filepath.Join(user, ".npm"), kind: folderNpm},
			diskDir{path: filepath.Join(user, ".cache", "ms-playwright"), kind: folderPlaywright})
	}
	return append(dirs, diskDir{path: os.TempDir(), kind: folderTmp})
}

// DiskItems returns the known caches and stores on the machine, biggest first, with their size, what their cleanup
// command frees and its risk. It measures them when the last measure no longer holds (passEvery), without blocking
// the caller when a large cache takes longer. Djinn never runs a cleanup command: it only reports it.
func (h *Harness) DiskItems() []*machinev1.DiskItem {
	d := &h.disk
	now := h.now()
	d.mu.Lock()
	fresh := !d.itemsMeasuredAt.IsZero() && now.Sub(d.itemsMeasuredAt) < cmp.Or(d.itemsEvery, diskFoldersEvery)
	if fresh || d.itemsMeasuring || h.ctx.Err() != nil {
		out := slices.Clone(d.items)
		d.mu.Unlock()
		return out
	}
	first := len(d.items) == 0
	previous := d.items
	d.itemsMeasuring = true
	done := make(chan struct{})
	d.wg.Add(1)
	d.mu.Unlock()

	go func() {
		defer logx.Crash("disk items")
		defer d.wg.Done()
		defer close(done)
		now := h.now()
		var cache string
		if d.cacheDir != nil {
			cache = d.cacheDir()
		} else if d.guard.Free != nil {
			cache = d.cache.find(h.ctx, now)
		}
		start := time.Now()
		var items []*machinev1.DiskItem
		var dirs map[string]uint64
		if d.measure != nil {
			items = d.measure(h.ctx, now, cache)
		} else {
			var onUpdate func([]*machinev1.DiskItem)
			if first {
				onUpdate = func(partial []*machinev1.DiskItem) {
					d.mu.Lock()
					d.items = partial
					d.mu.Unlock()
				}
			}
			items, dirs = h.measureDiskItems(h.ctx, now, cache, onUpdate)
		}
		items = keepWhole(items, previous)
		took := time.Since(start)
		d.mu.Lock()
		d.items = items
		d.itemDirs = dirs
		d.itemsMeasuring = false
		if h.ctx.Err() == nil {
			d.itemsMeasuredAt = h.now()
			d.itemsEvery = passEvery(took)
		}
		d.mu.Unlock()
		h.diskRest()
	}()

	select {
	case <-done:
	case <-time.After(cmp.Or(d.wait, diskFoldersWait)):
	case <-h.ctx.Done():
	}
	d.mu.Lock()
	out := slices.Clone(d.items)
	d.mu.Unlock()
	return out
}

// MeasureDisk measures Djinn's folders and the caches again now, as the Disk tab's button asks, whatever the age of
// the last measure; it says already, and starts none, when one is going. The measure that runs out of time on a folder
// keeps its last whole size, as any other. It deletes nothing.
func (h *Harness) MeasureDisk() (already bool) {
	d := &h.disk
	d.mu.Lock()
	if d.measuring || d.itemsMeasuring {
		d.mu.Unlock()
		return true
	}
	d.measuredAt = time.Time{}
	d.itemsMeasuredAt = time.Time{}
	d.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.DiskItems()
	}()
	h.DiskFolders()
	<-done
	return false
}

// DiskState says when Djinn's folders and the caches were last measured whole, the older of the two (zero before both
// were), and whether a measure of either is going now.
func (h *Harness) DiskState() (at time.Time, measuring bool) {
	d := &h.disk
	d.mu.Lock()
	defer d.mu.Unlock()
	measuring = d.measuring || d.itemsMeasuring
	if d.measuredAt.IsZero() || d.itemsMeasuredAt.IsZero() {
		return time.Time{}, measuring
	}
	if d.itemsMeasuredAt.Before(d.measuredAt) {
		return d.itemsMeasuredAt, measuring
	}
	return d.measuredAt, measuring
}

// keepWhole gives an item whose walk ran out of time its previous size when that was bigger.
func keepWhole(items, previous []*machinev1.DiskItem) []*machinev1.DiskItem {
	out := make([]*machinev1.DiskItem, 0, len(items))
	for _, it := range items {
		if it.GetPartial() {
			for _, p := range previous {
				if p.GetKind() == it.GetKind() && p.GetSizeBytes() > it.GetSizeBytes() {
					kept, _ := proto.Clone(it).(*machinev1.DiskItem)
					kept.SizeBytes, kept.ReclaimableBytes = p.GetSizeBytes(), p.GetReclaimableBytes()
					it = kept
				}
			}
		}
		out = append(out, it)
	}
	return sortedDiskItems(out)
}

// measureDiskItems measures the known caches and stores on the machine (Go build cache, isolated *-go-build caches,
// Go module and download caches, golangci-lint, Docker via `docker system df`, JetBrains, pnpm, pip, uv, npm), each
// walk within its own diskWalkTimeout, and returns the non-empty ones biggest first, with the folders it walked. Fast
// items are measured before large Go caches so onUpdate populates them within diskFoldersWait.
func (h *Harness) measureDiskItems(
	ctx context.Context, now time.Time, cache string, onUpdate func([]*machinev1.DiskItem),
) ([]*machinev1.DiskItem, map[string]uint64) {
	userHome, _ := os.UserHomeDir()
	dirs := map[string]uint64{}
	var items []*machinev1.DiskItem
	add := func(item *machinev1.DiskItem) {
		if item == nil || item.GetSizeBytes() == 0 {
			return
		}
		items = append(items, item)
		if onUpdate != nil {
			onUpdate(sortedDiskItems(items))
		}
	}
	// size walks each of paths in the home, and says what they take and whether a walk ran out of time.
	size := func(paths ...string) (uint64, bool) {
		var total uint64
		var partial bool
		for _, p := range paths {
			dir := filepath.Join(append([]string{userHome}, strings.Split(p, "/")...)...)
			w := walk(ctx, dir, diskWalkTimeout, nil, false)
			if w.bytes > 0 {
				dirs[filepath.Clean(dir)] = w.bytes
			}
			total += w.bytes
			partial = partial || w.partial
		}
		return total, partial
	}

	if userHome != "" {
		if n, partial := size(".npm"); n > 0 {
			add(&machinev1.DiskItem{
				Kind: "npm", Path: "~/.npm", SizeBytes: n, ReclaimableBytes: n, Partial: partial,
				Risk: machinev1.DiskRisk_DISK_RISK_REBUILD, Command: "npm cache clean --force",
			})
		}
		if n, partial := size(".cache/pip"); n > 0 {
			add(&machinev1.DiskItem{
				Kind: "pip", Path: "~/.cache/pip", SizeBytes: n, ReclaimableBytes: n, Partial: partial,
				Risk: machinev1.DiskRisk_DISK_RISK_REBUILD, Command: "pip cache purge",
			})
		}
		if n, partial := size(".cache/uv"); n > 0 {
			add(&machinev1.DiskItem{
				Kind: "uv", Path: "~/.cache/uv", SizeBytes: n, ReclaimableUnknown: true, Partial: partial,
				Risk: machinev1.DiskRisk_DISK_RISK_SAFE, Command: "uv cache prune",
			})
		}
		if n, partial := size(".local/share/pnpm", ".cache/pnpm"); n > 0 {
			add(&machinev1.DiskItem{
				Kind: "pnpm", Path: "~/.local/share/pnpm + ~/.cache/pnpm", SizeBytes: n, ReclaimableUnknown: true,
				Partial: partial, Risk: machinev1.DiskRisk_DISK_RISK_SAFE, Command: "pnpm store prune",
			})
		}
		if n, partial := size(".cache/JetBrains"); n > 0 {
			add(&machinev1.DiskItem{
				Kind: "jetbrains", Path: "~/.cache/JetBrains", SizeBytes: n, ReclaimableBytes: n, Partial: partial,
				Risk: machinev1.DiskRisk_DISK_RISK_LOSS, Command: "rm -rf ~/.cache/JetBrains",
			})
		}
		if n, partial := size(".cache/golangci-lint"); n > 0 {
			add(&machinev1.DiskItem{
				Kind: "golangci-lint", Path: "~/.cache/golangci-lint", SizeBytes: n, ReclaimableBytes: n,
				Partial: partial, Risk: machinev1.DiskRisk_DISK_RISK_REBUILD, Command: "golangci-lint cache clean",
			})
		}
	}

	for _, item := range measureDockerItems(ctx) {
		add(item)
	}

	if userHome != "" {
		if matches, err := filepath.Glob(filepath.Join(userHome, ".cache", "*-go-build")); err == nil {
			var isoSize uint64
			var isoCount int32
			var isoPartial bool
			for _, m := range matches {
				rel, _ := filepath.Rel(userHome, m)
				if n, partial := size(filepath.ToSlash(rel)); n > 0 {
					isoSize += n
					isoCount++
					isoPartial = isoPartial || partial
				}
			}
			if isoSize > 0 {
				add(&machinev1.DiskItem{
					Kind: "go-isolated", Path: "~/.cache/*-go-build", SizeBytes: isoSize, ReclaimableBytes: isoSize,
					Partial: isoPartial, Risk: machinev1.DiskRisk_DISK_RISK_REBUILD,
					Command: "rm -rf ~/.cache/*-go-build", Count: isoCount, Djinn: true,
				})
			}
		}
		// The whole ~/go/pkg: the module cache and its download cache (L26: 40.7 GB where pkg/mod alone read 13.2).
		if n, partial := size("go/pkg"); n > 0 {
			add(&machinev1.DiskItem{
				Kind: "go-mod", Path: "~/go/pkg", SizeBytes: n, ReclaimableBytes: n, Partial: partial,
				Risk: machinev1.DiskRisk_DISK_RISK_REBUILD, Command: "go clean -modcache",
			})
		}
	}

	goBuildDir := cache
	if h.disk.cacheDir == nil && userHome != "" {
		def := filepath.Join(userHome, ".cache", "go-build")
		if info, err := os.Stat(def); err == nil && info.IsDir() {
			goBuildDir = def
		}
	}
	if goBuildDir != "" {
		n, reclaim, partial := measureGoBuildCache(ctx, goBuildDir, now.Add(-goBuildReclaimableAge))
		if n > 0 {
			dirs[filepath.Clean(goBuildDir)] = n
			add(&machinev1.DiskItem{
				Kind: "go-build", Path: "~/.cache/go-build", SizeBytes: n, ReclaimableBytes: reclaim, Partial: partial,
				Risk: machinev1.DiskRisk_DISK_RISK_REBUILD, Command: "find ~/.cache/go-build -type f -mmin +360 -delete",
			})
		}
	}

	return sortedDiskItems(items), dirs
}

func sortedDiskItems(items []*machinev1.DiskItem) []*machinev1.DiskItem {
	out := slices.Clone(items)
	slices.SortStableFunc(out, func(a, b *machinev1.DiskItem) int {
		return cmp.Compare(b.GetSizeBytes(), a.GetSizeBytes())
	})
	return out
}

// measureGoBuildCache says what the Go build cache at dir takes on disk, what of it was unused since cutoff, and
// whether its walk ran out of diskWalkTimeout.
func measureGoBuildCache(ctx context.Context, dir string, cutoff time.Time) (total, reclaimable uint64, partial bool) {
	ctx, cancel := context.WithTimeout(ctx, diskWalkTimeout)
	defer cancel()
	_ = filepath.WalkDir(dir, func(_ string, e fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || e.IsDir() {
			return nil //nolint:nilerr // A folder it cannot read is left out of its size.
		}
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			n := onDisk(info)
			total += n
			if info.ModTime().Before(cutoff) {
				reclaimable += n
			}
		}
		return nil
	})
	return total, reclaimable, ctx.Err() != nil
}

// restRoots are the home folder, when on the disk of the data folder, and the system folders on that disk.
func (h *Harness) restRoots() (home string, system []string) {
	if h.disk.restRoots != nil {
		return h.disk.restRoots()
	}
	if h.home == "" {
		return "", nil
	}
	if user, err := os.UserHomeDir(); err == nil && (isSubfolder(h.home, user) || sameDisk(user, h.home)) {
		home = user
	}
	if runtime.GOOS == "linux" {
		for _, dir := range []string{"/var", "/usr", "/opt"} {
			if sameDisk(dir, h.home) {
				system = append(system, dir)
			}
		}
	}
	return home, system
}

// diskRest starts the slow pass when its measure is older than diskRestEvery, once the regular pass measured the
// folders and caches it leaves out. Without a disk guard nor test roots, it never runs.
func (h *Harness) diskRest() {
	d := &h.disk
	if d.restRoots == nil && d.guard.Free == nil {
		return
	}
	now := h.now()
	d.mu.Lock()
	h.loadRest()
	if d.restRunning || d.measuredAt.IsZero() || d.itemsMeasuredAt.IsZero() || h.ctx.Err() != nil ||
		(!d.restAt.IsZero() && now.Sub(d.restAt) < diskRestEvery) {
		d.mu.Unlock()
		return
	}
	d.restRunning = true
	known := map[string]uint64{}
	for _, f := range d.measured {
		known[filepath.Clean(f.GetPath())] = f.GetSizeBytes()
	}
	for dir, n := range d.itemDirs {
		known[dir] = n
	}
	if h.home != "" {
		known[filepath.Clean(h.home)] = 0
	}
	previous := d.rest
	minBytes := cmp.Or(d.restMin, diskRestMin)
	d.wg.Add(1)
	d.mu.Unlock()

	go func() {
		defer logx.Crash("disk rest")
		defer d.wg.Done()
		start := time.Now()
		home, system := h.restRoots()
		rest := measureRest(h.ctx, home, system, known, minBytes, previous, h.now())
		d.mu.Lock()
		d.restRunning = false
		if h.ctx.Err() == nil {
			d.rest, d.restAt = rest, h.now()
		}
		at := d.restAt
		d.mu.Unlock()
		if h.ctx.Err() == nil {
			h.saveRest(rest, at)
			logx.L().Info("disk rest measured", "entries", len(rest), "took", time.Since(start).Round(time.Second))
		}
	}()
}

// measureRest walks each entry of the home (those of restExpand's folders one by one) and each system folder, each
// within diskRestTimeout, leaving out the folders of known, which other rows count. A home entry under minBytes joins
// the "~" row. A walk that runs out of time keeps the previous size when it was bigger, said partial.
func measureRest(
	ctx context.Context, home string, system []string, known map[string]uint64, minBytes uint64,
	previous []*machinev1.DiskFolder, at time.Time,
) []*machinev1.DiskFolder {
	before := make(map[string]uint64, len(previous))
	for _, f := range previous {
		before[f.GetPath()] = f.GetSizeBytes()
	}
	var entries []diskDir
	if home != "" {
		for _, p := range homeEntries(home) {
			entries = append(entries, diskDir{path: p, kind: folderHome})
		}
	}
	for _, dir := range system {
		entries = append(entries, diskDir{path: dir, kind: folderSystem})
	}
	small := &machinev1.DiskFolder{Path: "~", Kind: folderHome, MeasureTime: timestamppb.New(at)}
	var out []*machinev1.DiskFolder
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		if _, ok := known[filepath.Clean(e.path)]; ok {
			continue
		}
		w := walk(ctx, e.path, diskRestTimeout, known, false)
		path := e.path
		if e.kind == folderHome {
			if rel, err := filepath.Rel(home, e.path); err == nil {
				path = "~/" + filepath.ToSlash(rel)
			}
		}
		bytes := w.bytes
		if w.partial && before[path] > bytes {
			bytes = before[path]
		}
		if e.kind == folderHome && bytes < minBytes {
			small.SizeBytes += bytes
			small.Count++
			small.Partial = small.Partial || w.partial
			continue
		}
		if bytes > 0 {
			out = append(out, &machinev1.DiskFolder{
				Path: path, Kind: e.kind, SizeBytes: bytes, Partial: w.partial, MeasureTime: timestamppb.New(at),
			})
		}
	}
	if small.GetSizeBytes() > 0 {
		out = append(out, small)
	}
	slices.SortStableFunc(out, func(a, b *machinev1.DiskFolder) int { return cmp.Compare(b.GetSizeBytes(), a.GetSizeBytes()) })
	return out
}

// homeEntries are the entries of home, those of restExpand's folders one by one; a symbolic link is left out, as
// what it points to is elsewhere.
func homeEntries(home string) []string {
	var out []string
	var add func(dir string)
	add = func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if e.Type()&fs.ModeSymlink != 0 {
				continue
			}
			if rel, err := filepath.Rel(home, p); err == nil && e.IsDir() && slices.Contains(restExpand, filepath.ToSlash(rel)) {
				add(p)
				continue
			}
			out = append(out, p)
		}
	}
	add(home)
	return out
}

// restSaved is diskRestFile: the slow pass's measure and when it ended.
type restSaved struct {
	Time    time.Time  `json:"time"`
	Folders []restLine `json:"folders"`
}

type restLine struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Bytes   uint64 `json:"bytes"`
	Partial bool   `json:"partial,omitempty"`
	Count   int32  `json:"count,omitempty"`
}

// loadRest reads diskRestFile once, so that a restart shows the last slow pass and runs it again only when it is
// older than diskRestEvery. d.mu is held.
func (h *Harness) loadRest() {
	d := &h.disk
	if d.restLoaded || h.home == "" {
		return
	}
	d.restLoaded = true
	data, err := os.ReadFile(filepath.Join(h.home, diskRestFile))
	if err != nil {
		return
	}
	var saved restSaved
	if err := json.Unmarshal(data, &saved); err != nil {
		logx.L().Debug("read the disk rest", "err", err)
		return
	}
	at := timestamppb.New(saved.Time)
	for _, l := range saved.Folders {
		d.rest = append(d.rest, &machinev1.DiskFolder{
			Path: l.Path, Kind: l.Kind, SizeBytes: l.Bytes, Partial: l.Partial, Count: l.Count, MeasureTime: at,
		})
	}
	d.restAt = saved.Time
}

// saveRest writes the slow pass's measure in diskRestFile.
func (h *Harness) saveRest(rest []*machinev1.DiskFolder, at time.Time) {
	if h.home == "" {
		return
	}
	saved := restSaved{Time: at}
	for _, f := range rest {
		saved.Folders = append(saved.Folders, restLine{
			Path: f.GetPath(), Kind: f.GetKind(), Bytes: f.GetSizeBytes(), Partial: f.GetPartial(), Count: f.GetCount(),
		})
	}
	data, err := json.Marshal(saved)
	if err == nil {
		err = os.WriteFile(filepath.Join(h.home, diskRestFile), data, 0o600)
	}
	if err != nil {
		logx.L().Warn("keep the disk rest", "err", err)
	}
}

// diskSource is a source of the disk's use, named for the lead.
type diskSource struct {
	name    string
	bytes   uint64
	ended   uint64 // of worktrees or scratch, what ended tasks' take
	partial bool
}

// diskSources are the sources of the disk's use Djinn knows, biggest first: Djinn's folders, the caches and the slow
// pass's home and system folders. It starts the measures that no longer hold, and waits up to wait on a first one.
func (h *Harness) diskSources(wait time.Duration) []diskSource {
	h.DiskFolders()
	h.DiskItems()
	d := &h.disk
	deadline := time.Now().Add(wait)
	for {
		d.mu.Lock()
		measured := !d.measuredAt.IsZero() && !d.itemsMeasuredAt.IsZero()
		d.mu.Unlock()
		if measured || time.Now().After(deadline) || h.ctx.Err() != nil {
			break
		}
		select {
		case <-h.ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	d.mu.Lock()
	folders, items, rest := slices.Clone(d.measured), slices.Clone(d.items), slices.Clone(d.rest)
	d.mu.Unlock()
	var out []diskSource
	for _, f := range folders {
		switch f.GetKind() {
		case folderData:
			continue // The sum of the others.
		case folderGoBuild, folderNpm:
			if len(items) > 0 {
				continue // An item counts it.
			}
		}
		s := diskSource{name: f.GetPath(), bytes: f.GetSizeBytes(), ended: f.GetEndedBytes(), partial: f.GetPartial()}
		if p := f.GetProject(); p != "" {
			s.name = "the " + f.GetKind() + " of " + p
		}
		out = append(out, s)
	}
	for _, it := range items {
		out = append(out, diskSource{name: it.GetPath(), bytes: it.GetSizeBytes(), partial: it.GetPartial()})
	}
	for _, f := range rest {
		name := f.GetPath()
		if name == "~" {
			name = "the home's small entries"
		}
		out = append(out, diskSource{name: name, bytes: f.GetSizeBytes(), partial: f.GetPartial()})
	}
	slices.SortStableFunc(out, func(a, b diskSource) int { return cmp.Compare(b.bytes, a.bytes) })
	return out
}
