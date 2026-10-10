package harness

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
)

// fakeGlab is a glab for the tests: it records each call's first words in $GLAB_DATA/calls, then answers mr view
// from the JSON of $GLAB_DATA/mr-<n>.json (n is the number of the call, the last file before it when there is none)
// and ci get from $GLAB_DATA/pipeline-<id>.json, through its --jq expression as glab 1.100 does (strings raw).
const fakeGlab = `#!/bin/sh
printf '%s %s %s %s\n' "$1" "$2" "$3" "$4" >> "$GLAB_DATA/calls"
command="$1 $2"
id=""
expr=""
while [ $# -gt 0 ]; do
	case $1 in
	--jq) expr=$2; shift ;;
	--pipeline-id) id=$2; shift ;;
	esac
	shift
done
case $command in
"mr view")
	n=$(grep -c '^mr view' "$GLAB_DATA/calls")
	while [ "$n" -gt 1 ] && [ ! -f "$GLAB_DATA/mr-$n.json" ]; do n=$((n - 1)); done
	file=$GLAB_DATA/mr-$n.json
	;;
"ci get") file=$GLAB_DATA/pipeline-$id.json ;;
*)
	echo "fake glab: no $command" >&2
	exit 1
	;;
esac
exec jq -r "$expr" "$file"
`

// TestBabysitMRReadsOnly: Djinn's babysit-mr template makes a wish from a GitLab merge request link, and its
// watcher, run as Djinn runs it under the repository's permissions, prints a paragraph on each change of the merge
// request (the same look twice prints nothing) and its done line on the merge, then exits. A fake glab on the PATH
// serves the merge request: the watcher only reads it, and the project's folder is as it was.
func TestBabysitMRReadsOnly(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the watcher is a POSIX shell script: on Windows the sh of Git for Windows runs it")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("the fake glab filters with jq")
	}
	root := filepath.Join("..", "..")
	own := filepath.Join(root, ".agents", "skills", "babysit-mr")
	tmpl, err := plan.ReadTemplate(own)
	if err != nil || tmpl == nil || tmpl.DoneWhen != "MERGED" {
		t.Fatalf("babysit-mr: %+v, %v", tmpl, err)
	}
	if title, _, ok := tmpl.Fill("babysit https://github.com/acme/lamp/pull/12"); ok {
		t.Errorf("a GitHub pull request made %q", title)
	}
	title, watch, ok := tmpl.Fill("babysit https://gitlab.example.com/acme/gong/-/merge_requests/12 please")
	if !ok || title != "Babysit !12" || watch != "sh .agents/skills/babysit-mr/watch.sh 12" {
		t.Fatalf("filled: %q, %q, %v", title, watch, ok)
	}
	perms, err := LoadPermissions(root)
	if err != nil {
		t.Fatal(err)
	}

	// The project: the skill as Djinn's repository holds it.
	dir := t.TempDir()
	skill := filepath.Join(dir, ".agents", "skills", "babysit-mr")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{plan.SkillFile, "watch.sh"} {
		b, err := os.ReadFile(filepath.Join(own, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skill, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before := files(t, dir)

	bin, data := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "glab"), []byte(fakeGlab), 0o755); err != nil {
		t.Fatal(err)
	}
	note := func(user, body, at string, system bool) string {
		return fmt.Sprintf(`{"author":{"username":%q},"body":%q,"created_at":%q,"system":%v}`, user, body, at, system)
	}
	review := note("a-reviewer", "Could this name say what it holds?\nIt reads as a verb.", "2026-10-09T10:00:00Z", false)
	for name, body := range map[string]string{
		// A failed pipeline, a system note and a comment; the second look sees the same.
		"mr-1.json": `{"iid":12,"state":"opened","detailed_merge_status":"ci_must_pass","head_pipeline":{"id":7,"status":"failed"},
			"Discussions":[{"notes":[` + note("bob", "added 1 commit", "2026-10-09T09:00:00Z", true) + `]},{"notes":[` + review + `]}]}`,
		// A new pipeline passed, and an answer in the thread.
		"mr-3.json": `{"iid":12,"state":"opened","detailed_merge_status":"mergeable","head_pipeline":{"id":8,"status":"success"},
			"Discussions":[{"notes":[` + review + `,` + note("alice", "Renamed.", "2026-10-09T11:00:00Z", false) + `]}]}`,
		"mr-4.json": `{"iid":12,"state":"merged","detailed_merge_status":"not_open","head_pipeline":{"id":8,"status":"success"}}`,
		"pipeline-7.json": `{"id":7,"status":"failed","jobs":[{"name":"lint","status":"failed","allow_failure":false},
			{"name":"test","status":"success"},{"name":"build","status":"success"},
			{"name":"flaky","status":"failed","allow_failure":true}]}`,
		"pipeline-8.json": `{"id":8,"status":"success","jobs":[{"name":"lint","status":"success"},{"name":"test","status":"success"}]}`,
	} {
		if err := os.WriteFile(filepath.Join(data, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The command's own environment, as a worker's: the test's stays as it is, and the other tests run beside it.
	env := []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "GLAB_DATA=" + data}

	// One look every 150 ms instead of every minute: the script's second argument, still allowed.
	w, err := Watch{Quiet: 50 * time.Millisecond}.Start(t.Context(), Spec{Dir: dir, Prompt: watch + " 0.15", Permissions: perms, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	events, res := watchEvents(t, w)
	if res.ExitCode != 0 || res.Err != nil {
		t.Fatalf("result = %+v", res)
	}
	var got []string
	for _, ev := range events {
		if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_TEXT {
			got = append(got, ev.Text)
		}
	}
	want := []string{
		"MR !12 · pipeline: 1 failed, 2 success, 1 warning (failed: lint) · merge status: ci_must_pass · comments: 1\n" +
			"latest comment, by a-reviewer: Could this name say what it holds?",
		"MR !12 · pipeline: 2 success · merge status: mergeable · comments: 2\nlatest comment, by alice: Renamed.",
		"MERGED: MR !12 is merged.",
	}
	if !slices.Equal(got, want) {
		t.Errorf("paragraphs:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if line := plan.DoneLine(&planv1.WishTemplate{DoneWhen: tmpl.DoneWhen}, got[len(got)-1]); line == "" {
		t.Errorf("the last paragraph %q is no done line", got[len(got)-1])
	}

	b, err := os.ReadFile(filepath.Join(data, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Split(strings.TrimSpace(string(b)), "\n")
	if want := []string{
		"mr view 12 --comments", "ci get --pipeline-id 7", "mr view 12 --comments", "ci get --pipeline-id 7",
		"mr view 12 --comments", "ci get --pipeline-id 8", "mr view 12 --comments",
	}; !slices.Equal(calls, want) {
		t.Errorf("glab calls:\n%s\nwant only reads:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
	if after := files(t, dir); !maps.Equal(after, before) {
		t.Errorf("the project's folder changed: %v", after)
	}
}
