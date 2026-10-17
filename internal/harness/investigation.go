package harness

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/logx"
)

// An investigation (TASK_KIND_INVESTIGATION) is a short study or audit the lead launches to align with the developer
// fast: a worker in a worktree of its project that reads everything, runs commands and tests, and ends in a tilasm and
// a question. Djinn never merges its commits. It reuses the investigator's place in the scheduler (dispatch.ServedFirst:
// before the planned work, the slot of a work task it pauses on a full machine, not held by red push checks), within
// a cap per machine (dispatch.InvestigationCap), and the Lamplighter's budgets of a task's seal, shortened: at its time
// limit or its budget, Djinn sends it a message asking it to publish what it has, then stops it past a grace.

// Defaults of an investigation, from the studies and mockups of the wish that asked for it (W213, 17/10/2026): twelve
// of them ran 9 to 40 minutes, 16 at the median, 11 of 12 under 25; they cost 3 to 12 dollars, 6.4 at the median, 11
// of 12 under 10.
const (
	DefaultInvestigationMinutes   = 25
	DefaultInvestigationBudgetUSD = 10.0
)

// investigationGrace is how long an investigation asked to publish may go on before the Lamplighter stops it: a tilasm
// and a question take a few minutes.
const investigationGrace = 10 * time.Minute

// investigationBudgetMargin is how far past its budget an investigation's agent may spend, as its provider enforces it
// (Spec.MaxBudgetUSD): Djinn asks it to publish at its budget, and the margin pays for that last turn, instead of the
// provider cutting it dead.
const investigationBudgetMargin = 1.5

// methodPublish is what the journal records when Djinn asks an investigation to publish what it has.
const methodPublish = "harness/publish"

// investigationSpawn makes task, about to be spawned by req for an agent of provider kind in project, an investigation
// when req asks for one: in a project in Git, never urgent, its time limit stored and its budget Djinn's default when
// req gives none and the project's is larger or unset.
func investigationSpawn(req *planv1.TaskServiceSpawnRequest, kind planv1.Provider, project *planv1.Project, task *planv1.Task) error {
	if req.GetKind() != planv1.TaskKind_TASK_KIND_INVESTIGATION {
		if req.GetMinutes() != 0 {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("--minutes is for an investigation (--kind investigation)"))
		}
		return nil
	}
	switch {
	case kind == planv1.Provider_PROVIDER_WATCH:
		return connect.NewError(connect.CodeInvalidArgument, errors.New("an investigation is an agent: a watcher runs a command"))
	case project == nil || !project.GetGit():
		return connect.NewError(connect.CodeInvalidArgument, errors.New("an investigation works in a worktree of a project in Git"))
	case req.GetUrgent() || req.GetFeedback():
		return connect.NewError(connect.CodeInvalidArgument,
			errors.New("an investigation is served first already: --urgent and --feedback are for work"))
	}
	task.Kind = planv1.TaskKind_TASK_KIND_INVESTIGATION
	task.Investigation = &planv1.TaskInvestigation{Minutes: req.GetMinutes()}
	if req.GetMaxBudgetUsd() == 0 && (task.GetMaxBudgetUsd() == 0 || task.GetMaxBudgetUsd() > DefaultInvestigationBudgetUSD) {
		task.MaxBudgetUsd = DefaultInvestigationBudgetUSD
	}
	return nil
}

// investigationLimit is the time limit of the investigation t: as spawned, else the default.
func investigationLimit(t *planv1.Task) time.Duration {
	if m := t.GetInvestigation().GetMinutes(); m > 0 {
		return time.Duration(m) * time.Minute
	}
	return DefaultInvestigationMinutes * time.Minute
}

// providerBudget is the spending cap the provider of t's worker enforces: an investigation's budget with its margin
// (investigationBudgetMargin), any other task's own.
func providerBudget(t *planv1.Task) float64 {
	if dispatch.Investigation(t) {
		return t.GetMaxBudgetUsd() * investigationBudgetMargin
	}
	return t.GetMaxBudgetUsd()
}

// investigationRule is what an investigation's worker is told of its kind, after its prompt.
func investigationRule(t *planv1.Task) string {
	return fmt.Sprintf("You are an investigation: a short study or audit to align the developer and the lead fast. Read "+
		"everything, run commands and tests as you need; Djinn never merges your commits, so change code only to "+
		"measure or try, never to deliver. End by publishing what you found: a tilasm that lays out the findings and "+
		"the possible choices (`djinn tilasm put`), then a question to the developer that links it "+
		"(`djinn question ask`, the tilasm as `djinn://tilasm/<id>`); write a block for what the lead needs. "+
		"You have %d minutes of work and $%.2f: at either, Djinn asks you to publish what you have; do it at once, "+
		"then end, or Djinn stops you %d minutes later.",
		int(investigationLimit(t).Minutes()), t.GetMaxBudgetUsd(), int(investigationGrace.Minutes()))
}

// publishText is the message that asks an investigation to publish, reached why.
func publishText(why string) string {
	return "Djinn: you reached " + why + ". Publish what you have now: your tilasm, as it stands, and your question " +
		"to the developer linking it, saying what is left open; then end your turn. Djinn stops you in " +
		fmt.Sprintf("%d minutes.", int(investigationGrace.Minutes()))
}

// investigationDue says what the Lamplighter does now with the investigation t, whose run is r: ask it to publish,
// for why it reached (ask), or stop it, past the grace since it was asked (stop); both empty while it is within its
// limits. It marks r asked. The caller holds h.mu.
func (h *Harness) investigationDue(t *planv1.Task, r *run, now time.Time) (ask, stop string) {
	if !r.publishAt.IsZero() {
		if now.Sub(r.publishAt) >= investigationGrace {
			return "", fmt.Sprintf("investigation stopped: asked to publish at %s, it went on %s", r.publishWhy,
				investigationGrace)
		}
		return "", ""
	}
	limit := investigationLimit(t)
	spent := max(t.GetUsage().GetCostUsd(), r.spent)
	switch {
	case taskWallTime(t, now) >= limit:
		ask = fmt.Sprintf("its time limit, %s", limit)
	case t.GetMaxBudgetUsd() > 0 && spent >= t.GetMaxBudgetUsd():
		ask = fmt.Sprintf("its budget, $%.2f", t.GetMaxBudgetUsd())
	default:
		return "", ""
	}
	r.publishAt, r.publishWhy = now, ask
	return ask, ""
}

// askToPublish sends the investigation of r the message that asks it to publish, reached why; the pump records when
// on its task (deliver). A worker that ended first is not asked.
func (h *Harness) askToPublish(r *run, code, why string) {
	defer logx.Crash("ask to publish", "task", code)
	ctx, cancel := context.WithTimeout(h.ctx, time.Minute)
	defer cancel()
	m := &message{procedure: methodPublish, publish: why, reply: make(chan sent, 1),
		req: &planv1.TaskServiceSendRequest{TaskId: r.id, Text: publishText(why)}}
	if _, err := h.sendTo(ctx, r, code, m); err != nil && ctx.Err() == nil {
		logx.L().Warn("ask an investigation to publish", "task", code, "err", err)
	}
	logx.L().Info("lamplighter asks an investigation to publish", "task", code, "why", why)
}

// published records on the run's task, as the pump delivers the message, that Djinn asked it to publish for why.
func published(r *run, why string, at time.Time) *planv1.Task {
	inv := proto.CloneOf(r.task.GetInvestigation())
	if inv == nil {
		inv = &planv1.TaskInvestigation{}
	}
	inv.PublishTime, inv.PublishWhy = timestamppb.New(at), why
	r.task.Investigation = inv
	return r.task
}

// investigationLeft is why an investigation's work stays out of its wish's branch, as its ENDED line says.
const investigationLeft = "an investigation, whose commits Djinn never merges"
