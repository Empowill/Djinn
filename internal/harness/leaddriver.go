package harness

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/ui"
)

// Guards of a batch of the driven lead: a turn of the lead takes 3 to 5 model calls and a few cents (L10, 16/10), so
// these stop only a session that loops.
const (
	// LeadMaxTurns caps the model turns of a batch's session.
	LeadMaxTurns = 60
	// LeadMaxBudgetUSD caps what a batch's session spends, as claude counts it.
	LeadMaxBudgetUSD = 5.0
	// LeadTimeout is how long a batch's session may run.
	LeadTimeout = 30 * time.Minute
	// LeadRetryAfter is how long Djinn waits before the second session of a batch whose first failed.
	LeadRetryAfter = 10 * time.Second
)

// LeadDriver drives the lead (decision Q31, W131): for each batch of the lead's events, it starts a fresh headless
// session whose system prompt holds the rules of the driven lead (plan.DrivenRules) and whose first message holds
// the batch (plan.LeadPacket): its events whole, the briefs of their wishes, the lead's journal. The session acts with
// djinn's commands and ends its turn; its last text goes to the journal, never to a terminal. A batch done
// acknowledges its events; one that fails is tried once more in a new session, then reported beside Lead in the
// window, its events waiting for the next event or for Lead. The events queued while a batch runs wait for the next
// batch. One batch runs at a time.
//
// Each batch runs where the developer seated the lead (plan.ReadLeadSeat, W132): an agent, a model, an account, read
// as the batch starts, so that a move takes effect at the next batch. A batch that hits a usage limit is not tried
// again: Djinn moves the lead nowhere by itself, it tells the developer and offers the other seats of this machine in
// one click; its events wait for a move, for Lead, or for the limit to reset.

type LeadDriver struct {
	// Wishes serve the lead's queue, and the plan its batches read.
	Wishes *plan.Wishes
	// Providers start each session, by the agent of the lead's seat: Djinn's own (Harness.LeadDriver).
	Providers map[planv1.Provider]Provider
	// Provider starts each session whatever the seat, when Providers is nil: a test's agent. Claude by default.
	Provider Provider
	// Ready tells whether a seat may take the lead after a limit, its agent installed and its sign-in not known to be
	// missing; by default, the agent's status command, run in the account's folder.
	Ready func(ctx context.Context, seat *planv1.LeadSeat, account *planv1.Account) bool
	// Home is Djinn's data folder: the sessions run in its lead folder (plan.LeadFolder) and may read it all.
	Home string
	// Changed tells the window that the lead changed: a batch started, ended or failed.
	Changed func()
	// Env is added to each session's environment, after DJINN_LEAD=1.
	Env []string
	// Track is told of each session's process (Spec.Track): none outlives Djinn.
	Track func(pid int, cgroup, mark string) (ended func())
	// MaxTurns, MaxBudgetUSD, Timeout and RetryAfter are the guards of a batch; the Lead constants by default.
	MaxTurns     int
	MaxBudgetUSD float64
	Timeout      time.Duration
	RetryAfter   time.Duration
	// Now is the driver's clock; time.Now by default.
	Now func() time.Time
}

// LeadDriver is the driver of the lead of h's Djinn, on wishes: h's Claude, its processes tracked.
func (h *Harness) LeadDriver(wishes *plan.Wishes, changed func()) *LeadDriver {
	d := &LeadDriver{Wishes: wishes, Home: h.home, Changed: changed, Now: h.now, Providers: h.providers}
	if h.children != nil {
		d.Track = h.children.tracker("lead", "lead")
	}
	return d
}

func (d *LeadDriver) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Run drives the lead until ctx ends: a batch each time events wait and settle.
func (d *LeadDriver) Run(ctx context.Context) {
	defer logx.Crash("lead driver")
	drive := plan.DriveLead(d.Wishes.Store, d.Changed)
	d.restore(ctx, drive)
	// After a batch failed twice, the newest event it took: its events wait for a newer one, or for Lead.
	held := ""
	retried := drive.Retried()
	for ctx.Err() == nil {
		changed := d.Wishes.LeadEventsChanged() // Before reading, so that none is missed.
		events, err := d.Wishes.WaitLeadEvents(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logx.L().Error("read the lead's events", "loop", "lead", "err", err)
			wait(ctx, 5*time.Second)
			continue
		}
		if d.Wishes.Drive() == nil || (held != "" && events[len(events)-1].GetId() <= held) {
			select {
			case <-ctx.Done():
				return
			case <-changed:
			case <-retried:
				held = ""
			}
			continue
		}
		batch := plan.BatchEvents(events)
		held = ""
		retried = drive.Retried()
		ended, limit := d.batch(ctx, drive, batch)
		if !ended && ctx.Err() == nil {
			held = batch[len(batch)-1].GetId()
			if limit != nil {
				// The same seat tries again once the limit resets: its events, and the newer ones, wait for it, for
				// a move, or for Lead.
				until := d.now().Add(plan.LimitWait)
				if limit.GetUntil() != nil {
					until = limit.GetUntil().AsTime()
				}
				t := time.NewTimer(until.Sub(d.now()))
				select {
				case <-ctx.Done():
				case <-t.C:
				case <-retried:
				}
				t.Stop()
				held = ""
			}
		}
	}
}

// restore shows the last batch in the window, and ends as failed one a stop or a crash cut: its events still wait.
func (d *LeadDriver) restore(ctx context.Context, drive *plan.LeadDrive) {
	batches, err := plan.LeadJournal(ctx, d.Wishes.Store, nil, 1)
	if err != nil || len(batches) == 0 {
		return
	}
	last := batches[0]
	if last.GetState() == planv1.LeadBatchState_LEAD_BATCH_STATE_RUNNING {
		last.State, last.Error = planv1.LeadBatchState_LEAD_BATCH_STATE_FAILED, "cut: Djinn stopped while its session ran"
		last.EndTime = timestamppb.New(d.now())
		if err := plan.EndLeadBatch(ctx, d.Wishes.Store, last, d.now()); err != nil {
			logx.L().Error("end a batch a stop cut", "loop", "lead", "batch", last.GetId(), "err", err)
		}
	}
	drive.SetBatch(last)
}

// batch runs one batch of events, a second time in a new session when the first fails, and tells whether it ended
// its turn. Failed twice, it is reported; stopped by a usage limit, it is reported at once, with the limit.
func (d *LeadDriver) batch(ctx context.Context, drive *plan.LeadDrive, events []*planv1.LeadEvent) (bool, *planv1.LeadLimit) {
	var err error
	for attempt := int32(1); attempt <= 2; attempt++ {
		var limit *Limit
		var seat *planv1.LeadSeat
		if seat, limit, err = d.attempt(ctx, drive, events, attempt); err == nil {
			return true, nil
		}
		if ctx.Err() != nil {
			return false, nil // Stopping: the events wait for the next djinn up.
		}
		if limit != nil {
			// The same seat would hit it again: the developer moves the lead, or the events wait for the reset.
			logx.L().Warn("the lead hit a usage limit", "loop", "lead", "limit", limit.What, "events", len(events))
			return false, d.reportLimit(ctx, drive, seat, *limit, len(events))
		}
		logx.L().Warn("a batch of the lead failed", "loop", "lead", "attempt", attempt, "events", len(events), "err", err)
		if attempt == 1 && !wait(ctx, cmp.Or(d.RetryAfter, LeadRetryAfter)) {
			return false, nil
		}
	}
	drive.Fail(fmt.Sprintf("The Djinn's batch of %d event(s) failed twice: %v. Its events wait: the next event, "+
		"or Lead, starts a new session for them.", len(events), err))
	return false, nil
}

// reportLimit tells the developer that the lead on seat hit l, with the seats it may move to, and returns it.
func (d *LeadDriver) reportLimit(ctx context.Context, drive *plan.LeadDrive, seat *planv1.LeadSeat, l Limit, n int) *planv1.LeadLimit {
	now := d.now()
	after := now.Add(plan.LimitWait)
	accounts := plan.ReadAccounts(d.Home)
	limit := &planv1.LeadLimit{Text: limitText(l, after, now), Seat: seat, Moves: d.moves(ctx, seat, accounts)}
	if !l.Until.IsZero() && l.Until.After(now) {
		limit.Until = timestamppb.New(l.Until)
	}
	why := fmt.Sprintf("The Djinn on %s hit %s: its %d event(s) wait. Move the lead to another agent or account, "+
		"or they go to the same one then.", plan.SeatText(seat, accounts), limit.GetText(), n)
	drive.SetLimit(limit, why)
	return limit
}

// leadAgents are the agents that may lead, in the order the moves offer them.
var leadAgents = []planv1.Provider{planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_CODEX}

// moves are the seats the lead may move to from seat: the other accounts of its agent first, its model kept, then
// the other agents, on their own sign-in then on each account; only those Djinn can start and whose sign-in is not
// known to be missing.
func (d *LeadDriver) moves(ctx context.Context, seat *planv1.LeadSeat, accounts []*planv1.Account) []*planv1.LeadSeat {
	agents := slices.Clone(leadAgents)
	if i := slices.Index(agents, seat.GetProvider()); i > 0 {
		agents = append(append([]planv1.Provider{seat.GetProvider()}, agents[:i]...), agents[i+1:]...)
	}
	var all []*planv1.LeadSeat
	for _, p := range agents {
		if d.Providers != nil && d.Providers[p] == nil {
			continue
		}
		model := ""
		if p == seat.GetProvider() {
			model = seat.GetModel()
		}
		all = append(all, &planv1.LeadSeat{Provider: p, Model: model})
		for _, a := range accounts {
			if a.GetProvider() == p {
				all = append(all, &planv1.LeadSeat{Provider: p, Model: model, Account: a.GetName()})
			}
		}
	}
	all = slices.DeleteFunc(all, func(s *planv1.LeadSeat) bool { return plan.SameSeat(s, seat) })
	ready := make([]bool, len(all))
	var wg sync.WaitGroup
	for i, s := range all {
		wg.Go(func() { ready[i] = d.ready(ctx, s, plan.FindAccount(accounts, s.GetAccount())) })
	}
	wg.Wait()
	var moves []*planv1.LeadSeat
	for i, s := range all {
		if ready[i] {
			moves = append(moves, s)
		}
	}
	return moves
}

func (d *LeadDriver) ready(ctx context.Context, seat *planv1.LeadSeat, account *planv1.Account) bool {
	if d.Ready != nil {
		return d.Ready(ctx, seat, account)
	}
	switch ui.SignedIn(ctx, seat.GetProvider(), plan.AccountEnv(account)) {
	case uiv1.ProviderState_PROVIDER_STATE_READY, uiv1.ProviderState_PROVIDER_STATE_UNKNOWN:
		return true
	}
	return false
}

// attempt starts a fresh session on the batch, on the lead's seat, follows it to its end, and records it in the
// journal: done, its events acknowledged, or failed. It returns the seat, and the usage limit that stopped it, if one
// did.
func (d *LeadDriver) attempt(
	ctx context.Context, drive *plan.LeadDrive, events []*planv1.LeadEvent, attempt int32,
) (*planv1.LeadSeat, *Limit, error) {
	s := d.Wishes.Store
	seat := plan.ReadLeadSeat(d.Home)
	wishes := plan.EventWishes(events)
	journal, err := plan.LeadJournal(ctx, s, wishes, plan.JournalMax)
	if err != nil {
		return seat, nil, err
	}
	prompt, err := plan.LeadPacket(ctx, s, d.Home, events, journal, attempt)
	if err != nil {
		return seat, nil, err
	}
	projects, err := store.List[*planv1.Project](ctx, s, nil)
	if err != nil {
		return seat, nil, err
	}
	dir := plan.LeadFolder(d.Home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return seat, nil, err
	}
	b := &planv1.LeadBatch{
		Id: store.NewID(), StartTime: timestamppb.New(d.now()), Provider: seat.GetProvider(), Model: seat.GetModel(),
		Account: seat.GetAccount(), SessionId: store.NewID(), Attempt: attempt, WishIds: wishes,
		State: planv1.LeadBatchState_LEAD_BATCH_STATE_RUNNING, PromptChars: int32(len(prompt)),
	}
	titles := plan.DigestTitles(ctx, s, events)
	for _, e := range events {
		b.EventIds = append(b.EventIds, e.GetId())
		b.Events = append(b.Events, plan.DigestLine(e, titles[e.GetWishId()]))
	}
	if err := plan.PutLeadBatch(ctx, s, b); err != nil {
		return seat, nil, err
	}
	drive.SetBatch(b)

	provider, env, err := d.seat(seat)
	if err != nil {
		return seat, nil, d.end(ctx, drive, b, err)
	}
	spec := Spec{
		TaskID: b.GetSessionId(), Dir: dir, Prompt: prompt, Model: seat.GetModel(),
		SystemPrompt: plan.DrivenRules(projects, d.Home),
		MaxTurns:     cmp.Or(d.MaxTurns, LeadMaxTurns), MaxBudgetUSD: cmp.Or(d.MaxBudgetUSD, LeadMaxBudgetUSD),
		// djinn and the read-only Git commands, as the lead's terminal allows them; its own folder to write in.
		Permissions: &djinnv1.Permissions{Commands: plan.LeadCommands, Mode: djinnv1.Mode_MODE_LISTED},
		WriteDirs:   []string{dir}, ReadDirs: []string{d.Home},
		// Its djinn commands are the lead's, not the developer's: they keep no ifrit lit (CallerLead). Djinn's own
		// environment may name a worker (a djinn up started from a worker's shell): its commands would be that
		// worker's, refused in another Djinn (W131's own try), so the lead's are cleared.
		// An account's folder comes last: the session signs in there.
		Env: slices.Concat([]string{"DJINN_LEAD=1", "DJINN_HOME=" + d.Home, "DJINN_TASK_ID=", "DJINN_WISH_ID=", "DJINN_WORKER_TOKEN=",
			"DJINN_CALLER="}, d.Env, env),
		Track: d.Track,
	}
	run, cancel := context.WithTimeout(ctx, cmp.Or(d.Timeout, LeadTimeout))
	defer cancel()
	w, err := provider.Start(run, spec)
	if err != nil {
		return seat, nil, d.end(ctx, drive, b, err)
	}
	type pendingCall struct {
		index   int
		command string
	}
	var last, failure string
	var limit *Limit
	var pending []pendingCall
	for ev := range w.Events() {
		if ev.Limit != nil {
			limit = ev.Limit
		}
		changed := false
		switch ev.Kind {
		case planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL:
			idx := -1
			c := leadCommand(ev.Text)
			if c != "" && len(b.Commands) < plan.JournalCommandsMax {
				idx = len(b.Commands)
				b.Commands = append(b.Commands, plan.JournalCommand(c))
				changed = true
			}
			pending = append(pending, pendingCall{index: idx, command: c})
		case planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT:
			if len(pending) > 0 {
				call := pending[0]
				pending = pending[1:]
				if call.index >= 0 && toolResultFailed(ev) {
					b.Commands[call.index] = plan.JournalFailedCommand(call.command)
					changed = true
				}
			}
		case planv1.TaskEventKind_TASK_EVENT_KIND_TEXT:
			last = ev.Text
		case planv1.TaskEventKind_TASK_EVENT_KIND_USAGE:
			if ev.Usage != nil {
				b.Usage = ev.Usage // What the session spent so far.
				changed = true
			}
		case planv1.TaskEventKind_TASK_EVENT_KIND_ERROR:
			failure = ev.Text
		}
		if ev.Model != "" && b.Model != ev.Model {
			b.Model = ev.Model
			changed = true
		}
		if ev.SessionID != "" && b.SessionId != ev.SessionID {
			b.SessionId = ev.SessionID
			changed = true
		}
		if changed {
			drive.SetBatch(b)
		}
	}
	res := w.Wait()
	b.Note = plan.JournalNote(last)
	err = res.Err
	switch {
	case err != nil:
	case failure != "":
		err = errors.New(failure)
	case run.Err() != nil && ctx.Err() == nil:
		err = fmt.Errorf("its session ran past %s", cmp.Or(d.Timeout, LeadTimeout))
	case res.ExitCode != 0:
		err = fmt.Errorf("its session exited with code %d", res.ExitCode)
	}
	if err != nil && limit == nil {
		if l, ok := limitIn(err.Error(), d.now()); ok {
			limit = &l
		}
	}
	if err == nil {
		limit = nil
	}
	return seat, limit, d.end(ctx, drive, b, err)
}

// seat is the provider that starts a session on seat, and what its account adds to the session's environment.
func (d *LeadDriver) seat(seat *planv1.LeadSeat) (Provider, []string, error) {
	if err := plan.LeadAgent(seat.GetProvider()); err != nil {
		return nil, nil, err
	}
	var env []string
	if name := seat.GetAccount(); name != "" {
		a := plan.FindAccount(plan.ReadAccounts(d.Home), name)
		if a == nil || a.GetProvider() != seat.GetProvider() {
			return nil, nil, fmt.Errorf("the lead's account %s is no longer %s's: move the lead (djinn lead move)",
				name, plan.SeatText(&planv1.LeadSeat{Provider: seat.GetProvider()}, nil))
		}
		env = plan.AccountEnv(a)
	}
	if d.Providers == nil {
		if d.Provider == nil {
			return Claude{}, env, nil
		}
		return d.Provider, env, nil
	}
	p := d.Providers[seat.GetProvider()]
	if p == nil {
		return nil, nil, fmt.Errorf("this Djinn cannot start %s", plan.SeatText(seat, nil))
	}
	return p, env, nil
}

// end records b as it ended, with err when it failed, and returns err.
func (d *LeadDriver) end(ctx context.Context, drive *plan.LeadDrive, b *planv1.LeadBatch, err error) error {
	b.EndTime, b.State = timestamppb.New(d.now()), planv1.LeadBatchState_LEAD_BATCH_STATE_DONE
	if ctx.Err() != nil && err == nil {
		err = errors.New("cut: Djinn stopped while its session ran")
	}
	if err != nil {
		b.State, b.Error = planv1.LeadBatchState_LEAD_BATCH_STATE_FAILED, err.Error()
		if ctx.Err() != nil {
			b.Error = "cut: Djinn stopped while its session ran"
		}
	}
	if werr := plan.EndLeadBatch(context.WithoutCancel(ctx), d.Wishes.Store, b, d.now()); werr != nil {
		logx.L().Error("record a batch of the lead", "loop", "lead", "batch", b.GetId(), "err", werr)
		if err == nil {
			err = werr // Its events are not acknowledged: they come again.
		}
	}
	drive.SetBatch(b)
	return err
}

// leadCommand is what a tool call of the lead did, as its journal keeps it: a command it ran, a file it wrote by its
// name; empty for a read.
func leadCommand(call string) string {
	name, input, _ := strings.Cut(call, " ")
	var in struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
	}
	if json.Unmarshal([]byte(input), &in) != nil {
		in.Command = input // The fake agent's: the command line as it is.
	}
	switch name {
	case "Bash", "PowerShell":
		return strings.TrimSpace(in.Command)
	case "Write", "Edit", "NotebookEdit":
		if in.FilePath == "" {
			return name + " " + strings.TrimSpace(input)
		}
		return name + " " + filepath.Base(in.FilePath)
	}
	return ""
}

// toolResultFailed tells whether a tool result event is an error or refusal.
func toolResultFailed(ev Event) bool {
	if ev.ToolError {
		return true
	}
	t := strings.TrimSpace(ev.Text)
	return strings.HasPrefix(t, "error:") || strings.HasPrefix(t, "declined:") || t == "error" || t == "declined" ||
		strings.HasPrefix(t, "exit ")
}

// wait waits for d or ctx's end, and tells whether d passed.
func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
