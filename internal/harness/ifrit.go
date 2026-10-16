package harness

import (
	"errors"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
)

// DefaultIfritIdle is how long the developer may leave Djinn alone before it puts an ifrit out, when neither the ifrit,
// djinn up --ifrit-idle nor the window's settings say (T51).
const DefaultIfritIdle = 30 * time.Minute

// WithIfritIdle sets how long the developer may leave Djinn alone before it puts an ifrit out, for an ifrit spawned
// without its own (djinn up --ifrit-idle); zero leaves it to the window's settings.
func WithIfritIdle(d time.Duration) Option { return func(h *Harness) { h.ifritIdle = d } }

// IfritIdle is how long the developer may leave Djinn alone before it puts the ifrit t out: its own (djinn task spawn
// --idle-minutes), else djinn up --ifrit-idle, else the window's settings, else DefaultIfritIdle.
func (h *Harness) IfritIdle(t *planv1.Task) time.Duration {
	if m := t.GetIfrit().GetIdleMinutes(); m > 0 {
		return time.Duration(m) * time.Minute
	}
	if h.ifritIdle > 0 {
		return h.ifritIdle
	}
	if m := machine.ReadIfritIdle(h.home); m > 0 {
		return time.Duration(m) * time.Minute
	}
	return DefaultIfritIdle
}

// ifritSpawn makes task, about to be spawned by req for an agent of provider kind in project, an ifrit when req asks
// for one: what it stands at is stored, nothing more yet.
func ifritSpawn(req *planv1.TaskServiceSpawnRequest, kind planv1.Provider, project *planv1.Project, task *planv1.Task) error {
	if req.GetKind() != planv1.TaskKind_TASK_KIND_IFRIT {
		if req.GetIdleMinutes() != 0 {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("--idle-minutes is for an ifrit (--kind ifrit)"))
		}
		return nil
	}
	switch {
	case kind == planv1.Provider_PROVIDER_WATCH:
		return connect.NewError(connect.CodeInvalidArgument, errors.New("an ifrit is an agent: a watcher runs a command"))
	case project == nil:
		return connect.NewError(connect.CodeInvalidArgument, errors.New("an ifrit works in a project: the wish has none"))
	}
	task.Kind = planv1.TaskKind_TASK_KIND_IFRIT
	task.Ifrit = &planv1.TaskIfrit{IdleMinutes: req.GetIdleMinutes()}
	return nil
}
