package harness

import (
	"strings"
	"testing"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// TestConfigReferenceIsMain (W150): the branch Djinn reads a project's configuration on is the reference branch it
// merges from (findMain), whatever names it: the remote's HEAD, its main or master, a remote not named origin, your
// main_branch and its upstream on another remote.
func TestConfigReferenceIsMain(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	tests := []struct {
		name  string
		setup [][]string
		named string
	}{
		{"the remote's HEAD", [][]string{
			{"remote", "add", "origin", "https://example.com/r.git"},
			{"update-ref", "refs/remotes/origin/trunk", "HEAD"}, {"update-ref", "refs/remotes/origin/main", "HEAD"},
			{"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk"},
		}, ""},
		{"main", [][]string{
			{"remote", "add", "origin", "https://example.com/r.git"}, {"update-ref", "refs/remotes/origin/main", "HEAD"},
		}, ""},
		{"master", [][]string{
			{"remote", "add", "origin", "https://example.com/r.git"}, {"update-ref", "refs/remotes/origin/master", "HEAD"},
		}, ""},
		{"the only remote", [][]string{
			{"remote", "add", "fork", "https://example.com/r.git"}, {"update-ref", "refs/remotes/fork/main", "HEAD"},
		}, ""},
		{"your branch and its upstream", [][]string{
			{"remote", "add", "origin", "https://example.com/r.git"}, {"remote", "add", "up", "https://example.com/u.git"},
			{"update-ref", "refs/remotes/origin/main", "HEAD"}, {"update-ref", "refs/remotes/up/release", "HEAD"},
			{"branch", "dev"}, {"config", "branch.dev.remote", "up"}, {"config", "branch.dev.merge", "refs/heads/release"},
		}, "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := gitRepo(t)
			for _, args := range tt.setup {
				if _, err := git(ctx, repo, args...); err != nil {
					t.Fatal(err)
				}
			}
			home := t.TempDir()
			project := &planv1.Project{Id: store.NewID(), Directory: repo, Git: true}
			if tt.named != "" {
				if err := plan.SaveDeveloperMainBranch(home, project.GetId(), tt.named); err != nil {
					t.Fatal(err)
				}
			}
			_, ref, err := findMain(ctx, repo, tt.named)
			if err != nil {
				t.Fatal(err)
			}
			if got := plan.ProjectConfig(home, project).Ref; got != strings.TrimPrefix(ref, "refs/remotes/") {
				t.Errorf("configuration read on %q, Djinn merges from %s", got, ref)
			}
		})
	}
}

// TestDecideAccessOnReference (W150): a worker's permissions are the project's on its reference branch: an
// unreadable file in the folder does not stop it, and an edit there grants nothing.
func TestDecideAccessOnReference(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := gitRepoWith(t, map[string]string{".agents/permissions.txtpb": "edit: true\ncommands: \"make\"\n"})
	for _, args := range [][]string{
		{"remote", "add", "origin", "https://example.com/r.git"}, {"update-ref", "refs/remotes/origin/main", "HEAD"},
	} {
		if _, err := git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, repo, ".agents/permissions.txtpb", "edit: yes please\ncommands: \"rm\"\n")
	project := &planv1.Project{Id: store.NewID(), Name: "app", Directory: repo, Git: true}
	home := t.TempDir()
	access, perms, err := decideAccess(home, project, planv1.Provider_PROVIDER_CLAUDE, planv1.Allowance_ALLOWANCE_NONE)
	if err != nil || access != planv1.TaskAccess_TASK_ACCESS_AGENTS || !perms.GetEdit() ||
		strings.Join(perms.GetCommands(), ",") != "make" {
		t.Errorf("access = %v, %v, %v; want origin/main's permissions", access, perms, err)
	}
	if _, _, err := decideAccess("", project, planv1.Provider_PROVIDER_CLAUDE, planv1.Allowance_ALLOWANCE_NONE); err == nil {
		t.Error("without Djinn's data folder, the folder's unreadable file: no error")
	}
	refusal := watchRefusal(home, project, "rm -rf x", &djinnv1.Permissions{Commands: []string{"make"}})
	if refusal == nil || !strings.Contains(refusal.Error(), "does not list") {
		t.Errorf("refusal = %v, want it to read origin/main's permissions", refusal)
	}
}
