package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
)

// TestSkillArgs: each provider gets the summoned skills by its agent's own path. Claude and agy add Djinn's folder
// of links; Claude may read the skills' folders and edit neither; codex is told where each SKILL.md is.
func TestSkillArgs(t *testing.T) {
	skills := []Skill{{Name: "babysit-mr", Source: "app/babysit-mr", Dir: "/src/app/.agents/skills/babysit-mr", Description: "Watch a MR."}}
	spec := Spec{TaskID: "t1", Skills: skills, SkillsDir: "/home/djinn/skills/t1"}

	claude := Claude{}.args(spec)
	if i := slices.Index(claude, "--add-dir"); i < 0 || claude[i+1] != spec.SkillsDir {
		t.Errorf("claude args = %v, want --add-dir %s", claude, spec.SkillsDir)
	}
	settings := claude[slices.Index(claude, "--settings")+1]
	want := `"permissions":{"allow":["Read(//src/app/.agents/skills/babysit-mr/**)"],` +
		`"deny":["Edit(//home/djinn/skills/t1/**)","Edit(//src/app/.agents/skills/babysit-mr/**)"]}`
	if !strings.Contains(settings, want) {
		t.Errorf("claude settings = %s\nwant %s", settings, want)
	}
	// With the project's permissions, the skills' rules come on top: never more than the file allows.
	spec.Permissions = &djinnv1.Permissions{Edit: true}
	settings = Claude{}.args(spec)[slices.Index(Claude{}.args(spec), "--settings")+1]
	var parsed struct {
		Permissions struct{ Allow, Deny []string }
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(parsed.Permissions.Allow, "Edit") || !slices.Contains(parsed.Permissions.Deny, "Edit(//src/app/.agents/skills/babysit-mr/**)") {
		t.Errorf("claude settings with permissions = %s", settings)
	}
	spec.Permissions = nil
	// A read-only worker gets the folder too: --restricted confines its reading tools to it and the project.
	ro := Claude{}.args(Spec{TaskID: "t1", ReadOnly: true, Skills: skills, SkillsDir: spec.SkillsDir})
	if i := slices.Index(ro, "--add-dir"); i < 0 || ro[i+1] != spec.SkillsDir || slices.Contains(ro, "--settings") {
		t.Errorf("claude read-only args = %v", ro)
	}
	// Without the folder of links (they could not be made), Claude gets nothing of them.
	if got := (Claude{}).args(Spec{TaskID: "t1", Skills: skills}); slices.Contains(got, "--add-dir") || strings.Contains(strings.Join(got, " "), "babysit") {
		t.Errorf("claude args without links = %v", got)
	}

	agy, err := Antigravity{}.args(spec)
	if i := slices.Index(agy, "--add-dir"); err != nil || i < 0 || agy[i+1] != spec.SkillsDir {
		t.Errorf("agy args = %v, %v", agy, err)
	}

	for _, resume := range []string{"", "th"} {
		spec.Resume = resume
		_, params := Codex{}.threadRequest(spec)
		text, _ := params["developerInstructions"].(string)
		if !strings.Contains(text, "babysit-mr (from app/babysit-mr): Watch a MR. SKILL.md: "+filepath.Join(skills[0].Dir, "SKILL.md")) {
			t.Errorf("codex developerInstructions (resume %q) = %q", resume, text)
		}
	}
	if _, params := (Codex{}).threadRequest(Spec{Dir: "/w"}); params["developerInstructions"] != nil {
		t.Errorf("codex without skills = %v", params)
	}
}

func TestClaudeAbsolute(t *testing.T) {
	if got := claudeAbsolute("/home/a/skills"); got != "//home/a/skills" {
		t.Errorf("claudeAbsolute = %q", got)
	}
}

// linkProbe is a provider that reads where the links of a worker's summoned skills lead, then plays a fake.
type linkProbe struct{ specs chan probed }

type probed struct {
	spec  Spec
	links map[string]string // link, relative to SkillsDir, to its target
}

func (p linkProbe) Start(ctx context.Context, spec Spec) (Worker, error) {
	links := map[string]string{}
	for _, folder := range plan.SkillFolders {
		for _, s := range spec.Skills {
			rel := filepath.Join(folder, s.Name)
			links[rel], _ = os.Readlink(filepath.Join(spec.SkillsDir, rel))
		}
	}
	p.specs <- probed{spec, links}
	spec.Prompt = "text read"
	return Fake{}.Start(ctx, spec)
}

// TestSummonAtLaunch: a skill summoned into a project reaches every worker there by its agent's path, from the
// source as it is; the project and the worker's worktree stay clean; a missing source is said; unsummoning stops it.
func TestSummonAtLaunch(t *testing.T) {
	home := t.TempDir()
	e := up(t, home)
	skills := planv1connect.NewSkillServiceClient(e.srv.Client(), e.srv.URL)

	app, infra := gitRepo(t), gitRepo(t)
	src := filepath.Join(app, ".agents", "skills", "babysit-mr")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: babysit-mr\ndescription: Watch a MR.\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.project.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: app, Name: "app"})); err != nil {
		t.Fatal(err)
	}
	wishID, infraID := e.wish(t, infra)
	if _, err := skills.Summon(t.Context(), connect.NewRequest(&planv1.SkillServiceSummonRequest{Skill: "app/babysit-mr", Into: infraID})); err != nil {
		t.Fatal(err)
	}

	spawn := func(kind planv1.Provider) (*planv1.Task, []*planv1.TaskEvent) {
		t.Helper()
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: "Watch the MR", Prompt: "x", Provider: kind,
		}))
		if err != nil {
			t.Fatal(err)
		}
		task := res.Msg.GetTask()
		return e.get(t, task.GetId()), e.watch(t.Context(), t, task.GetId(), 0)
	}
	clean := func(task *planv1.Task) {
		t.Helper()
		for _, dir := range []string{infra, task.GetWorktree()} {
			if out, err := git(t.Context(), dir, "status", "--porcelain", "--ignored"); err != nil || out != "" {
				t.Errorf("git status in %s = %q, %v; want it clean", dir, out, err)
			}
		}
		if _, err := os.Stat(skillsDir(home, task.GetId())); !os.IsNotExist(err) {
			t.Errorf("the task's links outlive it: %v", err)
		}
	}

	// The harness links the source's folder, under both folders, for the worker's whole life.
	probe := linkProbe{specs: make(chan probed, 1)}
	e.h.providers[planv1.Provider_PROVIDER_FAKE] = probe
	task, events := spawn(planv1.Provider_PROVIDER_FAKE)
	got := <-probe.specs
	if got.spec.SkillsDir != skillsDir(home, task.GetId()) || len(got.spec.Skills) != 1 || got.spec.Skills[0].Dir != src {
		t.Errorf("spec = %+v", got.spec)
	}
	for rel, target := range got.links {
		if target != src {
			t.Errorf("link %s leads to %q, want %s", rel, target, src)
		}
	}
	if !strings.Contains(events[1].GetText(), "with the summoned skills app/babysit-mr") {
		t.Errorf("start = %q", events[1].GetText())
	}
	clean(task)

	// Each real provider, played by the test binary, gets the right path in its arguments or its thread.
	for _, c := range []struct {
		kind     planv1.Provider
		provider Provider
		name     string
	}{
		{planv1.Provider_PROVIDER_CLAUDE, Claude{Command: os.Args[0]}, "claude"},
		{planv1.Provider_PROVIDER_ANTIGRAVITY, Antigravity{Command: os.Args[0]}, "antigravity"},
		{planv1.Provider_PROVIDER_CODEX, Codex{Command: os.Args[0]}, "codex"},
	} {
		env, argsFile, inputFile := fake{provider: c.name, fixture: "success", end: "eof"}.env(t)
		for _, kv := range env {
			k, v, _ := strings.Cut(kv, "=")
			t.Setenv(k, v)
		}
		e.h.providers[c.kind] = c.provider
		task, _ := spawn(c.kind)
		args, _ := os.ReadFile(argsFile)
		input, _ := os.ReadFile(inputFile)
		dir := skillsDir(home, task.GetId())
		switch c.name {
		case "claude":
			if !strings.Contains(string(args), "--add-dir\n"+dir+"\n") || !strings.Contains(string(args), "Read(/"+filepath.ToSlash(src)+"/**)") {
				t.Errorf("claude args = %s", args)
			}
		case "antigravity":
			if !strings.Contains(string(args), "--add-dir\n"+dir) {
				t.Errorf("agy args = %s", args)
			}
		case "codex":
			if !strings.Contains(string(input), `"developerInstructions":"Skills summoned`) || !strings.Contains(string(input), filepath.Join(src, "SKILL.md")) {
				t.Errorf("codex input = %s", input)
			}
		}
		if task.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE && task.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
			t.Errorf("%s task = %v", c.name, task)
		}
		clean(e.get(t, task.GetId()))
	}

	// The source loses the skill: the worker starts without it, and the task says why.
	if err := os.Rename(src, src+"-gone"); err != nil {
		t.Fatal(err)
	}
	task, events = spawn(planv1.Provider_PROVIDER_FAKE)
	if got = <-probe.specs; len(got.spec.Skills) != 0 || got.spec.SkillsDir != "" {
		t.Errorf("spec with the source gone = %+v", got.spec)
	}
	if !slices.ContainsFunc(events, func(ev *planv1.TaskEvent) bool {
		return strings.Contains(ev.GetText(), "summoned skill app/babysit-mr not found, the worker starts without it")
	}) {
		t.Errorf("events = %v", events)
	}
	clean(task)
	if err := os.Rename(src+"-gone", src); err != nil {
		t.Fatal(err)
	}

	// Unsummoned: the next worker gets nothing, and nothing is left behind.
	if _, err := skills.Unsummon(t.Context(), connect.NewRequest(&planv1.SkillServiceUnsummonRequest{Skill: "app/babysit-mr", From: infraID})); err != nil {
		t.Fatal(err)
	}
	task, _ = spawn(planv1.Provider_PROVIDER_FAKE)
	if got = <-probe.specs; len(got.spec.Skills) != 0 || got.spec.SkillsDir != "" {
		t.Errorf("spec after unsummon = %+v", got.spec)
	}
	clean(task)
}
