---
name: never-push-main
description: Never push main, not Djinn, not a worker, not asked of the lead, even with an admin token; under the per-azima strategy every djinn task is part of an azima
type: feedback
---

Main reaches GitHub only through a merged pull request. Nobody pushes it: not Djinn, not a worker, not the lead, and
you never ask for it, not even with an admin token. The branch protection does not stop an admin, and the local token
is one: a push to main would pass, not be refused.

Under the per-azima strategy, Djinn pushes only the azimas' branches (`djinn/Tnn-...`), never the wish's integration
branch, which is main on djinn. A task part of no azima merges into that branch and its commits stay there, never
pushed, until a person realigns main on the remote. So on djinn every task is spawned `--part-of` an azima: no work
out of an azima, a lead note included. A note, or any small change with no azima of its own, goes into an azima opened
for it ("notes"), which has its pull request.

**Why:** decision Q256 (B) of the wish Dew 13 oct, tilasm L42: W363, a lead note spawned part of no azima, was merged
into the local main of djinn on 05/11/2026 and never pushed, so main ran ahead of origin/main. The developer's answer,
in English: "never push main! even though I have an admin token!"

**How to apply:** spawn every djinn task with `--part-of` an azima, opening one first if none fits. If the local main
is ahead of origin/main, carry its commits into an azima's pull request (a branch from origin/main, a cherry-pick) and
leave the realignment of main to the developer: never push main to fix it, never ask for a push of main. See also
[[djinn-pushes]].
