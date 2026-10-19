package harness

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// fakeDisk is a file system's free space in a test, whatever the folder.
type fakeDisk struct {
	mu   sync.Mutex
	free uint64
}

func (d *fakeDisk) read(string) (uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.free, nil
}

func (d *fakeDisk) set(free uint64) {
	d.mu.Lock()
	d.free = free
	d.mu.Unlock()
}

// withDiskTest gives the disk guard cache as the Go build cache, never the machine's, reads the free space every poll
// while the disk is almost full, and measures the cache and the data folder's alone, never the machine's.
func withDiskTest(cache string, poll time.Duration) Option {
	return func(h *Harness) {
		h.disk.cacheDir = func() string { return cache }
		h.disk.poll = poll
		h.disk.folders = func(cache string) []string {
			return slices.DeleteFunc(h.knownFolders(cache), func(dir string) bool {
				return dir != cache && !strings.HasPrefix(dir, h.home)
			})
		}
	}
}

// diskLines are the texts of the lead's disk events, with their detail.
func diskLines(q *leadQueue) []string {
	var out []string
	for _, ev := range q.all() {
		if ev.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_DISK {
			out = append(out, strings.TrimSpace(ev.GetText()+" "+ev.GetDetail()))
		}
	}
	return out
}

// TestTrimmedGoFlags (W303): the processes Djinn starts get -trimpath, after the GOFLAGS already set, once.
func TestTrimmedGoFlags(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		env  []string
		want string // the last GOFLAGS entry
	}{
		{[]string{"HOME=/h"}, "GOFLAGS=-trimpath"},
		{[]string{"GOFLAGS=-mod=mod", "HOME=/h"}, "GOFLAGS=-mod=mod -trimpath"},
		{[]string{"GOFLAGS=", "HOME=/h"}, "GOFLAGS=-trimpath"},
		{[]string{"GOFLAGS=-trimpath -mod=mod"}, "GOFLAGS=-trimpath -mod=mod"},
		{[]string{"GOFLAGS=-trimpath=false"}, "GOFLAGS=-trimpath=false"},
		{[]string{"GOFLAGS=--trimpath"}, "GOFLAGS=--trimpath"},
		{[]string{"GOFLAGS=-race", "GOFLAGS=-v"}, "GOFLAGS=-v -trimpath"}, // the last one is the one a child reads
	} {
		env := slices.Clone(tt.env)
		got := trimmedGoFlags(env)
		var last string
		count := 0
		for _, e := range got {
			if strings.HasPrefix(e, "GOFLAGS=") {
				last = e
				count++
			}
		}
		if last != tt.want || count != max(1, strings.Count(strings.Join(tt.env, "\n"), "GOFLAGS=")) {
			t.Errorf("trimmedGoFlags(%q) = %q; want last GOFLAGS %q, no entry added beside it", tt.env, got, tt.want)
		}
		if !slices.Equal(env, tt.env) {
			t.Errorf("trimmedGoFlags(%q) changed its argument to %q", tt.env, env)
		}
	}
}

// goCacheFixture is a Go build cache with an entry of each kind, its files dated as named: an action entry (-a) names
// its output (-d).
type goCacheFixture struct {
	dir   string
	files map[string]string // by role: its path
}

func newGoCacheFixture(t *testing.T, now time.Time) *goCacheFixture {
	t.Helper()
	f := &goCacheFixture{dir: t.TempDir(), files: map[string]string{}}
	old, fresh := now.Add(-3*24*time.Hour), now.Add(-time.Hour)
	id := func(c byte) string { return strings.Repeat(string(c), 64) }
	write := func(role, name, content string, at time.Time) {
		path := filepath.Join(f.dir, name[:2], name)
		writeFile(t, f.dir, filepath.Join(name[:2], name), content)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
		f.files[role] = path
	}
	action := func(role, a, out string, at time.Time) {
		write(role, a+"-a", "v1 "+a+" "+out+"                  1234 1760000000000000000\n", at)
	}
	// Unused both: deleted together.
	action("old action", id('1'), id('a'), old)
	write("its old output", id('a')+"-d", "output a", old)
	// Used lately, its output not opened since: both kept, the cache never points to a missing output.
	action("fresh action", id('2'), id('b'), fresh)
	write("old output a fresh action names", id('b')+"-d", "output b", old)
	// An unused action whose output another build used lately: the action goes, the output stays.
	action("old action of a fresh output", id('3'), id('c'), old)
	write("fresh output", id('c')+"-d", "output c", fresh)
	// An output no action names, unused: deleted.
	write("orphan output", id('d')+"-d", "output d", old)
	// What is not an entry stays, however old.
	write("trim.txt", "trim.txt", "1760000000", old)
	write("not hex", strings.Repeat("z", 64)+"-d", "zzz", old)
	return f
}

// TestTrimGoCache (W303): a trim deletes the entries unused for a day, an action entry with its output, and never an
// output a kept action entry names, nor a file that is not an entry.
func TestTrimGoCache(t *testing.T) {
	t.Parallel()
	now := time.Now()
	f := newGoCacheFixture(t, now)
	deleted, freed, err := trimGoCache(t.Context(), f.dir, now.Add(-goCacheUnused))
	if err != nil {
		t.Fatal(err)
	}
	gone := []string{"old action", "its old output", "old action of a fresh output", "orphan output"}
	var want uint64
	for role, path := range f.files {
		_, err := os.Stat(path)
		if slices.Contains(gone, role) {
			if err == nil {
				t.Errorf("%s kept", role)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s deleted: %v", role, err)
		}
	}
	for _, role := range gone {
		if strings.HasSuffix(role, "action") || strings.Contains(role, "action of") {
			want += uint64(len("v1 " + strings.Repeat("1", 64) + " " + strings.Repeat("a", 64) + "                  1234 1760000000000000000\n"))
		} else {
			want += uint64(len("output a"))
		}
	}
	if deleted != len(gone) || freed != want {
		t.Errorf("trim deleted %d files, %d bytes; want %d, %d", deleted, freed, len(gone), want)
	}
}

// TestDiskGuardNoGo (W303): with no go on the PATH, the guard reads the data folder alone: no cache, no error.
func TestDiskGuardNoGo(t *testing.T) {
	disk := &fakeDisk{free: 20 * GB}
	e := up(t, t.TempDir(), WithDiskGuard(DiskGuard{Free: disk.read, Trim: DefaultDiskTrim, Full: DefaultDiskFull}))
	t.Setenv("PATH", t.TempDir())
	if e.h.diskGuard(t.Context()) {
		t.Error("20 GB free: the disk is almost full")
	}
	s := e.h.readDisk(t.Context())
	if s.cache != "" || s.cacheRead || !s.homeRead || s.home != 20*GB {
		t.Errorf("read %+v; want the data folder's 20 GB, no Go build cache", s)
	}
	if got := e.h.disk.cache.find(t.Context(), time.Now()); got != "" {
		t.Errorf("GOCACHE %q with no go on the PATH", got)
	}
}

// TestGoCacheFound: the Go build cache is what the go on the PATH says, asked once per goCacheEvery.
func TestGoCacheFound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a fake go is a shell script")
	}
	bin, cache, count := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "count")
	writeFile(t, bin, "go", "#!/bin/sh\necho x >> '"+count+"'\n[ \"$1 $2\" = 'env GOCACHE' ] && echo '"+cache+"'\n")
	if err := os.Chmod(filepath.Join(bin, "go"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	var c goCache
	now := time.Now()
	for _, at := range []time.Time{now, now.Add(time.Minute)} {
		if got := c.find(t.Context(), at); got != cache {
			t.Fatalf("GOCACHE %q; want %q", got, cache)
		}
	}
	if data, _ := os.ReadFile(count); strings.Count(string(data), "x") != 1 {
		t.Errorf("go ran %d times within goCacheEvery; want once", strings.Count(string(data), "x"))
	}
	c.find(t.Context(), now.Add(goCacheEvery))
	if data, _ := os.ReadFile(count); strings.Count(string(data), "x") != 2 {
		t.Errorf("go ran %d times; want it asked again after goCacheEvery", strings.Count(string(data), "x"))
	}
}

// TestDiskGuard (W303): under the trim threshold, the Go build cache loses its entries unused for a day, once per
// diskTrimEvery; under the full one, the lead hears it once, with the biggest folders, and once more as space comes
// back.
func TestDiskGuard(t *testing.T) {
	t.Parallel()
	start := time.Now()
	clock := &fakeClock{at: start}
	f := newGoCacheFixture(t, start)
	disk := &fakeDisk{free: 50 * GB}
	e := up(t, t.TempDir(), WithClock(clock.now), withDiskTest(f.dir, 10*time.Millisecond),
		WithDiskGuard(DiskGuard{Free: disk.read, Trim: DefaultDiskTrim, Full: DefaultDiskFull}))
	queue := &leadQueue{}
	e.h.NotifyLead(queue.notify)
	gone := func(role string) bool {
		_, err := os.Stat(f.files[role])
		return os.IsNotExist(err)
	}

	if e.h.diskGuard(t.Context()) {
		t.Error("50 GB free: the disk is almost full")
	}
	e.h.disk.wg.Wait()
	if gone("old action") {
		t.Error("50 GB free: the cache trimmed")
	}

	disk.set(20 * GB)
	if e.h.diskGuard(t.Context()) {
		t.Error("20 GB free: the disk is almost full")
	}
	e.h.disk.wg.Wait()
	if !gone("old action") || !gone("its old output") || gone("fresh action") || gone("old output a fresh action names") {
		t.Fatal("20 GB free: the cache's unused entries not trimmed, or a used one trimmed")
	}
	if lines := diskLines(queue); len(lines) != 0 {
		t.Errorf("the lead heard %q at 20 GB free", lines)
	}
	// Within diskTrimEvery, no trim again.
	writeFile(t, f.dir, filepath.Join("ee", strings.Repeat("e", 64)+"-d"), "output e")
	late := filepath.Join(f.dir, "ee", strings.Repeat("e", 64)+"-d")
	if err := os.Chtimes(late, start.Add(-48*time.Hour), start.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	clock.set(start.Add(10 * time.Minute))
	e.h.diskGuard(t.Context())
	e.h.disk.wg.Wait()
	if _, err := os.Stat(late); err != nil {
		t.Error("a trim within diskTrimEvery of the last one")
	}

	disk.set(5 * GB)
	if !e.h.diskGuard(t.Context()) {
		t.Fatal("5 GB free: the disk is not almost full")
	}
	e.h.diskGuard(t.Context()) // Said once.
	eventually(t, func() bool { return len(diskLines(queue)) > 0 }, func() string { return "no disk event" })
	lines := diskLines(queue)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "Djinn: disk almost full, 5.0 GB free where Djinn's data folder is (under 10 GB)") ||
		!strings.Contains(lines[0], "Biggest folders Djinn knows: "+f.dir+" ") {
		t.Errorf("the lead heard %q; want one line, with the free space and the Go build cache among the biggest folders", lines)
	}

	disk.set(40 * GB)
	eventually(t, func() bool { return len(diskLines(queue)) == 2 }, func() string { return strings.Join(diskLines(queue), "\n") })
	if got := diskLines(queue)[1]; got != "Djinn: disk space is back (40 GB free); the push checks it held run again." {
		t.Errorf("the lead heard %q as space came back", got)
	}
	if e.h.diskGuard(t.Context()) {
		t.Error("40 GB free: the disk is almost full")
	}
}

// TestDiskFullPushChecksWait (W303): with the disk almost full, the push checks wait instead of running and failing:
// nothing held, no correction; once space comes back, they run and the branch is pushed.
func TestDiskFullPushChecksWait(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	disk := &fakeDisk{free: 5 * GB}
	in := integrating(t, withDiskTest("", 10*time.Millisecond),
		WithDiskGuard(DiskGuard{Free: disk.read, Trim: DefaultDiskTrim, Full: DefaultDiskFull}))
	queue := &leadQueue{}
	in.h.NotifyLead(queue.notify)
	in.checking(t, testAtPush, nil)
	in.correctWith(func(dir, p string) string { return "wait" })
	bare, _ := in.pushDueOnW3(t)
	old := in.remoteTip(t, bare)
	in.pass(t, 0)
	if runs, _ := in.ran(); slices.Contains(runs, "test") {
		t.Errorf("runs %q with the disk almost full; want no push checks", runs)
	}
	if state := in.pushState(t); state.GetHeld() != "" || len(in.corrections(t)) != 0 || in.remoteTip(t, bare) != old {
		t.Fatalf("push checks waiting for space: %v, %d corrections; want nothing held, no correction, nothing pushed",
			state, len(in.corrections(t)))
	}

	disk.set(50 * GB)
	eventually(t, func() bool { return len(diskLines(queue)) == 2 }, func() string { return strings.Join(diskLines(queue), "\n") })
	in.pass(t, 0)
	if runs, _ := in.ran(); !slices.Contains(runs, "test") {
		t.Errorf("runs %q once space came back; want the push checks", runs)
	}
	if got := in.remoteTip(t, bare); got == old || got != in.tip(t) {
		t.Errorf("origin at %s, the branch at %s; want it pushed", got, in.tip(t))
	}
}

// TestDiskFilledDuringPushChecks (W303): push checks that ran out of space as the disk filled up hold nothing and start
// no correction: they run again once space comes back.
func TestDiskFilledDuringPushChecks(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	disk := &fakeDisk{free: 50 * GB}
	in := integrating(t, withDiskTest("", 10*time.Millisecond),
		WithDiskGuard(DiskGuard{Free: disk.read, Trim: DefaultDiskTrim, Full: DefaultDiskFull}))
	in.checking(t, testAtPush, nil)
	in.correctWith(func(dir, p string) string { return "wait" })
	bare, w3 := in.pushDueOnW3(t)
	in.testCode, in.testOut = 1, "go: writing stat cache: write /home/u/.cache/go-build/ab/x-d: no space left on device"
	in.afterTest = func() { disk.set(2 * GB) }
	in.pass(t, 0)
	if state := in.pushState(t); state.GetHeld() != "" || len(in.corrections(t)) != 0 {
		t.Fatalf("push checks out of space: %v, %d corrections; want nothing held, no correction", state, len(in.corrections(t)))
	}
	_, texts := in.integration(t, w3)
	if !slices.ContainsFunc(texts, func(s string) bool { return strings.Contains(s, "ran out of disk space") }) {
		t.Errorf("W3's events %q; want the push checks out of space said", texts)
	}
	in.testCode, in.testOut = 0, "ok"
	disk.set(50 * GB)
	eventually(t, func() bool { return !in.h.diskGuard(t.Context()) }, func() string { return "the disk still full" })
	in.pass(t, 0)
	if got := in.remoteTip(t, bare); got != in.tip(t) {
		t.Errorf("origin at %s, the branch at %s; want it pushed once space came back", got, in.tip(t))
	}
}

// TestDiskFolders (W435): the Disk tab's folder list measures Djinn's data folder, each project's worktrees and
// node_modules, the Go build cache and logs, biggest first, and caches the measurement across calls.
func TestDiskFolders(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	clock := &fakeClock{at: start}
	cache, projDir := t.TempDir(), folder(t)
	e := up(t, t.TempDir(), WithClock(clock.now), withDiskTest(cache, 0))
	_, projectID := e.wish(t, projDir)

	worktrees := filepath.Join(e.h.home, "projects", projectID, "worktrees")
	logs := filepath.Join(e.h.home, "logs")
	nodeModules := filepath.Join(projDir, "node_modules")

	writeFile(t, cache, "ab/entry", strings.Repeat("c", 500_000))
	writeFile(t, nodeModules, "pkg/index.js", strings.Repeat("n", 300_000))
	writeFile(t, worktrees, "w1/file.go", strings.Repeat("w", 80_000))
	writeFile(t, logs, "djinn.log", strings.Repeat("l", 10_000))

	got := e.h.DiskFolders()
	if len(got) < 5 {
		t.Fatalf("DiskFolders() = %v; want at least 5 folders (cache, node_modules, home, worktrees, logs)", got)
	}
	for i := 1; i < len(got); i++ {
		if got[i].GetSizeBytes() > got[i-1].GetSizeBytes() {
			t.Errorf("DiskFolders() not sorted biggest first at %d: %v > %v", i, got[i], got[i-1])
		}
	}
	sizes := map[string]uint64{}
	for _, f := range got {
		sizes[f.GetPath()] = f.GetSizeBytes()
	}
	if sizes[cache] != 500_000 || sizes[nodeModules] != 300_000 || sizes[worktrees] != 80_000 || sizes[logs] != 10_000 {
		t.Errorf("sizes = %v; want cache=500000, node_modules=300000, worktrees=80000, logs=10000", sizes)
	}
	if sizes[e.h.home] <= sizes[worktrees]+sizes[logs] {
		t.Errorf("home size = %d; want > worktrees+logs (%d)", sizes[e.h.home], sizes[worktrees]+sizes[logs])
	}

	// Within diskFoldersEvery, the cached measurement is returned without re-walking.
	writeFile(t, logs, "more.log", strings.Repeat("m", 20_000))
	clock.set(start.Add(time.Minute))
	cached := e.h.DiskFolders()
	for _, f := range cached {
		if f.GetPath() == logs && f.GetSizeBytes() != 10_000 {
			t.Errorf("within diskFoldersEvery, logs size = %d; want cached 10000", f.GetSizeBytes())
		}
	}

	// Past diskFoldersEvery, a new measurement runs.
	clock.set(start.Add(diskFoldersEvery + time.Second))
	fresh := e.h.DiskFolders()
	var freshLogs uint64
	for _, f := range fresh {
		if f.GetPath() == logs {
			freshLogs = f.GetSizeBytes()
		}
	}
	if freshLogs != 30_000 {
		t.Errorf("after diskFoldersEvery, logs size = %d; want 30000", freshLogs)
	}
}

// TestDiskItems (T65): DiskItems measures known machine and Djinn caches (go-build with 6h reclaimable cutoff,
// isolated *-go-build caches marked djinn=true, go/pkg/mod, Docker via `docker system df`, JetBrains, pnpm, pip, uv,
// npm), biggest first, and omits Docker rows when docker is absent.
func TestDiskItems(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a fake docker is a shell script")
	}
	start := time.Date(2026, 10, 19, 15, 35, 0, 0, time.UTC)
	clock := &fakeClock{at: start}
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	goBuild := filepath.Join(fakeHome, ".cache", "go-build")
	writeFile(t, goBuild, "01/old-entry", strings.Repeat("o", 600_000))
	writeFile(t, goBuild, "02/fresh-entry", strings.Repeat("f", 200_000))
	oldTime := start.Add(-7 * time.Hour)
	freshTime := start.Add(-1 * time.Hour)
	if err := os.Chtimes(filepath.Join(goBuild, "01", "old-entry"), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(goBuild, "02", "fresh-entry"), freshTime, freshTime); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(fakeHome, ".cache", "w12-go-build"), "aa/entry", strings.Repeat("a", 300_000))
	writeFile(t, filepath.Join(fakeHome, ".cache", "et4224-go-build"), "bb/entry", strings.Repeat("b", 200_000))
	writeFile(t, filepath.Join(fakeHome, "go", "pkg", "mod"), "cache/download/mod.zip", strings.Repeat("m", 400_000))
	writeFile(t, filepath.Join(fakeHome, ".cache", "JetBrains"), "GoLand/index.dat", strings.Repeat("j", 90_000))
	writeFile(t, filepath.Join(fakeHome, ".local", "share", "pnpm"), "store/v3/pkg", strings.Repeat("p", 50_000))
	writeFile(t, filepath.Join(fakeHome, ".cache", "pnpm"), "dlx/pkg", strings.Repeat("q", 30_000))
	writeFile(t, filepath.Join(fakeHome, ".cache", "pip"), "http/wheel", strings.Repeat("w", 40_000))
	writeFile(t, filepath.Join(fakeHome, ".cache", "uv"), "archive/whl", strings.Repeat("u", 30_000))
	writeFile(t, filepath.Join(fakeHome, ".npm"), "_cacache/tar", strings.Repeat("n", 20_000))

	bin := t.TempDir()
	dockerOut := strings.Join([]string{
		"TYPE            TOTAL     ACTIVE    SIZE      RECLAIMABLE",
		"Images          13        6         15.6GB    6.8GB (43%)",
		"Containers      14        0         1.1GB     1.1GB (100%)",
		"Local Volumes   20        11        3.2GB     1.1GB (34%)",
		"Build Cache     175       0         19GB      12.9GB",
	}, "\n")
	writeFile(t, bin, "docker", "#!/bin/sh\n[ \"$1 $2\" = 'system df' ] && printf '%s\\n' '"+dockerOut+"'\n")
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	e := up(t, t.TempDir(), WithClock(clock.now))
	got := e.h.DiskItems()
	if len(got) != 11 {
		t.Fatalf("DiskItems() count = %d (%v); want 11", len(got), got)
	}
	for i := 1; i < len(got); i++ {
		if got[i].GetSizeBytes() > got[i-1].GetSizeBytes() {
			t.Errorf("DiskItems() not sorted biggest first at %d: %v > %v", i, got[i], got[i-1])
		}
	}

	byKind := map[string]*machinev1.DiskItem{}
	for _, item := range got {
		byKind[item.GetKind()] = item
	}

	type wantItem struct {
		size        uint64
		reclaimable uint64
		unknown     bool
		risk        machinev1.DiskRisk
		command     string
		count       int32
		djinn       bool
	}
	wants := map[string]wantItem{
		"docker-builder": {19_000_000_000, 12_900_000_000, false, machinev1.DiskRisk_DISK_RISK_REBUILD, "docker builder prune -f", 0, false},
		"docker-images":  {15_600_000_000, 6_800_000_000, false, machinev1.DiskRisk_DISK_RISK_REBUILD, "docker image prune -a -f", 0, false},
		"docker-volumes": {3_200_000_000, 1_100_000_000, false, machinev1.DiskRisk_DISK_RISK_LOSS, "docker volume prune -f", 0, false},
		"go-build":       {800_000, 600_000, false, machinev1.DiskRisk_DISK_RISK_REBUILD, "find ~/.cache/go-build -type f -mmin +360 -delete", 0, false},
		"go-isolated":    {500_000, 500_000, false, machinev1.DiskRisk_DISK_RISK_REBUILD, "rm -rf ~/.cache/*-go-build", 2, true},
		"go-mod":         {400_000, 400_000, false, machinev1.DiskRisk_DISK_RISK_REBUILD, "go clean -modcache", 0, false},
		"jetbrains":      {90_000, 90_000, false, machinev1.DiskRisk_DISK_RISK_LOSS, "rm -rf ~/.cache/JetBrains", 0, false},
		"pnpm":           {80_000, 0, true, machinev1.DiskRisk_DISK_RISK_SAFE, "pnpm store prune", 0, false},
		"pip":            {40_000, 40_000, false, machinev1.DiskRisk_DISK_RISK_REBUILD, "pip cache purge", 0, false},
		"uv":             {30_000, 0, true, machinev1.DiskRisk_DISK_RISK_SAFE, "uv cache prune", 0, false},
		"npm":            {20_000, 20_000, false, machinev1.DiskRisk_DISK_RISK_REBUILD, "npm cache clean --force", 0, false},
	}
	for kind, w := range wants {
		it := byKind[kind]
		if it == nil {
			t.Errorf("missing DiskItem kind %q", kind)
			continue
		}
		if it.GetSizeBytes() != w.size || it.GetReclaimableBytes() != w.reclaimable ||
			it.GetReclaimableUnknown() != w.unknown || it.GetRisk() != w.risk ||
			it.GetCommand() != w.command || it.GetCount() != w.count || it.GetDjinn() != w.djinn {
			t.Errorf("DiskItem[%q] = %+v; want %+v", kind, it, w)
		}
	}

	// Without docker on PATH, no docker-* item is reported.
	t.Setenv("PATH", t.TempDir())
	clock.set(start.Add(diskFoldersEvery + time.Second))
	noDocker := e.h.DiskItems()
	for _, it := range noDocker {
		if strings.HasPrefix(it.GetKind(), "docker-") {
			t.Errorf("unexpected docker item when docker is absent: %+v", it)
		}
	}
	if len(noDocker) != 8 {
		t.Errorf("DiskItems() without docker = %d items; want 8", len(noDocker))
	}
}
