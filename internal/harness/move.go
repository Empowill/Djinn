package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
)

// leadMergeLine formats the message telling the lead what was merged and what moves were detected.
func leadMergeLine(batchCodes, branch, sha string, moves []string) string {
	line := fmt.Sprintf("Djinn: %s merged into %s as %s", batchCodes, branch, short8(sha))
	if len(moves) > 0 {
		line += ": " + strings.Join(moves, ", ")
	}
	return line
}

// needsMoveName returns the name of the move for an added needs: box.
func needsMoveName(box, machine string) string {
	if box != "" {
		return fmt.Sprintf("check %s on %s", box, machine)
	}
	return fmt.Sprintf("check on %s", machine)
}

// needsMoveQuestion builds the question for an added needs: box.
func needsMoveQuestion(wishID, box, machine string) *planv1.Question {
	text := fmt.Sprintf("Check %s on %s?", box, machine)
	context := fmt.Sprintf("A verification is needed on %s:\n- [ ] %s (needs: %s)", machine, box, machine)
	if box == "" {
		text = fmt.Sprintf("Check on %s?", machine)
		context = fmt.Sprintf("A verification is needed on %s:\n- [ ] (needs: %s)", machine, machine)
	}
	return &planv1.Question{
		WishId:  wishID,
		Text:    text,
		Context: context,
		Options: []string{"Verified", "Not yet"},
		Move:    true,
	}
}

// tagMoveName returns the name of the move for pushing a tag.
func tagMoveName(tag string) string {
	return fmt.Sprintf("push tag %s", tag)
}

// tagMoveQuestion builds the question for pushing a local tag.
func tagMoveQuestion(wishID, tag, remote string) *planv1.Question {
	return &planv1.Question{
		WishId:  wishID,
		Text:    fmt.Sprintf("Push tag %s to %s?", tag, remote),
		Context: fmt.Sprintf("Run `git push %s %s` to push the release tag to %s.", remote, tag, remote),
		Options: []string{fmt.Sprintf("Push tag %s", tag), "Skip"},
		Move:    true,
	}
}

const releaseMoveName = "start a release"

// releaseMoveQuestion builds the question for changed release files.
func releaseMoveQuestion(wishID, sha string) *planv1.Question {
	return &planv1.Question{
		WishId:  wishID,
		Text:    "Start release dry run?",
		Context: fmt.Sprintf("Release files changed in %s. Trigger the release workflow dry run or prepare the release.", short8(sha)),
		Options: []string{"Start release dry run", "Skip"},
		Move:    true,
	}
}

// prMoveName returns the name of the move for opening a pull request.
func prMoveName(main string) string {
	return fmt.Sprintf("open a pull request to %s", main)
}

// prMoveQuestion builds the question for opening a pull request to main.
func prMoveQuestion(wishID, main, title, body string) *planv1.Question {
	return &planv1.Question{
		WishId:  wishID,
		Text:    fmt.Sprintf("Open a pull request to %s?", main),
		Context: fmt.Sprintf("### %s\n\n%s", title, body),
		Options: []string{"Open pull request", "Not yet"},
		Move:    true,
	}
}

// prGrowsMoveName returns the move note when an open PR already exists for the branch.
func prGrowsMoveName(prNum string) string {
	if prNum != "" {
		return fmt.Sprintf("PR #%s grows with this work", prNum)
	}
	return "PR grows with this work"
}

// openPRFromBranch checks whether an open PR from branch exists for repo via gh.
// Returns the PR number (if parsed) and whether an open PR exists.
func openPRFromBranch(ctx context.Context, repo, branch string) (prNum string, ok bool) {
	cleanBranch := branch
	cleanBranch = strings.TrimPrefix(cleanBranch, "refs/heads/")
	cleanBranch = strings.TrimPrefix(cleanBranch, "origin/")
	if cleanBranch == "" {
		return "", false
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", "pr", "list", "--head", cleanBranch, "--state", "open")
	if repo != "" {
		cmd.Dir = repo
	}
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return "", false
	}

	// If output starts with '[', try parsing JSON
	if strings.HasPrefix(trimmed, "[") {
		var prs []struct {
			Number int `json:"number"`
		}
		if err := json.Unmarshal([]byte(trimmed), &prs); err == nil {
			if len(prs) == 0 {
				return "", false
			}
			return strconv.Itoa(prs[0].Number), true
		}
	}

	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Showing ") || strings.HasPrefix(line, "ID\t") || strings.HasPrefix(line, "ID ") {
			continue
		}
		normalized := strings.ReplaceAll(line, `\t`, "\t")
		fields := strings.Fields(normalized)
		if len(fields) > 0 {
			candidate := strings.TrimPrefix(fields[0], "#")
			if _, err := strconv.Atoi(candidate); err == nil {
				return candidate, true
			}
		}
		if idx := strings.Index(line, "/pull/"); idx != -1 {
			rest := line[idx+len("/pull/"):]
			num := ""
			for _, r := range rest {
				if r >= '0' && r <= '9' {
					num += string(r)
				} else {
					break
				}
			}
			if num != "" {
				return num, true
			}
		}
	}
	return "", false
}

// installMoveQuestion builds the question for an uninstalled build.
func installMoveQuestion(wishID, sha, branch string, delay time.Duration) *planv1.Question {
	s := short8(sha)
	return &planv1.Question{
		WishId: wishID,
		Text:   fmt.Sprintf("Install and restart %s?", s),
		Context: fmt.Sprintf("The build %s on %s has been integrated for %s and is not installed yet. Install and restart?",
			s, branch, delay.Round(time.Minute)),
		Options: []string{"Install and restart", "Skip"},
		Move:    true,
	}
}

// azimaProofMoveQuestion builds the question for an azima awaiting proof.
func azimaProofMoveQuestion(wishID, taskID, code, needsWords string, boxes []string) *planv1.Question {
	prompt := fmt.Sprintf("Check %s on %s?", code, needsWords)
	ctxt := fmt.Sprintf("Azima %s is awaiting proof:\n\n%s", code, strings.Join(boxes, "\n"))
	return &planv1.Question{
		WishId:  wishID,
		TaskId:  taskID,
		Text:    prompt,
		Context: ctxt,
		Options: []string{"Verified", "Not yet"},
		Move:    true,
	}
}

// isPlanFile tells whether path is a plan file under plan/*.md (the project's plan folder).
func isPlanFile(p string) bool {
	slash := filepath.ToSlash(filepath.Clean(p))
	if !strings.HasSuffix(slash, ".md") {
		return false
	}
	dir := path.Dir(slash)
	return dir == plan.PlanDir || dir == "./"+plan.PlanDir || strings.HasSuffix(dir, "/"+plan.PlanDir)
}

// isReleaseFile tells whether path is a release or packaging file by path.
func isReleaseFile(path string) bool {
	p := strings.ToLower(filepath.ToSlash(path))
	return strings.Contains(p, "release") || strings.Contains(p, ".goreleaser")
}

// hasPRQuestion tells whether a question to open a pull request has already been asked for projectID.
func (h *Harness) hasPRQuestion(ctx context.Context, projectID string, questions []*planv1.Question, wishesByID map[string]*planv1.Wish) bool {
	for _, q := range questions {
		if !q.GetMove() {
			continue
		}
		if !strings.Contains(strings.ToLower(q.GetText()), "pull request") {
			continue
		}
		if !h.isQuestionForProject(ctx, q, projectID, wishesByID) {
			continue
		}
		// Open or answered, unless already closed because merged to main.
		if q.GetAnswer() == nil || !strings.HasPrefix(q.GetAnswer().GetNote(), "merged to") {
			return true
		}
	}
	return false
}

// isPRWorthy checks whether branch in project is ahead of main and green by refs and checks:
// the wish is settled, branch is ahead of main, and if a remote exists, push state is green and up to date.
func (h *Harness) isPRWorthy(ctx context.Context, wish *planv1.Wish, project *planv1.Project, branch, sha string) (main, ref string, ok bool) {
	repo := project.GetDirectory()
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil {
		return "", "", false
	}
	strat, _ := plan.ResolveWishPushStrategy(wish, settings)
	if strat == planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
		return "", "", false
	}
	main, ref, err = mainRef(ctx, repo, settings.MainBranch)
	if err != nil || strings.EqualFold(main, branch) {
		return "", "", false
	}
	if !h.isWishSettled(ctx, wish.GetId()) {
		return "", "", false
	}
	headSha := sha
	if headSha == "" {
		headSha, _ = git(ctx, repo, "rev-parse", "refs/heads/"+branch)
	}
	if headSha == "" {
		return "", "", false
	}
	remote, _ := pushTarget(ctx, repo, branch)
	if remote == "" {
		return "", "", false
	}
	p := pushState(wish, project.GetId())
	if p != nil && (p.GetQuestionId() != "" || p.GetHeld() != "" || p.GetRefused() != "") {
		return "", "", false
	}
	if sha == "" {
		if p == nil || p.GetLast() == nil {
			return "", "", false
		}
		targetSha := p.GetLast().GetNewSha()
		if targetSha == "" || (!strings.HasPrefix(targetSha, headSha) && !strings.HasPrefix(headSha, targetSha)) {
			return "", "", false
		}
	}
	countStr, err := git(ctx, repo, "rev-list", "--count", ref+".."+headSha)
	count, _ := strconv.Atoi(strings.TrimSpace(countStr))
	if err != nil || count <= 0 {
		return "", "", false
	}
	return main, ref, true
}
