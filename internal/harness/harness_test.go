package harness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// env is djinn up in a test: the plan and task services on a store, behind a real Connect server.
type env struct {
	home    string
	db      *store.Store
	h       *Harness
	srv     *httptest.Server
	tasks   planv1connect.TaskServiceClient
	project planv1connect.ProjectServiceClient
	wishes  planv1connect.WishServiceClient
	plans   planv1connect.PlanServiceClient
	last    string // the wish e.wish made last
}

// testProviders are the providers of a test: a watcher's waits are the test's, not a second.
func testProviders() map[planv1.Provider]Provider {
	providers := Providers()
	providers[planv1.Provider_PROVIDER_WATCH] = fastWatch
	return providers
}

// fastWatch is a watcher whose command makes a paragraph after 50 ms of silence, and starts again 50 ms after it ends.
var fastWatch = Watch{Quiet: 50 * time.Millisecond, Backoff: 50 * time.Millisecond, Gap: 50 * time.Millisecond}

// up starts the services on the database in home, as djinn up does: recover, schedule, then serve.
func up(t *testing.T, home string, opts ...Option) *env {
	t.Helper()
	return upWith(t, home, testProviders(), opts...)
}

// upWith is up with these providers.
func upWith(t *testing.T, home string, providers map[planv1.Provider]Provider, opts ...Option) *env {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	h := New(db, home, providers, opts...)
	if err := h.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.Schedule()
	mux := http.NewServeMux()
	for prefix, handler := range plan.Handlers(db, plan.WithAnswered(h.Answered), plan.WithEnlightened(h.Enlightened),
		plan.WithWorkers(h)) {
		mux.Handle(prefix, handler)
	}
	mux.Handle(Handler(h))
	mux.Handle(PlanHandler(h))
	srv := httptest.NewServer(mux)
	e := &env{
		home: home, db: db, h: h, srv: srv,
		tasks:   planv1connect.NewTaskServiceClient(srv.Client(), srv.URL),
		project: planv1connect.NewProjectServiceClient(srv.Client(), srv.URL),
		wishes:  planv1connect.NewWishServiceClient(srv.Client(), srv.URL),
		plans:   planv1connect.NewPlanServiceClient(srv.Client(), srv.URL),
	}
	t.Cleanup(e.down)
	return e
}

// down stops djinn up: the server, the workers, then the database. It may run twice.
func (e *env) down() {
	if e.db == nil {
		return
	}
	e.srv.CloseClientConnections()
	e.srv.Close()
	e.h.Close()
	e.db.Close()
	e.db = nil
}

// wish adds dir as a project and makes a wish on it. The wish it made before is paused, as three wishes at most are
// active: a test makes one per case.
func (e *env) wish(t *testing.T, dir string) (wishID, projectID string) {
	t.Helper()
	p, err := e.project.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
	if err != nil {
		t.Fatal(err)
	}
	if e.last != "" {
		if _, err := e.wishes.Pause(t.Context(), connect.NewRequest(&planv1.WishServicePauseRequest{WishId: e.last})); err != nil {
			t.Fatal(err)
		}
	}
	w, err := e.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Run Djinn on itself", ProjectIds: []string{p.Msg.GetProject().GetId()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	e.last = w.Msg.GetWish().GetId()
	return e.last, p.Msg.GetProject().GetId()
}

func (e *env) spawn(t *testing.T, wishID, prompt string) *planv1.Task {
	t.Helper()
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Try the harness", Prompt: prompt, Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetTask()
}

// watch follows the task until its stream ends, and returns its events.
func (e *env) watch(ctx context.Context, t *testing.T, id string, after int64) []*planv1.TaskEvent {
	t.Helper()
	s, err := e.tasks.Watch(ctx, connect.NewRequest(&planv1.TaskServiceWatchRequest{TaskId: id, AfterSeq: after}))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var out []*planv1.TaskEvent
	for s.Receive() {
		out = append(out, s.Msg().GetEvent())
	}
	if err := s.Err(); err != nil && ctx.Err() == nil {
		t.Fatal(err)
	}
	return out
}

func (e *env) get(t *testing.T, id string) *planv1.Task {
	t.Helper()
	res, err := e.tasks.Get(t.Context(), connect.NewRequest(&planv1.TaskServiceGetRequest{TaskId: id}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetTask()
}

func eventKinds(events []*planv1.TaskEvent) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = strings.TrimPrefix(ev.GetKind().String(), "TASK_EVENT_KIND_")
	}
	return out
}

// checkSeqs fails unless the events are numbered from first, with no gap.
func checkSeqs(t *testing.T, events []*planv1.TaskEvent, first int64) {
	t.Helper()
	for i, ev := range events {
		if ev.GetSeq() != first+int64(i) {
			t.Fatalf("event %d has seq %d, want %d", i, ev.GetSeq(), first+int64(i))
		}
	}
}

func TestSpawnInGit(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	e := up(t, t.TempDir())
	wishID, projectID := e.wish(t, repo)

	task := e.spawn(t, wishID, "text hello\ntool Bash ls\nresult README.md\nusage 100 20 0.5")
	if task.GetCode() != "W1" || task.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || task.GetProjectId() != projectID {
		t.Errorf("spawned %v", task)
	}
	wantDir := filepath.Join(e.home, "projects", projectID, "worktrees", task.GetId())
	if task.GetWorktree() != wantDir || !strings.HasPrefix(task.GetBranch(), "w1-try-the-harness-") {
		t.Errorf("worktree %s on %s; want %s", task.GetWorktree(), task.GetBranch(), wantDir)
	}
	if _, err := os.Stat(filepath.Join(wantDir, "app", "README.md")); err != nil {
		t.Errorf("the worktree lacks the project: %v", err)
	}

	events := e.watch(t.Context(), t, task.GetId(), 0)
	want := []string{"PROMPT", "STATUS", "STATUS", "TEXT", "TOOL_CALL", "TOOL_RESULT", "USAGE", "STATUS"}
	if got := eventKinds(events); !slices.Equal(got, want) {
		t.Fatalf("events = %v\nwant %v", got, want)
	}
	checkSeqs(t, events, 1)
	if events[len(events)-1].GetText() != "done" || events[0].GetRaw() != "" {
		t.Errorf("last event %q; raw sent without --raw: %q", events[len(events)-1].GetText(), events[0].GetRaw())
	}

	done := e.get(t, task.GetId())
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || done.GetUsage().GetCostUsd() != 0.5 ||
		done.GetUsage().GetInputTokens() != 100 || done.GetSessionId() != task.GetId() || done.GetEndTime() == nil {
		t.Errorf("done = %v", done)
	}
	// Resuming from a position sends only what follows.
	if rest := e.watch(t.Context(), t, task.GetId(), 6); len(rest) != 2 || rest[0].GetSeq() != 7 {
		t.Errorf("after 6: %v", eventKinds(rest))
	}

	// A second task of the wish gets the next code and its own worktree.
	second := e.spawn(t, wishID, "text again")
	if second.GetCode() != "W2" || second.GetWorktree() == task.GetWorktree() {
		t.Errorf("second task %v", second)
	}
	e.watch(t.Context(), t, second.GetId(), 0)

	// Clean removes the worktree, keeps the branch, and leaves the project's folder as it was.
	cleaned, err := e.tasks.Clean(t.Context(), connect.NewRequest(&planv1.TaskServiceCleanRequest{TaskId: task.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	if cleaned.Msg.GetTask().GetWorktree() != "" {
		t.Errorf("cleaned task still has a worktree")
	}
	if _, err := os.Stat(wantDir); !os.IsNotExist(err) {
		t.Errorf("worktree still there: %v", err)
	}
	if out, _ := git(t.Context(), repo, "branch", "--list", task.GetBranch()); out == "" {
		t.Error("the branch went with the worktree")
	}
	if out, _ := git(t.Context(), repo, "status", "--porcelain"); out != "" {
		t.Errorf("the project's folder changed: %q", out)
	}
	if _, err := e.tasks.Clean(t.Context(), connect.NewRequest(&planv1.TaskServiceCleanRequest{TaskId: task.GetId()})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("clean twice: %v", err)
	}
}

func TestSpawnOutsideGit(t *testing.T) {
	t.Parallel()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, dir)
	task := e.spawn(t, wishID, "fail no tests")
	if task.GetWorktree() != "" || task.GetBranch() != "" {
		t.Errorf("a worktree outside Git: %v", task)
	}
	events := e.watch(t.Context(), t, task.GetId(), 0)
	if !strings.Contains(events[1].GetText(), "in "+dir) {
		t.Errorf("started %q, want in %s", events[1].GetText(), dir)
	}
	got := e.get(t, task.GetId())
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED || got.GetError() != "no tests" || got.GetExitCode() != 1 {
		t.Errorf("failed task = %v", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("Djinn wrote in the project's folder: %v", entries)
	}
}

func TestSpawnRefused(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir())
	wishID, projectID := e.wish(t, t.TempDir())
	other, err := e.project.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	two, err := e.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Two projects", ProjectIds: []string{projectID, other.Msg.GetProject().GetId()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		req  *planv1.TaskServiceSpawnRequest
		code connect.Code
	}{
		{"no title", &planv1.TaskServiceSpawnRequest{WishId: wishID}, connect.CodeInvalidArgument},
		{"unknown wish", &planv1.TaskServiceSpawnRequest{WishId: store.NewID(), Title: "x"}, connect.CodeNotFound},
		{"wish of two projects, none named", &planv1.TaskServiceSpawnRequest{WishId: two.Msg.GetWish().GetId(), Title: "x"}, connect.CodeInvalidArgument},
		{"project not of the wish", &planv1.TaskServiceSpawnRequest{WishId: wishID, Title: "x", ProjectId: store.NewID()}, connect.CodeNotFound},
	} {
		if _, err := e.tasks.Spawn(t.Context(), connect.NewRequest(tt.req)); connect.CodeOf(err) != tt.code {
			t.Errorf("%s: %v, want %v", tt.name, err, tt.code)
		}
	}
}

func TestStopLongWorker(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())
	task := e.spawn(t, wishID, "text working\nsleep 1h")

	watched := make(chan []*planv1.TaskEvent)
	go func() { watched <- e.watch(context.Background(), t, task.GetId(), 0) }()
	res, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Msg.GetTask(); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED || got.GetError() != "stopped on request" {
		t.Errorf("stopped task = %v", got)
	}
	select {
	case events := <-watched:
		if last := events[len(events)-1]; last.GetText() != "stopped: stopped on request" {
			t.Errorf("last event %q", last.GetText())
		}
		checkSeqs(t, events, 1)
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not end with the task")
	}
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("stop twice: %v", err)
	}
}

// TestNothingLostOnShutdown: djinn up stops while a worker runs; its task is interrupted, keeps what resuming it
// needs, and its events survive the restart. The next start resumes it, in the same task, worktree and session, and
// it finishes. A task a crash left running is resumed the same way.
func TestNothingLostOnShutdown(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	home := t.TempDir()
	e := up(t, home)
	wishID, _ := e.wish(t, repo)
	task := e.spawn(t, wishID, "text halfway\nusage 10 2 0.01\nsleep 1h")

	// Wait until the worker has said what it had to say before its long sleep.
	ctx, cancel := context.WithCancel(t.Context())
	s, err := e.tasks.Watch(ctx, connect.NewRequest(&planv1.TaskServiceWatchRequest{TaskId: task.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	for s.Receive() && s.Msg().GetEvent().GetKind() != planv1.TaskEventKind_TASK_EVENT_KIND_USAGE {
	}
	cancel()
	s.Close()
	e.down()
	before := storedTask(t, home, task.GetId())
	if before.GetStatus() != planv1.TaskStatus_TASK_STATUS_INTERRUPTED || before.GetProvider() != planv1.Provider_PROVIDER_FAKE ||
		before.GetSessionId() != task.GetId() || before.GetWorktree() == "" || before.GetUsage().GetCostUsd() != 0.01 {
		t.Errorf("after the stop: %v", before)
	}
	worktree := before.GetWorktree()

	e = up(t, home)
	events := e.watch(t.Context(), t, task.GetId(), 0)
	got := e.get(t, task.GetId())
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetWorktree() != worktree || got.GetSessionId() != task.GetId() ||
		got.GetUsage().GetCostUsd() != 0.01 || got.GetResumes() != 1 {
		t.Errorf("after the restart: %v", got)
	}
	if _, err := os.Stat(got.GetWorktree()); err != nil {
		t.Errorf("the worktree is gone: %v", err)
	}
	want := []string{"PROMPT", "STATUS", "STATUS", "TEXT", "USAGE", "STATUS", "STATUS", "STATUS", "STATUS", "TEXT", "STATUS"}
	if got := eventKinds(events); !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	checkSeqs(t, events, 1)
	for i, prefix := range map[int]string{
		5: "interrupted", 6: "resuming: djinn restarted while the worker ran", 7: "resumed after djinn restarted (1 of 3)",
		9: restartedLine, 10: "done",
	} {
		if !strings.HasPrefix(events[i].GetText(), prefix) {
			t.Errorf("event %d = %q, want %q", i+1, events[i].GetText(), prefix)
		}
	}
	if list := e.list(t, wishID); len(list) != 1 {
		t.Errorf("%d tasks in the wish, want the one resumed", len(list))
	}

	// A crash leaves a task running in the store: the next start marks it interrupted, then resumes it.
	crashed := proto.CloneOf(got)
	crashed.Id, crashed.Code, crashed.Status = store.NewID(), "W9", planv1.TaskStatus_TASK_STATUS_RUNNING
	crashed.EndTime, crashed.Error, crashed.Resumes = nil, "", 0
	putTask(t, e.db, crashed, "x")
	e.down()
	e = up(t, home)
	events = e.watch(t.Context(), t, crashed.GetId(), 0)
	after := e.get(t, crashed.GetId())
	if after.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || after.GetWorktree() != crashed.GetWorktree() || after.GetResumes() != 1 {
		t.Errorf("crashed task after the restart: %v", after)
	}
	if got := eventKinds(events); !slices.Equal(got, []string{"PROMPT", "STATUS", "STATUS", "STATUS", "STATUS", "TEXT", "STATUS"}) {
		t.Errorf("crashed task events: %v", got)
	}
}

// storedTask reads a task from the store of a djinn up that is down.
func storedTask(t *testing.T, home, id string) *planv1.Task {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := store.Get[*planv1.Task](t.Context(), db, id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// putTask writes a task as a previous djinn up left it, with its prompt as its first event.
func putTask(t *testing.T, db *store.Store, task *planv1.Task, prompt string) {
	t.Helper()
	if err := db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "crash", task); err != nil {
			return err
		}
		if err := tx.Put(task); err != nil {
			return err
		}
		return tx.Put(newEvent(task.GetId(), 1, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, Text: prompt}))
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWatchUnknown(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir())
	s, err := e.tasks.Watch(t.Context(), connect.NewRequest(&planv1.TaskServiceWatchRequest{TaskId: store.NewID()}))
	if err == nil {
		for s.Receive() {
		}
		err = s.Err()
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("watch an unknown task: %v", err)
	}
}

// TestRecoverLeavesPlannedTasks: a task that no worker ever started, such as the plan of an imported wish, stays
// pending across a restart; only a started one is interrupted.
func TestRecoverLeavesPlannedTasks(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := up(t, home)
	planned := &planv1.Task{Id: "01a118d0-0000-7000-8000-000000000001", WishId: "01a118d0-0000-7000-8000-0000000000aa",
		Code: "T01", Title: "planned", Status: planv1.TaskStatus_TASK_STATUS_PENDING}
	err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "put", planned); err != nil {
			return err
		}
		return tx.Put(planned)
	})
	if err != nil {
		t.Fatal(err)
	}
	e.down()
	e = up(t, home)
	got, err := store.Get[*planv1.Task](t.Context(), e.db, planned.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
		t.Errorf("planned task after a restart: %s, want pending", got.GetStatus())
	}
}
