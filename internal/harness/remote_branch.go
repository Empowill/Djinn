package harness

// A branch the developer names for a wish or an azima may live on the remote already, the branch of an open pull
// request: Djinn makes its local branch from the remote's, never from main (wish Dew, 13/10/2026: three azimas named
// after open merge requests got main's history, without the requests' own commits, and nothing could be pushed). A
// local branch behind the remote's is brought up to it before Djinn merges into it (catchUp); one that diverged from
// it takes no work and no push until a person aligns them, and Djinn says so to the lead.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/gitx"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// fetchBranch fetches branch from the remote Djinn pushes it to (pushTarget): the remote's branch, shown as
// <remote>/<name>, and its commit; "" for both when the repository has no remote, the commit "" when the remote has no
// such branch. An error when the remote did not answer: the commit is then the remote's as this repository last saw it.
func fetchBranch(ctx context.Context, repo, branch string) (shown, sha string, err error) {
	remote, target := pushTarget(ctx, repo, branch)
	if remote == "" {
		return "", "", nil
	}
	shown = remote + "/" + target
	fetching, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	if sha, err = gitx.FetchBranch(fetching, repo, remote, target); err != nil {
		return shown, commitOf(ctx, repo, "refs/remotes/"+shown), err
	}
	return shown, sha, nil
}

// apartText says how branch, at ours, stands against shown, the remote's branch at theirs, which holds commits it
// lacks.
func apartText(ctx context.Context, repo, branch, shown, ours, theirs string) string {
	ahead, behind := "?", "?"
	if counts, err := git(ctx, repo, "rev-list", "--left-right", "--count", ours+"..."+theirs); err == nil {
		if a, b, ok := strings.Cut(counts, "\t"); ok {
			ahead, behind = a, b
		}
	}
	return fmt.Sprintf("%s has diverged from %s (ahead %s, behind %s)", branch, shown, ahead, behind)
}

// alignText says how a person aligns a branch with shown, the remote's, which holds commits it lacks.
func alignText(shown string) string {
	return fmt.Sprintf("merge %s into it, or reset it to %s if its own commits are not wanted", shown, shown)
}

// tellApart tells the lead of wish, once for each why, that branch stands apart from its remote's, why says how.
func (h *Harness) tellApart(ctx context.Context, wish *planv1.Wish, why string) {
	key := wish.GetId() + "\x00" + why
	h.mu.Lock()
	told := h.toldApart[key]
	h.toldApart[key] = true
	h.mu.Unlock()
	if !told {
		h.tellLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_MOVE, wish.GetId(), "", "Djinn: "+why+".")
	}
}

// Catching up (W164, 16/10/2026, the developer: "you never know"): before Djinn merges work, or main, into a branch,
// it fetches the remote's branch and brings its own up to it when it is behind, a fast-forward, the person's clean
// checkout of it following (moveBranch). One that diverged, or whose checkout holds changes not committed, is left as
// it is: Djinn merges nothing into it and tells the lead. The developer pulls one by hand the same way, from the
// projects list or with djinn project pull.

// catchUpFetchTimeout is how long the fetch before a merge may take: a remote that does not answer holds the
// integration no longer, the branch then compared with the remote's as this repository last saw it.
const catchUpFetchTimeout = 30 * time.Second

// pulled is how pullBranch left a branch: shown, the remote's branch, <remote>/<name>; the commits it moved from and to,
// both "" when it did not move; why it could not be brought up to the remote's, "" when it holds every commit there;
// held: the person's checkout of it, with changes not committed, held it.
type pulled struct {
	shown, from, to, why string
	held                 bool
}

// pullBranch brings branch of repo up to its remote's branch when it is behind it, a fast-forward only: fetched first
// when fetch, else as this repository last saw it. A branch that diverged from the remote's, or whose checkout holds
// changes not committed, is left as it is, why saying so; nothing is ever forced. The zero pulled when the repository
// has no remote, the remote has no such branch, or the branch holds every commit of the remote's. An error when fetch
// is asked and the remote did not answer, the branch then compared with the remote's as last seen.
func pullBranch(ctx context.Context, repo, branch string, fetch bool) (pulled, error) {
	var p pulled
	var theirs string
	var fetchErr error
	if fetch {
		fetching, cancel := context.WithTimeout(ctx, catchUpFetchTimeout)
		p.shown, theirs, fetchErr = fetchBranch(fetching, repo, branch)
		cancel()
	} else if remote, target := pushTarget(ctx, repo, branch); remote != "" {
		p.shown = remote + "/" + target
		theirs = commitOf(ctx, repo, "refs/remotes/"+p.shown)
	}
	ours := commitOf(ctx, repo, "refs/heads/"+branch)
	if theirs == "" || ours == "" || theirs == ours || isAncestor(ctx, repo, theirs, ours) {
		return p, fetchErr
	}
	if !isAncestor(ctx, repo, ours, theirs) {
		p.why = apartText(ctx, repo, branch, p.shown, ours, theirs)
		return p, fetchErr
	}
	// Moved under a checkout with changes, its next commit would undo what the remote brought.
	holder, err := checkoutOf(ctx, repo, branch)
	if err != nil {
		p.why = fmt.Sprintf("%s is behind %s and cannot be brought up to it: %v", branch, p.shown, err)
		return p, fetchErr
	}
	if holder != "" {
		if changes, err := git(ctx, holder, "status", "--porcelain", "--untracked-files=no"); err != nil || changes != "" {
			p.why = fmt.Sprintf("%s is behind %s, and your checkout of it, %s, has changes not committed: it is left as it "+
				"is until you commit them or put them aside", branch, p.shown, holder)
			p.held = true
			return p, fetchErr
		}
	}
	if _, why, _ := moveBranch(ctx, repo, branch, ours, theirs, "djinn: brought up to "+p.shown); why != "" {
		p.why = fmt.Sprintf("%s is behind %s and cannot be brought up to it: %s", branch, p.shown, why)
		return p, fetchErr
	}
	logx.L().Info("branch brought up to the remote's", "branch", branch, "remote", p.shown, "from", short8(ours), "to", short8(theirs))
	p.from, p.to = ours, theirs
	return p, fetchErr
}

// catchUp brings branch, which Djinn is about to merge into for wish, up to its remote's (pullBranch), fetched first
// when fetch, once a pass; and says why nothing may merge into it when it could not be: it diverged from the remote's,
// or the person's checkout of it holds changes, the lead told once. Main too, when it is the branch work lands in.
func (h *Harness) catchUp(ctx context.Context, wish *planv1.Wish, repo, branch string, fetch bool) string {
	p, err := pullBranch(ctx, repo, branch, fetch && passMemoOf(ctx).fetchDue(repo, branch))
	if err != nil {
		logx.L().Warn("fetch before a merge", "branch", branch, "err", err)
	}
	if p.why == "" {
		return ""
	}
	why := p.why + ": Djinn merges nothing into it until then"
	if !p.held {
		why = p.why + ": Djinn merges nothing into it and pushes it no more until they are aligned, by hand (" +
			alignText(p.shown) + "); Djinn never forces a push"
	}
	h.tellApart(ctx, wish, why)
	return why
}

// Pull brings the branches of project's syncs that are behind their remote up to it, each fetched first (pullBranch):
// only branch when it is named, a refusal then an error; else every sync, each refusal said in its pull. A branch work
// merges into is refused, as one that diverged or whose checkout holds changes; the lead of its wish is not told: the
// developer asked, and reads the answer.
func (h *Harness) Pull(ctx context.Context, project *planv1.Project, branch string) ([]*planv1.BranchPull, error) {
	syncs, err := h.Syncs(ctx, project)
	if err != nil {
		return nil, err
	}
	if branch != "" {
		syncs = slices.DeleteFunc(syncs, func(s *planv1.ProjectSync) bool { return s.GetBranch() != branch })
		if len(syncs) == 0 {
			return nil, fmt.Errorf("%w: %s is no branch Djinn pushes in %s", store.ErrNotFound, branch, project.GetName())
		}
	}
	repo := project.GetDirectory()
	var pulls []*planv1.BranchPull
	for _, sync := range syncs {
		pull := &planv1.BranchPull{Branch: sync.GetBranch(), Remote: sync.GetRemote() + "/" + sync.GetTarget()}
		if why := h.mergeInto(ctx, project, sync); why != "" {
			pull.Refused = why + ": pull it once that merge has ended"
		} else if _, _, err := fetchBranch(ctx, repo, sync.GetBranch()); err != nil {
			pull.Refused = fmt.Sprintf("Djinn cannot tell where %s stands: %v", pull.GetRemote(), err)
		} else if p, _ := pullBranch(ctx, repo, sync.GetBranch(), false); p.why == "" && p.to == "" && branch == "" {
			continue // It holds every commit of the remote's: nothing to say.
		} else {
			pull.Remote = cmp.Or(p.shown, pull.GetRemote())
			pull.OldSha, pull.Sha, pull.Refused = p.from, p.to, p.why
		}
		if branch != "" && pull.GetRefused() != "" {
			return nil, errors.New(pull.GetRefused())
		}
		pulls = append(pulls, pull)
	}
	if len(pulls) > 0 {
		h.notify()
	}
	return pulls, nil
}

// mergeInto says what merges into the branch of sync, in project, now: a batch of work, a merge worker, or a
// correction of main's merge; "" when nothing does.
func (h *Harness) mergeInto(ctx context.Context, project *planv1.Project, sync *planv1.ProjectSync) string {
	wish, err := store.Get[*planv1.Wish](ctx, h.store, sync.GetWishId())
	if err != nil {
		return ""
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		return ""
	}
	if id := sync.GetAzimaId(); id != "" {
		if i := slices.IndexFunc(tasks, func(t *planv1.Task) bool { return t.GetId() == id }); i >= 0 {
			return plan.AzimaMergeRunning(tasks[i], tasks, sync.GetBranch())
		}
	}
	return plan.MergeRunning(wish, tasks, project.GetId())
}
