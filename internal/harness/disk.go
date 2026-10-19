package harness

// The disk guard (W303, decision Q71). On 17/10/2026 the disk filled up over two and a half days, the Go build cache of
// the worktrees at 429 GB, until no worker could write (L23). Before a worker starts and before push checks start,
// Djinn reads the free space of its data folder's file system, and of the Go build cache's when go is on its PATH (go
// env GOCACHE; no go, no cache step, nothing said). Under DiskGuard.Trim free, it deletes the cache entries unused for
// more than goCacheUnused, an action entry (-a) with its output (-d), so that the cache never points to a missing file,
// apart from the work that asked, at most once per diskTrimEvery; never go clean -cache, never /tmp/go-build*. Under
// DiskGuard.Full free, the lead hears "disk almost full", with the free space and the biggest folders Djinn knows, and
// the push checks wait instead of failing until space comes back, which Djinn reads every diskPollEvery: one line as
// it goes under, one as it comes back (LEAD_EVENT_KIND_DISK).

import (
	"cmp"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// GB is a gigabyte, as the disk thresholds count.
const GB = 1_000_000_000

// The disk thresholds djinn up uses unless told otherwise (--disk-trim-gb, --disk-full-gb).
const (
	DefaultDiskTrim = 30 * GB
	DefaultDiskFull = 10 * GB
)

const (
	// goCacheUnused is how long a Go build cache entry may stay unused before a trim deletes it. Go's own trim waits
	// five days, once a day: far too long for many worktrees.
	goCacheUnused = 24 * time.Hour
	// diskTrimEvery is how long Djinn waits after a trim before it trims again: what it left is younger than a day.
	diskTrimEvery = 30 * time.Minute
	// diskPollEvery is how often Djinn reads the free space while the disk is almost full, to see space come back.
	diskPollEvery = 30 * time.Second
	// diskMeasureTimeout bounds the measure of the biggest folders, a walk of each.
	diskMeasureTimeout = 2 * time.Minute
	// diskFoldersEvery is how long Djinn keeps the measured folder sizes for the hourglass panel before walking again.
	diskFoldersEvery = 5 * time.Minute
	// diskFoldersWait is how long DiskFolders waits on a measurement so fast folders come back on the first read.
	diskFoldersWait = 150 * time.Millisecond
)

// errDiskFull ends a push whose checks wait for space: it goes again once space comes back, never held red.
var errDiskFull = errors.New("the disk is almost full: Djinn runs the push checks once space comes back")

// DiskGuard is how Djinn watches the free space of its disks (djinn up gives it).
type DiskGuard struct {
	// Free is the space Djinn may use on the file system that holds dir, in bytes.
	Free func(dir string) (uint64, error)
	// Under Trim bytes free where the Go build cache is, Djinn trims it; 0: never.
	Trim uint64
	// Under Full bytes free where its data folder or the Go build cache is, the lead hears it and the push checks wait;
	// 0: never.
	Full uint64
}

// WithDiskGuard lets Djinn watch its disks. Without it, Djinn reads no free space, trims nothing and holds no check.
func WithDiskGuard(g DiskGuard) Option { return func(h *Harness) { h.disk.guard = g } }

// disk is what Djinn knows of its disks. Its mu is taken alone.
type disk struct {
	guard DiskGuard
	cache goCache
	// What tests set: the Go build cache in place of go env GOCACHE (cacheDir), how often the free space is read
	// while the disk is almost full (poll; 0: diskPollEvery), the folders measured in place of knownFolders'.
	cacheDir func() string
	poll     time.Duration
	folders  func(cache string) []string

	mu         sync.Mutex
	trimming   bool
	trimmed    time.Time // when the last trim started; zero: none yet
	full       bool      // under guard.Full: the push checks wait
	since      time.Time // when it went under
	measuring  bool
	measuredAt time.Time
	measured   []*machinev1.DiskFolder
	wg         sync.WaitGroup
}

// diskSpace is the free space of the folders Djinn watches, read at once.
type diskSpace struct {
	cache     string // the Go build cache; "" for none
	home      uint64 // free where the data folder is
	cacheFree uint64 // free where the cache is; 0 without one
	homeRead  bool
	cacheRead bool
}

// low is the least free space read, and where; ok false when none could be read.
func (s diskSpace) low() (free uint64, where string, ok bool) {
	switch {
	case s.homeRead && (!s.cacheRead || s.home <= s.cacheFree):
		return s.home, "Djinn's data folder", true
	case s.cacheRead:
		return s.cacheFree, "the Go build cache", true
	}
	return 0, "", false
}

// readDisk reads the free space of the data folder's file system, and of the Go build cache's when go is on the PATH.
func (h *Harness) readDisk(ctx context.Context) diskSpace {
	g := h.disk.guard
	var s diskSpace
	if h.home != "" {
		if free, err := g.Free(h.home); err == nil {
			s.home, s.homeRead = free, true
		} else {
			logx.L().Debug("read the data folder's free space", "err", err)
		}
	}
	if h.disk.cacheDir != nil {
		s.cache = h.disk.cacheDir()
	} else {
		s.cache = h.disk.cache.find(ctx, h.now())
	}
	if s.cache != "" {
		if free, err := g.Free(s.cache); err == nil {
			s.cacheFree, s.cacheRead = free, true
		} else {
			logx.L().Debug("read the Go build cache's free space", "err", err)
		}
	}
	return s
}

// diskGuard reads the free space before a worker starts or push checks start: under the trim threshold, it trims the
// Go build cache apart; under the full one, the lead hears it once and the push checks wait. It says whether the disk
// is almost full. Without a guard, it reads nothing.
func (h *Harness) diskGuard(ctx context.Context) bool {
	g := h.disk.guard
	if g.Free == nil {
		return false
	}
	s := h.readDisk(ctx)
	h.diskTrim(s)
	free, where, ok := s.low()
	if !ok || g.Full == 0 || free >= g.Full {
		return false // Back over it: diskFullLoop says so at its next read.
	}
	d := &h.disk
	d.mu.Lock()
	went := !d.full
	if went {
		d.full, d.since = true, h.now()
		d.wg.Add(1)
	}
	d.mu.Unlock()
	if went {
		logx.L().Warn("disk almost full", "free", free, "where", where, "threshold", g.Full)
		go h.diskFullLoop(s, free, where)
	}
	return true
}

// diskTrim trims the Go build cache apart, when s says it is on a file system under the trim threshold and no trim
// ran within diskTrimEvery.
func (h *Harness) diskTrim(s diskSpace) {
	g, d := h.disk.guard, &h.disk
	if s.cache == "" || !s.cacheRead || g.Trim == 0 || s.cacheFree >= g.Trim {
		return
	}
	now := h.now()
	d.mu.Lock()
	if d.trimming || (!d.trimmed.IsZero() && now.Sub(d.trimmed) < diskTrimEvery) {
		d.mu.Unlock()
		return
	}
	d.trimming, d.trimmed = true, now
	d.wg.Add(1)
	d.mu.Unlock()
	go func() {
		defer logx.Crash("go cache trim")
		defer d.wg.Done()
		start := time.Now()
		entries, freed, err := trimGoCache(h.ctx, s.cache, now.Add(-goCacheUnused))
		d.mu.Lock()
		d.trimming = false
		if freed > 0 {
			d.measuredAt = time.Time{}
		}
		d.mu.Unlock()
		args := []any{"cache", s.cache, "free before", s.cacheFree, "entries", entries, "freed", freed,
			"took", time.Since(start).Round(time.Millisecond)}
		if err != nil {
			logx.L().Warn("go build cache trimmed in part", append(args, "err", err)...)
			return
		}
		logx.L().Info("go build cache trimmed", args...)
	}()
}

// diskFullLoop tells the lead the disk is almost full, with the biggest folders Djinn knows, then reads the free space
// every diskPollEvery until it is back over the threshold: the lead hears it, and the push checks it held go.
func (h *Harness) diskFullLoop(s diskSpace, free uint64, where string) {
	defer logx.Crash("disk guard")
	defer h.disk.wg.Done()
	ctx, cancel := context.WithTimeout(h.ctx, diskMeasureTimeout)
	known := h.knownFolders
	if h.disk.folders != nil {
		known = h.disk.folders
	}
	biggest := biggestFolders(ctx, known(s.cache), 3)
	cancel()
	h.tellDisk(fullLine(free, where, h.disk.guard.Full), fullDetail(biggest))
	tick := time.NewTicker(cmp.Or(h.disk.poll, diskPollEvery))
	defer tick.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-tick.C:
		}
		s := h.readDisk(h.ctx)
		h.diskTrim(s)
		free, _, ok := s.low()
		if ok && free < h.disk.guard.Full {
			continue
		}
		d := &h.disk
		d.mu.Lock()
		since := d.since
		d.full = false
		d.mu.Unlock()
		logx.L().Info("disk space back", "free", free, "since", since)
		h.tellDisk(fmt.Sprintf("Djinn: disk space is back (%s free); the push checks it held run again.", gbText(free)), "")
		h.kickIntegrate()
		return
	}
}

// fullLine is the lead's line as the disk goes under the full threshold.
func fullLine(free uint64, where string, full uint64) string {
	return fmt.Sprintf("Djinn: disk almost full, %s free where %s is (under %s); the push checks wait for space.",
		gbText(free), where, gbText(full))
}

// fullDetail is what the lead reads under the line: the biggest folders, and what Djinn does.
func fullDetail(biggest []folderSize) string {
	var b strings.Builder
	if len(biggest) > 0 {
		b.WriteString("Biggest folders Djinn knows: ")
		for i, f := range biggest {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(f.dir + " " + gbText(f.bytes))
		}
		b.WriteString(". ")
	}
	b.WriteString("Djinn trims the Go build cache entries unused for a day, holds the push checks and runs them once " +
		"space comes back. Ask the developer what to free beyond that: never delete what is not Djinn's.")
	return b.String()
}

// tellDisk queues the Djinn's line about the disk, of no wish.
func (h *Harness) tellDisk(line, detail string) {
	e := &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_DISK, Text: line, Detail: detail}
	if err := h.toLeadEvent(h.ctx, e); err != nil && !errors.Is(err, plan.ErrNoLead) && h.ctx.Err() == nil {
		logx.L().Warn("tell the lead of the disk", "err", err)
	}
}

// gbText says bytes in GB, with one decimal under 10.
func gbText(bytes uint64) string {
	gb := float64(bytes) / GB
	if gb < 10 {
		return fmt.Sprintf("%.1f GB", gb)
	}
	return fmt.Sprintf("%.0f GB", gb)
}

// DiskFolders returns the main folders Djinn knows on disk, biggest first, with their size in bytes. It measures them
// on the server at most once per diskFoldersEvery, without blocking the caller when a large cache takes longer.
func (h *Harness) DiskFolders() []*machinev1.DiskFolder {
	d := &h.disk
	now := h.now()
	d.mu.Lock()
	fresh := !d.measuredAt.IsZero() && now.Sub(d.measuredAt) < diskFoldersEvery
	if fresh || d.measuring || h.ctx.Err() != nil {
		out := slices.Clone(d.measured)
		d.mu.Unlock()
		return out
	}
	first := len(d.measured) == 0
	d.measuring = true
	done := make(chan struct{})
	d.wg.Add(1)
	d.mu.Unlock()

	go func() {
		defer logx.Crash("disk folders")
		defer d.wg.Done()
		defer close(done)
		ctx, cancel := context.WithTimeout(h.ctx, diskMeasureTimeout)
		defer cancel()
		var cache string
		if d.cacheDir != nil {
			cache = d.cacheDir()
		} else if d.guard.Free != nil {
			cache = d.cache.find(ctx, h.now())
		}
		dirs := h.diskFolders(ctx, cache)
		var onUpdate func([]folderSize)
		if first {
			onUpdate = func(partial []folderSize) {
				d.mu.Lock()
				d.measured = toDiskFolders(partial)
				d.mu.Unlock()
			}
		}
		biggest := measureFolders(ctx, dirs, onUpdate)
		d.mu.Lock()
		d.measured = toDiskFolders(biggest)
		d.measuring = false
		if h.ctx.Err() == nil {
			d.measuredAt = h.now()
		}
		d.mu.Unlock()
	}()

	select {
	case <-done:
	case <-time.After(diskFoldersWait):
	case <-h.ctx.Done():
	}
	d.mu.Lock()
	out := slices.Clone(d.measured)
	d.mu.Unlock()
	return out
}

func toDiskFolders(sizes []folderSize) []*machinev1.DiskFolder {
	out := make([]*machinev1.DiskFolder, len(sizes))
	for i, s := range sizes {
		out[i] = &machinev1.DiskFolder{Path: s.dir, SizeBytes: s.bytes}
	}
	return out
}

// diskFolders are the main folders the hourglass Disk tab lists: Djinn's data folder, each project's worktrees and
// node_modules, Djinn's logs and tilasms, the Go build cache, the npm and Playwright caches, the temporary folder.
func (h *Harness) diskFolders(ctx context.Context, cache string) []string {
	var raw []string
	if h.home != "" {
		if projects, err := filepath.Glob(filepath.Join(h.home, "projects", "*", "worktrees")); err == nil {
			raw = append(raw, projects...)
		}
		raw = append(raw, filepath.Join(h.home, "logs"), filepath.Join(h.home, "tilasms"), h.home)
	}
	if h.store != nil {
		if projects, err := store.List[*planv1.Project](ctx, h.store, nil); err == nil {
			for _, p := range projects {
				if dir := p.GetDirectory(); dir != "" {
					raw = append(raw, filepath.Join(dir, "node_modules"))
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
	var dirs []string
	for _, dir := range raw {
		if dir == "" {
			continue
		}
		clean := filepath.Clean(dir)
		if !seen[clean] {
			seen[clean] = true
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// knownFolders are the folders that fill a disk Djinn works on: the Go build cache, each project's worktrees, Djinn's
// logs and tilasms, the npm and Playwright caches, the temporary folder.
func (h *Harness) knownFolders(cache string) []string {
	var dirs []string
	if cache != "" {
		dirs = append(dirs, cache)
	}
	if h.home != "" {
		if projects, err := filepath.Glob(filepath.Join(h.home, "projects", "*", "worktrees")); err == nil {
			dirs = append(dirs, projects...)
		}
		dirs = append(dirs, filepath.Join(h.home, "logs"), filepath.Join(h.home, "tilasms"))
	}
	if user, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(user, ".npm"), filepath.Join(user, ".cache", "ms-playwright"))
	}
	return append(dirs, os.TempDir())
}

// folderSize is the size of a folder, what its files hold.
type folderSize struct {
	dir   string
	bytes uint64
}

// biggestFolders measures dirs and returns the n biggest, biggest first; a folder missing, empty or unread is left
// out. A folder inside another one measured is measured on its own too.
func biggestFolders(ctx context.Context, dirs []string, n int) []folderSize {
	sizes := measureFolders(ctx, dirs, nil)
	if n > 0 && n < len(sizes) {
		return sizes[:n]
	}
	return sizes
}

func isSubfolder(child, parent string) bool {
	c, p := filepath.Clean(child), filepath.Clean(parent)
	return len(c) > len(p) && strings.HasPrefix(c, p+string(filepath.Separator))
}

func measureFolders(ctx context.Context, dirs []string, onUpdate func([]folderSize)) []folderSize {
	walk := slices.Clone(dirs)
	for i := 0; i < len(walk); i++ {
		for j := i + 1; j < len(walk); j++ {
			if isSubfolder(walk[j], walk[i]) {
				sub := walk[j]
				copy(walk[i+1:j+1], walk[i:j])
				walk[i] = sub
			}
		}
	}
	known := make(map[string]uint64, len(walk))
	for _, dir := range walk {
		cleanRoot := filepath.Clean(dir)
		var total uint64
		_ = filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return nil //nolint:nilerr // A folder it cannot read is left out of its size.
			}
			if e.IsDir() {
				if clean := filepath.Clean(path); clean != cleanRoot {
					if sub, ok := known[clean]; ok {
						total += sub
						return filepath.SkipDir
					}
				}
				return nil
			}
			if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
				total += uint64(info.Size())
			}
			return nil
		})
		known[cleanRoot] = total
		if total > 0 && onUpdate != nil {
			onUpdate(sortedFolderSizes(dirs, known))
		}
	}
	return sortedFolderSizes(dirs, known)
}

func sortedFolderSizes(dirs []string, known map[string]uint64) []folderSize {
	var sizes []folderSize
	for _, dir := range dirs {
		if total := known[filepath.Clean(dir)]; total > 0 {
			sizes = append(sizes, folderSize{dir: dir, bytes: total})
		}
	}
	slices.SortStableFunc(sizes, func(a, b folderSize) int { return cmp.Compare(b.bytes, a.bytes) })
	return sizes
}

// trimGoCache deletes the entries of the Go build cache at dir unused since cutoff, and says how many files it
// deleted and what they held. Go marks an entry used by its file's time, both its action entry (<action id>-a, which
// names its output) and its output (<output id>-d). An action entry unused is deleted first; then an output unused that
// no action entry kept names: the cache never points to a missing output, and a build that runs meanwhile finds an
// entry whole or none (a miss it compiles again). Other files (Go's trim.txt, README) stay.
func trimGoCache(ctx context.Context, dir string, cutoff time.Time) (deleted int, freed uint64, err error) {
	type output struct {
		id   [32]byte
		size uint64
	}
	kept := map[[32]byte]bool{} // the outputs a kept action entry names
	var oldActions []string
	var oldOutputs []output
	subdirs, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, err
	}
	for _, sub := range subdirs {
		if !sub.IsDir() || len(sub.Name()) != 2 {
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, sub.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if ctx.Err() != nil {
				return deleted, freed, ctx.Err()
			}
			id, kind, ok := cacheEntryName(f.Name())
			if !ok {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			path := filepath.Join(dir, sub.Name(), f.Name())
			used := !info.ModTime().Before(cutoff)
			switch {
			case kind == 'a' && used:
				if out, ok := actionOutput(path); ok {
					kept[out] = true
				}
			case kind == 'a':
				oldActions = append(oldActions, path)
			case !used:
				oldOutputs = append(oldOutputs, output{id: id, size: uint64(info.Size())})
			}
		}
	}
	for _, path := range oldActions {
		if size, ok := removeUnused(path, cutoff); ok {
			deleted++
			freed += size
		}
	}
	for _, o := range oldOutputs {
		if kept[o.id] {
			continue
		}
		name := hex.EncodeToString(o.id[:]) + "-d"
		if size, ok := removeUnused(filepath.Join(dir, name[:2], name), cutoff); ok {
			deleted++
			freed += size
		}
	}
	return deleted, freed, ctx.Err()
}

// cacheEntryName reads a Go build cache file's name: a SHA-256 in hex, then -a (an action entry) or -d (an output).
func cacheEntryName(name string) (id [32]byte, kind byte, ok bool) {
	if len(name) != 66 || name[64] != '-' || (name[65] != 'a' && name[65] != 'd') {
		return id, 0, false
	}
	if _, err := hex.Decode(id[:], []byte(name[:64])); err != nil {
		return id, 0, false
	}
	return id, name[65], true
}

// actionOutput reads the output an action entry names: "v1 <action id> <output id> <size> <time>".
func actionOutput(path string) (out [32]byte, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return out, false
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 || len(fields[2]) != 64 {
		return out, false
	}
	if _, err := hex.Decode(out[:], []byte(fields[2])); err != nil {
		return out, false
	}
	return out, true
}

// removeUnused deletes the file at path unless a build used it since cutoff, and says what it held.
func removeUnused(path string, cutoff time.Time) (uint64, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().Before(cutoff) {
		return 0, false
	}
	if err := os.Remove(path); err != nil {
		return 0, false
	}
	return uint64(info.Size()), true
}
