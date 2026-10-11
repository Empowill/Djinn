package harness

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

const maxDiffLines = 2000

// Diff returns the list of files changed by a task, or the unified diff with syntax tokens for a single file.
func (h *Harness) Diff(ctx context.Context, procedure string, req *planv1.TaskServiceDiffRequest) (*planv1.TaskServiceDiffResponse, error) {
	task, err := h.resolveTask(ctx, req.GetTask(), req.GetWishId())
	if err != nil {
		return nil, err
	}
	if task.GetProjectId() == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s has no project", task.GetCode()))
	}
	project, err := store.Get[*planv1.Project](ctx, h.store, task.GetProjectId())
	if err != nil {
		return nil, plan.Status(err)
	}
	if !project.GetGit() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("project %s is not a git repository", project.GetName()))
	}

	wish, _ := store.Get[*planv1.Wish](ctx, h.store, task.GetWishId())
	base, head, err := h.resolveBaseAndHead(ctx, project, wish, task)
	if err != nil {
		return nil, err
	}

	settings, _ := plan.LoadSettings(h.home, project)
	globs := append([]string{"gen/**"}, settings.Generated...)

	dir := project.GetDirectory()

	// If no specific file was requested, return the list of changed files with counts.
	if req.GetFile() == "" {
		files, err := h.diffFiles(ctx, dir, base, head, globs)
		if err != nil {
			return nil, err
		}
		return &planv1.TaskServiceDiffResponse{
			Files: files,
			Base:  base,
			Head:  head,
		}, nil
	}

	// Single file diff requested.
	targetPath := req.GetFile()
	if filepath.IsAbs(targetPath) {
		if rel, err := filepath.Rel(dir, targetPath); err == nil && !strings.HasPrefix(rel, "..") {
			targetPath = rel
		}
	}
	targetPath = filepath.ToSlash(filepath.Clean(targetPath))
	targetPath = strings.TrimPrefix(targetPath, "/")

	fileDiff, err := h.diffSingleFile(ctx, dir, base, head, targetPath, globs)
	if err != nil {
		return nil, err
	}

	return &planv1.TaskServiceDiffResponse{
		File: fileDiff,
		Base: base,
		Head: head,
	}, nil
}

func (h *Harness) resolveTask(ctx context.Context, ref *planv1.TaskRef, wishID string) (*planv1.Task, error) {
	if ref == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("task reference is required"))
	}
	if id := ref.GetId(); id != "" {
		t, err := store.Get[*planv1.Task](ctx, h.store, id)
		if err != nil {
			return nil, plan.Status(err)
		}
		return t, nil
	}
	code := ref.GetCode()
	if code == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("task code or id is required"))
	}
	where := store.Where{}
	if wishID != "" {
		where["wish_id"] = wishID
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, where)
	if err != nil {
		return nil, plan.Status(err)
	}
	var matches []*planv1.Task
	for _, t := range tasks {
		if strings.EqualFold(t.GetCode(), code) {
			matches = append(matches, t)
		}
	}
	switch len(matches) {
	case 0:
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("task %s not found", code))
	case 1:
		return matches[0], nil
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("multiple tasks match code %s: specify --wish-id", code))
	}
}

func (h *Harness) resolveBaseAndHead(ctx context.Context, project *planv1.Project, wish *planv1.Wish, task *planv1.Task) (base, head string, err error) {
	dir := project.GetDirectory()
	// If integrated and committed, use the merge commit and its first parent.
	if task.GetIntegration() != nil && task.GetIntegration().GetSha() != "" && task.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
		sha := task.GetIntegration().GetSha()
		return sha + "^1", sha, nil
	}

	branch := task.GetBranch()
	if branch == "" {
		return "", "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s has no branch", task.GetCode()))
	}
	head = branch

	var targetBranch string
	if wish != nil {
		targetBranch, _ = h.integrationBranch(ctx, wish, project)
	}
	if targetBranch != "" {
		if mb, err := git(ctx, dir, "merge-base", branch, targetBranch); err == nil && mb != "" {
			return mb, head, nil
		}
	}
	if mb, err := git(ctx, dir, "merge-base", branch, "HEAD"); err == nil && mb != "" {
		return mb, head, nil
	}
	if p, err := git(ctx, dir, "rev-parse", branch+"~1"); err == nil && p != "" {
		return p, head, nil
	}
	if hSha, err := git(ctx, dir, "rev-parse", "HEAD"); err == nil && hSha != "" {
		return hSha, head, nil
	}
	return "HEAD", head, nil
}

func (h *Harness) diffFiles(ctx context.Context, dir, base, head string, globs []string) ([]*planv1.TaskDiffFile, error) {
	rawOut, err := gitRaw(ctx, dir, "diff", "--raw", "-z", "-M", base, head)
	if err != nil {
		return nil, err
	}
	numstatOut, err := gitRaw(ctx, dir, "diff", "--numstat", "-z", "-M", base, head)
	if err != nil {
		return nil, err
	}

	// Parse numstat output: <added>\t<deleted>\t<path>\0, or for renames: <added>\t<deleted>\t<old>\0<new>\0
	type stat struct {
		added   int32
		deleted int32
		binary  bool
	}
	stats := make(map[string]stat)
	numTokens := strings.Split(numstatOut, "\x00")
	for i := 0; i < len(numTokens); i++ {
		entry := numTokens[i]
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, "\t")
		if len(parts) < 3 {
			continue
		}
		addStr, delStr, p := parts[0], parts[1], parts[2]
		var isBin bool
		var add, del int64
		if addStr == "-" || delStr == "-" {
			isBin = true
		} else {
			add, _ = strconv.ParseInt(addStr, 10, 32)
			del, _ = strconv.ParseInt(delStr, 10, 32)
		}

		filePath := p
		// If renamed, numstat has old_path and then next token is new_path
		if p == "" && i+1 < len(numTokens) {
			// old_path is empty in parts[2]? No, in git numstat -z, rename has parts[2] = old, and numTokens[i+1] = new.
			i++
			filePath = numTokens[i]
		}
		stats[filePath] = stat{added: int32(add), deleted: int32(del), binary: isBin}
	}

	// Parse --raw output
	var files []*planv1.TaskDiffFile
	rawTokens := strings.Split(rawOut, "\x00")
	for i := 0; i < len(rawTokens); i++ {
		header := rawTokens[i]
		if !strings.HasPrefix(header, ":") {
			continue
		}
		fields := strings.Fields(header)
		if len(fields) < 5 {
			continue
		}
		statusCode := fields[4]

		if i+1 >= len(rawTokens) {
			break
		}
		i++
		srcPath := rawTokens[i]
		destPath := srcPath
		var oldPath string

		status := planv1.DiffStatus_DIFF_STATUS_MODIFIED
		renamed := false
		switch {
		case strings.HasPrefix(statusCode, "A"):
			status = planv1.DiffStatus_DIFF_STATUS_ADDED
		case strings.HasPrefix(statusCode, "D"):
			status = planv1.DiffStatus_DIFF_STATUS_DELETED
		case strings.HasPrefix(statusCode, "R"):
			status = planv1.DiffStatus_DIFF_STATUS_RENAMED
			renamed = true
			if i+1 < len(rawTokens) {
				i++
				destPath = rawTokens[i]
				oldPath = srcPath
			}
		case strings.HasPrefix(statusCode, "C"):
			status = planv1.DiffStatus_DIFF_STATUS_COPIED
			if i+1 < len(rawTokens) {
				i++
				destPath = rawTokens[i]
				oldPath = srcPath
			}
		}

		st := stats[destPath]
		if !st.binary && (st.added == 0 && st.deleted == 0) {
			// Check if stats recorded under srcPath
			if s, ok := stats[srcPath]; ok {
				st = s
			}
		}

		gen := isGenerated(destPath, globs)
		lock := isLockFile(destPath)
		folded := gen || lock || st.binary

		files = append(files, &planv1.TaskDiffFile{
			Path:      destPath,
			OldPath:   oldPath,
			Added:     st.added,
			Deleted:   st.deleted,
			Binary:    st.binary,
			Renamed:   renamed,
			Generated: gen,
			Lock:      lock,
			Folded:    folded,
			Status:    status,
		})
	}

	return files, nil
}

func (h *Harness) diffSingleFile(ctx context.Context, dir, base, head, filePath string, globs []string) (*planv1.TaskFileDiff, error) {
	out, err := gitRaw(ctx, dir, "diff", "-U3", "-M", base, head, "--", filePath)
	if err != nil {
		return nil, err
	}

	gen := isGenerated(filePath, globs)
	lock := isLockFile(filePath)
	binary := strings.Contains(out, "Binary files")

	totalLines := strings.Count(out, "\n")
	if totalLines > maxDiffLines {
		cmd := fmt.Sprintf("git diff %s %s -- %s", base, head, filePath)
		return &planv1.TaskFileDiff{
			Path:       filePath,
			Generated:  gen,
			Lock:       lock,
			Binary:     binary,
			Folded:     gen || lock || binary,
			TooLarge:   true,
			TotalLines: int32(totalLines),
			Command:    cmd,
		}, nil
	}

	hunks, added, deleted, isBin := parseHunks(out, filePath)
	if isBin {
		binary = true
	}

	return &planv1.TaskFileDiff{
		Path:       filePath,
		Generated:  gen,
		Lock:       lock,
		Binary:     binary,
		Folded:     gen || lock || binary,
		Added:      added,
		Deleted:    deleted,
		Hunks:      hunks,
		Patch:      out,
		TotalLines: int32(totalLines),
	}, nil
}

func parseHunks(diffText, filePath string) (hunks []*planv1.DiffHunk, added, deleted int32, binary bool) {
	lexer := lexers.Match(filePath)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	lines := strings.Split(diffText, "\n")
	var currentHunk *planv1.DiffHunk
	var oldLine, newLine int32

	for _, line := range lines {
		if strings.HasPrefix(line, "Binary files") {
			return nil, 0, 0, true
		}
		if strings.HasPrefix(line, "@@") {
			if currentHunk != nil {
				hunks = append(hunks, currentHunk)
			}
			hunkHeader, oStart, oLines, nStart, nLines := parseHunkHeader(line)
			oldLine = oStart
			newLine = nStart
			currentHunk = &planv1.DiffHunk{
				Header:   hunkHeader,
				OldStart: oStart,
				OldLines: oLines,
				NewStart: nStart,
				NewLines: nLines,
			}
			continue
		}
		if currentHunk == nil {
			// Diff metadata before the first hunk (diff --git, index, ---, +++)
			continue
		}

		if len(line) == 0 {
			// Empty line in hunk is treated as context
			currentHunk.Lines = append(currentHunk.Lines, &planv1.DiffLine{
				Kind:    planv1.DiffLineKind_DIFF_LINE_KIND_CONTEXT,
				OldLine: oldLine,
				NewLine: newLine,
				Text:    "",
			})
			oldLine++
			newLine++
			continue
		}

		prefix := line[0]
		text := line[1:]

		switch prefix {
		case ' ':
			tokens := highlightLine(lexer, text)
			currentHunk.Lines = append(currentHunk.Lines, &planv1.DiffLine{
				Kind:    planv1.DiffLineKind_DIFF_LINE_KIND_CONTEXT,
				OldLine: oldLine,
				NewLine: newLine,
				Text:    text,
				Tokens:  tokens,
			})
			oldLine++
			newLine++
		case '+':
			added++
			tokens := highlightLine(lexer, text)
			currentHunk.Lines = append(currentHunk.Lines, &planv1.DiffLine{
				Kind:    planv1.DiffLineKind_DIFF_LINE_KIND_ADDED,
				OldLine: 0,
				NewLine: newLine,
				Text:    text,
				Tokens:  tokens,
			})
			newLine++
		case '-':
			deleted++
			tokens := highlightLine(lexer, text)
			currentHunk.Lines = append(currentHunk.Lines, &planv1.DiffLine{
				Kind:    planv1.DiffLineKind_DIFF_LINE_KIND_DELETED,
				OldLine: oldLine,
				NewLine: 0,
				Text:    text,
				Tokens:  tokens,
			})
			oldLine++
		case '\\':
			// "\ No newline at end of file"
			continue
		}
	}

	if currentHunk != nil {
		hunks = append(hunks, currentHunk)
	}

	return hunks, added, deleted, binary
}

func parseHunkHeader(headerLine string) (header string, oldStart, oldLines, newStart, newLines int32) {
	header = headerLine
	// @@ -1,5 +1,6 @@ optional section heading
	parts := strings.Split(headerLine, "@@")
	if len(parts) < 3 {
		return header, 1, 0, 1, 0
	}
	rangeStr := strings.TrimSpace(parts[1])
	rangeParts := strings.Fields(rangeStr)
	for _, rp := range rangeParts {
		if strings.HasPrefix(rp, "-") {
			nums := strings.Split(rp[1:], ",")
			s, _ := strconv.ParseInt(nums[0], 10, 32)
			oldStart = int32(s)
			if len(nums) > 1 {
				l, _ := strconv.ParseInt(nums[1], 10, 32)
				oldLines = int32(l)
			} else {
				oldLines = 1
			}
		} else if strings.HasPrefix(rp, "+") {
			nums := strings.Split(rp[1:], ",")
			s, _ := strconv.ParseInt(nums[0], 10, 32)
			newStart = int32(s)
			if len(nums) > 1 {
				l, _ := strconv.ParseInt(nums[1], 10, 32)
				newLines = int32(l)
			} else {
				newLines = 1
			}
		}
	}
	return header, oldStart, oldLines, newStart, newLines
}

func highlightLine(lexer chroma.Lexer, text string) []*planv1.DiffToken {
	if text == "" {
		return nil
	}
	iterator, err := lexer.Tokenise(nil, text)
	if err != nil {
		return []*planv1.DiffToken{{Text: text}}
	}
	var tokens []*planv1.DiffToken
	for t := iterator(); t != chroma.EOF; t = iterator() {
		cat := tokenCategory(t.Type)
		tokens = append(tokens, &planv1.DiffToken{
			Text:     t.Value,
			Category: cat,
		})
	}
	return tokens
}

func tokenCategory(t chroma.TokenType) string {
	switch {
	case t == chroma.Whitespace:
		return ""
	case t.InCategory(chroma.Comment):
		return "comment"
	case t.InCategory(chroma.Keyword):
		return "keyword"
	case t.InSubCategory(chroma.NameFunction):
		return "function"
	case t == chroma.NameClass || t == chroma.NameConstant || t == chroma.NameEntity || t == chroma.NameNamespace || t == chroma.NameTag || t == chroma.NameLabel:
		return "type"
	case t == chroma.NameVariable || t.InSubCategory(chroma.NameVariable) || t == chroma.NameProperty:
		return "variable"
	case t.InSubCategory(chroma.LiteralString):
		return "string"
	case t.InSubCategory(chroma.LiteralNumber):
		return "number"
	case t.InCategory(chroma.Operator) || t.InCategory(chroma.Punctuation):
		return "operator"
	case t == chroma.GenericHeading || t == chroma.GenericSubheading:
		return "heading"
	case t == chroma.GenericDeleted:
		return "deleted"
	case t == chroma.GenericInserted:
		return "inserted"
	default:
		return ""
	}
}

func isGenerated(name string, globs []string) bool {
	for _, g := range globs {
		if matchGlob(g, name) {
			return true
		}
	}
	return false
}

func isLockFile(name string) bool {
	base := path.Base(name)
	switch base {
	case "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "go.sum", "Cargo.lock",
		"poetry.lock", "composer.lock", "Gemfile.lock", "flake.lock", "bun.lockb":
		return true
	}
	return strings.HasSuffix(base, ".lock")
}
