package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/gitx"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestCLIAnswersWhileFetchHangs is the incident W504: djinn up stopped answering its command line for minutes. While
// the integration's fetch of main hangs, as an ssh to a remote that stalls and keeps its output open, the calls the
// lead makes answer within their budget on a wish whose task holds thousands of events; the fetch is killed at its
// deadline, and the integration says why it holds main.
func TestCLIAnswersWhileFetchHangs(t *testing.T) {
	testx.Portable(t)
	if runtime.GOOS == "windows" {
		t.Skip("the hanging git is a shell script")
	}
	u := keepingUp(t, "")
	ctx := t.Context()
	worked := &planv1.Task{
		Id: store.NewID(), WishId: u.wishID, ProjectId: u.projectID, Code: "W1", Title: "Long work",
		Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: timestamppb.New(u.now),
		StartTime: timestamppb.New(u.now), FirstStartTime: timestamppb.New(u.now), EndTime: timestamppb.New(u.now.Add(time.Hour)),
		Runs: []*planv1.TaskRun{{StartTime: timestamppb.New(u.now), EndTime: timestamppb.New(u.now.Add(time.Hour))}},
	}
	err := u.db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/put", worked); err != nil {
			return err
		}
		if err := tx.Put(worked); err != nil {
			return err
		}
		line := strings.Repeat("a line a worker wrote, ", 40)
		for seq := int64(1); seq <= 300; seq++ {
			ev := &planv1.TaskEvent{
				Id: store.NewID(), TaskId: worked.GetId(), Seq: seq, Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT,
				Text: line, CreateTime: timestamppb.New(u.now.Add(time.Duration(seq) * time.Second)),
			}
			if err := tx.Put(ev); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	fetching := filepath.Join(bin, "fetching")
	script := fmt.Sprintf(`#!/bin/sh
for a in "$@"; do
	case "$a" in fetch) touch %q; sleep 30 & wait; exit 1;; esac
done
exec %q "$@"
`, fetching, realGit)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	network, waitDelay := gitx.Network, gitx.WaitDelay
	gitx.Network, gitx.WaitDelay = 350*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { gitx.Network, gitx.WaitDelay = network, waitDelay })

	passed := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		u.h.integratePass(ctx)
		passed <- time.Since(start)
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(fetching); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the integration never fetched main")
		}
	}

	const budget = time.Second
	questions := planv1connect.NewQuestionServiceClient(u.srv.Client(), u.srv.URL)
	calls := []struct {
		name string
		call func(context.Context) error
	}{
		{"task list", func(ctx context.Context) error {
			_, err := u.tasks.List(ctx, connect.NewRequest(&planv1.TaskServiceListRequest{WishId: u.wishID}))
			return err
		}},
		{"wish list", func(ctx context.Context) error {
			_, err := u.wishes.List(ctx, connect.NewRequest(&planv1.WishServiceListRequest{}))
			return err
		}},
		{"project list", func(ctx context.Context) error {
			_, err := u.env.project.List(ctx, connect.NewRequest(&planv1.ProjectServiceListRequest{}))
			return err
		}},
		{"question ask", func(ctx context.Context) error {
			_, err := questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
				WishId: u.wishID, Text: "Is djinn still there?", Options: []string{"Yes"},
			}))
			return err
		}},
	}
	for _, c := range calls {
		callCtx, cancel := context.WithTimeout(ctx, budget)
		start := time.Now()
		err := c.call(callCtx)
		cancel()
		if err != nil {
			t.Errorf("%s while the fetch hangs: %v after %v (budget %v)", c.name, err, time.Since(start), budget)
		}
	}
	if _, err := os.Stat(fetching); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-passed:
		if d > gitx.Network+5*time.Second {
			t.Errorf("the integration's pass took %v past a fetch killed at %v", d, gitx.Network)
		}
	case <-time.After(gitx.Network + 10*time.Second):
		t.Fatal("the integration's pass still waits on a fetch past its deadline")
	}
	if held := u.mainState(t).GetHeld(); !strings.HasPrefix(held, "fetch ") {
		t.Errorf("main held = %q, want the fetch that failed", held)
	}
}
