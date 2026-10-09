package plan

import (
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

func TestRouteRanking(t *testing.T) {
	shop := &planv1.Project{Id: store.NewID(), Name: "shop", Remote: "git@gitlab.com:acme/shop.git"}
	lamp := &planv1.Project{Id: store.NewID(), Name: "lamp", Remote: "https://github.com/acme/lamp"}
	projects := []*planv1.Project{shop, lamp}
	wish := func(title string, state planv1.WishState, rank int32, projects ...*planv1.Project) *planv1.Wish {
		w := &planv1.Wish{Id: store.NewID(), Title: title, State: state, Rank: rank}
		for _, p := range projects {
			w.ProjectIds = append(w.ProjectIds, p.GetId())
		}
		return w
	}
	active, paused, granted := planv1.WishState_WISH_STATE_ACTIVE, planv1.WishState_WISH_STATE_PAUSED,
		planv1.WishState_WISH_STATE_GRANTED
	babysit := wish("Babysit !37 · shop", active, 1, shop)
	sidebar := wish("Lamp: the sidebar", active, 2, lamp)
	notes := wish("Write the release notes", paused, 0)
	colours := wish("Sidebar colours of the lamp", granted, 0, lamp)
	oil := wish("Lamp oil", active, 3, lamp)
	blocks := map[string][]*planv1.Block{notes.GetId(): {{WishId: notes.GetId(), Title: "Changelog", Content: "Draft for v1.2"}}}
	type want struct {
		kinds  []planv1.RouteKind
		first  string // the wish of the first option, or the title of a new one
		reason string // in the first option's reason
		ids    []string
		pause  string
		filed  []*planv1.Wish // the wishes of the options that file the request, in order: the wish it came to last
	}
	file, made := planv1.RouteKind_ROUTE_KIND_FILE, planv1.RouteKind_ROUTE_KIND_NEW
	for _, c := range []struct {
		name    string
		request string
		from    *planv1.Wish
		wishes  []*planv1.Wish
		want    want
	}{{
		name:    "a link to another merge request of a project: a new wish there",
		request: "babysit https://gitlab.com/acme/shop/-/merge_requests/41 please",
		from:    sidebar, wishes: []*planv1.Wish{babysit, sidebar, notes, colours},
		want: want{kinds: []planv1.RouteKind{made, file, file, file}, first: "Babysit shop!41 please", reason: "not close enough",
			ids: []string{shop.GetId()}, filed: []*planv1.Wish{babysit, notes, sidebar}},
	}, {
		name:    "a link to the merge request a wish babysits: that wish",
		request: "the CI of https://gitlab.com/acme/shop/-/merge_requests/37 is red",
		from:    sidebar, wishes: []*planv1.Wish{babysit, sidebar, notes, colours},
		want: want{kinds: []planv1.RouteKind{file, file, made, file}, first: babysit.GetId(), reason: "the same !37",
			filed: []*planv1.Wish{babysit, notes, sidebar}},
	}, {
		name:    "a repository's path, without a link",
		request: "look at the pipelines of acme/shop",
		from:    sidebar, wishes: []*planv1.Wish{babysit, sidebar, notes},
		want: want{kinds: []planv1.RouteKind{made, file, file, file}, first: "Look at the pipelines of acme/shop",
			ids: []string{shop.GetId()}, filed: []*planv1.Wish{babysit, notes, sidebar}},
	}, {
		name:    "title words and a project named: that wish, never a granted one",
		request: "the lamp sidebar flickers",
		from:    babysit, wishes: []*planv1.Wish{babysit, sidebar, notes, colours},
		want: want{kinds: []planv1.RouteKind{file, file, made, file}, first: sidebar.GetId(), reason: "its title shares sidebar",
			filed: []*planv1.Wish{sidebar, notes, babysit}},
	}, {
		name:    "a title word and the latest blocks: proposed, not recommended",
		request: "write the changelog",
		from:    babysit, wishes: []*planv1.Wish{babysit, sidebar, notes},
		want: want{kinds: []planv1.RouteKind{made, file, file, file}, first: "Write the changelog", ids: babysit.GetProjectIds(),
			filed: []*planv1.Wish{notes, sidebar, babysit}},
	}, {
		name:    "nothing close: new wish recommended, other wishes still offered, then keep",
		request: "order more coffee beans",
		from:    babysit, wishes: []*planv1.Wish{babysit, sidebar, notes},
		want: want{kinds: []planv1.RouteKind{made, file, file, file}, first: "Order more coffee beans", reason: "no wish is close",
			ids: babysit.GetProjectIds(), filed: []*planv1.Wish{sidebar, notes, babysit}},
	}, {
		name:    "only one other wish scoring 0 is still offered",
		request: "order more coffee beans",
		from:    babysit, wishes: []*planv1.Wish{babysit, notes, colours},
		want: want{kinds: []planv1.RouteKind{made, file, file}, first: "Order more coffee beans", reason: "no wish is close",
			ids: babysit.GetProjectIds(), filed: []*planv1.Wish{notes, babysit}},
	}, {
		name:    "three active: wait paused, or pause the last one but the lead's own",
		request: "order more coffee beans",
		from:    oil, wishes: []*planv1.Wish{babysit, sidebar, oil},
		want: want{kinds: []planv1.RouteKind{planv1.RouteKind_ROUTE_KIND_QUEUE, planv1.RouteKind_ROUTE_KIND_SWAP, file, file},
			first: "Order more coffee beans", reason: "waits paused", ids: oil.GetProjectIds(), pause: sidebar.GetId(),
			filed: []*planv1.Wish{babysit, oil}},
	}} {
		t.Run(c.name, func(t *testing.T) {
			route := propose(routeInput{
				lang: "en", text: c.request, from: c.from, wishes: c.wishes, projects: projects, blocks: blocks,
			})
			var kinds []planv1.RouteKind
			var filed []string
			for _, o := range route.GetOptions() {
				kinds = append(kinds, o.GetKind())
				if o.GetWishId() == colours.GetId() {
					t.Errorf("proposed the granted %q", o.GetTitle())
				}
				if o.GetKind() != file {
					continue
				}
				filed = append(filed, o.GetWishId())
				switch {
				case o.GetWishId() == c.from.GetId():
					if !strings.Contains(o.GetReason(), "keep it if it belongs here") {
						t.Errorf("kept, reason %q", o.GetReason())
					}
				case o.GetScore() < routeShown:
					if !strings.HasPrefix(o.GetReason(), "an existing wish") {
						t.Errorf("far %q, reason %q", o.GetTitle(), o.GetReason())
					}
				}
			}
			if !slices.Equal(kinds, c.want.kinds) {
				t.Fatalf("kinds %v, want %v: %v", kinds, c.want.kinds, route)
			}
			var want []string
			for _, w := range c.want.filed {
				want = append(want, w.GetId())
			}
			if !slices.Equal(filed, want) {
				t.Errorf("filed in %v, want %v: %v", filed, want, route)
			}
			if last := route.GetOptions()[len(route.GetOptions())-1]; last.GetWishId() != c.from.GetId() {
				t.Errorf("the last option %v is not to keep it in the wish it came to", last)
			}
			first := route.GetOptions()[0]
			if got := first.GetWishId() + first.GetTitle(); first.GetKind() == file && first.GetWishId() != c.want.first ||
				first.GetKind() != file && first.GetTitle() != c.want.first {
				t.Errorf("first = %q, want %q", got, c.want.first)
			}
			if !strings.Contains(first.GetReason(), c.want.reason) {
				t.Errorf("reason %q, want it to hold %q", first.GetReason(), c.want.reason)
			}
			if first.GetKind() != file && !slices.Equal(first.GetProjectIds(), c.want.ids) {
				t.Errorf("projects %v, want %v", first.GetProjectIds(), c.want.ids)
			}
			if c.want.pause != "" && route.GetOptions()[1].GetPauseWishId() != c.want.pause {
				t.Errorf("pauses %s, want %s", route.GetOptions()[1].GetPauseWishId(), c.want.pause)
			}
		})
	}
}

func TestProposeTitle(t *testing.T) {
	for in, want := range map[string]string{
		"babysit shop!41. Then merge it":                     "Babysit shop!41",
		"  fix the\nflaky test  ":                            "Fix the",
		"why is the CI red? It was green":                    "Why is the CI red",
		strings.Repeat("lamp ", 30):                          "L" + strings.TrimSpace(strings.Repeat("lamp ", 16))[1:] + "…",
		"":                                                   "?",
		"\u00e9teindre la lampe avant de partir.":            "\u00c9teindre la lampe avant de partir",
		"https://example.com/a/b is down":                    "https://example.com/a/b is down",
		"a link [https://gitlab.com/x/y/-/merge_requests/3]": "A link [y!3]",
	} {
		short := readRequest(in, nil).short
		if got := proposeTitle(short); got != want {
			t.Errorf("proposeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// route asks Djinn where a request goes, as a question on the wish from.
func (c clients) route(t *testing.T, from, request string) *planv1.Question {
	t.Helper()
	res, err := c.wishes.Route(t.Context(), connect.NewRequest(&planv1.WishServiceRouteRequest{
		Request: request, WishId: from, Ask: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetQuestion()
}

// requests are the requests filed in a wish.
func (c clients) requests(t *testing.T, wishID string) []string {
	t.Helper()
	res, err := c.blocks.List(t.Context(), connect.NewRequest(&planv1.BlockServiceListRequest{WishId: wishID, Kind: "request"}))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, b := range res.Msg.GetBlocks() {
		out = append(out, b.GetContent())
	}
	return out
}

func TestRouteAnswered(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the lines are checked for the shells of Unix")
	}
	ctx := t.Context()
	home := t.TempDir()
	c, leads := serveLeads(t, home)
	shop := &planv1.Project{Id: store.NewID(), Name: "shop", Directory: t.TempDir(), Git: true,
		Remote: "git@gitlab.com:acme/shop.git"}
	lamp := &planv1.Project{Id: store.NewID(), Name: "lamp", Directory: t.TempDir(), Git: true,
		Remote: "https://github.com/acme/lamp.git"}
	c.put(t, shop, lamp)
	make := func(title string, p *planv1.Project) *planv1.Wish {
		t.Helper()
		res, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
			Title: title, ProjectIds: []string{p.GetId()},
		}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetWish()
	}
	here := make("Lamp: the sidebar", lamp)
	babysit := make("Babysit !37 · shop", shop)
	for id, s := range map[string]string{here.GetId(): session, babysit.GetId(): "7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"} {
		if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: id, SessionId: s}); err != nil {
			t.Fatal(err)
		}
	}

	// --ask needs the wish the request came to; without --ask, nothing is asked.
	_, err := c.wishes.Route(ctx, connect.NewRequest(&planv1.WishServiceRouteRequest{Request: "x", Ask: true}))
	if code(err) != connect.CodeInvalidArgument {
		t.Errorf("--ask without --wish-id: %v", err)
	}
	dry, err := c.wishes.Route(ctx, connect.NewRequest(&planv1.WishServiceRouteRequest{Request: "x", WishId: here.GetId()}))
	if err != nil || dry.Msg.GetQuestion() != nil || len(dry.Msg.GetRoute().GetOptions()) != 3 {
		t.Errorf("without --ask: %v, %v", dry, err)
	}

	// A request about another merge request: the card recommends a new wish in its project, offers the other wish and
	// to keep it here; rubbing the lamp makes the new wish, files the request there, and starts its lead on it in its
	// own terminal.
	request := "babysit https://gitlab.com/acme/shop/-/merge_requests/41"
	q := c.route(t, here.GetId(), request)
	if q.GetCode() != "Q01" || !slices.Equal(q.GetOptions(), []string{"New wish “Babysit shop!41”, in shop",
		"File it in “Babysit !37 · shop”", "Keep it in “Lamp: the sidebar”"}) || !strings.HasPrefix(q.GetRecommendation(), "A: ") ||
		!strings.Contains(q.GetContext(), "> "+request) || q.GetRoute().GetRequest() != request {
		t.Fatalf("question = %v", q)
	}
	if choice, ok := Recommended(q); !ok || choice != planv1.Choice_CHOICE_A {
		t.Errorf("recommended %v, %v", choice, ok)
	}
	opened := len(leads.opened)
	if _, err := c.marks.Put(ctx, connect.NewRequest(&planv1.MarkServicePutRequest{
		WishId: here.GetId(), Target: &planv1.MarkTarget{Ref: &planv1.MarkTarget_Code{Code: "Q01"}},
		Kind: planv1.MarkKind_MARK_KIND_APPROVED,
	})); err != nil {
		t.Fatal(err)
	}
	made := wishTitled(t, c, "Babysit shop!41")
	if made == nil || !Active(made) || !slices.Equal(made.GetProjectIds(), []string{shop.GetId()}) ||
		made.GetLead().GetSessionId() == "" || made.GetLead().GetDirectory() != shop.GetDirectory() {
		t.Fatalf("new wish = %v", made)
	}
	if got := c.requests(t, made.GetId()); !slices.Equal(got, []string{request}) {
		t.Errorf("requests filed = %q", got)
	}
	moving, err := os.ReadFile(filepath.Join(home, PagesDir, made.GetId(), LeadBriefFile))
	if err != nil || !strings.HasPrefix(string(moving), FirstLine(here.GetTitle(), request)+"\n\n# The wish: Babysit shop!41") {
		t.Errorf("the new lead's brief: %q, %v", moving, err)
	}
	// The new lead first; then the lead of the wish the request came to, reopened to be told.
	if len(leads.opened) != opened+2 || !strings.HasPrefix(leads.opened[opened], "claude --session-id "+made.GetLead().GetSessionId()) ||
		!strings.HasSuffix(leads.opened[opened], " in "+shop.GetDirectory()) ||
		!strings.HasPrefix(leads.opened[opened+1], "claude --resume "+session) {
		t.Errorf("opened %q", leads.opened[opened:])
	}
	if !slices.Contains(leads.shown, made.GetId()+"/"+LeadTerminal(made.GetId())) {
		t.Errorf("shown %q", leads.shown)
	}
	answered := c.questionOf(t, q.GetId())
	if answered.GetRoute().GetOptions()[0].GetWishId() != made.GetId() {
		t.Errorf("the decision does not say where the request went: %v", answered.GetRoute())
	}
	told := LeadTerminal(here.GetId()) + ": " + RoutedLine(answered)
	if !slices.Contains(leads.said, told) || !strings.Contains(told, `the new wish "Babysit shop!41"`) {
		t.Errorf("said %q, want %q", leads.said, told)
	}
	// Routed once: answering again would make a second wish.
	if _, err := c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q.GetId()}}, Choice: planv1.Choice_CHOICE_B,
	})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("answer again: %v", err)
	}

	// A request about the merge request a wish babysits: filed there, and its lead told.
	request = "the CI of https://gitlab.com/acme/shop/-/merge_requests/37 is red"
	q = c.route(t, here.GetId(), request)
	if q.GetRoute().GetOptions()[0].GetWishId() != babysit.GetId() {
		t.Fatalf("route = %v", q.GetRoute())
	}
	c.answer(t, q, planv1.Choice_CHOICE_A, "")
	if got := c.requests(t, babysit.GetId()); !slices.Equal(got, []string{request}) {
		t.Errorf("requests filed = %q", got)
	}
	if want := LeadTerminal(babysit.GetId()) + ": " + FiledLine(babysit.GetId(), here.GetTitle(), request); !slices.Contains(leads.said, want) {
		t.Errorf("said %q, want %q", leads.said, want)
	}

	// Three wishes are active: the card offers to wait paused, or to pause the last one, then a wish to file it in and
	// to keep it here; each does what it says.
	q = c.route(t, here.GetId(), "order more coffee beans")
	if len(q.GetOptions()) != 4 || !strings.Contains(q.GetOptions()[1], "pause “Babysit shop!41”") ||
		!strings.HasPrefix(q.GetOptions()[2], "File it in ") || q.GetOptions()[3] != "Keep it in “Lamp: the sidebar”" {
		t.Fatalf("options %q", q.GetOptions())
	}
	opened = len(leads.opened)
	c.answer(t, q, planv1.Choice_CHOICE_A, "")
	queued := wishTitled(t, c, "Order more coffee beans")
	if queued.GetState() != planv1.WishState_WISH_STATE_PAUSED || len(leads.opened) != opened ||
		!slices.Equal(c.requests(t, queued.GetId()), []string{"order more coffee beans"}) {
		t.Errorf("queued = %v, opened %q", queued, leads.opened[opened:])
	}
	q = c.route(t, here.GetId(), "polish the brass of the lamp")
	c.answer(t, q, planv1.Choice_CHOICE_B, "")
	if w := wishTitled(t, c, "Babysit shop!41"); w.GetState() != planv1.WishState_WISH_STATE_PAUSED {
		t.Errorf("not paused: %v", w)
	}
	if w := wishTitled(t, c, "Polish the brass of the lamp"); !Active(w) || len(leads.opened) != opened+1 {
		t.Errorf("swapped in = %v, opened %q", w, leads.opened[opened:])
	}
	if actives := Ranked(c.list(t)); len(actives) != MaxActive {
		t.Errorf("%d active", len(actives))
	}

	// The developer keeps the request in the wish it came to: filed there, no new wish, and its lead told to do it.
	request = "the sidebar also needs a dark mode"
	q = c.route(t, here.GetId(), request)
	keep := len(q.GetOptions()) - 1
	if q.GetOptions()[keep] != "Keep it in “Lamp: the sidebar”" {
		t.Fatalf("options %q", q.GetOptions())
	}
	wishes, said := len(c.list(t)), len(leads.said)
	c.answer(t, q, planv1.Choice_CHOICE_A+planv1.Choice(keep), "")
	if got := c.requests(t, here.GetId()); !slices.Equal(got, []string{request}) {
		t.Errorf("requests kept = %q", got)
	}
	if len(c.list(t)) != wishes {
		t.Errorf("a wish was made: %d, was %d", len(c.list(t)), wishes)
	}
	if filed := LeadTerminal(here.GetId()) + ": " + FiledLine(here.GetId(), here.GetTitle(), request); slices.Contains(leads.said, filed) {
		t.Errorf("said the request was filed: %q", leads.said[said:])
	}
	told = LeadTerminal(here.GetId()) + ": " + RoutedLine(c.questionOf(t, q.GetId()))
	if !slices.Equal(leads.said[said:], []string{told}) || !strings.Contains(told, "keeps the request in this wish") {
		t.Errorf("said %q, want %q", leads.said[said:], told)
	}
}

func wishTitled(t *testing.T, c clients, title string) *planv1.Wish {
	t.Helper()
	for _, w := range c.list(t) {
		if w.GetTitle() == title {
			return w
		}
	}
	t.Fatalf("no wish %q", title)
	return nil
}

func (c clients) questionOf(t *testing.T, id string) *planv1.Question {
	t.Helper()
	q, err := store.Get[*planv1.Question](t.Context(), c.store, id)
	if err != nil {
		t.Fatal(err)
	}
	return q
}
