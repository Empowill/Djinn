package plan

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// Each Markdown tilasm has a document agent (TASK_ROLE_DOCUMENT): a light worker Djinn wakes when the developer writes
// in one of its discussions, which answers there, proposes changes and tells the lead what matters for the wish. Only a
// message the developer commits wakes it: a draft never reaches Djinn, and what an agent writes wakes nobody.

// DocumentAuthor is who the document agent writes as: its commands default their author to it ($DJINN_AUTHOR).
const DocumentAuthor = "document"

// WithDiscussed calls f with a discussion of a Markdown tilasm once a message the developer wrote in it is stored: the
// harness wakes the tilasm's document agent.
func WithDiscussed(f func(context.Context, *planv1.TilasmThread)) Option {
	return func(o *options) { o.discussed = append(o.discussed, f) }
}

// byDeveloper tells whether author is the developer, who writes in the window.
func byDeveloper(author string) bool { return strings.EqualFold(author, developerAuthor) }

// AwaitsAgent tells whether the discussion waits for the document agent: open, and nothing answered the developer's
// last message, neither a message by someone else after it nor a proposal linked to the discussion made after it
// (pending, accepted or rejected). proposals may hold any proposals: only the discussion's count.
func AwaitsAgent(thread *planv1.TilasmThread, proposals []*planv1.TilasmProposal) bool {
	msgs := thread.GetMessages()
	if thread.GetResolved() || len(msgs) == 0 || !byDeveloper(msgs[len(msgs)-1].GetAuthor()) {
		return false
	}
	asked := msgs[len(msgs)-1].GetCreateTime().AsTime()
	for _, p := range proposals {
		if strings.EqualFold(p.GetThreadId(), thread.GetId()) && !p.GetCreateTime().AsTime().Before(asked) {
			return false
		}
	}
	return true
}

// AwaitingAgent are the discussions of the tilasm that wait for its document agent (AwaitsAgent).
func AwaitingAgent(ctx context.Context, r store.Reader, tilasmID string) ([]*planv1.TilasmThread, error) {
	threads, proposals, err := discussionsOf(ctx, r, tilasmID)
	if err != nil {
		return nil, err
	}
	var out []*planv1.TilasmThread
	for _, thread := range threads {
		if AwaitsAgent(thread, proposals) {
			out = append(out, thread)
		}
	}
	return out, nil
}

// ThreadKind is what a discussion is for, a comment when it does not say.
func ThreadKind(thread *planv1.TilasmThread) planv1.TilasmThreadKind {
	if k := thread.GetKind(); k != planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED {
		return k
	}
	return planv1.TilasmThreadKind_TILASM_THREAD_KIND_COMMENT
}

// Ongoing tells whether a task still works or will: planned, running, paused, or one Djinn resumes.
func Ongoing(t *planv1.Task) bool {
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskStatus_TASK_STATUS_RUNNING,
		planv1.TaskStatus_TASK_STATUS_PAUSED, planv1.TaskStatus_TASK_STATUS_RESUMING,
		planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
		return true
	}
	return false
}

// DocumentAgentOf is the latest document agent of the tilasm in its wish, the one Djinn wakes again; nil when it has
// none yet.
func DocumentAgentOf(ctx context.Context, r store.Reader, wishID, tilasmID string) (*planv1.Task, error) {
	tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": wishID})
	if err != nil {
		return nil, err
	}
	var latest *planv1.Task
	for _, t := range tasks {
		if t.GetRole() != planv1.TaskRole_TASK_ROLE_DOCUMENT || !strings.EqualFold(t.GetTilasmId(), tilasmID) {
			continue
		}
		// An ongoing one wins: there is one at a time, but the oldest still working is the one told.
		if latest == nil || Ongoing(t) && !Ongoing(latest) ||
			Ongoing(t) == Ongoing(latest) && t.GetCreateTime().AsTime().After(latest.GetCreateTime().AsTime()) {
			latest = t
		}
	}
	return latest, nil
}

// discussedBy calls the hooks with thread, in which the developer just wrote author's message.
func (t *Tilasms) discussedBy(ctx context.Context, thread *planv1.TilasmThread, author string) {
	if !byDeveloper(author) {
		return // An agent's message wakes no agent: no loop.
	}
	for _, f := range t.Discussed {
		f(ctx, thread)
	}
}

// fillAgentPending marks the discussions the tilasm's document agent works on now.
func fillAgentPending(ctx context.Context, r store.Reader, wishID, tilasmID string, threads []*planv1.TilasmThread) error {
	agent, err := DocumentAgentOf(ctx, r, wishID, tilasmID)
	if err != nil || agent == nil || !Ongoing(agent) {
		return err
	}
	proposals, err := store.List[*planv1.TilasmProposal](ctx, r, store.Where{"tilasm_id": tilasmID})
	if err != nil {
		return err
	}
	for _, thread := range threads {
		thread.AgentPending = AwaitsAgent(thread, proposals)
	}
	return nil
}

// Wake asks the document agent again to answer a discussion that waits for it, as when the developer writes in it,
// without a new message: a discussion left unanswered (its agent failed, stopped, or was off) is not silent forever.
func (t *Tilasms) Wake(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceWakeRequest],
) (*connect.Response[planv1.TilasmServiceWakeResponse], error) {
	thread, _, r, err := t.threadOf(ctx, req.Msg.GetThread())
	if err != nil {
		return nil, err
	}
	proposals, err := store.List[*planv1.TilasmProposal](ctx, t.Store, store.Where{"tilasm_id": thread.GetTilasmId()})
	if err != nil {
		return nil, Status(err)
	}
	if !AwaitsAgent(thread, proposals) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(
			"the discussion waits for no answer: it is resolved, or the developer's last message has a reply or a "+
				"proposal after it"))
	}
	for _, f := range t.Discussed {
		f(ctx, thread)
	}
	fillThread(r, thread)
	threads := []*planv1.TilasmThread{thread}
	if err := fillAgentPending(ctx, t.Store, thread.GetWishId(), thread.GetTilasmId(), threads); err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TilasmServiceWakeResponse{Thread: thread}), nil
}
