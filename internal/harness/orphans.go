package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/logx"
)

// runDir is the folder of Djinn's data folder where it records each process it starts as long as it runs: a worker,
// a watcher's command, a check run under a gate, and the command a worker runs under a gate (W514). Backups leave it
// out.
const runDir = "run"

// child is a process Djinn started, as recorded in runDir/<pid>.json: what the next djinn up needs to find it left
// running, tell it from a later process with the same PID, and name it.
type child struct {
	PID int `json:"pid"`
	// Group is its process group on Unix, which the process leads when Djinn started it; 0 when unknown.
	Group int `json:"group,omitempty"`
	// Start is when the system started it (procStart): a process with the same PID and another start is not it.
	Start string `json:"start"`
	// Cgroup is the cgroup of its systemd scope, empty without one: it holds even what left the group.
	Cgroup string `json:"cgroup,omitempty"`
	// Mark is the value of markVar in its environment, which what it starts inherits: it finds what left its group
	// and outlived it (startCommand). Empty for a process Djinn did not mark.
	Mark string `json:"mark,omitempty"`
	// Djinn and DjinnStart are the djinn up that started it, and when that one started.
	Djinn      int    `json:"djinn"`
	DjinnStart string `json:"djinn_start"`
	// Task is the code of its task, empty for none; Kind what it is: worker, watcher, gate, serve or a check.
	Task string `json:"task,omitempty"`
	Kind string `json:"kind"`
	// Stop is a serve's stop command, run in Dir once it is stopped, whether it still ran or not: what it started
	// outside its processes, like containers, outlives them (serve.go). Empty for none.
	Stop string `json:"stop,omitempty"`
	Dir  string `json:"dir,omitempty"`
}

// label names the child in the restart event: "W488's watcher", or its kind alone without a task.
func (c child) label() string {
	if c.Task == "" {
		return c.Kind
	}
	return c.Task + "'s " + c.Kind
}

// children records the processes Djinn starts, and guards them: they die with Djinn (the sentinel on Unix, a job
// object on Windows, with PR_SET_PDEATHSIG on Linux), and a djinn up stops at its start what a previous one left
// running all the same (sweep). Its methods do nothing on a nil *children.
type children struct {
	dir   string
	grace time.Duration
	self  identity // the djinn up that records

	mu       sync.Mutex
	guard    guard // started at the first child; nil before
	noGuard  bool  // tests: a djinn up of before W514, whose children outlive it
	closed   bool
	guardErr error // why the guard could not start, logged once
}

// identity is a process: its PID and when it started.
type identity struct {
	pid   int
	start string
}

// guard keeps the children alive no longer than Djinn: told of each one as it starts and ends, it stops those left
// once Djinn has ended, however it ended.
type guard interface {
	add(c child)
	remove(c child)
	close()
}

// newChildren records the children in dir. Without dir (a test without a data folder), nothing is recorded.
func newChildren(dir string, grace time.Duration) *children {
	if dir == "" {
		return nil
	}
	start, _ := procStart(os.Getpid())
	return &children{dir: dir, grace: grace, self: identity{pid: os.Getpid(), start: start}}
}

// track records the process pid of the task code (empty: none), of this kind, that Djinn has just started, in the
// cgroup of its scope (empty: none), with its mark (empty: none). leads says whether it leads a process group of its
// own. It returns what to call once it ended. On a nil *children, it records nothing.
func (cs *children) track(pid int, cgroup, mark, task, kind string, leads bool) (ended func()) {
	return cs.trackChild(child{PID: pid, Cgroup: cgroup, Mark: mark, Task: task, Kind: kind}, leads)
}

// trackServe is track for the process of a serve of the ifrit code, its stop command stop (empty: none) run in dir.
func (cs *children) trackServe(pid int, cgroup, mark, code, stop, dir string) (ended func()) {
	return cs.trackChild(child{PID: pid, Cgroup: cgroup, Mark: mark, Task: code, Kind: "serve", Stop: stop, Dir: dir}, true)
}

// trackChild is track for the child c, its PID, cgroup, mark, task, kind and stop command set.
func (cs *children) trackChild(c child, leads bool) (ended func()) {
	pid := c.PID
	if cs == nil || pid <= 0 {
		return func() {}
	}
	c.Djinn, c.DjinnStart = cs.self.pid, cs.self.start
	c.Start, _ = procStart(pid)
	c.Group = groupOf(pid, leads)
	if err := cs.write(c); err != nil {
		logx.L().Warn("child not recorded", "pid", pid, "kind", c.Kind, "err", err)
	}
	if g := cs.guarded(); g != nil {
		g.add(c)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			if g := cs.guarded(); g != nil {
				g.remove(c)
			}
			if err := os.Remove(cs.file(pid)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				logx.L().Warn("child record not removed", "pid", pid, "err", err)
			}
		})
	}
}

// tracker is track for the processes of one task and kind, as startCommand takes it.
func (cs *children) tracker(task, kind string) func(pid int, cgroup, mark string) func() {
	if cs == nil {
		return nil
	}
	return func(pid int, cgroup, mark string) func() { return cs.track(pid, cgroup, mark, task, kind, true) }
}

// guarded is the guard, started at the first call; nil when it could not start, or once closed.
func (cs *children) guarded() guard {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.closed || cs.noGuard {
		return nil
	}
	if cs.guard == nil && cs.guardErr == nil {
		cs.guard, cs.guardErr = startGuard(cs.grace)
		if cs.guardErr != nil {
			logx.L().Warn("children are not guarded: a forced quit may leave them running", "err", cs.guardErr)
		}
	}
	return cs.guard
}

// close lets the guard go: what is left of the children stops as if Djinn had ended. Djinn calls it once its
// workers have ended.
func (cs *children) close() {
	if cs == nil {
		return
	}
	cs.mu.Lock()
	g := cs.guard
	cs.closed, cs.guard = true, nil
	cs.mu.Unlock()
	if g != nil {
		g.close()
	}
}

func (cs *children) file(pid int) string { return filepath.Join(cs.dir, strconv.Itoa(pid)+".json") }

func (cs *children) write(c child) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cs.dir, 0o700); err != nil {
		return err
	}
	// Whole or not there, as sweep reads it after a kill; no sync: the record matters only as long as the process
	// runs, which a power cut ends too.
	return fsx.WriteAtomicNoSync(cs.file(c.PID), b, 0o600)
}

// sweep stops what a previous djinn up left running: each child recorded whose djinn up is gone, and that still
// runs, the same process (its start time tells it from a later one with its PID), or that left processes with its
// mark running. It asks them to stop, then kills them after the grace delay, and returns their labels. It forgets the
// records of the children that ended; it keeps, and leaves alone, those of a djinn up that still runs. A process
// Djinn did not start is never touched. Then it runs the stop commands of the serves among them, running or ended
// (runStops).
func (cs *children) sweep() []string {
	if cs == nil {
		return nil
	}
	entries, err := os.ReadDir(cs.dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			logx.L().Warn("children of the last djinn up not read", "err", err)
		}
		return nil
	}
	var left, stops []child
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(cs.dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var c child
		if json.Unmarshal(b, &c) != nil || c.PID <= 0 {
			_ = os.Remove(path)
			continue
		}
		if c.Djinn != cs.self.pid && same(identity{pid: c.Djinn, start: c.DjinnStart}) {
			continue // Its djinn up runs: its children are its own.
		}
		if !c.running() {
			_ = os.Remove(path) // It ended, and its PID may be another process's now.
			if c.Stop != "" {
				stops = append(stops, c)
			}
			continue
		}
		left = append(left, c)
	}
	if len(left) == 0 {
		runStops(stops)
		return nil
	}
	for _, c := range left {
		stopChild(c, false)
	}
	deadline := time.Now().Add(cs.grace)
	for time.Now().Before(deadline) && slices.ContainsFunc(left, child.running) {
		time.Sleep(20 * time.Millisecond)
	}
	labels := make([]string, 0, len(left))
	for _, c := range left {
		stopChild(c, true) // Ended or not, what it started may be left in its cgroup.
		labels = append(labels, c.label())
		_ = os.Remove(cs.file(c.PID))
		logx.L().Info("orphan stopped", "pid", c.PID, "kind", c.Kind, "task", c.Task, "djinn", c.Djinn)
		if c.Stop != "" {
			stops = append(stops, c)
		}
	}
	runStops(stops)
	return labels
}

// runStops runs the stop commands of the serves a previous djinn up left, each in its folder, beside the others, for
// serveStopLimit at most: their processes are gone, what they started outside them, like containers, may not be.
func runStops(serves []child) {
	var wg sync.WaitGroup
	for _, c := range serves {
		if info, err := os.Stat(c.Dir); err != nil || !info.IsDir() {
			logx.L().Warn("serve stop command not run: its folder is gone", "task", c.Task, "dir", c.Dir)
			continue
		}
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), serveStopLimit)
			defer cancel()
			_, code, err := runTracked(ctx, c.Dir, shellArgs(c.Stop), nil, nil, false)
			logx.L().Info("serve stop command of the last djinn up run", "task", c.Task, "exit", code, "err", err)
		})
	}
	wg.Wait()
}

// running says whether the child still runs, the same process, or a process with its mark does.
func (c child) running() bool {
	return same(identity{c.PID, c.Start}) || len(procsOf(c.Start, 0, markEntry(c.Mark))) > 0
}

// markVar is the environment variable whose value marks each process Djinn starts, and what it starts in turn
// (startCommand): a daemon that left the worker's group and session, reparented to init, still carries it, and only
// what the worker started does, the value being random. A process that clears its environment loses it.
const markVar = "DJINN_MARK"

// markEntry is the entry of mark in an environment, empty for no mark.
func markEntry(mark string) string {
	if mark == "" {
		return ""
	}
	return markVar + "=" + mark
}

// same says whether the process id still runs, the same one: started when it was. Unknown starts are no proof.
func same(id identity) bool {
	if id.pid <= 0 || id.start == "" {
		return false
	}
	start, err := procStart(id.pid)
	return err == nil && start == id.start
}

// orphansPart is what the restart event says of the orphans stopped: "2 orphans stopped: W488's watcher ×2". Empty
// for none.
func orphansPart(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	counts := map[string]int{}
	var order []string
	for _, l := range labels {
		if counts[l] == 0 {
			order = append(order, l)
		}
		counts[l]++
	}
	slices.Sort(order)
	names := make([]string, len(order))
	for i, l := range order {
		names[i] = l
		if counts[l] > 1 {
			names[i] = fmt.Sprintf("%s ×%d", l, counts[l])
		}
	}
	return fmt.Sprintf("%d %s stopped: %s", len(labels), plural(len(labels), "orphan"), strings.Join(names, ", "))
}

// TrackGate records the process pid, djinn gate run in a worker of the task taskID, as it holds a gate: the command
// it runs, in the group of the worker's shell, may run for long, and must not outlive Djinn. It returns what to call
// once the gate is given back. A gate taken outside a running worker's task is not recorded.
func (h *Harness) TrackGate(pid int, taskID string) (given func()) {
	h.mu.Lock()
	code := ""
	if r := h.runs[taskID]; r != nil && r.worker != nil {
		code = r.code
	}
	h.mu.Unlock()
	if code == "" {
		return func() {}
	}
	return h.children.track(pid, "", "", code, "gate", false)
}
