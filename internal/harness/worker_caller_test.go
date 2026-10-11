package harness

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/server"
)

func TestHarnessCallerUnit(t *testing.T) {
	h := &Harness{
		runs: make(map[string]*run),
	}

	// Empty runs: returns nil
	if got := h.Caller(os.Getpid(), "tok"); got != nil {
		t.Fatalf("expected nil for empty runs, got: %+v", got)
	}

	// Register a simulated worker run
	h.runs["task-1"] = &run{
		id:    "task-1",
		wish:  "wish-1",
		task:  &planv1.Task{Id: "task-1", WishId: "wish-1"},
		pid:   os.Getpid(),
		token: "secret-worker-token-xyz",
	}

	// 1. Matched by token when PID is not available (e.g. over TCP or non-Unix socket)
	c1 := h.Caller(0, "secret-worker-token-xyz")
	if c1 == nil || c1.TaskID != "task-1" || c1.WishID != "wish-1" {
		t.Fatalf("expected caller matching task-1/wish-1 by token, got: %+v", c1)
	}

	// 2. Matched by PID even if token is empty (worker cleared DJINN_WORKER_TOKEN and DJINN_TASK_ID)
	c2 := h.Caller(os.Getpid(), "")
	if c2 == nil || c2.TaskID != "task-1" || c2.WishID != "wish-1" {
		t.Fatalf("expected caller matching task-1/wish-1 by PID, got: %+v", c2)
	}

	// 3. Matched when both PID and token are present
	c3 := h.Caller(os.Getpid(), "secret-worker-token-xyz")
	if c3 == nil || c3.TaskID != "task-1" || c3.WishID != "wish-1" {
		t.Fatalf("expected caller matching task-1/wish-1 by both, got: %+v", c3)
	}

	// 4. Unmatched token and unmatched PID returns nil
	if got := h.Caller(9999999, "wrong-token"); got != nil {
		t.Fatalf("expected nil for unmatched, got: %+v", got)
	}

	// 5. Zero PID and empty token returns nil
	if got := h.Caller(0, ""); got != nil {
		t.Fatalf("expected nil for zero/empty, got: %+v", got)
	}
}

// workerRoundTripper injects the worker token header into requests to simulate the CLI.
type workerRoundTripper struct {
	base  http.RoundTripper
	token string
}

func (w *workerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	if w.token != "" {
		clone.Header.Set("X-Djinn-Worker-Token", w.token)
	}
	return w.base.RoundTrip(clone)
}

func TestWorkerClearedTaskIDCannotMakeWish(t *testing.T) {
	home := t.TempDir()
	e := up(t, home)

	// Spawn a worker task so it has an active run in the harness with a token.
	wishID, _ := e.wish(t, gitRepo(t))
	task := e.spawn(t, wishID, "wait for instructions\nwait\n")

	// Retrieve the worker's token from harness runs.
	e.h.mu.Lock()
	r := e.h.runs[task.GetId()]
	var workerToken string
	if r != nil {
		workerToken = r.token
	}
	e.h.mu.Unlock()

	if workerToken == "" {
		t.Fatal("expected spawned worker to have a non-empty token")
	}

	workerHTTPClient := &http.Client{
		Transport: &workerRoundTripper{
			base:  e.srv.Client().Transport,
			token: workerToken,
		},
	}

	workerWishes := planv1connect.NewWishServiceClient(workerHTTPClient, e.srv.URL)
	workerQuestions := planv1connect.NewQuestionServiceClient(workerHTTPClient, e.srv.URL)
	workerBlocks := planv1connect.NewBlockServiceClient(workerHTTPClient, e.srv.URL)
	workerTasks := planv1connect.NewTaskServiceClient(workerHTTPClient, e.srv.URL)

	// 1. Worker attempts to create a wish: must be refused!
	// Even though DJINN_TASK_ID is cleared, the worker token identifies the worker.
	_, err := workerWishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Stray Wish",
	}))
	if err == nil {
		t.Fatal("expected WishService.Make by worker to be refused, but succeeded")
	}
	want := "a worker cannot make a wish; to try a command by hand, start a Djinn of your own with its own data folder"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got: %v", want, err)
	}

	// 2. Permitted command: QuestionService.Ask
	// Worker calls question ask without specifying WishId or TaskId:
	// WorkerGuard defaults them to the worker's wish and task!
	askRes, err := workerQuestions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Should we use PostgreSQL or SQLite?",
	}))
	if err != nil {
		t.Fatalf("expected question ask by worker to succeed, got: %v", err)
	}
	if askRes.Msg.GetQuestion().GetWishId() != wishID {
		t.Fatalf("expected question wish %s, got: %s", wishID, askRes.Msg.GetQuestion().GetWishId())
	}
	if askRes.Msg.GetQuestion().GetTaskId() != task.GetId() {
		t.Fatalf("expected question task %s, got: %s", task.GetId(), askRes.Msg.GetQuestion().GetTaskId())
	}

	// 3. Worker attempts QuestionService.Ask for a different wish: must be refused!
	_, err = workerQuestions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
		WishId: "other-wish-id",
		Text:   "Question in other wish",
	}))
	if err == nil || !strings.Contains(err.Error(), "a worker cannot ask a question in wish") {
		t.Fatalf("expected question ask in other wish to be refused, got: %v", err)
	}

	// 4. Permitted command: BlockService.Put
	// Worker puts a block without specifying WishId or TaskId:
	// WorkerGuard defaults them to the worker's wish and task!
	blockRes, err := workerBlocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{
		Kind:    "decision",
		Title:   "Storage engine",
		Content: "Use SQLite for metadata.",
	}))
	if err != nil {
		t.Fatalf("expected block put by worker to succeed, got: %v", err)
	}
	if blockRes.Msg.GetBlock().GetWishId() != wishID {
		t.Fatalf("expected block wish %s, got: %s", wishID, blockRes.Msg.GetBlock().GetWishId())
	}

	// 5. Permitted command: TaskService.Update of own task
	_, err = workerTasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: task.GetId(),
		Title:  proto.String("Updated title by worker"),
	}))
	if err != nil {
		t.Fatalf("expected task update of own task to succeed, got: %v", err)
	}

	// 6. Worker attempts TaskService.Update of other task: must be refused!
	_, err = workerTasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: "other-task-id",
		Title:  proto.String("Malicious update"),
	}))
	if err == nil || !strings.Contains(err.Error(), "a worker can only update its own task") {
		t.Fatalf("expected task update of other task to be refused, got: %v", err)
	}

	// 7. Disallowed mutating procedure: WishService.Pause: must be refused!
	_, err = workerWishes.Pause(t.Context(), connect.NewRequest(&planv1.WishServicePauseRequest{
		WishId: wishID,
	}))
	if err == nil || !strings.Contains(err.Error(), "a worker cannot call") {
		t.Fatalf("expected wish pause to be refused, got: %v", err)
	}

	// 8. Read-only procedure: WishService.List: must succeed!
	listRes, err := workerWishes.List(t.Context(), connect.NewRequest(&planv1.WishServiceListRequest{}))
	if err != nil {
		t.Fatalf("expected wish list to succeed for worker, got: %v", err)
	}
	if len(listRes.Msg.GetWishes()) == 0 {
		t.Fatalf("expected at least 1 wish in list, got 0")
	}
}

func TestWorkerClearedTaskIDAndTokenIdentifiedByPID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket peer credentials not available on Windows")
	}

	home := t.TempDir()
	e := up(t, home)

	wishID, _ := e.wish(t, gitRepo(t))
	task := e.spawn(t, wishID, "wait for instructions\nwait\n")

	// Set r.pid to current process PID
	e.h.mu.Lock()
	r := e.h.runs[task.GetId()]
	if r != nil {
		r.pid = os.Getpid()
	}
	e.h.mu.Unlock()

	dir, err := os.MkdirTemp("", "dj")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "djinn.sock")
	ln, err := server.ListenUnix(socket)
	if err != nil {
		t.Fatal(err)
	}
	// Serve using server.Serve, which configures ConnContext to extract kernel peer PID via SO_PEERCRED
	go server.Serve(t.Context(), ln, e.srv.Config.Handler) //nolint:errcheck // server runs in background until context cancellation

	var d net.Dialer
	unixClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}
	workerWishes := planv1connect.NewWishServiceClient(unixClient, "http://djinn")
	workerQuestions := planv1connect.NewQuestionServiceClient(unixClient, "http://djinn")

	// 1. WishService.Make must be refused because kernel socket peer credentials identify the worker PID,
	// even with NO worker token header and NO task/wish IDs!
	_, err = workerWishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Stray Wish by PID",
	}))
	if err == nil {
		t.Fatal("expected WishService.Make by worker PID to be refused")
	}
	want := "a worker cannot make a wish; to try a command by hand, start a Djinn of your own with its own data folder"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got: %v", want, err)
	}

	// 2. Permitted command succeeds!
	askRes, err := workerQuestions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Can I proceed?",
	}))
	if err != nil {
		t.Fatalf("expected question ask to succeed, got: %v", err)
	}
	if askRes.Msg.GetQuestion().GetWishId() != wishID || askRes.Msg.GetQuestion().GetTaskId() != task.GetId() {
		t.Fatalf("expected wish %s, task %s, got: wish %s, task %s", wishID, task.GetId(), askRes.Msg.GetQuestion().GetWishId(), askRes.Msg.GetQuestion().GetTaskId())
	}
}
