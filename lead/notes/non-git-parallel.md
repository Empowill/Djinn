---
name: non-git-parallel
description: On a project outside Git, give every spawn --write-scopes (its paths, an external label like n8n-flow-2.1-only, or a dedicated read-only label), and chain tasks on the same external resource with --after
type: feedback
---

On a project outside Git, Djinn creates no worktree: every worker runs in the project's folder itself, and the
scheduler (`dispatch.Held`) keeps two writers apart whenever their `--write-scopes` overlap (`W12 writes the whole
folder, which overlaps …`). A task spawned without `--write-scopes` (or with `.` or `/`) reserves `the whole folder`,
and every other work task in that project waits for it to finish. Once a task has started, `djinn task update
--write-scopes` is refused (`task W12 is running: the rest after it ends`, and once it ran, `only title, prompt,
provider, model and azima may change`): the write scope must be given at `djinn task spawn` (or while the task is still
planned and has never run).

Give every spawn on a non-Git project an explicit `--write-scopes`:

- **Project files**: the relative paths or subfolders in the project that the task writes (`--write-scopes
  scripts/sync.py --write-scopes config/flows`).
- **External writes only**: `cleanScopes` refuses absolute paths and `..`, but accepts any clean relative name even when
  no such path exists on disk. When a task only writes outside the project folder (an n8n workflow, external docs, a
  cache), give it a synthetic label naming that external target (`--write-scopes n8n-flow-2.1-only`, `--write-scopes
  claude-docs-only`, `--write-scopes cache-rocket-only`) so it does not reserve the whole folder.
- **Read-only work**: spawn with `--kind investigation` (which holds no write scope at all), or give a dedicated
  read-only label per task (`--write-scopes read-only-w16`) so two read-only tasks do not share a label and hold one
  another.
- **Same external resource**: two tasks that write to the same external resource (the same n8n workflow, the same doc)
  must follow one another with `--after` (`djinn task spawn … --after <other>` or `djinn task depend <task> --after
  <other>`).

**Why:** 19/10/2026, wish Rocket Time (a project outside Git): W12 was spawned without `--write-scopes` for work on an
n8n workflow. Because no scope means `the whole folder`, W12 held W13, W16 and W17 in place, and once running its
`--write-scopes` could no longer be narrowed with `djinn task update`. Cancelled and respawned as W19 with an n8n
scope (`n8n-flow-2.1-only`), W19, W13, W16 and W17 all started and ran side by side.

**How to apply:**
- Before spawning on a project whose brief line says `outside Git: workers share its folder, and their write scopes keep
  them apart`, always pass `--write-scopes` on `djinn task spawn`.
- Never leave `--write-scopes` empty for a task that only calls an external tool, API, doc or cache: name that external
  scope with a `<target>-only` label, and chain tasks that touch the same external target with `--after`.
- If a task already started without `--write-scopes` and holds independent tasks behind `writes the whole folder`,
  cancel it (`djinn task cancel <task> --note "…"`, see [[cancel-not-stop]]) and respawn it with its scope.

See also [[conflict-free-dag]], [[cancel-not-stop]], [[worker-prompts]].
