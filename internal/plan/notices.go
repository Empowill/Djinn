package plan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// Notice kinds, topics and actions, short.
const (
	toFix       = planv1.ProjectNoticeKind_PROJECT_NOTICE_KIND_TO_FIX
	information = planv1.ProjectNoticeKind_PROJECT_NOTICE_KIND_INFORMATION

	topicSettings    = planv1.ProjectNoticeTopic_PROJECT_NOTICE_TOPIC_SETTINGS
	topicPermissions = planv1.ProjectNoticeTopic_PROJECT_NOTICE_TOPIC_PERMISSIONS
	topicSkill       = planv1.ProjectNoticeTopic_PROJECT_NOTICE_TOPIC_SKILL
	topicSetup       = planv1.ProjectNoticeTopic_PROJECT_NOTICE_TOPIC_SETUP
	topicBranch      = planv1.ProjectNoticeTopic_PROJECT_NOTICE_TOPIC_BRANCH
	topicSource      = planv1.ProjectNoticeTopic_PROJECT_NOTICE_TOPIC_SOURCE

	fixFile      = planv1.ProjectNoticeAction_PROJECT_NOTICE_ACTION_FIX_FILE
	plugSource   = planv1.ProjectNoticeAction_PROJECT_NOTICE_ACTION_PLUG_SOURCE
	unplugSource = planv1.ProjectNoticeAction_PROJECT_NOTICE_ACTION_UNPLUG_SOURCE
	unsummon     = planv1.ProjectNoticeAction_PROJECT_NOTICE_ACTION_UNSUMMON_SKILL
	nameBranch   = planv1.ProjectNoticeAction_PROJECT_NOTICE_ACTION_NAME_BRANCH
)

// projectNotices are what Djinn finds in project's configuration on this machine: its settings files, its
// permissions, its reference branch, its setup's last run, its skills and the sources this machine has not decided
// on. The things to fix come first, then the information, each in that order.
func (p *Projects) projectNotices(ctx context.Context, project *planv1.Project) []*planv1.ProjectNotice {
	files := readSettingsFiles(p.Home, project)
	out := settingsNotices(files)
	var repo, dev *planv1.ProjectSettings
	repoFile, devFile := settingsFiles(p.Home, project)
	for _, f := range files {
		if f.team {
			repo = f.settings
		} else {
			dev = f.settings
		}
	}
	settings := ResolveSettings(repo, dev)
	fileOf := func(from planv1.SettingSource) string {
		if from == planv1.SettingSource_SETTING_SOURCE_REPOSITORY {
			return repoFile
		}
		return devFile
	}
	if dir := project.GetDirectory(); dir != "" {
		out = append(out, permissionNotices(dir)...)
		if project.GetGit() && p.Pusher != nil {
			out = append(out, p.branchNotices(ctx, project, settings.MainBranch, fileOf(settings.MainBranchFrom))...)
		}
		out = append(out, setupNotices(project, settings.Setup, fileOf(settings.SetupFrom))...)
	}
	out = append(out, p.skillNotices(ctx, project)...)
	out = append(out, p.sourceNotices(ctx, project)...)
	slices.SortStableFunc(out, func(a, b *planv1.ProjectNotice) int { return cmp.Compare(a.GetKind(), b.GetKind()) })
	return out
}

// countToFix is how many of notices wait for you.
func countToFix(notices []*planv1.ProjectNotice) int32 {
	var n int32
	for _, notice := range notices {
		if notice.GetKind() == toFix {
			n++
		}
	}
	return n
}

// settingsNotices are what Djinn finds in the settings files files: one it cannot read, and what it ignores in
// those it reads.
func settingsNotices(files []settingsRead) []*planv1.ProjectNotice {
	var out []*planv1.ProjectNotice
	for _, f := range files {
		whose, subject := "your own settings of the project", f.path
		if f.team {
			whose, subject = "the team's settings", filepath.ToSlash(SettingsFile)
		}
		if f.err != nil {
			out = append(out, &planv1.ProjectNotice{
				Kind: toFix, Topic: topicSettings, Subject: subject, Action: fixFile, File: f.path, Detail: f.err.Error(),
				Text: "Djinn cannot read " + whose + ": the project's tasks do not spawn until the file is fixed.",
			})
			continue
		}
		for _, w := range f.warnings {
			n := &planv1.ProjectNotice{Kind: information, Topic: topicSettings, Subject: subject, Detail: w.text}
			if w.ignored {
				n.Text, n.Action, n.File = "Djinn ignores a line of "+whose+": a developer allows these commands in "+
					"their own file.", fixFile, f.path
			} else {
				n.Text = "Djinn ignores a field of " + whose + " that a newer Djinn knows."
			}
			out = append(out, n)
		}
	}
	return out
}

// permissionNotices are what Djinn finds in the permissions of the project folder dir.
func permissionNotices(dir string) []*planv1.ProjectNotice {
	subject, file := filepath.ToSlash(PermissionsFile), filepath.Join(dir, PermissionsFile)
	_, unknowns, err := ReadPermissions(dir)
	if err != nil {
		return []*planv1.ProjectNotice{{
			Kind: toFix, Topic: topicPermissions, Subject: subject, Action: fixFile, File: file, Detail: err.Error(),
			Text: "Djinn cannot read the project's permissions: its workers, watchers and sources do not start until " +
				"the file is fixed.",
		}}
	}
	var out []*planv1.ProjectNotice
	for _, u := range unknowns {
		out = append(out, &planv1.ProjectNotice{
			Kind: information, Topic: topicPermissions, Subject: subject,
			Text: "Djinn ignores a field of the project's permissions that a newer Djinn knows.", Detail: "unknown field " + u,
		})
	}
	return out
}

// branchNotices say whether Djinn finds project's reference branch, from name, its main_branch setting that file
// holds or would hold; and which other branch the project's folder has checked out.
func (p *Projects) branchNotices(ctx context.Context, project *planv1.Project, name, file string) []*planv1.ProjectNotice {
	branch, ref, err := p.Pusher.Reference(ctx, project, name)
	if err != nil {
		n := &planv1.ProjectNotice{
			Kind: toFix, Topic: topicBranch, Subject: cmp.Or(shortRef(ref), name), Action: nameBranch, File: file,
			Detail: err.Error(),
			Text: "Djinn finds no reference branch: neither the remote's default branch, nor main, nor master. It " +
				"keeps no wish up to date with it until main_branch names one.",
		}
		if name != "" {
			n.Text = fmt.Sprintf("Djinn does not find the reference branch %s: it keeps no wish up to date with it "+
				"until main_branch names a branch the repository holds.", n.GetSubject())
		}
		return []*planv1.ProjectNotice{n}
	}
	head, err := gitOut(ctx, project.GetDirectory(), "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || head == "" || head == branch {
		return nil // detached, or on the reference branch
	}
	return []*planv1.ProjectNotice{{
		Kind: information, Topic: topicBranch, Subject: head,
		Text: fmt.Sprintf("Your copy has %s checked out, not the reference branch %s: Djinn reads the project's "+
			"configuration from your copy, on %s.", head, shortRef(ref), head),
	}}
}

// shortRef is ref without refs/remotes/ nor refs/heads/: origin/main, main.
func shortRef(ref string) string {
	return strings.TrimPrefix(strings.TrimPrefix(ref, "refs/remotes/"), "refs/heads/")
}

// setupNotices say whether project's setup failed its last run, when it still is setup, the command file sets.
func setupNotices(project *planv1.Project, setup, file string) []*planv1.ProjectNotice {
	if setup == "" {
		return nil
	}
	for _, r := range project.GetCheckRuns() {
		if !r.GetSetup() || r.GetPassed() || r.GetCommand() != setup {
			continue
		}
		return []*planv1.ProjectNotice{{
			Kind: toFix, Topic: topicSetup, Subject: setup, Action: fixFile, File: file,
			Detail: cmp.Or(r.GetReason(), fmt.Sprintf("it exited %d", r.GetExitCode())),
			Text: "The setup failed the last time it made a worktree ready: the checks of the project's work do not " +
				"run until it passes.",
		}}
	}
	return nil
}

// skillNotices are what Djinn finds in the skills project holds and summons: a SKILL.md it cannot read, a wish
// template, inbox source or babysit it cannot use, a summoned skill it no longer finds; and, as an information, a
// front matter that breaks the YAML outside its metadata. A summoned skill's babysit
// does not count: it would run in the folder of the project that holds it.
func (p *Projects) skillNotices(ctx context.Context, project *planv1.Project) []*planv1.ProjectNotice {
	var out []*planv1.ProjectNotice
	check := func(name, dir string, own bool) {
		file := filepath.Join(dir, SkillFile)
		notice := func(text string, err error) {
			out = append(out, &planv1.ProjectNotice{
				Kind: toFix, Topic: topicSkill, Subject: name, Action: fixFile, File: file, Detail: err.Error(),
				Text: fmt.Sprintf(text, name),
			})
		}
		var unreadable *UnreadableSkillError
		if _, err := readDjinn(dir); errors.As(err, &unreadable) {
			notice("Djinn cannot read the skill %s: it ignores its wish template, inbox source and babysit until its "+
				"SKILL.md is fixed.", unreadable.Err)
			return
		}
		if _, warning, err := skillFront(dir); err == nil && warning != "" {
			out = append(out, &planv1.ProjectNotice{
				Kind: information, Topic: topicSkill, Subject: name, Action: fixFile, File: file, Detail: warning,
				Text: fmt.Sprintf("A line of the front matter of the skill %s breaks the YAML: Claude Code reads it "+
					"anyway, and Djinn reads its metadata apart.", name),
			})
		}
		if _, err := ReadTemplate(dir); err != nil {
			notice("The wish template of the skill %s cannot be used: no wish is made from it.", err)
		}
		if _, err := ReadSource(dir); err != nil {
			notice("The inbox source of the skill %s cannot be used: it runs on no machine.", err)
		}
		if _, err := ReadBabysit(dir); err != nil && own {
			notice("The babysit of the skill %s cannot be used: Djinn does not watch the project's pull requests "+
				"with it.", err)
		}
	}
	for _, s := range ProjectSkills(project.GetDirectory()) {
		check(s.Name, s.Dir, true)
	}
	summoned, err := SummonedSkills(ctx, p.Store, project)
	if err != nil {
		return out
	}
	for _, s := range summoned {
		if s.Missing == "" {
			check(s.Source, s.Dir, false)
			continue
		}
		out = append(out, &planv1.ProjectNotice{
			Kind: toFix, Topic: topicSkill, Subject: s.Source, Action: unsummon, Detail: s.Missing,
			Command: fmt.Sprintf("djinn skill unsummon %s --from %s", s.Source, project.GetName()),
			Text:    fmt.Sprintf("Djinn no longer finds the skill %s the project summons: its workers do not get it.", s.Source),
		})
	}
	return out
}

// sourceNotices are the inbox sources of project's skills this machine has not decided on, plugged in or said no
// to, and those it holds plugged in that no skill declares any more. A source that cannot be used is a notice of
// its skill.
func (p *Projects) sourceNotices(ctx context.Context, project *planv1.Project) []*planv1.ProjectNotice {
	srcs, err := declaredSources(ctx, p.Store, []*planv1.Project{project})
	if err != nil {
		return nil
	}
	var out []*planv1.ProjectNotice
	for _, s := range srcs {
		switch {
		case s.Dir == "" && strings.EqualFold(s.HolderID, project.GetId()):
			out = append(out, &planv1.ProjectNotice{
				Kind: information, Topic: topicSource, Subject: s.Name(), Action: unplugSource,
				Command: "djinn inbox unplug " + s.Name(),
				Text: fmt.Sprintf("The source %s is plugged in on this machine, but no skill declares it any more: it "+
					"runs nothing.", s.Name()),
			})
		case s.Err != nil || s.Plugged || s.Declined:
		default:
			out = append(out, &planv1.ProjectNotice{
				Kind: toFix, Topic: topicSource, Subject: s.Name(), Action: plugSource, Command: "djinn inbox plug " + s.Name(),
				Text: fmt.Sprintf("The skill %s offers an inbox source this machine has not decided on: plug it in for "+
					"its items to come here, or unplug it to say no.", s.Skill),
			})
		}
	}
	return out
}
