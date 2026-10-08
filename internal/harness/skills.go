package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
)

// skillsDir is the folder of a task's summoned skills, under Djinn's data folder: never in the project.
func skillsDir(home, taskID string) string {
	return filepath.Join(home, "skills", taskID)
}

// summoned are the skills project summons, for a worker of the task taskID: those found, linked from the task's
// own folder (skillsDir) under .claude/skills and .agents/skills, and a note for each one the worker does not get.
// Nothing is copied: a link reaches the source's folder as it is now. Nothing is written in the project.
func (h *Harness) summoned(ctx context.Context, project *planv1.Project, taskID string) (skills []Skill, dir string, notes []string) {
	if project == nil || len(project.GetSummons()) == 0 {
		return nil, "", nil
	}
	found, err := plan.SummonedSkills(ctx, h.store, project)
	if err != nil {
		return nil, "", []string{"summoned skills not read: " + err.Error()}
	}
	for _, s := range found {
		if s.Missing != "" {
			notes = append(notes, fmt.Sprintf("summoned skill %s not found, the worker starts without it: %s", s.Source, s.Missing))
			continue
		}
		skills = append(skills, Skill{Name: s.Name, Source: s.Source, Dir: s.Dir, Description: s.Description})
	}
	if len(skills) == 0 {
		return nil, "", notes
	}
	dir = skillsDir(h.home, taskID)
	if err := linkSkills(dir, skills); err != nil {
		_ = os.RemoveAll(dir)
		notes = append(notes, fmt.Sprintf("summoned skills not linked (%v): codex reads them where they are, claude and agy do not get them", err))
		return skills, "", notes
	}
	return skills, dir, notes
}

// linkSkills makes dir hold, under each folder of plan.SkillFolders, a link named after each skill to its folder.
// It starts from an empty dir, so a skill unsummoned since the last start is gone.
func linkSkills(dir string, skills []Skill) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for _, folder := range plan.SkillFolders {
		if err := os.MkdirAll(filepath.Join(dir, folder), 0o700); err != nil {
			return err
		}
		for _, s := range skills {
			if err := os.Symlink(s.Dir, filepath.Join(dir, folder, s.Name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// summon gives the run's worker the skills project summons (summoned), and says on the task what it does not get.
func (h *Harness) summon(ctx context.Context, r *run, project *planv1.Project) ([]Skill, string) {
	skills, dir, notes := h.summoned(ctx, project, r.id)
	for _, n := range notes {
		h.write(r, actorHarness, methodEvent, nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: n})
	}
	return skills, dir
}

// skillsText says which summoned skills a worker gets, for its start event.
func skillsText(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = s.Source
	}
	return ", with the summoned skills " + strings.Join(names, ", ")
}

// skillsInstructions tell an agent that cannot be shown the skills by a folder (codex) where they are, the way
// agents list their skills: a name, what it does, the path of its SKILL.md. The agent reads one when it fits.
func skillsInstructions(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Skills summoned from other projects, in the Agent Skills format. When the task matches a skill's " +
		"description, read its SKILL.md first and follow it; its other files sit next to it. They belong to their " +
		"source project: read them, never change them.\n")
	for _, s := range skills {
		desc := strings.TrimSpace(s.Description)
		if desc != "" {
			desc += " "
		}
		fmt.Fprintf(&b, "- %s (from %s): %sSKILL.md: %s\n", s.Name, s.Source, desc, filepath.Join(s.Dir, plan.SkillFile))
	}
	return b.String()
}
