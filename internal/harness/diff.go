package harness

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/gitx"
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

	settings, _ := plan.LoadSettings(h.home, project)
	globs := append([]string{"gen/**"}, settings.Generated...)

	dir := project.GetDirectory()
	if req.GetUncommitted() {
		return diffUncommitted(ctx, task.GetWorktree(), dir, globs, req)
	}

	wish, _ := store.Get[*planv1.Wish](ctx, h.store, task.GetWishId())
	base, head, err := h.resolveBaseAndHead(ctx, project, wish, task)
	if err != nil {
		return nil, err
	}

	// If no specific file was requested, return the list of changed files with counts.
	if req.GetFile() == "" {
		files, err := diffFiles(ctx, dir, globs, base, head)
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
	targetPath := diffPath(req.GetFile(), dir)
	fileDiff, err := diffSingleFile(ctx, targetPath, globs, req.GetOffset(), req.GetLimit(), gitDiffLines(dir, targetPath, base, head))
	if err != nil {
		return nil, err
	}

	return &planv1.TaskServiceDiffResponse{
		File: fileDiff,
		Base: base,
		Head: head,
	}, nil
}

// diffUncommitted is Diff for what the worktree wt holds not committed, against its HEAD: the changes to tracked files,
// staged or not, then the untracked files, whole. A worktree never made, or removed since, has nothing to show.
func diffUncommitted(ctx context.Context, wt, projectDir string, globs []string, req *planv1.TaskServiceDiffRequest) (*planv1.TaskServiceDiffResponse, error) {
	if wt == "" {
		return &planv1.TaskServiceDiffResponse{NoWorktree: true}, nil
	}
	if _, err := os.Stat(wt); errors.Is(err, os.ErrNotExist) {
		return &planv1.TaskServiceDiffResponse{NoWorktree: true}, nil
	}
	base, err := git(ctx, wt, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return nil, err
	}
	if req.GetFile() == "" {
		files, err := diffFiles(ctx, wt, globs, base)
		if err != nil {
			return nil, err
		}
		untracked, err := untrackedFiles(ctx, wt, "")
		if err != nil {
			return nil, err
		}
		for _, name := range untracked {
			lines, binary, err := scanUntracked(filepath.Join(wt, filepath.FromSlash(name)))
			if err != nil {
				return nil, err
			}
			gen, lock := isGenerated(name, globs), isLockFile(name)
			files = append(files, &planv1.TaskDiffFile{
				Path:      name,
				Added:     lines,
				Binary:    binary,
				Generated: gen,
				Lock:      lock,
				Folded:    gen || lock || binary,
				Status:    planv1.DiffStatus_DIFF_STATUS_ADDED,
			})
		}
		slices.SortFunc(files, func(a, b *planv1.TaskDiffFile) int { return strings.Compare(a.GetPath(), b.GetPath()) })
		return &planv1.TaskServiceDiffResponse{Files: files, Base: base}, nil
	}

	// The command line makes a file absolute from the caller's folder: the worktree's, or the project's.
	name := diffPath(req.GetFile(), wt, projectDir)
	untracked, err := untrackedFiles(ctx, wt, name)
	if err != nil {
		return nil, err
	}
	lines := gitDiffLines(wt, name, base)
	if len(untracked) > 0 {
		lines = func(_ context.Context, visit func(string)) error {
			return untrackedDiff(filepath.Join(wt, filepath.FromSlash(name)), name, visit)
		}
	}
	fileDiff, err := diffSingleFile(ctx, name, globs, req.GetOffset(), req.GetLimit(), lines)
	if err != nil {
		return nil, err
	}
	return &planv1.TaskServiceDiffResponse{File: fileDiff, Base: base}, nil
}

// diffPath is a file of a diff request relative to the repository root, given relative already or absolute within one
// of dirs.
func diffPath(file string, dirs ...string) string {
	if filepath.IsAbs(file) {
		for _, dir := range dirs {
			if rel, err := filepath.Rel(dir, file); err == nil && !strings.HasPrefix(rel, "..") {
				file = rel
				break
			}
		}
	}
	return strings.TrimPrefix(filepath.ToSlash(filepath.Clean(file)), "/")
}

// untrackedFiles lists the files git does not track in the worktree wt, the .gitignore applying, one by one: all of
// them, or only name when it is one.
func untrackedFiles(ctx context.Context, wt, name string) ([]string, error) {
	args := []string{"ls-files", "--others", "--exclude-standard", "-z"}
	if name != "" {
		args = append(args, "--", ":(literal)"+name)
	}
	out, err := gitRaw(ctx, wt, args...)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		// A folder holding a repository of its own is listed as itself, with a slash: it is no file to show.
		if f != "" && !strings.HasSuffix(f, "/") {
			files = append(files, f)
		}
	}
	return files, nil
}

// binarySniff is how much of a file git reads to tell a binary one: a NUL byte there.
const binarySniff = 8000

// untrackedContent opens the content git would diff for the untracked file at p: a symbolic link's target, or the
// file's bytes.
func untrackedContent(p string) (io.ReadCloser, string, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return nil, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(p)
		if err != nil {
			return nil, "", err
		}
		return io.NopCloser(strings.NewReader(filepath.ToSlash(target))), "120000", nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, "", err
	}
	mode := "100644"
	if info.Mode()&0o111 != 0 {
		mode = "100755"
	}
	return f, mode, nil
}

// scanUntracked counts the lines of the untracked file at p, and tells whether it is binary.
func scanUntracked(p string) (lines int32, binary bool, err error) {
	f, _, err := untrackedContent(p)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	buf := make([]byte, 64<<10)
	var read int64
	last := byte('\n')
	for {
		n, rerr := f.Read(buf)
		if chunk := buf[:n]; len(chunk) > 0 {
			if read < binarySniff && bytes.IndexByte(chunk[:min(int64(len(chunk)), binarySniff-read)], 0) >= 0 {
				return 0, true, nil
			}
			read += int64(len(chunk))
			lines += int32(bytes.Count(chunk, []byte{'\n'}))
			last = chunk[len(chunk)-1]
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return 0, false, rerr
		}
	}
	if last != '\n' {
		lines++
	}
	return lines, false, nil
}

// untrackedDiff hands visit the lines of the unified diff git would print for the untracked file name, at p: the whole
// file added, or a binary file's notice.
func untrackedDiff(p, name string, visit func(string)) error {
	lines, binary, err := scanUntracked(p)
	if err != nil {
		return err
	}
	f, mode, err := untrackedContent(p)
	if err != nil {
		return err
	}
	defer f.Close()
	visit("diff --git a/" + name + " b/" + name)
	visit("new file mode " + mode)
	if binary {
		visit("Binary files /dev/null and b/" + name + " differ")
		return nil
	}
	if lines == 0 {
		return nil
	}
	visit("--- /dev/null")
	visit("+++ b/" + name)
	if lines == 1 {
		visit("@@ -0,0 +1 @@")
	} else {
		visit(fmt.Sprintf("@@ -0,0 +1,%d @@", lines))
	}
	br := bufio.NewReader(f)
	for {
		line, rerr := br.ReadString('\n')
		if line != "" {
			visit("+" + strings.TrimSuffix(line, "\n"))
			if !strings.HasSuffix(line, "\n") {
				visit("\\ No newline at end of file")
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
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
	branch := task.GetBranch()
	var bSha string
	if branch != "" {
		bSha, _ = git(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
		if task.GetReview() == nil && task.GetCorrection() == nil && branchHasNoCommits(ctx, dir, branch) {
			if bSha != "" {
				return bSha, bSha, nil
			}
			if sha := task.GetIntegration().GetSha(); sha != "" {
				return sha, sha, nil
			}
		}
	}

	// If integrated and committed, use the merge commit and its first parent, unless
	// Integration.Sha is not the 2-parent merge commit of branch (e.g. a multi-task batch or alreadyIn).
	if task.GetIntegration() != nil && task.GetIntegration().GetSha() != "" && task.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
		sha := task.GetIntegration().GetSha()
		if bSha != "" && task.GetReview() == nil && task.GetCorrection() == nil {
			if p2, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", sha+"^2^{commit}"); err != nil || p2 != bSha {
				if start := branchStartCommit(ctx, dir, branch); start != "" && start != bSha {
					return start, bSha, nil
				}
			}
		}
		return sha + "^1", sha, nil
	}

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
			if bSha != "" && mb == bSha {
				if start := branchStartCommit(ctx, dir, branch); start != "" && start != bSha {
					return start, head, nil
				}
			}
			return mb, head, nil
		}
	}
	if mb, err := git(ctx, dir, "merge-base", branch, "HEAD"); err == nil && mb != "" {
		if bSha != "" && mb == bSha {
			if start := branchStartCommit(ctx, dir, branch); start != "" && start != bSha {
				return start, head, nil
			}
		}
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

// diffFiles lists the files git diff revs changes in dir, with their counts: between two commits, or from one to the
// worktree's files.
func diffFiles(ctx context.Context, dir string, globs []string, revs ...string) ([]*planv1.TaskDiffFile, error) {
	rawOut, err := gitRaw(ctx, dir, append([]string{"diff", "--raw", "-z", "-M"}, revs...)...)
	if err != nil {
		return nil, err
	}
	numstatOut, err := gitRaw(ctx, dir, append([]string{"diff", "--numstat", "-z", "-M"}, revs...)...)
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

// diffLines hands visit the lines of a file's unified diff, one at a time, without their newline.
type diffLines func(ctx context.Context, visit func(string)) error

// gitDiffLines reads the unified diff of filePath from git diff revs in dir: between two commits, or from one to the
// worktree's files.
func gitDiffLines(dir, filePath string, revs ...string) diffLines {
	return func(ctx context.Context, visit func(string)) error {
		return gitLines(ctx, dir, visit, append(append([]string{"diff", "-U3", "-M"}, revs...), "--", filePath)...)
	}
}

// diffSingleFile reads the file's unified diff line by line and keeps only the chunk [offset, offset+limit): a long
// diff is never held whole, nor highlighted beyond what the reader asked for.
func diffSingleFile(ctx context.Context, filePath string, globs []string, offset, limit int32, lines diffLines) (*planv1.TaskFileDiff, error) {
	if limit <= 0 || limit > diffChunkLines {
		limit = diffChunkLines
	}
	r := newDiffChunk(filePath, int(offset), int(offset)+int(limit))
	if err := lines(ctx, r.line); err != nil {
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
	cmd, cancel := gitx.Command(ctx, dir, args...)
	defer cancel()
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
