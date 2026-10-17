package harness

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

func TestDiff(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()

	// 1. Set up a real git repo with base files.
	repo := gitRepo(t)
	mainGo := filepath.Join(repo, "main.go")
	if err := os.WriteFile(mainGo, []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldGo := filepath.Join(repo, "old.go")
	if err := os.WriteFile(oldGo, []byte("package main\n\nfunc Old() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "--quiet", "-m", "Base commit"},
	} {
		if _, err := git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}

	baseBranch := plan.CheckedOutBranch(ctx, repo)

	// 2. Create task branch with added, modified, renamed, generated, lock, binary, and large files.
	branch := "djinn/W1-diff-task"
	if _, err := git(ctx, repo, "checkout", "-b", branch); err != nil {
		t.Fatal(err)
	}

	// Added file
	addedGo := filepath.Join(repo, "added.go")
	if err := os.WriteFile(addedGo, []byte("package main\n\nfunc Added() string {\n\treturn \"added\"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Modified file
	if err := os.WriteFile(mainGo, []byte("package main\n\nfunc main() {\n\tprintln(\"modified world\")\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Renamed file
	if _, err := git(ctx, repo, "mv", "old.go", "renamed.go"); err != nil {
		t.Fatal(err)
	}

	// Generated file (under gen/)
	genDir := filepath.Join(repo, "gen")
	if err := os.MkdirAll(genDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(genDir, "client.go"), []byte("package gen\n\n// Code generated. DO NOT EDIT.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Lock file
	if err := os.WriteFile(filepath.Join(repo, "package-lock.json"), []byte("{\n  \"name\": \"app\",\n  \"lockfileVersion\": 3\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Binary file
	if err := os.WriteFile(filepath.Join(repo, "image.png"), []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00}, 0o600); err != nil {
		t.Fatal(err)
	}

	// Large file (2,500 lines)
	var largeBuf bytes.Buffer
	for i := 1; i <= 2500; i++ {
		fmt.Fprintf(&largeBuf, "Line %d of a very large diff\n", i)
	}
	if err := os.WriteFile(filepath.Join(repo, "large.txt"), largeBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"add", "."},
		{"commit", "--quiet", "-m", "Task changes"},
	} {
		if _, err := git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}

	// Switch back to base branch
	if _, err := git(ctx, repo, "checkout", baseBranch); err != nil {
		t.Fatal(err)
	}

	// 3. Start test harness environment
	e := up(t, t.TempDir())
	wishID, projectID := e.wish(t, repo)

	taskID := "01a12863-c9d3-7852-99ce-ac3cff3fae5c"
	err := e.db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
			return err
		}
		return tx.Put(&planv1.Task{
			Id:         taskID,
			WishId:     wishID,
			ProjectId:  projectID,
			Code:       "W1",
			Title:      "Diff inspection task",
			Branch:     branch,
			Status:     planv1.TaskStatus_TASK_STATUS_DONE,
			CreateTime: timestamppb.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	// 4. Test TaskService.Diff file list (req.File == "")
	diffReq := &planv1.TaskServiceDiffRequest{
		Task:   &planv1.TaskRef{Ref: &planv1.TaskRef_Code{Code: "W1"}},
		WishId: wishID,
	}
	res, err := e.tasks.Diff(ctx, connect.NewRequest(diffReq))
	if err != nil {
		t.Fatalf("TaskService.Diff files list failed: %v", err)
	}
	files := res.Msg.GetFiles()
	if len(files) == 0 {
		t.Fatalf("expected changed files, got 0")
	}

	byPath := make(map[string]*planv1.TaskDiffFile)
	for _, f := range files {
		byPath[f.GetPath()] = f
	}

	// Verify added file
	if f, ok := byPath["added.go"]; !ok {
		t.Errorf("missing added.go")
	} else {
		if f.GetStatus() != planv1.DiffStatus_DIFF_STATUS_ADDED {
			t.Errorf("added.go status = %v, want ADDED", f.GetStatus())
		}
		if f.GetAdded() == 0 {
			t.Errorf("added.go additions = 0, want > 0")
		}
	}

	// Verify modified file
	if f, ok := byPath["main.go"]; !ok {
		t.Errorf("missing main.go")
	} else {
		if f.GetStatus() != planv1.DiffStatus_DIFF_STATUS_MODIFIED {
			t.Errorf("main.go status = %v, want MODIFIED", f.GetStatus())
		}
		if f.GetAdded() == 0 || f.GetDeleted() == 0 {
			t.Errorf("main.go additions/deletions = +%d/-%d, want both > 0", f.GetAdded(), f.GetDeleted())
		}
	}

	// Verify renamed file
	if f, ok := byPath["renamed.go"]; !ok {
		t.Errorf("missing renamed.go")
	} else {
		if f.GetStatus() != planv1.DiffStatus_DIFF_STATUS_RENAMED {
			t.Errorf("renamed.go status = %v, want RENAMED", f.GetStatus())
		}
		if f.GetOldPath() != "old.go" {
			t.Errorf("renamed.go old_path = %q, want old.go", f.GetOldPath())
		}
	}

	// Verify generated file
	if f, ok := byPath["gen/client.go"]; !ok {
		t.Errorf("missing gen/client.go")
	} else {
		if !f.GetGenerated() {
			t.Errorf("gen/client.go generated = false, want true")
		}
		if !f.GetFolded() {
			t.Errorf("gen/client.go folded = false, want true")
		}
	}

	// Verify lock file
	if f, ok := byPath["package-lock.json"]; !ok {
		t.Errorf("missing package-lock.json")
	} else {
		if !f.GetLock() {
			t.Errorf("package-lock.json lock = false, want true")
		}
		if !f.GetFolded() {
			t.Errorf("package-lock.json folded = false, want true")
		}
	}

	// Verify binary file
	if f, ok := byPath["image.png"]; !ok {
		t.Errorf("missing image.png")
	} else {
		if !f.GetBinary() {
			t.Errorf("image.png binary = false, want true")
		}
		if !f.GetFolded() {
			t.Errorf("image.png folded = false, want true")
		}
	}

	// 5. Test single file diff for main.go (with Chroma tokens)
	fileDiffReq := &planv1.TaskServiceDiffRequest{
		Task:   &planv1.TaskRef{Ref: &planv1.TaskRef_Code{Code: "W1"}},
		File:   "main.go",
		WishId: wishID,
	}
	resFile, err := e.tasks.Diff(ctx, connect.NewRequest(fileDiffReq))
	if err != nil {
		t.Fatalf("TaskService.Diff single file failed: %v", err)
	}
	d := resFile.Msg.GetFile()
	if d == nil {
		t.Fatalf("expected Diff message, got nil")
	}
	if d.GetPath() != "main.go" {
		t.Errorf("diff path = %q, want main.go", d.GetPath())
	}
	if len(d.GetHunks()) == 0 {
		t.Fatalf("expected hunks in main.go, got 0")
	}
	hasTokens := false
	for _, h := range d.GetHunks() {
		for _, l := range h.GetLines() {
			if len(l.GetTokens()) > 0 {
				hasTokens = true
				break
			}
		}
	}
	if !hasTokens {
		t.Errorf("expected syntax tokens in main.go diff lines")
	}

	// 6. A diff past 2 000 lines comes by chunks: large.txt is a new file of 2 500 lines, its diff 2 506 with the
	// header (5 lines) and the hunk's.
	chunk := func(offset, limit int32) *planv1.TaskFileDiff {
		t.Helper()
		res, err := e.tasks.Diff(ctx, connect.NewRequest(&planv1.TaskServiceDiffRequest{
			Task: &planv1.TaskRef{Ref: &planv1.TaskRef_Code{Code: "W1"}}, File: "large.txt", WishId: wishID,
			Offset: offset, Limit: limit,
		}))
		if err != nil {
			t.Fatalf("TaskService.Diff large.txt from %d: %v", offset, err)
		}
		return res.Msg.GetFile()
	}
	first := chunk(0, 0)
	if first.GetTotalLines() != 2506 || first.GetOffset() != 0 || first.GetNextOffset() != 2000 || first.GetAdded() != 2500 {
		t.Errorf("large.txt first chunk: total %d, offset %d, next %d, added %d; want 2506, 0, 2000, 2500",
			first.GetTotalLines(), first.GetOffset(), first.GetNextOffset(), first.GetAdded())
	}
	if h := first.GetHunks(); len(h) != 1 || h[0].GetContinued() || len(h[0].GetLines()) != 1994 ||
		h[0].GetLines()[1993].GetNewLine() != 1994 {
		t.Fatalf("large.txt first chunk hunks = %d, want one of 1994 lines ending at line 1994", len(h))
	}
	if n := strings.Count(first.GetPatch(), "\n"); n != 2000 || !strings.HasPrefix(first.GetPatch(), "diff --git") {
		t.Errorf("large.txt first chunk patch has %d lines, want the diff's first 2000", n)
	}
	last := chunk(first.GetNextOffset(), 0)
	if last.GetOffset() != 2000 || last.GetNextOffset() != 0 || last.GetTotalLines() != 2506 {
		t.Errorf("large.txt last chunk: offset %d, next %d, total %d; want 2000, 0, 2506",
			last.GetOffset(), last.GetNextOffset(), last.GetTotalLines())
	}
	if h := last.GetHunks(); len(h) != 1 || !h[0].GetContinued() || h[0].GetHeader() != "@@ -0,0 +1,2500 @@" ||
		len(h[0].GetLines()) != 506 || h[0].GetLines()[0].GetNewLine() != 1995 || h[0].GetLines()[505].GetText() != "Line 2500 of a very large diff" {
		t.Fatalf("large.txt last chunk does not carry the hunk on to line 2500: %v", h)
	}
	if small := chunk(10, 5); small.GetNextOffset() != 15 || len(small.GetHunks()[0].GetLines()) != 5 ||
		small.GetHunks()[0].GetLines()[0].GetNewLine() != 5 {
		t.Errorf("large.txt lines 10 to 15: next %d, want 15 and new lines 5 to 9", small.GetNextOffset())
	}
	if past := chunk(5000, 0); len(past.GetHunks()) != 0 || past.GetNextOffset() != 0 || past.GetTotalLines() != 2506 {
		t.Errorf("large.txt past its end: %d hunks, next %d", len(past.GetHunks()), past.GetNextOffset())
	}
	if _, err := e.tasks.Diff(ctx, connect.NewRequest(&planv1.TaskServiceDiffRequest{
		Task: &planv1.TaskRef{Ref: &planv1.TaskRef_Code{Code: "W1"}}, File: "large.txt", WishId: wishID, Limit: 2001,
	})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a chunk of 2001 lines: %v, want invalid_argument", err)
	}

	// 7. Test CLI: djinn task diff W1 and djinn task diff W1 --file main.go
	var stdout, stderr bytes.Buffer
	cliCfg := cli.Config{
		Version: "test",
		Addr:    e.srv.URL,
		HTTP:    e.srv.Client(),
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := cli.Run(ctx, []string{"task", "diff", "W1", "--wish-id", wishID}, cliCfg)
	if code != 0 {
		t.Fatalf("cli task diff W1 exited %d: %s", code, stderr.String())
	}
	outStr := stdout.String()
	if !strings.Contains(outStr, "main.go") || !strings.Contains(outStr, "added.go") {
		t.Errorf("cli task diff W1 stdout missing files: %s", outStr)
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run(ctx, []string{"task", "diff", "W1", "--file", "main.go", "--wish-id", wishID}, cliCfg)
	if code != 0 {
		t.Fatalf("cli task diff W1 --file main.go exited %d: %s", code, stderr.String())
	}
	fileOutStr := stdout.String()
	if !strings.Contains(fileOutStr, "main.go") {
		t.Errorf("cli task diff W1 --file stdout missing main.go: %s", fileOutStr)
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run(ctx, []string{"task", "diff", "W1", "--file", filepath.Join(repo, "large.txt"), "--offset", "2000", "--wish-id", wishID, "--json"}, cliCfg)
	if code != 0 {
		t.Fatalf("cli task diff W1 --file large.txt --offset 2000 exited %d: %s", code, stderr.String())
	}
	var cliLast planv1.TaskServiceDiffResponse
	if err := protojson.Unmarshal(stdout.Bytes(), &cliLast); err != nil {
		t.Fatal(err)
	}
	if f := cliLast.GetFile(); f.GetOffset() != 2000 || f.GetNextOffset() != 0 || !strings.HasSuffix(f.GetPatch(), "+Line 2500 of a very large diff\n") ||
		strings.Contains(f.GetPatch(), "Line 1994 of") {
		t.Errorf("cli task diff --offset 2000 does not show the last chunk alone: offset %d, %.200s", f.GetOffset(), f.GetPatch())
	}

	// 8. Test integrated task diff (from integration.sha against sha^1)
	mergeCommitSha, err := git(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	mergeCommitSha = strings.TrimSpace(mergeCommitSha)

	err = e.db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
			return err
		}
		return tx.Put(&planv1.Task{
			Id:        taskID,
			WishId:    wishID,
			ProjectId: projectID,
			Code:      "W1",
			Title:     "Integrated diff task",
			Branch:    branch,
			Status:    planv1.TaskStatus_TASK_STATUS_DONE,
			Integration: &planv1.TaskIntegration{
				Sha: mergeCommitSha,
			},
			CreateTime: timestamppb.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	resIntegrated, err := e.tasks.Diff(ctx, connect.NewRequest(diffReq))
	if err != nil {
		t.Fatalf("integrated task diff failed: %v", err)
	}
	if len(resIntegrated.Msg.GetFiles()) == 0 {
		t.Errorf("integrated task diff expected changed files, got 0")
	}

	// 9. Merge branch into baseBranch (--no-ff) and verify diff when Integration is not yet COMMITTED
	// (merge-base(branch, baseBranch) == branch, so resolveBaseAndHead uses branchStartCommit)
	// and when a subsequent 0-commit branch has a stale Integration.Sha pointing to the merge commit.
	if _, err := git(ctx, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "merge", "--no-ff", "-m", "Merge W1", branch); err != nil {
		t.Fatal(err)
	}
	mergedHead, err := git(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	noCommitBranch := "djinn/W2-no-commits"
	if _, err := git(ctx, repo, "branch", noCommitBranch, mergedHead); err != nil {
		t.Fatal(err)
	}
	err = e.db.Tx(ctx, func(tx *store.Tx) error {
		_ = tx.Journal("local", "test", &planv1.Task{})
		if err := tx.Put(&planv1.Task{
			Id:         taskID,
			WishId:     wishID,
			ProjectId:  projectID,
			Code:       "W1",
			Title:      "Merged branch without committed state",
			Branch:     branch,
			Status:     planv1.TaskStatus_TASK_STATUS_DONE,
			CreateTime: timestamppb.Now(),
		}); err != nil {
			return err
		}
		return tx.Put(&planv1.Task{
			Id:        store.NewID(),
			WishId:    wishID,
			ProjectId: projectID,
			Code:      "W2",
			Title:     "No-commit task with stale integration sha",
			Branch:    noCommitBranch,
			Status:    planv1.TaskStatus_TASK_STATUS_DONE,
			Integration: &planv1.TaskIntegration{
				State:  planv1.IntegrationState_INTEGRATION_STATE_COMMITTED,
				Branch: baseBranch,
				Sha:    mergedHead,
			},
			CreateTime: timestamppb.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	resAlreadyMerged, err := e.tasks.Diff(ctx, connect.NewRequest(diffReq))
	if err != nil {
		t.Fatalf("already-merged task diff failed: %v", err)
	}
	if len(resAlreadyMerged.Msg.GetFiles()) == 0 {
		t.Errorf("already-merged task diff expected changed files from branchStartCommit..branch, got 0")
	}
	resNoCommit, err := e.tasks.Diff(ctx, connect.NewRequest(&planv1.TaskServiceDiffRequest{
		Task:   &planv1.TaskRef{Ref: &planv1.TaskRef_Code{Code: "W2"}},
		WishId: wishID,
	}))
	if err != nil {
		t.Fatalf("no-commit task diff failed: %v", err)
	}
	if len(resNoCommit.Msg.GetFiles()) != 0 {
		t.Errorf("no-commit task diff expected 0 files even with stale integration sha, got %d", len(resNoCommit.Msg.GetFiles()))
	}
}

// TestDiffChunks reads a diff of several hunks by chunks of every size: put end to end, the chunks give back the whole
// diff's lines, a hunk cut by a chunk carried on in the next.
func TestDiffChunks(t *testing.T) {
	t.Parallel()
	diff := []string{
		"diff --git a/a.go b/a.go", "index 1..2 100644", "--- a/a.go", "+++ b/a.go",
		"@@ -1,3 +1,4 @@ func A()", " package a", "-var x = 1", "+var x = 2", "+var y = 3", " ",
		"@@ -10,2 +11,2 @@", " func B() {}", "-func C() {}", "\\ No newline at end of file", "+func D() {}",
	}
	read := func(from, to int) *diffChunk {
		c := newDiffChunk("a.go", from, to)
		for _, l := range diff {
			c.line(l)
		}
		c.end()
		return c
	}
	type row struct {
		hunk     string
		old, new int32
		text     string
	}
	rows := func(hunks []*planv1.DiffHunk) (out []row) {
		for _, h := range hunks {
			for _, l := range h.GetLines() {
				out = append(out, row{h.GetHeader(), l.GetOldLine(), l.GetNewLine(), l.GetText()})
			}
		}
		return out
	}
	whole := read(0, len(diff))
	want := rows(whole.hunks)
	if len(whole.hunks) != 2 || len(want) != 8 || whole.added != 3 || whole.deleted != 2 || whole.n != len(diff) {
		t.Fatalf("whole diff: %d hunks, %d lines, +%d -%d, %d read", len(whole.hunks), len(want), whole.added, whole.deleted, whole.n)
	}
	if want[2] != (row{"@@ -1,3 +1,4 @@ func A()", 0, 2, "var x = 2"}) || want[7] != (row{"@@ -10,2 +11,2 @@", 0, 12, "func D() {}"}) {
		t.Errorf("whole diff lines = %v", want)
	}
	for size := 1; size <= len(diff); size++ {
		var got []row
		var patch strings.Builder
		for from := 0; from < len(diff); from += size {
			c := read(from, from+size)
			if c.added != 3 || c.deleted != 2 || c.n != len(diff) {
				t.Errorf("chunk %d+%d: +%d -%d of %d lines, want the whole diff's counts", from, size, c.added, c.deleted, c.n)
			}
			for _, h := range c.hunks {
				// A hunk carries on one begun before the chunk when its header lies before the chunk's first line.
				if h.GetContinued() != (slices.Index(diff, h.GetHeader()) < from) {
					t.Errorf("chunk %d+%d hunk %s continued = %v", from, size, h.GetHeader(), h.GetContinued())
				}
			}
			got = append(got, rows(c.hunks)...)
			patch.WriteString(c.patch.String())
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("chunks of %d lines give %v, want %v", size, got, want)
		}
		if patch.String() != strings.Join(diff, "\n")+"\n" {
			t.Errorf("chunks of %d lines give back another patch: %q", size, patch.String())
		}
	}
}

// TestDiffUncommitted reads what a task's worktree holds not committed: a tracked file changed and not staged, a new
// file staged, untracked files, text or binary, the ignored ones left out; then nothing once the worktree is gone.
func TestDiffUncommitted(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()

	repo := gitRepo(t)
	write := func(dir, name, text string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(repo, "main.go", "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n")
	write(repo, ".gitignore", "*.log\n")
	for _, args := range [][]string{{"add", "."}, {"commit", "--quiet", "-m", "Base commit"}} {
		if _, err := git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	branch := "djinn/W1-uncommitted"
	wt := filepath.Join(t.TempDir(), "wt")
	if _, err := git(ctx, repo, "worktree", "add", "--quiet", "-b", branch, wt); err != nil {
		t.Fatal(err)
	}
	// Committed work, which the uncommitted view leaves to the task's diff.
	write(wt, "done.go", "package main\n")
	for _, args := range [][]string{{"add", "done.go"}, {"commit", "--quiet", "-m", "Done"}} {
		if _, err := git(ctx, wt, args...); err != nil {
			t.Fatal(err)
		}
	}
	head, err := git(ctx, wt, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	write(wt, "main.go", "package main\n\nfunc main() {\n\tprintln(\"lamp\")\n}\n")
	write(wt, "staged.go", "package main\n\nvar staged = 1\n")
	if _, err := git(ctx, wt, "add", "staged.go"); err != nil {
		t.Fatal(err)
	}
	write(wt, "notes/new file.md", "first\nsecond\nno newline")
	write(wt, "image.bin", "PNG\x00\x01")
	write(wt, "build.log", "ignored\n")

	e := up(t, t.TempDir())
	wishID, projectID := e.wish(t, repo)
	put := func(code, worktree string) {
		t.Helper()
		err := e.db.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
				return err
			}
			return tx.Put(&planv1.Task{
				Id: store.NewID(), WishId: wishID, ProjectId: projectID, Code: code, Title: "Uncommitted " + code,
				Branch: branch, Worktree: worktree, Status: planv1.TaskStatus_TASK_STATUS_RUNNING, CreateTime: timestamppb.Now(),
			})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	put("W1", wt)
	put("W2", "")
	diff := func(code, file string) *planv1.TaskServiceDiffResponse {
		t.Helper()
		res, err := e.tasks.Diff(ctx, connect.NewRequest(&planv1.TaskServiceDiffRequest{
			Task: &planv1.TaskRef{Ref: &planv1.TaskRef_Code{Code: code}}, WishId: wishID, File: file, Uncommitted: true,
		}))
		if err != nil {
			t.Fatalf("Diff %s %q: %v", code, file, err)
		}
		return res.Msg
	}

	list := diff("W1", "")
	if list.GetNoWorktree() || list.GetBase() != head || list.GetHead() != "" {
		t.Errorf("list: no_worktree %v, base %q head %q, want base %s and no head", list.GetNoWorktree(), list.GetBase(), list.GetHead(), head)
	}
	type file struct {
		path           string
		status         planv1.DiffStatus
		added, deleted int32
		binary         bool
	}
	var got []file
	for _, f := range list.GetFiles() {
		got = append(got, file{f.GetPath(), f.GetStatus(), f.GetAdded(), f.GetDeleted(), f.GetBinary()})
	}
	want := []file{
		{"image.bin", planv1.DiffStatus_DIFF_STATUS_ADDED, 0, 0, true},
		{"main.go", planv1.DiffStatus_DIFF_STATUS_MODIFIED, 1, 1, false},
		{"notes/new file.md", planv1.DiffStatus_DIFF_STATUS_ADDED, 3, 0, false},
		{"staged.go", planv1.DiffStatus_DIFF_STATUS_ADDED, 3, 0, false},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("files = %v, want %v", got, want)
	}
	if !list.GetFiles()[0].GetFolded() {
		t.Error("image.bin is not folded")
	}

	lines := func(d *planv1.TaskFileDiff) (out []string) {
		for _, h := range d.GetHunks() {
			for _, l := range h.GetLines() {
				out = append(out, fmt.Sprintf("%d %d %d %s", l.GetKind(), l.GetOldLine(), l.GetNewLine(), l.GetText()))
			}
		}
		return out
	}
	untracked := diff("W1", "notes/new file.md").GetFile()
	if got, want := lines(untracked), []string{"2 0 1 first", "2 0 2 second", "2 0 3 no newline"}; !slices.Equal(got, want) {
		t.Errorf("untracked lines = %q, want %q", got, want)
	}
	if untracked.GetAdded() != 3 || len(untracked.GetHunks()) != 1 || untracked.GetHunks()[0].GetHeader() != "@@ -0,0 +1,3 @@" {
		t.Errorf("untracked: +%d, hunks %v", untracked.GetAdded(), untracked.GetHunks())
	}
	if !strings.HasPrefix(untracked.GetPatch(), "diff --git a/notes/new file.md b/notes/new file.md\nnew file mode 100644\n--- /dev/null\n") ||
		!strings.HasSuffix(untracked.GetPatch(), "+no newline\n\\ No newline at end of file\n") {
		t.Errorf("untracked patch = %q", untracked.GetPatch())
	}
	// The command line gives a file absolute, from the worktree.
	modified := diff("W1", filepath.Join(wt, "main.go")).GetFile()
	if modified.GetPath() != "main.go" || modified.GetAdded() != 1 || modified.GetDeleted() != 1 || !strings.Contains(modified.GetPatch(), "+\tprintln(\"lamp\")") {
		t.Errorf("main.go: %s +%d -%d %q", modified.GetPath(), modified.GetAdded(), modified.GetDeleted(), modified.GetPatch())
	}
	if staged := diff("W1", "staged.go").GetFile(); staged.GetAdded() != 3 {
		t.Errorf("staged.go: +%d, want 3", staged.GetAdded())
	}
	if bin := diff("W1", "image.bin").GetFile(); !bin.GetBinary() || len(bin.GetHunks()) != 0 {
		t.Errorf("image.bin: binary %v, %d hunks", bin.GetBinary(), len(bin.GetHunks()))
	}
	if ignored := diff("W1", "build.log").GetFile(); ignored.GetTotalLines() != 0 {
		t.Errorf("an ignored file shows %d lines", ignored.GetTotalLines())
	}
	// The task's committed work stays in its diff: done.go is not in the worktree's changes.
	if committed := diff("W1", "done.go").GetFile(); committed.GetTotalLines() != 0 {
		t.Errorf("done.go shows %d lines of committed work", committed.GetTotalLines())
	}

	// A clean worktree has nothing to show; one never made, or removed, says so.
	for _, args := range [][]string{{"reset", "--hard", "--quiet"}, {"clean", "-fd", "--quiet"}} {
		if _, err := git(ctx, wt, args...); err != nil {
			t.Fatal(err)
		}
	}
	if clean := diff("W1", ""); clean.GetNoWorktree() || len(clean.GetFiles()) != 0 {
		t.Errorf("clean worktree: no_worktree %v, %d files", clean.GetNoWorktree(), len(clean.GetFiles()))
	}
	if none := diff("W2", ""); !none.GetNoWorktree() || len(none.GetFiles()) != 0 {
		t.Errorf("no worktree: no_worktree %v, %d files", none.GetNoWorktree(), len(none.GetFiles()))
	}
	if _, err := git(ctx, repo, "worktree", "remove", "--force", wt); err != nil {
		t.Fatal(err)
	}
	if gone := diff("W1", ""); !gone.GetNoWorktree() {
		t.Error("removed worktree: no_worktree = false")
	}
	if gone := diff("W1", "main.go"); !gone.GetNoWorktree() || gone.GetFile() != nil {
		t.Errorf("removed worktree, one file: no_worktree %v, file %v", gone.GetNoWorktree(), gone.GetFile())
	}
}
