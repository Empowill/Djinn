package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
)

// TestAgyProject: the agy project of a worker grants its folder and Git's folders for it, the listed commands in
// LISTED, every command in AUTO, and denies the denied commands, git push always in AUTO. Git's folders are found as git lays
// them out, without running git: a linked worktree's .git file names its folder in the repository, whose commondir
// names the repository's .git.
func TestAgyProject(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	repo, wt := filepath.Join(root, "repo"), filepath.Join(root, "worktrees", "W1")
	common := filepath.Join(repo, ".git")
	writeFile(t, common, filepath.Join("worktrees", "W1", "commondir"), "../..\n")
	writeFile(t, wt, ".git", "gitdir: "+filepath.Join(common, "worktrees", "W1")+"\n")
	// A worktree git writes with relative paths (worktree.useRelativePaths).
	rel := filepath.Join(root, "worktrees", "W2")
	writeFile(t, common, filepath.Join("worktrees", "W2", "commondir"), "../..\n")
	writeFile(t, rel, ".git", "gitdir: ../../repo/.git/worktrees/W2\n")
	// A worktree whose folder in the repository lies elsewhere: both are granted.
	apart := filepath.Join(root, "apart")
	writeFile(t, root, filepath.Join("elsewhere", "W3", "commondir"), common+"\n")
	writeFile(t, apart, ".git", "gitdir: "+filepath.Join(root, "elsewhere", "W3")+"\n")
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(filepath.Join(plain, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitDirs := []struct {
		dir  string
		want []string
	}{
		{wt, []string{common}},
		{filepath.Join(wt, "sub"), []string{common}},
		{rel, []string{common}},
		{apart, []string{common, filepath.Join(root, "elsewhere", "W3")}},
		{plain, []string{filepath.Join(plain, ".git")}},
	}
	for _, tt := range gitDirs {
		if got := agyGitDirs(tt.dir); !slices.Equal(got, tt.want) {
			t.Errorf("agyGitDirs(%s) = %q, want %q", tt.dir, got, tt.want)
		}
	}
	auto := &djinnv1.Permissions{Edit: true, Mode: djinnv1.Mode_MODE_AUTO, Commands: []string{"go tool task lint", "git status"},
		DeniedCommands: []string{"git push"}}
	listed := &djinnv1.Permissions{Edit: true, Commands: []string{"go tool task lint"}}
	autoOwn := &djinnv1.Permissions{Edit: true, Mode: djinnv1.Mode_MODE_AUTO, DeniedCommands: []string{"rm -rf"}}
	projects := []struct {
		spec        Spec
		allow, deny []string
	}{
		{Spec{Dir: wt, Permissions: auto}, []string{"write_file(" + wt + ")", "write_file(" + common + ")", "command(*)"},
			[]string{"command(git push)"}},
		// AUTO denies git push even when the project does not; outside Git, every command all the same.
		{Spec{Dir: wt, Permissions: autoOwn}, []string{"write_file(" + wt + ")", "write_file(" + common + ")", "command(*)"},
			[]string{"command(rm -rf)", "command(git push)"}},
		{Spec{Dir: filepath.Join(root, "nogit"), Permissions: autoOwn}, []string{"write_file(" + filepath.Join(root, "nogit") + ")", "command(*)"},
			[]string{"command(rm -rf)", "command(git push)"}},
		// LISTED: only what is listed, Git's folders all the same.
		{Spec{Dir: wt, Permissions: listed}, []string{"write_file(" + wt + ")", "write_file(" + common + ")", "command(go tool task lint)"}, nil},
	}
	for _, tt := range projects {
		f := agyProject(tt.spec)
		g := f.PermissionGrants.PermissionGrants
		if f.ID != agyProjectID(tt.spec.Dir) || !slices.Equal(g.Allow, tt.allow) || !slices.Equal(g.Deny, tt.deny) {
			t.Errorf("agyProject(%v) = %+v\nwant allow %q, deny %q", tt.spec.Permissions, f, tt.allow, tt.deny)
		}
	}
	if agyProjectID(wt) == agyProjectID(rel) || agyProjectID(wt) != agyProjectID(wt+string(filepath.Separator)) {
		t.Errorf("project ids: %s %s", agyProjectID(wt), agyProjectID(rel))
	}

	// Written in agy's projects folder, over an earlier one; removed with the worktree.
	spec := Spec{Dir: wt, Permissions: listed}
	if err := writeAgyProject(Spec{Dir: wt, Permissions: auto}); err != nil {
		t.Fatal(err)
	}
	if err := writeAgyProject(spec); err != nil {
		t.Fatal(err)
	}
	dir, _ := agyProjectsDir()
	path := filepath.Join(dir, agyProjectID(wt)+".json")
	if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), `"command(go tool task lint)"`) || strings.Contains(string(b), "command(*)") {
		t.Errorf("project file = %s, %v", b, err)
	}
	forgetAgyProject(wt)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("project file after the worktree is gone: %v", err)
	}
}
