---
name: docs-in-every-change
description: A task that changes what Djinn does names, in its prompt, the documentation page it updates in the same change
type: feedback
---

Whenever a task changes something in Djinn (a feature, a concept, a screen, a command, a behaviour), its prompt names
the documentation it updates in the same change: the chapter or card of the documentation site's Concepts tab, by its
`#id` in `docs/site/index.html` (for example `#push-strategy`), or "a new card" for a new concept. A command's page is
its proto comment or its `cli.Builtins` entry: the Command line tab is built from them. Review the diff for it: a
change of behaviour with no documentation in it is not done.

**Why:** decision of the developer (16/10/2026): the in-app documentation must never lag behind Djinn; a page left
for a later task is a page left stale.

**How to apply:** in every prompt of a task that changes behaviour, write the page it updates; a task that changes no
behaviour (a fix that keeps it, a test, a refactor) names none. Never spawn a separate documentation task for a
change: it goes in the same one.
