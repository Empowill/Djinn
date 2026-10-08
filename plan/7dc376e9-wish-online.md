---
id: 01a118f6-c07d-7d18-8418-4cf67dc376e9
code: T24
phase: 3
status: open
---

# T24 · A wish online: sync now, collaborate later

**Goal.** A wish has a page anyone can open, kept up to date as the wish moves. Today, for
hand-offs: one person works, the other reads. Later, several people work on the same wish.

## Decided
- **The word is `sync`.** `djinn wish sync <wish>`. One way today (Djinn to the page), both ways
  once people collaborate.
- **Djinn renders, the model does not.** Djinn writes the page from its store, in Go, on every
  change of the wish: no token spent. Empty sections are not rendered (the lamp and the smoke).
- **The lead only publishes.** When Djinn's lead is Claude, it republishes the rendered file as
  an artifact at the same address, in one tool call, without reading the page. With another
  agent, the page is a file to share.
- **Nothing leaves without a go.** The first publish of a wish asks; then every sync goes to
  the same page. No secret, no local path on the page.

## Later: collaborate on a wish
- Several people on one wish, even on one branch: answers and comments from the page come back
  into the wish, through the lead, never by editing the page.
- Not today: we hand off, we do not work at the same time.

## Done when
- [ ] `djinn wish sync` renders the page in Go, and the lead republishes it in one call.
- [ ] A change in the wish updates the page without the model writing HTML.
- [ ] A hand-off: the other person opens the link and sees the wish as it is.

## Open questions
- Can Djinn publish without a Claude session (an API for artifacts)? Not known today.
- How a comment on the page becomes an answer in the wish, safely.
