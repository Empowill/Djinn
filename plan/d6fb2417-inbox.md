---
id: 01a12054-f8e9-7235-8f47-c54dd6fb2417
code: T28
phase: 3
status: in-progress
after: T26 T27
---

# T28 · An inbox: what comes from outside becomes a proposed wish

The design's third part (design block "every request finds its wish", decision Q51 = B), after the routing
([T26](e1210c5e-request-routing.md)) and the wish templates ([T27](bac5e018-wish-templates.md)). How a project
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

## Done when

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

## Next

- A click on an item's notification raises the window as it is: showing the flight plan would need the window's show
  request (`UiServiceWatchShowResponse`) to name it.

