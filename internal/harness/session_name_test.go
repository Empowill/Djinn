package harness

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestSessionUnkeptSaid (W35): a Claude worker whose folder would name its folder of transcripts longer than the file
// system of its sign-in's folder takes says so at its start, with the length and the limit, as Claude Code writes no
// transcript there and says nothing (W30); a folder within the limit, or a limit unknown, says nothing.
func TestSessionUnkeptSaid(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	for _, tc := range []struct {
		name  string
		limit int
		said  bool
	}{
		{"a name longer than the file system takes", 20, true},
		{"a name within the limit", 4096, false},
		{"a limit unknown", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var asked []string
			e := up(t, t.TempDir(), WithNameMax(func(path string) int {
				mu.Lock()
				defer mu.Unlock()
				asked = append(asked, path)
				return tc.limit
			}))
			wishID, _ := e.wish(t, t.TempDir())
			task := e.spawnClaude(t, wishID)
			e.ended(t, task.GetId())
			events, err := store.List[*planv1.TaskEvent](t.Context(), e.db, store.Where{"task_id": task.GetId()})
			if err != nil {
				t.Fatal(err)
			}
			var start string
			for _, ev := range events {
				if strings.HasPrefix(ev.GetText(), "started ") {
					start = ev.GetText()
					break
				}
			}
			if start == "" {
				t.Fatalf("no start event: %v", events)
			}
			if got := strings.Contains(start, "its session not kept: the folder of its transcripts would be named in "); got != tc.said {
				t.Errorf("start %q says its session is not kept: %v, want %v", start, got, tc.said)
			}
			if tc.said && !strings.Contains(start, "takes 20 at most") {
				t.Errorf("start %q does not name the limit, 20", start)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(asked) == 0 || filepath.Base(asked[0]) != "projects" {
				t.Errorf("limit asked of %v, want the sign-in's folder of projects", asked)
			}
		})
	}
}
