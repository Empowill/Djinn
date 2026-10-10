package plan

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// watchInterval is the least time between two messages of a Watch stream: the changes in between come as one.
var watchInterval = 100 * time.Millisecond

// SetWatchInterval sets the least time between two messages of a Watch stream, for tests and benchmarks, and
// returns what puts it back.
func SetWatchInterval(d time.Duration) (restore func()) {
	old := watchInterval
	watchInterval = d
	return func() { watchInterval = old }
}

// maxChanged is the most tasks, questions and blocks of a wish one message carries: past it, the reader reads them
// all again, which costs less than reading each.
const maxChanged = 200

// everything is what the first message of a stream names: read it all.
var everything = []planv1.Change{
	planv1.Change_CHANGE_WISH, planv1.Change_CHANGE_TASK, planv1.Change_CHANGE_QUESTION,
	planv1.Change_CHANGE_BLOCK, planv1.Change_CHANGE_PROJECT, planv1.Change_CHANGE_INBOX, planv1.Change_CHANGE_TILASM,
	planv1.Change_CHANGE_LOAD,
}

// watchers fans the committed changes of the store out to the open Watch streams. Its zero value is ready: it
// listens to the store from the first stream on.
type watchers struct {
	once sync.Once
	mu   sync.Mutex
	subs map[*watcher]struct{}
}

// watcher is one stream: the changes not sent yet, by wish ("" for the projects).
type watcher struct {
	wishID  string // empty for every wish
	mu      sync.Mutex
	pending map[string]*unsent
	kick    chan struct{}
	// shapes are the tasks of the wishes whose tasks changed, by wish then by id, as they decide the wish's azimas
	// and whether it is ready: only the stream's goroutine touches them.
	shapes map[string]map[string]*planv1.Task
}

// unsent is what changed in a wish since the stream's last message: the kinds, and the tasks, questions and blocks
// by id.
type unsent struct {
	kinds                    map[planv1.Change]bool
	tasks, questions, blocks map[string]bool
	load                     *djinnv1.LoadNotch
}

// Watch follows the changes of the wishes: a first message that names everything, then the changes, coalesced, with
// the tasks, questions and blocks that changed.
func (w *Wishes) Watch(
	ctx context.Context, req *connect.Request[planv1.WishServiceWatchRequest],
	stream *connect.ServerStream[planv1.WishServiceWatchResponse],
) error {
	// The validating interceptor only sees unary calls.
	if err := protovalidate.Validate(req.Msg); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	sub := w.watch.add(w, req.Msg.GetWishId())
	defer w.watch.remove(sub)
	initial := &planv1.WishServiceWatchResponse{Changes: everything}
	if w.Load != nil {
		n := w.Load()
		initial.Load = &n
	}
	if err := stream.Send(initial); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sub.kick:
		}
		for _, msg := range sub.take(ctx, w.Store) {
			if err := stream.Send(msg); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(watchInterval):
		}
	}
}

func (ws *watchers) add(w *Wishes, wishID string) *watcher {
	ws.once.Do(func() { w.Store.OnCommit(ws.changed) })
	sub := &watcher{
		wishID: wishID, pending: map[string]*unsent{}, kick: make(chan struct{}, 1),
		shapes: map[string]map[string]*planv1.Task{},
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.subs == nil {
		ws.subs = map[*watcher]struct{}{}
	}
	ws.subs[sub] = struct{}{}
	return sub
}

func (ws *watchers) remove(sub *watcher) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	delete(ws.subs, sub)
}

// changed notes what a committed transaction touched, for every stream. It runs in the writer's goroutine: it only
// marks, and each stream reads and sends.
func (ws *watchers) changed(ms []proto.Message) {
	type change struct {
		wish string
		kind planv1.Change
		id   string // the task, question or block
	}
	var changes []change
	for _, m := range ms {
		switch m := m.(type) {
		case *planv1.Wish:
			changes = append(changes, change{m.GetId(), planv1.Change_CHANGE_WISH, ""})
		case *planv1.Task:
			// Whether Djinn proposes to grant the wish follows its tasks: take says whether this one changes it.
			changes = append(changes, change{m.GetWishId(), planv1.Change_CHANGE_TASK, m.GetId()})
		case *planv1.Question:
			// And its questions.
			changes = append(changes, change{m.GetWishId(), planv1.Change_CHANGE_QUESTION, m.GetId()},
				change{m.GetWishId(), planv1.Change_CHANGE_WISH, ""})
		case *planv1.Block:
			changes = append(changes, change{m.GetWishId(), planv1.Change_CHANGE_BLOCK, m.GetId()})
		case *planv1.Tilasm:
			changes = append(changes, change{m.GetWishId(), planv1.Change_CHANGE_TILASM, ""})
		case *planv1.Project:
			// The inbox's sources come from the projects' skills.
			changes = append(changes, change{"", planv1.Change_CHANGE_PROJECT, ""}, change{"", planv1.Change_CHANGE_INBOX, ""})
		case *planv1.InboxItem, *planv1.PluggedSource:
			changes = append(changes, change{"", planv1.Change_CHANGE_INBOX, ""})
		}
	}
	if len(changes) == 0 {
		return
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	for sub := range ws.subs {
		marked := false
		sub.mu.Lock()
		for _, c := range changes {
			wish := c.wish
			if sub.wishID != "" && wish != "" && !strings.EqualFold(wish, sub.wishID) {
				continue
			}
			p := sub.pending[wish]
			if p == nil {
				p = &unsent{kinds: map[planv1.Change]bool{}}
				sub.pending[wish] = p
			}
			p.kinds[c.kind] = true
			switch c.kind {
			case planv1.Change_CHANGE_TASK:
				p.tasks = mark(p.tasks, c.id)
			case planv1.Change_CHANGE_QUESTION:
				p.questions = mark(p.questions, c.id)
			case planv1.Change_CHANGE_BLOCK:
				p.blocks = mark(p.blocks, c.id)
			}
			marked = true
		}
		sub.mu.Unlock()
		if marked {
			select {
			case sub.kick <- struct{}{}:
			default: // Already kicked.
			}
		}
	}
}

// changeLoad notes that Djinn's load notch changed, for every open stream.
func (ws *watchers) changeLoad(notch djinnv1.LoadNotch) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	for sub := range ws.subs {
		sub.mu.Lock()
		p := sub.pending[""]
		if p == nil {
			p = &unsent{kinds: map[planv1.Change]bool{}}
			sub.pending[""] = p
		}
		p.kinds[planv1.Change_CHANGE_LOAD] = true
		n := notch
		p.load = &n
		sub.mu.Unlock()
		select {
		case sub.kick <- struct{}{}:
		default: // Already kicked.
		}
	}
}

// ChangeLoad tells the open Watch streams that Djinn's load notch changed.
func (w *Wishes) ChangeLoad(notch djinnv1.LoadNotch) {
	w.watch.changeLoad(notch)
}

func mark(ids map[string]bool, id string) map[string]bool {
	if ids == nil {
		ids = map[string]bool{}
	}
	ids[id] = true
	return ids
}

// take empties what the stream has not sent yet, one message per wish, in a stable order, with the tasks, questions
// and blocks that changed read from s.
func (sub *watcher) take(ctx context.Context, s *store.Store) []*planv1.WishServiceWatchResponse {
	sub.mu.Lock()
	all := sub.pending
	sub.pending = map[string]*unsent{}
	sub.mu.Unlock()
	out := make([]*planv1.WishServiceWatchResponse, 0, len(all))
	for _, wish := range slices.Sorted(maps.Keys(all)) {
		p := all[wish]
		msg := &planv1.WishServiceWatchResponse{WishId: wish}
		reshaped := false
		if wish != "" && len(p.tasks)+len(p.questions)+len(p.blocks) > 0 {
			var err error
			if msg.Changed, reshaped, err = sub.read(ctx, s, wish, p); err != nil {
				// The reader reads them again; so do the shapes.
				delete(sub.shapes, wish)
			}
		}
		if reshaped || msg.Changed == nil && len(p.tasks) > 0 {
			p.kinds[planv1.Change_CHANGE_WISH] = true
		}
		msg.Changes = slices.Sorted(maps.Keys(p.kinds))
		if p.load != nil {
			msg.Load = p.load
		}
		out = append(out, msg)
	}
	return out
}

// errTooMany is read's error when more changed than a message carries.
var errTooMany = errors.New("too many changes for one message")

// read reads the tasks, questions and blocks of a wish that changed, as their services list them; a deleted one
// gives its id. reshaped says whether a task changed what decides whether the wish is ready.
func (sub *watcher) read(ctx context.Context, s *store.Store, wishID string, p *unsent) (
	changed *planv1.WishChanges, reshaped bool, err error,
) {
	if len(p.tasks)+len(p.questions)+len(p.blocks) > maxChanged {
		return nil, false, errTooMany
	}
	changed = &planv1.WishChanges{}
	if len(p.tasks) > 0 {
		var deleted []string
		if changed.Tasks, deleted, reshaped, err = sub.readTasks(ctx, s, wishID, p.tasks); err != nil {
			return nil, false, err
		}
		changed.Deleted = append(changed.Deleted, deleted...)
	}
	if changed.Questions, err = readAll[*planv1.Question](ctx, s, p.questions, &changed.Deleted); err != nil {
		return nil, false, err
	}
	if changed.Blocks, err = readAll[*planv1.Block](ctx, s, p.blocks, &changed.Deleted); err != nil {
		return nil, false, err
	}
	return changed, reshaped, nil
}

// readAll reads the entities of ids, in their order; those no longer there go to deleted.
func readAll[T proto.Message](ctx context.Context, s *store.Store, ids map[string]bool, deleted *[]string) ([]T, error) {
	var out []T
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		m, err := store.Get[T](ctx, s, id)
		switch {
		case errors.Is(err, store.ErrNotFound):
			*deleted = append(*deleted, id)
		case err != nil:
			return nil, err
		default:
			out = append(out, m)
		}
	}
	return out, nil
}

// readTasks reads the tasks of ids, as TaskService.List gives them: with their azima and the tilasms that cite them.
// When one changes what decides the azimas, the azimas that changed with it come too; reshaped says so.
func (sub *watcher) readTasks(ctx context.Context, s *store.Store, wishID string, ids map[string]bool) (
	tasks []*planv1.Task, deleted []string, reshaped bool, err error,
) {
	shapes := sub.shapes[wishID]
	// The azimas as the reader has them; all of them when the shapes are new, as Djinn cannot tell which changed.
	before := map[string]*planv1.Azima{}
	if shapes == nil {
		// Once per wish and stream: every task's shape, and its azimas.
		all, err := store.List[*planv1.Task](ctx, s, store.Where{"wish_id": wishID})
		if err != nil {
			return nil, nil, false, err
		}
		shapes = make(map[string]*planv1.Task, len(all))
		for _, t := range all {
			shapes[t.GetId()] = shape(t)
		}
		FillAzimas(slices.Collect(maps.Values(shapes)))
		sub.shapes[wishID] = shapes
		for id, t := range shapes {
			if IsAzima(t) {
				before[id] = nil
			}
		}
		reshaped = true
	} else {
		for id, t := range shapes {
			if IsAzima(t) {
				before[id] = t.GetAzima()
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		t, err := store.Get[*planv1.Task](ctx, s, id)
		switch {
		case errors.Is(err, store.ErrNotFound):
			deleted = append(deleted, id)
			if shapes[id] != nil {
				delete(shapes, id)
				reshaped = true
			}
			continue
		case err != nil:
			return nil, nil, false, err
		}
		if old := shapes[id]; old == nil || !sameShape(old, t) {
			shapes[id] = shape(t)
			reshaped = true
		}
		tasks = append(tasks, t)
	}
	if reshaped {
		FillAzimas(slices.Collect(maps.Values(shapes)))
		for _, id := range slices.Sorted(maps.Keys(before)) {
			now := shapes[id]
			if ids[id] || now == nil || before[id] != nil && proto.Equal(before[id], now.GetAzima()) {
				continue
			}
			t, err := store.Get[*planv1.Task](ctx, s, id)
			if err != nil {
				return nil, nil, false, err
			}
			tasks = append(tasks, t)
		}
	}
	// In the store's order, as TaskService.List gives them.
	slices.SortFunc(tasks, func(a, b *planv1.Task) int { return strings.Compare(a.GetId(), b.GetId()) })
	for _, t := range tasks {
		if IsAzima(t) {
			t.Azima = proto.CloneOf(shapes[t.GetId()].GetAzima())
		}
	}
	if err := FillTilasms(ctx, s, tasks); err != nil {
		return nil, nil, false, err
	}
	return tasks, deleted, reshaped, nil
}

// shape is what of a task decides its wish's azimas and whether the wish is ready.
func shape(t *planv1.Task) *planv1.Task {
	return &planv1.Task{
		Id: t.GetId(), Status: t.GetStatus(), Kind: t.GetKind(), PartOf: t.GetPartOf(), DependsOn: t.GetDependsOn(),
		Code: t.GetCode(), Title: t.GetTitle(), Draft: t.GetDraft(),
	}
}

// sameShape tells whether the task t has the shape old.
func sameShape(old, t *planv1.Task) bool {
	return old.GetStatus() == t.GetStatus() && old.GetKind() == t.GetKind() && old.GetPartOf() == t.GetPartOf() &&
		slices.Equal(old.GetDependsOn(), t.GetDependsOn()) &&
		old.GetCode() == t.GetCode() && old.GetTitle() == t.GetTitle() && old.GetDraft() == t.GetDraft()
}
