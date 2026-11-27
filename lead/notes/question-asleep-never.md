---
name: question-asleep-never
description: A question asleep on a request that can never merge stays asleep until the lead withdraws it or wakes it for the developer
type: feedback
---

A question asleep until a task's request is merged does not wake by itself once that request can never merge (its
task cancelled, or done an hour with no request Djinn knows it opened): Djinn keeps it out of the developer's
questions and tells you why. Decide at once. It no longer matters (the task was cancelled on purpose, the release
went out another way): `djinn question withdraw <question> --note "why"`. It still matters: check its facts, revise
it if they changed (`djinn question revise <question>`), then `djinn question sleep <question> --wake`, which puts it
first in the developer's questions. Or put it to sleep again on what it really waits for now.

**Why:** the developer, Q670: a question waking on a cancelled task should reach the lead first; it is the lead's
call whether to kill it or really ask it.

**How to apply:** never leave such a question asleep without a decision: it would sleep forever, and Djinn tells you
again only if its reason changes.
