---
name: short-questions
description: A question is never long - the decision, what each option changes, the recommendation; details go in a tilasm it links
type: feedback
---

A question to the developer is never long. Its context is plain on the decision to take: what each option changes,
what it costs, the recommendation. When they need details to choose with full light (an analysis, a comparison, a
mockup, a design), the details go in a tilasm that lays out the possible choices, and the question links it,
`djinn://tilasm/<id>`, instead of carrying them.

**Why:** on 16/10/2026 the developer set it as a rule of arbitration: a question is read to decide, and a long one
buries the decision under its analysis. The details stay one click away, laid out where a page can show them.

**How to apply:**
- The bound: the context fits on one screen of the window. Outside its code block of commands, at most 1200
  characters: a one-line why, a few bullets (up to six), the options, no paragraph over three lines. A longer context
  is the sign that its details belong in a tilasm: move them there, keep the decision here.
- `djinn question ask` and `djinn question revise` warn on a context over 1200 characters outside its code blocks,
  and suggest a tilasm: cut it at once.
- The tilasm lays out the choices side by side (one section per option, what it changes, a diagram or a mockup),
  `djinn tilasm put <folder> --wish <wish>`; the question links it in one bullet, "Details: `djinn://tilasm/<id>`".
  Link a tilasm the wish already has before making another (`djinn tilasm list`).
- This binds the lead, the investigation workers that revise a question after "Enlighten me", and any worker that
  designs a mockup: the mockup is the tilasm, the question links it.
- An investigator cannot write files: it links a tilasm the wish has, or spawns a worker that makes one, keeps the
  question short meanwhile, and names that worker in its end line; the lead revises the question with the link once
  the tilasm exists.
- The commands the developer must run are not details: they stay in the code block at the top
  ([[readable-questions]]), which the bound does not count.

See also [[readable-questions]], [[blocking-or-can-wait]].
