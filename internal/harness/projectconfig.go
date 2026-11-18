package harness

import (
	"sync"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
)

// A project tells Djinn about itself in its djinn.yaml (djinn.v1.ProjectConfig, plan.ReadProjectConfig), read where
// Djinn reads its configuration: on the reference branch, so that a worker never changes what bounds it.

// projectConfigWarned holds the warnings already logged about a project's djinn.yaml: one each, not one per worker.
var projectConfigWarned sync.Map

// projectContainers are the ceilings project sets in its djinn.yaml, and the file Djinn read them in, relative to the
// project: nil, and Djinn's defaults hold, when it sets none or when its file is unreadable, which is warned once with
// the file and the error. A field Djinn does not know is ignored, warned once with the file and the field, and the
// rest of the file applies. With both files, the root's is read, and a warning says the other is not. The project
// view's To fix says the same (plan.projectConfigNotices).
func (h *Harness) projectContainers(project *planv1.Project) (*djinnv1.Containers, string) {
	dir := plan.ConfigDir(h.home, project)
	if dir == "" {
		return nil, ""
	}
	read, err := plan.ReadProjectConfig(dir)
	if read.Ignored != "" {
		warnProjectConfig(project, dir+"\x00both", "two files configure Djinn: the first is read, the other is not",
			"read", read.File, "ignored", read.Ignored)
	}
	for _, u := range read.Unknown {
		warnProjectConfig(project, dir+"\x00unknown\x00"+read.File+"\x00"+u,
			"Djinn ignores a field of its configuration it does not know: the rest applies", "file", read.File, "field", u)
	}
	if err != nil {
		warnProjectConfig(project, dir+"\x00"+err.Error(), "Djinn's configuration unread: Djinn's defaults hold",
			"err", err)
		return nil, read.File
	}
	return read.Config.GetContainers(), read.File
}

// projectAdmits says whether the commands of project's workers wait for their admission by the gates: unless its
// djinn.yaml, read on the reference branch, says admit: false (W139). A file that does not read admits them, Djinn's
// default; projectContainers warns of it.
func (h *Harness) projectAdmits(project *planv1.Project) bool {
	if project == nil {
		return true
	}
	dir := plan.ConfigDir(h.home, project)
	if dir == "" {
		return true
	}
	read, err := plan.ReadProjectConfig(dir)
	if err != nil || read.Config == nil || read.Config.Admit == nil || read.Config.GetAdmit() {
		return true
	}
	warnProjectConfig(project, project.GetId()+"\x00admit", "the project's workers' commands wait for no admission by the gates",
		"file", read.File)
	return false
}

// warnProjectConfig logs msg about project's djinn.yaml, once per key.
func warnProjectConfig(project *planv1.Project, key, msg string, args ...any) {
	if _, seen := projectConfigWarned.LoadOrStore(key, true); seen {
		return
	}
	logx.L().Warn(msg, append([]any{"project", project.GetId()}, args...)...)
}

// ProjectGates are the gates project declares in its djinn.yaml, as Djinn applies them on this machine
// (plan.LoadProjectGates): what is wrong in the declaration, a gate left out, a pattern of one word, is warned once.
func (h *Harness) ProjectGates(project *planv1.Project) plan.ProjectGates {
	read := plan.LoadProjectGates(h.home, project)
	for _, w := range read.Warnings {
		warnProjectConfig(project, project.GetId()+"\x00gates\x00"+w, "Djinn's gates: "+w, "file", read.File)
	}
	return read
}
