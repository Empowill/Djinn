package harness

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
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

// diffChunkLines is the most lines of a file's diff one answer holds: past it, the file comes by chunks.
const diffChunkLines = 2000

// Diff returns the list of files changed by a task, or the unified diff with syntax tokens for a single file.
func (h *Harness) Diff(ctx context.Context, procedure string, req *planv1.TaskServiceDiffRequest) (*planv1.TaskServiceDiffResponse, error) {
	task, err := h.resolveTask(ctx, req.GetTask(), req.GetWishId())
	if err != nil {
		return nil, err
	}
	_, _ = h.checkCommentsResolution(ctx, task)
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

	fileDiff, err := h.diffSingleFile(ctx, dir, base, head, targetPath, globs, req.GetOffset(), req.GetLimit())
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

// diffSingleFile reads the file's unified diff from git line by line and keeps only the chunk [offset,
// offset+limit): a long diff is never held whole, nor highlighted beyond what the reader asked for.
func (h *Harness) diffSingleFile(ctx context.Context, dir, base, head, filePath string, globs []string, offset, limit int32) (*planv1.TaskFileDiff, error) {
	if limit <= 0 || limit > diffChunkLines {
		limit = diffChunkLines
	}
	r := newDiffChunk(filePath, int(offset), int(offset)+int(limit))
	if err := gitLines(ctx, dir, r.line, "diff", "-U3", "-M", base, head, "--", filePath); err != nil {
		return nil, err
	}
	r.end()

	gen := isGenerated(filePath, globs)
	lock := isLockFile(filePath)
	d := &planv1.TaskFileDiff{
		Path:       filePath,
		Generated:  gen,
		Lock:       lock,
		Binary:     r.binary,
		Folded:     gen || lock || r.binary,
		Added:      r.added,
		Deleted:    r.deleted,
		Hunks:      r.hunks,
		Patch:      r.patch.String(),
		TotalLines: int32(r.n),
		Offset:     offset,
	}
	if r.n > r.to {
		d.NextOffset = int32(r.to)
	}
	if r.binary {
		d.Hunks = nil
	}
	return d, nil
}

// gitLines runs git in dir and hands its output to visit one line at a time, without its newline.
func gitLines(ctx context.Context, dir string, visit func(string), args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("git %s: %w", args[0], err)
	}
	br := bufio.NewReader(out)
	for {
		line, rerr := br.ReadString('\n')
		if line != "" {
			visit(strings.TrimSuffix(line, "\n"))
		}
		if rerr != nil {
			break
		}
	}
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git %s: %s", args[0], msg)
	}
	return nil
}

// diffChunk parses a unified diff line by line: it counts every line and every change, and keeps the hunks of the
// lines in [from, to) only, with their syntax tokens.
type diffChunk struct {
	lexer            chroma.Lexer
	from, to         int
	n                int // lines read so far
	header           *planv1.DiffHunk
	cur              *planv1.DiffHunk
	hunks            []*planv1.DiffHunk
	patch            strings.Builder
	oldLine, newLine int32
	added            int32
	deleted          int32
	binary           bool
}

func newDiffChunk(filePath string, from, to int) *diffChunk {
	lexer := lexers.Match(filePath)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	return &diffChunk{lexer: chroma.Coalesce(lexer), from: from, to: to}
}

func (c *diffChunk) line(line string) {
	in := c.n >= c.from && c.n < c.to
	c.n++
	if in {
		c.patch.WriteString(line)
		c.patch.WriteByte('\n')
	}
	if strings.HasPrefix(line, "@@") {
		c.end()
		hunkHeader, oStart, oLines, nStart, nLines := parseHunkHeader(line)
		c.oldLine, c.newLine = oStart, nStart
		c.header = &planv1.DiffHunk{Header: hunkHeader, OldStart: oStart, OldLines: oLines, NewStart: nStart, NewLines: nLines}
		if in {
			c.cur = c.header
		}
		return
	}
	if c.header == nil {
		// Diff metadata before the first hunk (diff --git, index, ---, +++), or a binary file's notice.
		if strings.HasPrefix(line, "Binary files") {
			c.binary = true
		}
		return
	}

	kind := planv1.DiffLineKind_DIFF_LINE_KIND_CONTEXT
	text := line
	if line != "" {
		text = line[1:]
		switch line[0] {
		case '+':
			kind = planv1.DiffLineKind_DIFF_LINE_KIND_ADDED
		case '-':
			kind = planv1.DiffLineKind_DIFF_LINE_KIND_DELETED
		case '\\':
			// "\ No newline at end of file"
			return
		}
	}
	l := &planv1.DiffLine{Kind: kind}
	switch kind {
	case planv1.DiffLineKind_DIFF_LINE_KIND_ADDED:
		c.added++
		l.NewLine = c.newLine
		c.newLine++
	case planv1.DiffLineKind_DIFF_LINE_KIND_DELETED:
		c.deleted++
		l.OldLine = c.oldLine
		c.oldLine++
	default:
		l.OldLine, l.NewLine = c.oldLine, c.newLine
		c.oldLine++
		c.newLine++
	}
	if !in {
		return
	}
	if c.cur == nil {
		// The chunk starts within a hunk shown in an earlier chunk: carry it on, its header said there.
		c.cur = &planv1.DiffHunk{
			Header: c.header.GetHeader(), OldStart: c.header.GetOldStart(), OldLines: c.header.GetOldLines(),
			NewStart: c.header.GetNewStart(), NewLines: c.header.GetNewLines(), Continued: true,
		}
	}
	l.Text = text
	l.Tokens = highlightLine(c.lexer, text)
	c.cur.Lines = append(c.cur.Lines, l)
}

// end closes the hunk being filled.
func (c *diffChunk) end() {
	if c.cur != nil {
		c.hunks = append(c.hunks, c.cur)
		c.cur = nil
	}
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
