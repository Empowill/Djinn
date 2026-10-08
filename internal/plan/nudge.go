package plan

import (
	"context"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// NudgeGroup is how long a nudge gathers what the developer decides before it goes: answers clicked in the same
// second reach the lead as one message.
const NudgeGroup = time.Second

// noteMax is the most of an answer's note a nudge carries; the lead reads the rest with djinn question list.
const noteMax = 160

// Nudges tells a wish's lead what moved in its wish, as a line in its terminal: an answer to one of the wish's
// questions, an approval, a task that ended or waits. The lead hears of it without waiting for the developer to
// write to it.
type Nudges struct {
	// Tell gives text to the lead of the wish, if one runs; it returns at once.
	Tell func(wishID, text string)
	// Group is how long a message gathers news before it goes; NudgeGroup when zero.
	Group time.Duration

	mu      sync.Mutex
	pending map[string][]string          // news waiting to go, by wish
	status  map[string]planv1.TaskStatus // last status of each task, by id, once followed
}

// Follow tells the leads, from now on, of each task of their wish that ends done or failed, or comes to wait for an
// answer. A task that was already so says nothing.
func (n *Nudges) Follow(ctx context.Context, s *store.Store) error {
	n.mu.Lock()
	n.status = map[string]planv1.TaskStatus{}
	n.mu.Unlock()
	s.OnCommit(n.committed)
	tasks, err := store.List[*planv1.Task](ctx, s, nil)
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, t := range tasks {
		if _, seen := n.status[t.GetId()]; !seen { // A commit since has the newer status.
			n.status[t.GetId()] = t.GetStatus()
		}
	}
	return nil
}

// committed tells of the tasks of changes that came to an end or to wait; it runs in the writer's goroutine, so it
// never waits. A task seen for the first time is new, or imported: it says nothing.
func (n *Nudges) committed(changes []proto.Message) {
	for _, m := range changes {
		t, ok := m.(*planv1.Task)
		if !ok {
			continue
		}
		n.mu.Lock()
		before, seen := n.status[t.GetId()]
		n.status[t.GetId()] = t.GetStatus()
		n.mu.Unlock()
		if !seen || before == t.GetStatus() {
			continue
		}
		why := ""
		if e := oneLine(t.GetError()); e != "" {
			why = ": " + clipRunes(e, noteMax)
		}
		var news string
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_DONE:
			news = t.GetCode() + " ended (done)."
		case planv1.TaskStatus_TASK_STATUS_FAILED:
			news = t.GetCode() + " ended (failed" + why + ")."
		case planv1.TaskStatus_TASK_STATUS_WAITING:
			news = t.GetCode() + " is waiting."
			if why != "" {
				news = t.GetCode() + " is waiting (" + strings.TrimPrefix(why, ": ") + ")."
			}
		default:
			continue
		}
		n.add(t.GetWishId(), news)
	}
}

// Answered tells the lead of q's wish its answer.
func (n *Nudges) Answered(_ context.Context, q *planv1.Question) {
	if q.GetAnswer() == nil {
		return
	}
	news := q.GetCode() + " answered: " + choiceLetter(q.GetAnswer().GetChoice())
	if note := oneLine(q.GetAnswer().GetNote()); note != "" {
		news += ` (note: "` + clipRunes(note, noteMax) + `")`
	}
	n.add(q.GetWishId(), news+".")
}

// Approved tells the lead of m's wish that the developer approved what m marks: a block, or a question already
// answered. Other marks say nothing to the lead.
func (n *Nudges) Approved(_ context.Context, m *planv1.Marked) {
	if m.GetMark().GetKind() != planv1.MarkKind_MARK_KIND_APPROVED {
		return
	}
	news := m.GetLabel() + " approved."
	if m.GetBlockId() != "" {
		news = "Block " + m.GetBlockId() + ` "` + clipLine(m.GetTitle()) + `" approved.`
	}
	n.add(m.GetWishId(), news)
}

// add gathers news for the wish's lead; the first of a group sends them all once the group is over.
func (n *Nudges) add(wishID, news string) {
	if wishID == "" || n.Tell == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.pending == nil {
		n.pending = map[string][]string{}
	}
	first := len(n.pending[wishID]) == 0
	n.pending[wishID] = append(n.pending[wishID], news)
	if !first {
		return
	}
	group := n.Group
	if group <= 0 {
		group = NudgeGroup
	}
	time.AfterFunc(group, func() {
		n.mu.Lock()
		all := n.pending[wishID]
		delete(n.pending, wishID)
		n.mu.Unlock()
		n.Tell(wishID, strings.Join(all, " ")+" Continue.")
	})
}

// choiceLetter is a stored choice as the lead reads it: the option's letter, or yes.
func choiceLetter(c planv1.Choice) string {
	if c >= planv1.Choice_CHOICE_A && c <= planv1.Choice_CHOICE_D {
		return string(rune('A' + c - planv1.Choice_CHOICE_A))
	}
	if c == planv1.Choice_CHOICE_NO {
		return "no"
	}
	return "yes"
}
