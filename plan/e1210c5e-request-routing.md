---
id: 01a1203c-1d89-752d-84fb-a2a1e1210c5e
code: T26
phase: 2
status: in-progress
after: T13
---

# T26 · Every request finds its wish

**Goal.** Every request finds its wish: routed to the right one, started from a wish template, or taken in from
outside as a proposed wish.

The developer's words: "When I ask you to babysit something, or anything that has nothing to do with the current wish,
offer to open a wish, dreaming a little further, or to file it in an existing one." Two babysits of merge requests
slipped into one wish and into its lead's session. Decision Q51 = B; the design is the wish's design block "every request
finds its wish". This task is its first part, the routing.

**What was decided.**
- **`WishService.Route`**, `djinn wish route "<request>" [--wish-id <wish>] [--ask] [--title …] [--project-id …]`.
  Djinn ranks the wishes without a model (`plan.propose`): a project the request links to (a GitLab or GitHub link
  whose repository is a project's remote, or the repository's path, `acme/shop`) or names, the words of the titles
  (a project's name counted once), the same merge request or issue (`!41`, `#12`, from the text or the link; another
  one weighs against), and a little for the words of the five latest blocks. A wish is proposed from 3, and
  recommended from 6; else Djinn recommends a new wish, its title made from the request (links shortened to
  `shop!41`), in the projects the request points to, else those of the wish it came to. Filing in an existing wish
  is always a choice: the wishes under 3 fill the places the close ones leave (`route.why_far`), and the wish it came
  to is proposed last, to keep the request there (`route.keep`); then its lead does the work. The granted ones are
  never proposed.
- **The card is a question**: `Question.route` (13) holds the `Route`, its options in the question's order, the
  recommended one first (`A: …`), so Rub the lamp takes it. The texts are the developer's language
  (`plan.WithLanguage`, `locales` `route.*`).
- **Answering acts in the same transaction** (`Questions.Settle`, `Marks.Settle`), each change journaled as the
  command that would make it: filing puts a `request` block in the wish, and tells its lead (W52's path); a new wish
  is made with its projects and the request block, its option records the new wish, and its lead starts from the
  brief in its own terminal (`lead-<wish>`), the request first (`plan.FirstLine`), shown in the window. The lead of
  the wish the request came to is told where it went (`plan.RoutedLine`). A route question answers once.
- **Three wishes at most**: when full, the card offers the new wish paused (`ROUTE_KIND_QUEUE`, no lead started) or
  to pause the last active wish but the lead's own (`ROUTE_KIND_SWAP`).
- **The brief's rules** tell the lead: a request that is not about this wish goes through
  `djinn wish route "<request>" --wish-id <wish> --ask`; hand it over, do not do its work.

## Done when

- [x] `djinn wish route` ranks the wishes and proposes where a request goes, without a model. (`TestRouteRanking`:
  a link to another merge request → a new wish in its project; the same merge request → that wish; a repository's
  path; title words and a project named, never a granted wish; the latest blocks; no match → a new wish; three
  active → queue or swap. `TestProposeTitle`)
- [x] `--ask` asks it as a question card on the current wish, the recommendation first, rubbable.
  (`TestRouteAnswered`: options, recommendation, `Recommended` = A)
- [x] Both answers act end to end with fake leads: a new wish made with its project, its request block, its lead
  started on the brief with the request first and shown, the current lead told; filing adds the block and tells
  that wish's lead; queue and swap keep three active; a second answer is refused. (`TestRouteAnswered`)
- [x] The brief's rules hand a request over through `djinn wish route --ask`. (`internal/plan/brief.go`, `briefRules`)
- [x] E2e: route a request, rub the lamp, see the new wish with its lead terminal. (`e2e/wish-route.spec.ts`)
- [x] The new lead reads the request once: on its first line, not again in its brief's latest blocks; a later
  `djinn wish brief` still lists the request block. (`TestLeadBriefOnce`, `plan.LeadBrief`; `TestRouteAnswered`)
- [x] Filing in an existing wish is always a choice: far wishes fill the places, the wish it came to is the last
  option and keeping it there tells its own lead to do the work, no filed line. (`TestRouteRanking`: nothing close
  still offers the other wishes then keep, one other wish scoring 0 is still offered; `TestRouteAnswered`: keep;
  `e2e/wish-route.spec.ts`)
- [ ] A real lead hands a request over by itself, from the brief's rule. (needs: a person and a real model)

## Next (the design's parts 2 and 3)

- Wish templates drawn from skills (babysit with a `watch` provider, Dew, QA): the section from T27 below.
- An inbox: what comes from outside (a mention, an assigned merge request) becomes a proposed wish: the section from
  T28 below.

## Open questions

- A new lead's provider follows the lead of the wish the request came to, else claude. Should the card offer it?

## From T27 · Wish templates, drawn from skills

The design's second part (design block "every request finds its wish", decision Q51 = B), after the routing
(above) and the watcher (`--provider watch`). A request that comes back, such as babysitting a pull request,
opens a wish that already knows how to work. How a project declares one: [`docs/wish-templates.md`](../docs/wish-templates.md).

**What was decided.**
- **A template is a skill with a few more lines**, in its `SKILL.md` front matter under `metadata.djinn.wish`:
  `title` and `match` (required), `watch`, `done_when`, `restart`. `match` is a Go regular expression whose named
  groups fill the `{name}` placeholders; a value is one quoted word of the watcher's command line, never more.
  Read with go-yaml (`go.yaml.in/yaml/v3`, MIT and Apache-2.0, already linked through protovalidate).
- **Routing proposes it** (`plan.propose`): the templates of the new wish's projects, own skills then summoned
  ones, the first match wins. The new wish's option carries `RouteOption.template`, its title from the template,
  "from the skill …" on the card. A wish on the same piece of work still takes the request (the bar to file rises
  to `routeRef + routeWord`).
- **The wish keeps it**: `Wish.template` (`WishTemplate`: skill, project, watch filled, done_when, restart), mapped
  on import like the projects. Answering the card makes the wish, then starts its watcher in the skill's project
  (`plan.WithWatchers`, `harness.SpawnWatcher`), then its lead, whose first line names the skill's `SKILL.md` and
  the watcher's code (or why it did not start, and the command to start it). The brief says it too.
- **The done line is a card, never a grant**: each new paragraph of a watcher reaches `plan.Wishes.Watched`
  (`harness.OnWatched`); a line that is `done_when`, or starts with it before a sign, asks `Question.grant`: A grants
  the wish in the answer's transaction (journaled as `djinn wish grant`), B keeps it open. Asked once while open.
  A watcher that restarts its command ends, done, on its done line.
- **Djinn ships two**: `.agents/skills/babysit-pr/` for GitHub, its `watch.sh` on `gh pr checks` and
  `gh pr view --json`; `.agents/skills/babysit-mr/` for GitLab, its `watch.sh` on `glab mr view --comments` and
  `glab ci get` (glab 1.100 or later, for `--jq`). Both print only on change, exit on the merge, and only read.
  Djinn's `.agents/permissions.txtpb` lists both.

### Done when

- [x] A skill's template parses, or says why it cannot be used, in `djinn skill list`. (`TestReadTemplate`,
  `TestSkillListTemplate`)
- [x] A request matches a template and fills its placeholders, one quoted word each. (`TestReadTemplate`,
  `TestFillQuotes`)
- [x] Routing proposes the template's wish; the same piece of work still files. (`TestRouteTemplate`)
- [x] The wish is made with its template, its watcher started in the skill's project, its lead on the skill.
  (`TestTemplateWish`, `TestTemplateWithoutWatchers`, `TestWatcherDoneLine`)
- [x] The done line asks once whether to grant; A grants, B keeps it open; a restarting watcher ends done.
  (`TestDoneLine`, `TestTemplateWish`, `TestWatcherDoneLine`, `TestWatcherFinishes`)
- [x] E2e with a fake watcher: route, rub the lamp, the watcher's MERGED card, rub the lamp, the wish is granted.
  (`e2e/wish-template.spec.ts`)
- [x] `babysit-pr` for GitHub, and the docs of a GitLab one. (`.agents/skills/babysit-pr/`, run by hand on a public
  pull request open then merged: a summary, then `MERGED` and exit 0; `docs/wish-templates.md`)
- [x] `babysit-mr` for GitLab: its template matches a merge request's link or `!12`, not a pull request; its watcher,
  under Djinn's permissions, prints each change of the pipeline's jobs, merge status and comments, `MERGED` on the
  merge and exits, and only reads. (`TestBabysitMRReadsOnly`, a fake `glab` on the `PATH`; `docs/wish-templates.md`)
- [ ] `babysit-mr` run by hand on a real merge request open then merged. (needs: a person and a GitLab project)
- [x] The brief's rules tell a lead to spawn a watcher (`--provider watch`, `--restart`) instead of polling, and to
  propose a template (`metadata.djinn.wish`) for a request that comes back. (`TestBrief`, `briefRules`;
  `docs/agent-protocol.md`)
- [ ] A real lead babysits a real pull request of Djinn to its merge, from the template. (needs: a person and a real
  model)

### Next

- More templates: QA of a feature, a queue of tickets (one task per ticket). Each is a skill, no code.
- The inbox, the design's third part: the section from T28 below.

### Open questions

- The Agent Skills format describes `metadata` as a map of strings; `metadata.djinn.wish` nests a map. Claude Code
  reads it; a strict validator may not. A flat form (`djinn.wish.title: …`) would satisfy both, at the cost of
  readability. Keep the nested form until a tool refuses it?

## From T28 · An inbox: what comes from outside becomes a proposed wish

The design's third part (design block "every request finds its wish", decision Q51 = B), after the routing
(above) and the wish templates (the section from T27, above). How a project
declares a source: [`docs/wish-templates.md`](../docs/wish-templates.md#the-inbox-what-comes-from-outside).

What comes from outside becomes a wish proposed, waiting for your click: a merge request assigned to you, a mention
in a GitLab or GitHub thread, a Slack thread that names you, a Notion ticket passed to you. Each one is routed as a
request is: filed in a wish, or a new one, from a template when one matches (an assigned merge request → "Babysit
!12").

**The rules, from the start.**
- **Nothing is made without a click.** An item is a card in the inbox; only your answer files it or makes the wish.
- **Nothing is sent out.** Djinn reads; it never comments, reacts, marks as read nor replies on your behalf.
- **Djinn reads only what you plugged in**, and holds no secret: a source is a command you already use, logged in
  by its own tool (`glab`, `gh`, a Slack or Notion CLI), run as a watcher. Djinn never stores its token. Plugged in is
  about the person, not the repository: a source runs only once plugged in on this machine.
- **Lamp and smoke.** An inbox item is lamp only if code computes on it (its source, its state: new, routed,
  dismissed); its text is smoke.

**What was decided.**
- **Sources live where templates do**: a skill's `SKILL.md`, under `metadata.djinn.source` next to `wish`: `watch`
  (the command line, no placeholder) and `every` (5 minutes by default, 1 minute at least). A source is per project,
  as a skill is; a skill summoned by several projects runs once. Nothing in the code argued for Djinn's own settings:
  a source and the template its items match travel together, and the project's `.agents/permissions.txtpb` already
  says which commands may run without review. `djinn skill list` shows the command, or why it cannot be used.
- **A source runs as a watcher** (`harness.RunSources`, `internal/harness/watch_source.go`): the `Watch` provider's
  loop, without a task nor a wish, no slot, its input closed (`Watch.Items`: an empty line ends an item too). It
  starts again `every` after each start; a failing one waits at least as long. The skills are read again on each
  change of a project and every minute.
- **An item** (`InboxItem`, lamp: source, project, key, state, route, wish; smoke: its text) is made by
  `plan.Inbox.Receive`: its route is `plan.propose`'s, the new wish's project being the source's when the item links
  to none, so the source's template matches. Its key is its first link, else its first line, unique: an item already
  seen (new, routed, dismissed) or whose link a wish's title or block holds is not proposed again.
- **A source is plugged in per machine** (W76's open question, answer B): a `PluggedSource` (the project that holds
  the skill, the skill) in Djinn's data, never in the repository; none by default. `harness.RunSources` runs only the
  plugged ones (`plan.Sources`); a plug or an unplug wakes it, and an unplugged source's watcher stops. A source is
  named `<project>/<skill>`, or by its skill alone when one project holds it. `djinn inbox sources` lists the declared
  ones (plugged or not, or why they cannot run, a plugged one whose skill is gone included), `djinn inbox plug
  <source>`, `djinn inbox unplug <source>`. The empty inbox lists them with "Plug in" / "Unplug"; with items, they
  fold under the cards. A project added or a source plugged tells the window `CHANGE_INBOX`.
- **Djinn's own source**: `.agents/skills/babysit-pr/inbox.sh`, read only (`gh pr list --search assignee:@me`, then
  `review-requested:@me`), one paragraph per pull request with its link; an assigned one reads "Babysit PR #n", so
  the inbox proposes the skill's wish. Unplugged by default, like every source.
- **`InboxService`**: `djinn inbox list [--all]`, `djinn inbox dismiss <item>`, `djinn inbox route <item> <letter>`.
  Routing acts as answering a route question (`Wishes.routeTo`, shared with T26's `settle`): a request block, the new
  wish with its template, its watcher, then its lead (`plan.InboxFirstLine`), or the lead of the wish it is filed in
  told (`plan.InboxFiledLine`). `Change.CHANGE_INBOX` tells the window.
- **The window**: the inbox heads the flight plan (`src/inbox.tsx`), newest first, each card with its source, its
  options (the recommended first), "Rub the lamp" and "Dismiss". Without items, it lists the declared sources; without
  sources either, it is hidden.
- **Outside the flight plan**: the side panel's flight plan entry counts the new items (an inbox icon beside the
  questions' count), and shows while one waits even with no active wish. A new item is a system notification
  (`ui.Notices`, as a question's): its source as title, its text as body, no button; a click brings the window
  forward. An item dismissed, routed or older than a minute is not news.

### Done when

- [x] A source's new item becomes an inbox card with its proposed route, without a model. (`TestInbox`: an assigned
  merge request proposes the babysit template's wish in the source's project, a mention about a wish proposes filing
  it there; `TestSources`, `TestReadSource`; `tests/screens.test.mjs` "the inbox shows each item with its proposed
  route")
- [x] Nothing is made nor sent without a click; a test proves no source command writes. (`TestSourceReadsOnly`: the
  source ran once, read 0 bytes of input, and neither a dismissal nor a routing ran a command or changed the project's
  folder; `TestSourceRefused`: a command the project does not list never runs; `TestInbox`: no wish before the click)
- [x] A dismissed or routed item is not proposed again. (`TestInbox`: the same link said otherwise, a dismissed item,
  a routed one, a link a wish's block holds; `TestItemKey`)
- [x] E2e: a fake source prints an assigned merge request, the card proposes the babysit template, a click makes the
  wish with its watcher. (`e2e/inbox.spec.ts`)
- [x] The inbox shows outside the flight plan: a count in the side panel, a notification for a new item.
  (`TestNoticesShowNewInboxItems`: a new item is notified once, with its source and its text; a dismissed, routed or
  old one is not; `tests/screens.test.mjs` "the side panel counts the new inbox items on the flight plan's entry,
  even with no active wish")
- [x] A source runs only once plugged in, per machine; an unplugged one never starts a command. (`TestSourceUnplugged`:
  no watcher and 0 commands before the plug, 1 after and no other over two more passes, its watcher stopped by the
  unplug and a new one on the next plug, each pass awaited, no sleep; `TestSourceRefused`: plugged in, a command the
  project does not list still never runs; `TestSources`: plug, unplug, a name held by two projects, a source that
  cannot run, a plugged one whose skill is gone; `TestWatchSaysPlugged`: a plug and an unplug tell the window
  `CHANGE_INBOX`; `TestInboxPlug`: `djinn inbox sources/plug/unplug` against a real `djinn up`, the item come and one
  run; `tests/data-store.test.mjs` "a change of the inbox reads it and its sources again, and nothing else";
  `tests/screens.test.mjs` "the empty inbox lists the sources the skills declare, each with Plug in or Unplug;
  without any, it is hidden"; `e2e/inbox.spec.ts`: the fake source is not run until "Plug in", then its card comes)
- [x] Djinn's own `babysit-pr` ships a read-only `gh` source, unplugged by default. (`TestOwnSource`: with a fake `gh`,
  only two `gh pr list` calls; an assigned pull request proposes "Babysit PR #12", one to review proposes no template;
  `TestRepoPermissions`)
- [ ] A real source (`glab mr list --assignee=@me`, wrapped) brings a real merge request to a babysit wish. (needs: a
  person, logged in to a real forge)

### Next

- A click on an item's notification raises the window as it is: showing the flight plan would need the window's show
  request (`UiServiceWatchShowResponse`) to name it.
