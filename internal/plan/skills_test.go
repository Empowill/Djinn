package plan

import (
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// writeSkill makes a skill folder under dir/folder, with a SKILL.md holding front.
func writeSkill(t *testing.T, dir, folder, name, front string) string {
	t.Helper()
	skill := filepath.Join(dir, folder, name)
	if err := os.MkdirAll(skill, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, SkillFile), []byte("---\n"+front+"---\n# Body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return skill
}

func TestDescription(t *testing.T) {
	tests := []struct{ front, want string }{
		{"name: x\ndescription: Watch a merge request.\n", "Watch a merge request."},
		{"description: \"Quoted: yes\"\n", "Quoted: yes"},
		{"description: >-\n  Folded over\n  two lines.\nlicense: MIT\n", "Folded over two lines."},
		{"name: x\n", ""},
	}
	for _, tt := range tests {
		dir := writeSkill(t, t.TempDir(), "s", "x", tt.front)
		if got := Description(dir); got != tt.want {
			t.Errorf("Description(%q) = %q, want %q", tt.front, got, tt.want)
		}
	}
	if got := Description(t.TempDir()); got != "" {
		t.Errorf("Description without SKILL.md = %q", got)
	}
}

// TestProjectSkills: a project's skills are read in .agents/skills then .claude/skills; a name in both counts once,
// and a folder without SKILL.md is no skill.
func TestProjectSkills(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, SkillFolders[0], "babysit-mr", "description: From agents.\n")
	writeSkill(t, dir, SkillFolders[1], "Babysit-MR", "description: From claude.\n")
	writeSkill(t, dir, SkillFolders[1], "deploy", "description: Deploy.\n")
	if err := os.MkdirAll(filepath.Join(dir, SkillFolders[0], "notes"), 0o700); err != nil {
		t.Fatal(err)
	}
	got := ProjectSkills(dir)
	if len(got) != 2 || got[0].Name != "babysit-mr" || got[0].Description != "From agents." || got[1].Name != "deploy" {
		t.Fatalf("skills = %+v", got)
	}
	if s, err := FindSkill(dir, "BABYSIT-mr"); err != nil || s.Name != "babysit-mr" {
		t.Errorf("FindSkill = %+v, %v", s, err)
	}
	if _, err := FindSkill(dir, "nope"); err == nil {
		t.Errorf("FindSkill found a skill that is not there")
	}
}

// TestSummon: a skill of one project is summoned into another, listed, refused when it cannot be, said missing
// when its source loses it, and unsummoned. Neither project's folder changes.
func TestSummon(t *testing.T) {
	c := serve(t)
	skills := c.skills
	app, infra := t.TempDir(), t.TempDir()
	src := writeSkill(t, app, SkillFolders[0], "babysit-mr", "description: Watch a merge request.\n")
	writeSkill(t, infra, SkillFolders[1], "terraform", "description: Plan and apply.\n")
	for name, dir := range map[string]string{"app": app, "infra": infra} {
		if _, err := c.projects.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir, Name: name})); err != nil {
			t.Fatal(err)
		}
	}
	before := tree(t, infra)

	summon := func(skill, into string) (*planv1.Skill, error) {
		res, err := skills.Summon(t.Context(), connect.NewRequest(&planv1.SkillServiceSummonRequest{Skill: skill, Into: into}))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetSkill(), nil
	}
	got, err := summon("App/Babysit-MR", "infra")
	if err != nil {
		t.Fatal(err)
	}
	if got.GetName() != "babysit-mr" || got.GetSource() != "app/babysit-mr" || got.GetDirectory() != src ||
		got.GetDescription() != "Watch a merge request." || got.GetProject() != "infra" {
		t.Errorf("summoned = %v", got)
	}
	if _, err := summon("app/babysit-mr", "infra"); err != nil {
		t.Errorf("summoning twice = %v, want no change", err)
	}
	for _, tt := range []struct {
		skill, into string
		code        connect.Code
	}{
		{"app/nope", "infra", connect.CodeNotFound},
		{"nowhere/babysit-mr", "infra", connect.CodeNotFound},
		{"app/babysit-mr", "nowhere", connect.CodeNotFound},
		{"app/babysit-mr", "app", connect.CodeInvalidArgument},
		{"app/../x", "infra", connect.CodeInvalidArgument},
		{"app/babysit-mr", "", connect.CodeInvalidArgument},
	} {
		if _, err := summon(tt.skill, tt.into); code(err) != tt.code {
			t.Errorf("summon %s into %q = %v, want %v", tt.skill, tt.into, err, tt.code)
		}
	}
	// A project's own skill is never hidden by a summoned one.
	writeSkill(t, app, SkillFolders[0], "terraform", "description: Another.\n")
	if _, err := summon("app/terraform", "infra"); code(err) != connect.CodeAlreadyExists {
		t.Errorf("summon over an own skill = %v", err)
	}

	list := func() []*planv1.Skill {
		res, err := skills.List(t.Context(), connect.NewRequest(&planv1.SkillServiceListRequest{Project: "INFRA"}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetSkills()
	}
	l := list()
	if len(l) != 2 || l[0].GetName() != "terraform" || l[0].GetSource() != "" || l[1].GetSource() != "app/babysit-mr" || l[1].GetMissing() != "" {
		t.Errorf("list = %v", l)
	}

	// The source loses the skill: it is said missing, not dropped.
	if err := os.Rename(src, src+"-gone"); err != nil {
		t.Fatal(err)
	}
	if l = list(); len(l) != 2 || l[1].GetMissing() == "" {
		t.Errorf("list with the source gone = %v", l)
	}
	if err := os.Rename(src+"-gone", src); err != nil {
		t.Fatal(err)
	}

	if _, err := skills.Unsummon(t.Context(), connect.NewRequest(&planv1.SkillServiceUnsummonRequest{Skill: "app/babysit-mr", From: "infra"})); err != nil {
		t.Fatal(err)
	}
	if l = list(); len(l) != 1 || l[0].GetName() != "terraform" {
		t.Errorf("list after unsummon = %v", l)
	}
	_, err = skills.Unsummon(t.Context(), connect.NewRequest(&planv1.SkillServiceUnsummonRequest{Skill: "app/babysit-mr", From: "infra"}))
	if code(err) != connect.CodeNotFound {
		t.Errorf("unsummon twice = %v", err)
	}
	if after := tree(t, infra); after != before {
		t.Errorf("infra changed:\n%s\nwant\n%s", after, before)
	}
}

// tree lists every file under dir with its size: what a summon must leave as it was.
func tree(t *testing.T, dir string) string {
	t.Helper()
	var out string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out += rel + " " + info.Mode().String() + "\n"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
