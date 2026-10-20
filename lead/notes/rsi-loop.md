---
name: rsi-loop
description: An RSI loop (recursive self-improvement) - cycles "QA, then corrections" the lead runs alone, bounded by limits it asks first, on a named scope with an automatic check; no blocking question meanwhile, the cycle count in each batch's last line
type: feedback
---

An RSI loop (recursive self-improvement) is an autonomous iteration: cycles of "QA, then corrections" on a piece of
work, each cycle launched by the lead alone, without going through the developer. A night is one case of it, not the
rule. It applies only when the developer asks for it in so many words, in an answer or a message; never by default,
never inferred from a quiet stretch.

Before the loop opens, three things are set, or there is no loop:
- **Its limits**, asked of the developer by a question: the time allowed, the number of cycles, or both. Never assumed:
  a loop with no bound is not a mandate.
- **Its scope**, precise and named: the files, packages or behaviours each cycle may change. The corrections stay
  inside it.
- **Its check**, automatic: tests, commands, measurable criteria an agent runs and scores alone, with no human eye.
  Without a check an agent can run, there is no loop; say so to the developer instead.

Then write the mandate, with these three, in a decision block of the wish, so that every batch reads the same one.

While the loop runs:
- **No blocking question.** The lead decides, and says each decision the developer would want to know with
  `djinn lead say <wish> --decided "…"`. A question that remains is asked without `--move` nor `--before`: it can
  wait ([[blocking-or-can-wait]]). A worker's blocking question is answered by the lead from the mandate and the wish's
  decisions ([[worker-blocking-questions]]).
- **The cycle**, numbered N from 1:
  1. A task "QA cycle N" plays the user on 6 to 8 realistic cases with synthetic data, traps included (a case that
     misleads the obvious choice, a verbose request), runs the check, scores each output on a fixed grid, publishes a
     tilasm "QA cycle N" that names the commit it tested, and sorts the defects by layer or zone in its final note.
  2. The lead spawns one correction per zone touched, inside the scope, with disjoint write scopes, side by side, part
     of the azima (`--part-of`), and none for a zone without defect ([[conflict-free-dag]], [[worker-prompts]]).
  3. Once they are merged, "QA cycle N+1" is spawned `--after` them, on the integrated commit.
- **The count in the journal.** Each batch ends its turn with the count against the limit, "cycle 3/10: QA W41
  running": the journal carries that last text to the next batch, and nothing else passes from one batch to the next.
- **Small, described commits**: one per correction, saying what it changes and which QA defect it answers, so that the
  developer can walk from version to version and pick one.
- **The end**: at the limit (the time or the last cycle, whichever comes first), a summary tilasm (the scores cycle by
  cycle, what improved, what did not, the commit of each version) and one question to the developer on what comes
  next, which links it.

What the mandate does not lift: the rights Djinn grants (a refused command stays refused; a grant is asked without
`--wait`), the moves only the developer makes (open or merge a pull request, push a tag, release), and whatever a
decision of the wish forbids (a benchmark, a zone, real data). The QA runs on synthetic data only.

**Why:** the developer, 20/10/2026, in the answer to a question of a wish: iterate all night, at least 10 cycles, the
lead launching each cycle without going through them, as an experiment in self-improvement within Djinn. They then set
it as an RSI loop, bounded: limits asked first, a precise scope, an automatic check, the night being only one case. A
question that blocks stalls the loop until someone looks; a loop without limits has no end the developer agreed to; one
without an automatic check needs a human at each cycle; a cycle that no batch counts is lost at the next batch.

Example: a static site generator in four packages (parse, templates, theme, lint), at least 10 cycles in a night.
Each QA cycle plays a user on 6 to 8 site requests with synthetic content (a blog, a documentation tree, a page in two
languages; one theme to match from a description, one verbose request, one content tree that traps the template
choice), runs the lint, renders the pages in a headless browser, scores them, keeps the rendered pages in its tilasm,
which names the tested commit of the branch, and lists the defects by package. The lead spawns one correction per
package hit, part of the azima, then the next QA cycle after them; the reference pages a decision of the wish freezes
are never touched. At the end: a summary tilasm of the cycles and a question on what comes next.

**How to apply:** when the developer asks for a loop, ask its limits, name its scope and its automatic check, put the
three in a decision block, say with `--decided` what the loop will run, and spawn QA cycle 1. At each batch, read the
open work, take the cycle where the journal left it, and end the turn with its count. Stop at the limit with the
summary and the question.
