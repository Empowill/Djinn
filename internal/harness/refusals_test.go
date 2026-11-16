package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// endedEvents are the events of kind ended the lead got about code.
func endedEvents(q *leadQueue, code string) []string {
	var lines []string
	for _, e := range q.all() {
		if e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED && e.GetCode() == code {
			lines = append(lines, e.GetText())
		}
	}
	return lines
}

// twoProjects makes a wish of two projects (one native folder, one Git repository), as the lead makes one for a study
// in another project, and returns it with the second project.
func twoProjects(t *testing.T, e *env) (wishID, projectID string) {
	t.Helper()
	var ids []string
	for _, dir := range []string{nativeFolder(t), gitRepo(t)} {
		p, err := e.project.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.Msg.GetProject().GetId())
	}
	w, err := e.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Dew", ProjectIds: ids}))
	if err != nil {
		t.Fatal(err)
	}
	return w.Msg.GetWish().GetId(), ids[1]
}

// TestDoneWithoutCommitAndRefusalsTellTheLead replays W1 of wish Dew (12/10/2026) and the lead's done notifications:
//   - a worker in a Git project that commits nothing and ends done tells the lead once that its work brought nothing to
//     merge (W196);
//   - a worker whose calls were refused, djinn's own included, tells the lead how many and which, and the command that
//     grants them, whole in the digest;
//   - the lead's event carries the worker's final note whole, apart from its line, which keeps the first line (16/10/2026).
func TestDoneWithoutCommitAndRefusalsTellTheLead(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir())
	queue := &leadQueue{}
	e.h.NotifyLead(queue.notify)
	wishID, gitID := twoProjects(t, e)
	wish, err := store.Get[*planv1.Wish](t.Context(), e.db, wishID)
	if err != nil {
		t.Fatal(err)
	}
	nativeID := wish.GetProjectIds()[0]

	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, ProjectId: gitID, Title: "Silae for bardahl: feasibility study",
		Prompt:   "text reading the scraper\ntext Feasible without the salary data: two questions for the developer.\nThe rest of the report.",
		Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	task := res.Msg.GetTask()
	e.watch(t.Context(), t, task.GetId(), 0)
	got, err := store.Get[*planv1.Task](t.Context(), e.db, task.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetAccess() != planv1.TaskAccess_TASK_ACCESS_NATIVE {
		t.Fatalf("task %v, access %v: want done, native", got.GetStatus(), got.GetAccess())
	}
	want := []string{"Djinn: " + task.GetCode() + " done, nothing to merge: no commit of its own. Its final note: The rest of the report."}
	if lines := endedEvents(queue, task.GetCode()); !slices.Equal(lines, want) {
		t.Errorf("ended events %q, want %q", lines, want)
	}
	if got.GetRefused() != nil {
		t.Errorf("refused %v, want none", got.GetRefused())
	}

	prompt := strings.Join([]string{
		"refuse Bash cd cmd/integreat/scrap/silae && ls -la && wc -l *.go",
		"refuse Bash R=/home/x/worktrees/w1; git -C $R log --oneline -15",
		"refuse Bash ls -la /home/nyhu/dolmen_sensitive/secrets/",
		"refuse Bash SILAE_CHECK=1 SILAE_CLIENT=bardahl task test-pkg PKG=./cmd/integreat/scrap/silae/...",
		"refuse Bash gcloud config get-value project",
		"refuse Bash djinn block put --help",
		"text Half of the study: djinn was refused, the questions are below.",
	}, "\n")
	res2, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, ProjectId: nativeID, Title: "Silae study", Prompt: prompt, Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	task2 := res2.Msg.GetTask()
	e.watch(t.Context(), t, task2.GetId(), 0)
	got2, err := store.Get[*planv1.Task](t.Context(), e.db, task2.GetId())
	if err != nil {
		t.Fatal(err)
	}
	r := got2.GetRefused()
	if wantNames := []string{"djinn", "ls", "git", "task", "gcloud"}; r.GetCount() != 6 || !slices.Equal(r.GetNames(), wantNames) {
		t.Errorf("refused %v, want 6 calls of %v", r, wantNames)
	}
	lines := endedEvents(queue, task2.GetCode())
	if len(lines) != 1 {
		t.Fatalf("ended events %q, want one", lines)
	}
	command := "djinn wish allow " + wishID + " --mode auto --project-id " + nativeID
	wantLine := "Djinn: " + task2.GetCode() + " done, 6 commands refused (permission); " + command +
		" would let the agent judge them, a right only the developer grants. Its final note: Half of the study: djinn was refused, the questions are below."
	if lines[0] != wantLine {
		t.Errorf("ended event\n got %s\nwant %s", lines[0], wantLine)
	}
	digest := plan.DigestLine(&planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, Code: task2.GetCode(), Text: lines[0]}, "")
	if !strings.Contains(digest, command) {
		t.Errorf("the digest cuts the command: %s", digest)
	}

	note := "Added djinn question get.\n\nLeft to verify:\n- the Windows build"
	e.h.Note(task2.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, Text: note + "\n"})
	e.h.tellDone(t.Context(), task2, "", "")
	events := queue.all()
	last := events[len(events)-1]
	if last.GetKind() != planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED || last.GetNote() != note {
		t.Errorf("event %v, want its note whole: %q", last, note)
	}
	if want := "Djinn: " + task2.GetCode() + " done: Added djinn question get."; last.GetText() != want {
		t.Errorf("line %q, want %q", last.GetText(), want)
	}
}

// TestDoneLine: the names refused come as many as the digest keeps with the whole command, djinn first; paths an agy
// project refused propose no mode, which would not grant them (W360).
func TestDoneLine(t *testing.T) {
	t.Parallel()
	wishID, projectID := store.NewID(), store.NewID()
	task := &planv1.Task{Code: "W1", WishId: wishID, ProjectId: projectID,
		Refused: &planv1.TaskRefusals{Count: 6, Names: []string{"djinn", "ls", "git", "task", "gcloud"}}}
	one := &planv1.Wish{Id: wishID, ProjectIds: []string{projectID}}
	for _, c := range []struct {
		task       *planv1.Task
		note, why  string
		want       string
		wantDigest bool
	}{
		{task: task, note: "Feasible.", wantDigest: true, want: "Djinn: W1 done, 6 commands refused (permission): djinn, ls, git, …; djinn wish allow " +
			wishID + " --mode auto would let the agent judge them, a right only the developer grants. Its final note: Feasible."},
		{task: &planv1.Task{Code: "W2", WishId: wishID, Refused: &planv1.TaskRefusals{Count: 1, Names: []string{"gcloud"}}},
			wantDigest: true, want: "Djinn: W2 done, 1 command refused (permission): gcloud; djinn wish allow " + wishID +
				" --mode auto would let the agent judge them, a right only the developer grants. It left no final note."},
		{task: &planv1.Task{Code: "W3"}, note: "Done.", why: "nothing to merge into djinn/dew",
			want: "Djinn: W3 done, nothing to merge into djinn/dew. Its final note: Done."},
		{task: &planv1.Task{Code: "W4"}, want: "Djinn: W4 done. It left no final note."},
		{task: &planv1.Task{Code: "W5", WishId: wishID, Refused: &planv1.TaskRefusals{Count: 2, Names: []string{"read_file"}}},
			note: "Read the tilasm with djinn tilasm get.", want: "Djinn: W5 done, 2 file accesses refused (permission): " +
				"read_file, paths outside the folders Djinn opens to the worker: no mode grants them. Its final note: Read the tilasm with djinn tilasm get."},
		{task: &planv1.Task{Code: "W6", WishId: wishID, Refused: &planv1.TaskRefusals{Count: 2, Names: []string{"read_file", "gcloud"}}},
			want: "Djinn: W6 done, 2 commands refused (permission): read_file, gcloud; djinn wish allow " + wishID +
				" --mode auto would let the agent judge them, a right only the developer grants. It left no final note."},
	} {
		got := doneLine(c.task, one, c.note, c.why, "")
		if got != c.want {
			t.Errorf("doneLine\n got %s\nwant %s", got, c.want)
		}
		digest := plan.DigestLine(&planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, Code: c.task.GetCode(), Text: got}, "")
		if c.wantDigest && !strings.Contains(digest, "djinn wish allow "+wishID+" --mode auto") {
			t.Errorf("the digest cuts the command: %s", digest)
		}
	}
}

func TestRefusedName(t *testing.T) {
	t.Parallel()
	for what, want := range map[string]string{
		`Bash {"command":"cd cmd/silae && ls -la && wc -l *.go","description":"List"}`: "ls",
		`Bash {"command":"R=/x/w1; git -C $R log --oneline"}`:                          "git",
		`Bash {"command":"SILAE_CHECK=1 SILAE_CLIENT=bardahl task test-pkg"}`:          "task",
		`Bash {"command":"/usr/bin/gcloud config get-value project"}`:                  "gcloud",
		`Bash {"command":"djinn block put --help"}`:                                    "djinn",
		`Bash {"command":"(cd app && npm test) | tail"}`:                               "npm",
		`Edit {"file_path":"/x/a.go"}`:                                                 "Edit",
		`Write notes.md`:                                                               "Write",
		`commandExecution gcloud auth list (network)`:                                  "gcloud",
		`read_file /home/u/.config/djinn/tilasms/01a1/v5/index.html`:                   "read_file",
	} {
		if got := refusedName(what); got != want {
			t.Errorf("refusedName(%s) = %q, want %q", what, got, want)
		}
	}
}

// TestSpawnNoteSaysDjinnIsRefused: a claude worker started with its own settings, in a project whose settings allow
// no djinn command, cannot report: the spawn says so, and how to grant it. Settings that allow djinn say nothing.
func TestSpawnNoteSaysDjinnIsRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	e := up(t, t.TempDir())
	dir := nativeFolder(t)
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	wishID, projectID := e.wish(t, dir)
	project, err := store.Get[*planv1.Project](t.Context(), e.db, projectID)
	if err != nil {
		t.Fatal(err)
	}
	task := &planv1.Task{Code: "W1", WishId: wishID, ProjectId: projectID, Provider: planv1.Provider_PROVIDER_CLAUDE}
	note := e.h.spawnNote(t.Context(), task)
	if !strings.Contains(note, "W1 runs with claude's own settings") ||
		!strings.Contains(note, "djinn wish allow "+wishID+" --mode auto") {
		t.Errorf("note %q", note)
	}
	settings := filepath.Join(project.GetDirectory(), ".claude", "settings.json")
	if err := os.WriteFile(settings, []byte(`{"permissions":{"allow":["Bash(git:*)","Bash(djinn:*)"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if note := e.h.spawnNote(t.Context(), task); note != "" {
		t.Errorf("note %q with djinn allowed", note)
	}
	task.Provider = planv1.Provider_PROVIDER_FAKE
	if err := os.Remove(settings); err != nil {
		t.Fatal(err)
	}
	if note := e.h.spawnNote(t.Context(), task); note != "" {
		t.Errorf("note %q for an agent whose configuration Djinn cannot read", note)
	}
}

func TestClaudeAllowsDjinn(t *testing.T) {
	t.Parallel()
	for settings, want := range map[string]bool{
		`{"permissions":{"allow":["Bash(git:*)","Bash(ls:*)"]}}`: false,
		`{"permissions":{"allow":["Bash(djinn:*)"]}}`:            true,
		`{"permissions":{"allow":["Bash(djinn block put *)"]}}`:  true,
		`{"permissions":{"allow":["Bash"]}}`:                     true,
		`{"permissions":{"defaultMode":"auto"}}`:                 true,
		`{"permissions":{"defaultMode":"acceptEdits"}}`:          false,
		`not json`: false,
	} {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := claudeAllowsDjinn(dir, ""); got != want {
			t.Errorf("claudeAllowsDjinn(%s) = %v, want %v", settings, got, want)
		}
		if got := claudeAllowsDjinn(t.TempDir(), dir); got != want {
			t.Errorf("claudeAllowsDjinn(home %s) = %v, want %v", settings, got, want)
		}
	}
}
