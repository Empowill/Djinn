package harness

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// leadListener records the paragraphs Djinn tells the leads.
type leadListener struct {
	mu         sync.Mutex
	paragraphs []string
}

func (l *leadListener) tell(_ context.Context, wishID, paragraph string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.paragraphs = append(l.paragraphs, paragraph)
	return nil
}

func (l *leadListener) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.paragraphs)
}

// TestTellLeads verifies the paragraphs Djinn tells the wish's lead when workers fail or cannot be resumed.
func TestTellLeads(t *testing.T) {
	t.Parallel()

	t.Run("worker_fails", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir(), WithTellDelay(10*time.Millisecond))
		lead := &leadListener{}
		e.h.TellLeads(lead.tell)
		wishID, _ := e.wish(t, fakeProject(t, ""))

		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: "Build parser", Prompt: "fail syntax error on line 42\nmore details here",
			Provider: planv1.Provider_PROVIDER_FAKE, Model: "fake-model",
		}))
		if err != nil {
			t.Fatal(err)
		}
		task := res.Msg.GetTask()
		e.watch(t.Context(), t, task.GetId(), 0)
		e.h.FlushTell(t.Context())

		paras := lead.all()
		if len(paras) != 1 {
			t.Fatalf("told %d paragraphs, want 1: %v", len(paras), paras)
		}
		want := fmt.Sprintf("Djinn: %s (Build parser) failed (fake, fake-model): syntax error on line 42. The move is yours.", task.GetCode())
		if paras[0] != want {
			t.Errorf("told %q, want %q", paras[0], want)
		}
	})

	t.Run("batch_paragraph", func(t *testing.T) {
		t.Parallel()
		// The window never closes on its own here: FlushTell closes it, once both workers have failed. With a window of
		// 500 ms, a slow runner (Windows CI) let it close between the two failures: two paragraphs.
		e := up(t, t.TempDir(), WithTellDelay(time.Hour))
		lead := &leadListener{}
		e.h.TellLeads(lead.tell)
		wishID, _ := e.wish(t, fakeProject(t, ""))

		// W2 starts once W1 has failed: however far apart they fail, both lines come in one paragraph.
		t1 := e.spawn(t, wishID, "fail error one\nline 2")
		e.watch(t.Context(), t, t1.GetId(), 0)
		t2 := e.spawn(t, wishID, "fail error two\nline 2")
		e.watch(t.Context(), t, t2.GetId(), 0)

		e.h.FlushTell(t.Context())

		paras := lead.all()
		if len(paras) != 1 {
			t.Fatalf("told %d paragraphs, want 1: %v", len(paras), paras)
		}
		lines := strings.Split(paras[0], "\n")
		if len(lines) != 2 {
			t.Fatalf("got %d lines in paragraph, want 2: %q", len(lines), paras[0])
		}
		want1 := fmt.Sprintf("Djinn: %s (Try the harness) failed (fake): error one. The move is yours.", t1.GetCode())
		want2 := fmt.Sprintf("Djinn: %s (Try the harness) failed (fake): error two. The move is yours.", t2.GetCode())
		if !slices.Contains(lines, want1) || !slices.Contains(lines, want2) {
			t.Errorf("lines %v do not contain both %q and %q", lines, want1, want2)
		}
	})

	for _, tc := range []struct {
		name     string
		attempts int
		prompt   string
		tail     string
	}{
		{"correction_worker_failing", 2, "fail I cannot settle it\nmore details", "A correction worker started."},
		{"correction_worker_attempts_spent", 1, "fail I cannot settle it", "The move is yours."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testx.Portable(t)
			t.Parallel()
			in := integrating(t)
			writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
				fmt.Sprintf("provider: PROVIDER_FAKE\ngenerated: \"gen/**\"\ngenerate: \"gen\"\ntest: \"test\"\ncorrection_attempts: %d\n", tc.attempts))
			in.correctWith(func(string, string) string { return tc.prompt })
			lead := &leadListener{}
			in.h.TellLeads(lead.tell)

			in.finished(t, "W1", map[string]string{"app/README.md": "# One\n"})
			w2 := in.finished(t, "W2", map[string]string{"app/README.md": "# Two\n"})

			in.pass(t, 0) // W2 conflicts with W1, gone in before it.
			w3 := in.correction(t, w2)
			if w3.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
				t.Fatalf("W3 %v", w3)
			}
			in.h.FlushTell(t.Context())

			paras := lead.all()
			var found bool
			want := fmt.Sprintf("Djinn: %s (%s), its correction worker (served %s), failed (fake): I cannot settle it. %s",
				w3.GetCode(), w3.GetTitle(), w2.GetCode(), tc.tail)
			for _, p := range paras {
				for _, l := range strings.Split(p, "\n") {
					if l == want {
						found = true
					}
					if strings.HasPrefix(l, fmt.Sprintf("Djinn: %s ", w2.GetCode())) && strings.Contains(l, "failed") {
						t.Errorf("lead heard of DONE worker %s as failed: %q", w2.GetCode(), l)
					}
				}
			}
			if !found {
				t.Errorf("did not find %q in %v", want, paras)
			}
		})
	}

	t.Run("interrupted_worktree_gone", func(t *testing.T) {
		testx.Portable(t)
		t.Parallel()
		home := t.TempDir()
		e := up(t, home)
		wishID, projectID := e.wish(t, gitRepo(t))
		task := &planv1.Task{
			Id: store.NewID(), WishId: wishID, ProjectId: projectID, Code: "W1", Title: "Build parser",
			Status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED, Provider: planv1.Provider_PROVIDER_FAKE,
			Worktree: filepath.Join(t.TempDir(), "non-existent"), SessionId: "s-W1", Scheduled: true,
			Error: "djinn up ended while the worker ran", StartTime: timestamppb.Now(), EndTime: timestamppb.Now(),
		}
		putTask(t, e.db, task, "text first prompt")
		e.down()

		lead := &leadListener{}
		e = up(t, home, WithTellDelay(10*time.Millisecond))
		e.h.TellLeads(lead.tell)
		e.h.FlushTell(t.Context())

		paras := lead.all()
		want := "Djinn: W1 (Build parser) failed (fake): its worktree is gone: it cannot be resumed. The move is yours."
		var found bool
		for _, p := range paras {
			for _, l := range strings.Split(p, "\n") {
				if l == want {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("did not find %q in %v", want, paras)
		}
	})
}
