package harness

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
)

// errProtectedPush says that Djinn refused to push a protected branch, the project's reference branch or a branch its
// forge protects: it reaches the remote only through a merged pull or merge request (refuseProtected).
var errProtectedPush = errors.New("a protected branch is never pushed")

// How Djinn reads the branches a forge protects (forgeProtects): again once its last read is this old, or this old
// once it failed, each read given up past its timeout.
const (
	protectedLookEvery   = 10 * time.Minute
	protectedRetry       = time.Minute
	protectedLookTimeout = 10 * time.Second
)

// protectedLook is what Djinn last read of the branches a forge protects in one of its repositories.
type protectedLook struct {
	names  []string  // the branch names, or GitLab wildcards, the forge protects, as last read; nil when never read
	at     time.Time // when Djinn last asked
	failed bool      // the last ask failed: names are those of the read before it
}

// refuseProtected refuses the push of branch, of wish in project, to remote where it is named target, when it is a
// protected branch: the project's reference branch, main on most projects, as Djinn names it (resolveMainSha), or a
// branch the remote's forge, GitHub or GitLab, protects (forgeProtects). Whatever the wish, its push strategy and the
// project, such a branch reaches its remote only through a merged pull or merge request. Before it, a wish that
// integrated into main with the wish strategy had Djinn push main ten times from 07/11/2026, with no pull request
// (tilasm L09); the forge's branch protection did not stop it, the local token being an admin's (Q34). The reference
// branch is read with no network call and refused offline too; the forge's protection is read from a cache, and a
// push it cannot read goes on. The refusal says the command that gives the wish a branch of its own, on the push's
// line and, once per refusal, in the events of committed; nil when branch is not protected.
func (h *Harness) refuseProtected(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, remote, branch, target string, committed []*planv1.Task,
	run *pushRun,
) error {
	note := h.protectedRefusal(ctx, wish, project, remote, branch, target)
	if note == "" {
		return nil
	}
	// The run as stored, read in the transaction that records the refusal: not as wish says it, a copy the pass read
	// before it started this push, maybe before the push just before this one refused it already (W62).
	if was := run.swap(ctx, planv1.PushStage_PUSH_STAGE_FAILED, "", note); was.GetBranch() != branch ||
		was.GetStage() != planv1.PushStage_PUSH_STAGE_FAILED || was.GetNote() != note {
		h.pushSteps(ctx, committed, "integration: "+note)
		logx.L().Warn("push refused: protected branch", "loop", "push", "wish", wish.GetId(), "project", project.GetId(),
			"branch", branch, "target", target)
	}
	return fmt.Errorf("%w: %s", errProtectedPush, note)
}

// protectedRefusal says why Djinn does not push branch to remote, where it is named target, when it is protected;
// "" when it is not.
func (h *Harness) protectedRefusal(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, remote, branch, target string,
) string {
	name := cmp.Or(project.GetName(), project.GetId())
	settings, _ := plan.LoadSettings(h.home, project)
	main, _, _ := resolveMainSha(ctx, project.GetDirectory(), settings) // None found: no reference branch to keep.
	switch {
	case main != "" && strings.EqualFold(branch, main):
		return protectedNote(wish, project, branch, branch+" is the reference branch of "+name)
	case main != "" && strings.EqualFold(target, main):
		return protectedNote(wish, project, branch, branch+" goes to "+main+", the reference branch of "+name)
	}
	where, names := h.forgeProtects(ctx, project.GetDirectory(), remote)
	if !protects(names, target) {
		return ""
	}
	if target != branch {
		return protectedNote(wish, project, branch, branch+" goes to "+target+", a branch "+where+" protects")
	}
	return protectedNote(wish, project, branch, where+" protects "+branch)
}

// protectedNote says why Djinn does not push branch, of wish in project, what, and the command that gives the wish a
// branch of its own there.
func protectedNote(wish *planv1.Wish, project *planv1.Project, branch, what string) string {
	return fmt.Sprintf("Djinn never pushes %s: %s, which reaches its remote only through a merged pull or merge request. "+
		"The wish %q integrates into it; give it a branch of its own, pushed and proposed as a pull request: "+
		"djinn wish set-integration %s --project-id %s --branch <branch> --create-branch",
		branch, what, wish.GetTitle(), wish.GetId(), project.GetId())
}

// forgeProtects is the repository remote names on its forge, host/path, and the branches the forge protects there, as
// Djinn last read them: again once the read is protectedLookEvery old, or protectedRetry old once it failed. A
// remote on no forge (a folder, an SSH alias) gives none, as a read that never succeeded, or that Djinn does not try
// while the network is down: the push goes on, the forge refusing it if it must.
func (h *Harness) forgeProtects(ctx context.Context, repo, remote string) (where string, names []string) {
	if remote == "" {
		return "", nil
	}
	address, err := git(ctx, repo, "config", "--get", "remote."+remote+".url")
	if err != nil {
		return "", nil
	}
	host, path := plan.RemoteRepo(address)
	if !strings.Contains(host, ".") || strings.HasPrefix(host, ".") {
		return "", nil // A folder, or a name only SSH knows: no forge to ask.
	}
	where = host + "/" + path
	h.mu.Lock()
	look, seen := h.protectedLooks[where]
	h.mu.Unlock()
	age := h.now().Sub(look.at)
	if seen && (age < protectedLookEvery && !look.failed || age < protectedRetry) || h.netHolds() {
		return where, look.names
	}
	read, ok := h.readProtected(ctx, repo, host, path)
	look.at, look.failed = h.now(), !ok
	if ok {
		look.names = read
	} else {
		logx.L().Info("protected branches unread", "loop", "push", "repository", where)
	}
	h.mu.Lock()
	h.protectedLooks[where] = look
	h.mu.Unlock()
	return where, look.names
}

// readProtected asks the forge of host which branches it protects in its repository path: gh on GitHub, glab on
// GitLab. False when it could not tell.
func (h *Harness) readProtected(ctx context.Context, repo, host, path string) ([]string, bool) {
	ctx, cancel := context.WithTimeout(ctx, protectedLookTimeout)
	defer cancel()
	if strings.Contains(host, "gitlab") {
		out, code, err := h.runCmd(ctx, repo, []string{"glab", "api", "--hostname", host, "--paginate", "--output", "ndjson",
			"projects/" + url.PathEscape(path) + "/protected_branches?per_page=100"})
		if err != nil || code != 0 {
			return nil, false
		}
		names := []string{}
		for line := range strings.Lines(out) {
			var b struct {
				Name string `json:"name"`
			}
			if line = strings.TrimSpace(line); line == "" {
				continue
			}
			if json.Unmarshal([]byte(line), &b) != nil {
				return nil, false
			}
			names = append(names, b.Name)
		}
		return names, true
	}
	out, code, err := h.runCmd(ctx, repo, []string{"gh", "api", "--hostname", host, "--paginate",
		"repos/" + path + "/branches?protected=true&per_page=100", "--jq", ".[].name"})
	if err != nil || code != 0 {
		return nil, false
	}
	return strings.Fields(out), true
}

// protects tells whether the forge that protects names protects branch: one of them, or a GitLab wildcard whose *
// stands for any text.
func protects(names []string, branch string) bool {
	return slices.ContainsFunc(names, func(name string) bool {
		if !strings.Contains(name, "*") {
			return name == branch
		}
		pattern := "^" + strings.ReplaceAll(regexp.QuoteMeta(name), `\*`, ".*") + "$"
		ok, _ := regexp.MatchString(pattern, branch)
		return ok
	})
}
