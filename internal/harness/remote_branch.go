package harness

// A branch the developer names for a wish or an azima may live on the remote already, the branch of an open pull
// request: Djinn makes its local branch from the remote's, never from main (wish Dew, 13/10/2026: three azimas named
// after open merge requests got main's history, without the requests' own commits, and nothing could be pushed). A
// local branch whose remote holds commits it lacks is behind or has diverged: Djinn merges nothing into it and pushes
// it no more until a person aligns them, and says so to the lead.

import (
	"context"
	"fmt"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/gitx"
	"github.com/empowill/djinn/internal/plan"
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

// apart says, in a phrase, how branch stands apart from its remote's branch, as this repository last saw it, when the
// remote holds commits the branch lacks: anything Djinn merged into it then could not be pushed without forcing. ""
// when it holds none, the repository has no remote, or branch is main, which main's own rules keep up (main.go).
func apart(ctx context.Context, repo string, settings plan.Settings, branch string) string {
	remote, target := pushTarget(ctx, repo, branch)
	if remote == "" {
		return ""
	}
	if main, _, err := mainRef(ctx, repo, settings.MainBranch); err == nil && strings.EqualFold(main, branch) {
		return ""
	}
	theirs, ours := commitOf(ctx, repo, "refs/remotes/"+remote+"/"+target), commitOf(ctx, repo, "refs/heads/"+branch)
	if theirs == "" || ours == "" || theirs == ours || isAncestor(ctx, repo, theirs, ours) {
		return ""
	}
	shown := remote + "/" + target
	return apartText(ctx, repo, branch, shown, ours, theirs) + ": Djinn merges nothing into it and pushes it no more " +
		"until they are aligned, by hand (" + alignText(shown) + "); Djinn never forces a push"
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
