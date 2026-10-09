package harness

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
)

// folder is a project folder outside Git, without any agent configuration.
func folder(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func (e *env) questions() planv1connect.QuestionServiceClient {
	return planv1connect.NewQuestionServiceClient(e.srv.Client(), e.srv.URL)
}

// editQuestionOf is the edit question of the task, as the question service lists it.
func (e *env) editQuestionOf(t *testing.T, task *planv1.Task) *planv1.Question {
	t.Helper()
	res, err := e.questions().List(t.Context(), connect.NewRequest(&planv1.QuestionServiceListRequest{WishId: task.GetWishId()}))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range res.Msg.GetQuestions() {
		if q.GetId() == task.GetEditQuestionId() {
			return q
		}
	}
	t.Fatalf("task %s has no edit question (%q)", task.GetCode(), task.GetEditQuestionId())
	return nil
}

func (e *env) answer(t *testing.T, q *planv1.Question, choice planv1.Choice) {
	t.Helper()
	if _, err := e.questions().Answer(t.Context(), connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q.GetId()}}, Choice: choice,
	})); err != nil {
		t.Fatal(err)
	}
}

func eventTexts(events []*planv1.TaskEvent) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.GetText()
	}
	return out
}

func hasText(events []*planv1.TaskEvent, prefix string) bool {
	return slices.ContainsFunc(events, func(ev *planv1.TaskEvent) bool { return strings.HasPrefix(ev.GetText(), prefix) })
}

// TestAskToEdit: in a folder outside Git without agent configuration, the worker starts read-only and the task asks
// whether it may edit. Without an answer nothing is written and the task waits; "no" leaves it read-only and done;
// "yes" starts the worker again, allowed to edit.
func TestAskToEdit(t *testing.T) {
	const script = "text reading\nwrite notes.md hello"
	e := up(t, t.TempDir())

	// Without an answer.
	dir := folder(t)
	wishID, _ := e.wish(t, dir)
	task := e.spawn(t, wishID, script)
	if task.GetAccess() != planv1.TaskAccess_TASK_ACCESS_ASKING || task.GetEditQuestionId() == "" {
		t.Fatalf("spawned %v", task)
	}
	q := e.editQuestionOf(t, task)
	if q.GetCode() != "Q01" || len(q.GetOptions()) != 2 || !strings.Contains(q.GetText(), "task W1") ||
		!strings.HasPrefix(q.GetOptions()[0], "Yes") || !strings.HasPrefix(q.GetOptions()[1], "No") {
		t.Errorf("question = %v", q)
	}
	events := e.watch(t.Context(), t, task.GetId(), 0)
	if !strings.Contains(events[1].GetText(), "read-only until Q01 is answered") || !hasText(events, "permission denied: Write notes.md") {
		t.Errorf("events = %q", eventTexts(events))
	}
	got := e.get(t, task.GetId())
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_WAITING || got.GetAccess() != planv1.TaskAccess_TASK_ACCESS_ASKING {
		t.Errorf("unanswered task = %v", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("written without an answer: %v", entries)
	}

	// No.
	e.answer(t, q, planv1.Choice_CHOICE_B)
	got = e.get(t, task.GetId())
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetAccess() != planv1.TaskAccess_TASK_ACCESS_EDIT_REFUSED {
		t.Errorf("after no: %v", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("written after no: %v", entries)
	}
	// Answering again changes nothing: only the first answer counts.
	e.answer(t, q, planv1.Choice_CHOICE_A)
	if again := e.get(t, task.GetId()); again.GetAccess() != planv1.TaskAccess_TASK_ACCESS_EDIT_REFUSED {
		t.Errorf("a second answer counted: %v", again)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("written after a second answer: %v", entries)
	}

	// Yes.
	dir = folder(t)
	wishID, _ = e.wish(t, dir)
	task = e.spawn(t, wishID, script)
	before := e.watch(t.Context(), t, task.GetId(), 0)
	e.answer(t, e.editQuestionOf(t, task), planv1.Choice_CHOICE_A)
	after := e.watch(t.Context(), t, task.GetId(), int64(len(before)))
	checkSeqs(t, append(before, after...), 1)
	if !hasText(after, "edit granted (Q01)") || !hasText(after, "started fake again") || !hasText(after, "Write notes.md") {
		t.Errorf("events after yes = %q", eventTexts(after))
	}
	got = e.get(t, task.GetId())
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetAccess() != planv1.TaskAccess_TASK_ACCESS_EDIT_GRANTED {
		t.Errorf("after yes: %v", got)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "notes.md")); err != nil || string(b) != "hello\n" {
		t.Errorf("notes.md = %q, %v", b, err)
	}
}

// TestAskToEditWhileRunning: a yes while the read-only worker runs stops it, and starts it again allowed to edit;
// the task's watchers follow both workers in one stream.
func TestAskToEditWhileRunning(t *testing.T) {
	e := up(t, t.TempDir())
	dir := folder(t)
	wishID, _ := e.wish(t, dir)
	task := e.spawn(t, wishID, "write notes.md hello\nsleep 1h")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	s, err := e.tasks.Watch(ctx, connect.NewRequest(&planv1.TaskServiceWatchRequest{TaskId: task.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	waitFor := func(prefix string) {
		t.Helper()
		for s.Receive() {
			if strings.HasPrefix(s.Msg().GetEvent().GetText(), prefix) {
				return
			}
		}
		t.Fatalf("no event %q: %v", prefix, s.Err())
	}
	waitFor("permission denied: Write notes.md")
	e.answer(t, e.editQuestionOf(t, task), planv1.Choice_CHOICE_A)
	waitFor("edit granted (Q01)")
	waitFor("Write notes.md")
	if b, err := os.ReadFile(filepath.Join(dir, "notes.md")); err != nil || string(b) != "hello\n" {
		t.Errorf("notes.md = %q, %v", b, err)
	}
	res, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Msg.GetTask(); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED || got.GetAccess() != planv1.TaskAccess_TASK_ACCESS_EDIT_GRANTED {
		t.Errorf("stopped = %v", got)
	}
}

// TestAskToEditUnableToRead: an agent that cannot be kept from writing does not start before the answer; a yes
// starts it, allowed to edit.
func TestAskToEditUnableToRead(t *testing.T) {
	e := up(t, t.TempDir())
	e.h.providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = Antigravity{Command: "djinn-no-such-agy"}
	wishID, _ := e.wish(t, folder(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Edit with agy", Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
	}))
	if err != nil {
		t.Fatal(err)
	}
	task := res.Msg.GetTask()
	if task.GetStatus() != planv1.TaskStatus_TASK_STATUS_WAITING {
		t.Errorf("spawned %v", task)
	}
	e.answer(t, e.editQuestionOf(t, task), planv1.Choice_CHOICE_A)
	events := e.watch(t.Context(), t, task.GetId(), 0)
	got := e.get(t, task.GetId())
	// agy is not installed here: it was started, allowed to edit, and failed for that.
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED || !strings.Contains(got.GetError(), "djinn-no-such-agy not found") ||
		!hasText(events, "edit granted (Q01)") {
		t.Errorf("task = %v\nevents = %q", got, eventTexts(events))
	}
}

// TestAskToEditWaitsForASlot: a yes to the edit question of a task no worker runs goes through the scheduler: on a
// full machine the task waits, resuming, and says why; it starts once a slot frees, allowed to edit.
func TestAskToEditWaitsForASlot(t *testing.T) {
	l := &limit{slots: 1}
	e := up(t, t.TempDir(), WithCapacity(l.capacity))
	dir := folder(t)
	wishID, _ := e.wish(t, dir)
	task := e.mustSpawn(t, wishID, "Edit", "text reading\nwrite notes.md hello",
		&planv1.TaskServiceSpawnRequest{WriteScopes: []string{"notes.md"}})
	e.watch(t.Context(), t, task.GetId(), 0)
	if got := e.get(t, task.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_WAITING {
		t.Fatalf("before the answer: %v", got)
	}
	// It reads, and takes the one slot.
	busy := e.mustSpawn(t, wishID, "Busy", "sleep 1h", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"other"}})

	e.answer(t, e.editQuestionOf(t, task), planv1.Choice_CHOICE_A)
	got := e.get(t, task.GetId())
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RESUMING || got.GetAccess() != planv1.TaskAccess_TASK_ACCESS_EDIT_GRANTED ||
		!strings.HasPrefix(got.GetWaitReason(), "edit granted; ") || !strings.Contains(got.GetWaitReason(), "the most this machine holds") {
		t.Fatalf("after yes on a full machine: %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.md")); err == nil {
		t.Fatal("written before a slot freed")
	}
	if n := e.h.Running(); n != 1 {
		t.Errorf("%d workers run, want the busy one alone", n)
	}

	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: busy.GetId()})); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	events := e.watch(ctx, t, task.GetId(), 0)
	checkSeqs(t, events, 1)
	got = e.get(t, task.GetId())
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetAccess() != planv1.TaskAccess_TASK_ACCESS_EDIT_GRANTED ||
		got.GetResumes() != 0 || got.GetWaitReason() != "" {
		t.Errorf("after the slot freed: %v", got)
	}
	if !hasText(events, "edit granted (Q01)") || !hasText(events, "waiting: edit granted; ") || !hasText(events, "started fake again") {
		t.Errorf("events = %q", eventTexts(events))
	}
	if b, err := os.ReadFile(filepath.Join(dir, "notes.md")); err != nil || string(b) != "hello\n" {
		t.Errorf("notes.md = %q, %v", b, err)
	}
}

// TestSpawnAccess: what the worker is started with, by where it runs.
func TestSpawnAccess(t *testing.T) {
	e := up(t, t.TempDir())
	rec := recorder{specs: make(chan Spec, 1)}
	e.h.providers[planv1.Provider_PROVIDER_CLAUDE] = rec
	spawn := func(dir string) (*planv1.Task, Spec) {
		t.Helper()
		wishID, _ := e.wish(t, dir)
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{WishId: wishID, Title: "Look"}))
		if err != nil {
			t.Fatal(err)
		}
		spec := <-rec.specs
		e.watch(t.Context(), t, res.Msg.GetTask().GetId(), 0)
		return e.get(t, res.Msg.GetTask().GetId()), spec
	}

	// .agents/permissions.txtpb: translated, never read-only.
	dir := folder(t)
	writeFile(t, dir, ".agents/permissions.txtpb", "edit: false\ncommands: \"ls\"\n")
	task, spec := spawn(dir)
	if task.GetAccess() != planv1.TaskAccess_TASK_ACCESS_AGENTS || spec.ReadOnly || spec.Permissions.GetEdit() ||
		spec.Permissions.GetCommands()[0] != "ls" || task.GetEditQuestionId() != "" || task.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("with .agents: %v, %+v", task, spec)
	}

	// A Git repository without configuration: the agent's own configuration, nothing passed.
	task, spec = spawn(gitRepo(t))
	if task.GetAccess() != planv1.TaskAccess_TASK_ACCESS_NATIVE || spec.ReadOnly || spec.Permissions != nil || task.GetEditQuestionId() != "" {
		t.Errorf("in Git: %v, %+v", task, spec)
	}

	// A folder with a CLAUDE.md: the same.
	dir = folder(t)
	writeFile(t, dir, "CLAUDE.md", "# Notes\n")
	task, spec = spawn(dir)
	if task.GetAccess() != planv1.TaskAccess_TASK_ACCESS_NATIVE || spec.ReadOnly || spec.Permissions != nil {
		t.Errorf("with CLAUDE.md: %v, %+v", task, spec)
	}

	// A folder without anything: read-only, asking.
	task, spec = spawn(folder(t))
	if task.GetAccess() != planv1.TaskAccess_TASK_ACCESS_ASKING || !spec.ReadOnly || task.GetStatus() != planv1.TaskStatus_TASK_STATUS_WAITING {
		t.Errorf("empty folder: %v, %+v", task, spec)
	}

	// An invalid permissions file refuses the task, and says why.
	dir = folder(t)
	writeFile(t, dir, ".agents/permissions.txtpb", "edit: perhaps\n")
	wishID, _ := e.wish(t, dir)
	_, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{WishId: wishID, Title: "Look"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "permissions.txtpb") {
		t.Errorf("invalid file: %v", err)
	}
}

// TestWishAllow: a wish's allowance applies to every task of that wish in that project, over the project's
// configuration, and to no other wish.
func TestWishAllow(t *testing.T) {
	e := up(t, t.TempDir())
	rec := recorder{specs: make(chan Spec, 1)}
	e.h.providers[planv1.Provider_PROVIDER_CLAUDE] = rec
	dir := folder(t)
	writeFile(t, dir, ".agents/permissions.txtpb", "edit: false\ncommands: \"ls\"\n")
	wishID, projectID := e.wish(t, dir)
	other, err := e.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Another", ProjectIds: []string{projectID}}))
	if err != nil {
		t.Fatal(err)
	}
	spawn := func(wishID string) (*planv1.Task, Spec) {
		t.Helper()
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{WishId: wishID, Title: "Edit"}))
		if err != nil {
			t.Fatal(err)
		}
		spec := <-rec.specs
		e.watch(t.Context(), t, res.Msg.GetTask().GetId(), 0)
		return res.Msg.GetTask(), spec
	}
	allow := func(mode planv1.Allowance) *planv1.Wish {
		t.Helper()
		res, err := e.wishes.Allow(t.Context(), connect.NewRequest(&planv1.WishServiceAllowRequest{WishId: wishID, Mode: mode}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetWish()
	}

	// No allowance: the project's .agents decides.
	task, spec := spawn(wishID)
	if task.GetAccess() != planv1.TaskAccess_TASK_ACCESS_AGENTS || spec.Permissions.GetEdit() {
		t.Errorf("no allowance: %v, %+v", task, spec)
	}
	// Edit: the worker may edit, the project's commands stay, nothing more.
	if w := allow(planv1.Allowance_ALLOWANCE_EDIT); len(w.GetAllowances()) != 1 || w.GetAllowances()[0].GetProjectId() != projectID {
		t.Errorf("allowed wish = %v", w)
	}
	task, spec = spawn(wishID)
	if p := spec.Permissions; task.GetAccess() != planv1.TaskAccess_TASK_ACCESS_WISH_EDIT || spec.ReadOnly || !p.GetEdit() ||
		p.GetMode() != djinnv1.Mode_MODE_LISTED || p.GetNetwork() || !slices.Equal(p.GetCommands(), []string{"ls"}) {
		t.Errorf("edit: %v, %+v", task, spec)
	}
	// Auto: the agent's auto mode, editing.
	if w := allow(planv1.Allowance_ALLOWANCE_AUTO); len(w.GetAllowances()) != 1 {
		t.Errorf("a second allowance added a row: %v", w)
	}
	task, spec = spawn(wishID)
	if p := spec.Permissions; task.GetAccess() != planv1.TaskAccess_TASK_ACCESS_WISH_AUTO || !p.GetEdit() || p.GetMode() != djinnv1.Mode_MODE_AUTO {
		t.Errorf("auto: %v, %+v", task, spec)
	}
	// Another wish on the same project does not inherit it.
	task, spec = spawn(other.Msg.GetWish().GetId())
	if task.GetAccess() != planv1.TaskAccess_TASK_ACCESS_AGENTS || spec.Permissions.GetEdit() {
		t.Errorf("another wish: %v, %+v", task, spec)
	}
	// None takes it back.
	if w := allow(planv1.Allowance_ALLOWANCE_NONE); len(w.GetAllowances()) != 0 {
		t.Errorf("after none: %v", w)
	}
	if task, _ = spawn(wishID); task.GetAccess() != planv1.TaskAccess_TASK_ACCESS_AGENTS {
		t.Errorf("after none: %v", task)
	}

	// An allowance on a folder outside Git without configuration: no question, the fake edits.
	e.h.providers[planv1.Provider_PROVIDER_CLAUDE] = Fake{}
	plain := folder(t)
	wishID, _ = e.wish(t, plain)
	allow(planv1.Allowance_ALLOWANCE_EDIT)
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{WishId: wishID, Title: "Edit", Prompt: "write notes.md hi"}))
	if err != nil {
		t.Fatal(err)
	}
	e.watch(t.Context(), t, res.Msg.GetTask().GetId(), 0)
	if got := e.get(t, res.Msg.GetTask().GetId()); got.GetEditQuestionId() != "" || got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("allowed folder: %v", got)
	}
	if b, err := os.ReadFile(filepath.Join(plain, "notes.md")); err != nil || string(b) != "hi\n" {
		t.Errorf("notes.md = %q, %v", b, err)
	}

	// Refused: a project not of the wish; a wish of two projects without one named.
	if _, err := e.wishes.Allow(t.Context(), connect.NewRequest(&planv1.WishServiceAllowRequest{
		WishId: wishID, ProjectId: projectID, Mode: planv1.Allowance_ALLOWANCE_EDIT,
	})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a project not of the wish: %v", err)
	}
	two, err := e.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Two", Paused: true, ProjectIds: []string{projectID, e.get(t, res.Msg.GetTask().GetId()).GetProjectId()}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.wishes.Allow(t.Context(), connect.NewRequest(&planv1.WishServiceAllowRequest{
		WishId: two.Msg.GetWish().GetId(), Mode: planv1.Allowance_ALLOWANCE_EDIT,
	})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("two projects, none named: %v", err)
	}
	if _, err := e.wishes.Allow(t.Context(), connect.NewRequest(&planv1.WishServiceAllowRequest{WishId: wishID})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("no mode: %v", err)
	}
}
