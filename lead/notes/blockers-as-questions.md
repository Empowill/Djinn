---
name: blockers-as-questions
description: The developer reads only the window, never your terminal; every blocker, report or move of theirs is a question in the wish concerned
type: feedback
---

The developer does not read the lead's terminal and never answers there. Whatever is for them goes into a question in
the wish concerned (`djinn question ask`), statements included: a task done, a finding, a status, "done as you asked"
is a question too, often a yes/no acknowledgement with "OK, nothing to do" as an option; their answer tells you
nothing is left. End your turns with no text at all.

**Why:** on 12/10/2026 (wish Dew) a blocker explained only in the terminal (a right only the developer could grant,
`djinn wish allow`) left them with an azima "to validate" and nothing running for an hour; a report block alone was
invisible in the window too. Their words: "assume I never answer in the terminal and never look at what you point out
there. Everything must become a question, even statements." On 13/10/2026, after terminal summaries crept back: "I do
not read the terminal... it costs you output tokens for nothing."

**How to apply:**
- Whenever work waits on the developer (a right, a command only they can run, a refused call, a report to read), ask
  one action-oriented question at once, `--move` when the gesture is theirs: what happened, the report's key points in
  its `--context`, the exact command in a code block, what starts once it is done.
- Every pull or merge request a task opens gets a `--move` question with its link: taking it out of draft is the
  developer's own responsibility.
- Only what deserves their attention (Q223 note): a routine merge or an expected step needs no question; a finding, a
  behaviour change, a decision made in their place, or a wait on them does.
- Doubt is a question too: never turn an answer into tasks while the choice, its note or how they fit is unclear;
  ask again, quoting what you did not understand, even on an answered question.
- Keep their action list clean: when another task or Djinn now does what a question asked (a worker asked "push and
  open the MR" while another task opens it and Djinn pushes), withdraw it at once with a note. Never two questions for
  one gesture. After each task ends, check that its questions still need them.

See also [[no-obvious-questions]], [[question-context-heredoc]], [[push-hold-deadlock]].
