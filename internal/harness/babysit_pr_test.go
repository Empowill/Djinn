package harness

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// fakeGh is a gh for the tests: it records each call's first words in $GH_DATA/calls, and answers for the look it is
// in, the number of pr view calls so far (n): it fails when $GH_DATA/fail-<n> exists, else answers pr view from
// $GH_DATA/view-<n>.json, pr checks from $GH_DATA/checks-<n>.json and api graphql (the comments) from
// $GH_DATA/notes-<n>.json (the last file before n when there is none; nothing when there is no file at all), through its --jq expression as gh does (strings raw). A jq takes tens of milliseconds to start: on a command's first
// call, one jq filters all of its files, each output line after its file's name, and the answers are kept.
const fakeGh = `#!/bin/sh
printf '%s %s %s\n' "$1" "$2" "$3" >> "$GH_DATA/calls"
command="$1 $2"
expr=""
while [ $# -gt 0 ]; do
	[ "$1" = --jq ] && { expr=$2; shift; }
	shift
done
n=$(grep -c '^pr view' "$GH_DATA/calls")
case $command in
"pr view")
	if [ -f "$GH_DATA/fail-$n" ]; then
		echo "error connecting to api.github.com" >&2
		exit 1
	fi
	name=view
	;;
"pr checks") name=checks ;;
"api graphql") name=notes ;;
*)
	echo "fake gh: no $command" >&2
	exit 1
	;;
esac
while [ "$n" -gt 1 ] && [ ! -f "$GH_DATA/$name-$n.json" ]; do n=$((n - 1)); done
answers=$GH_DATA/answers-$name
if [ ! -d "$answers" ]; then
	mkdir "$answers"
	ls "$GH_DATA/$name"-*.json >/dev/null 2>&1 || exit 0
	jq -r "($expr) | \"\(input_filename)\t\(.)\"" "$GH_DATA/$name"-*.json |
		awk -F '\t' -v dir="$answers" '{ n = split($1, path, "/"); print substr($0, length($1) + 2) > (dir "/" path[n]) }'
fi
touch "$answers/$name-$n.json"
exec cat "$answers/$name-$n.json"
`

func TestWatch_BabysitPR(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the watcher is a POSIX shell script, for Linux and macOS")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("the fake gh filters with jq")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", ".agents", "skills", "babysit-pr", "watch.sh"))
	if err != nil {
		t.Fatal(err)
	}

	// Djinn's babysit-pr watcher prints a paragraph only for what needs the lead, each once, even across restarts: a
	// check that newly fails (again after a new push), all checks passing after a push, each new comment (of the
	// conversation, a review, on a line of the diff) in a paragraph of its own, changes requested, a conflict, a failed
	// look, the merge. Pending checks and partial passes print nothing. A restart says the checks again on its first
	// look, which Djinn tells no lead when its task said it last; never a comment again.
	t.Run("speaks only for the lead", func(t *testing.T) {
		t.Parallel()
		dir, bin, data, home := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGh), 0o755); err != nil {
			t.Fatal(err)
		}

		view := func(state, mergeable, decision, head string) string {
			return fmt.Sprintf(`{"state":%q,"mergeable":%q,"reviewDecision":%q,"headRefOid":%q}`, state, mergeable, decision, head)
		}
		// notes answers the GraphQL query for the comments: those of the conversation and the reviews, as GitHub
		// gives them, oldest first.
		notes := func(notes ...string) string {
			var comments, reviews []string
			for _, n := range notes {
				if strings.Contains(n, `"submittedAt"`) {
					reviews = append(reviews, n)
				} else {
					comments = append(comments, n)
				}
			}
			return fmt.Sprintf(`{"data":{"repository":{"pullRequest":{"comments":{"nodes":[%s]},"reviews":{"nodes":[%s]}}}}}`,
				strings.Join(comments, ","), strings.Join(reviews, ","))
		}
		const pull = "https://github.com/o/r/pull/12#"
		note := func(anchor, login, at, body string) string {
			return fmt.Sprintf(`{"url":%q,"author":{"login":%q},"createdAt":%q,"body":%q}`, pull+anchor, login, at, body)
		}
		review := func(anchor, login, state, at, body string, lines ...string) string {
			return fmt.Sprintf(`{"url":%q,"author":{"login":%q},"state":%q,"submittedAt":%q,"body":%q,"comments":{"nodes":[%s]}}`,
				pull+anchor, login, state, at, body, strings.Join(lines, ","))
		}
		line := func(anchor, login, path string, line int, body string) string {
			l := "null"
			if line > 0 {
				l = strconv.Itoa(line)
			}
			return fmt.Sprintf(`{"url":%q,"author":{"login":%q},"path":%q,"line":%s,"originalLine":7,"body":%q}`,
				pull+anchor, login, path, l, body)
		}
		checks := func(buckets ...string) string {
			var out []string
			for i, name := range []string{"lint", "test", "build"} {
				out = append(out, fmt.Sprintf(`{"name":%q,"bucket":%q}`, name, buckets[i]))
			}
			return "[" + strings.Join(out, ",") + "]"
		}
		early := note("issuecomment-1", "dave", "2026-10-09T08:00:00Z", "Thanks!")
		approval := review("pullrequestreview-2", "bob", "APPROVED", "2026-10-09T09:00:00Z", "")
		comment := note("issuecomment-3", "a-reviewer", "2026-10-09T10:00:00Z", "\r\nCould this name say what it holds?\r\nIt reads as a verb.")
		changes := review("pullrequestreview-4", "carol", "CHANGES_REQUESTED", "2026-10-09T11:00:00Z", "Please split this function.",
			line("discussion_r5", "carol", "watch.sh", 12, "```suggestion\nheld := names\n```"))
		pending := review("pullrequestreview-6", "carol", "PENDING", "", "", line("discussion_r7", "carol", "watch.sh", 3, "Draft."))
		// PR #7, 20/10/2026: a comment on a line of the diff, alone in a review without a body, was never said.
		onLine := review("pullrequestreview-8", "dave", "COMMENTED", "2026-10-09T12:00:00Z", "",
			line("discussion_r9", "dave", "lead/notes/rsi-loop.md", 0, "This is too specific to another project."))
		again := note("issuecomment-10", "a-reviewer", "2026-10-09T12:30:00Z", "Could this name say what it holds?")

		// Each look's answers; a look without a file reads the last one before it.
		for name, body := range map[string]string{
			"view-1.json":   view("OPEN", "MERGEABLE", "REVIEW_REQUIRED", "a1"),
			"notes-1.json":  notes(early, approval),
			"checks-1.json": checks("pending", "pending", "pending"),
			"checks-2.json": checks("pass", "pending", "pending"),
			"checks-3.json": checks("fail", "pass", "pending"),
			// 4, and 5 after a restart: the same failure.
			"view-6.json":   view("OPEN", "MERGEABLE", "REVIEW_REQUIRED", "b2"), // a new push
			"checks-6.json": checks("pending", "pending", "pending"),
			"checks-7.json": checks("fail", "pending", "pass"), // the same check fails on it
			"view-8.json":   view("OPEN", "MERGEABLE", "REVIEW_REQUIRED", "c3"),
			"checks-8.json": checks("pass", "pending", "pass"),
			"checks-9.json": checks("pass", "pass", "pass"),
			// 10: the same.
			"notes-11.json": notes(early, approval, comment),
			"view-12.json":  view("OPEN", "MERGEABLE", "CHANGES_REQUESTED", "c3"),
			"notes-12.json": notes(early, approval, comment, changes, pending),
			"view-13.json":  view("OPEN", "UNKNOWN", "CHANGES_REQUESTED", "c3"),
			"notes-13.json": notes(early, approval, comment, changes, pending, onLine, again),
			"view-14.json":  view("OPEN", "CONFLICTING", "CHANGES_REQUESTED", "c3"),
			"fail-15":       "",
			"fail-16":       "",
			"fail-17":       "", // after a restart
			// 18: as 14.
			"view-19.json": view("MERGED", "UNKNOWN", "CHANGES_REQUESTED", "c3"),
		} {
			if err := os.WriteFile(filepath.Join(data, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		before := files(t, dir)

		// run starts the watcher as Djinn does, for so many looks (0: until it exits), and gives its paragraphs.
		run := func(looks int) []string {
			t.Helper()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "sh", script, "12", "0", strconv.Itoa(looks))
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "GH_DATA="+data,
				"DJINN_HOME="+home, "DJINN_TASK_ID=w1")
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("watch.sh: %v\n%s", err, stderr.String())
			}
			var paragraphs []string
			for p := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n\n") {
				if p != "" {
					paragraphs = append(paragraphs, p)
				}
			}
			return paragraphs
		}

		for i, step := range []struct {
			looks int
			want  []string
		}{
			// Its first look says the latest comment only.
			{4, []string{"PR #12 · approved by bob: " + pull + "pullrequestreview-2", "PR #12 · checks failed at a1: lint"}},
			// The restart says the checks again on look 5; look 7 says the new push's failure.
			{4, []string{"PR #12 · checks failed at a1: lint", "PR #12 · checks failed at b2: lint"}},
			// Each new comment once, in a paragraph of its own; a pending review's never.
			{5, []string{
				"PR #12 · all checks pass at c3 (3)",
				"PR #12 · new comment by a-reviewer: " + pull + "issuecomment-3\n> Could this name say what it holds?",
				"PR #12 · changes requested",
				"PR #12 · changes requested by carol: " + pull + "pullrequestreview-4\n> Please split this function.",
				"PR #12 · new comment by carol on watch.sh:12: " + pull + "discussion_r5\n> held := names",
				"PR #12 · new comment by dave on lead/notes/rsi-loop.md:7: " + pull + "discussion_r9\n> This is too specific to another project.",
				"PR #12 · new comment by a-reviewer: " + pull + "issuecomment-10\n> Could this name say what it holds?",
			}},
			{3, []string{
				"PR #12 · all checks pass at c3 (3) · mergeable: CONFLICTING",
				"PR #12: gh failed, trying again: error connecting to api.github.com",
			}},
			// Look 17 fails, said already; 18, the first that reads the pull request, says the checks again.
			{0, []string{"PR #12 · all checks pass at c3 (3)", "MERGED: PR #12 is merged."}},
		} {
			if got := run(step.looks); !slices.Equal(got, step.want) {
				t.Errorf("run %d, paragraphs:\n%s\nwant:\n%s", i+1, strings.Join(got, "\n--\n"), strings.Join(step.want, "\n--\n"))
			}
		}

		b, err := os.ReadFile(filepath.Join(data, "calls"))
		if err != nil {
			t.Fatal(err)
		}
		for call := range strings.Lines(string(b)) {
			if call != "pr view 12\n" && call != "pr checks 12\n" && call != "api graphql -F\n" {
				t.Errorf("gh %q: the watcher only reads", call)
			}
		}
		if after := files(t, dir); !maps.Equal(after, before) {
			t.Errorf("the project's folder changed: %v", after)
		}
		if _, err := os.Stat(filepath.Join(home, "watchers", "w1", "babysit-pr-12")); err != nil {
			t.Errorf("no state under DJINN_HOME: %v", err)
		}
	})

	// PR #2, 12/10/2026: the watcher said "checks failed: Lint and test (Linux)" at 9d62c3ad; the next push, afe2ff14,
	// failed the same check, and the lead was never told. Run as Djinn runs it, each new failure now wakes the lead
	// once, a restart once again only when the task did not record it last, and a re-run's failure too.
	t.Run("new push failing the same check", func(t *testing.T) {
		t.Parallel()
		dir, bin, data, home := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGh), 0o755); err != nil {
			t.Fatal(err)
		}
		view := func(head string) string {
			return fmt.Sprintf(`{"state":"OPEN","mergeable":"MERGEABLE","reviewDecision":"","headRefOid":%q,"comments":[],"reviews":[]}`, head)
		}
		check := func(bucket string, job int) string {
			return fmt.Sprintf(`[{"name":"Lint and test (Linux)","bucket":%q,"link":"https://github.com/o/r/actions/runs/%d/job/%d"},`+
				`{"name":"Tests (Windows)","bucket":"pass","link":""}]`, bucket, job, job)
		}
		const a, b, c = "9d62c3ad11", "afe2ff1422", "c0ffee3333"
		for name, body := range map[string]string{
			"view-1.json": view(a), "checks-1.json": check("fail", 1),
			"view-2.json": view(b), "checks-2.json": check("pending", 2),
			"checks-3.json": check("fail", 2), // the same check fails on the new push
			"view-4.json":   view(c), "checks-4.json": check("pending", 3),
			"checks-5.json": check("fail", 3),    // and again, as Djinn hangs
			"checks-7.json": check("pending", 4), // a re-run
			"checks-8.json": check("fail", 4),
		} {
			if err := os.WriteFile(filepath.Join(data, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		env := []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "GH_DATA=" + data,
			"DJINN_HOME=" + home, "DJINN_TASK_ID=w488"}
		command := func(looks int) string { return fmt.Sprintf("sh '%s' 2 0.1 %d", script, looks) }

		// watcher runs the script for so many looks as Djinn's watcher of a task whose last line is last, and gives each
		// paragraph with whether it woke the lead.
		watcher := func(looks int, last string) []string {
			t.Helper()
			w, err := Watch{Quiet: 30 * time.Millisecond}.Start(t.Context(), Spec{Dir: dir, Prompt: command(looks), Env: env, LastLine: last})
			if err != nil {
				t.Fatal(err)
			}
			events, res := watchEvents(t, w)
			if res.ExitCode != 0 || res.Err != nil {
				t.Fatalf("watch.sh: %+v", res)
			}
			var got []string
			for _, ev := range events {
				if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_TEXT {
					got = append(got, fmt.Sprintf("%s wake=%v", ev.Text, ev.Watched.Wake))
				}
			}
			return got
		}
		failed := func(head string, job int) string {
			return fmt.Sprintf("PR #2 · checks failed at %.7s: Lint and test (Linux)\nLint and test (Linux): https://github.com/o/r/actions/runs/%d/job/%d", head, job, job)
		}
		firstLine := func(s string) string { first, _, _ := strings.Cut(s, "\n"); return first }

		for i, step := range []struct {
			looks int
			last  string
			want  []string
		}{
			// Looks 1 to 3: A fails, B is pushed, the same check fails on it.
			{3, "", []string{failed(a, 1) + " wake=true", failed(b, 2) + " wake=true"}},
			// Looks 4 and 5 printed to a Djinn that hung, then was killed: the next one says the checks again.
			{0, "", nil},
			// Looks 6 to 8: that one, then the re-run's failure.
			{3, firstLine(failed(b, 2)), []string{failed(c, 3) + " wake=true", failed(c, 4) + " wake=true"}},
			// Look 9, after another restart: what the task said last.
			{1, firstLine(failed(c, 4)), []string{failed(c, 4) + " wake=false"}},
		} {
			var got []string
			if step.looks == 0 {
				cmd := exec.CommandContext(t.Context(), "sh", script, "2", "0", "2")
				cmd.Dir, cmd.Env = dir, append(os.Environ(), env...)
				if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), failed(c, 3)) {
					t.Fatalf("the paragraph lost with Djinn: %v\n%s", err, out)
				}
			} else {
				got = watcher(step.looks, step.last)
			}
			if !slices.Equal(got, step.want) {
				t.Errorf("step %d, paragraphs:\n%s\nwant:\n%s", i+1, strings.Join(got, "\n--\n"), strings.Join(step.want, "\n--\n"))
			}
		}
	})

	// The watcher speaks of the pull requests it is given, a comma-separated list, and exits once all are merged; never
	// of another open one, though its branch is an azima's (djinn/...): that one has its own watcher (W8: watch.sh 6
	// spoke of PR #7).
	t.Run("watches only the pull requests it is given", func(t *testing.T) {
		t.Parallel()
		const multiPRGh = `#!/bin/sh
set -e
cmd="$1 $2"
arg3="${3:-}"
expr=""
while [ $# -gt 0 ]; do
	case $1 in
	--jq) expr=$2; shift ;;
	esac
	shift
done
case $cmd in
"pr view") file="$GH_DATA/view-$arg3.json" ;;
"pr checks") file="$GH_DATA/checks-$arg3.json" ;;
"pr list") file="$GH_DATA/list.json" ;;
"api graphql") exit 0 ;;
*) exit 1 ;;
esac
exec jq -r "$expr" "$file"
`
		repoDir := gitRepo(t)
		binDir, dataDir, homeDir := t.TempDir(), t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(multiPRGh), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := git(t.Context(), repoDir, "branch", "djinn/T1-one"); err != nil {
			t.Fatal(err)
		}
		if _, err := git(t.Context(), repoDir, "branch", "djinn/T2-two"); err != nil {
			t.Fatal(err)
		}

		for name, body := range map[string]string{
			"list.json":       `[{"number":101,"headRefName":"djinn/T1-one"},{"number":102,"headRefName":"djinn/T2-two"},{"number":103,"headRefName":"djinn/T3-three"}]`,
			"view-103.json":   `{"state":"OPEN","mergeable":"MERGEABLE","reviewDecision":"","headRefOid":"c3","comments":[],"reviews":[]}`,
			"checks-103.json": `[{"name":"lint","bucket":"pass"}]`,
			"view-101.json":   `{"state":"OPEN","mergeable":"MERGEABLE","reviewDecision":"","headRefOid":"a1","comments":[],"reviews":[]}`,
			"view-102.json":   `{"state":"OPEN","mergeable":"MERGEABLE","reviewDecision":"","headRefOid":"b2","comments":[],"reviews":[]}`,
			"checks-101.json": `[{"name":"lint","bucket":"fail"},{"name":"test","bucket":"pass"}]`,
			"checks-102.json": `[{"name":"lint","bucket":"pass"},{"name":"test","bucket":"pass"}]`,
		} {
			if err := os.WriteFile(filepath.Join(dataDir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		env := append(os.Environ(),
			"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"GH_DATA="+dataDir,
			"DJINN_HOME="+homeDir,
			"DJINN_TASK_ID=watch-azimas-task",
		)
		cmd := exec.CommandContext(t.Context(), "sh", script, "101,102", "0", "1")
		cmd.Dir, cmd.Env = repoDir, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("watch.sh failed: %s (%v)", out, err)
		}
		outStr := string(out)
		if !strings.Contains(outStr, "PR #101 · checks failed at a1: lint") {
			t.Errorf("output missing PR 101 failure: %s", outStr)
		}
		if !strings.Contains(outStr, "PR #102 · all checks pass at b2 (2)") {
			t.Errorf("output missing PR 102 pass: %s", outStr)
		}
		if strings.Contains(outStr, "PR #103") {
			t.Errorf("output speaks of PR 103, not given: %s", outStr)
		}

		// Next look: both PRs are merged.
		for name, body := range map[string]string{
			"view-101.json": `{"state":"MERGED","mergeable":"UNKNOWN","reviewDecision":"","headRefOid":"a1","comments":[],"reviews":[]}`,
			"view-102.json": `{"state":"MERGED","mergeable":"UNKNOWN","reviewDecision":"","headRefOid":"b2","comments":[],"reviews":[]}`,
		} {
			if err := os.WriteFile(filepath.Join(dataDir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		cmd2 := exec.CommandContext(t.Context(), "sh", script, "101,102", "0", "1")
		cmd2.Dir, cmd2.Env = repoDir, env
		out2, err := cmd2.CombinedOutput()
		if err != nil {
			t.Fatalf("watch.sh look 2 failed: %s (%v)", out2, err)
		}
		out2Str := string(out2)
		if !strings.Contains(out2Str, "MERGED: PR #101 is merged.") {
			t.Errorf("output missing PR 101 merged: %s", out2Str)
		}
		if !strings.Contains(out2Str, "MERGED: PR #102 is merged.") {
			t.Errorf("output missing PR 102 merged: %s", out2Str)
		}
	})
}
