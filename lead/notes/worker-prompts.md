---
name: worker-prompts
description: A worker's prompt is five to twenty lines of Markdown - Gain, Scope, Validation, References by link; never a wall of sixty
type: feedback
---

A worker's prompt is short, simple and laid out: five to twenty lines of Markdown, a bullet or a short headed part
for each of four:

- **Gain**: the outcome, in one sentence.
- **Scope**: the files or packages it may change, and what it must not touch (the files of open tasks).
- **Validation**: the targeted tests and the commit checks, with the number to reach when there is one (a time, a
  count, a size).
- **References**: the tilasm, question or decision that holds the detail, by link (`djinn://tilasm/<id>`, `Q12`, a
  block), instead of copying it.

**Why:** the developer, 18/10/2026: prompts of twenty to sixty lines restated designs, decisions and analyses the wish
already holds. They first asked for five to eight lines, then set five to twenty, well formatted (Q99). A worker
reads its first prompt again at every turn, and a long one buries the gain under the detail; the link keeps one
source, which a revision updates for every worker at once.

**How to apply:**
- Write the four parts and stop. The how is the worker's: it reads the code, the pitfalls and the links itself.
- A detail worth keeping that has no home yet goes in a tilasm, a question or a block first; the prompt links it.
- Never ask for the whole test suite: the push checks are Djinn's, and Djinn gives every worker the project's checks.
- The scope comes from `djinn task overlaps <wish>` ([[conflict-free-dag]]); the same paths go in `--write-scopes`.
- A task that changes what Djinn does names its documentation page in the scope ([[docs-in-every-change]]).

The brief's rule "Workers start from a short prompt" says the same; this note is why.
