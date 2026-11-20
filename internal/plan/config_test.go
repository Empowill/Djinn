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

// gitIn runs git with args in dir, as a test identity; what it printed, trimmed. No automatic maintenance after a
// commit: one more git each time, which costs on Windows (W396).
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	args = append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false",
		"-c", "maintenance.auto=false"}, args...)
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
	t.Parallel()
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
	writeFileAt(t, filepath.Join(local, ProjectConfigFiles[1]), teamYAML("setup: \"echo team\"\n"))
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
// the settings, the permissions, the skills and what they declare, Djinn's djinn.yaml at the root. A message names the
// file on the branch; a fix is made in the folder.
func TestProjectConfigReadsReference(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	skill := "---\nname: ship\ndescription: Ships it.\nmetadata:\n  djinn:\n    babysit:\n      watch: sh watch.sh {pr}\n---\n"
	p := configProject(t, map[string]string{
		".agents/permissions.txtpb":     "commands: \"make\"\n",
		"djinn.yaml":                    "containers: {pids: 64}\n" + teamYAML("setup: \"echo team\"\n"),
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
	writeFileAt(t, filepath.Join(dir, PermissionsFile), "commands: \"rm\"\n")
	writeFileAt(t, filepath.Join(dir, "djinn.yaml"), "containers: {pids: 4096}\n"+teamYAML("setup: \"echo copy\"\n"))
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
	// Djinn's djinn.yaml: a worker never raises its own ceilings.
	if read, err := ReadProjectConfig(ConfigDir(home, p)); err != nil || read.Config.GetContainers().GetPids() != 64 {
		t.Errorf("djinn.yaml: %v, %v; want origin/main's", read.Config, err)
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
	if got := c.Named(filepath.Join(c.Dir, PermissionsFile)); got != "origin/main:.agents/permissions.txtpb" {
		t.Errorf("named %q", got)
	}

	// A file Djinn cannot read on the branch: its message names it there.
	gitIn(t, dir, "checkout", "-q", "main")
	writeFileAt(t, filepath.Join(dir, "djinn.yaml"), teamYAML("nope: 1\nsetup: [\n"))
	commitAs(t, dir, "refs/remotes/origin/main")
	if _, err := LoadSettings(home, p); err == nil || !strings.Contains(err.Error(), "origin/main:djinn.yaml") {
		t.Errorf("unreadable on origin/main: %v", err)
	}
}

// TestProjectConfigFollowsFetch (W150): a fetch that moves the reference branch changes the configuration Djinn
// reads; a commit that leaves the configuration as it was reads the same folder of the cache.
func TestProjectConfigFollowsFetch(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	p := configProject(t, map[string]string{".agents/djinn.yaml": teamYAML("setup: \"echo one\"\n")})
	dir := p.GetDirectory()
	first := ProjectConfig(home, p)
	writeFileAt(t, filepath.Join(dir, "README.md"), "# Other\n")
	commitAs(t, dir, "refs/remotes/origin/main")
	if again := ProjectConfig(home, p); again.Dir != first.Dir || again.Commit == first.Commit {
		t.Errorf("same configuration, another commit: %+v then %+v; want the same folder", first, again)
	}
	writeFileAt(t, filepath.Join(dir, ProjectConfigFiles[1]), teamYAML("setup: \"echo two\"\n"))
	commitAs(t, dir, "refs/remotes/origin/main")
	if s, _ := LoadSettings(home, p); s.Setup != "echo two" {
		t.Errorf("after a fetch: setup %q, want echo two", s.Setup)
	}
	if ProjectConfig(home, p).Dir == first.Dir {
		t.Error("another configuration, the same folder")
	}
	// A gate declared on origin/main, then taken out of it (W100 found no test): the fetch that moves the branch
	// changes the gates Djinn applies, beside the settings of the same file.
	writeFileAt(t, filepath.Join(dir, ProjectConfigFiles[1]), teamYAML("setup: \"echo two\"\n")+
		"gates:\n  - name: stack\n    alone: [task run-back]\n")
	commitAs(t, dir, "refs/remotes/origin/main")
	if g := LoadProjectGates(home, p); len(g.Gates) != 1 || g.Gates[0].GetName() != "stack" {
		t.Errorf("a gate on origin/main: %v %v, want stack", g.Gates, g.Warnings)
	}
	writeFileAt(t, filepath.Join(dir, ProjectConfigFiles[1]), teamYAML("setup: \"echo two\"\n")+"gates: []\n")
	commitAs(t, dir, "refs/remotes/origin/main")
	if g := LoadProjectGates(home, p); len(g.Gates) != 0 || len(g.Warnings) != 0 {
		t.Errorf("gates: [] on origin/main: %v %v, want none", g.Gates, g.Warnings)
	}
	if s, _ := LoadSettings(home, p); s.Setup != "echo two" {
		t.Errorf("with the gates: setup %q, want echo two", s.Setup)
	}
}

// TestProjectConfigMemo (W150): once the repository's files are old enough, Djinn keeps where it reads a project's
// configuration without running git; a change of a remote ref shows at once.
func TestProjectConfigMemo(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	p := configProject(t, map[string]string{".agents/djinn.yaml": teamYAML("setup: \"echo one\"\n")})
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
	writeFileAt(t, filepath.Join(dir, ProjectConfigFiles[1]), teamYAML("setup: \"echo two\"\n"))
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
	t.Parallel()
	home := t.TempDir()
	p := configProject(t, map[string]string{".agents/djinn.yaml": teamYAML("main_branch: \"dev\"\nsetup: \"echo main\"\n")})
	dir := p.GetDirectory()
	if c := ProjectConfig(home, p); c.FromReference() {
		t.Errorf("origin/dev not fetched: %+v, want the folder", c)
	}
	writeFileAt(t, filepath.Join(dir, ProjectConfigFiles[1]), teamYAML("setup: \"echo dev\"\n"))
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

// TestProjectConfigConfigBranch (W178, L37 A): the developer's own config_branch makes Djinn read the djinn.yaml and
// the skills on that branch, to try them before they merge; the permissions and main_branch stay those of the
// reference branch, so that no agent's commit widens its own rights nor moves the base of the requests. The branch
// gone from the remote, Djinn reads the reference branch again (Q110). The team's file cannot name it.
func TestProjectConfigConfigBranch(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	p := configProject(t, map[string]string{
		".agents/djinn.yaml":            teamYAML("setup: \"echo main\"\n"),
		".agents/permissions.txtpb":     "commands: \"make\"\n",
		".agents/skills/kept/SKILL.md":  "---\nname: kept\ndescription: Kept.\n---\n",
		".agents/skills/older/SKILL.md": "---\nname: older\ndescription: Older.\n---\n",
	})
	dir := p.GetDirectory()
	gitIn(t, dir, "checkout", "-q", "-b", "djinn/gates")
	writeFileAt(t, filepath.Join(dir, ProjectConfigFiles[1]), teamYAML("setup: \"echo gates\"\nmain_branch: \"elsewhere\"\n"))
	writeFileAt(t, filepath.Join(dir, PermissionsFile), "commands: \"rm\"\n")
	writeFileAt(t, filepath.Join(dir, ".agents", "skills", "tried", SkillFile), "---\nname: tried\ndescription: Tried.\n---\n")
	if err := os.RemoveAll(filepath.Join(dir, ".agents", "skills", "older")); err != nil {
		t.Fatal(err)
	}
	commitAs(t, dir, "refs/remotes/origin/djinn/gates")
	gitIn(t, dir, "checkout", "-q", "main")
	if err := SaveDeveloperConfigBranch(home, p.GetId(), "djinn/gates"); err != nil {
		t.Fatal(err)
	}

	c := ProjectConfig(home, p)
	if c.Ref != "origin/djinn/gates" || c.ReferenceRef != "origin/main" || c.ConfigBranchMissing() {
		t.Errorf("config_branch djinn/gates: %+v", c)
	}
	s, err := LoadSettings(home, p)
	if err != nil || s.Setup != "echo gates" || s.MainBranch != "" || s.ConfigBranch != "djinn/gates" ||
		s.ConfigBranchFrom != planv1.SettingSource_SETTING_SOURCE_DEVELOPER {
		t.Errorf("settings: setup %q, main_branch %q, config_branch %q, %v; want the branch's setup, main's main_branch",
			s.Setup, s.MainBranch, s.ConfigBranch, err)
	}
	var skills []string
	for _, sk := range ProjectSkills(c.Dir) {
		skills = append(skills, sk.Name)
	}
	if !slices.Equal(skills, []string{"kept", "tried"}) {
		t.Errorf("skills %v, want the branch's", skills)
	}
	perms, _, err := ReadPermissions(PermissionsDir(home, p))
	if err != nil || !slices.Equal(perms.GetCommands(), []string{"make"}) {
		t.Errorf("permissions %v, %v; want main's", perms.GetCommands(), err)
	}
	if ref, err := ConfigReference(t.Context(), home, p); err != nil || ref != "refs/remotes/origin/main" {
		t.Errorf("the watch's reference %q, %v", ref, err)
	}

	// The branch merged and deleted: back on main, silently for the work.
	gitIn(t, dir, "update-ref", "-d", "refs/remotes/origin/djinn/gates")
	c = ProjectConfig(home, p)
	if c.Ref != "origin/main" || !c.ConfigBranchMissing() || c.ConfigBranch != "djinn/gates" {
		t.Errorf("branch gone: %+v, want origin/main", c)
	}
	if s, err := LoadSettings(home, p); err != nil || s.Setup != "echo main" {
		t.Errorf("branch gone: setup %q, %v", s.Setup, err)
	}

	// Taken back.
	if err := SaveDeveloperConfigBranch(home, p.GetId(), ""); err != nil {
		t.Fatal(err)
	}
	if c := ProjectConfig(home, p); c.ConfigBranch != "" || c.Ref != "origin/main" {
		t.Errorf("unset: %+v", c)
	}
}

// TestProjectConfigTeamConfigBranch (W178): a config_branch in the team's djinn.yaml is ignored, a warning says so:
// only the developer's own file names it.
func TestProjectConfigTeamConfigBranch(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	p := configProject(t, map[string]string{".agents/djinn.yaml": teamYAML("config_branch: \"djinn/gates\"\nsetup: \"echo main\"\n")})
	dir := p.GetDirectory()
	gitIn(t, dir, "checkout", "-q", "-b", "djinn/gates")
	writeFileAt(t, filepath.Join(dir, ProjectConfigFiles[1]), teamYAML("setup: \"echo gates\"\n"))
	commitAs(t, dir, "refs/remotes/origin/djinn/gates")
	s, err := LoadSettings(home, p)
	if c := ProjectConfig(home, p); c.Ref != "origin/main" || c.ConfigBranch != "" {
		t.Errorf("team names a config_branch: %+v", c)
	}
	if err != nil || s.Setup != "echo main" || s.ConfigBranch != "" ||
		!slices.ContainsFunc(s.Warnings, func(w string) bool { return strings.Contains(w, "config_branch is ignored") }) {
		t.Errorf("settings %q %q %v, %v", s.Setup, s.ConfigBranch, s.Warnings, err)
	}
}

// TestProjectConfigBranchNotice (W178, Q110): djinn project set-branch --config names the configuration branch in the
// developer's own file; djinn project show and the window's notice say the configuration is read there, not on the
// reference branch, with the command that takes it back; the branch gone, a warning says Djinn reads main again.
func TestProjectConfigBranchNotice(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	c := serve(t, WithHome(home), WithPusher(&refPusher{}))
	p := configProject(t, map[string]string{".agents/djinn.yaml": teamYAML("setup: \"echo main\"\n")})
	dir := p.GetDirectory()
	gitIn(t, dir, "checkout", "-q", "-b", "djinn/gates")
	writeFileAt(t, filepath.Join(dir, ProjectConfigFiles[1]), teamYAML("setup: \"echo gates\"\n"))
	commitAs(t, dir, "refs/remotes/origin/djinn/gates")
	gitIn(t, dir, "checkout", "-q", "main")
	c.put(t, p)
	set := func(branch string, unset bool) {
		t.Helper()
		if _, err := c.projects.SetBranch(ctx, connect.NewRequest(&planv1.ProjectServiceSetBranchRequest{
			Project: "repo", Branch: branch, Unset: unset, Config: true,
		})); err != nil {
			t.Fatal(err)
		}
	}
	show := func() (*planv1.ProjectServiceShowResponse, *planv1.ProjectNotice) {
		t.Helper()
		res, err := c.projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{Project: "repo"}))
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range res.Msg.GetNotices() {
			if n.GetTopic() == topicBranch && n.GetSubject() == "djinn/gates" {
				return res.Msg, n
			}
		}
		return res.Msg, nil
	}
	set("djinn/gates", false)
	if dev, err := ReadSettings(DeveloperSettingsFile(home, p.GetId())); err != nil || dev.GetConfigBranch() != "djinn/gates" ||
		dev.GetMainBranch() != "" {
		t.Errorf("your file: %v, %v", dev, err)
	}
	res, n := show()
	if ref := res.GetReference(); ref.GetBranch() != "main" || ref.GetConfigBranch() != "djinn/gates" ||
		ref.GetConfigRef() != "origin/djinn/gates" {
		t.Errorf("reference %v", ref)
	}
	if n.GetKind() != information || !strings.HasPrefix(n.GetText(), "Configuration read from origin/djinn/gates, not origin/main") ||
		n.GetCommand() != "djinn project set-branch repo --config --unset" {
		t.Errorf("notice %v", n)
	}
	if !slices.ContainsFunc(res.GetSettings(), func(s *planv1.ProjectSetting) bool {
		return s.GetName() == "config_branch" && s.GetValue() == "djinn/gates" && s.GetDeveloper() == "djinn/gates"
	}) {
		t.Errorf("no config_branch row: %v", res.GetSettings())
	}

	gitIn(t, dir, "update-ref", "-d", "refs/remotes/origin/djinn/gates")
	res, n = show()
	if ref := res.GetReference(); ref.GetConfigBranch() != "djinn/gates" || ref.GetConfigRef() != "" {
		t.Errorf("branch gone: reference %v", ref)
	}
	if n.GetKind() != warning || !strings.HasPrefix(n.GetText(), "config_branch djinn/gates is not on the remote") ||
		!strings.Contains(n.GetText(), "reads the configuration on origin/main") {
		t.Errorf("branch gone: notice %v", n)
	}

	set("", true)
	if res, n := show(); n != nil || res.GetReference().GetConfigBranch() != "" {
		t.Errorf("unset: notice %v, reference %v", n, res.GetReference())
	}
}

// TestProjectConfigSubfolder (W150): a project in a folder of its repository reads that folder of the tree.
func TestProjectConfigSubfolder(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	root := configProject(t, map[string]string{
		".agents/djinn.yaml":     teamYAML("setup: \"echo root\"\n"),
		"app/.agents/djinn.yaml": teamYAML("setup: \"echo app\"\n"),
	})
	p := &planv1.Project{Id: store.NewID(), Directory: filepath.Join(root.GetDirectory(), "app"), Git: true}
	writeFileAt(t, filepath.Join(p.GetDirectory(), ProjectConfigFiles[1]), teamYAML("setup: \"echo copy\"\n"))
	if s, _ := LoadSettings(home, p); s.Setup != "echo app" || !ProjectConfig(home, p).FromReference() {
		t.Errorf("setup %q, want app's on origin/main", s.Setup)
	}
}

// TestProjectConfigNotices (W150): the project's notices read the reference branch and point at the folder's file to
// fix; a copy on another branch is told that it does not matter. A summoned skill is read on its own project's
// reference branch.
func TestProjectConfigNotices(t *testing.T) {
	t.Parallel()
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

// TestExtractConfig (W448): an empty SHA fails rather than returning the cache root; a read after the digest memo was
// cleared still returns the commit's extracted folder.
func TestExtractConfig(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	p := configProject(t, map[string]string{".agents/djinn.yaml": teamYAML("setup: \"echo team\"\n")})
	dir := p.GetDirectory()
	if got, err := extractConfig(ctx, home, dir, ""); err == nil || got != "" {
		t.Errorf("empty sha: got (%q, %v), want an error", got, err)
	}
	sha := gitIn(t, dir, "rev-parse", "refs/remotes/origin/main")
	first, err := extractConfig(ctx, home, dir, sha)
	if err != nil || first == ConfigCache(home) || !strings.HasPrefix(first, ConfigCache(home)) {
		t.Fatalf("first extract: (%q, %v), want a folder inside %s", first, err, ConfigCache(home))
	}
	configDigests.Lock()
	delete(configDigests.m, sha+"\x00"+dir)
	configDigests.Unlock()
	if again, err := extractConfig(ctx, home, dir, sha); err != nil || again != first {
		t.Errorf("after clearing configDigests: (%q, %v), want (%q, nil)", again, err, first)
	}
}

// TestProjectConfigMemoUnsettled: right after a ref is written (within configSettle), ProjectConfig still keeps its
// memo by content digest without running git again, while a second ref write in the same clock tick invalidates it.
func TestProjectConfigMemoUnsettled(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	p := configProject(t, map[string]string{".agents/djinn.yaml": teamYAML("setup: \"echo one\"\n")})
	dir := p.GetDirectory()
	kept := ProjectConfig(home, p)
	key := home + "\x00" + dir
	configMemos.Lock()
	_, found := configMemos.m[key]
	configMemos.Unlock()
	if !found {
		t.Fatal("ProjectConfig did not keep its memo while files are fresh")
	}
	writeFileAt(t, filepath.Join(dir, ProjectConfigFiles[1]), teamYAML("setup: \"echo two\"\n"))
	commitAs(t, dir, "refs/remotes/origin/main")
	if got := ProjectConfig(home, p); got.Commit == kept.Commit {
		t.Errorf("after ref update in same settle window: Commit=%q unchanged", got.Commit)
	}
}

func TestCheckedOutHead(t *testing.T) {
	t.Parallel()
	p := configProject(t, map[string]string{"README": "hi\n"})
	dir := p.GetDirectory()
	if got, ok := checkedOutHead(dir); !ok || got != "main" {
		t.Errorf("checkedOutHead(main) = (%q, %v), want (\"main\", true)", got, ok)
	}
	gitIn(t, dir, "checkout", "--detach", "HEAD")
	if got, ok := checkedOutHead(dir); !ok || got != "" {
		t.Errorf("checkedOutHead(detached) = (%q, %v), want (\"\", true)", got, ok)
	}
}
