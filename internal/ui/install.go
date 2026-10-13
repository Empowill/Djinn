package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/logx"
)

// How a build installs (W554). An install runs within djinn up's life, not within the call that asked for it: a call
// whose answer is lost, or a window that reloads, leaves it running, and WatchUpdate tells how it ends. It works within
// a deadline, its waits aside: past it, Djinn stops it and says so, with the last lines its command wrote. A build of
// the running Djinn's line (Build.auto) installs by itself, at low priority, one at a time, once the machine is free;
// the restart stays the person's click.

// InstallRun is how an install runs, and what it tells as it runs.
type InstallRun struct {
	// Low runs its commands at a low-priority worker's CPU priority: an install Djinn starts by itself.
	Low bool
	// Progress is told each step as it comes, and what the install waits for while it waits.
	Progress func(step uiv1.InstallStep, waiting string)
	// Output is told each line its commands write, as they write it.
	Output func(line string)
}

const (
	// DefaultInstallLimit is how long an install may work, its waits aside, before Djinn stops it: go tool task install
	// takes from 10 s to a minute, a setup after the lock files changed a few minutes more.
	DefaultInstallLimit = 20 * time.Minute
	// DefaultAutoRetry is how long an install Djinn starts by itself waits before it looks at a busy machine again.
	DefaultAutoRetry = 30 * time.Second
	// failedLines is how many of the last lines a failed install's command wrote the window shows.
	failedLines = 20
)

var (
	errDeadline   = errors.New("past its deadline")
	errSuperseded = errors.New("stopped for the install asked in the window")
	errBusy       = errors.New("another install runs")
)

// job is an install that runs.
type job struct {
	sha    string
	auto   bool
	cancel context.CancelCauseFunc
	done   chan struct{} // closed once it ended: newer and err are set
	newer  bool
	err    error
}

// life is the context the installs run in.
func (s *Service) life() context.Context {
	if s.Life != nil {
		return s.Life
	}
	return context.Background()
}

// install installs build, at the person's click (auto false) or by itself (auto true), and returns whether a newer
// Djinn waits at the path of the running one since. One install runs at a time: the person's click joins the install
// of the same build, stops one Djinn started by itself, and waits for another; an install Djinn starts by itself never
// waits (errBusy). ctx's end ends the wait, not the install.
func (s *Service) install(ctx context.Context, build *uiv1.Build, auto bool) (bool, error) {
	sha := build.GetSha()
	for {
		s.updates.Lock()
		cur := s.job
		if cur == nil {
			j := &job{sha: sha, auto: auto, done: make(chan struct{})}
			var jctx context.Context
			jctx, j.cancel = context.WithCancelCause(s.life())
			s.job = j
			s.updates.Unlock()
			go s.run(jctx, j, build)
			return wait(ctx, j)
		}
		s.updates.Unlock()
		switch {
		case auto:
			return false, errBusy
		case cur.sha == sha:
			return wait(ctx, cur)
		case cur.auto:
			cur.cancel(errSuperseded)
		default:
			s.setInstalling(&uiv1.Installing{
				Sha: sha, Step: uiv1.InstallStep_INSTALL_STEP_WAITING, Waiting: "the install of " + short(cur.sha) + " to end",
			})
		}
		select {
		case <-cur.done:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

// wait waits for j to end, or ctx.
func wait(ctx context.Context, j *job) (bool, error) {
	select {
	case <-j.done:
		return j.newer, j.err
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// run runs the install j of build, telling the window each step, and how it ended: the build proposed no more once
// installed, what failed and the last lines its command wrote otherwise.
func (s *Service) run(ctx context.Context, j *job, build *uiv1.Build) {
	defer logx.Crash("install", "sha", short(j.sha))
	start := time.Now()
	logx.L().Info("install starts", "sha", short(j.sha), "branch", build.GetBranch(), "auto", j.auto)
	limit := cmp.Or(s.InstallLimit, DefaultInstallLimit)
	var mu sync.Mutex
	state := &uiv1.Installing{Sha: j.sha, Step: uiv1.InstallStep_INSTALL_STEP_PREPARING, Auto: j.auto}
	// The deadline counts the time it works, from the start (a harness that never says its first step included), and
	// stops while it waits for its turn.
	var deadline *time.Timer
	left, since := limit, time.Time{}
	tell := func(change func(in *uiv1.Installing)) {
		mu.Lock()
		change(state)
		switch working := state.GetStep() != uiv1.InstallStep_INSTALL_STEP_WAITING; {
		case working && deadline == nil:
			since, deadline = time.Now(), time.AfterFunc(left, func() { j.cancel(errDeadline) })
		case !working && deadline != nil:
			deadline.Stop()
			left, deadline = left-time.Since(since), nil
		}
		in := proto.CloneOf(state)
		mu.Unlock()
		s.setInstalling(in)
	}
	tell(func(*uiv1.Installing) {})
	newer, out, err := s.Install(ctx, build, InstallRun{
		Low: j.auto,
		Progress: func(step uiv1.InstallStep, waiting string) {
			tell(func(in *uiv1.Installing) { in.Step, in.Waiting = step, waiting })
		},
		Output: func(line string) {
			if doing := stepOf(line); doing != "" {
				tell(func(in *uiv1.Installing) { in.Doing = doing })
			}
		},
	})
	mu.Lock()
	if deadline != nil {
		deadline.Stop()
	}
	doing := state.GetDoing()
	mu.Unlock()
	cause := context.Cause(ctx)
	if err != nil && errors.Is(cause, errDeadline) {
		err = fmt.Errorf("it did not end within %s: Djinn stopped it", limit)
		if doing != "" {
			err = fmt.Errorf("it did not end within %s, at %s: Djinn stopped it", limit, doing)
		}
	}
	j.cancel(nil)

	s.updates.Lock()
	j.newer, j.err = newer, err
	s.job = nil
	if err == nil { // Proposed no more, unless a newer one came meanwhile.
		if i := slices.Index(s.builds, build); i >= 0 {
			s.builds = slices.Delete(s.builds, i, i+1)
			s.notifyUpdate()
		}
	}
	s.updates.Unlock()
	switch {
	case err != nil && errors.Is(cause, errSuperseded):
		s.setInstalling(nil) // The install asked in the window says where it stands.
	case err != nil:
		s.setInstalling(&uiv1.Installing{
			Sha: j.sha, Step: uiv1.InstallStep_INSTALL_STEP_FAILED, Auto: j.auto, Doing: doing, Error: err.Error(),
			Log: lastLines(out, failedLines),
		})
	default:
		// Installed: the newer Djinn is ready, the restart left to Update, or to the person's click.
		s.setInstalling(nil)
	}
	if err != nil {
		logx.L().Warn("install failed", "sha", short(j.sha), "branch", build.GetBranch(), "auto", j.auto,
			"took", time.Since(start).Round(time.Millisecond), "err", err)
	} else {
		logx.L().Info("install ended", "sha", short(j.sha), "branch", build.GetBranch(), "auto", j.auto, "newer", newer,
			"took", time.Since(start).Round(time.Millisecond))
	}
	close(j.done)
}

// autoInstall installs build, of the running Djinn's line, by itself, in place of one that waits to: one at a time,
// once the machine is free.
func (s *Service) autoInstall(build *uiv1.Build) {
	s.updates.Lock()
	s.autoNext = build
	start := !s.autoRuns
	s.autoRuns = true
	if s.autoWake == nil {
		s.autoWake = make(chan struct{}, 1)
	}
	wake := s.autoWake
	s.updates.Unlock()
	if start {
		go s.autoLoop()
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

// autoLoop installs the builds autoInstall is given, the latest only, until none waits: each once no other install
// runs and Busy says the machine is free, telling the window what it waits for meanwhile.
func (s *Service) autoLoop() {
	defer logx.Crash("auto install")
	life := s.life()
	retry := cmp.Or(s.AutoRetry, DefaultAutoRetry)
	for {
		s.updates.Lock()
		b, wake := s.autoNext, s.autoWake
		if b == nil || !slices.Contains(s.builds, b) || life.Err() != nil {
			// Installed by hand meanwhile, or replaced by a push of its branch that is not to install by itself.
			s.autoNext, s.autoRuns = nil, false
			if in := s.installing; s.job == nil && in.GetStep() == uiv1.InstallStep_INSTALL_STEP_WAITING && in.GetAuto() {
				s.installing = nil
				s.notifyUpdate()
			}
			s.updates.Unlock()
			return
		}
		why := ""
		if s.job != nil {
			why = "the install of " + short(s.job.sha) + " to end"
		}
		s.updates.Unlock()
		if why == "" && s.Busy != nil {
			why = s.Busy()
		}
		if why == "" {
			_, err := s.install(life, b, true)
			s.updates.Lock()
			if s.autoNext == b && !errors.Is(err, errBusy) {
				s.autoNext = nil // Installed, or failed: the window offers it, and the next push installs by itself.
			}
			s.updates.Unlock()
			if !errors.Is(err, errBusy) {
				continue
			}
		} else if s.idle() {
			s.setInstalling(&uiv1.Installing{Sha: b.GetSha(), Step: uiv1.InstallStep_INSTALL_STEP_WAITING, Waiting: why, Auto: true})
		}
		select {
		case <-life.Done():
		case <-wake:
		case <-time.After(retry):
		}
	}
}

// idle tells that no install runs.
func (s *Service) idle() bool {
	s.updates.Lock()
	defer s.updates.Unlock()
	return s.job == nil
}

// taskLine is a line go-task writes as each of its commands starts: task: [ui] npm run build.
var taskLine = regexp.MustCompile(`^task: \[([^\]]+)\] (.+)$`)

// stepOf is the step of an install a line of its output names, "ui: npm run build"; "" for a line that names none.
func stepOf(line string) string {
	m := taskLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return ""
	}
	return cut(m[1]+": "+m[2], 120)
}

// lastLines is the last n lines of out that are not blank, each cut short.
func lastLines(out string, n int) []string {
	var lines []string
	for line := range strings.SplitSeq(out, "\n") {
		if line = strings.TrimRight(line, " \t\r"); strings.TrimSpace(line) != "" {
			lines = append(lines, cut(line, 300))
		}
	}
	return lines[max(0, len(lines)-n):]
}

// cut is s, cut at n characters with an ellipsis.
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// short is the short form of a commit, as the window shows it.
func short(sha string) string { return sha[:min(8, len(sha))] }
