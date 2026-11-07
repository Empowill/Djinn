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
// the file and the error. With both files, the root's is read, and a warning says the other is not.
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
	if err != nil {
		warnProjectConfig(project, dir+"\x00"+err.Error(), "Djinn's configuration unread: Djinn's defaults hold",
			"err", err)
		return nil, read.File
	}
	return read.Config.GetContainers(), read.File
}

// warnProjectConfig logs msg about project's djinn.yaml, once per key.
func warnProjectConfig(project *planv1.Project, key, msg string, args ...any) {
	if _, seen := projectConfigWarned.LoadOrStore(key, true); seen {
		return
	}
	logx.L().Warn(msg, append([]any{"project", project.GetId()}, args...)...)
}
