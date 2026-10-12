package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// babysitSkill writes a skill named name in dir whose metadata.djinn holds djinn (YAML at its indent).
func babysitSkill(t *testing.T, dir, name, djinn string) string {
	t.Helper()
	skill := filepath.Join(dir, ".agents", "skills", name)
	if err := os.MkdirAll(skill, 0o700); err != nil {
		t.Fatal(err)
	}
	text := "---\nname: " + name + "\ndescription: Babysit.\nmetadata:\n  djinn:\n" + djinn + "---\n\n# Babysit\n"
	if err := os.WriteFile(filepath.Join(skill, SkillFile), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return skill
}

// TestReadBabysit: a declaration needs a watch with {pr} or {mr}, knows only its placeholders and two forges.
func TestReadBabysit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, c := range []struct{ name, yaml, err string }{
		{"ok", "    babysit:\n      forge: GitLab\n      watch: \"go run ./mrwatch -mr {mr} -watch -state {state}\"\n      restart: true\n      done_when: MERGED\n", ""},
		{"none", "    wish:\n      title: x\n      match: x\n", ""},
		{"no-watch", "    babysit:\n      done_when: MERGED\n", "has no watch"},
		{"no-number", "    babysit:\n      watch: \"mrwatch -state {state}\"\n", "has no {pr} nor {mr}"},
		{"unknown", "    babysit:\n      watch: \"mrwatch {mr} {branch}\"\n", "{branch} is none of"},
		{"forge", "    babysit:\n      forge: bitbucket\n      watch: \"w {pr}\"\n", "neither github nor gitlab"},
	} {
		b, err := ReadBabysit(babysitSkill(t, dir, c.name, c.yaml))
		switch {
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%s: %v, want %q", c.name, err, c.err)
		case c.err == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.name == "none" && b != nil:
			t.Errorf("none: %v, want nil", b)
		case c.name == "ok" && (b.Forge != ForgeGitLab || !b.Restart || b.DoneWhen != "MERGED" || b.Skill != "ok"):
			t.Errorf("ok: %+v", b)
		}
	}
}

// TestProjectBabysit: the project's skill for its forge wins over one for any forge; none for the other forge.
func TestProjectBabysit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	babysitSkill(t, dir, "a-any", "    babysit:\n      watch: \"any {pr}\"\n")
	babysitSkill(t, dir, "b-gitlab", "    babysit:\n      forge: gitlab\n      watch: \"lab {mr}\"\n")
	project := &planv1.Project{Directory: dir}
	if b := ProjectBabysit(project, ForgeGitLab); b == nil || b.Skill != "b-gitlab" {
		t.Errorf("gitlab: %+v, want b-gitlab", b)
	}
	if b := ProjectBabysit(project, ForgeGitHub); b == nil || b.Skill != "a-any" {
		t.Errorf("github: %+v, want a-any", b)
	}
	if b := ProjectBabysit(&planv1.Project{Directory: t.TempDir()}, ForgeGitHub); b != nil {
		t.Errorf("no skill: %+v, want nil", b)
	}
}

// TestFillBabysit: the number fills {pr} and {mr}; the state file is one word, quoted when it needs it.
func TestFillBabysit(t *testing.T) {
	t.Parallel()
	got := FillBabysit("mrwatch -mr {mr} -watch -state {state} # {pr}", 123, "/home/a b/watchers/x/s-123")
	if want := "mrwatch -mr 123 -watch -state '/home/a b/watchers/x/s-123' # 123"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
