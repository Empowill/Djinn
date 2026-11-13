package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestSessionNotices (W35): the project view says, among the things to fix, that Claude Code cannot keep the sessions
// of the project's workers when the name of their folder of transcripts, made of the folder they run in, is longer
// than the file system of Claude Code's folder takes; a folder within the limit, a limit unknown, or a project of
// another agent says nothing.
func TestSessionNotices(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		limit    int
		settings string
		want     bool
	}{
		{"a name longer than the file system takes", 20, "", true},
		{"a name within the limit", 4096, "", false},
		{"a limit unknown", 0, "", false},
		{"another agent's project", 20, "provider: PROVIDER_CODEX\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			c := serve(t, WithHome(t.TempDir()), WithNameMax(func(string) int { return tc.limit }))
			dir := t.TempDir()
			if tc.settings != "" {
				writeFileAt(t, filepath.Join(dir, SettingsFile), tc.settings)
			}
			add, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
			if err != nil {
				t.Fatal(err)
			}
			res, err := c.projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{
				Project: add.Msg.GetProject().GetName(),
			}))
			if err != nil {
				t.Fatal(err)
			}
			i := slices.IndexFunc(res.Msg.GetNotices(), func(n *planv1.ProjectNotice) bool {
				return n.GetTopic() == planv1.ProjectNoticeTopic_PROJECT_NOTICE_TOPIC_SESSION
			})
			if got := i >= 0; got != tc.want {
				t.Fatalf("notices %v: a session notice %v, want %v", noticeLines(res.Msg.GetNotices()), got, tc.want)
			}
			if !tc.want {
				return
			}
			// The project keeps its folder with its links resolved, as a worker's process sees it: /private/var for
			// macOS's /var, a long name for a short one on Windows.
			want := add.Msg.GetProject().GetDirectory()
			n := res.Msg.GetNotices()[i]
			if n.GetKind() != planv1.ProjectNoticeKind_PROJECT_NOTICE_KIND_TO_FIX || n.GetSubject() != want {
				t.Errorf("notice %v, want a thing to fix on %s", noticeLines([]*planv1.ProjectNotice{n}), want)
			}
			if !strings.Contains(n.GetDetail(), "takes 20 at most") || !strings.HasPrefix(n.GetText(), "Claude Code cannot save") {
				t.Errorf("notice %q, detail %q: want the sentence and the limit", n.GetText(), n.GetDetail())
			}
			if res.Msg.GetProject().GetToFix() != 1 {
				t.Errorf("to fix %d, want 1", res.Msg.GetProject().GetToFix())
			}
		})
	}
}

// TestSessionNoticesGit (W38): in a Git project a worker runs in a worktree under Djinn's data folder, not in the
// project's folder: the name checked is the one a worktree's path makes, the notice is on that worktree and asks to
// move Djinn's data folder; a limit one short of that name says it, a limit of its length says nothing.
func TestSessionNoticesGit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		short int
		want  bool
	}{
		{"a limit one short of the worktree's name", 1, true},
		{"a limit as long as the worktree's name", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			home := t.TempDir()
			var limit atomic.Int64
			var asked sync.Map
			c := serve(t, WithHome(home), WithNameMax(func(dir string) int {
				asked.Store(dir, true)
				return int(limit.Load())
			}))
			repo := filepath.Join(t.TempDir(), "repo")
			if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
				t.Fatal(err)
			}
			add, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: repo}))
			if err != nil {
				t.Fatal(err)
			}
			p := add.Msg.GetProject()
			if !p.GetGit() {
				t.Fatalf("project %v, want in Git", p)
			}
			worktree := filepath.Join(home, "projects", p.GetId(), "worktrees", sampleTaskID)
			n := ClaudeSlugLen(worktree)
			if n <= ClaudeSlugLen(p.GetDirectory()) {
				t.Fatalf("worktree name %d, project's %d: the test needs the worktree's longer", n, ClaudeSlugLen(p.GetDirectory()))
			}
			limit.Store(int64(n - tc.short))
			res, err := c.projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{Project: p.GetName()}))
			if err != nil {
				t.Fatal(err)
			}
			asked.Range(func(k, _ any) bool {
				if filepath.Base(k.(string)) != "projects" {
					t.Errorf("limit measured on %s, want a sign-in folder's projects folder", k)
				}
				return true
			})
			i := slices.IndexFunc(res.Msg.GetNotices(), func(n *planv1.ProjectNotice) bool {
				return n.GetTopic() == planv1.ProjectNoticeTopic_PROJECT_NOTICE_TOPIC_SESSION
			})
			if got := i >= 0; got != tc.want {
				t.Fatalf("limit %d, name %d: notices %v, a session notice %v, want %v", n-tc.short, n,
					noticeLines(res.Msg.GetNotices()), got, tc.want)
			}
			if !tc.want {
				return
			}
			s := res.Msg.GetNotices()[i]
			if s.GetSubject() != worktree {
				t.Errorf("notice on %s, want the worktree %s", s.GetSubject(), worktree)
			}
			if !strings.Contains(s.GetText(), "Move Djinn's data folder") {
				t.Errorf("notice %q, want it to move Djinn's data folder", s.GetText())
			}
			if want := fmt.Sprintf("named in %d characters", n); !strings.Contains(s.GetDetail(), "A worker runs in "+worktree+":") ||
				!strings.Contains(s.GetDetail(), want) {
				t.Errorf("detail %q, want the worktree and %q", s.GetDetail(), want)
			}
		})
	}
}

// TestClaudeSlugLen: the name of a folder of transcripts is as long as its folder's path, as JavaScript counts, and
// past 200, Claude Code cuts it to 200, a dash and a hash of six at most.
func TestClaudeSlugLen(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		dir  string
		want int
	}{
		{"/home/a/b", 9},
		{"/home/\u00e9", 7},
		{"/home/\U0001F600", 8},
		{"/" + strings.Repeat("a", 199), 200},
		{"/" + strings.Repeat("a", 300), 207},
	} {
		if got := ClaudeSlugLen(tc.dir); got != tc.want {
			t.Errorf("ClaudeSlugLen(%q) = %d, want %d", tc.dir, got, tc.want)
		}
	}
}
