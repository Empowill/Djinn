---
id: 01a12054-f8e9-7235-8f47-c54dd6fb2417
code: T28
phase: 3
status: open
---

# T28 · An inbox: what comes from outside becomes a proposed wish

The design's third part (design block "every request finds its wish", decision Q51 = B), after the routing
([T26](e1210c5e-request-routing.md)) and the wish templates ([T27](bac5e018-wish-templates.md)).

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

## To do

- A source: a watcher command per project or per machine that prints one item per paragraph (a link, a title, who),
  for instance `glab mr list --assignee=@me` wrapped to print only what is new. Sources declared like templates, in
  a skill's front matter?
- The inbox in the window: the items, newest first, each with its proposed route; dismiss, file, make.
- Dedup: an item already routed, or a link a wish already holds, is not proposed again.
- Tests with fake sources; an e2e from a fake source to a wish made at a click.

## Done when

- [ ] A source's new item becomes an inbox card with its proposed route, without a model.
- [ ] Nothing is made nor sent without a click; a test proves no source command writes.
- [ ] A dismissed or routed item is not proposed again.
- [ ] E2e: a fake source prints an assigned merge request, the card proposes the babysit template, a click makes the
  wish with its watcher.

## Open questions

- Where do sources live: a skill's front matter like templates, or Djinn's own settings, per machine?
- How often may a source run when no watcher command waits on its own (a polling period per source)?
