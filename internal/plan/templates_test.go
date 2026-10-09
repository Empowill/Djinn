package plan

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// agentSkill writes the skill name in the project folder dir, under .agents/skills, its front matter holding front.
func agentSkill(t *testing.T, dir, name, front string) string {
	t.Helper()
	return writeSkill(t, dir, SkillFolders[0], name, "name: "+name+"\ndescription: Test skill.\n"+front)
}

// babysitFront is a template as a project writes it, for the tests.
const babysitFront = `metadata:
  other-tool: kept apart
  djinn:
    wish:
      title: "Babysit PR #{pr}"
      match: '(?i)\bbabysit\w*\b.*?(?:/pull/|#|\bpr\s*#?)(?P<pr>\d+)'
      watch: "sh watch.sh {pr}"
      done_when: MERGED
`

// TestReadTemplate: Djinn's own babysit-pr template reads and fills; a skill without one has none; a template that
// cannot be used says why.
func TestReadTemplate(t *testing.T) {
	own, err := ReadTemplate(filepath.Join("..", "..", ".agents", "skills", "babysit-pr"))
	if err != nil || own == nil {
		t.Fatalf("babysit-pr: %v, %v", own, err)
	}
	if own.Skill != "babysit-pr" || own.Title != "Babysit PR #{pr}" || own.DoneWhen != "MERGED" {
		t.Errorf("babysit-pr = %+v", own)
	}
	for request, want := range map[string]string{
		"babysit https://github.com/acme/lamp/pull/12":    "12",
		"Please babysit PR #34, the CI is flaky":          "34",
		"peux-tu babysitter la PR 56 ?":                   "56",
		"babysit #78":                                     "78",
		"fix the lint of PR #12":                          "",
		"babysit the release, then open an issue":         "",
		"babysit https://github.com/acme/lamp/issues/12 ": "",
	} {
		title, watch, ok := own.Fill(request)
		if want == "" {
			if ok {
				t.Errorf("%q matched: %q, %q", request, title, watch)
			}
			continue
		}
		if !ok || title != "Babysit PR #"+want || watch != "sh .agents/skills/babysit-pr/watch.sh "+want {
			t.Errorf("%q: %q, %q, %v", request, title, watch, ok)
		}
	}

	dir := t.TempDir()
	for name, front := range map[string]string{
		"plain":       "",
		"other-meta":  "metadata:\n  author: someone\n",
		"djinn-other": "metadata:\n  djinn:\n    colour: blue\n",
	} {
		if tmpl, err := ReadTemplate(agentSkill(t, dir, name, front)); tmpl != nil || err != nil {
			t.Errorf("%s: %v, %v", name, tmpl, err)
		}
	}
	for name, c := range map[string]struct{ front, want string }{
		"no-title":    {"metadata:\n  djinn:\n    wish:\n      match: x\n", "no title"},
		"no-match":    {"metadata:\n  djinn:\n    wish:\n      title: x\n", "no match"},
		"bad-regexp":  {"metadata:\n  djinn:\n    wish:\n      title: x\n      match: '(x'\n", "match"},
		"unknown":     {"metadata:\n  djinn:\n    wish:\n      title: 'Fix {bug}'\n      match: '(?P<pr>\\d+)'\n", "{bug}"},
		"no-watch":    {"metadata:\n  djinn:\n    wish:\n      title: x\n      match: x\n      done_when: DONE\n", "no watch"},
		"restart":     {"metadata:\n  djinn:\n    wish:\n      title: x\n      match: x\n      restart: true\n", "no watch"},
		"not-yaml":    {"metadata: [\n", "not YAML"},
		"wish-string": {"metadata:\n  djinn:\n    wish: babysit\n", "metadata.djinn"},
	} {
		tmpl, err := ReadTemplate(agentSkill(t, dir, name, c.front))
		if tmpl != nil || err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, %v; want an error about %q", name, tmpl, err, c.want)
		}
	}
}

// TestFillQuotes: a value from a request is one word of the watcher's command line, whatever it holds.
func TestFillQuotes(t *testing.T) {
	tmpl, err := parseTemplate("s", "", templateYAML{Title: "Watch {what}", Match: `watch (?P<what>.+)`, Watch: "w --on {what}"})
	if err != nil {
		t.Fatal(err)
	}
	for request, want := range map[string]string{
		"watch acme/lamp!12":     "w --on acme/lamp!12",
		"watch it's; rm -rf ~":   `w --on 'it'\''s; rm -rf ~'`,
		"watch $(touch x) `x` y": "w --on '$(touch x) `x` y'",
	} {
		if _, watch, ok := tmpl.Fill(request); !ok || watch != want {
			t.Errorf("%q: %q, want %q", request, watch, want)
		}
	}
}

func TestDoneLine(t *testing.T) {
	tmpl := &planv1.WishTemplate{DoneWhen: "MERGED"}
	for text, want := range map[string]string{
		"MERGED": "MERGED",
		"checks: 3 pass\n  MERGED: PR #12 merged": "MERGED: PR #12 merged",
		"MERGEDX":    "",
		"NOT MERGED": "",
		"merged":     "",
	} {
		if got := DoneLine(tmpl, text); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
	if got := DoneLine(&planv1.WishTemplate{}, "MERGED"); got != "" {
		t.Errorf("without done_when: %q", got)
	}
}

// TestRouteTemplate: a request that matches a skill's template in the new wish's project proposes its wish, titled
// by the template; one that does not, or in a project without the skill, proposes none. A wish on the same piece of
// work still takes the request.
func TestRouteTemplate(t *testing.T) {
	lamp := &planv1.Project{Id: store.NewID(), Name: "lamp", Directory: t.TempDir(), Git: true,
		Remote: "https://github.com/acme/lamp.git"}
	bare := &planv1.Project{Id: store.NewID(), Name: "bare", Directory: t.TempDir(), Git: true}
	agentSkill(t, lamp.GetDirectory(), "babysit", babysitFront)
	templates, err := Templates(t.Context(), nil, []*planv1.Project{lamp, bare})
	if err != nil || len(templates) != 1 || templates[0].ProjectID != lamp.GetId() {
		t.Fatalf("templates = %v, %v", templates, err)
	}
	from := &planv1.Wish{Id: "from", Title: "The sidebar", ProjectIds: []string{lamp.GetId()}, State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
	in := func(text string, wishes ...*planv1.Wish) routeInput {
		return routeInput{lang: "en", text: text, from: from, wishes: append([]*planv1.Wish{from}, wishes...),
			projects: []*planv1.Project{lamp, bare}, templates: templates}
	}

	route := propose(in("babysit https://github.com/acme/lamp/pull/12"))
	opt := route.GetOptions()[0]
	if opt.GetKind() != planv1.RouteKind_ROUTE_KIND_NEW || opt.GetTitle() != "Babysit PR #12" ||
		opt.GetTemplate().GetSkill() != "babysit" || opt.GetTemplate().GetWatch() != "sh watch.sh 12" ||
		opt.GetTemplate().GetDoneWhen() != "MERGED" || opt.GetTemplate().GetProjectId() != lamp.GetId() ||
		!strings.Contains(opt.GetReason(), "the skill babysit makes this wish") {
		t.Errorf("option = %v", opt)
	}
	if got := optionText("en", opt, "", map[string]string{lamp.GetId(): "lamp"}); got != "New wish “Babysit PR #12”, in lamp, from the skill babysit" {
		t.Errorf("option text = %q", got)
	}
	if opt := propose(in("fix the lint of the sidebar")).GetOptions()[0]; opt.GetTemplate() != nil {
		t.Errorf("no match: %v", opt)
	}
	other := in("babysit PR #12")
	other.from = &planv1.Wish{Id: "from", ProjectIds: []string{bare.GetId()}}
	if opt := propose(other).GetOptions()[0]; opt.GetTemplate() != nil {
		t.Errorf("a project without the skill: %v", opt)
	}

	// The same pull request babysat already: filing is recommended. A wish that only shares words is not.
	same := &planv1.Wish{Id: "same", Title: "Babysit PR #12", ProjectIds: []string{lamp.GetId()}, State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 2}
	if opt := propose(in("babysit PR #12 again", same)).GetOptions()[0]; opt.GetKind() != planv1.RouteKind_ROUTE_KIND_FILE || opt.GetWishId() != "same" {
		t.Errorf("same pull request: %v", opt)
	}
	near := &planv1.Wish{Id: "near", Title: "Babysit the flaky checks", ProjectIds: []string{lamp.GetId()}, State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 2}
	if opt := propose(in("babysit the flaky checks of PR #12", near)).GetOptions()[0]; opt.GetTemplate() == nil {
		t.Errorf("a wish sharing words: %v", opt)
	}
}

// spawned records the watchers a test starts.
type spawned struct{ calls []string }

func (s *spawned) spawn(_ context.Context, wishID, projectID, title, watch string, restart bool) (string, error) {
	if restart {
		watch += " (restart)"
	}
	s.calls = append(s.calls, strings.Join([]string{wishID, projectID, title, watch}, " | "))
	return "W1", nil
}

// TestTemplateWish: rubbing the lamp on a template's card makes the wish with its template, starts its watcher in
// the skill's project, and starts its lead on the skill. The watcher's done line asks once whether to grant the
// wish; B keeps it open, A grants it.
func TestTemplateWish(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the lines are checked for the shells of Unix")
	}
	ctx := t.Context()
	home := t.TempDir()
	watchers := &spawned{}
	c, leads := serveLeads(t, home, WithWatchers(watchers.spawn))
	lamp := &planv1.Project{Id: store.NewID(), Name: "lamp", Directory: t.TempDir(), Git: true,
		Remote: "https://github.com/acme/lamp.git"}
	skill := agentSkill(t, lamp.GetDirectory(), "babysit", babysitFront)
	c.put(t, lamp)
	res, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "The sidebar", ProjectIds: []string{lamp.GetId()}}))
	if err != nil {
		t.Fatal(err)
	}
	here := res.Msg.GetWish()
	if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: here.GetId(), SessionId: session}); err != nil {
		t.Fatal(err)
	}

	request := "babysit https://github.com/acme/lamp/pull/12"
	q := c.route(t, here.GetId(), request)
	if q.GetOptions()[0] != "New wish “Babysit PR #12”, in lamp, from the skill babysit" {
		t.Fatalf("options = %q", q.GetOptions())
	}
	opened := len(leads.opened)
	c.answer(t, q, planv1.Choice_CHOICE_A, "")
	made := wishTitled(t, c, "Babysit PR #12")
	if made == nil || made.GetTemplate().GetSkill() != "babysit" || made.GetTemplate().GetWatch() != "sh watch.sh 12" {
		t.Fatalf("made = %v", made)
	}
	if want := []string{made.GetId() + " | " + lamp.GetId() + " | Watch, for the skill babysit | sh watch.sh 12"}; !slices.Equal(watchers.calls, want) {
		t.Errorf("watchers %q, want %q", watchers.calls, want)
	}
	if len(leads.opened) <= opened || !strings.HasPrefix(leads.opened[opened], "claude --session-id "+made.GetLead().GetSessionId()) {
		t.Errorf("opened %q", leads.opened[opened:])
	}
	first, err := os.ReadFile(filepath.Join(home, PagesDir, made.GetId(), LeadFirstFile))
	want := FirstLine(here.GetTitle(), request) + " This wish follows the skill babysit (" + filepath.Join(skill, SkillFile) +
		"): read it first, and follow it. Its watcher W1 runs sh watch.sh 12 and tells you what changes. When it prints " +
		"MERGED, Djinn asks the developer whether to grant the wish.\n\n" + StartLine(made.GetId())
	if err != nil || string(first) != want {
		t.Errorf("the lead's first message:\n%s\nwant\n%s", first, want)
	}
	brief, err := BuildBrief(ctx, c.store, home, made.GetId())
	if err != nil || !strings.Contains(brief.Moving, "- Made from the wish template of the skill `babysit`: follow that skill. Its watcher runs `sh watch.sh 12`") {
		t.Errorf("the brief does not say the template (%v):\n%s", err, brief.Moving)
	}

	// The watcher's paragraphs: a change asks nothing; the done line asks once, B keeps the wish open.
	wishes := &Wishes{Store: c.store, Language: "en"}
	watcher := &planv1.Task{Id: store.NewID(), WishId: made.GetId(), Code: "W1"}
	grants := func() []*planv1.Question {
		t.Helper()
		qs, err := store.List[*planv1.Question](ctx, c.store, store.Where{"wish_id": made.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		return slices.DeleteFunc(qs, func(q *planv1.Question) bool { return !q.GetGrant() })
	}
	for _, text := range []string{"PR #12 · checks: 1 fail (lint)", "MERGED: PR #12 is merged.", "MERGED: PR #12 is merged."} {
		done, err := wishes.Watched(ctx, watcher, text)
		if err != nil || done != strings.HasPrefix(text, "MERGED") {
			t.Fatalf("%q: %v, %v", text, done, err)
		}
	}
	asked := grants()
	if len(asked) != 1 || asked[0].GetText() != "W1's watcher says “MERGED: PR #12 is merged.”: grant “Babysit PR #12”?" ||
		!slices.Equal(asked[0].GetOptions(), []string{"Grant the wish", "Keep it open"}) || !strings.HasPrefix(asked[0].GetRecommendation(), "A: ") {
		t.Fatalf("grant questions = %v", asked)
	}
	c.answer(t, asked[0], planv1.Choice_CHOICE_B, "a follow-up is left")
	if w := wishTitled(t, c, "Babysit PR #12"); w.GetState() == planv1.WishState_WISH_STATE_GRANTED {
		t.Errorf("B granted the wish")
	}
	if _, err := wishes.Watched(ctx, watcher, "MERGED"); err != nil {
		t.Fatal(err)
	}
	asked = grants()
	if len(asked) != 2 || asked[1].GetAnswer() != nil {
		t.Fatalf("asked again = %v", asked)
	}
	c.answer(t, asked[1], planv1.Choice_CHOICE_A, "")
	if w := wishTitled(t, c, "Babysit PR #12"); w.GetState() != planv1.WishState_WISH_STATE_GRANTED {
		t.Errorf("A did not grant the wish: %v", w)
	}
	// Granted: the done line asks nothing more, and a wish made without a template never asks.
	if done, err := wishes.Watched(ctx, watcher, "MERGED"); !done || err != nil || len(grants()) != 2 {
		t.Errorf("after the grant: %v, %d questions", err, len(grants()))
	}
	if done, err := wishes.Watched(ctx, &planv1.Task{WishId: here.GetId(), Code: "W2"}, "MERGED"); done || err != nil {
		t.Fatalf("without a template: %v, %v", done, err)
	}
	if qs, _ := store.List[*planv1.Question](ctx, c.store, store.Where{"wish_id": here.GetId()}); slices.ContainsFunc(qs, (*planv1.Question).GetGrant) {
		t.Errorf("a wish without a template was asked to be granted")
	}
}

// TestTemplateWithoutWatchers: where djinn up runs no tasks, the lead is told to start its watcher itself; a watcher
// that fails to start says why.
func TestTemplateWithoutWatchers(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	lamp := &planv1.Project{Id: store.NewID(), Name: "lamp", Directory: t.TempDir()}
	c.put(t, lamp)
	wish := &planv1.Wish{Id: store.NewID(), Title: "Babysit PR #3", ProjectIds: []string{lamp.GetId()},
		Template: &planv1.WishTemplate{Skill: "babysit", ProjectId: lamp.GetId(), Watch: "sh watch.sh 3", Restart: true}}
	w := &Wishes{Store: c.store}
	if got := w.startTemplate(ctx, wish); !strings.Contains(got, "the skill babysit: read it first") ||
		!strings.Contains(got, `Start its watcher: djinn task spawn `+wish.GetId()+` "Watch, for the skill babysit" --project-id `+
			lamp.GetId()+` --provider watch --prompt "sh watch.sh 3" --restart.`) {
		t.Errorf("without watchers: %q", got)
	}
	w.Watchers = func(context.Context, string, string, string, string, bool) (string, error) {
		return "", connect.NewError(connect.CodeFailedPrecondition, os.ErrPermission)
	}
	if got := w.startTemplate(ctx, wish); !strings.Contains(got, "Its watcher did not start (failed_precondition: permission denied)") {
		t.Errorf("a failed watcher: %q", got)
	}
}

// TestSkillListTemplate: djinn skill list shows a skill's template, or why it cannot be used.
func TestSkillListTemplate(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	lamp := &planv1.Project{Id: store.NewID(), Name: "lamp", Directory: t.TempDir()}
	agentSkill(t, lamp.GetDirectory(), "babysit", babysitFront)
	agentSkill(t, lamp.GetDirectory(), "broken", "metadata:\n  djinn:\n    wish:\n      title: x\n")
	agentSkill(t, lamp.GetDirectory(), "plain", "")
	c.put(t, lamp)
	res, err := c.skills.List(ctx, connect.NewRequest(&planv1.SkillServiceListRequest{Project: "lamp"}))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range res.Msg.GetSkills() {
		got[s.GetName()] = s.GetTemplate() + "|" + s.GetTemplateError()
	}
	if got["babysit"] != "Babysit PR #{pr}|" || got["broken"] != "|metadata.djinn.wish has no match" || got["plain"] != "|" {
		t.Errorf("skills = %q", got)
	}
}
