---
id: 01a11a06-4f06-793a-9e8a-1e784a699d0f
code: T25
phase: 2
status: in-progress
---

# T25 · Review and decide at a glance

The developer's words: "I want to understand what is going on at a glance. Readability, decision and fast action
come first." The window (`src/`) keeps Clément's components and CSS approach, with a new layout and visual language
(`src/review.css`), and the lamp learns what the developer says with a click instead of a word.

**What was decided.**
- **Marks** live on what they mark: `repeated Mark marks` on `Question` (10) and `Block` (11), one mark of each kind
  (read, approved), with who and when. `djinn mark put <Q03|id> read|approved [--remove]`, `djinn mark list <wish>`,
  and a "Marked by the developer" section in the brief. Approving an open question answers it with the option its
  recommendation names (`plan.Recommended`: a letter first, `B: …`, or the first word of one option only).
- **Rounds**: "Enlighten me" (`djinn question enlighten`) and the lead's `djinn question revise` add a `Round` to the
  question (11), the revision bumps `revision` (12). A question whose last round is a request is *being
  investigated*: computed (`plan.Investigating`), not stored, and not counted as waiting for the developer. The
  revision keeps the former context, options and recommendation. The brief lists "To investigate", with the note.
- **Theme**: the system's by default, dark or light in the settings. Every surface takes its colours from the tokens
  of `src/theme.css`: Clément's greys became a neutral ramp (`--n-XX` is `#XXXXXX` in the dark theme), read again on
  a light ground; the terminal's colours (`--term-*`) and Mermaid read the same tokens.

## Done when

- [x] A sticky bar at the top of the flight plan and of a wish, only when something waits: one line each, coloured
  by how blocking it is, a click jumps there. (`src/attention.tsx`; e2e `review.spec.ts`; screens "a wish's screen
  puts its questions first…")
- [x] One status language, icon and word, never colour alone: done, running with a live dot, waiting for you,
  investigating, planned, failed, interrupted, stopped, paused; on tasks, wishes in the side panel (questions
  waiting, workers running) and counts in pills. Contrast 4.5:1 at least in both themes. (`src/status.tsx`; test
  "the status colours keep their contrast in the dark and the light theme"; screens "the side panel ranks…")
- [x] Question cards: title, the recommendation boxed first, options as buttons, context with headings below,
  "Rub the lamp" (apply the recommendation), "Enlighten me", a read mark. (screens "a question shows its options by
  letter…"; e2e `review.spec.ts`)
- [x] Read marks and approvals in the lamp, read by the lead with `djinn mark list` and in the brief. (`TestMarks`,
  `TestRecommended`; e2e: Rub the lamp read back with `djinn question list` and `djinn mark list`, a block marked
  read seen in `djinn wish brief`)
- [x] Enlighten and Revise, with their history, in the brief and on the page. (`TestRounds` in `internal/plan` and in
  `internal/render`; e2e: enlighten with a note, read in the brief, revised from the CLI, the revised card rubbed)
- [x] Titles, subtitles, a readable measure; the blocks in Markdown typography; logs, events and rounds as compact
  tables, folded. (screenshots `test-results/e2e/review-*.png`)
- [ ] Clément has reviewed the layout and the visual language. (needs: Clément's review)
- [x] The light theme on the dialogs, the agent chat and the terminal, then the system's theme by default. (tokens
  in `src/theme.css`; tests "the theme's tokens keep their contrast in the dark and the light theme" and "the
  stylesheets take their greys from the tokens"; e2e `theme.spec.ts`, screens `test-results/e2e/theme-*.png`)
