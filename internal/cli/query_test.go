package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/query"
	"github.com/empowill/djinn/internal/store"
)

// queryServer serves the real QueryService over a store holding two tasks: W1, done, created on 1 November 2026 and
// costing 2 dollars; W2, running, created two days later, an old review. queries counts the calls to
// QueryService.Query.
func queryServer(t *testing.T) (srv *httptest.Server, queries *atomic.Int32) {
	t.Helper()
	s, err := store.Open(t.Context(), "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	day := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	err = s.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "/test.v1.TestService/Put", &planv1.ProjectServiceListRequest{}); err != nil {
			return err
		}
		for _, task := range []*planv1.Task{
			{Id: "01a00000-0000-7000-8000-000000000001", Code: "W1", Title: "Core", WishId: "w",
				Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: timestamppb.New(day),
				Usage: &planv1.Usage{CostUsd: 2}},
			{Id: "01a00000-0000-7000-8000-000000000002", Code: "W2", Title: "Docs", WishId: "w",
				Status: planv1.TaskStatus_TASK_STATUS_RUNNING, CreateTime: timestamppb.New(day.Add(48 * time.Hour)),
				Review: &planv1.TaskReview{}},
		} {
			if err := tx.Put(task); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	path, h, err := query.Handler(s)
	if err != nil {
		t.Fatal(err)
	}
	queries = &atomic.Int32{}
	mux := http.NewServeMux()
	mux.Handle(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/Query") {
			queries.Add(1)
		}
		h.ServeHTTP(w, r)
	}))
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, queries
}

// TestQueryTexts: djinn query query reads its texts on the caller's side, typed by the schema the server gives, and
// the server, which reads typed messages only, answers a text table; a text that does not read fails before the query.
func TestQueryTexts(t *testing.T) {
	t.Parallel()
	srv, _ := queryServer(t)
	run := func(args ...string) (int, string, string) {
		var out, errs bytes.Buffer
		code := Run(context.Background(), args, Config{
			Version: "test", Addr: srv.URL, HTTP: srv.Client(), Stdout: &out, Stderr: &errs, Getenv: noEnv,
		})
		return code, out.String(), errs.String()
	}
	for _, tt := range []struct {
		name     string
		args     []string
		wantCode int
		want     []string
		wantErr  string
	}{
		{"typed filters, a number and a time", []string{"query", "query", "tasks", "--select", "code",
			"--filter", "usage.cost_usd>1", "--filter", "create_time<2026-11-02T00:00:00Z"}, 0, []string{"W1", "1 row"}, ""},
		{"a kind read from an old review", []string{"query", "query", "tasks", "--select", "code",
			"--filter", "type=review"}, 0, []string{"W2", "1 row"}, ""},
		{"none of, sorted", []string{"query", "query", "tasks", "--select", "code", "--filter", "status!=running",
			"--sort", "-code"}, 0, []string{"W1", "1 row"}, ""},
		{"a group by day, counted", []string{"query", "query", "tasks", "--group-by", "create_time::day",
			"--measure", "n=count", "--format", "csv"}, 0, []string{"create_time::day,n"}, ""},
		{"a value of the wrong type", []string{"query", "query", "tasks", "--filter", "usage.cost_usd>a"}, 2, nil,
			"is not a number"},
		{"an unknown ref", []string{"query", "query", "tasks", "--filter", "nope=1"}, 2, nil, "no ref nope"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, out, errs := run(tt.args...)
			if code != tt.wantCode || !strings.Contains(errs, tt.wantErr) {
				t.Fatalf("code %d\nstdout:\n%s\nstderr:\n%s", code, out, errs)
			}
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("stdout lacks %q:\n%s", w, out)
				}
			}
		})
	}
}

// TestMCPQueryTexts: djinn mcp's query_query tool reads its texts as the command line does, typed by the schema the
// real QueryService gives, and answers the server's table; a text that does not read is a tool error, and the
// query is never asked.
func TestMCPQueryTexts(t *testing.T) {
	t.Parallel()
	srv, queries := queryServer(t)
	call := func(args string) (mcpCallResult, int32) {
		t.Helper()
		before := queries.Load()
		var out, errs bytes.Buffer
		in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"query_query",` +
			`"arguments":` + args + `}}` + "\n")
		cfg := Config{Version: "test", Addr: srv.URL, HTTP: srv.Client(), Stdin: in, Stdout: &out, Stderr: &errs,
			Getenv: noEnv}
		if code := Run(context.Background(), []string{"mcp"}, cfg); code != 0 {
			t.Fatalf("djinn mcp exited with %d: %s", code, errs.String())
		}
		var msg struct {
			Result mcpCallResult `json:"result"`
			Error  *rpcError     `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &msg); err != nil || msg.Error != nil || len(msg.Result.Content) != 1 {
			t.Fatalf("answer %q: %v", out.String(), err)
		}
		return msg.Result, queries.Load() - before
	}
	for _, tt := range []struct {
		name    string
		args    string
		wantErr bool
		want    []string
		lacks   []string
	}{
		{"typed filters, a number and a time",
			`{"collection":"tasks","select":"code","filter":["usage.cost_usd>1","create_time<2026-11-02T00:00:00Z"]}`,
			false, []string{"W1", "1 row"}, []string{"W2"}},
		{"a kind read from an old review", `{"collection":"tasks","select":["code"],"filter":"type=review"}`,
			false, []string{"W2", "1 row"}, []string{"W1"}},
		{"sorted, descending", `{"collection":"tasks","select":"code","sort":"-code","format":"csv"}`,
			false, []string{"code\nW2\nW1\n"}, nil},
		{"a group by day, counted",
			`{"collection":"tasks","group_by":"create_time::day","measure":"n=count","format":"csv"}`,
			false, []string{"create_time::day,n\n", "Z,1\n"}, []string{",2\n"}},
		{"a value of the wrong type", `{"collection":"tasks","filter":"usage.cost_usd>a"}`,
			true, []string{"is not a number"}, nil},
		{"an unknown ref", `{"collection":"tasks","filter":"nope=1"}`, true, []string{"no ref nope"}, nil},
		{"bins on a text ref", `{"collection":"tasks","group_by":"code::bins(1,2)","measure":"n=count"}`,
			true, []string{"has no bins"}, nil},
		{"an empty sort", `{"collection":"tasks","sort":""}`, true, []string{"no ref"}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res, asked := call(tt.args)
			text := res.Content[0].Text
			if res.IsError != tt.wantErr {
				t.Fatalf("isError %v, want %v: %s", res.IsError, tt.wantErr, text)
			}
			if want := map[bool]int32{false: 1, true: 0}[tt.wantErr]; asked != want {
				t.Errorf("QueryService.Query asked %d times, want %d: %s", asked, want, text)
			}
			for _, w := range tt.want {
				if !strings.Contains(text, w) {
					t.Errorf("answer lacks %q:\n%s", w, text)
				}
			}
			for _, l := range tt.lacks {
				if strings.Contains(text, l) {
					t.Errorf("answer holds %q:\n%s", l, text)
				}
			}
		})
	}
}
