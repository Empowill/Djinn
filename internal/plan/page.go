package plan

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/render"
	"github.com/empowill/djinn/internal/store"
)

// PagesDir is the folder of the synced pages in the data directory: one folder per wish, named by its id, holding
// page.html.
const PagesDir = "wishes"

// PageFile is the name of a wish's synced page.
const PageFile = "page.html"

// dataName stands for Djinn's data folder on a page.
const dataName = "djinn-data"

// Pages renders the pages of wishes, and keeps the synced ones up to date: every committed change of a synced wish
// renders its page again, at most once an Interval. A page is synced while its file exists: djinn up takes back the
// pages it finds when it starts, and deleting a file stops it.
type Pages struct {
	store *store.Store
	// Home is the data directory, left out of the pages, which hold the synced ones.
	Home string
	// Version of Djinn, shown on the pages.
	Version string
	// Language of the pages' own texts.
	Language string
	// Interval is the least time between two renders of the synced pages: pageInterval by default.
	Interval time.Duration

	mu       sync.Mutex
	synced   map[string]bool   // wishes whose page is kept up to date
	dirty    map[string]bool   // synced wishes changed since their last render
	tasks    map[string]bool   // tasks with new events, whose wish is not known yet
	taskWish map[string]string // task → wish, as the changes taught it
	kick     chan struct{}

	write sync.Mutex // one render at a time: the last file written holds the last state
}

// pageInterval is the least time between two renders of a synced page. Its workers speak every second or so, and each
// render reads the wish, its journal and its latest events again: a page a few seconds old costs a fifth of one kept
// to the second, and its reader, who opens a file the lead publishes, does not see the difference.
const pageInterval = 5 * time.Second

// foreignEvents is how many events of other wishes pageEvents reads from the last back, at most, before it reads each
// task's own instead.
const foreignEvents = 2000

// NewPages returns the pages of the wishes in s, and starts listening to its changes. Run keeps them up to date.
func NewPages(s *store.Store, home, version string) *Pages {
	p := &Pages{
		store: s, Home: home, Version: version, Language: render.SystemLanguage(), Interval: pageInterval,
		synced: map[string]bool{}, dirty: map[string]bool{}, tasks: map[string]bool{}, taskWish: map[string]string{},
		kick: make(chan struct{}, 1),
	}
	s.OnCommit(p.changed)
	return p
}

// WithPages gives the wishes their pages, for WishService.Render and Sync.
func WithPages(p *Pages) Option { return func(o *options) { o.pages = p } }

// File is where the synced page of a wish is.
func (p *Pages) File(wishID string) string {
	return filepath.Join(p.Home, PagesDir, strings.ToLower(wishID), PageFile)
}

// Page renders the page of a wish, without secret nor local path.
func (p *Pages) Page(ctx context.Context, wishID string) ([]byte, error) {
	return p.page(ctx, wishID, pageEvents, time.Now())
}

// page renders the page of a wish at now, from the events that events reads.
func (p *Pages) page(ctx context.Context, wishID string, events eventsOf, now time.Time) ([]byte, error) {
	exp, projects, err := collect(ctx, p.store, wishID, events)
	if err != nil {
		return nil, err
	}
	all, err := store.List[*planv1.Project](ctx, p.store, nil)
	if err != nil {
		return nil, err
	}
	// The page shows where the wish stands here, which an export leaves out: its rank, and whether Djinn proposes
	// to grant it.
	wish := exp.GetWish()
	rank := wish.GetRank()
	ready := wish.GetState() != planv1.WishState_WISH_STATE_GRANTED && Ready(exp.GetTasks(), exp.GetQuestions())
	exp = portable(exp, newScrubber(all, p.Home, dataName))
	exp.Wish.Rank, exp.Wish.Ready = rank, ready
	var unattached []string
	for _, project := range projects {
		if project.GetDirectory() == "" {
			unattached = append(unattached, project.GetName())
		}
	}
	return render.Page(render.Input{
		Export: exp, Unattached: unattached, Version: p.Version, Language: p.Language, Now: now,
	})
}

// pageEvents are the events of tasks that a page shows (render.Shown): the render.MaxEvents last ones, and each task's
// last one, its last word. A wish's events are most of the store and a page renders each time a worker speaks: they are
// read from the last written back, no further than the page needs. When other wishes' events come first, past
// foreignEvents of them, each task's are read apart instead, their render.MaxEvents last at most.
func pageEvents(ctx context.Context, r store.Reader, tasks []*planv1.Task) ([]*planv1.TaskEvent, error) {
	pos := make(map[string]int, len(tasks))
	for i, t := range tasks {
		pos[strings.ToLower(t.GetId())] = i
	}
	picked := map[string]*planv1.TaskEvent{}
	spoke := map[string]bool{} // the tasks whose last word is picked
	foreign := 0
	err := store.Latest(ctx, r, nil, func(e *planv1.TaskEvent) bool {
		task := strings.ToLower(e.GetTaskId())
		if _, ok := pos[task]; !ok {
			foreign++
		} else if render.Shown(e) {
			picked[e.GetId()], spoke[task] = e, true
		}
		return len(picked) < render.MaxEvents && foreign <= foreignEvents
	})
	if err != nil {
		return nil, err
	}
	most := 1 // each task's last word
	if foreign > foreignEvents && len(picked) < render.MaxEvents {
		most, spoke = render.MaxEvents, nil
	}
	for _, t := range tasks {
		if spoke[strings.ToLower(t.GetId())] {
			continue
		}
		n := 0
		err := store.Latest(ctx, r, store.Where{"task_id": t.GetId()}, func(e *planv1.TaskEvent) bool {
			if render.Shown(e) {
				picked[e.GetId()] = e
				n++
			}
			return n < most
		})
		if err != nil {
			return nil, err
		}
	}
	out := make([]*planv1.TaskEvent, 0, len(picked))
	for _, e := range picked {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b *planv1.TaskEvent) int {
		if d := pos[strings.ToLower(a.GetTaskId())] - pos[strings.ToLower(b.GetTaskId())]; d != 0 {
			return d
		}
		return int(a.GetSeq() - b.GetSeq())
	})
	return out, nil
}

// Sync renders the page of a wish in its file, and keeps it up to date from now on.
func (p *Pages) Sync(ctx context.Context, wishID string) (string, error) {
	id := strings.ToLower(wishID)
	if err := p.render(ctx, id, true); err != nil {
		return "", err
	}
	p.mu.Lock()
	p.synced[id] = true
	p.mu.Unlock()
	return p.File(id), nil
}

// render writes the page of a synced wish. Unless create is set, a page whose file is gone is not written again,
// and its wish no longer synced.
func (p *Pages) render(ctx context.Context, id string, create bool) error {
	p.write.Lock()
	defer p.write.Unlock()
	file := p.File(id)
	if !create {
		if _, err := os.Stat(file); errors.Is(err, fs.ErrNotExist) {
			p.forget(id)
			return nil
		}
	}
	data, err := p.Page(ctx, id)
	if errors.Is(err, store.ErrNotFound) && !create {
		p.forget(id)
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	return writeFile(file, data)
}

func (p *Pages) forget(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.synced, id)
	delete(p.dirty, id)
}

// changed notes the synced wishes a committed transaction touched. It runs in the writer's goroutine: it only
// marks them, and Run renders.
func (p *Pages) changed(ms []proto.Message) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.synced) == 0 {
		return
	}
	mark := func(wishID string) {
		if id := strings.ToLower(wishID); p.synced[id] {
			p.dirty[id] = true
		}
	}
	for _, m := range ms {
		switch m := m.(type) {
		case *planv1.Wish:
			mark(m.GetId())
		case *planv1.Task:
			p.taskWish[strings.ToLower(m.GetId())] = m.GetWishId()
			mark(m.GetWishId())
		case *planv1.TaskEvent:
			if wishID, ok := p.taskWish[strings.ToLower(m.GetTaskId())]; ok {
				mark(wishID)
			} else {
				p.tasks[strings.ToLower(m.GetTaskId())] = true
			}
		case *planv1.Question:
			mark(m.GetWishId())
		case *planv1.Block:
			mark(m.GetWishId())
		case *planv1.Project:
			// A project's name or folder shows on the pages of its wishes: few changes, all pages.
			for id := range p.synced {
				p.dirty[id] = true
			}
		}
	}
	if len(p.dirty) > 0 || len(p.tasks) > 0 {
		select {
		case p.kick <- struct{}{}:
		default: // Already kicked.
		}
	}
}

// Run keeps the synced pages up to date until ctx ends. It first takes back the pages of the data directory, and
// renders them again.
func (p *Pages) Run(ctx context.Context) {
	entries, err := os.ReadDir(filepath.Join(p.Home, PagesDir))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("djinn: pages: %v", err)
	}
	p.mu.Lock()
	for _, e := range entries {
		id := strings.ToLower(e.Name())
		if _, err := os.Stat(p.File(id)); e.IsDir() && err == nil {
			p.synced[id], p.dirty[id] = true, true
		}
	}
	p.mu.Unlock()
	select {
	case p.kick <- struct{}{}:
	default:
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.kick:
		}
		p.flush(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.Interval):
		}
	}
}

// flush renders the pages of the wishes changed since the last flush.
func (p *Pages) flush(ctx context.Context) {
	p.mu.Lock()
	tasks := p.tasks
	p.tasks = map[string]bool{}
	p.mu.Unlock()
	for id := range tasks {
		t, err := store.Get[*planv1.Task](ctx, p.store, id)
		if err != nil {
			continue
		}
		p.changed([]proto.Message{t})
	}
	p.mu.Lock()
	dirty := p.dirty
	p.dirty = map[string]bool{}
	p.mu.Unlock()
	for id := range dirty {
		if err := p.render(ctx, id, false); err != nil && ctx.Err() == nil {
			log.Printf("djinn: render the page of wish %s: %v", id, err)
		}
	}
}

// Render writes the page of a wish to a file.
func (w *Wishes) Render(
	ctx context.Context, req *connect.Request[planv1.WishServiceRenderRequest],
) (*connect.Response[planv1.WishServiceRenderResponse], error) {
	pages := w.Pages
	if pages == nil {
		pages = &Pages{store: w.Store, Language: render.SystemLanguage()}
	}
	data, err := pages.Page(ctx, req.Msg.GetWishId())
	if err != nil {
		return nil, Status(err)
	}
	file := req.Msg.GetFile()
	if file == "" {
		wish, err := store.Get[*planv1.Wish](ctx, w.Store, req.Msg.GetWishId())
		if err != nil {
			return nil, Status(err)
		}
		dir, err := Downloads()
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("find the Downloads folder: %w", err))
		}
		file = freePath(dir, fileName(wish.GetTitle()), ".html")
	}
	if !filepath.IsAbs(file) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("file: %q is not an absolute path", file))
	}
	if err := writeFile(file, data); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&planv1.WishServiceRenderResponse{File: file, Size: int64(len(data))}), nil
}

// Sync renders the page of a wish in the data directory, and keeps it up to date while djinn up runs.
func (w *Wishes) Sync(
	ctx context.Context, req *connect.Request[planv1.WishServiceSyncRequest],
) (*connect.Response[planv1.WishServiceSyncResponse], error) {
	if w.Pages == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("pages are kept up to date by djinn up only"))
	}
	file, err := w.Pages.Sync(ctx, req.Msg.GetWishId())
	if err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.WishServiceSyncResponse{File: file}), nil
}
