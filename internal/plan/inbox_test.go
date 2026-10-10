package plan

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// babysitMRFront is a GitLab babysit skill with an inbox source: the merge requests assigned to you.
const babysitMRFront = `metadata:
  djinn:
    wish:
      title: "Babysit !{mr}"
      match: '(?i)\bbabysit\w*\b.*?(?:/merge_requests/|!)(?P<mr>\d+)'
      watch: "mrwatch -mr {mr}"
      done_when: MERGED
    source:
      watch: "sh assigned.sh"
      every: 10m
`

// TestReadSource: a skill's source reads with its period, five minutes by default; one that cannot be used says
// why, in djinn skill list too.
func TestReadSource(t *testing.T) {
	dir := t.TempDir()
	src, err := ReadSource(agentSkill(t, dir, "babysit-mr", babysitMRFront))
	if err != nil || src == nil || src.Skill != "babysit-mr" || src.Watch != "sh assigned.sh" || src.Every != 10*time.Minute {
		t.Fatalf("source = %+v, %v", src, err)
	}
	src, err = ReadSource(agentSkill(t, dir, "mentions", "metadata:\n  djinn:\n    source:\n      watch: mentions --new\n"))
	if err != nil || src.Every != SourceEvery {
		t.Errorf("default period: %+v, %v", src, err)
	}
	for name, front := range map[string]string{"plain": "", "wish-only": babysitFront} {
		if src, err := ReadSource(agentSkill(t, dir, name, front)); src != nil || err != nil {
			t.Errorf("%s: %v, %v", name, src, err)
		}
	}
	for name, c := range map[string]struct{ front, want string }{
		"no-watch":    {"metadata:\n  djinn:\n    source:\n      every: 5m\n", "no watch"},
		"too-often":   {"metadata:\n  djinn:\n    source:\n      watch: x\n      every: 10s\n", "too often"},
		"bad-period":  {"metadata:\n  djinn:\n    source:\n      watch: x\n      every: soon\n", "every"},
		"placeholder": {"metadata:\n  djinn:\n    source:\n      watch: x {mr}\n", "no placeholder"},
	} {
		src, err := ReadSource(agentSkill(t, dir, name, c.front))
		if src != nil || err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, %v; want an error about %q", name, src, err, c.want)
		}
	}

	c := serve(t)
	gong := &planv1.Project{Id: store.NewID(), Name: "gong", Directory: dir}
	c.put(t, gong)
	res, err := c.skills.List(t.Context(), connect.NewRequest(&planv1.SkillServiceListRequest{Project: "gong"}))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range res.Msg.GetSkills() {
		got[s.GetName()] = s.GetInboxSource() + "|" + s.GetInboxSourceError()
	}
	if got["babysit-mr"] != "sh assigned.sh|" || !strings.HasPrefix(got["too-often"], "|metadata.djinn.source.every") {
		t.Errorf("skills = %q", got)
	}
}

// TestSources: the sources of the skills the projects use, their own and summoned ones, each skill once. None runs
// until it is plugged in on this machine; a source is named <project>/<skill>, or by its skill alone when one
// project holds it. Unplugged, it runs no more; one plugged in whose skill is gone is listed, to be unplugged.
func TestSources(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	gong := &planv1.Project{Id: store.NewID(), Name: "gong", Directory: t.TempDir()}
	bell := &planv1.Project{Id: store.NewID(), Name: "bell", Directory: t.TempDir(),
		Summons: []*planv1.Summon{{ProjectId: gong.GetId(), Skill: "babysit-mr"}}}
	lamp := &planv1.Project{Id: store.NewID(), Name: "lamp", Directory: t.TempDir()}
	agentSkill(t, gong.GetDirectory(), "babysit-mr", babysitMRFront)
	agentSkill(t, bell.GetDirectory(), "mentions", "metadata:\n  djinn:\n    source:\n      watch: mentions --new\n")
	agentSkill(t, lamp.GetDirectory(), "mentions", "metadata:\n  djinn:\n    source:\n      watch: mentions --all\n")
	agentSkill(t, lamp.GetDirectory(), "noisy", "metadata:\n  djinn:\n    source:\n      watch: x\n      every: 10s\n")
	c.put(t, gong, bell, lamp)
	running := func() []string {
		t.Helper()
		srcs, err := Sources(ctx, c.store, []*planv1.Project{bell, gong, lamp})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, s := range srcs {
			got = append(got, s.Skill+"@"+s.ProjectID)
		}
		return got
	}
	listed := func() []string {
		t.Helper()
		res, err := c.inbox.Sources(ctx, connect.NewRequest(&planv1.InboxServiceSourcesRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, s := range res.Msg.GetSources() {
			got = append(got, fmt.Sprintf("%s %t %s|%s", s.GetName(), s.GetPlugged(), s.GetWatch(), s.GetError()))
		}
		return got
	}
	plug := func(name string) error {
		_, err := c.inbox.Plug(ctx, connect.NewRequest(&planv1.InboxServicePlugRequest{Source: name}))
		return err
	}

	if got := running(); len(got) != 0 {
		t.Errorf("sources not plugged in run: %q", got)
	}
	want := []string{
		"bell/mentions false mentions --new|",
		"gong/babysit-mr false sh assigned.sh|",
		"lamp/mentions false mentions --all|",
		"lamp/noisy false |metadata.djinn.source.every: 10s is too often, a source runs at most every 1m0s",
	}
	if got := listed(); !slices.Equal(got, want) {
		t.Errorf("listed %q, want %q", got, want)
	}
	for name, want := range map[string]connect.Code{
		"mentions":   connect.CodeInvalidArgument, // Two projects hold one.
		"bell/bells": connect.CodeNotFound,
		"lamp/noisy": connect.CodeFailedPrecondition,
	} {
		if err := plug(name); code(err) != want {
			t.Errorf("plug %s: %v, want %v", name, err, want)
		}
	}
	for _, name := range []string{"BELL/Mentions", "babysit-mr", "babysit-mr"} {
		if err := plug(name); err != nil {
			t.Fatalf("plug %s: %v", name, err)
		}
	}
	if got, want := running(), []string{"mentions@" + bell.GetId(), "babysit-mr@" + bell.GetId()}; !slices.Equal(got, want) {
		t.Errorf("running %q, want %q", got, want)
	}
	plugged, err := store.List[*planv1.PluggedSource](ctx, c.store, nil)
	if err != nil || len(plugged) != 2 || plugged[1].GetProjectId() != gong.GetId() || plugged[1].GetSkill() != "babysit-mr" {
		t.Errorf("plugged = %v, %v", plugged, err)
	}

	res, err := c.inbox.Unplug(ctx, connect.NewRequest(&planv1.InboxServiceUnplugRequest{Source: "gong/babysit-mr"}))
	if err != nil || res.Msg.GetSource().GetPlugged() {
		t.Fatalf("unplug: %v, %v", res, err)
	}
	if got, want := running(), []string{"mentions@" + bell.GetId()}; !slices.Equal(got, want) {
		t.Errorf("running %q after unplugging, want %q", got, want)
	}

	// The skill is gone: its source runs nothing, and is listed so it can be unplugged.
	if err := os.RemoveAll(filepath.Join(bell.GetDirectory(), ".agents", "skills", "mentions")); err != nil {
		t.Fatal(err)
	}
	if got := running(); len(got) != 0 {
		t.Errorf("running %q, want none", got)
	}
	if got := listed(); !slices.Contains(got, "bell/mentions true |"+
		"no skill of the projects declares this source on this machine any more: it runs nothing") {
		t.Errorf("listed %q", got)
	}
	if _, err := c.inbox.Unplug(ctx, connect.NewRequest(&planv1.InboxServiceUnplugRequest{Source: "bell/mentions"})); err != nil {
		t.Fatal(err)
	}
	if plugged, err := store.List[*planv1.PluggedSource](ctx, c.store, nil); err != nil || len(plugged) != 0 {
		t.Errorf("plugged = %v, %v", plugged, err)
	}
}

// TestOwnSource: Djinn's own babysit-pr declares a source that reads with gh, and changes nothing: run with a fake
// gh, it lists the pull requests assigned to the developer and those that request their review, read only. An assigned
// one proposes the skill's wish; one to review proposes none of the skill.
func TestOwnSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the source and the fake gh are shell scripts")
	}
	ctx := t.Context()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	src, err := ReadSource(filepath.Join(root, ".agents", "skills", "babysit-pr"))
	if err != nil || src == nil || src.Watch != "sh .agents/skills/babysit-pr/inbox.sh" || src.Every != SourceEvery {
		t.Fatalf("source = %+v, %v", src, err)
	}

	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	fake := `#!/bin/sh
printf '%s\n' "$*" >> "$GH_CALLS"
case "$*" in
*assignee:@me*) printf 'Babysit PR #12: Fix the wick (assigned to you)\nhttps://github.com/acme/lamp/pull/12\n\n' ;;
*review-requested:@me*) printf 'Review PR #14: Polish the brass (your review is requested)\nhttps://github.com/acme/lamp/pull/14\n\n' ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "sh", filepath.Join(root, ".agents", "skills", "babysit-pr", "inbox.sh"))
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "GH_CALLS="+calls)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("inbox.sh: %v\n%s", err, out)
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "pr list --search assignee:@me --json number,title,url --jq ") ||
		!strings.HasPrefix(lines[1], "pr list --search review-requested:@me --json number,title,url --jq ") {
		t.Errorf("gh ran %q; want two pr lists, read only", lines)
	}

	c := serve(t)
	djinn := &planv1.Project{Id: store.NewID(), Name: "djinn", Directory: root, Git: true}
	c.put(t, djinn)
	srcs, err := declaredSources(ctx, c.store, []*planv1.Project{djinn})
	if err != nil || len(srcs) != 1 || srcs[0].Name() != "djinn/babysit-pr" || srcs[0].Plugged {
		t.Fatalf("sources = %v, %v", srcs, err)
	}
	inbox := &Inbox{Wishes: &Wishes{Store: c.store, Language: "en"}}
	var got []string
	for _, para := range strings.Split(strings.TrimSpace(string(out)), "\n\n") {
		item, err := inbox.Receive(ctx, srcs[0], para)
		if err != nil || item == nil {
			t.Fatalf("receive %q: %v, %v", para, item, err)
		}
		opt := item.GetRoute().GetOptions()[0]
		got = append(got, item.GetKey()+" → "+opt.GetTitle()+" "+opt.GetTemplate().GetSkill())
	}
	if want := []string{
		"https://github.com/acme/lamp/pull/12 → Babysit PR #12 babysit-pr",
		"https://github.com/acme/lamp/pull/14 → Review PR #14: Polish the brass (your review is requested) ",
	}; !slices.Equal(got, want) {
		t.Errorf("items %q, want %q", got, want)
	}
}

// TestInbox: a source's item becomes an inbox card with its proposed route, without a model, and nothing is made
// until an answer. The same item, or one whose link a wish holds, is not proposed again; a dismissed or routed item
// neither. Routing it makes the template's wish with its watcher and lead, or files it and tells the lead.
func TestInbox(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the lines are checked for the shells of Unix")
	}
	ctx := t.Context()
	home := t.TempDir()
	watchers := &spawned{}
	c, leads := serveLeads(t, home, WithWatchers(watchers.spawn))
	gong := &planv1.Project{Id: store.NewID(), Name: "gong", Directory: t.TempDir(), Git: true,
		Remote: "https://gitlab.example.com/acme/gong.git"}
	agentSkill(t, gong.GetDirectory(), "babysit-mr", babysitMRFront)
	c.put(t, gong)
	res, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Docs wording", ProjectIds: []string{gong.GetId()}}))
	if err != nil {
		t.Fatal(err)
	}
	docs := res.Msg.GetWish()
	if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: docs.GetId(), SessionId: session}); err != nil {
		t.Fatal(err)
	}
	srcs, err := declaredSources(ctx, c.store, []*planv1.Project{gong})
	if err != nil || len(srcs) != 1 {
		t.Fatalf("sources = %v, %v", srcs, err)
	}
	src := srcs[0]
	inbox := &Inbox{Wishes: &Wishes{Store: c.store, Language: "en"}}
	wishes := func() int {
		t.Helper()
		return len(c.list(t))
	}

	link := "https://gitlab.example.com/acme/gong/-/merge_requests/12"
	mr := "Babysit !12 · Fix the lamp's wick, assigned by alice\n" + link
	item, err := inbox.Receive(ctx, src, mr)
	if err != nil || item == nil {
		t.Fatalf("receive: %v, %v", item, err)
	}
	opt := item.GetRoute().GetOptions()[0]
	if item.GetState() != planv1.InboxState_INBOX_STATE_NEW || item.GetSource() != "babysit-mr" || item.GetKey() != link ||
		item.GetProjectId() != gong.GetId() || opt.GetKind() != planv1.RouteKind_ROUTE_KIND_NEW ||
		opt.GetTitle() != "Babysit !12" || opt.GetTemplate().GetWatch() != "mrwatch -mr 12" ||
		!slices.Equal(opt.GetProjectIds(), []string{gong.GetId()}) {
		t.Fatalf("item = %v", item)
	}
	// The same merge request again, said otherwise, and an empty paragraph: nothing new.
	for _, text := range []string{mr, "Babysit !12, still assigned\n" + link + ".", "  "} {
		if again, err := inbox.Receive(ctx, src, text); again != nil || err != nil {
			t.Errorf("%q: %v, %v", text, again, err)
		}
	}
	mention := "bob: could you check the docs wording of the lamp?\nhttps://chat.example.com/t/77"
	filed, err := inbox.Receive(ctx, src, mention)
	if err != nil || filed.GetRoute().GetOptions()[0].GetWishId() != docs.GetId() {
		t.Fatalf("a mention about a wish: %v, %v", filed, err)
	}
	dismissed, err := inbox.Receive(ctx, src, "carol mentioned you: lunch?")
	if err != nil || dismissed == nil {
		t.Fatalf("receive: %v, %v", dismissed, err)
	}
	if n := wishes(); n != 1 {
		t.Errorf("an item made a wish without a click: %d wishes", n)
	}

	list, err := c.inbox.List(ctx, connect.NewRequest(&planv1.InboxServiceListRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, i := range list.Msg.GetItems() {
		ids = append(ids, i.GetId())
	}
	if want := []string{dismissed.GetId(), filed.GetId(), item.GetId()}; !slices.Equal(ids, want) {
		t.Errorf("listed %q, want the newest first %q", ids, want)
	}

	// Dismissed: not proposed again, nor listed, and it cannot be routed.
	if _, err := c.inbox.Dismiss(ctx, connect.NewRequest(&planv1.InboxServiceDismissRequest{ItemId: dismissed.GetId()})); err != nil {
		t.Fatal(err)
	}
	if again, err := inbox.Receive(ctx, src, "carol mentioned you: lunch?"); again != nil || err != nil {
		t.Errorf("a dismissed item came again: %v, %v", again, err)
	}
	if _, err := c.inbox.Route(ctx, connect.NewRequest(&planv1.InboxServiceRouteRequest{
		ItemId: dismissed.GetId(), Choice: planv1.Choice_CHOICE_A,
	})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("route a dismissed item: %v", err)
	}

	// A click on the template's option: the wish, its watcher, its lead on the item.
	if _, err := c.inbox.Route(ctx, connect.NewRequest(&planv1.InboxServiceRouteRequest{
		ItemId: item.GetId(), Choice: planv1.Choice_CHOICE_D,
	})); code(err) != connect.CodeInvalidArgument {
		t.Errorf("a letter without a destination: %v", err)
	}
	opened := len(leads.opened)
	routed, err := c.inbox.Route(ctx, connect.NewRequest(&planv1.InboxServiceRouteRequest{ItemId: item.GetId(), Choice: planv1.Choice_CHOICE_A}))
	if err != nil {
		t.Fatal(err)
	}
	made := wishTitled(t, c, "Babysit !12")
	if got := routed.Msg.GetItem(); got.GetState() != planv1.InboxState_INBOX_STATE_ROUTED || got.GetWishId() != made.GetId() {
		t.Errorf("routed = %v", got)
	}
	if made.GetTemplate().GetSkill() != "babysit-mr" || !slices.Equal(c.requests(t, made.GetId()), []string{mr}) {
		t.Errorf("made = %v, requests %q", made, c.requests(t, made.GetId()))
	}
	if want := []string{made.GetId() + " | " + gong.GetId() + " | Watch, for the skill babysit-mr | mrwatch -mr 12"}; !slices.Equal(watchers.calls, want) {
		t.Errorf("watchers %q, want %q", watchers.calls, want)
	}
	if len(leads.opened) <= opened {
		t.Errorf("no lead started")
	}
	first, err := os.ReadFile(filepath.Join(home, PagesDir, made.GetId(), LeadFirstFile))
	if err != nil || !strings.HasPrefix(string(first), InboxFirstLine("babysit-mr", mr)+" This wish follows the skill babysit-mr") {
		t.Errorf("the lead's first line:\n%s", first)
	}
	if _, err := c.inbox.Route(ctx, connect.NewRequest(&planv1.InboxServiceRouteRequest{
		ItemId: item.GetId(), Choice: planv1.Choice_CHOICE_A,
	})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("route twice: %v", err)
	}

	// Filed in the wish it is about: its block, and its lead told.
	if _, err := c.inbox.Route(ctx, connect.NewRequest(&planv1.InboxServiceRouteRequest{ItemId: filed.GetId(), Choice: planv1.Choice_CHOICE_A})); err != nil {
		t.Fatal(err)
	}
	if got := c.requests(t, docs.GetId()); !slices.Equal(got, []string{mention}) {
		t.Errorf("requests filed = %q", got)
	}
	if want := LeadTerminal(docs.GetId()) + ": " + InboxFiledLine(docs.GetId(), "babysit-mr", mention); !slices.Contains(leads.said, want) {
		t.Errorf("said %q, want %q", leads.said, want)
	}

	// Nothing waits any more; --all lists every item. A link a wish holds is not proposed.
	list, err = c.inbox.List(ctx, connect.NewRequest(&planv1.InboxServiceListRequest{}))
	if err != nil || len(list.Msg.GetItems()) != 0 {
		t.Errorf("new items = %v, %v", list.Msg.GetItems(), err)
	}
	list, err = c.inbox.List(ctx, connect.NewRequest(&planv1.InboxServiceListRequest{All: true}))
	if err != nil || len(list.Msg.GetItems()) != 3 {
		t.Errorf("every item = %v, %v", list.Msg.GetItems(), err)
	}
	if _, err := c.blocks.Put(ctx, connect.NewRequest(&planv1.BlockServicePutRequest{
		WishId: docs.GetId(), Kind: "notes", Content: "Next: https://gitlab.example.com/acme/gong/-/merge_requests/13",
	})); err != nil {
		t.Fatal(err)
	}
	if again, err := inbox.Receive(ctx, src, "Babysit !13\nhttps://gitlab.example.com/acme/gong/-/merge_requests/13"); again != nil || err != nil {
		t.Errorf("a link a wish holds: %v, %v", again, err)
	}
}

func TestItemKey(t *testing.T) {
	for text, want := range map[string]string{
		"Babysit !12\nhttps://gitlab.example.com/acme/gong/-/merge_requests/12.": "https://gitlab.example.com/acme/gong/-/merge_requests/12",
		"  carol   mentioned you\nlunch?":                                        "carol mentioned you",
	} {
		if got := itemKey(text); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
}
