package harness

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// counting is the fake agent, each worker working a while first, which counts its workers: the tasks it starts, in
// order, and the most that run at once.
type counting struct {
	mu            sync.Mutex
	starts        []string // task identifiers
	running, peak int
	// pairs, when not 0, holds each worker until another runs beside it, or until pairs workers have started (none
	// is left to come): two slots run two workers, however long the harness takes to start one (a git command
	// costs more than the 50 ms of work on Windows).
	pairs int
	live  []*counted
}

func (c *counting) Start(ctx context.Context, spec Spec) (Worker, error) {
	spec.Prompt = "sleep 50ms\n" + spec.Prompt
	w, err := Fake{}.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.starts = append(c.starts, spec.TaskID)
	c.running++
	c.peak = max(c.peak, c.running)
	cw := &counted{Worker: w, c: c, paired: make(chan struct{})}
	c.live = append(c.live, cw)
	if c.running >= 2 {
		for _, l := range c.live {
			l.pair()
		}
	}
	if len(c.starts) >= c.pairs {
		cw.pair()
	}
	return cw, nil
}

func (c *counting) seen() (starts []string, peak int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.starts), c.peak
}

// pairUpTo holds the workers until another runs beside each, until n workers have started in all.
func (c *counting) pairUpTo(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pairs = n
}

// counted is a worker of counting: it stops counting once the harness has its result, before its slot is free; held
// until paired, it keeps its slot meanwhile.
type counted struct {
	Worker
	c       *counting
	once    sync.Once
	paired  chan struct{} // closed once another worker ran beside it, or none is left to come
	pairing sync.Once
}

func (w *counted) pair() { w.pairing.Do(func() { close(w.paired) }) }

func (w *counted) Wait() Result {
	res := w.Worker.Wait()
	select {
	case <-w.paired:
	case <-time.After(5 * time.Second): // The machine runs one worker at a time: the test says so.
	}
	w.once.Do(func() {
		w.c.mu.Lock()
		w.c.running--
		w.c.live = slices.DeleteFunc(w.c.live, func(l *counted) bool { return l == w })
		w.c.mu.Unlock()
	})
	return res
}

// countingProviders are the providers, the fake one counting its workers.
func countingProviders() (map[planv1.Provider]Provider, *counting) {
	c := &counting{}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_FAKE] = c
	return providers, c
}

// putWish records the wish as it is, as a previous djinn left it.
func putWish(t *testing.T, db *store.Store, w *planv1.Wish) {
	t.Helper()
	if err := db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "crash", w); err != nil {
			return err
		}
		return tx.Put(w)
	}); err != nil {
		t.Fatal(err)
	}
}

// leftRunning records a fake task of the wish in the project whose worker ran when Djinn stopped, in worktree, as
// edit leaves it.
func leftRunning(t *testing.T, db *store.Store, wishID, projectID, code, worktree string, created time.Time, edit ...func(*planv1.Task)) *planv1.Task {
	t.Helper()
	tk := &planv1.Task{
		Id: store.NewID(), WishId: wishID, ProjectId: projectID, Code: code, Title: code, Scheduled: true,
		Status: planv1.TaskStatus_TASK_STATUS_RUNNING, Provider: planv1.Provider_PROVIDER_FAKE, Worktree: worktree,
		SessionId: "s-" + code, CreateTime: timestamppb.New(created), StartTime: timestamppb.New(created),
	}
	for _, e := range edit {
		e(tk)
	}
	putTask(t, db, tk, "text "+code)
	return tk
}

// TestStoppedBeforeItsWorkerStarted: djinn up stops while the worktree of a planned task is being made, before its
// worker starts: the task waits for a worker again, with no worktree to lose, and the next start runs it.
func TestStoppedBeforeItsWorkerStarted(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	e := up(t, home, WithTick(time.Hour))
	wishID, projectID := e.wish(t, gitRepo(t))
	e.h.cancel() // djinn up stops: git cannot make the worktree any more.
	<-e.h.loopDone
	planned := &planv1.Task{
		Id: store.NewID(), WishId: wishID, ProjectId: projectID, Code: "W1", Title: "W1", Scheduled: true,
		Status: planv1.TaskStatus_TASK_STATUS_PENDING, Provider: planv1.Provider_PROVIDER_FAKE, CreateTime: timestamppb.Now(),
	}
	putTask(t, e.db, planned, "text ok")
	if err := e.h.launchPlanned(t.Context(), planned); err == nil {
		t.Fatal("a worker started while djinn up stops")
	}
	got := e.get(t, planned.GetId())
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || got.GetError() != "" || got.GetWorktree() != "" ||
		got.GetEndTime() != nil || got.GetWaitReason() != whyStoppedFirst {
		t.Errorf("stopped before its worker started = %v", got)
	}
	e.down()

	e = up(t, home)
	e.until(t, planned.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
}

// TestRestartResumesInOrder: Djinn restarts with 6 workers cut short over 2 wishes, a planned task, and 2 tasks of a
// paused wish, on a machine of 2 slots. The orchestrator alone puts them back: the first wish's go first, then the
// second's, each in its own order, then the planned one, never more than 2 at once, the others waiting for a slot and
// saying so; all end done, each worker started once. The paused wish's tasks wait for it, and resume once it is active.
func TestRestartResumesInOrder(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	e := up(t, home)
	second, secondProject := e.wish(t, gitRepo(t))
	first, firstProject := e.wish(t, gitRepo(t))
	shelved, shelvedProject := e.wish(t, gitRepo(t))
	for id, rank := range map[string]int32{first: 1, second: 2, shelved: 0} {
		w, err := store.Get[*planv1.Wish](t.Context(), e.db, id)
		if err != nil {
			t.Fatal(err)
		}
		w.State, w.Rank = planv1.WishState_WISH_STATE_ACTIVE, rank
		if rank == 0 {
			w.State = planv1.WishState_WISH_STATE_PAUSED
		}
		putWish(t, e.db, w)
	}
	// The tasks as the previous djinn left them, written while it is down: its scheduler would start the planned one.
	e.down()
	db, err := store.Open(t.Context(), filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour)
	code := map[string]string{}
	var resumed []*planv1.Task
	// The second wish's tasks are the oldest: the rank comes first.
	for i, c := range []string{"S1", "S2", "S3", "F1", "F2", "F3"} {
		wish, project := second, secondProject
		if c[0] == 'F' {
			wish, project = first, firstProject
		}
		tk := leftRunning(t, db, wish, project, c, t.TempDir(), at.Add(time.Duration(i)*time.Minute))
		code[tk.GetId()] = c
		resumed = append(resumed, tk)
	}
	// Planned before any of them, in the first wish: it starts after the ones Djinn resumes.
	planned := &planv1.Task{
		Id: store.NewID(), WishId: first, ProjectId: firstProject, Code: "F0", Title: "F0", Scheduled: true,
		Status: planv1.TaskStatus_TASK_STATUS_PENDING, Provider: planv1.Provider_PROVIDER_FAKE,
		CreateTime: timestamppb.New(at.Add(-time.Hour)), WaitReason: "2 workers run, the most this machine holds (set by the test)",
	}
	putTask(t, db, planned, "text F0")
	code[planned.GetId()] = "F0"
	// The paused wish's: one its pause stopped, one that ran when Djinn stopped.
	stopped := leftRunning(t, db, shelved, shelvedProject, "P1", t.TempDir(), at, func(tk *planv1.Task) {
		tk.Status, tk.WaitReason, tk.EndTime = planv1.TaskStatus_TASK_STATUS_RESUMING, whyWishPaused, timestamppb.Now()
	})
	cut := leftRunning(t, db, shelved, shelvedProject, "P2", t.TempDir(), at.Add(time.Minute))
	code[stopped.GetId()], code[cut.GetId()] = "P1", "P2"
	db.Close()

	providers, c := countingProviders()
	c.pairUpTo(7)
	e = upWith(t, home, providers, WithCapacity((&limit{slots: 2}).capacity))
	for _, tk := range append(resumed, planned) {
		got := e.until(t, tk.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
		if want := int32(1); tk != planned && got.GetResumes() != want {
			t.Errorf("%s resumed %d times, want %d", tk.GetCode(), got.GetResumes(), want)
		}
	}
	starts, peak := c.seen()
	var order []string
	for _, id := range starts {
		order = append(order, code[id])
	}
	if want := []string{"F1", "F2", "F3", "S1", "S2", "S3", "F0"}; !slices.Equal(order, want) {
		t.Errorf("started %v, want %v", order, want)
	}
	if peak != 2 {
		t.Errorf("at most %d workers ran at once, want 2", peak)
	}
	all := strings.Join(eventTexts(e.watch(t.Context(), t, resumed[2].GetId(), 0)), "\n") // S3, the last resumed
	for _, want := range []string{
		"interrupted: djinn up ended while the worker ran",
		"resuming: " + whyRestarted + "; Djinn starts it again by itself",
		"waiting: 2 workers run, the most this machine holds (set by the test)",
		"resumed after djinn restarted (1 of 3): started fake in ",
		"done",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no event %q in S3's:\n%s", want, all)
		}
	}
	if n := strings.Count(all, "resumed after djinn restarted"); n != 1 {
		t.Errorf("S3 resumed %d times:\n%s", n, all)
	}

	// The paused wish's tasks waited for it, and start once it is active again, each once.
	for _, tk := range []*planv1.Task{stopped, cut} {
		got := e.get(t, tk.GetId())
		if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RESUMING ||
			got.GetWaitReason() != map[string]string{"P1": whyWishPaused, "P2": "its wish is paused"}[tk.GetCode()] {
			t.Errorf("%s while its wish is paused: %v", tk.GetCode(), got)
		}
	}
	c.pairUpTo(9)
	if _, err := e.wishes.Activate(t.Context(), connect.NewRequest(&planv1.WishServiceActivateRequest{WishId: shelved})); err != nil {
		t.Fatal(err)
	}
	for _, tk := range []*planv1.Task{stopped, cut} {
		e.until(t, tk.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	}
	starts, peak = c.seen()
	if len(starts) != 9 || peak != 2 {
		t.Errorf("%d workers started, %d at once at most; want 9, 2", len(starts), peak)
	}
	seen := map[string]bool{}
	for _, id := range starts {
		if seen[id] {
			t.Errorf("%s started twice", code[id])
		}
		seen[id] = true
	}
	if got := e.get(t, stopped.GetId()); got.GetResumes() != 0 {
		t.Errorf("P1, stopped by its wish's pause, spent a resume: %v", got)
	}
	if got := e.get(t, cut.GetId()); got.GetResumes() != 1 {
		t.Errorf("P2, cut short by the restart: %v", got)
	}
}

// TestRestartBeforeNewSpawn: a task spawned as a slot frees after a restart does not take it from a task Djinn
// resumes: it waits, and starts once the resumed one is done.
func TestRestartBeforeNewSpawn(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	e := up(t, home)
	wishID, projectID := e.wish(t, gitRepo(t))
	cut := leftRunning(t, e.db, wishID, projectID, "W1", t.TempDir(), time.Now().Add(-time.Hour))
	e.down()

	providers, _ := countingProviders()
	l := &limit{slots: 1, pressure: "swap in use"}
	e = upWith(t, home, providers, WithCapacity(l.capacity), WithTick(time.Hour)) // Only a spawn or an end wakes it.
	e.until(t, cut.GetId(), func(t *planv1.Task) bool {
		return t.GetWaitReason() == "the machine is under pressure: swap in use"
	})
	l.set(1, "")
	fresh := e.mustSpawn(t, wishID, "New", "text new", nil)
	if fresh.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING ||
		fresh.GetWaitReason() != "1 worker runs, the most this machine holds (set by the test)" {
		t.Errorf("a task spawned before the resumed one starts: %v", fresh)
	}
	resumed := e.until(t, cut.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	fresh = e.until(t, fresh.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if fresh.GetStartTime().AsTime().Before(resumed.GetEndTime().AsTime()) {
		t.Errorf("the new task started at %v, before the resumed one ended at %v", fresh.GetStartTime().AsTime(),
			resumed.GetEndTime().AsTime())
	}
}
