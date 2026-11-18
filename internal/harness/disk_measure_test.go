package harness

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// TestOnDisk (W122): a file counts by the blocks it takes, as du counts: a sparse file of a gigabyte takes next to
// nothing, a file of one byte takes a block.
func TestOnDisk(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows counts a file's length")
	}
	dir := t.TempDir()
	sparse := filepath.Join(dir, "sparse")
	f, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(GB); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got := onDiskOf(t, sparse); got >= 1_000_000 {
		t.Errorf("a sparse file of 1 GB takes %d bytes; want next to nothing", got)
	}
	writeFile(t, dir, "byte", "b")
	if got := onDiskOf(t, filepath.Join(dir, "byte")); got < 1 {
		t.Errorf("a file of one byte takes %d bytes; want a block", got)
	}
}

// TestDiskRestLeavesLittleUnmeasured (W122, L26): on 18/11/2026 the Disk tab left ~700 GB "not measured": the
// developer's repositories, the browsers' and editors' folders, ~/go/.go, a crash dump, none of which any row counted.
// The slow pass names each entry of the home, those of ~/.cache, ~/.local/share and ~/go one by one, without the
// folders the other rows count: the rows sum up to what the home takes, to the byte. The worktrees and scratch say
// what ended tasks' take, and the lead hears these sources when the disk fills.
func TestDiskRestLeavesLittleUnmeasured(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	data := filepath.Join(fakeHome, ".config", "djinn")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	e := up(t, data, func(h *Harness) {
		h.disk.wait = 5 * time.Second
		h.disk.restRoots = func() (string, []string) { return fakeHome, nil }
		h.disk.restMin = 10_000
	})
	wishID, projectID := e.wish(t, folder(t))
	t.Setenv("PATH", t.TempDir()) // No docker.

	put := func(code string, status planv1.TaskStatus) string {
		task := &planv1.Task{Id: store.NewID(), WishId: wishID, ProjectId: projectID, Code: code, Title: "Measure",
			Status: status, CreateTime: timestamppb.Now()}
		if err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
			if err := tx.Journal("test", "put", task); err != nil {
				return err
			}
			return tx.Put(task)
		}); err != nil {
			t.Fatal(err)
		}
		return task.GetId()
	}
	done, live := put("W1", planv1.TaskStatus_TASK_STATUS_DONE), put("W2", planv1.TaskStatus_TASK_STATUS_STOPPED)
	worktrees := filepath.Join(data, "projects", projectID, "worktrees")
	writeFile(t, worktrees, done+"/main.go", strings.Repeat("d", 40_000))
	writeFile(t, worktrees, live+"/main.go", strings.Repeat("l", 10_000))
	writeFile(t, worktrees, "gone/main.go", strings.Repeat("g", 5_000)) // A task Djinn no longer knows.
	writeFile(t, filepath.Join(data, "scratch", projectID), done+"/bench.txt", strings.Repeat("s", 8_000))

	// The caches the items count.
	writeFile(t, fakeHome, ".cache/go-build/ab/entry", strings.Repeat("c", 60_000))
	writeFile(t, fakeHome, ".cache/golangci-lint/entry", strings.Repeat("c", 20_000))
	writeFile(t, fakeHome, "go/pkg/mod/cache/download/mod.zip", strings.Repeat("m", 50_000))
	// What no row counted.
	writeFile(t, fakeHome, "empowill/dolmen/src/main.go", strings.Repeat("r", 400_000))
	writeFile(t, fakeHome, "go/.go/pkg/old.a", strings.Repeat("o", 100_000))
	writeFile(t, fakeHome, ".cache/google-chrome/cache", strings.Repeat("b", 30_000))
	writeFile(t, fakeHome, ".local/share/JetBrains/index", strings.Repeat("j", 25_000))
	writeFile(t, fakeHome, "java_error_in_goland.hprof", strings.Repeat("h", 35_000))
	writeFile(t, fakeHome, ".bashrc", "alias ll='ls -l'\n")
	writeFile(t, fakeHome, ".profile", "umask 022\n")

	e.h.DiskFolders()
	items := e.h.DiskItems()
	e.h.disk.wg.Wait() // The slow pass starts once both measured.
	folders := e.h.DiskFolders()

	byPath := map[string]*machinev1.DiskFolder{}
	var rest, accounted uint64
	for _, f := range folders {
		byPath[f.GetPath()] = f
		if f.GetKind() == folderHome {
			rest += f.GetSizeBytes()
		}
	}
	for _, it := range items {
		accounted += it.GetSizeBytes()
	}
	// What the home takes outside Djinn's data folder, which its own row counts.
	used := walk(t.Context(), fakeHome, time.Minute, map[string]uint64{filepath.Clean(data): 0}, false).bytes
	if accounted >= used/2 {
		t.Fatalf("the caches count %d of %d bytes: the fixture no longer leaves most of the home unmeasured", accounted, used)
	}
	if accounted+rest != used {
		t.Errorf("the caches (%d) and the home's entries (%d) count %d bytes; the home takes %d: %d not measured",
			accounted, rest, accounted+rest, used, int64(used)-int64(accounted+rest))
	}
	for _, path := range []string{"~/empowill", "~/go/.go", "~/.cache/google-chrome", "~/.local/share/JetBrains",
		"~/java_error_in_goland.hprof"} {
		if byPath[path] == nil {
			t.Errorf("no row for %s in %v", path, folders)
		}
	}
	for _, path := range []string{"~/.config", "~/.config/djinn", "~/.cache/go-build", "~/go/pkg", "~/.cache"} {
		if byPath[path] != nil {
			t.Errorf("a home row for %s, which another row counts or which the pass splits", path)
		}
	}
	if small := byPath["~"]; small == nil || small.GetCount() < 2 {
		t.Errorf("the home's small entries = %v; want .bashrc and .profile summed", small)
	}

	trees := byPath[worktrees]
	ended := onDiskOf(t, filepath.Join(worktrees, done, "main.go"), filepath.Join(worktrees, "gone", "main.go"))
	if trees == nil || trees.GetKind() != folderWorktrees || trees.GetProject() == "" || trees.GetCount() != 3 ||
		trees.GetEndedCount() != 2 || trees.GetEndedBytes() != ended {
		t.Errorf("worktrees = %v; want its project's name, 3 task folders, 2 ended taking %d bytes", trees, ended)
	}
	if scratch := byPath[filepath.Join(data, "scratch", projectID)]; scratch == nil || scratch.GetEndedCount() != 1 {
		t.Errorf("scratch = %v; want the project's scratch, its one task ended", scratch)
	}

	detail := fullDetail(e.h.diskSources(time.Second), 20)
	for _, name := range []string{"~/empowill ", "the worktrees of " + trees.GetProject() + " ", "of it ended tasks'"} {
		if !strings.Contains(detail, name) {
			t.Errorf("the lead's detail %q does not name %q", detail, name)
		}
	}

	// A restart reads the slow pass's measure back: no new walk of the home for diskRestEvery.
	if data, err := os.ReadFile(filepath.Join(data, diskRestFile)); err != nil || !strings.Contains(string(data), "~/empowill") {
		t.Errorf("%s = %q, %v; want the slow pass's rows", diskRestFile, data, err)
	}
}

// TestDiskWalkKeepsWhole (W122, L26): a folder whose walk ran out of time stays in the list, said partial, with its last
// whole size when that was bigger; it no longer falls to 0 and leaves the list.
func TestDiskWalkKeepsWhole(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "a/file", strings.Repeat("a", 20_000))
	if w := walk(t.Context(), dir, 0, nil, false); !w.partial {
		t.Errorf("a walk out of time = %+v; want partial", w)
	}
	if w := walk(t.Context(), dir, time.Minute, nil, false); w.partial || w.bytes == 0 {
		t.Errorf("a walk within its time = %+v; want whole", w)
	}

	h := &Harness{}
	dirs := []diskDir{{path: "/slow", kind: folderLogs}, {path: "/slower", kind: folderTilasms}}
	// Keyed as measureFolders keys them: filepath.Clean makes /slow \slow on Windows.
	walks := map[string]walked{
		filepath.Clean("/slow"):   {bytes: 10, partial: true},
		filepath.Clean("/slower"): {bytes: 30, partial: true},
	}
	previous := []*machinev1.DiskFolder{{Path: "/slow", SizeBytes: 50}, {Path: "/slower", SizeBytes: 20}}
	got := h.toDiskFolders(t.Context(), false, dirs, walks, previous, time.Now())
	if len(got) != 2 || got[0].GetPath() != "/slow" || got[0].GetSizeBytes() != 50 || !got[0].GetPartial() ||
		got[1].GetSizeBytes() != 30 || !got[1].GetPartial() {
		t.Errorf("folders = %v; want /slow at its last 50, /slower at its floor 30, both partial", got)
	}

	items := keepWhole([]*machinev1.DiskItem{{Kind: "go-mod", SizeBytes: 13, Partial: true}},
		[]*machinev1.DiskItem{{Kind: "go-mod", SizeBytes: 40}})
	if len(items) != 1 || items[0].GetSizeBytes() != 40 || !items[0].GetPartial() {
		t.Errorf("items = %v; want go-mod at its last 40, partial", items)
	}
}
