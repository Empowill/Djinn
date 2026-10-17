---
name: blockers-as-questions
description: The developer never reads your terminal - every question, doubt or choice, blocker, report or move of theirs is a djinn question in the wish concerned; the terminal reply one short line at most
type: feedback
---

**The developer does not read the lead's terminal** and never answers there. Every question, doubt or choice the lead
has goes into a djinn question in the wish concerned (`djinn question ask`), never only into terminal text; a fact or
a decision for the next leads and workers goes into a block. Whatever is for them is a question, statements included:
a task done, a finding, a status, "done as you asked" is a question too, often a yes/no acknowledgement with "OK,
nothing to do" as an option; their answer tells you nothing is left. The terminal reply stays one short line at most,
what you did and what you wait for: no recap, no summary, no question in it. A driven lead (T59) has no terminal: the
same holds for its last text, which only the next leads read in the journal.

**Why:** on 12/10/2026 (wish Dew) a blocker explained only in the terminal (a right only the developer could grant,
`djinn wish allow`) left them with an azima "to validate" and nothing running for an hour; a report block alone was
invisible in the window too. Their words: "assume I never answer in the terminal and never look at what you point out
there. Everything must become a question, even statements." On 13/10/2026, after terminal summaries crept back: "I do
not read the terminal... it costs you output tokens for nothing." On 17/10/2026 (W234) they asked to make it explicit
and prominent: every question, doubt or choice into a djinn question or a block, never only into the terminal, whose
reply stays one short line.

**How to apply:**
- Whenever work waits on the developer (a right only they grant, a command only they can run, a report to read), ask
  one action-oriented question at once, `--move` when the gesture is theirs: what happened, the report's key points in
  its `--context`, the exact command in the code block at its top, what starts once it is done
  ([[readable-questions]]). Mark it right: `--move` with `--before` when it cannot wait, neither when it can
  ([[blocking-or-can-wait]]).
- A worker's refused calls, and a worker that failed, are yours, never the developer's (Q273, Q24 of 16/10/2026: W19,
  an investigator failed on refused commands, showed as waiting for them with nothing they could do; the window no
  longer shows it so). Take it up again otherwise, close it, or spawn it where a right already covers the calls. It
  reaches the developer only as a `--move` question naming the exact right it lacks and, in the code block at the top
  of its `--context`, the command that grants it (`djinn wish allow …`, a permission line).
- Every pull or merge request a task opens gets a `--move` question with its link: taking it out of draft is the
  developer's own responsibility.
- Only what deserves their attention (Q223 note): a routine merge or an expected step needs no question; a finding, a
  behaviour change, a decision made in their place, or a wait on them does.
- Before you end a turn, read your last text: a question mark, an "I am not sure", "should I", "either ... or" in
  it is a question or a choice left in the terminal. Ask it with `djinn question ask` first, then cut the line.
- Doubt is a question too: never turn an answer into tasks while the choice, its note or how they fit is unclear;
  ask again, quoting what you did not understand, even on an answered question.
- Keep their action list clean: when another task or Djinn now does what a question asked (a worker asked "push and
  open the MR" while another task opens it and Djinn pushes), withdraw it at once with a note. Never two questions for
  one gesture. After each task ends, check that its questions still need them.

See also [[no-obvious-questions]], [[question-context-heredoc]], [[push-hold-deadlock]], [[readable-questions]],
[[blocking-or-can-wait]], [[short-questions]].
