---
id: 01a11a06-4f06-793a-9e8a-1e784a699d0f
code: T25
phase: 2
status: in-progress
after: T03
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
- **Decision log** (W59): a decision is an answered question or a block of kind decision, nothing else; they live in
  a "Decisions" tab, read only. `Question.icon` (15) and `Block.icon` (12), one emoji, set with `--icon` (`💬` and
  `📌` by default; Djinn's own questions carry 🔓, 🧭, 🏁); `Task.decision` (32), set with `djinn task spawn
  --decision` (an answered question's code, or a decision block's id), links a task to its decision. Who decided is
  computed (`render.Decisions`): the developer for an answered question or an approved block (the tone *human*),
  else the worker the block is about, else the lead. Marks stay on open questions and on the other blocks only.

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
- [x] A task is closed by hand, reliably, instead of deleted: `djinn task done <task> [--note …] [--by developer]`
  (`TaskService.Done`) closes a task no worker runs (planned, waiting, cut short, failed, stopped, imported), records
  `Task.closed` (who, when, why), journals it, keeps it through export and import, lets a dependent start; a running,
  paused or done task is refused. A yes to the edit question of a task closed while waiting starts nothing.
  (`TestDone`, `TestDoneStartsDependent`, `TestDoneWaitingThenYes`, `TestDoneCommand`, `TestExportKeepsClosure`)
- [x] The tasks have their own tab, in a wish and in the flight plan: what moves or waits first, by status (running,
  interrupted, failed, then waiting, paused, then planned), then every finished task, the latest ended first, with
  who closed it and why; "Mark done", with an optional note, on any card no worker runs. The page and the brief follow
  the same order. (screens "the Tasks tab lists what moves or waits by status…", "a task no worker runs can be marked
  done…"; data "the Tasks tab: what moves or waits by status…"; `TestFinishedNewestFirst`, `TestMovingByStatus`,
  `TestBriefFinished`; e2e `task-done.spec.ts`)
- [x] A "Decisions" tab in a wish and in the flight plan: the answered questions and the decision blocks, the latest
  first, without a button; who decided, the developer's in the human tone with an icon and a word; one emoji per
  decision; links to the tasks it led to, and from a task to its decision. The page's decision table and the brief
  follow. (`TestIcon`, `TestDecision`, `TestSpawnDecision`, `TestDecisions`, `TestDecisionTable`, `TestContrast`,
  `TestBrief`; data "the decisions are the answered questions and the decision blocks…", "the Decisions tab lists
  them without a button…", "a task links back to the decision it comes from"; "the status colours keep their
  contrast…"; e2e `decision-log.spec.ts`, screens `test-results/e2e/decision-log-*.png`)
- [ ] Clément has reviewed the layout and the visual language. (needs: Clément's review)
- [x] The light theme on the dialogs, the agent chat and the terminal, then the system's theme by default. (tokens
  in `src/theme.css`; tests "the theme's tokens keep their contrast in the dark and the light theme" and "the
  stylesheets take their greys from the tokens"; e2e `theme.spec.ts`, screens `test-results/e2e/theme-*.png`)
