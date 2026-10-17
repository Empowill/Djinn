package harness

// Inbox sources. A skill declares a command (metadata.djinn.source) that prints what comes from outside: once the
// developer plugs it in on this machine, Djinn runs it like a watcher, without a task nor a wish, in the folder of the
// project that holds or summons the skill. A source not plugged in never starts. Each item it prints becomes an item
// of the inbox (plan.Inbox.Receive). It takes no slot and spends no token; its input is closed, and Djinn never writes
// anything back to it. Each run's end, how many items it printed and why it failed are kept on this machine
// (plan.KeepSourceRun): djinn inbox sources shows them.

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// sourceRescan is how often the sources are read again from the skills, beside each change of a project: a skill
// edited on disk follows.
const sourceRescan = time.Minute

// ReceiveFunc reads an item an inbox source printed: plan.Inbox.Receive.
type ReceiveFunc func(ctx context.Context, src *plan.Source, text string) (*planv1.InboxItem, error)

// RunSources runs the inbox sources of the projects' skills until Close, and gives each item they print to receive.
// djinn up calls it once.
func (h *Harness) RunSources(receive ReceiveFunc) { h.runSources(receive, Watch{}, sourceRescan) }

// runSources is RunSources, its watchers made from c and the skills read again every rescan. scan reads them again at
// once and returns when that pass is over, its watchers started or stopped: how many sources run then.
func (h *Harness) runSources(receive ReceiveFunc, c Watch, rescan time.Duration) (scan func() int) {
	kick := make(chan struct{}, 1)
	asked := make(chan chan int)
	scan = func() int {
		done := make(chan int, 1)
		select {
		case asked <- done:
		case <-h.ctx.Done():
			return 0
		}
		select {
		case n := <-done:
			return n
		case <-h.ctx.Done():
			return 0
		}
	}
	h.store.OnCommit(func(ms []proto.Message) {
		for _, m := range ms {
			switch m.(type) {
			case *planv1.Project, *planv1.PluggedSource:
				select {
				case kick <- struct{}{}:
				default: // Already kicked.
				}
				return
			}
		}
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return scan
	}
	h.wg.Add(1)
	go h.sources(receive, c, rescan, kick, asked)
	return scan
}

// sourceRun is a source's command at work.
type sourceRun struct {
	src    *plan.Source
	worker Worker
	done   chan struct{} // closed once its worker has ended
	err    error         // why it ended, once done
}

// sources keeps one watcher per plugged source running: it starts the new ones, and stops those unplugged or no skill
// declares any more. A source whose command ended (not found, or refused) starts again at the next rescan, its error said once.
// Each pass answers the scans asked before it began.
func (h *Harness) sources(receive ReceiveFunc, c Watch, rescan time.Duration, kick <-chan struct{}, asked <-chan chan int) {
	defer logx.Crash("inbox")
	defer h.wg.Done()
	c.Items, c.Runs = true, true
	running := map[string]*sourceRun{}
	said := map[string]string{} // the last error said, by source
	say := func(key string, s *plan.Source, err error) (news bool) {
		if said[key] == err.Error() {
			return false
		}
		said[key] = err.Error()
		logx.L().Warn("inbox source does not run", "loop", "inbox", "skill", s.Skill, "err", err)
		return true
	}
	tick := time.NewTicker(rescan)
	defer tick.Stop()
	defer func() {
		for _, r := range running {
			r.worker.Stop()
			<-r.done
		}
	}()
	var waiting []chan int
	for {
		want, err := h.readSources()
		if err != nil {
			logx.L().Error("read the inbox sources", "loop", "inbox", "err", err)
		}
		for key, r := range running {
			select {
			case <-r.done:
				if r.err != nil {
					say(key, r.src, r.err)
				}
				delete(running, key)
				continue
			default:
			}
			if _, ok := want[key]; !ok && err == nil {
				r.worker.Stop()
				<-r.done
				delete(running, key)
			}
		}
		for key, s := range want {
			if running[key] != nil {
				continue
			}
			r, err := h.startSource(c, s, receive)
			if err != nil {
				if say(key, s.src, err) {
					// Each pass tries it again: its run says why once, not once a pass.
					h.keepSourceRun(s.src, time.Now(), 0, err)
				}
				continue
			}
			running[key] = r
			delete(said, key) // It runs: the same error, should it come back, is news again.
		}
		for _, done := range waiting {
			done <- len(running)
		}
		waiting = nil
		select {
		case <-h.ctx.Done():
			return
		case <-kick:
		case <-tick.C:
		case done := <-asked:
			waiting = append(waiting, done)
		}
	}
}

// wantedSource is a source, the folder its command runs in, and the folder its project's permissions are read in.
type wantedSource struct {
	src         *plan.Source
	dir, config string
}

// readSources reads the sources the projects' skills declare and this machine plugged in, by what makes each one run
// apart.
func (h *Harness) readSources() (map[string]wantedSource, error) {
	ctx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
	defer cancel()
	projects, err := store.List[*planv1.Project](ctx, h.store, nil)
	if err != nil {
		return nil, err
	}
	srcs, err := plan.Sources(ctx, h.store, h.home, projects)
	if err != nil {
		return nil, err
	}
	byID := map[string]*planv1.Project{}
	for _, p := range projects {
		byID[p.GetId()] = p
	}
	out := map[string]wantedSource{}
	for _, s := range srcs {
		// The skill, not its folder: a folder of the cache changes with any change of the project's configuration.
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", s.HolderID, s.Skill, s.ProjectID, s.Watch, s.Every)
		p := byID[s.ProjectID]
		out[key] = wantedSource{src: s, dir: p.GetDirectory(), config: plan.ConfigDir(h.home, p)}
	}
	return out, nil
}

// startSource starts the command of a source, as a watcher that starts it again every s.Every, and gives each item
// it prints to receive. Where the project lists the commands its workers may run, the command must be one of them.
func (h *Harness) startSource(c Watch, s wantedSource, receive ReceiveFunc) (*sourceRun, error) {
	perms, err := LoadPermissions(s.config)
	if err != nil {
		return nil, err
	}
	c.Gap, c.Endless = s.src.Every, true
	w, err := c.Start(h.ctx, Spec{Dir: s.dir, Prompt: s.src.Watch, Permissions: perms, Scope: h.scope("inbox"), Restart: true})
	if err != nil {
		return nil, err
	}
	r := &sourceRun{src: s.src, worker: w, done: make(chan struct{})}
	go func() {
		defer logx.Crash("inbox", "skill", s.src.Skill)
		defer close(r.done)
		items := 0 // printed by the run under way
		for ev := range w.Events() {
			switch {
			case ev.Ran != nil:
				h.keepSourceRun(s.src, ev.Ran.End, items, ev.Ran.Err)
				items = 0
			case ev.Watched != nil && ev.Watched.First != "":
				items++
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if _, err := receive(ctx, s.src, ev.Text); err != nil {
					logx.L().Error("keep an inbox item", "loop", "inbox", "skill", s.src.Skill, "err", err)
				}
				cancel()
			case ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && ev.Text != "":
				logx.L().Info("inbox source status", "loop", "inbox", "skill", s.src.Skill, "status", ev.Text)
			}
		}
		r.err = w.Wait().Err
	}()
	return r, nil
}

// keepSourceRun keeps a run of the source src on this machine, as plan.KeepSourceRun does; the log says why it
// could not.
func (h *Harness) keepSourceRun(src *plan.Source, end time.Time, items int, failed error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := plan.KeepSourceRun(ctx, h.store, src, end, items, failed); err != nil {
		logx.L().Error("keep an inbox source's run", "loop", "inbox", "skill", src.Skill, "err", err)
	}
}
