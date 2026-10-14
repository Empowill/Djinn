package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

const mrURL = "https://gitlab.com/acme/dolmen/-/merge_requests/41"

// proposedMR replays wish Dew (13/10/2026) up to its question: a wish of a GitLab project pushes per azima, its azima
// T1's part is committed and its branch pushed; Djinn asks to open its merge request. It gives the question, the azima
// and its branch. forge answers glab.
func proposedMR(t *testing.T, forge *fakeForge, settings string) (*integration, *planv1.Question, *planv1.Task, string) {
	t.Helper()
	in := integrating(t)
	in.mu.Lock()
	in.forge = forge
	in.mu.Unlock()
	if settings != "" {
		writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
			"generated: \"gen/**\"\ngenerate: \"gen\"\ntest: \"test\"\n"+settings)
	}
	in.setAzimaStrategy(t)
	bare := filepath.Join(t.TempDir(), "gitlab.com", "acme", "dolmen.git")
	in.git(t, in.repo, "init", "--quiet", "--bare", bare)
	in.git(t, in.repo, "remote", "add", "origin", bare)
	in.git(t, in.repo, "push", "--quiet", "origin", in.branch)

	azima := in.azima(t, in.wishID, "Single local process")
	azima.Description = "## Goal\nOne local process.\n"
	putTaskInDB(t, in.db, azima)
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
	in.pass(t, 0)
	branch := "djinn/T1-single-local-process"
	in.git(t, in.repo, "push", "origin", branch)
	in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+branch, in.git(t, in.repo, "rev-parse", "refs/heads/"+branch))
	in.pass(t, 0)

	questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range questions {
		if q.GetDjinnMove().GetKind() == planv1.MoveKind_MOVE_KIND_PULL_REQUEST {
			if q.GetDjinnMove().GetRef() != branch || q.GetDjinnMove().GetProjectId() != in.projectID {
				t.Errorf("the question records %v, want branch %s in project %s", q.GetDjinnMove(), branch, in.projectID)
			}
			return in, q, azima, branch
		}
	}
	t.Fatalf("no merge request proposed; questions: %v", questions)
	return nil, nil, nil, ""
}

// digestKeeps fails t when the lead's digest cuts the line telling it the developer answered q and Djinn did did.
func digestKeeps(t *testing.T, q *planv1.Question, did string) {
	t.Helper()
	q = proto.CloneOf(q)
	q.Answer = &planv1.Answer{Choice: planv1.Choice_CHOICE_A}
	e := &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER, Code: q.GetCode(), Text: plan.AnswerLine(q, did)}
	if !plan.DigestKeeps(e) {
		t.Errorf("the lead's digest cuts %q", plan.DigestLine(e))
	}
}

// creates are the gh pr create and glab mr create forge ran.
func creates(forge *fakeForge) []string {
	var out []string
	for _, c := range forge.called() {
		if strings.HasPrefix(c, "gh pr create") || strings.HasPrefix(c, "glab mr create") {
			out = append(out, c)
		}
	}
	return out
}

// TestOpenMRWhenTheDeveloperSaysYes replays Q21 of wish Dew (13/10/2026): the developer answered "Open pull request"
// for an azima of a GitLab project, Djinn noted it and opened nothing. A yes now opens the merge request, as a draft,
// from the azima's branch into its base, with the title and description the question drafted; the azima's card shows
// it open, and the lead reads its address.
func TestOpenMRWhenTheDeveloperSaysYes(t *testing.T) {
	testx.Portable(t)
	forge := &fakeForge{created: "Creating draft merge request for djinn/T1-single-local-process into main\n\n" + mrURL + "\n"}
	in, q, azima, branch := proposedMR(t, forge, "")

	did := in.answer(t, q, planv1.Choice_CHOICE_A)
	if want := "Djinn opened draft MR !41: " + mrURL; did != want {
		t.Errorf("what Djinn did: %q, want %q", did, want)
	}
	digestKeeps(t, q, did)
	got := creates(forge)
	want := "glab mr create --draft --source-branch " + branch + " --target-branch " + in.branch +
		" --title One local process. --description ## Goal\n\nOne local process.\n\n### Parts\n\n- **W1**: Work of W1 --yes"
	if len(got) != 1 || got[0] != want {
		t.Errorf("forge created %q, want one %q", got, want)
	}
	if pr := in.get(t, azima.GetId()).GetAzima().GetPr(); pr.GetState() != planv1.AzimaPrState_AZIMA_PR_STATE_OPEN || pr.GetUrl() != mrURL {
		t.Errorf("the azima records %v, want its merge request open at %s", pr, mrURL)
	}

	// No: nothing is opened, the answer left for later.
	if did := in.answer(t, q, planv1.Choice_CHOICE_B); did != "left for later" {
		t.Errorf("a no: %q", did)
	}
	if got := creates(forge); len(got) != 1 {
		t.Errorf("a no opened %q", got)
	}
}

// TestOpenMRFailsToTheLead: glab cannot open the merge request (no token, a network down). The lead reads why, and that
// the developer must open it: never a yes noted and nothing opened. A merge request open for the branch already is
// named, none opened.
func TestOpenMRFailsToTheLead(t *testing.T) {
	testx.Portable(t)
	forge := &fakeForge{}
	in, q, _, branch := proposedMR(t, forge, "")

	did := in.answer(t, q, planv1.Choice_CHOICE_A)
	if want := "Djinn could not open the MR of " + branch + " (fake forge: 401 Unauthorized): the developer must open it"; did != want {
		t.Errorf("what Djinn did: %q, want %q", did, want)
	}
	digestKeeps(t, q, did)

	forge.mu.Lock()
	forge.lists = map[string]string{branch: `[{"iid":12,"web_url":"https://gitlab.com/acme/dolmen/-/merge_requests/12"}]`}
	forge.mu.Unlock()
	did = in.answer(t, q, planv1.Choice_CHOICE_A)
	if want := "MR !12 is open already: https://gitlab.com/acme/dolmen/-/merge_requests/12"; did != want {
		t.Errorf("what Djinn did: %q, want %q", did, want)
	}
	if got := creates(forge); len(got) != 1 {
		t.Errorf("forge created %q, want the one that failed", got)
	}
}

// TestOpenMRWithTheProjectSkill: dolmen holds open-mr-empowill, its house rules for a merge request (title, template,
// ticket link). A yes spawns a worker in the wish that opens it with that skill, from the azima's branch, pushing
// nothing; Djinn runs no glab mr create of its own.
func TestOpenMRWithTheProjectSkill(t *testing.T) {
	testx.Portable(t)
	forge := &fakeForge{created: mrURL}
	in, q, _, branch := proposedMR(t, forge, "provider: PROVIDER_FAKE\n")
	skill := filepath.Join(in.repo, "app", ".agents", "skills", "open-mr-acme")
	if err := os.MkdirAll(skill, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: open-mr-acme\ndescription: Open a MR.\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	did := in.answer(t, q, planv1.Choice_CHOICE_A)
	tasks, err := store.List[*planv1.Task](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil {
		t.Fatal(err)
	}
	var opener *planv1.Task
	for _, task := range tasks {
		if task.GetTitle() == "Open the draft MR of "+branch {
			opener = task
		}
	}
	if opener == nil {
		t.Fatalf("no task opens the merge request; Djinn did %q", did)
	}
	if want := "Djinn spawned " + opener.GetCode() + ": it opens the draft MR of " + branch + " with the skill open-mr-acme"; did != want {
		t.Errorf("what Djinn did: %q, want %q", did, want)
	}
	digestKeeps(t, q, did)
	if opener.GetPartOf() != "" || opener.GetProjectId() != in.projectID {
		t.Errorf("the opener is part of %q in %q: it reopens no azima, works in the project", opener.GetPartOf(), opener.GetProjectId())
	}
	prompt := openPrompt(&opening{branch: branch, base: in.branch, title: "One local process.",
		body: "## Goal\n\nOne local process.\n\n### Parts\n\n- **W1**: Work of W1"}, "MR", "open-mr-acme")
	for _, want := range []string{"following the project's skill open-mr-acme", "of branch " + branch + " into " + in.branch, "push nothing"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the opener's prompt lacks %q: %q", want, prompt)
		}
	}
	if got := creates(forge); len(got) != 0 {
		t.Errorf("Djinn created %q itself", got)
	}
}

// TestOpenSkill: a skill whose name says it opens a request; reviewing or babysitting one is not it.
func TestOpenSkill(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	for names, want := range map[string]string{
		"babysit-mr-empowill,open-mr-empowill,review-mr-empowill": "open-mr-empowill",
		"create-pull-request":                 "create-pull-request",
		"babysit-pr,review-mr,open-dashboard": "",
	} {
		dir := t.TempDir()
		for name := range strings.SplitSeq(names, ",") {
			skill := filepath.Join(dir, ".agents", "skills", name)
			if err := os.MkdirAll(skill, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if got := openSkill(&planv1.Project{Directory: dir}); got != want {
			t.Errorf("skills %s: %q, want %q", names, got, want)
		}
	}
}
