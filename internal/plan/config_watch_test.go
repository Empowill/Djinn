package plan

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// lineTexts are the sentences of lines, a mark before those that ask.
func lineTexts(lines []*planv1.ConfigChangeLine) []string {
	var out []string
	for _, l := range lines {
		text := l.GetText()
		if l.GetAsks() {
			text = "! " + text
		}
		out = append(out, text)
	}
	return out
}

// configFolder is a folder laid out as a project's, holding files.
func configFolder(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		writeFileAt(t, filepath.Join(dir, filepath.FromSlash(name)), content)
	}
	return dir
}

// skillMD is a SKILL.md whose metadata.djinn holds djinn, indented under it.
func skillMD(description, djinn, body string) string {
	front := "---\nname: s\ndescription: " + description + "\n"
	if djinn != "" {
		front += "metadata:\n  djinn:\n" + djinn
	}
	return front + "---\n" + body
}

// TestConfigLines (W151): the difference between two configurations, told in sentences: what gives the workers more,
// or changes what a skill makes or runs for Djinn, asks for your look; the rest is applied without asking.
func TestConfigLines(t *testing.T) {
	template := "    wish:\n      title: Babysit PR {pr}\n      match: 'PR #(?P<pr>\\d+)'\n      watch: sh watch.sh {pr}\n"
	source := "    source:\n      watch: sh inbox.sh\n"
	old := configFolder(t, map[string]string{
		".agents/settings.txtpb": "setup: \"npm ci\"\nmodel: \"a\"\n" +
			"checks { name: \"lint\" command: \"go tool task lint\" when: CHECK_WHEN_COMMIT }\n" +
			"checks { name: \"old\" command: \"make old\" when: CHECK_WHEN_PUSH }\n",
		".agents/permissions.txtpb":          "commands: \"make\"\ncommands: \"go test\"\n",
		".agents/skills/recette/SKILL.md":    skillMD("Recette.", "", "Do it.\n"),
		".agents/skills/gone/SKILL.md":       skillMD("Gone.", "", ""),
		".agents/skills/watcher/SKILL.md":    skillMD("Watch.", template, ""),
		".agents/skills/inbox/SKILL.md":      skillMD("Inbox.", source, ""),
		".agents/skills/quiet/SKILL.md":      skillMD("Quiet.", source, ""),
		".agents/notes.md":                   "one\n",
		".claude/skills/claude-one/SKILL.md": skillMD("One.", "", ""),
	})
	newer := configFolder(t, map[string]string{
		".agents/settings.txtpb": "setup: \"npm ci\"\nmodel: \"b\"\n" +
			"checks { name: \"lint\" command: \"go tool task lint\" when: CHECK_WHEN_COMMIT when: CHECK_WHEN_PUSH }\n" +
			"checks { name: \"e2e\" command: \"go tool task e2e\" when: CHECK_WHEN_PUSH }\n",
		".agents/permissions.txtpb":          "commands: \"make\"\ncommands: \"curl\"\nedit: true\n",
		".agents/skills/recette/SKILL.md":    skillMD("Recette.", "", "Do it twice.\n"),
		".agents/skills/recette/run.sh":      "echo\n",
		".agents/skills/watcher/SKILL.md":    skillMD("Watch.", strings.ReplaceAll(template, "watch.sh", "watch2.sh"), ""),
		".agents/skills/inbox/SKILL.md":      skillMD("Inbox.", strings.ReplaceAll(source, "inbox.sh", "inbox2.sh"), ""),
		".agents/skills/quiet/SKILL.md":      skillMD("Quiet.", strings.ReplaceAll(source, "inbox.sh", "inbox2.sh"), ""),
		".agents/skills/open-pr/SKILL.md":    skillMD("Opens.", template, ""),
		".agents/skills/feed/SKILL.md":       skillMD("Feeds.", source, ""),
		".agents/notes.md":                   "two\n",
		".claude/skills/claude-one/SKILL.md": skillMD("One.", "", ""),
	})
	plugged := func(skill string) bool { return skill == "inbox" }
	got := lineTexts(configLines(old, newer, plugged))
	want := []string{
		"The check lint changes: go tool task lint, before a commit and a push.",
		"New check e2e before a push: go tool task e2e.",
		"The check old is gone.",
		"The team's settings change model.",
		"! Permissions: 1 more command the workers may run: curl.",
		"Permissions: 1 command fewer: go test.",
		"! Permissions: the workers may now edit the project's files.",
		"New skill feed, with an inbox source.",
		"! The inbox source of the skill inbox, plugged in here, changes its watch.",
		"! New skill open-pr, with a wish template (Babysit PR {pr}).",
		"The inbox source of the skill quiet changes its watch.",
		"The skill recette changes its instructions and its files run.sh.",
		"! The wish template of the skill watcher changes its watch.",
		"The skill gone is gone.",
		"Other files of .agents change: .agents/notes.md.",
	}
	if !slices.Equal(got, want) {
		t.Errorf("lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Files that appear and go.
	empty := t.TempDir()
	if got := lineTexts(configLines(empty, old, plugged)); !slices.Contains(got, "The team's settings file appears.") ||
		!slices.Contains(got, "! Permissions: 2 more commands the workers may run: make, go test.") {
		t.Errorf("from nothing: %q", got)
	}
	if got := lineTexts(configLines(old, empty, plugged)); !slices.Contains(got,
		"The team's settings file is gone: Djinn's defaults apply, and no check runs.") ||
		!slices.Contains(got, "The skill inbox is gone: its inbox source, plugged in here, no longer runs.") {
		t.Errorf("to nothing: %q", got)
	}
	broken := configFolder(t, map[string]string{".agents/settings.txtpb": "nope {", ".agents/skills/recette/SKILL.md": "---\nmetadata: [\n---\n"})
	if got := lineTexts(configLines(old, broken, plugged)); !slices.Contains(got,
		"Djinn can no longer read the team's settings: the project's tasks do not spawn until the file is fixed.") ||
		!slices.Contains(got, "Djinn can no longer read the skill recette: it ignores its wish template, inbox source and babysit.") {
		t.Errorf("unreadable: %q", got)
	}
	if got := configLines(old, old, plugged); len(got) != 0 {
		t.Errorf("the same configuration: %q", lineTexts(got))
	}
}

// watchEnv is a project added to a served Djinn, in Git with an origin whose main is fetched, and a look at it.
type watchEnv struct {
	t       *testing.T
	c       clients
	home    string
	project *planv1.Project
	fetched []string
	fail    error
	clock   time.Time
}

func newWatchEnv(t *testing.T, files map[string]string) *watchEnv {
	t.Helper()
	e := &watchEnv{t: t, home: t.TempDir(), clock: time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)}
	e.c = serve(t, WithHome(e.home))
	dir := configProject(t, files).GetDirectory()
	res, err := e.c.projects.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
	if err != nil {
		t.Fatal(err)
	}
	e.project = res.Msg.GetProject()
	return e
}

// commit commits files in the project's repository, and moves origin/main to it, as a fetch would.
func (e *watchEnv) commit(files map[string]string) string {
	e.t.Helper()
	for name, content := range files {
		writeFileAt(e.t, filepath.Join(e.project.GetDirectory(), filepath.FromSlash(name)), content)
	}
	return commitAs(e.t, e.project.GetDirectory(), "refs/remotes/origin/main")
}

func (e *watchEnv) look() {
	e.t.Helper()
	e.clock = e.clock.Add(ConfigLookEvery)
	fetch := func(_ context.Context, folder, ref string) error {
		e.fetched = append(e.fetched, ref)
		return e.fail
	}
	if err := LookAtConfig(e.t.Context(), e.c.store, e.home, e.project, fetch, e.clock); err != nil {
		e.t.Fatal(err)
	}
}

func (e *watchEnv) show() *planv1.ProjectServiceShowResponse {
	e.t.Helper()
	res, err := e.c.projects.Show(e.t.Context(), connect.NewRequest(&planv1.ProjectServiceShowRequest{Project: e.project.GetName()}))
	if err != nil {
		e.t.Fatal(err)
	}
	return res.Msg
}

func (e *watchEnv) writes() int {
	e.t.Helper()
	commands, err := store.Commands(e.t.Context(), e.c.store, func(c store.Command) bool { return c.Method == methodConfigLook })
	if err != nil {
		e.t.Fatal(err)
	}
	return len(commands)
}

// TestLookAtConfig (W151): the watch fetches the reference branch, keeps its digest, writes nothing while the
// configuration has not moved, and tells each commit that changed it in the journal of changes. The first look tells
// the last commits as history; a permission given asks for your look, which lights the project until it is marked
// seen; a new source asks through its own notice.
func TestLookAtConfig(t *testing.T) {
	e := newWatchEnv(t, map[string]string{".agents/settings.txtpb": "setup: \"npm ci\"\n", "README.md": "# App\n"})
	e.look()
	shown := e.show()
	if !slices.Equal(e.fetched, []string{"refs/remotes/origin/main"}) {
		t.Errorf("fetched %q", e.fetched)
	}
	w := shown.GetWatch()
	if w.GetRef() != "origin/main" || w.GetCommit() == "" || w.GetDigest() == "" || w.GetFetchError() != "" {
		t.Fatalf("watch after the first look: %v", w)
	}
	if len(shown.GetChanges()) != 1 || !shown.GetChanges()[0].GetFirstLook() ||
		!slices.Equal(lineTexts(shown.GetChanges()[0].GetLines()), []string{"The team's settings file appears.",
			"The setup is now npm ci."}) {
		t.Fatalf("first look: %v", shown.GetChanges())
	}
	if shown.GetProject().GetToFix() != 0 {
		t.Errorf("the history asks nothing: to fix %d", shown.GetProject().GetToFix())
	}

	// Nothing moved: nothing written.
	writes := e.writes()
	e.look()
	if e.writes() != writes {
		t.Error("a look that found nothing wrote")
	}
	// Another file moved the branch, not the configuration: the commit kept, no entry.
	sha := e.commit(map[string]string{"README.md": "# Other\n"})
	e.look()
	if shown := e.show(); shown.GetWatch().GetCommit() != sha || len(shown.GetChanges()) != 1 {
		t.Errorf("a commit outside the configuration: watch %v, %d changes", shown.GetWatch(), len(shown.GetChanges()))
	}

	// A permission given asks for your look.
	sha = e.commit(map[string]string{".agents/permissions.txtpb": "commands: \"curl\"\n"})
	e.look()
	shown = e.show()
	last := shown.GetChanges()[0]
	if last.GetCommit() != sha || last.GetFirstLook() || last.GetAuthor() != "t" || last.GetTitle() != "config" ||
		!slices.Equal(lineTexts(last.GetLines()), []string{"! Permissions: 1 more command the workers may run: curl."}) {
		t.Fatalf("a permission given: %v", last)
	}
	if shown.GetProject().GetToFix() != 1 || !strings.Contains(shown.GetProject().GetFirstFix(), "waits for your look") {
		t.Errorf("to fix %d %q", shown.GetProject().GetToFix(), shown.GetProject().GetFirstFix())
	}
	notice := shown.GetNotices()[0]
	if notice.GetTopic() != topicChange || notice.GetSubject() != last.GetId() ||
		notice.GetCommand() != "djinn project acknowledge "+e.project.GetName()+" "+sha[:7] {
		t.Errorf("the notice: %v", notice)
	}

	// Marked seen by the start of its commit: the gear goes out.
	if _, err := e.c.projects.Acknowledge(t.Context(), connect.NewRequest(&planv1.ProjectServiceAcknowledgeRequest{
		Project: e.project.GetName(), Change: "abc",
	})); code(err) != connect.CodeInvalidArgument {
		t.Errorf("a change named by 3 characters: %v", err)
	}
	if _, err := e.c.projects.Acknowledge(t.Context(), connect.NewRequest(&planv1.ProjectServiceAcknowledgeRequest{
		Project: e.project.GetName(), Change: "0000000",
	})); code(err) != connect.CodeNotFound {
		t.Errorf("a change the journal lacks: %v", err)
	}
	res, err := e.c.projects.Acknowledge(t.Context(), connect.NewRequest(&planv1.ProjectServiceAcknowledgeRequest{
		Project: e.project.GetName(), Change: sha[:7],
	}))
	if err != nil || len(res.Msg.GetChanges()) != 1 || res.Msg.GetChanges()[0].GetSeenTime() == nil {
		t.Fatalf("acknowledge: %v %v", res, err)
	}
	if shown := e.show(); shown.GetProject().GetToFix() != 0 || shown.GetChanges()[0].GetSeenTime() == nil {
		t.Errorf("after acknowledge: to fix %d", shown.GetProject().GetToFix())
	}

	// A new source asks through its own notice, not its change's.
	e.commit(map[string]string{".agents/skills/feed/SKILL.md": skillMD("Feeds.", "    source:\n      watch: sh inbox.sh\n", "")})
	e.look()
	shown = e.show()
	if got := lineTexts(shown.GetChanges()[0].GetLines()); !slices.Equal(got, []string{"New skill feed, with an inbox source."}) {
		t.Errorf("a new source: %q", got)
	}
	if shown.GetProject().GetToFix() != 1 || shown.GetNotices()[0].GetTopic() != topicSource {
		t.Errorf("a new source: %v", noticeLines(shown.GetNotices()))
	}

	// A failed fetch is kept, and the look goes on with what the copy holds.
	e.fail = errors.New("fetch main from origin: could not read Username")
	e.look()
	if got := e.show().GetWatch().GetFetchError(); got != e.fail.Error() {
		t.Errorf("fetch error %q", got)
	}
	e.fail = nil
	e.look()
	if got := e.show().GetWatch().GetFetchError(); got != "" {
		t.Errorf("fetch error after a good fetch: %q", got)
	}
}

// TestLookAtConfigManyCommits (W151): past configCommitsMax commits in one look, the older ones are told together,
// from the last look to the oldest told alone.
func TestLookAtConfigManyCommits(t *testing.T) {
	e := newWatchEnv(t, map[string]string{".agents/permissions.txtpb": "commands: \"c0\"\n"})
	e.look()
	n := configCommitsMax + 3
	for i := 1; i <= n; i++ {
		e.commit(map[string]string{".agents/permissions.txtpb": fmt.Sprintf("commands: \"c%d\"\n", i)})
	}
	e.look()
	changes, err := ProjectChanges(t.Context(), e.c.store, e.project.GetId())
	if err != nil {
		t.Fatal(err)
	}
	var alone, together []*planv1.ConfigChange
	for _, c := range changes {
		switch {
		case c.GetFirstLook():
		case c.GetCommits() == 1:
			alone = append(alone, c)
		default:
			together = append(together, c)
		}
	}
	if len(alone) != configCommitsMax || len(together) != 1 || together[0].GetCommits() != 3 {
		t.Fatalf("%d alone, together %v", len(alone), together)
	}
	if got := lineTexts(together[0].GetLines()); !slices.Equal(got, []string{
		"! Permissions: 1 more command the workers may run: c3.", "Permissions: 1 command fewer: c0.",
	}) {
		t.Errorf("told together: %q", got)
	}
	if got := e.show().GetProject().GetToFix(); got != int32(configCommitsMax+1) {
		t.Errorf("to fix %d", got)
	}
	if _, err := e.c.projects.Acknowledge(t.Context(), connect.NewRequest(&planv1.ProjectServiceAcknowledgeRequest{
		Project: e.project.GetName(),
	})); err != nil {
		t.Fatal(err)
	}
	if got := e.show().GetProject().GetToFix(); got != 0 {
		t.Errorf("after acknowledging all: to fix %d", got)
	}
}

// TestLookAtConfigBranchChange (W151): when the reference branch itself changes, one entry tells the whole difference,
// and the watch fetches the new branch.
func TestLookAtConfigBranchChange(t *testing.T) {
	e := newWatchEnv(t, map[string]string{".agents/settings.txtpb": "setup: \"npm ci\"\n"})
	e.look()
	dir := e.project.GetDirectory()
	gitIn(t, dir, "checkout", "-q", "-b", "dev")
	e.commit(map[string]string{".agents/settings.txtpb": "setup: \"make\"\n"})
	gitIn(t, dir, "update-ref", "refs/remotes/origin/dev", "HEAD")
	if err := SaveDeveloperMainBranch(e.home, e.project.GetId(), "dev"); err != nil {
		t.Fatal(err)
	}
	e.fetched = nil
	e.look()
	if !slices.Equal(e.fetched, []string{"refs/remotes/origin/dev"}) {
		t.Errorf("fetched %q", e.fetched)
	}
	shown := e.show()
	if shown.GetWatch().GetRef() != "origin/dev" {
		t.Errorf("watch %v", shown.GetWatch())
	}
	if got := lineTexts(shown.GetChanges()[0].GetLines()); !slices.Equal(got, []string{
		"The reference branch is now origin/dev, not origin/main: Djinn reads the configuration there.",
		"The setup is now make.",
	}) {
		t.Errorf("a branch change: %q", got)
	}
}
