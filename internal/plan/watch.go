package plan

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// watchInterval is the least time between two messages of a Watch stream: the changes in between come as one.
var watchInterval = 100 * time.Millisecond

// everything is what the first message of a stream names: read it all.
var everything = []planv1.Change{
	planv1.Change_CHANGE_WISH, planv1.Change_CHANGE_TASK, planv1.Change_CHANGE_QUESTION,
	planv1.Change_CHANGE_BLOCK, planv1.Change_CHANGE_PROJECT, planv1.Change_CHANGE_INSTRUCTION,
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
	pending map[string]map[planv1.Change]bool
	kick    chan struct{}
}

// Watch follows the changes of the wishes: a first message that names everything, then the changes, coalesced.
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
	if err := stream.Send(&planv1.WishServiceWatchResponse{Changes: everything}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sub.kick:
		}
		for _, msg := range sub.take() {
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
	sub := &watcher{wishID: wishID, pending: map[string]map[planv1.Change]bool{}, kick: make(chan struct{}, 1)}
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
// marks, and each stream sends.
func (ws *watchers) changed(ms []proto.Message) {
	var changes []watched
	for _, m := range ms {
		switch m := m.(type) {
		case *planv1.Wish:
			changes = append(changes, watched{m.GetId(), planv1.Change_CHANGE_WISH})
		case *planv1.Task:
			// Whether Djinn proposes to grant the wish follows its tasks and questions.
			changes = append(changes, watched{m.GetWishId(), planv1.Change_CHANGE_TASK},
				watched{m.GetWishId(), planv1.Change_CHANGE_WISH})
		case *planv1.Question:
			changes = append(changes, watched{m.GetWishId(), planv1.Change_CHANGE_QUESTION},
				watched{m.GetWishId(), planv1.Change_CHANGE_WISH})
		case *planv1.Instruction:
			changes = append(changes, watched{m.GetWishId(), planv1.Change_CHANGE_INSTRUCTION}, watched{m.GetWishId(), planv1.Change_CHANGE_WISH})
		case *planv1.Block:
			changes = append(changes, watched{m.GetWishId(), planv1.Change_CHANGE_BLOCK})
		case *planv1.Project:
			changes = append(changes, watched{"", planv1.Change_CHANGE_PROJECT})
		}
	}
	ws.notify(changes)
}

// watched is what changed in a wish ("" for every wish, or the projects).
type watched struct {
	wish string
	kind planv1.Change
}

// notify marks changes for every stream; it never waits.
func (ws *watchers) notify(changes []watched) {
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
			if sub.pending[wish] == nil {
				sub.pending[wish] = map[planv1.Change]bool{}
			}
			sub.pending[wish][c.kind] = true
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

// take empties what the stream has not sent yet, one message per wish, in a stable order.
func (sub *watcher) take() []*planv1.WishServiceWatchResponse {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	wishes := make([]string, 0, len(sub.pending))
	for wish := range sub.pending {
		wishes = append(wishes, wish)
	}
	slices.Sort(wishes)
	out := make([]*planv1.WishServiceWatchResponse, 0, len(wishes))
	for _, wish := range wishes {
		msg := &planv1.WishServiceWatchResponse{WishId: wish}
		for kind := range sub.pending[wish] {
			msg.Changes = append(msg.Changes, kind)
		}
		slices.Sort(msg.Changes)
		out = append(out, msg)
	}
	clear(sub.pending)
	return out
}
