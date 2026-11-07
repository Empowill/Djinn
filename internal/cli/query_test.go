package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/query"
	"github.com/empowill/djinn/internal/store"
)

// TestQueryTexts: djinn query query reads its texts on the caller's side, typed by the schema the server gives, and
// the server, which reads typed messages only, answers a text table; a text that does not read fails before the query.
func TestQueryTexts(t *testing.T) {
	t.Parallel()
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
	mux := http.NewServeMux()
	mux.Handle(path, h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
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
