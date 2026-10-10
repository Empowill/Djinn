package harness

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// forkLead is what Task.fork_of says of a fork of the lead's session.
const forkLead = "lead"

// fork is the session a spawn starts from, where it comes from, and the agent that forks it.
type fork struct {
	session  string
	of       string // the source task's code, or forkLead
	provider planv1.Provider
	task     *planv1.Task // the source task; nil for the lead
}

// forkSource is the session a spawn forks: a task of the wish (--fork), or the wish's lead (--from-lead); none
// without either, the default. The fork runs the source's agent: another one is refused, as is a source without
// a session yet, and Antigravity, which cannot fork a conversation.
func forkSource(ctx context.Context, r store.Reader, wish *planv1.Wish, req *planv1.TaskServiceSpawnRequest) (fork, error) {
	var src fork
	switch {
	case req.GetFork() != "" && req.GetFromLead():
		return src, connect.NewError(connect.CodeInvalidArgument, errors.New("--fork and --from-lead exclude each other"))
	case req.GetFromLead():
		lead := wish.GetLead()
		if lead.GetSessionId() == "" {
			return src, connect.NewError(connect.CodeFailedPrecondition, errors.New(
				"the wish has no lead session to fork: djinn wish set-lead records it"))
		}
		src = fork{session: lead.GetSessionId(), of: forkLead, provider: lead.GetProvider()}
	case req.GetFork() != "":
		tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": wish.GetId()})
		if err != nil {
			return src, err
		}
		i := slices.IndexFunc(tasks, func(t *planv1.Task) bool {
			return t.GetId() == req.GetFork() || strings.EqualFold(t.GetCode(), req.GetFork())
		})
		if i < 0 {
			return src, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("--fork %s is not a task of the wish", req.GetFork()))
		}
		t := tasks[i]
		if t.GetSessionId() == "" {
			return src, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"%s has no session to fork yet: its worker has not started", t.GetCode()))
		}
		src = fork{session: t.GetSessionId(), of: t.GetCode(), provider: t.GetProvider(), task: t}
	default:
		return src, nil
	}
	if src.provider == planv1.Provider_PROVIDER_UNSPECIFIED {
		src.provider = planv1.Provider_PROVIDER_CLAUDE
	}
	if src.provider == planv1.Provider_PROVIDER_ANTIGRAVITY {
		return src, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%s runs antigravity, which cannot fork a conversation: give the worker a prompt instead", forkText(src.of)))
	}
	if p := req.GetProvider(); p != planv1.Provider_PROVIDER_UNSPECIFIED && p != src.provider {
		return src, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"%s is a %s session: a fork runs the same agent, not %s", forkText(src.of), short(src.provider), short(p)))
	}
	return src, nil
}

// forkText names the source of a fork for a reader: W1's session, the lead's session.
func forkText(of string) string {
	if of == forkLead {
		return "the lead's session"
	}
	return of + "'s session"
}
