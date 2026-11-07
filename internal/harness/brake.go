package harness

// The memory brake (T2; A3 of L03 v2, G11 of L04). The gates share the machine: a worker's command that neither reads
// nor edits waits for its admission, and none is admitted under pressure. What escapes them can still fill the memory:
// a process a command left running, a command through an absolute path, an agent without a hook, the sum of the
// workers. The kernel's ceilings stop a runaway (TasksMax, the containers' memory and pids); the brake catches a slow
// growth, which no ceiling sees.
//
// Under strong pressure (the memory available under Brake.Available of the machine's, or the PSI full avg10 of memory
// at Brake.Full or more, Brake.Readings readings in a row), the brake freezes, one at a time, the worker whose memory
// grew most over the last Brake.Window, among those that hold no gate nor admission: its scope (cgroup.freeze, as a
// pause) and its containers (docker pause). Still frozen after Brake.Stop, the memory still short, it is interrupted:
// its task resumes later in its session, its containers gone. A worker whose command the gates admitted is never
// interrupted, nor frozen while anything else grows: when only admitted workers grow, the brake freezes the last one
// admitted, and only thaws it. Its command lives on, frozen: its admission is not taken back, and the gates keep the
// peak it reached as its command's cost, which the next run waits for. Once calm (the memory available above Brake.Thaw
// and the PSI full under Brake.ThawFull for Brake.Calm), the frozen workers thaw one at a time, the last frozen first.
// The lead of each worker's wish hears every freeze, interruption and thaw. A frozen worker takes no slot, and the
// slot stays its own; its frozen time is left out of its budget, as a pause's is (taskWallTime: piste d). Never Djinn
// itself, its own commands and checks (they are no worker), the lead (no task), a watcher, a question worker or an ifrit.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"runtime"
	"slices"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/wrap"
)

// methodBrake is journaled when the memory brake freezes, thaws or interrupts a worker.
const methodBrake = "harness/brake"

// whyBrake starts what the brake says of a worker: its pause, its resume, its wait to resume.
const whyBrake = "memory brake: "

// Brake is when the memory brake freezes and thaws (WithBrake): DefaultBrake gives each field.
type Brake struct {
	// Available: the memory is short under this share of the machine's memory available (0.10: 10 %); 0: never for
	// the memory available alone.
	Available float64
	// Full: or at this PSI full avg10 of memory or more, every task waiting for memory that share of the last 10 s;
	// 0: the PSI never counts.
	Full float64
	// Readings: so many readings in a row, Every apart at least, find the memory short before a freeze.
	Readings int
	Every    time.Duration
	// Thaw and ThawFull: calm, the memory available is above Thaw of the machine's and the PSI full under ThawFull.
	// After Calm of calm, the last frozen thaws; the one before after another Calm.
	Thaw, ThawFull float64
	Calm           time.Duration
	// Stop: a worker frozen for a growth outside the gates is interrupted once frozen this long, the memory short.
	Stop time.Duration
	// Window: a worker's growth is its memory now less its lowest over this; Growth: the least that counts.
	Window time.Duration
	Growth uint64
	// Between: how long after a freeze the next may come: the kernel's readings follow the freeze.
	Between time.Duration
	// Memory reads what a worker holds now, its containers with it; nil: the anonymous memory of its scope and of its
	// task's containers (brakeMemory), else its last reading. ok false: unread.
	Memory func(taskID, cgroup string) (bytes uint64, ok bool)
	// Git says whether a git runs in the worker's cgroup: frozen, it would hold a lock of the repository Djinn merges
	// into. nil: read from the cgroup (machine.CgroupRuns).
	Git func(cgroup string) bool
}

// DefaultBrake freezes under percent of the machine's memory available or at a PSI full avg10 of 20, two readings in
// a row a second apart, and thaws above percent+5 and a PSI full under 5 after a minute of calm; percent 0: no brake.
// A worker grows when its memory rose 256 MiB in a minute; one frozen for a growth outside the gates is interrupted
// after 30 s; freezes come 10 s apart at least.
func DefaultBrake(percent int) Brake {
	if percent <= 0 {
		return Brake{}
	}
	return Brake{
		Available: float64(percent) / 100, Full: 20, Readings: 2, Every: time.Second,
		Thaw: float64(min(percent+5, 100)) / 100, ThawFull: 5, Calm: time.Minute,
		Stop: 30 * time.Second, Window: time.Minute, Growth: 256 << 20, Between: 10 * time.Second,
	}
}

// on says whether the brake looks at the machine at all.
func (b Brake) on() bool { return b.Available > 0 || b.Full > 0 }

// short says why the memory of the machine s is short now, "" when it is not.
func (b Brake) short(s machine.Snapshot) string {
	if b.Full > 0 && s.Memory != nil && s.Memory.Full >= b.Full {
		return fmt.Sprintf("tasks all waited for memory %.0f%% of the last 10 s", s.Memory.Full)
	}
	if b.Available > 0 && s.MemoryTotal > 0 && float64(s.MemoryAvailable) < b.Available*float64(s.MemoryTotal) {
		return fmt.Sprintf("%s of memory available (%s)", percent(s.MemoryAvailable, s.MemoryTotal), wrap.Size(s.MemoryAvailable))
	}
	return ""
}

// calm says whether the machine s is calm enough for a frozen worker to thaw.
func (b Brake) calm(s machine.Snapshot) bool {
	if s.Memory != nil && b.ThawFull > 0 && s.Memory.Full >= b.ThawFull {
		return false
	}
	return s.MemoryTotal == 0 || float64(s.MemoryAvailable) >= b.Thaw*float64(s.MemoryTotal)
}

// percent writes part of whole as a person reads it: 6%.
func percent(part, whole uint64) string {
	return fmt.Sprintf("%.0f%%", math.Round(100*float64(part)/float64(max(whole, 1))))
}

// WithBrake has Djinn freeze, under strong memory pressure, the worker that grows outside the gates (b): read gives the
// machine (djinn up gives its monitor's). Without it, or with b's thresholds 0, no brake.
func WithBrake(read func() machine.Snapshot, b Brake) Option {
	return func(h *Harness) { h.brakeRead, h.brake = read, b }
}

// GateGrants tells the harness when each task's latest gate or admission held now was granted, by task ID: the brake
// freezes the last admitted worker when only admitted ones grow. djinn up gives the gates' (gate.Gates.Intervals). It
// runs without h.mu: it may call the harness back.
func (h *Harness) GateGrants(grants func() map[string]time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.grants = grants
}

// brakeState is what the brake remembers between the scheduler's passes, which alone read and change it (h.sched).
type brakeState struct {
	read     time.Time               // the last reading of the machine
	readings int                     // the readings in a row the memory was short
	calm     time.Time               // since when the machine is calm; zero while it is not
	froze    time.Time               // the last freeze
	samples  map[string][]brakePoint // by task: its memory over the window
	frozen   []*brakeFrozen          // the workers the brake froze, the first frozen first
}

// brakePoint is a worker's memory at one time.
type brakePoint struct {
	at     time.Time
	memory uint64
}

// brakeFrozen is a worker the brake froze.
type brakeFrozen struct {
	id, code, wish string
	at             time.Time // when it froze
	admitted       bool      // it held a gate or an admission: never interrupted
	kept           bool      // its provider cannot resume a session: kept frozen until calm, never interrupted
	memory         uint64    // what it held as it froze
	containers     bool      // its task runs containers, which docker paused
}

// brakeCandidate is a running worker the brake may freeze.
type brakeCandidate struct {
	id, code, wish, cgroup string
	memory                 uint64    // what it holds now
	growth                 int64     // over the window
	admitted               bool      // holds a gate or an admission
	granted                time.Time // when its latest one was granted (admitted only; zero when not known)
	git                    bool      // a git runs in its cgroup
}

// brakePass is the brake's turn in a scheduler pass: it reads the machine, every Brake.Every at most, and the
// workers' memory, then freezes, interrupts or thaws one worker at most. The caller holds h.sched.
func (h *Harness) brakePass(ctx context.Context) {
	b := h.brake
	if h.brakeRead == nil || !b.on() || h.goosIs("windows") {
		return
	}
	st := &h.brakeAt
	now := h.now()
	if !st.read.IsZero() && now.Sub(st.read) < b.Every {
		return
	}
	st.read = now
	s := h.brakeRead()
	cands := h.brakeSample(now)
	h.brakeForget()
	short := b.short(s)
	if short == "" {
		st.readings = 0
		if !b.calm(s) {
			st.calm = time.Time{}
			return
		}
		if st.calm.IsZero() {
			st.calm = now
		}
		if len(st.frozen) > 0 && now.Sub(st.calm) >= b.Calm {
			h.brakeThaw(ctx, s, now)
			st.calm = now // The next thaws after another calm.
		}
		return
	}
	st.calm = time.Time{}
	st.readings++
	if st.readings < max(b.Readings, 1) {
		return
	}
	for _, f := range st.frozen {
		if !f.admitted && !f.kept && now.Sub(f.at) >= b.Stop {
			h.brakeInterrupt(ctx, f, now)
			return
		}
	}
	if !st.froze.IsZero() && now.Sub(st.froze) < b.Between {
		return
	}
	// One frozen for a growth outside the gates frees its memory once interrupted: no admitted worker is frozen before.
	pending := slices.ContainsFunc(st.frozen, func(f *brakeFrozen) bool { return !f.admitted && !f.kept })
	if c := h.brakePick(cands, pending); c != nil {
		h.brakeFreeze(ctx, c, short, now)
	}
}

// goosIs says whether Djinn runs on the system goos.
func (h *Harness) goosIs(goos string) bool {
	if h.goos != nil {
		return h.goos() == goos
	}
	return runtime.GOOS == goos
}

// brakeSample reads the memory of every worker the brake may freeze, keeps it over the window, and gives them with
// their growth. A worker it froze is not read: it no longer grows.
func (h *Harness) brakeSample(now time.Time) []brakeCandidate {
	st := &h.brakeAt
	h.mu.Lock()
	var cands []brakeCandidate
	live := map[string]bool{}
	for id, r := range h.runs {
		live[id] = true
		if r.worker == nil || r.paused || r.stopping || r.final || r.shelved || r.watcher || r.light || r.ifrit {
			continue
		}
		if _, ok := r.worker.(Pauser); !ok {
			continue
		}
		t := r.shown // Not r.task: its pump changes it without a lock (W182).
		if t == nil || t.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
			continue
		}
		c := brakeCandidate{id: id, code: cmp.Or(t.GetCode(), r.code), wish: r.wish, cgroup: r.cgroup}
		c.admitted = h.held != nil && len(h.held(id)) > 0
		cands = append(cands, c)
	}
	h.mu.Unlock()
	for id := range st.samples {
		if !live[id] {
			delete(st.samples, id)
		}
	}
	if st.samples == nil {
		st.samples = map[string][]brakePoint{}
	}
	cut := now.Add(-h.brake.Window)
	for i := range cands {
		c := &cands[i]
		memory, ok := h.brakeMemory(c.id, c.cgroup)
		if !ok {
			continue
		}
		c.memory = memory
		pts := append(st.samples[c.id], brakePoint{at: now, memory: memory})
		pts = slices.DeleteFunc(pts, func(p brakePoint) bool { return p.at.Before(cut) })
		st.samples[c.id] = pts
		low := memory
		for _, p := range pts {
			low = min(low, p.memory)
		}
		c.growth = int64(memory) - int64(low)
		if c.cgroup != "" {
			c.git = h.brakeGit(c.cgroup)
		}
	}
	return cands
}

// brakeMemory reads what the worker of the task holds now (Brake.Memory).
func (h *Harness) brakeMemory(taskID, cgroup string) (uint64, bool) {
	if h.brake.Memory != nil {
		return h.brake.Memory(taskID, cgroup)
	}
	fsys := os.DirFS("/")
	var total uint64
	read := false
	if cgroup != "" {
		if anon, err := machine.ReadAnon(fsys, cgroup); err == nil {
			total, read = total+anon, true
		}
	}
	if read {
		// Its containers run in docker's tree, under its task's slice; none, no slice.
		if anon, err := machine.ReadAnon(fsys, path.Join(machine.ContainersSlice, wrap.Slice(taskID))); err == nil {
			total += anon
		}
		return total, true
	}
	// No scope, or no memory controller: its last reading (measure), its containers counted.
	h.mu.Lock()
	r := h.runs[taskID]
	h.mu.Unlock()
	if r == nil {
		return 0, false
	}
	h.useMu.Lock()
	defer h.useMu.Unlock()
	if r.resources == nil || r.resources.GetReadTime() == nil {
		return 0, false
	}
	return r.resources.GetMemoryBytes(), true
}

// brakeGit says whether a git runs in the cgroup (Brake.Git).
func (h *Harness) brakeGit(cgroup string) bool {
	if h.brake.Git != nil {
		return h.brake.Git(cgroup)
	}
	return machine.CgroupRuns(os.DirFS("/"), cgroup, "git")
}

// brakeForget forgets the frozen workers that no longer are: resumed by hand, or ended.
func (h *Harness) brakeForget() {
	st := &h.brakeAt
	h.mu.Lock()
	defer h.mu.Unlock()
	st.frozen = slices.DeleteFunc(st.frozen, func(f *brakeFrozen) bool {
		r := h.runs[f.id]
		return r == nil || !r.brakePaused || r.stopping || r.final
	})
}

// brakePick is the worker to freeze among cands, nil for none (pickBrake), the admitted ones' grants read only when
// one of them may be picked.
func (h *Harness) brakePick(cands []brakeCandidate, pending bool) *brakeCandidate {
	least := int64(max(h.brake.Growth, 1))
	h.mu.Lock()
	grants := h.grants
	h.mu.Unlock()
	if grants != nil && slices.ContainsFunc(cands, func(c brakeCandidate) bool { return c.admitted && c.growth >= least }) {
		at := grants()
		for i := range cands {
			cands[i].granted = at[cands[i].id]
		}
	}
	return pickBrake(cands, least, pending)
}

// pickBrake is the worker to freeze among cands, nil for none. A worker grows when its memory rose least or more over
// the window. The one that grew most outside the gates goes first; never an admitted worker while another grows, nor
// while one frozen for a growth outside the gates waits for its interruption (pending): only when admitted workers
// alone grow, the last admitted, the one that grew most when the grants are not known. A worker a git runs in waits
// for the next pass: frozen, it would hold the repository's lock.
func pickBrake(cands []brakeCandidate, least int64, pending bool) *brakeCandidate {
	var out, in *brakeCandidate
	escapes := pending
	for i := range cands {
		c := &cands[i]
		if c.growth < least {
			continue
		}
		if !c.admitted {
			escapes = true
			if !c.git && (out == nil || c.growth > out.growth || c.growth == out.growth && c.code < out.code) {
				out = c
			}
			continue
		}
		if c.git {
			continue
		}
		if in == nil || c.granted.After(in.granted) ||
			c.granted.Equal(in.granted) && (c.growth > in.growth || c.growth == in.growth && c.code < in.code) {
			in = c
		}
	}
	if out != nil || escapes {
		return out // Something grows outside the gates: no admitted worker is frozen while it does.
	}
	return in
}

// brakeFreeze freezes the candidate's worker and its containers, and tells its lead.
func (h *Harness) brakeFreeze(ctx context.Context, c *brakeCandidate, short string, now time.Time) {
	st := &h.brakeAt
	why := whyBrake + short
	if c.admitted {
		why += "; only admitted commands grow, this one admitted last: never interrupted"
	} else {
		why += fmt.Sprintf("; it grew %s in %s outside the gates", wrap.Size(uint64(max(c.growth, 0))), span(h.brake.Window))
	}
	if _, err := h.hold(ctx, methodBrake, &planv1.TaskServicePauseRequest{TaskId: c.id}, c.id, true, why); err != nil {
		logx.L().Warn("the memory brake cannot freeze a worker", "task", c.code, "err", err)
		return
	}
	f := &brakeFrozen{id: c.id, code: c.code, wish: c.wish, at: now, admitted: c.admitted, memory: c.memory}
	h.mu.Lock()
	if r := h.runs[c.id]; r != nil && r.shown != nil {
		f.kept = !c.admitted && !plan.CanResume(r.shown.GetProvider())
	}
	h.mu.Unlock()
	f.containers = h.pauseContainers(ctx, c.id, true) > 0
	st.frozen = append(st.frozen, f)
	st.froze = now
	logx.L().Warn("the memory brake froze a worker", "task", c.code, "why", why, "containers", f.containers)
	line := fmt.Sprintf("%s frozen by the memory brake: %s; it grew %s in %s outside the gates. Interrupted in %s if it lasts.",
		c.code, short, wrap.Size(uint64(max(c.growth, 0))), span(h.brake.Window), span(h.brake.Stop))
	switch {
	case c.admitted:
		line = fmt.Sprintf("%s frozen by the memory brake: %s; only admitted commands grow, %s's last. Never interrupted: thawed once calm.",
			c.code, short, c.code)
	case f.kept:
		line = fmt.Sprintf("%s frozen by the memory brake: %s; it grew %s in %s outside the gates. Its agent cannot resume: thawed once calm.",
			c.code, short, wrap.Size(uint64(max(c.growth, 0))), span(h.brake.Window))
	}
	h.brakeTell(c.wish, c.code, line)
}

// brakeThaw thaws the last worker the brake froze, its containers first, and tells its lead.
func (h *Harness) brakeThaw(ctx context.Context, s machine.Snapshot, now time.Time) {
	st := &h.brakeAt
	f := st.frozen[len(st.frozen)-1]
	st.frozen = st.frozen[:len(st.frozen)-1]
	calm := "the memory is calm again"
	if s.MemoryTotal > 0 {
		calm += " (" + percent(s.MemoryAvailable, s.MemoryTotal) + " available)"
	}
	if f.containers {
		h.pauseContainers(ctx, f.id, false)
	}
	if _, err := h.hold(ctx, methodBrake, &planv1.TaskServiceResumeRequest{TaskId: f.id}, f.id, false, whyBrake+calm); err != nil {
		logx.L().Warn("the memory brake cannot thaw a worker", "task", f.code, "err", err)
		return
	}
	logx.L().Info("the memory brake thawed a worker", "task", f.code, "frozen", now.Sub(f.at))
	kept := ""
	if f.admitted {
		kept = ", its command's peak kept by the gates"
	}
	h.brakeTell(f.wish, f.code, fmt.Sprintf("%s thawed: %s. Frozen %s at %s%s, left out of its budget.",
		f.code, calm, span(now.Sub(f.at)), wrap.Size(f.memory), kept))
}

// brakeInterrupt interrupts the worker f the brake froze for a growth outside the gates, the memory still short: its
// task resumes later in its session. One the gates admitted meanwhile, or that cannot resume, stays frozen.
func (h *Harness) brakeInterrupt(ctx context.Context, f *brakeFrozen, now time.Time) {
	st := &h.brakeAt
	why := fmt.Sprintf("%sfrozen %s, the memory stayed short: it resumes in its session once the machine allows",
		whyBrake, span(now.Sub(f.at)))
	h.mu.Lock()
	r := h.runs[f.id]
	switch {
	case r == nil || !r.brakePaused || r.stopping || r.final || r.worker == nil:
		h.mu.Unlock()
		return // Gone, or resumed: brakeForget drops it.
	case h.held != nil && len(h.held(f.id)) > 0:
		f.admitted = true // A command of its holds its admission: never interrupted.
		h.mu.Unlock()
		return
	case r.shown == nil || !plan.CanResume(r.shown.GetProvider()):
		f.kept = true
		h.mu.Unlock()
		return
	}
	r.preemptedWhy = why
	r.stopping = true
	if p, ok := r.worker.(Pauser); ok {
		_ = p.Resume()
	}
	r.worker.Stop()
	h.mu.Unlock()
	st.frozen = slices.DeleteFunc(st.frozen, func(o *brakeFrozen) bool { return o == f })
	st.froze = now // What it held goes back to the machine: the next freeze waits for the readings to show it.
	if err := h.store.Tx(ctx, func(tx *store.Tx) error {
		return tx.Journal(actorHarness, methodBrake, &planv1.TaskServiceStopRequest{TaskId: f.id})
	}); err != nil {
		logx.L().Error("journal the brake's interruption", "task", f.code, "err", err)
	}
	logx.L().Warn("the memory brake interrupted a worker", "task", f.code, "frozen", now.Sub(f.at))
	h.brakeTell(f.wish, f.code, fmt.Sprintf("%s interrupted by the memory brake: frozen %s, the memory stayed short; "+
		"it resumes in its session once the machine allows.", f.code, span(now.Sub(f.at))))
}

// pauseContainers freezes or thaws the task's containers, when it runs any: it says how many.
func (h *Harness) pauseContainers(ctx context.Context, taskID string, pause bool) int {
	if h.containers.Pause == nil {
		return 0
	}
	if h.containers.Read != nil {
		if _, err := h.containers.Read(taskID); err != nil {
			return 0 // No slice: no container.
		}
	}
	return h.containers.Pause(ctx, pause, wrap.TaskLabel+"="+taskID, wrap.HomeLabel+"="+h.home)
}

// brakeTell tells the lead of the wish what the brake did to the worker code, apart from the pass.
func (h *Harness) brakeTell(wishID, code, line string) {
	go func() {
		defer logx.Crash("tell the brake", "task", code)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := h.toLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_HEALTH, wishID, code, line); err != nil &&
			!errors.Is(err, plan.ErrNoLead) {
			logx.L().Warn("tell the lead of the brake", "wish", wishID, "task", code, "err", err)
		}
	}()
}

// span writes d as a person reads it: 30 s, 3 min.
func span(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d.Round(time.Second).Seconds()))
	}
	return fmt.Sprintf("%d min", int(d.Round(time.Minute).Minutes()))
}
