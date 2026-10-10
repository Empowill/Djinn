//go:build headless

package server_test

// Benchmarks of reading a whole wish and streaming changes over a real Unix socket, run with `go tool task bench`.
// They use the real-size wish generator of W174 (internal/testx/bigwish) at real and x10 sizes to measure:
//   (a) reading a whole wish from the store (store.Get + store.List of tasks, questions, blocks, tilasms, and azima fills),
//   (b) what the server sends to the window when something changes (proto marshal, unary call, and Connect stream),
//   (c) one change followed by the re-read, testing the hypothesis of Q63 that each change re-reads everything.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/fstest"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/harness"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx/bigwish"
)

type wishFixture struct {
	size            bigwish.Size
	db              *store.Store
	socket          string
	client          *http.Client
	tasksClient     planv1connect.TaskServiceClient
	wishesClient    planv1connect.WishServiceClient
	questionsClient planv1connect.QuestionServiceClient
	blocksClient    planv1connect.BlockServiceClient
	tilasmsClient   planv1connect.TilasmServiceClient
	workTasks       []*planv1.Task
	allTasks        []*planv1.Task
}

var (
	fixturesMu sync.Mutex
	fixtures   = map[string]*wishFixture{}
)

func getFixture(tb testing.TB, size bigwish.Size) *wishFixture {
	tb.Helper()
	fixturesMu.Lock()
	defer fixturesMu.Unlock()
	if fix, ok := fixtures[size.Name]; ok {
		return fix
	}
	plan.SetWatchInterval(0)
	ctx := context.Background()
	db, err := store.Open(ctx, "", plan.Entities()...)
	if err != nil {
		tb.Fatal(err)
	}
	data, err := bigwish.Data(size)
	if err != nil {
		tb.Fatal(err)
	}
	wishes := &plan.Wishes{Store: db}
	if _, err := wishes.ImportData(ctx, connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: data})); err != nil {
		tb.Fatal(err)
	}

	harnessDir, err := os.MkdirTemp("", "djb-harness-")
	if err != nil {
		tb.Fatal(err)
	}
	h := harness.New(db, harnessDir, harness.Providers())
	services := plan.Handlers(db)
	taskPrefix, taskHandler := harness.Handler(h)
	services[taskPrefix] = taskHandler

	hdl := server.Handler(fstest.MapFS{}, nil, services)

	dir, err := os.MkdirTemp("", "djb-bench-")
	if err != nil {
		tb.Fatal(err)
	}
	socket := filepath.Join(dir, server.SocketFile)
	ln, err := server.ListenUnix(socket)
	if err != nil {
		tb.Fatal(err)
	}
	srvCtx, cancel := context.WithCancel(ctx)
	_ = cancel
	go server.Serve(srvCtx, ln, hdl) //nolint:errcheck // Closed with the benchmark.

	hc := client(socket)
	tasksClient := planv1connect.NewTaskServiceClient(hc, "http://djinn")
	wishesClient := planv1connect.NewWishServiceClient(hc, "http://djinn")
	questionsClient := planv1connect.NewQuestionServiceClient(hc, "http://djinn")
	blocksClient := planv1connect.NewBlockServiceClient(hc, "http://djinn")
	tilasmsClient := planv1connect.NewTilasmServiceClient(hc, "http://djinn")

	allTasks, err := store.List[*planv1.Task](ctx, db, store.Where{"wish_id": bigwish.WishID})
	if err != nil {
		tb.Fatal(err)
	}
	var workTasks []*planv1.Task
	for _, t := range allTasks {
		if !plan.IsAzima(t) {
			workTasks = append(workTasks, t)
		}
	}

	fix := &wishFixture{
		size:            size,
		db:              db,
		socket:          socket,
		client:          hc,
		tasksClient:     tasksClient,
		wishesClient:    wishesClient,
		questionsClient: questionsClient,
		blocksClient:    blocksClient,
		tilasmsClient:   tilasmsClient,
		workTasks:       workTasks,
		allTasks:        allTasks,
	}
	fixtures[size.Name] = fix
	return fix
}

func client(socket string) *http.Client {
	var d net.Dialer
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}
}

// BenchmarkWishStoreRead measures reading a whole wish from the store: the wish itself, its tasks
// (with azimas and tilasms filled), questions, blocks and tilasms.
func BenchmarkWishStoreRead(b *testing.B) {
	for _, size := range bigwish.Sizes() {
		b.Run(size.Name, func(b *testing.B) {
			fix := getFixture(b, size)
			b.ResetTimer()
			b.ReportAllocs()
			var totalBytes int64
			for b.Loop() {
				wish, err := store.Get[*planv1.Wish](b.Context(), fix.db, bigwish.WishID)
				if err != nil {
					b.Fatal(err)
				}
				tasks, err := store.List[*planv1.Task](b.Context(), fix.db, store.Where{"wish_id": bigwish.WishID})
				if err != nil {
					b.Fatal(err)
				}
				questions, err := store.List[*planv1.Question](b.Context(), fix.db, store.Where{"wish_id": bigwish.WishID})
				if err != nil {
					b.Fatal(err)
				}
				blocks, err := store.List[*planv1.Block](b.Context(), fix.db, store.Where{"wish_id": bigwish.WishID})
				if err != nil {
					b.Fatal(err)
				}
				tilasms, err := store.List[*planv1.Tilasm](b.Context(), fix.db, store.Where{"wish_id": bigwish.WishID})
				if err != nil {
					b.Fatal(err)
				}
				plan.FillAzimas(tasks)
				if err := plan.FillTilasms(b.Context(), fix.db, tasks); err != nil {
					b.Fatal(err)
				}
				if totalBytes == 0 {
					totalBytes = int64(proto.Size(wish))
					for _, t := range tasks {
						totalBytes += int64(proto.Size(t))
					}
					for _, q := range questions {
						totalBytes += int64(proto.Size(q))
					}
					for _, blk := range blocks {
						totalBytes += int64(proto.Size(blk))
					}
					for _, til := range tilasms {
						totalBytes += int64(proto.Size(til))
					}
				}
			}
			b.SetBytes(totalBytes)
		})
	}
}

// BenchmarkWishServerSend measures what the server sends to the window:
//   - proto: pure serialization (proto.Marshal) of the full task list response,
//   - unary: the unary call TaskService.List over the Unix socket (HTTP/1.1),
//   - stream: delivering a watch response over the Connect stream on a task change.
func BenchmarkWishServerSend(b *testing.B) {
	for _, size := range bigwish.Sizes() {
		fix := getFixture(b, size)

		b.Run(size.Name+"/proto", func(b *testing.B) {
			res, err := fix.tasksClient.List(b.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: bigwish.WishID}))
			if err != nil {
				b.Fatal(err)
			}
			msg := res.Msg
			b.ResetTimer()
			b.ReportAllocs()
			var wireBytes int64
			for b.Loop() {
				data, err := proto.Marshal(msg)
				if err != nil {
					b.Fatal(err)
				}
				wireBytes = int64(len(data))
			}
			b.SetBytes(wireBytes)
		})

		b.Run(size.Name+"/unary", func(b *testing.B) {
			req := connect.NewRequest(&planv1.TaskServiceListRequest{WishId: bigwish.WishID})
			b.ResetTimer()
			b.ReportAllocs()
			var wireBytes int64
			for b.Loop() {
				res, err := fix.tasksClient.List(b.Context(), req)
				if err != nil {
					b.Fatal(err)
				}
				wireBytes = int64(proto.Size(res.Msg))
			}
			b.SetBytes(wireBytes)
		})

		b.Run(size.Name+"/stream", func(b *testing.B) {
			stream, err := fix.wishesClient.Watch(b.Context(), connect.NewRequest(&planv1.WishServiceWatchRequest{WishId: bigwish.WishID}))
			if err != nil {
				b.Fatal(err)
			}
			defer stream.Close()
			if !stream.Receive() {
				b.Fatal(stream.Err())
			}
			steps := 0
			var totalBytes int64
			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				steps++
				t := fix.workTasks[steps%len(fix.workTasks)]
				t.LastLine = fmt.Sprintf("bench-send %d", steps)
				err := fix.db.Tx(b.Context(), func(tx *store.Tx) error {
					if err := tx.Journal("bench", "bench/progress", t); err != nil {
						return err
					}
					return tx.Put(t)
				})
				if err != nil {
					b.Fatal(err)
				}
				for stream.Receive() {
					msg := stream.Msg()
					if slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_TASK) {
						totalBytes += int64(proto.Size(msg))
						break
					}
				}
				if err := stream.Err(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(totalBytes)/float64(b.N), "wire-B/op")
		})
	}
}

// BenchmarkWishChange measures what one change of a task costs djinn up and the window, on a real and x10 wish:
// the change committed, its Watch message received, and what the window then reads.
// It compares:
//   - reread: the window re-reads all tasks and wishes after each change (the hypothesis of Q63),
//   - incremental: the watch message brings the changed entities directly, re-reading wishes only on state changes.
func BenchmarkWishChange(b *testing.B) {
	for _, size := range bigwish.Sizes() {
		fix := getFixture(b, size)

		for _, done := range []bool{false, true} {
			kind := "progress"
			if done {
				kind = "done"
			}

			b.Run(fmt.Sprintf("%s/%s/reread", size.Name, kind), func(b *testing.B) {
				stream, err := fix.wishesClient.Watch(b.Context(), connect.NewRequest(&planv1.WishServiceWatchRequest{WishId: bigwish.WishID}))
				if err != nil {
					b.Fatal(err)
				}
				defer stream.Close()
				if !stream.Receive() {
					b.Fatal(stream.Err())
				}
				steps := 0
				var wireBytes int64
				b.ResetTimer()
				b.ReportAllocs()
				for b.Loop() {
					steps++
					t := fix.workTasks[steps%len(fix.workTasks)]
					t.LastLine = fmt.Sprintf("bench-change %d", steps)
					if done {
						if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE {
							t.Status = planv1.TaskStatus_TASK_STATUS_RUNNING
						} else {
							t.Status = planv1.TaskStatus_TASK_STATUS_DONE
						}
					}
					err := fix.db.Tx(b.Context(), func(tx *store.Tx) error {
						if err := tx.Journal("bench", "bench/progress", t); err != nil {
							return err
						}
						return tx.Put(t)
					})
					if err != nil {
						b.Fatal(err)
					}
					var msg *planv1.WishServiceWatchResponse
					for stream.Receive() {
						m := stream.Msg()
						if slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) {
							msg = m
							break
						}
					}
					if err := stream.Err(); err != nil || msg == nil {
						b.Fatalf("stream err: %v", err)
					}
					tasksRes, err := fix.tasksClient.List(b.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: bigwish.WishID}))
					if err != nil {
						b.Fatal(err)
					}
					wishesRes, err := fix.wishesClient.List(b.Context(), connect.NewRequest(&planv1.WishServiceListRequest{}))
					if err != nil {
						b.Fatal(err)
					}
					wireBytes += int64(proto.Size(msg) + proto.Size(tasksRes.Msg) + proto.Size(wishesRes.Msg))
				}
				b.ReportMetric(float64(wireBytes)/float64(b.N), "wire-B/op")
			})

			b.Run(fmt.Sprintf("%s/%s/incremental", size.Name, kind), func(b *testing.B) {
				stream, err := fix.wishesClient.Watch(b.Context(), connect.NewRequest(&planv1.WishServiceWatchRequest{WishId: bigwish.WishID}))
				if err != nil {
					b.Fatal(err)
				}
				defer stream.Close()
				if !stream.Receive() {
					b.Fatal(stream.Err())
				}
				steps := 0
				var wireBytes int64
				b.ResetTimer()
				b.ReportAllocs()
				for b.Loop() {
					steps++
					t := fix.workTasks[steps%len(fix.workTasks)]
					t.LastLine = fmt.Sprintf("bench-change %d", steps)
					if done {
						if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE {
							t.Status = planv1.TaskStatus_TASK_STATUS_RUNNING
						} else {
							t.Status = planv1.TaskStatus_TASK_STATUS_DONE
						}
					}
					err := fix.db.Tx(b.Context(), func(tx *store.Tx) error {
						if err := tx.Journal("bench", "bench/progress", t); err != nil {
							return err
						}
						return tx.Put(t)
					})
					if err != nil {
						b.Fatal(err)
					}
					var msg *planv1.WishServiceWatchResponse
					for stream.Receive() {
						m := stream.Msg()
						if slices.Contains(m.GetChanges(), planv1.Change_CHANGE_TASK) {
							msg = m
							break
						}
					}
					if err := stream.Err(); err != nil || msg == nil {
						b.Fatalf("stream err: %v", err)
					}
					if msg.GetChanged() == nil {
						b.Fatalf("%v: no changed tasks", msg)
					}
					wireBytes += int64(proto.Size(msg))
					if slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_WISH) {
						wishesRes, err := fix.wishesClient.List(b.Context(), connect.NewRequest(&planv1.WishServiceListRequest{}))
						if err != nil {
							b.Fatal(err)
						}
						wireBytes += int64(proto.Size(wishesRes.Msg))
					}
				}
				b.ReportMetric(float64(wireBytes)/float64(b.N), "wire-B/op")
			})
		}
	}
}
