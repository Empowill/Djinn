package plan

import (
	"path/filepath"
	"slices"
	"strings"
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
			n := res.Msg.GetNotices()[i]
			if n.GetKind() != planv1.ProjectNoticeKind_PROJECT_NOTICE_KIND_TO_FIX || n.GetSubject() != dir {
				t.Errorf("notice %v, want a thing to fix on %s", noticeLines([]*planv1.ProjectNotice{n}), dir)
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
