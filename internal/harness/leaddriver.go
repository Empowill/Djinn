package harness

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
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
type LeadDriver struct {
	// Wishes serve the lead's queue, and the plan its batches read.
	Wishes *plan.Wishes
	// Provider starts each session; Claude by default, as found on the PATH.
	Provider Provider
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
	d := &LeadDriver{Wishes: wishes, Home: h.home, Changed: changed, Now: h.now,
		Provider: h.providers[planv1.Provider_PROVIDER_CLAUDE]}
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
	for ctx.Err() == nil {
		changed, retried := d.Wishes.LeadEventsChanged(), drive.Retried() // Before reading, so that none is missed.
		events, err := d.Wishes.WaitLeadEvents(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logx.L().Error("read the lead's events", "loop", "lead", "err", err)
			wait(ctx, 5*time.Second)
			continue
		}
		if held != "" && events[len(events)-1].GetId() <= held {
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
		if !d.batch(ctx, drive, batch) && ctx.Err() == nil {
			held = batch[len(batch)-1].GetId()
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
// its turn. Failed twice, it is reported.
func (d *LeadDriver) batch(ctx context.Context, drive *plan.LeadDrive, events []*planv1.LeadEvent) bool {
	var err error
	for attempt := int32(1); attempt <= 2; attempt++ {
		if err = d.attempt(ctx, drive, events, attempt); err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false // Stopping: the events wait for the next djinn up.
		}
		logx.L().Warn("a batch of the lead failed", "loop", "lead", "attempt", attempt, "events", len(events), "err", err)
		if attempt == 1 && !wait(ctx, cmp.Or(d.RetryAfter, LeadRetryAfter)) {
			return false
		}
	}
	drive.Fail(fmt.Sprintf("The Djinn's batch of %d event(s) failed twice: %v. Its events wait: the next event, "+
		"or Lead, starts a new session for them.", len(events), err))
	return false
}

// attempt starts a fresh session on the batch, follows it to its end, and records it in the journal: done, its
// events acknowledged, or failed.
func (d *LeadDriver) attempt(ctx context.Context, drive *plan.LeadDrive, events []*planv1.LeadEvent, attempt int32) error {
	s := d.Wishes.Store
	wishes := plan.EventWishes(events)
	journal, err := plan.LeadJournal(ctx, s, wishes, plan.JournalMax)
	if err != nil {
		return err
	}
	prompt, err := plan.LeadPacket(ctx, s, d.Home, events, journal, attempt)
	if err != nil {
		return err
	}
	projects, err := store.List[*planv1.Project](ctx, s, nil)
	if err != nil {
		return err
	}
	dir := plan.LeadFolder(d.Home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b := &planv1.LeadBatch{
		Id: store.NewID(), StartTime: timestamppb.New(d.now()), Provider: planv1.Provider_PROVIDER_CLAUDE,
		SessionId: store.NewID(), Attempt: attempt, WishIds: wishes, State: planv1.LeadBatchState_LEAD_BATCH_STATE_RUNNING,
		PromptChars: int32(len(prompt)),
	}
	for _, e := range events {
		b.EventIds = append(b.EventIds, e.GetId())
		b.Events = append(b.Events, plan.DigestLine(e))
	}
	if err := plan.PutLeadBatch(ctx, s, b); err != nil {
		return err
	}
	drive.SetBatch(b)

	provider := d.Provider
	if provider == nil {
		provider = Claude{}
	}
	spec := Spec{
		TaskID: b.GetSessionId(), Dir: dir, Prompt: prompt, SystemPrompt: plan.DrivenRules(projects, d.Home),
		MaxTurns: cmp.Or(d.MaxTurns, LeadMaxTurns), MaxBudgetUSD: cmp.Or(d.MaxBudgetUSD, LeadMaxBudgetUSD),
		// djinn and the read-only Git commands, as the lead's terminal allows them; its own folder to write in.
		Permissions: &djinnv1.Permissions{Commands: plan.LeadCommands, Mode: djinnv1.Mode_MODE_LISTED},
		WriteDirs:   []string{dir}, ReadDirs: []string{d.Home},
		// Its djinn commands are the lead's, not the developer's: they keep no ifrit lit (CallerLead). Djinn's own
		// environment may name a worker (a djinn up started from a worker's shell): its commands would be that
		// worker's, refused in another Djinn (W131's own try), so the lead's are cleared.
		Env:   append([]string{"DJINN_LEAD=1", "DJINN_TASK_ID=", "DJINN_WISH_ID=", "DJINN_WORKER_TOKEN=", "DJINN_CALLER="}, d.Env...),
		Track: d.Track,
	}
	run, cancel := context.WithTimeout(ctx, cmp.Or(d.Timeout, LeadTimeout))
	defer cancel()
	w, err := provider.Start(run, spec)
	if err != nil {
		return d.end(ctx, drive, b, err)
	}
	var last, failure string
	for ev := range w.Events() {
		switch ev.Kind {
		case planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL:
			if c := leadCommand(ev.Text); c != "" && len(b.Commands) < plan.JournalCommandsMax {
				b.Commands = append(b.Commands, plan.JournalCommand(c))
			}
		case planv1.TaskEventKind_TASK_EVENT_KIND_TEXT:
			last = ev.Text
		case planv1.TaskEventKind_TASK_EVENT_KIND_USAGE:
			if ev.Usage != nil {
				b.Usage = ev.Usage // What the session spent so far.
			}
		case planv1.TaskEventKind_TASK_EVENT_KIND_ERROR:
			failure = ev.Text
		}
		if ev.Model != "" {
			b.Model = ev.Model
		}
		if ev.SessionID != "" {
			b.SessionId = ev.SessionID
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
	return d.end(ctx, drive, b, err)
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
