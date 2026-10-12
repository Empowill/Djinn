package harness

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// fakeForge is a gh and a glab for the tests, without network nor jq: it answers pr view, pr list, mr view and mr
// list from its maps, records each call, and runs any other command for real.
type fakeForge struct {
	mu    sync.Mutex
	calls []string
	// views are the answers to gh pr view <n> and glab mr view <n>, by number; lists to gh pr list --head <branch>
	// and glab mr list --source-branch <branch>, by the branch's prefix.
	views map[string]string
	lists map[string]string
}

func (f *fakeForge) run(ctx context.Context, dir string, args []string) (string, int, error) {
	if len(args) < 4 || (args[0] != "gh" && args[0] != "glab") {
		return runCommand(ctx, dir, args)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(args, " "))
	switch strings.Join(args[1:3], " ") {
	case "pr view", "mr view":
		if out, ok := f.views[args[3]]; ok {
			return out, 0, nil
		}
		return "no pull request found", 1, nil
	case "pr list", "mr list":
		for prefix, out := range f.lists {
			if strings.HasPrefix(args[4], prefix) {
				return out, 0, nil
			}
		}
		return "[]", 0, nil
	}
	return "fake forge: no " + strings.Join(args[:3], " "), 1, nil
}

func (f *fakeForge) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// babysitRepo is a Git repository on the forge at remote whose skill declares babysit (empty: none), and whose
// settings say settings (empty: none). Its watcher prints its number and its state file, waits, then says MERGED
// and waits again: only its done line ends it.
func babysitRepo(t *testing.T, remote, babysit, settings string) string {
	t.Helper()
	repo := gitRepo(t)
	if _, err := git(t.Context(), repo, "remote", "add", "origin", remote); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(repo, ".agents", "skills", "babysit-it")
	if err := os.MkdirAll(skill, 0o700); err != nil {
		t.Fatal(err)
	}
	front := "---\nname: babysit-it\ndescription: Babysit a request.\n"
	if babysit != "" {
		front += "metadata:\n  djinn:\n    babysit:\n" + babysit
	}
	files := map[string]string{
		filepath.Join(skill, "SKILL.md"): front + "---\n\n# Babysit\n",
		filepath.Join(skill, "watch.sh"): "printf 'request %s, state %s\\n' \"$1\" \"$2\"\nsleep 0.2\necho 'MERGED: it is merged.'\nsleep 30\n",
	}
	if settings != "" {
		files[filepath.Join(repo, ".agents", "settings.txtpb")] = settings
	}
	for path, text := range files {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// gitlabBabysit is how dolmen's babysit-mr-empowill declares its watcher, with a shell script in place of mrwatch.
const gitlabBabysit = `      forge: gitlab
      watch: "sh .agents/skills/babysit-it/watch.sh {mr} {state}"
      restart: true
      done_when: MERGED
`

// babysitWish starts a Djinn on the fake forge, with a wish in repo, and gives its lead's queue.
func babysitWish(t *testing.T, forge *fakeForge, repo string) (*env, *leadQueue, string, string) {
	t.Helper()
	e := up(t, t.TempDir(), WithCommands(forge.run))
	queue := &leadQueue{}
	e.h.NotifyLead(queue.notify)
	wishID, projectID := e.wish(t, repo)
	return e, queue, wishID, projectID
}

// endTask spawns a fake worker on prompt in the wish, part of the azima partOf when set, and waits for its end, and for
// what Djinn looked for then.
func endTask(t *testing.T, e *env, wishID, projectID, prompt string, partOf ...string) *planv1.Task {
	t.Helper()
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, ProjectId: projectID, Title: "Open the draft MR", Prompt: prompt, Provider: planv1.Provider_PROVIDER_FAKE,
		PartOf: strings.Join(partOf, ""),
	}))
	if err != nil {
		t.Fatal(err)
	}
	e.watch(t.Context(), t, res.Msg.GetTask().GetId(), 0)
	e.h.babysitting.Wait()
	return e.get(t, res.Msg.GetTask().GetId())
}

// watchers are the watchers of the wish.
func watchers(t *testing.T, e *env, wishID string) []*planv1.Task {
	t.Helper()
	tasks, err := store.List[*planv1.Task](t.Context(), e.db, store.Where{"wish_id": wishID})
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(tasks, func(t *planv1.Task) bool { return !watching(t) })
}

// leadWatchLines are the lines of kind watch the lead got.
func leadWatchLines(q *leadQueue) []string {
	var lines []string
	for _, e := range q.all() {
		if e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_WATCH {
			lines = append(lines, e.GetText())
		}
	}
	return lines
}

// TestBabysitWhatATaskOpens replays W5 of wish Dew (12/10/2026): a task opens a draft merge request with glab mr
// create, and ends. Djinn starts the project's babysit watcher in the same wish, on the merge request's iid, its state
// under its own folder, part of the task's azima, and tells the lead once. Another task of the wish that ends, its branch's merge request being
// the same one, starts no second watcher. The watcher's MERGED line ends it, done, though its command restarts.
func TestBabysitWhatATaskOpens(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	const url = "https://gitlab.com/acme/dolmen/-/merge_requests/123"
	forge := &fakeForge{
		views: map[string]string{"123": `{"iid":123,"state":"opened","web_url":"` + url + `"}`},
		lists: map[string]string{"w2-": `[{"iid":123,"web_url":"` + url + `"}]`},
	}
	e, queue, wishID, projectID := babysitWish(t, forge, babysitRepo(t, "git@gitlab.com:acme/dolmen.git", gitlabBabysit, ""))
	azima, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, ProjectId: projectID, Title: "Open the MR", Kind: planv1.TaskKind_TASK_KIND_AZIMA,
	}))
	if err != nil {
		t.Fatal(err)
	}
	opener := endTask(t, e, wishID, projectID, strings.Join([]string{
		`tool Bash {"command":"glab mr create --draft --fill --yes"}`,
		"result Creating draft merge request for w1 into main in acme/dolmen " + url,
		"text Draft MR opened.",
	}, "\n"), azima.Msg.GetTask().GetId())
	if opener.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("the opener: %s %s", opener.GetStatus(), opener.GetError())
	}
	got := watchers(t, e, wishID)
	if len(got) != 1 {
		t.Fatalf("watchers %v, want one", got)
	}
	w := got[0]
	want := &planv1.TaskBabysit{Url: url, Number: 123, Skill: "babysit-it", DoneWhen: "MERGED", OpenedBy: opener.GetCode()}
	if b := w.GetBabysit(); b.GetUrl() != want.GetUrl() || b.GetNumber() != 123 || b.GetSkill() != want.GetSkill() ||
		b.GetDoneWhen() != want.GetDoneWhen() || b.GetOpenedBy() != want.GetOpenedBy() {
		t.Errorf("babysit %v, want %v", b, want)
	}
	state := filepath.Join(e.home, "watchers", w.GetId(), "babysit-it-123")
	prompt, err := lastPrompt(t.Context(), e.db, w.GetId())
	if err != nil || prompt != "sh .agents/skills/babysit-it/watch.sh 123 "+state {
		t.Errorf("command %q (%v), want the iid and %s", prompt, err, state)
	}
	if !w.GetRestart() || w.GetProjectId() != projectID || w.GetTitle() != "Babysit MR !123" ||
		w.GetPartOf() != azima.Msg.GetTask().GetId() {
		t.Errorf("watcher %s %q, restart %v, part of %q: want restarted, in the project, part of the opener's azima",
			w.GetCode(), w.GetTitle(), w.GetRestart(), w.GetPartOf())
	}
	line := "Djinn: " + opener.GetCode() + " opened MR !123: " + w.GetCode() +
		" babysits it; follow the skill babysit-it each time it speaks. " + url
	if lines := leadWatchLines(queue); !slices.Contains(lines, line) {
		t.Errorf("the lead heard %q, want %q", lines, line)
	}

	endTask(t, e, wishID, projectID, "text Fixed the lint.")
	if got := watchers(t, e, wishID); len(got) != 1 {
		t.Errorf("watchers %v after a second task on the same MR, want still one", got)
	}
	waitFor(t, "the watcher ended on MERGED", func() bool {
		return e.get(t, w.GetId()).GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE
	})
	var told int
	for _, l := range leadWatchLines(queue) {
		if strings.Contains(l, "babysits it") {
			told++
		}
	}
	if told != 1 {
		t.Errorf("the lead was told %d times of the watcher, want once: %q", told, leadWatchLines(queue))
	}
}

// TestBabysitTheTaskBranch: on GitHub, a task whose output names no pull request, but whose branch has one open,
// gets it babysat with the skill's command; the lead hears it the task opened it.
func TestBabysitTheTaskBranch(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	const url = "https://github.com/acme/lamp/pull/12"
	forge := &fakeForge{lists: map[string]string{"w1-": `[{"number":12,"url":"` + url + `"}]`}}
	e, queue, wishID, projectID := babysitWish(t, forge, babysitRepo(t, "https://github.com/acme/lamp.git",
		"      watch: \"sh .agents/skills/babysit-it/watch.sh {pr}\"\n", ""))
	opener := endTask(t, e, wishID, projectID, "text Pushed.")
	got := watchers(t, e, wishID)
	if len(got) != 1 || got[0].GetBabysit().GetNumber() != 12 || got[0].GetTitle() != "Babysit PR #12" {
		t.Fatalf("watchers %v, want one on PR #12", got)
	}
	if prompt, _ := lastPrompt(t.Context(), e.db, got[0].GetId()); prompt != "sh .agents/skills/babysit-it/watch.sh 12" {
		t.Errorf("command %q", prompt)
	}
	if lines := leadWatchLines(queue); !slices.ContainsFunc(lines, func(l string) bool {
		return strings.HasPrefix(l, "Djinn: "+opener.GetCode()+" opened PR #12: ")
	}) {
		t.Errorf("the lead heard %q", lines)
	}
	if calls := forge.called(); !slices.Contains(calls, "gh pr list --head "+opener.GetBranch()+" --state open --json number,url") {
		t.Errorf("forge calls %q, want the task's branch looked up", calls)
	}
}

// TestBabysitOffOrUndeclared: a project whose settings turn babysit off, or whose skills declare none, starts no
// watcher, asks the forge nothing, and tells the lead nothing.
func TestBabysitOffOrUndeclared(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	const url = "https://gitlab.com/acme/dolmen/-/merge_requests/123"
	for name, repo := range map[string]func() string{
		"off": func() string {
			return babysitRepo(t, "git@gitlab.com:acme/dolmen.git", gitlabBabysit, "babysit: false\n")
		},
		"undeclared": func() string { return babysitRepo(t, "git@gitlab.com:acme/dolmen.git", "", "") },
	} {
		forge := &fakeForge{views: map[string]string{"123": `{"iid":123,"state":"opened","web_url":"` + url + `"}`}}
		e, queue, wishID, projectID := babysitWish(t, forge, repo())
		task := endTask(t, e, wishID, projectID, "tool Bash glab mr create --draft\nresult "+url)
		if task.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Errorf("%s: the task %s %s", name, task.GetStatus(), task.GetError())
		}
		if got := watchers(t, e, wishID); len(got) != 0 {
			t.Errorf("%s: watchers %v, want none", name, got)
		}
		if calls := forge.called(); len(calls) != 0 {
			t.Errorf("%s: forge calls %q, want none", name, calls)
		}
		if lines := leadWatchLines(queue); len(lines) != 0 {
			t.Errorf("%s: the lead heard %q", name, lines)
		}
	}
}

// TestBabysitByHand: djinn task spawn --babysit --after W1 waits for W1, then watches the merge request W1 opened,
// with the project's command; a project without a declaration refuses it at once.
func TestBabysitByHand(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	const url = "https://gitlab.com/acme/dolmen/-/merge_requests/7"
	forge := &fakeForge{views: map[string]string{"7": `{"iid":7,"state":"opened","web_url":"` + url + `"}`}}
	e, _, wishID, projectID := babysitWish(t, forge, babysitRepo(t, "https://gitlab.com/acme/dolmen.git", gitlabBabysit, "babysit: false\n"))
	opener, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, ProjectId: projectID, Title: "Open the MR", Provider: planv1.Provider_PROVIDER_FAKE,
		Prompt: "wait\ntool Bash glab mr create --fill\nresult " + url,
	}))
	if err != nil {
		t.Fatal(err)
	}
	code := opener.Msg.GetTask().GetCode()
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Babysit " + code + "'s MR", Babysit: true, After: []string{code},
	}))
	if err != nil {
		t.Fatal(err)
	}
	w := res.Msg.GetTask()
	if w.GetProvider() != planv1.Provider_PROVIDER_WATCH || w.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || !w.GetRestart() {
		t.Fatalf("babysit %s %s, restart %v: want a planned watcher that restarts", w.GetProvider(), w.GetStatus(), w.GetRestart())
	}
	if _, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{TaskId: opener.Msg.GetTask().GetId(), Text: "go"})); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the babysit to start on !7", func() bool { return e.get(t, w.GetId()).GetBabysit().GetNumber() == 7 })
	got := e.get(t, w.GetId())
	if got.GetBabysit().GetOpenedBy() != code || got.GetBabysit().GetUrl() != url {
		t.Errorf("babysit %v, want !7 opened by %s", got.GetBabysit(), code)
	}
	state := filepath.Join(e.home, "watchers", w.GetId(), "babysit-it-7")
	if prompt, _ := lastPrompt(t.Context(), e.db, w.GetId()); prompt != "sh .agents/skills/babysit-it/watch.sh 7 "+state {
		t.Errorf("command %q", prompt)
	}

	plain, _, wish2, _ := babysitWish(t, forge, babysitRepo(t, "https://gitlab.com/acme/dolmen.git", "", ""))
	_, err = plain.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wish2, Title: "Babysit", Babysit: true,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "names no babysit") {
		t.Errorf("--babysit without a declaration: %v", err)
	}
}
