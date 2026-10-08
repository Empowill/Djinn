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

## Decided along the way
- **One file, in the data folder.** `djinn wish sync` writes `<data>/wishes/<id>/page.html` and prints its path. A
  page is synced while its file exists: `djinn up` takes back the pages it finds when it starts, and deleting the
  file stops it. No new state in the store. `djinn wish render` writes the page once, to Downloads or `--file`.
- **A commit hook, not a poll.** The store calls back after each commit with the entities it changed; the pages
  mark the synced wishes they touch, and render them at most once a second.
- **Order of the page:** what matters now first. Header with contents (pills naming only the sections present), to
  decide (open questions; a question a waiting task needs is red, open and first; the others folded, their
  recommendation in sight), waiting for you, running now (the workers running), tasks (running, waiting, failed in
  clear, with their dependencies and why a planned one waits; planned and finished folded), decisions (the latest
  first, a compact table; past 15, folded), notes (the blocks in their order, each under its title; a long block or
  a run of more than three of one kind folded), journal (commands and `log` blocks, the latest first; past 30,
  folded). Empty sections are not rendered.
- **"Waiting for you" comes from the lamp only:** a task waiting on its edit question, a task cut short by a stop,
  a project not on this machine. No block kind is read as an action.
- **Same scrubbing as the export,** plus the data folder (`djinn-data`). Tool calls and results stay out.
- **Markdown by goldmark (MIT), raw HTML left out**, dangerous links emptied. A block of another media type shows
  as text. No script, no external font: the page opens offline.
- **The page's texts are translated** (`page.*` keys), in the language of the user's locale.

## Done when
- [ ] `djinn wish sync` renders the page in Go (done), and the lead republishes it in one call (its instructions: next).
- [x] A change in the wish updates the page without the model writing HTML.
- [ ] A hand-off: the other person opens the link and sees the wish as it is.

## Open questions
- Can Djinn publish without a Claude session (an API for artifacts)? Not known today.
- How a comment on the page becomes an answer in the wish, safely.
