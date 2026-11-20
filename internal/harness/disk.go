package harness

// The disk guard (W303, decision Q71). On 17/10/2026 the disk filled up over two and a half days, the Go build cache of
// the worktrees at 429 GB, until no worker could write (L23). Before a worker starts and before push checks start,
// Djinn reads the free space of its data folder's file system, and of the Go build cache's when go is on its PATH (go
// env GOCACHE; no go, no cache step, nothing said). Under DiskGuard.Trim free, it deletes the cache entries unused for
// more than goCacheUnused, an action entry (-a) with its output (-d), so that the cache never points to a missing file,
// apart from the work that asked, at most once per diskTrimEvery; never go clean -cache, never /tmp/go-build*. Under
// DiskGuard.Full free, the lead hears "disk almost full", with the free space and the biggest sources Djinn knows, and
// the push checks wait instead of failing until space comes back, which Djinn reads every diskPollEvery: one line as
// it goes under, one as it comes back (LEAD_EVENT_KIND_DISK).

import (
	"cmp"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
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
	// goBuildReclaimableAge is the age above which a Go build cache file counts as reclaimable in the hourglass Disk
	// tab (`find ~/.cache/go-build -type f -mmin +360 -delete`).
	goBuildReclaimableAge = 6 * time.Hour
	// dockerSystemDFTimeout bounds the `docker system df` command when measuring Docker disk usage.
	dockerSystemDFTimeout = 10 * time.Second
	// diskTrimEvery is how long Djinn waits after a trim before it trims again: what it left is younger than a day.
	diskTrimEvery = 30 * time.Minute
	// diskPollEvery is how often Djinn reads the free space while the disk is almost full, to see space come back.
	diskPollEvery = 30 * time.Second
	// diskMeasureTimeout is how long the disk guard waits on a first measure of the folders and caches before it tells
	// the lead what it has.
	diskMeasureTimeout = 2 * time.Minute
	// diskFoldersEvery is how long Djinn keeps the measured folder sizes for the hourglass panel before walking again,
	// at least: a measure that took long holds longer (passEvery).
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
	// while the disk is almost full (poll; 0: diskPollEvery), the folders measured in place of knownFolders', how long
	// a read waits on a measurement (wait; 0: diskFoldersWait), the caches measured in place of the machine's (measure),
	// the home and system folders of the slow pass in place of the machine's (restRoots), the size under which a home
	// entry joins the "~" row (restMin; 0: diskRestMin).
	cacheDir  func() string
	poll      time.Duration
	folders   func(cache string) []diskDir
	wait      time.Duration
	measure   func(ctx context.Context, now time.Time, cache string) []*machinev1.DiskItem
	restRoots func() (home string, system []string)
	restMin   uint64

	mu              sync.Mutex
	trimming        bool
	trimmed         time.Time // when the last trim started; zero: none yet
	full            bool      // under guard.Full: the push checks wait
	since           time.Time // when it went under
	measuring       bool
	measuredAt      time.Time
	foldersEvery    time.Duration // how long the last folder measure holds; 0: diskFoldersEvery
	measured        []*machinev1.DiskFolder
	itemsMeasuring  bool
	itemsMeasuredAt time.Time
	itemsEvery      time.Duration // how long the last item measure holds; 0: diskFoldersEvery
	items           []*machinev1.DiskItem
	itemDirs        map[string]uint64 // the folders the items walked, by clean path: the slow pass leaves them out
	restLoaded      bool              // diskRestFile read
	restRunning     bool
	restAt          time.Time // when the slow pass last ended; zero: never
	rest            []*machinev1.DiskFolder
	wg              sync.WaitGroup
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
			d.itemsMeasuredAt = time.Time{}
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

// diskFullLoop tells the lead the disk is almost full, with the biggest sources Djinn knows, then reads the free space
// every diskPollEvery until it is back over the threshold: the lead hears it, and the push checks it held go.
func (h *Harness) diskFullLoop(s diskSpace, free uint64, where string) {
	defer logx.Crash("disk guard")
	defer h.disk.wg.Done()
	h.tellDisk(fullLine(free, where, h.disk.guard.Full), fullDetail(h.diskSources(diskMeasureTimeout), 5))
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

// fullDetail is what the lead reads under the line: the n biggest sources on the disk Djinn knows, and what Djinn does.
func fullDetail(sources []diskSource, n int) string {
	var b strings.Builder
	if len(sources) > 0 {
		b.WriteString("Biggest on the disk: ")
		for i, s := range sources[:min(n, len(sources))] {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(s.name + " " + gbText(s.bytes))
			if s.integrated > 0 {
				b.WriteString(", " + gbText(s.integrated) + " of it freed 30 days after its tasks' end")
			}
			if s.partial {
				b.WriteString(" at least")
			}
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

func measureDockerItems(ctx context.Context) []*machinev1.DiskItem {
	path, err := exec.LookPath("docker")
	if err != nil {
		return nil
	}
	dctx, cancel := context.WithTimeout(ctx, dockerSystemDFTimeout)
	defer cancel()
	out, err := exec.CommandContext(dctx, path, "system", "df").Output()
	if err != nil {
		return nil
	}
	return parseDockerSystemDF(string(out))
}

func parseDockerSystemDF(out string) []*machinev1.DiskItem {
	type dfRow struct {
		Type        string `json:"Type"`
		Size        string `json:"Size"`
		Reclaimable string `json:"Reclaimable"`
	}
	var items []*machinev1.DiskItem
	for _, rawLine := range strings.Split(out, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "TYPE") {
			continue
		}
		var typ, sizeStr, reclaimStr string
		if strings.HasPrefix(line, "{") {
			var r dfRow
			if json.Unmarshal([]byte(line), &r) == nil {
				typ = r.Type
				sizeStr = r.Size
				if fields := strings.Fields(r.Reclaimable); len(fields) > 0 {
					reclaimStr = fields[0]
				}
			}
		} else {
			for _, prefix := range []string{"Build Cache", "Local Volumes", "Images"} {
				if rest, ok := strings.CutPrefix(line, prefix); ok {
					fields := strings.Fields(rest)
					if len(fields) >= 4 {
						typ = prefix
						sizeStr = fields[2]
						reclaimStr = fields[3]
					}
					break
				}
			}
		}
		sizeBytes := parseDockerBytes(sizeStr)
		if sizeBytes == 0 {
			continue
		}
		reclaimBytes := min(parseDockerBytes(reclaimStr), sizeBytes)
		switch typ {
		case "Build Cache":
			items = append(items, &machinev1.DiskItem{
				Kind:             "docker-builder",
				Path:             "docker builder",
				SizeBytes:        sizeBytes,
				ReclaimableBytes: reclaimBytes,
				Risk:             machinev1.DiskRisk_DISK_RISK_REBUILD,
				Command:          "docker builder prune -f",
			})
		case "Images":
			items = append(items, &machinev1.DiskItem{
				Kind:             "docker-images",
				Path:             "docker images",
				SizeBytes:        sizeBytes,
				ReclaimableBytes: reclaimBytes,
				Risk:             machinev1.DiskRisk_DISK_RISK_REBUILD,
				Command:          "docker image prune -a -f",
			})
		case "Local Volumes":
			items = append(items, &machinev1.DiskItem{
				Kind:             "docker-volumes",
				Path:             "docker volume",
				SizeBytes:        sizeBytes,
				ReclaimableBytes: reclaimBytes,
				Risk:             machinev1.DiskRisk_DISK_RISK_LOSS,
				Command:          "docker volume prune -f",
			})
		}
	}
	return items
}

func parseDockerBytes(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	i := 0
	for i < len(s) && ((s[i] >= '0' && s[i] <= '9') || s[i] == '.') {
		i++
	}
	if i == 0 {
		return 0
	}
	val, err := strconv.ParseFloat(s[:i], 64)
	if err != nil || val <= 0 {
		return 0
	}
	var mult float64
	switch strings.ToLower(strings.TrimSpace(s[i:])) {
	case "", "b":
		mult = 1
	case "kb":
		mult = 1e3
	case "kib":
		mult = 1024
	case "mb":
		mult = 1e6
	case "mib":
		mult = 1024 * 1024
	case "gb":
		mult = 1e9
	case "gib":
		mult = 1024 * 1024 * 1024
	case "tb":
		mult = 1e12
	case "tib":
		mult = 1024 * 1024 * 1024 * 1024
	default:
		return 0
	}
	return uint64(val * mult)
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
