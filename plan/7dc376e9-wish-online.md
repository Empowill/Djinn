---
id: 01a118f6-c07d-7d18-8418-4cf67dc376e9
code: T24
phase: 3
status: in-progress
after: T08 T13
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
- **Order of the page:** what matters now first. A bar pinned to the top while something waits for the user (each
  blocking question a line, the other questions a line each while they are two at most, else one line naming them;
  the tasks cut short in one line; a project to attach; a ready wish), the most urgent first. Header with the tasks
  counted by status and contents (pills naming only the sections present, coloured as their most urgent item). To
  decide (open questions, a card each: the recommendation boxed and first, then the options, then the context; a
  question a waiting task needs is red, open and first; then those needed before something, orange under their
  `before` words; then those that can wait, grey; folded, their recommendation in sight; a lone question open), waiting for you, who runs now (the running workers as cards, with their last event; the
  finished work folded below as a table), tasks (waiting, failed and cut short in clear, with their dependencies;
  planned folded as a table with why each waits), decisions (the latest first, a table: when, the question and the
  choice in bold, why; past 15, folded), notes (the blocks in their order, each under its title; a long block
  folded; a run of more than three of one kind gathered in one card, a folded line each, eight in sight), journal
  (commands and `log` blocks, the latest first, a compact table; past 10, folded), worker events (what they said,
  their status changes and errors, the latest first; past 10, folded). Empty sections are not rendered.
- **One colour language, never colour alone.** Each state has a colour, an icon and a word: done green ✓, running
  blue with a live dot, waiting for you orange ?, planned grey ○, failed red ✕, interrupted amber ↺, paused indigo ‖,
  stopped grey ■; and for what waits: blocking red !, waiting for you orange ?, can wait neutral ◷. Used by the
  tasks, the workers, the bar and the pills. A test checks every colour at 4.5:1 at least on its soft colour and on a
  card, in light and dark.
- **A Mermaid diagram shows as its source**, with a line that says so: the page runs no script.
- **"Waiting for you" comes from the lamp only:** a task waiting on its edit question, a task cut short by a stop,
  a project not on this machine. No block kind is read as an action.
- **Same scrubbing as the export,** plus the data folder (`djinn-data`). Tool calls and results stay out.
- **Markdown by goldmark (MIT), raw HTML left out**, dangerous links emptied. A block of another media type shows
  as text. No script, no external font: the page opens offline.
- **The page's texts are translated** (`page.*` keys), in the language of the user's locale.

## Done when
- [ ] `djinn wish sync` renders the page in Go (done), and the lead republishes it in one call.
  - [x] The lead's instructions say so. (d3881ed: the stable brief, `internal/plan/brief.go`: republish the
    printed file as it is, in one call, without reading or rewriting it, no HTML by hand; `TestBrief` checks it)
  - [ ] Seen publishing. (needs: a real lead session)
- [x] A change in the wish updates the page without the model writing HTML.
- [x] The page reads at a glance: a bar of what waits, questions first with their recommendation boxed, one colour
  language with icons and words, who runs now, compact tables for the rest, light and dark, 375 px without
  horizontal scroll. (w37: `TestBar`, `TestColourLanguage`, `TestContrast`, `TestEvents`, `TestDiagram`,
  `TestBusyPage`; screenshots of an imported wish rendered by a test djinn, light, dark and 375 px)
- [ ] A hand-off: the other person opens the link and sees the wish as it is. (needs: two people)

## Open questions
- Can Djinn publish without a Claude session (an API for artifacts)? Not known today.
- How a comment on the page becomes an answer in the wish, safely.
