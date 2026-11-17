---
name: never-push-main
description: Never push a project's protected branch (main on djinn; which branches are protected varies per project), not Djinn, not a worker, not asked of the lead, even with an admin token; Djinn refuses to push the reference branch and the branches the forge protects (W50, W59); per azima every djinn task is part of an azima
type: feedback
---

A protected branch reaches its forge only through a merged pull or merge request. Nobody pushes it: not Djinn, not a
worker, not the lead, and you never ask for it, not even with an admin token. The branch protection does not stop an
admin, and the local token is one: a push would pass, not be refused.

Which branches are protected is set per project, on its forge, and varies: often the reference branch (`main_branch`),
sometimes release or environment branches too, sometimes none. Do not assume it is main: read the forge (on GitHub,
`gh api repos/{owner}/{repo}/branches --jq '.[] | select(.protected) | .name'`; on GitLab, `glab api
projects/:id/protected_branches`). On djinn (Empowill/Djinn on GitHub), main is the only protected branch (read on
05/11/2026).

Under the per-azima strategy, Djinn pushes only the azimas' branches (`djinn/Tnn-...`), never the wish's integration
branch, which is main on djinn. Before W401, a task part of no azima merged into that branch and its commits stayed
there, never pushed, until a person realigned main on the remote; since W401, Djinn gives such work an azima of its
own, with its pull request (see [[djinn-pushes]]). Still spawn every djinn task `--part-of` the azima it belongs to,
a lead note included: a note, or any small change with no azima of its own, goes into an azima opened for it
("notes"), rather than one pull request per task.

Djinn itself refuses to push a protected branch (W50, W59): the project's reference branch, and any branch its forge
(GitHub, GitLab) protects. Whatever the wish, its push strategy and the project, a push of that branch, or of a branch
whose upstream is it, fails before its push checks, the branch's line and the tasks' events naming the wish and the
command that gives it a branch of its own (`djinn wish set-integration <wish> --project-id <project> --branch <branch>
--create-branch`). The reference branch is read from the settings and the repository, refused even offline; the
forge's protected branches are read by gh or glab, kept ten minutes, and a forge Djinn cannot read refuses nothing more.
A wish under the wish strategy whose integration branch is main therefore never reaches the remote: give it a branch
of its own, or switch it to the per-azima strategy, before its work piles up. A refused push on main, or on a branch
the forge protects, is that configuration, not a bug to work around: never push it by hand to clear it.

**Why:** decision Q256 (B) of the wish Dew 13 oct, tilasm L42: W363, a lead note spawned part of no azima, was merged
into the local main of djinn on 05/11/2026 and never pushed, so main ran ahead of origin/main. Tilasm L09 (W43): the
wish Djinn gates integrated into main with the wish strategy, and Djinn pushed main ten times from 07/11/2026, with no
pull request; decision Q34 (A) moved it to its own branch djinn/gates, W50 added the refusal, and W59 extended it to
the branches the forge protects. The developer's answer, in English: "never push main! even though I have an admin
token!"

**How to apply:** spawn every djinn task with `--part-of` an azima, opening one first if none fits. If the local main
is ahead of origin/main, carry its commits into an azima's pull request (a branch from origin/main, a cherry-pick) and
leave the realignment of main to the developer: never push main to fix it, never ask for a push of main. See also
[[djinn-pushes]].
