---
name: azima-draft-request
description: Per azima, the draft pull or merge request opens at the azima's first push, by a task you spawn, then gets babysat; never count on Djinn's push or its end-of-azima question to open it
type: feedback
---

Under the per-azima strategy, each azima lands in main through its own pull or merge request, and the wish usually asks
for it as a draft from the start. Djinn pushes the azima's branch by itself ([[djinn-pushes]]), but a push opens
nothing: Djinn proposes the request to the developer only once every part of the azima is done and green. Until then,
the request is yours.

As soon as an azima's branch is pushed, ahead of main, with no request open, Djinn records its request proposed: the
brief's azima line says "pushed with no pull request: open it as a draft", and you get one MOVE event, "Djinn: T45
pushed, no PR: open it as a draft, by a task", until the request opens. Nothing repeats it: act on the first one.

**Why:** wish Dew, 07/11/2026 (W593): the branches of the djinn azimas T41, T42, T43 and T45 were on origin with no pull
request, though the wish's description asked a draft for each azima. On dolmen the task's skill opens the merge request;
on djinn nothing did, and no event recalled it. Each lead saw only its batch, so nobody opened them.

**How to apply:**
- At the azima's first push (the event above, or the brief), spawn a task `--part-of` the azima that opens its request
  as a draft from the azima's branch into main, by the project's own skill for opening requests when it has one, else
  with the forge's command line (`gh pr create --draft`, `glab mr create --draft`). Never open it from a branch of
  your own, never re-target it.
- Then have it babysat: a project whose skill declares a babysit gets its watcher when the task ends done; otherwise
  spawn one, `djinn task spawn --babysit --after <task>`.
- Ask the developer the `--move` question with its link that every request a task opens gets ([[blockers-as-questions]]):
  taking it out of draft and merging it are theirs.
- A move question "Open a pull request for T45 to main?" may still come once the parts are done: an open request
  answers it, Djinn takes the one open.

See also [[djinn-pushes]], [[never-push-main]], [[blockers-as-questions]].
