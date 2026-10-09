---
id: 01a12054-f8e9-7235-8f47-c54dd6fb2417
code: T28
phase: 3
status: in-progress
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
  by its own tool (`glab`, `gh`, a Slack or Notion CLI), run as a watcher. Djinn never stores its token.
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
- **`InboxService`**: `djinn inbox list [--all]`, `djinn inbox dismiss <item>`, `djinn inbox route <item> <letter>`.
  Routing acts as answering a route question (`Wishes.routeTo`, shared with T26's `settle`): a request block, the new
  wish with its template, its watcher, then its lead (`plan.InboxFirstLine`), or the lead of the wish it is filed in
  told (`plan.InboxFiledLine`). `Change.CHANGE_INBOX` tells the window.
- **The window**: the inbox heads the flight plan (`src/inbox.tsx`), newest first, each card with its source, its
  options (the recommended first), "Rub the lamp" and "Dismiss". Empty, it is hidden.

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
- [ ] A real source (`glab mr list --assignee=@me`, wrapped) brings a real merge request to a babysit wish. (needs: a
  person, logged in to a real forge)

## Next

- A `source` for Djinn's own `babysit-pr`, on `gh pr list --assignee @me`: it would poll GitHub for whoever opens
  Djinn's repository, so it waits for the developer's say.
- The inbox outside the flight plan: a count in the side panel, a notification for a new item.

## Open questions

- Should a source be opt-in per machine (a project's skill could otherwise start polling a forge as soon as the
  project is added)? Today the project's `.agents/permissions.txtpb` is the gate, when it lists commands.
