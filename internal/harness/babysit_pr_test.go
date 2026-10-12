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
// $GH_DATA/view-<n>.json and pr checks from $GH_DATA/checks-<n>.json (the last file before n when there is none),
// through its --jq expression as gh does (strings raw). A jq takes tens of milliseconds to start: on a command's first
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
*)
	echo "fake gh: no $command" >&2
	exit 1
	;;
esac
while [ "$n" -gt 1 ] && [ ! -f "$GH_DATA/$name-$n.json" ]; do n=$((n - 1)); done
answers=$GH_DATA/answers-$name
if [ ! -d "$answers" ]; then
	mkdir "$answers"
	jq -r "($expr) | \"\(input_filename)\t\(.)\"" "$GH_DATA/$name"-*.json |
		awk -F '\t' -v dir="$answers" '{ n = split($1, path, "/"); print substr($0, length($1) + 2) > (dir "/" path[n]) }'
fi
touch "$answers/$name-$n.json"
exec cat "$answers/$name-$n.json"
`

// TestBabysitPRSpeaksOnlyForTheLead: Djinn's babysit-pr watcher prints a paragraph only for what needs the lead,
// each once, even across restarts: a check that newly fails (again after a new push), all checks passing after a
// push, a new comment or review, changes requested, a conflict, a failed look, the merge. Pending checks and partial
// passes print nothing. A restart says the checks again on its first look, which Djinn tells no lead when its task
// said it last (TestBabysitPRNewPushFailingTheSameCheck). A fake gh on the PATH serves the pull request, one look at
// a time, without a pause.
func TestBabysitPRSpeaksOnlyForTheLead(t *testing.T) {
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
	dir, bin, data, home := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGh), 0o755); err != nil {
		t.Fatal(err)
	}

	view := func(state, mergeable, decision, head string, notes ...string) string {
		var comments, reviews []string
		for _, n := range notes {
			if strings.Contains(n, `"submittedAt"`) {
				reviews = append(reviews, n)
			} else {
				comments = append(comments, n)
			}
		}
		return fmt.Sprintf(`{"state":%q,"mergeable":%q,"reviewDecision":%q,"headRefOid":%q,"comments":[%s],"reviews":[%s]}`,
			state, mergeable, decision, head, strings.Join(comments, ","), strings.Join(reviews, ","))
	}
	checks := func(buckets ...string) string {
		var out []string
		for i, name := range []string{"lint", "test", "build"} {
			out = append(out, fmt.Sprintf(`{"name":%q,"bucket":%q}`, name, buckets[i]))
		}
		return "[" + strings.Join(out, ",") + "]"
	}
	comment := `{"author":{"login":"a-reviewer"},"body":"Could this name say what it holds?\nIt reads as a verb.","createdAt":"2026-10-09T10:00:00Z"}`
	approval := `{"author":{"login":"bob"},"body":"","state":"APPROVED","submittedAt":"2026-10-09T09:00:00Z"}`
	changes := `{"author":{"login":"carol"},"body":"Please split this function.","state":"CHANGES_REQUESTED","submittedAt":"2026-10-09T11:00:00Z"}`

	// Each look's answers; a look without a file reads the last one before it.
	for name, body := range map[string]string{
		"view-1.json":   view("OPEN", "MERGEABLE", "REVIEW_REQUIRED", "a1", approval),
		"checks-1.json": checks("pending", "pending", "pending"),
		"checks-2.json": checks("pass", "pending", "pending"),
		"checks-3.json": checks("fail", "pass", "pending"),
		// 4, and 5 after a restart: the same failure.
		"view-6.json":   view("OPEN", "MERGEABLE", "REVIEW_REQUIRED", "b2", approval), // a new push
		"checks-6.json": checks("pending", "pending", "pending"),
		"checks-7.json": checks("fail", "pending", "pass"), // the same check fails on it
		"view-8.json":   view("OPEN", "MERGEABLE", "REVIEW_REQUIRED", "c3", approval),
		"checks-8.json": checks("pass", "pending", "pass"),
		"checks-9.json": checks("pass", "pass", "pass"),
		// 10: the same.
		"view-11.json": view("OPEN", "MERGEABLE", "REVIEW_REQUIRED", "c3", approval, comment),
		"view-12.json": view("OPEN", "MERGEABLE", "CHANGES_REQUESTED", "c3", approval, comment, changes),
		"view-13.json": view("OPEN", "UNKNOWN", "CHANGES_REQUESTED", "c3", approval, comment, changes),
		"view-14.json": view("OPEN", "CONFLICTING", "CHANGES_REQUESTED", "c3", approval, comment, changes),
		"fail-15":      "",
		"fail-16":      "",
		"fail-17":      "", // after a restart
		// 18: as 14.
		"view-19.json": view("MERGED", "UNKNOWN", "CHANGES_REQUESTED", "c3", approval, comment, changes),
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
		{4, []string{"PR #12 · checks failed at a1: lint"}},
		// The restart says the checks again on look 5; look 7 says the new push's failure.
		{4, []string{"PR #12 · checks failed at a1: lint", "PR #12 · checks failed at b2: lint"}},
		{5, []string{
			"PR #12 · all checks pass at c3 (3)",
			"PR #12 · new comment by a-reviewer\nlatest comment, by a-reviewer: Could this name say what it holds?",
			"PR #12 · changes requested · new comment by carol\nlatest comment, by carol: Please split this function.",
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
		if call != "pr view 12\n" && call != "pr checks 12\n" {
			t.Errorf("gh %q: the watcher only reads", call)
		}
	}
	if after := files(t, dir); !maps.Equal(after, before) {
		t.Errorf("the project's folder changed: %v", after)
	}
	if _, err := os.Stat(filepath.Join(home, "watchers", "w1", "babysit-pr-12")); err != nil {
		t.Errorf("no state under DJINN_HOME: %v", err)
	}
}

// TestBabysitPRNewPushFailingTheSameCheck: PR #2, 12/10/2026. The watcher said "checks failed: Lint and test (Linux)"
// at 9d62c3ad; the next push, afe2ff14, failed the same check, and the lead was never told. Two causes. Djinn tells
// the lead a paragraph only when it differs from the last one, and the second said the same words. And Djinn hung
// before it recorded that paragraph, then was killed: the watcher kept it as said, and the one Djinn started again
// said nothing. Run as Djinn runs it, each new failure now wakes the lead once, a restart once again only when the
// task did not record it last, and a re-run's failure too.
func TestBabysitPRNewPushFailingTheSameCheck(t *testing.T) {
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
}
