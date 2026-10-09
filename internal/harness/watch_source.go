package harness

// Inbox sources. A skill declares a command (metadata.djinn.source) that prints what comes from outside: Djinn runs
// it like a watcher, without a task nor a wish, in the folder of the project that holds or summons the skill. Each
// item it prints becomes an item of the inbox (plan.Inbox.Receive). It takes no slot and spends no token; its input
// is closed, and Djinn never writes anything back to it.

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
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

// runSources is RunSources, its watchers made from c and the skills read again every rescan.
func (h *Harness) runSources(receive ReceiveFunc, c Watch, rescan time.Duration) {
	kick := make(chan struct{}, 1)
	h.store.OnCommit(func(ms []proto.Message) {
		for _, m := range ms {
			if _, ok := m.(*planv1.Project); ok {
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
		return
	}
	h.wg.Add(1)
	go h.sources(receive, c, rescan, kick)
}

// sourceRun is a source's command at work.
type sourceRun struct {
	src    *plan.Source
	worker Worker
	done   chan struct{} // closed once its worker has ended
	err    error         // why it ended, once done
}

// sources keeps one watcher per source running: it starts the new ones, and stops those no skill declares any more.
// A source whose command ended (not found, or refused) starts again at the next rescan, its error said once.
func (h *Harness) sources(receive ReceiveFunc, c Watch, rescan time.Duration, kick <-chan struct{}) {
	defer h.wg.Done()
	c.Items = true
	running := map[string]*sourceRun{}
	said := map[string]string{} // the last error said, by source
	say := func(key string, s *plan.Source, err error) {
		if said[key] != err.Error() {
			said[key] = err.Error()
			log.Printf("djinn: the inbox source of the skill %s does not run: %v", s.Skill, err)
		}
	}
	tick := time.NewTicker(rescan)
	defer tick.Stop()
	defer func() {
		for _, r := range running {
			r.worker.Stop()
			<-r.done
		}
	}()
	for {
		want, err := h.readSources()
		if err != nil {
			log.Printf("djinn: read the inbox sources: %v", err)
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
				say(key, s.src, err)
				continue
			}
			running[key] = r
		}
		select {
		case <-h.ctx.Done():
			return
		case <-kick:
		case <-tick.C:
		}
	}
}

// wantedSource is a source and the folder its command runs in.
type wantedSource struct {
	src *plan.Source
	dir string
}

// readSources reads the sources the projects' skills declare, by what makes each one run apart.
func (h *Harness) readSources() (map[string]wantedSource, error) {
	ctx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
	defer cancel()
	projects, err := store.List[*planv1.Project](ctx, h.store, nil)
	if err != nil {
		return nil, err
	}
	srcs, err := plan.Sources(ctx, h.store, projects)
	if err != nil {
		return nil, err
	}
	dirs := map[string]string{}
	for _, p := range projects {
		dirs[p.GetId()] = p.GetDirectory()
	}
	out := map[string]wantedSource{}
	for _, s := range srcs {
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%s", s.Dir, s.ProjectID, s.Watch, s.Every)
		out[key] = wantedSource{src: s, dir: dirs[s.ProjectID]}
	}
	return out, nil
}

// startSource starts the command of a source, as a watcher that starts it again every s.Every, and gives each item
// it prints to receive. Where the project lists the commands its workers may run, the command must be one of them.
func (h *Harness) startSource(c Watch, s wantedSource, receive ReceiveFunc) (*sourceRun, error) {
	perms, err := LoadPermissions(s.dir)
	if err != nil {
		return nil, err
	}
	c.Gap = s.src.Every
	w, err := c.Start(h.ctx, Spec{Dir: s.dir, Prompt: s.src.Watch, Permissions: perms, Scope: h.scope("inbox"), Restart: true})
	if err != nil {
		return nil, err
	}
	r := &sourceRun{src: s.src, worker: w, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		for ev := range w.Events() {
			switch {
			case ev.Watched != nil && ev.Watched.First != "":
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if _, err := receive(ctx, s.src, ev.Text); err != nil {
					log.Printf("djinn: the inbox source of the skill %s: an item was not kept: %v", s.src.Skill, err)
				}
				cancel()
			case ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && ev.Text != "":
				log.Printf("djinn: the inbox source of the skill %s: %s", s.src.Skill, ev.Text)
			}
		}
		r.err = w.Wait().Err
	}()
	return r, nil
}
