package plan

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// gitIn runs git with args in dir, as a test identity; what it printed, trimmed.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	args = append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitAs commits everything in the repository of dir, and points each of refs at that commit: a remote ref, as a
// fetch would.
func commitAs(t *testing.T, dir string, refs ...string) string {
	t.Helper()
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "config")
	sha := gitIn(t, dir, "rev-parse", "HEAD")
	for _, ref := range refs {
		gitIn(t, dir, "update-ref", ref, sha)
	}
	return sha
}

// configProject is a project in Git, its folder in a repository with an origin, its main fetched holding files; the
// folder holds them too, as the commit leaves them.
func configProject(t *testing.T, files map[string]string) *planv1.Project {
	t.Helper()
	dir := gitRepo(t, "repo", "https://example.com/repo.git")
	gitIn(t, dir, "symbolic-ref", "HEAD", "refs/heads/main")
	for name, content := range files {
		writeFileAt(t, filepath.Join(dir, filepath.FromSlash(name)), content)
	}
	commitAs(t, dir, "refs/remotes/origin/main")
	return &planv1.Project{Id: store.NewID(), Name: "repo", Directory: dir, Git: true}
}

func writeFileAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestProjectConfigInFolder (W150): outside Git, without a remote, without Djinn's data folder, or when the reference
// branch is not fetched, Djinn reads a project's configuration in its folder, as before.
func TestProjectConfigInFolder(t *testing.T) {
	home := t.TempDir()
	plain := &planv1.Project{Id: store.NewID(), Directory: t.TempDir()}
	if c := ProjectConfig(home, plain); c.Dir != plain.GetDirectory() || c.FromReference() {
		t.Errorf("outside Git: %+v", c)
	}
	if c := ProjectConfig(home, &planv1.Project{}); c.Dir != "" {
		t.Errorf("no folder: %+v", c)
	}

	local := resolvedTempDir(t)
	gitIn(t, local, "init", "-q")
	writeFileAt(t, filepath.Join(local, SettingsFile), "setup: \"echo team\"\n")
	commitAs(t, local)
	p := &planv1.Project{Id: store.NewID(), Directory: local, Git: true}
	if c := ProjectConfig(home, p); c.Dir != local || c.FromReference() {
		t.Errorf("no remote: %+v", c)
	}

	unfetched := gitRepo(t, "unfetched", "https://example.com/u.git")
	commitAs(t, unfetched)
	p = &planv1.Project{Id: store.NewID(), Directory: unfetched, Git: true}
	if c := ProjectConfig(home, p); c.Dir != unfetched || c.FromReference() {
		t.Errorf("main not fetched: %+v", c)
	}

	fetched := configProject(t, nil)
	if c := ProjectConfig("", fetched); c.Dir != fetched.GetDirectory() {
		t.Errorf("no data folder: %+v", c)
	}
}

// TestProjectConfigReadsReference (W150): in Git with a remote, each kind of reader reads the configuration in the
// reference branch's tree at the last fetch, not in the folder, whatever branch the folder has out or what it changed:
// the settings, the permissions, the skills and what they declare. A message names the file on the branch; a fix is
// made in the folder.
func TestProjectConfigReadsReference(t *testing.T) {
	home := t.TempDir()
	skill := "---\nname: ship\ndescription: Ships it.\nmetadata:\n  djinn:\n    babysit:\n      watch: sh watch.sh {pr}\n---\n"
	p := configProject(t, map[string]string{
		".agents/settings.txtpb":        "setup: \"echo team\"\n",
		".agents/permissions.txtpb":     "commands: \"make\"\n",
		".agents/skills/ship/SKILL.md":  skill,
		".agents/skills/ship/watch.sh":  "echo watch\n",
		".claude/skills/other/SKILL.md": "---\nname: other\ndescription: Another.\n---\n",
		"app/README.md":                 "# App\n",
	})
	dir := p.GetDirectory()
	if err := os.Chmod(filepath.Join(dir, ".agents/skills/ship/watch.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	commitAs(t, dir, "refs/remotes/origin/main")
	// The folder moves on: another branch out, files changed and added, none of it on origin/main.
	gitIn(t, dir, "checkout", "-q", "-b", "feature")
	writeFileAt(t, filepath.Join(dir, SettingsFile), "setup: \"echo copy\"\n")
	writeFileAt(t, filepath.Join(dir, PermissionsFile), "commands: \"rm\"\n")
	writeFileAt(t, filepath.Join(dir, ".agents/skills/local/SKILL.md"), "---\nname: local\ndescription: Local.\n---\n")

	c := ProjectConfig(home, p)
	if !c.FromReference() || c.Ref != "origin/main" || c.Folder != dir || !strings.HasPrefix(c.Dir, ConfigCache(home)) {
		t.Fatalf("config = %+v, want origin/main extracted in the cache", c)
	}
	// Settings.
	if s, err := LoadSettings(home, p); err != nil || s.Setup != "echo team" {
		t.Errorf("settings: setup %q, %v; want the team's on origin/main", s.Setup, err)
	}
	// Permissions.
	if perms, _, err := ReadPermissions(ConfigDir(home, p)); err != nil || !slices.Equal(perms.GetCommands(), []string{"make"}) {
		t.Errorf("permissions: %v, %v; want origin/main's", perms, err)
	}
	// Skills, both folders, and what they declare.
	var names []string
	for _, s := range ProjectSkills(c.Dir) {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"other", "ship"}) {
		t.Errorf("skills = %v, want origin/main's other and ship, not the folder's local", names)
	}
	if b := ProjectBabysit(home, p, ForgeGitHub); b == nil || b.Skill != "ship" {
		t.Errorf("babysit = %v, want ship's", b)
	}
	if _, err := FindProjectSkill(home, p, "local"); err == nil || !strings.Contains(err.Error(), "origin/main") {
		t.Errorf("a skill only in the folder: %v, want none found on origin/main", err)
	}
	found, err := FindProjectSkill(home, p, "SHIP")
	if err != nil || c.Shown(found.Dir) != filepath.Join(dir, ".agents", "skills", "ship") {
		t.Errorf("ship: %v, %v; shown %s", found, err, c.Shown(found.Dir))
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Join(found.Dir, "watch.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
			t.Errorf("watch.sh extracted %v, %v; want it executable", info, err)
		}
	}
	if _, err := os.Stat(filepath.Join(c.Dir, "app")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the cache holds the rest of the tree: %v", err)
	}
	if got := c.Named(filepath.Join(c.Dir, SettingsFile)); got != "origin/main:.agents/settings.txtpb" {
		t.Errorf("named %q", got)
	}

	// A file Djinn cannot read on the branch: its message names it there.
	gitIn(t, dir, "checkout", "-q", "main")
	writeFileAt(t, filepath.Join(dir, SettingsFile), "nope: 1\nsetup: [\n")
	commitAs(t, dir, "refs/remotes/origin/main")
	if _, err := LoadSettings(home, p); err == nil || !strings.Contains(err.Error(), "origin/main:.agents/settings.txtpb") {
		t.Errorf("unreadable on origin/main: %v", err)
	}
}

// TestProjectConfigFollowsFetch (W150): a fetch that moves the reference branch changes the configuration Djinn
// reads; a commit that leaves the configuration as it was reads the same folder of the cache.
func TestProjectConfigFollowsFetch(t *testing.T) {
	home := t.TempDir()
	p := configProject(t, map[string]string{".agents/settings.txtpb": "setup: \"echo one\"\n"})
	dir := p.GetDirectory()
	first := ProjectConfig(home, p)
	writeFileAt(t, filepath.Join(dir, "README.md"), "# Other\n")
	commitAs(t, dir, "refs/remotes/origin/main")
	if again := ProjectConfig(home, p); again.Dir != first.Dir || again.Commit == first.Commit {
		t.Errorf("same configuration, another commit: %+v then %+v; want the same folder", first, again)
	}
	writeFileAt(t, filepath.Join(dir, SettingsFile), "setup: \"echo two\"\n")
	commitAs(t, dir, "refs/remotes/origin/main")
	if s, _ := LoadSettings(home, p); s.Setup != "echo two" {
		t.Errorf("after a fetch: setup %q, want echo two", s.Setup)
	}
	if ProjectConfig(home, p).Dir == first.Dir {
		t.Error("another configuration, the same folder")
	}
}

// TestProjectConfigMemo (W150): once the repository's files are old enough, Djinn keeps where it reads a project's
// configuration without running git; a change of a remote ref shows at once.
func TestProjectConfigMemo(t *testing.T) {
	home := t.TempDir()
	p := configProject(t, map[string]string{".agents/settings.txtpb": "setup: \"echo one\"\n"})
	dir := p.GetDirectory()
	old := time.Now().Add(-time.Hour)
	age := func() {
		t.Helper()
		common := filepath.Join(dir, ".git")
		paths := []string{filepath.Join(common, "config")}
		_ = filepath.WalkDir(filepath.Join(common, "refs", "remotes"), func(path string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				paths = append(paths, path)
			}
			return nil
		})
		for _, path := range paths {
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	age()
	kept := ProjectConfig(home, p)
	// The ref moved, but its files look as they did: the memo answers, no git runs.
	writeFileAt(t, filepath.Join(dir, SettingsFile), "setup: \"echo two\"\n")
	commitAs(t, dir, "refs/remotes/origin/main")
	age()
	if got := ProjectConfig(home, p); got != kept {
		t.Errorf("memo: %+v, want %+v", got, kept)
	}
	// A folder of the cache removed is extracted again.
	if err := os.RemoveAll(kept.Dir); err != nil {
		t.Fatal(err)
	}
	if got := ProjectConfig(home, p); got.Dir == kept.Dir {
		t.Errorf("after the cache was removed: %+v", got)
	}
	// A ref written now changes its folder's time.
	commitAs(t, dir, "refs/remotes/origin/main")
	if s, _ := LoadSettings(home, p); s.Setup != "echo two" {
		t.Errorf("after a fetch: setup %q, want echo two", s.Setup)
	}
}

// TestProjectConfigBranch (W150): the reference branch is the one your file names, else the one the team's file
// names in the default branch's tree, else the default branch; one not fetched leaves the folder's configuration.
func TestProjectConfigBranch(t *testing.T) {
	home := t.TempDir()
	p := configProject(t, map[string]string{".agents/settings.txtpb": "main_branch: \"dev\"\nsetup: \"echo main\"\n"})
	dir := p.GetDirectory()
	if c := ProjectConfig(home, p); c.FromReference() {
		t.Errorf("origin/dev not fetched: %+v, want the folder", c)
	}
	writeFileAt(t, filepath.Join(dir, SettingsFile), "setup: \"echo dev\"\n")
	commitAs(t, dir, "refs/remotes/origin/dev")
	s, err := LoadSettings(home, p)
	if c := ProjectConfig(home, p); c.Ref != "origin/dev" || c.TeamBranch != "dev" {
		t.Errorf("team names dev: %+v", c)
	}
	if err != nil || s.Setup != "echo dev" || s.MainBranch != "dev" ||
		s.MainBranchFrom != planv1.SettingSource_SETTING_SOURCE_REPOSITORY {
		t.Errorf("settings on dev: %+v, %v; want dev's setup, dev the team's reference", s, err)
	}
	if err := SaveDeveloperMainBranch(home, p.GetId(), "main"); err != nil {
		t.Fatal(err)
	}
	if s, _ := LoadSettings(home, p); ProjectConfig(home, p).Ref != "origin/main" || s.Setup != "echo main" {
		t.Errorf("you name main: setup %q", s.Setup)
	}
}

// TestProjectConfigSubfolder (W150): a project in a folder of its repository reads that folder of the tree.
func TestProjectConfigSubfolder(t *testing.T) {
	home := t.TempDir()
	root := configProject(t, map[string]string{
		".agents/settings.txtpb":     "setup: \"echo root\"\n",
		"app/.agents/settings.txtpb": "setup: \"echo app\"\n",
	})
	p := &planv1.Project{Id: store.NewID(), Directory: filepath.Join(root.GetDirectory(), "app"), Git: true}
	writeFileAt(t, filepath.Join(p.GetDirectory(), SettingsFile), "setup: \"echo copy\"\n")
	if s, _ := LoadSettings(home, p); s.Setup != "echo app" || !ProjectConfig(home, p).FromReference() {
		t.Errorf("setup %q, want app's on origin/main", s.Setup)
	}
}

// TestProjectConfigNotices (W150): the project's notices read the reference branch and point at the folder's file to
// fix; a copy on another branch is told that it does not matter. A summoned skill is read on its own project's
// reference branch.
func TestProjectConfigNotices(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	c := serve(t, WithHome(home), WithPusher(&refPusher{}))
	p := configProject(t, map[string]string{
		".agents/permissions.txtpb":    "commands: [\n",
		".agents/skills/bad/SKILL.md":  "---\nname: bad\nmetadata:\n  djinn:\n    source:\n      every: 1s\n---\n",
		".agents/skills/good/SKILL.md": "---\nname: good\ndescription: Good.\n---\n",
	})
	dir := p.GetDirectory()
	gitIn(t, dir, "checkout", "-q", "-b", "feature")
	writeFileAt(t, filepath.Join(dir, PermissionsFile), "commands: \"make\"\n")
	c.put(t, p)
	shown, err := c.projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{Project: p.GetName()}))
	if err != nil {
		t.Fatal(err)
	}
	n := shown.Msg.GetNotices()
	var perms, skill, branch *planv1.ProjectNotice
	for _, notice := range n {
		switch notice.GetTopic() {
		case topicPermissions:
			perms = notice
		case topicSkill:
			skill = notice
		case topicBranch:
			branch = notice
		}
	}
	if perms.GetFile() != filepath.Join(dir, PermissionsFile) || !strings.HasPrefix(perms.GetDetail(), "origin/main:.agents/permissions.txtpb") {
		t.Errorf("permissions: %v", perms)
	}
	if skill.GetSubject() != "bad" || skill.GetFile() != filepath.Join(dir, ".agents", "skills", "bad", SkillFile) {
		t.Errorf("skill: %v", skill)
	}
	if !strings.Contains(branch.GetText(), "no matter, Djinn reads the project's configuration on origin/main") {
		t.Errorf("branch: %v", branch)
	}

	other := &planv1.Project{
		Id: store.NewID(), Name: "other", Directory: t.TempDir(), Git: false,
		Summons: []*planv1.Summon{{ProjectId: p.GetId(), Skill: "good"}},
	}
	c.put(t, other)
	summoned, err := SummonedSkills(ctx, c.store, home, other)
	if err != nil || len(summoned) != 1 || summoned[0].Missing != "" ||
		!strings.HasPrefix(summoned[0].Dir, ConfigCache(home)) ||
		summoned[0].Folder != filepath.Join(dir, ".agents", "skills", "good") {
		t.Errorf("summoned = %+v, %v", summoned, err)
	}
	res, err := c.skills.List(ctx, connect.NewRequest(&planv1.SkillServiceListRequest{Project: "repo"}))
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, s := range res.Msg.GetSkills() {
		listed = append(listed, s.GetName())
	}
	if !slices.Equal(listed, []string{"bad", "good"}) {
		t.Errorf("listed %v", listed)
	}
}
