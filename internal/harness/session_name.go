package harness

import (
	"path/filepath"
	"sync"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
)

// WithNameMax lets each Claude worker's start check that Claude Code can keep its session, with the file system's
// longest name nameMax measures (fsx.NameMax). Without it, nothing is checked.
func WithNameMax(nameMax func(string) int) Option { return func(h *Harness) { h.nameMax = nameMax } }

// sessionUnkeptWarned holds, by sign-in folder and the folder above the workers', the starts already warned of in the
// log: once each, not at every worker.
var sessionUnkeptWarned sync.Map

// sessionUnkept is what the start of t's Claude worker in dir on the sign-in name says when Claude Code cannot keep its
// session there: the name of its folder of transcripts is longer than the file system of the sign-in's folder takes,
// so Claude Code writes no transcript and says nothing, and the worker, once stopped, starts over on a new session
// instead of resuming its own (W30). "" when it can, when the limit is unknown, or for another agent. The project
// view says it under To fix (plan.sessionNotices).
func (h *Harness) sessionUnkept(t *planv1.Task, name, dir string) string {
	if h.nameMax == nil || agentOf(t) != planv1.Provider_PROVIDER_CLAUDE {
		return ""
	}
	s := plan.ClaudeSessionName(h.nameMax, h.signInFolder(name), dir)
	if !s.Lost() {
		return ""
	}
	if _, warned := sessionUnkeptWarned.LoadOrStore(s.Folder+"\x00"+filepath.Dir(dir), true); !warned {
		logx.L().Warn("Claude Code cannot keep the workers' sessions: a stopped worker starts over on a new session",
			"task", t.GetCode(), "dir", dir, "folder", s.Folder, "name", s.N, "limit", s.Limit)
	}
	return ", its session not kept: " + s.Why() + ", so a stop starts it over on a new session"
}
