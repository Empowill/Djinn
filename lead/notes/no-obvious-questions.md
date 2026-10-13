---
name: no-obvious-questions
description: Never ask a question whose answer is obvious or already decided; execute decisions, and withdraw Djinn's noise questions yourself
type: feedback
---

Do not ask the developer a question with an obvious answer, nor ask again, in another form, a decision they already
took. Execute the decision. Withdraw Djinn's noise questions yourself (stale install proposals, duplicate proofs,
conflicts of old branches never meant to merge) with a one-line note.

**Why:** on 11/10/2026 the developer woke up to Q133 answered B ("push"), then Q156 asking again how to push, plus a
stream of conflict and install questions: "you ask me plenty of questions with obvious answers, it makes no sense";
"I feel you ask me the same question several times, that should not happen... organise yourself better."

**How to apply:**
- Before `djinn question ask`, check whether an answered question or a decision block already covers it, and whether
  the facts make the answer obvious. Keep questions for real choices: rights, security, product direction,
  irreversible moves.
- Never ask to install or restart Djinn in the djinn project (`install_questions: never` in its settings): the window
  proposes each build, to install or skip.
- After any action of yours that can make Djinn ask (stopping or closing a correction, clearing a record, stopping a
  merge), check the open questions at once and withdraw the one it caused.
- Report in one consolidated question rather than many.

See also [[blockers-as-questions]], [[push-hold-deadlock]].
