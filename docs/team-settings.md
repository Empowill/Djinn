# Team settings

A team that works on one repository shares a few defaults for its workers: which agent runs them, which model,
how much one may spend, how their branches are named. The repository carries them in one file, next to the permissions:
`.agents/settings.txtpb`. Each developer may keep their own next to their Djinn's data, and theirs win.

## The file

A `plan.v1.ProjectSettings` in text protobuf ([`api/plan/v1/plan.proto`](../api/plan/v1/plan.proto)), committed
with the code:

```
# proto-file: api/plan/v1/plan.proto
# proto-message: plan.v1.ProjectSettings

# The team's workers run claude with opus, spend at most 3 dollars each, and work on djinn/<code>-<slug>-<uuid8>.
provider: PROVIDER_CLAUDE
model: "opus"
max_budget_usd: 3
branch: "djinn/{code}-{slug}-{uuid8}"

# Djinn integrates their finished work by itself: npm ci makes a fresh worktree ready, go tool task gen makes gen/**,
# the lint runs before each commit and the tests before each push, go tool task install installs the build pushed,
# once you say so.
generated: "gen/**"
generate: "go tool task gen"
setup: "npm ci"
checks { name: "lint" command: "go tool task lint" when: CHECK_WHEN_COMMIT }
checks { name: "test" command: "go tool task test" when: CHECK_WHEN_PUSH }
test_one: "go tool task test-pkg -- {pkg}"
install: "go tool task install"
```

| Setting          | What it sets, when a task names none                                   | Not set                    |
| ---------------- | ---------------------------------------------------------------------- | -------------------------- |
| `provider`       | The agent of the project's workers: claude, codex, antigravity, fake.  | claude                     |
| `model`          | Their model, a model of that provider. `""`: the provider's default.   | the provider's default     |
| `max_budget_usd` | The most one worker may spend, when its provider can enforce it. `0`: no limit. | no limit          |
| `branch`         | The branch of a worker's worktree, a template: see [below](#branch-names). | `{code}-{slug}-{uuid8}` |
| `generated`      | The files code generation makes, as globs (`gen/**`), repeated: see [integration](#integration). | none |
| `generate`       | The command that makes them, in the project's folder.                   | none                       |
| `setup`          | The command that makes a fresh worktree ready (`npm ci`), in the project's folder: see [checks](#checks). | none |
| `checks`         | The commands Djinn runs before it commits a task's work, before it pushes, or both, each through a gate of its name: see [checks](#checks). Set, Djinn integrates finished work. A file that sets some sets them all. | none: integration into a wish's own branch only, untested |
| `test_one`       | The command template a worker runs for targeted tests (`go tool task test-pkg -- {pkg}`): see [checks](#checks). | none: worker runs tests covering what it changed |
| `test`           | The former name of a check run before each commit: `test: "make test"` is the check `test` at `commit`. | none |
| `correction_attempts` | How many correction workers Djinn starts for work that conflicts in code or tests red, before it asks you: see [integration](#integration). `0`: it asks at once. | `2` |
| `install`        | The command that installs the integration branch once pushed, in the project's folder: the window proposes it. | none: nothing proposed |
| `install_questions` | When Djinn asks the developer to install builds: `always` (default), `when_needed` (only when necessary, stating why; for projects whose builds install automatically after each push), `never`. | `always` |
| `main_branch`    | The project's main branch, which Djinn merges into each wish's integration branch: see [keeping up with main](#keeping-up-with-main). | the remote's default branch, else `main`, else `master` |
| `merge_main`     | When Djinn merges it: `MERGE_MAIN_RELEASE` (once main holds a release the branch lacks), `MERGE_MAIN_COMMIT` (once it holds any commit the branch lacks), `MERGE_MAIN_OFF`. | at each release |
| `merge_main_minutes` | How often, in minutes, Djinn fetches main to look at it, at most. A release the running Djinn finds makes it look at once. | `60` |
| `install_releases` | For the project a Djinn built from a checkout comes from: a newer release installs by itself while that checkout is on main. `false`: it is only offered. | on |
| `install_builds` | For the project a Djinn built from a checkout comes from: a build Djinn pushes of the running Djinn's line, past the push checks, installs by itself; the restart stays your click: see [integration](#integration). `false`: it is only offered. | on |
| `push`           | When Djinn pushes the integration branch: `PROJECT_PUSH_STANDARD` (today's cadence: at an azima's end or 3 tasks and an hour), `PROJECT_PUSH_ON_DEMAND` (never pushes by itself, only the developer's push does). | `PROJECT_PUSH_STANDARD` |
| `push_strategy`  | The branch strategy for integration and push: `PUSH_STRATEGY_WISH` (today's behavior: one integration branch per wish and project), `PUSH_STRATEGY_AZIMA` (one branch per azima, `djinn/<code>-<slug>`, cut from main and landing in main). | `PUSH_STRATEGY_WISH` |
| `babysit`        | `false`: a pull or merge request a task opens is not watched by itself: see [babysitting](wish-templates.md#babysitting-what-a-task-opens). `djinn task spawn --babysit` still works. | on, when a skill of the project declares one |

A watcher (`--provider watch`) runs a command: no setting above applies to it, and none can make one. One more is
read from **your own file only**, never from the repository's (which lists its commands in
`.agents/permissions.txtpb`, and is warned of a `watch_commands` it sets):

| Setting          | What it sets                                                            | Not set |
| ---------------- | ----------------------------------------------------------------------- | ------- |
| `watch_commands` | The commands a watcher may run in the project besides those `.agents/permissions.txtpb` lists, each a command or its start on a word boundary, repeated: see [what a watcher may run](wish-templates.md#what-a-watcher-may-run). The project's `denied_commands` still win. `djinn project allow <project> --watch '<command>'` adds one (`--revoke` takes it back), as does the project's panel in the window; `djinn project show` lists them, from `DEVELOPER`. | none |

Four more set the *investigators*, the small tasks Djinn starts by itself on a question of a wish after
"Enlighten me": `Q03: enlighten` investigates and revises the question (see [agent protocol](agent-protocol.md)).
Answers are turned into tasks by the lead only: answer workers are removed.
They run read-only and take a slot like any agent, the newest work task's when the machine is full: on the project's `provider` when that provider can run them read-only (claude,
codex), falling back to claude otherwise; or on `question_provider`.

| Setting               | What it sets                                                                                 | Not set                    |
| --------------------- | -------------------------------------------------------------------------------------------- | -------------------------- |
| `enlighten_workers`   | `false`: no investigator starts after "Enlighten me"; the lead investigates itself.           | on                         |
| `question_provider`   | Their provider (`PROVIDER_CLAUDE`, `PROVIDER_CODEX`...).                                     | the project's provider when read-only, else claude |
| `question_model`      | Their model. `""`: the provider's default.                                                   | `sonnet` for claude, else the provider's default |
| `question_budget_usd` | The most one may spend, when its provider can enforce it. `0`: no limit.                    | 2                          |

A file that sets `provider` or `question_provider` resets `question_model` to its default too. When the project's
provider cannot run read-only (like Antigravity), question workers fall back to claude with `question_model` (`sonnet`),
and their start event says why (`antigravity cannot run read-only: claude`). On `djinn up`, `--enlighten-workers` (or
`DJINN_ENLIGHTEN_WORKERS`) overrides `enlighten_workers` across all projects. (A settings file naming the removed
`answer_workers` or `question_workers` is refused; delete the line.)

## Branch names

In a Git repository each worker edits its own worktree, on a new branch from the tip of its wish's
[integration branch](#integration), or the project's `HEAD` when the wish has none. `branch` names it,
with three placeholders:

| Placeholder | Becomes                                                                 | Task W1 "Fix the login page" |
| ----------- | ----------------------------------------------------------------------- | ---------------------------- |
| `{code}`    | The task's code, in lower case.                                          | `w1`                         |
| `{slug}`    | Its title in lower case, accents folded, letters and digits joined by `-`, 40 characters at most. | `fix-the-login-page` |
| `{uuid8}`   | The last 8 characters of its UUIDv7, the random part.                    | `89abcdef`                   |

`{uuid8}` is required: two tasks never share a branch, and a code repeats from one wish to the next. Around the
placeholders, letters and digits joined by at most one of `.` `_` `/` `-` in a row: a template Git would refuse as a
branch (`feature//{uuid8}`, `-{uuid8}`, `{uuid8}/`) is refused when Djinn reads the file, like an unknown placeholder.
A placeholder that comes out empty, a title with no letter, takes a separator next to it away: `{code}-{slug}-{uuid8}`
gives `w3-89abcdef` for "!!!".

A task gets its branch when its worker starts: a planned task takes the template as the files are then. Its branch
never changes after, and stays when `djinn task clean` removes its worktree.

## Your own file

Your Djinn keeps yours in its data folder: `projects/<project id>/settings.txtpb` (by default under
`~/.config/djinn`). Same format, never committed. `djinn project show <project>` prints its path.

## Who wins

Setting by setting, the first that sets it:

1. **The task's own flags**: `djinn task spawn --model … --max-budget-usd …`, or what the lead asked.
2. **Your own file.**
3. **The repository's file.**
4. **Djinn's default**: claude, its default model, no limit, `{code}-{slug}-{uuid8}`.

A file that sets `provider` sets the model with it: its own `model`, or else the provider's default, whatever the
other file says. Your `provider: PROVIDER_CODEX` never runs codex with the team's claude model. A model applies only
to a task of that provider: `--provider fake` gets no model from the files. Your file can lift the team's budget
with `max_budget_usd: 0`; a task's flag cannot, as `0` there means "not given".

Djinn reads both files each time a task is spawned, from the project's own folder (the branch checked out there),
not from the task's worktree. A task keeps what it got: changing a file changes the next tasks only.

## See where each setting comes from

```
$ djinn project show app
settings:
  - name: provider
    value: claude
    source: repository
  - name: model
    value: sonnet
    source: developer
  - name: max_budget_usd
    value: 3
    source: repository
  - name: branch
    value: djinn/{code}-{slug}-{uuid8}
    source: repository
  - name: checks
    value: 'lint: go tool task lint (commit); test: go tool task test (push)'
    source: repository
  …
repository_file: /home/me/src/app/.agents/settings.txtpb
developer_file: /home/me/.config/djinn/projects/01a1…/settings.txtpb
```

`source` is `repository`, `developer` or `default`.

## A file Djinn cannot read

A malformed file, an unknown field, a value out of range: Djinn still starts, and `djinn project show` lists the
problem under `problems`, with the file and the line. The other file still shows. Spawning a task in the project
fails with the same message until you fix it: a team's budget never silently vanishes.

## No secret, ever

The file is shared and versioned: it holds no key. Djinn refuses a file, comments included, where a field name says
it holds one (`api_key`, `token`, `password`…), where a word starts like a well-known key (`sk-`, `ghp_`,
`AKIA`…), or where a long run of letters and digits looks like one. The error gives the line, never the value. A
worker's keys stay where its agent keeps them.

## Checks

What Djinn runs on a project's work before it goes further: each check has a name, a command and when it runs,
`CHECK_WHEN_COMMIT` (before a task's work is committed into its wish's integration branch), `CHECK_WHEN_PUSH` (before
Djinn pushes that branch), or both. Each runs in the wish's integration worktree, never your checkout, through the gate
of its name (`djinn gate run lint`), in the order the file names them.

```
# Djinn: the lint before each commit, the whole test suite before each push.
setup: "npm ci"
checks { name: "lint" command: "go tool task lint" when: CHECK_WHEN_COMMIT }
checks { name: "test" command: "go tool task test" when: CHECK_WHEN_PUSH }

# A project whose test suite takes too long to run before each push: the lint at both, its tests left to CI.
checks { name: "lint" command: "make lint" when: [CHECK_WHEN_COMMIT, CHECK_WHEN_PUSH] }
```

- **Commit checks** run on the merge of each task's work: red, the merge fails as a conflict does, and a correction
  worker starts (below). A check whose command the task's worker ran last through `djinn gate run`, and passed, on the
  very tree of the merge (its uncommitted changes and files not tracked included, the same once it ended) does not run
  again: the task's events say `commit checks: lint passed by the worker on this tree, not run again`. Any change
  after it, a merge that brings other work, or a failed run, and the check runs.
- **Push checks** run on the branch's tip when a push is due. A check that passed as a commit check on that very commit
  does not run again. Red, the push is **held**: the tasks' events and the wish's head say why. Red twice in a row, or
  at once when no work of the wish is left to commit in the project, Djinn starts a correction worker (within
  `correction_attempts`). If the correction fails, or its attempts are spent, or none can start, Djinn asks you: try
  again with a new correction worker, take the push checks over yourself, or push without the push checks this once.
  A correction stopped on request stays the correction for ten minutes, time to continue it on another agent. A
  question about the push withdrawn, or the branch moved since its checks were red (even pushed by hand), runs the
  push checks again: green, the hold lifts; red at a new commit, Djinn withdraws a stale question and acts again.
- **The setup** makes the integration worktree, and the install worktree, ready before the first command there (a
  check, `generate`, `install`): once, then again when the setup command or a lock file changes (`package-lock.json`,
  `go.sum`, `yarn.lock`, `pnpm-lock.yaml`, `Cargo.lock`, `poetry.lock`, `uv.lock` and the like, anywhere in the
  project), and in a worktree made anew. Failed, the merge is red.
- **The workers know them.** The lead's brief lists each project's checks and when they run; each worker that edits a
  worktree gets them at the end of its first prompt, to run the commit checks (`djinn gate run lint -- go tool task
  lint`) before it ends; again when it resumes from its first prompt (a session never known).
  When writing a worker's prompt, ask for the commit checks and the targeted tests, never "run the whole test suite"; the push checks are Djinn's.
- **Targeted tests (`test_one`)**: when a project names `test_one` (such as `test_one: "go tool task test-pkg -- {pkg}"`),
  Djinn includes the command template in the project's checks brief. Workers run the commit checks before they end,
  and then only the targeted tests covering what they changed, leaving the push checks (such as the full test suite) for Djinn.
- `djinn project show <project>` and the project's view in the window list the setup and the checks, each with its
  last run: the commit it checked, when, how long, and why it failed.

A check's name is letters, digits, `-` and `_`; two checks never share a name, case ignored. `test: "make test"`, the
former single test command, still works: it is the check `test` at `commit`, unless the file names a check `test`.

## Integration

A worker's work counts once it is in its wish's branch, checked. In a project whose settings name a check, Djinn
brings it there by itself, no model ([T07](../plan/8e8d3d76-orchestrator.md)):

1. When a worker ends done, its task's work is **pending** (`djinn task get` shows `integration`).
2. Djinn commits each task's work **at once, alone**, in the order the tasks ended: its dependents build on it
   straight away.
3. In a worktree of its own per wish and project, under Djinn's data folder (`projects/<project id>/integration/<wish
   id>`), detached at the integration branch's tip, never in your checkout: it merges the task's branch with
   `--no-ff`, once its worktree holds nothing not committed (step 8). A conflict only in `generated` files takes the
   task's side and runs `generate` under the gate `gen`. Then the `setup` runs if the worktree needs it, and each
   commit check under the gate of its name.
4. **Green**: the integration branch moves to the result, Git checking it is still where the merge started; the task
   is **committed**, with the commit, and the journal records it. Its worktree is then removed, as `djinn task clean`
   does, its branch kept; a worktree that holds changes not committed stays, and its events say so. Your checkout of
   the branch, clean, follows by a fast-forward. With changes not committed, it is left as it is, and so is the
   branch: moved under it, your next commit would undo the work. The task stays pending, saying why, and the branch
   moves once your changes are committed or put aside, without testing again.
   Djinn merges and commits as you, with git's `user.name` and `user.email`, and never invents an identity: without
   one, the task stays pending, saying the fix (`git config --global user.name "Your Name"` and
   `git config --global user.email you@example.com`, or without `--global` in the project's repository), never a
   conflict; the next pass merges it once git has one.
5. **Red** (a commit check, or the setup), or a **conflict** in code: the branch stays as it was, and the task says
   what failed. The tasks that end
   after it are committed on their own meanwhile.
6. **A correction worker** starts by itself: a work task part of the same azima as the failed task, of its provider,
   its worktree on the failed merge (the branch, and that merge again, its conflicts left in place; or the work merged,
   for a red check), what failed in its first prompt: the files in conflict, or the check's command and
   the end of its output. It commits its work, which concludes the merge, and its branch integrates like any task's,
   at once and alone. Green, the failed tasks are committed with it, saying
   `corrected by W5`. A correction whose work fails in turn, or whose worker fails, counts as an attempt.
7. Past `correction_attempts` (2 by default), Djinn **asks you** on the wish: try again (a new correction worker, its
   attempts counted again), leave it (the work stays out of the branch), or you take it (the question says how to
   find the failed merge). The question blocks nothing else: the rest of the wish goes on. A correction worker you
   stop leaves it to you, asked the same.
8. **Work not committed is not merged, and Djinn commits nothing blindly.** Before merging a task, a correction
   worker's included, Djinn looks at its worktree (`git status --porcelain`, untracked files included, the project's
   `.gitignore` applying). Clean, it merges as above. With changes, the task is **uncommitted**: a **review worker**
   starts by itself, a work task part of the same azima, of the task's provider, in that task's worktree and on its
   branch, the task's title and prompt, the files and their diff (clipped) in its first prompt. It commits what
   belongs to the task, with a message in the repository's style, reverts or deletes the rest (debug output, scratch
   files, artifacts), and ends with a line saying what it kept, what it dropped, and why. Its branch then integrates
   like any task's, and the task says `uncommitted: reviewed by W5`. A review that fails, or leaves changes in turn,
   counts as an attempt; past `correction_attempts`, Djinn asks you, as above. Review and correction workers may run
   `git add`, `git commit`, `git restore`, `git rm` and `git clean` besides the project's `commands`; its
   `denied_commands` still win.

In a project whose settings name no check, Djinn does the same, no check run, when the wish has a branch of its own
there (`djinn wish set-integration <wish> --branch feat/x --create-branch`), neither the project's main branch nor an
azima's: untested work never goes into main. The work of a done task it leaves out for want of one, commits and all,
says why in the task's events (`integration: none: ...`) and in the one line the lead hears of its end.

A task that waits for another (`--after W5`) waits for W5's work to be **committed**, not only done: it says `waits for
W5 to be committed`, and W5 is committed at once, alone. Its worktree then starts from the integration branch's tip,
whatever branch your checkout is on, so it builds on W5's work; its start event says `from feat/x at 1a2b3c4d`. Work
Djinn does not integrate (a project that names no check, its wish on main) counts once done, as before.

Where each task's work stands shows in `djinn task get` (`integration`: its state, the commit, what failed, the task
that corrects it), the lead's brief, the wish's page and the Tasks tab: done, waiting to be committed, being
committed, committed as `1a2b3c4d`, conflict, red, corrected by W9, uncommitted, reviewed by W9. A finished task's
integration record can be cleared with `djinn task update <task> --integration none` (refused while it is merging),
leaving it without an integration record so Djinn will not integrate it on its own. `djinn task update <task>
--integration pending` puts the work of a done task without one back in line, as when its worker ended: once the
wish has a branch of its own, say; refused, saying why, when Djinn would not integrate it.

### Pushing

Pushing the integration branch to its remote is Djinn's, never an agent's: `.agents/permissions.txtpb` denies `git
push` to every worker, and their environment makes the push impossible whatever the command ([no worker
pushes](providers.md#no-worker-pushes)). Djinn checks it each time a task's merge ends, and a push is **due**:

- when an azima ends: it is done or to validate, as its state says (every part finished: a failed part keeps it in
  progress, one cut short for good does not), and its last part is committed;
- or once three tasks are committed since the last push and more than an hour has passed since it.

The [push checks](#checks) run first, on the branch's tip: red, they hold the push.

The remote is the branch's upstream, else `origin`, else the repository's only remote; a repository without a remote
pushes nothing. Djinn runs `git push` in your checkout's repository, with your own credentials (your SSH agent, your
credential helper), never prompting for them and **never forcing**. By default it pushes by itself (`auto`); in `ask`
mode it asks a question first, "Push feat/x to origin? (3 commits: …)", which Rub the lamp answers in one click, and
nothing more is asked until you answer. A push the remote refuses (your branch is behind, or protected) is said in the
tasks' events and asked about: push again once you have brought the remote's commits in, or leave it until the next
push due. Each push is in the journal; the wish's head shows the last one, its commits on hover, and when.
`djinn wish set-integration <wish> --push-after-minutes 30 --push-after-tasks 2 --push-mode ask` changes the hour, the
count and the mode, for that wish.

A project may choose its cadence with `push`: `PROJECT_PUSH_STANDARD` (the default cadence above, where the wish's
`push_mode` still applies) or `PROJECT_PUSH_ON_DEMAND` (Djinn commits each task's work into the integration branch as
now, but never pushes by itself and asks nothing; only the developer's push does). The side panel's project row, the
window's project view and an azima's card list each branch Djinn pushes that is out of sync with its remote, one line
each: the integration branch of every active or paused wish there, and every azima's own branch, as `feat/x ↑2 ↓1`
(commits to push, commits the remote has that the branch lacks), its wish and azima on hover; a small button pushes
that branch alone, offered only when it has commits to push. Past three branches, the lines fold behind the first two.
A push shows on its line until it ends, whoever started it (the window, the command line, or Djinn by its cadence),
as the wish records it (`WishPush.run`): a spinner while its push checks run (the check on hover) and while git
pushes, every button of the list waiting meanwhile; a check mark a few seconds once pushed; a cross when its push
checks hold it, with the check that failed, the first lines of its output, and the correction Djinn started, on hover;
a cross with git's words when the remote refused it. Each change of a push's state, and each push, has the window read
its projects again: the counts follow the remote at once. A push that finds the remote up to date is no failure:
the line says "up to date", and `djinn project push` exits 0 with `up_to_date`. The project view has the cadence
switch; `djinn project push <project> [--wish <wish>] [--branch <branch>]` pushes from the command line. In On demand,
the wish's head says "pushes on demand".

A project also sets the default push strategy with `push_strategy`: `PUSH_STRATEGY_WISH` (the default: one
integration branch per wish and per project) or `PUSH_STRATEGY_AZIMA` (one integration branch per azima).
`djinn project push-strategy <project> [--strategy wish|azima]` shows or sets the developer's project default.
Each wish can override this strategy: `djinn wish push-strategy <wish> [--strategy wish|azima]` (or
`djinn wish set-integration <wish> --push-strategy wish|azima`) shows or sets the push strategy of that wish.
A wish created without a value takes the project's default, and a project without a value defaults to `wish`.
To protect work already merged, switching push strategy is refused when it would strand merged work: to per azima, when
the work of an azima is merged into a branch (the wish's integration branch) that is not that azima's own; to per wish,
when work is merged into an azima's branch that is not the wish's integration branch. Work of no azima stays in the
wish's integration branch either way. The refusal names the work and the branch it lives on, and how to keep it there.

Per azima, each azima has a branch of its own, `djinn/<code>-<slug of its title>` (`djinn/T07-the-orchestrator`),
which works like a wish's integration branch:

- **Cut from main** when the azima is made (by `djinn task spawn --kind azima` or `djinn plan sync`): main's commit as
  Djinn knows it, never another azima's branch. An azima made before its wish was switched gets its branch when its
  first part starts, or its first work is merged. Djinn records on the azima its branch, the main it lands in and the
  commit it was cut from (`djinn task get <azima> --json`: `azima.branch`, `azima.base_branch`, `azima.cut_from`); a
  title changed later keeps the branch's name. It is never cut again while it exists, and never rebased.
- **Its work merges into it** as a wish's does ([integration](#integration)); work of no azima goes to the wish's
  integration branch.
- **It keeps up with main** by the same `merge_main` rules ([keeping up with main](#keeping-up-with-main)), where it
  stands recorded on the azima (`azima.main`).
- **It lands in main.** Once its parts are committed and its branch pushed, Djinn proposes its pull request to main;
  you open it. It targets main from the start and is never re-targeted; each push of the branch updates it. Its card
  shows it proposed, open or merged; merged, the azima is done.
- **No azima waits for another**, each branch holding main and its own work: Djinn refuses an azima after another,
  directly or through their work (`djinn task spawn --after`, `djinn task depend`, `djinn task update`,
  `djinn task group`, a plan file's `after:` line at `djinn plan sync`), and refuses switching a wish, or a project's
  default, to per azima while azimas depend on each other, naming them. Tasks inside one azima wait for each other as
  usual.

A branch made before, stacked on the branch of the azima it depended on, keeps working as it is: its work merges into
it, main is merged into it, and nothing of it is rewritten.

**Naming an azima's branch.** A project whose branches must follow a convention (a ticket's key, `ET-1234-<slug>`, that
its forge or its sync tools require) names each azima's branch: `djinn task update <azima> --branch ET-1234-the-fix`.
A branch that exists is adopted as it is, never cut again nor moved: Djinn records it with the main it lands in and the
commit of main it was cut from, their merge base. One that does not exist is cut from main under that name. It is
refused on a work task, for main itself, for another azima's branch, while a merge runs into the azima's branch (or into
the wish's, when it adopts it), and when the azima's branch so far holds commits the named one lacks. Where the branch
stands against main and its pull request follow the branch: Djinn finds an open one by the branch, and the azima's
card and the lead's brief show the branch and its pull request. In a project whose settings name no check, Djinn merges
the azima's work into a branch named this way untested, as into a wish's own branch; never into a branch it named.

**Switching a wish to per azima when its work already lives on the wish's branch.** A wish that pushed per wish, one
azima carrying its subject, has that azima's work merged into the wish's integration branch, `ET-1234-the-fix`, perhaps
pushed and with a merge request open. Switching to per azima as it is would give the azima a new branch cut from main,
the merged work left behind: Djinn refuses, naming it. Have the azima adopt the branch its work lives on, then switch:

```
djinn task update T1 --branch ET-1234-the-fix
djinn wish push-strategy <wish> --strategy azima
```

Nothing is cut again or moved: the azima's work keeps merging into `ET-1234-the-fix`, main is merged into it by the
same rules, it is pushed as before, and the merge request already open on it is the azima's. While the azima's branch is
the wish's integration branch, work of no azima goes there too.

Once Djinn has pushed in a project whose settings name an `install` command, the window proposes, as for a new
version, to install it and restart on it, with what changed (each task with the last paragraph its worker wrote folded
under it, and the titles of the commits pushed folded at the end). The command runs under the
gate `install`, at that commit, in the project's install worktree (set up as the integration's is), never in your
checkout nor in an integration worktree: an install never waits for an integration to end. While it runs, the banner
says what it waits for (another install, a gate and who holds it, the machine under pressure), then that it builds and
which step of the command runs (go-task's `task: [ui] npm run build` reads "ui: npm run build"), then that Djinn
restarts; when it installed a newer Djinn at the path of the running one, Djinn restarts on it, as the update button
does; otherwise it says the build is installed.

**What installs by itself.** A build of the running Djinn's own line (the branch it was built from, or one that carries
it on, W527), pushed past the push checks, installs without a click when `install_builds` is on, the default: one at a
time, at a low-priority worker's CPU priority, once no gate is held and the machine is not under pressure (the banner
says what it waits for meanwhile); a newer push of the line replaces one that waits. A build that does not hold the
build installed last, when that one is newer than the running Djinn (another branch's, installed by your click), is
only offered, and so is every other branch's build, a push made without its checks, and a project with no push check.
Once it is installed, the banner says "Djinn local-abc1234 is ready" with what it adds: **Restart** stays your click,
never automatic. Your click on another build stops an install Djinn started by itself; a click on the build it
installs joins it.

**An install never spins forever.** It runs within Djinn, not within the window's call: a call whose answer is lost, or
a window that reloads, leaves it running, and the banner learns from Djinn how it ends. It may work 20 minutes, its
waits aside; past that, Djinn stops it. A failure, the deadline's included, shows on the build's line with why, the
step it was at, the last lines its command wrote under **Details**, and **Try again**. A click Djinn says nothing of
within 30 seconds, or a restart that does not come within 3 minutes, is said to have failed. `djinn.log` notes each
install asked, started and ended.

By default (`install_questions: always`), Djinn raises an "Install and restart" question for uninstalled builds. With
`install_questions: when_needed`, Djinn raises no routine question: the update banner and post-push install are enough.
It asks only when necessary, stating why in the question and context: when a build carries the fix for a task or question
you are waiting on (named), a change that takes effect after restart and matters now (such as a harness fix for failing
workers, naming them), or when the running build is broken (a crash or failed check). With `install_questions: never`
(set in Djinn's own settings: the window proposes each new build, to install or skip), no install questions are asked.

The commands' words are split on spaces, without a shell. The integration branch of a wish is, in each project, the
branch your checkout was on when the wish was made; `djinn wish set-integration <wish> --branch feat/x` changes it, or
the chip that names it in the wish's head, to a local branch (`djinn project branches <project>` lists them, Djinn's
task branches left out) or to a new one made from it (`--create-branch`). A change is refused while a merge into the
branch runs (a batch merged and tested, a correction or review worker, main's merge settled), Djinn naming it. Per wish,
a wish's work lands in that branch; per azima, an azima's work lands in main through its own branch and pull request.

## Keeping up with main

A wish's integration branch is a branch of its own (`feat/x`); others merge into the project's main branch meanwhile.
Djinn keeps the branch up with main by itself, no model, in each project whose settings name a check; per azima, it keeps
each azima's branch up the same way (an azima not done, not a draft), main fetched once for them all, a correction worker
part of the azima, and where the branch stands recorded on the azima. An azima's branch with nothing of its own beyond
main simply moves to main's commit:

1. **When it looks.** At most every `merge_main_minutes` (an hour by default), and at once when the running Djinn finds
   a new release, Djinn fetches main from its remote, with the tags, through git with your own credentials, never
   prompting for them; a repository without a remote looks at its local main. Main is `main_branch`, else the remote's
   default branch (`origin/HEAD`, as a clone records it), else `main`, else `master`. A wish whose integration branch is
   main itself has nothing to merge.
2. **When it merges.** By default (`MERGE_MAIN_RELEASE`), once main holds a release the branch lacks: a tag `v1.2.3`
   whose commit, or the commit it sits on, is in main (`task release` puts the built interface in a commit of its own
   on top of main, off any branch: that commit never comes in). `MERGE_MAIN_COMMIT` merges as soon as main holds any
   commit the branch lacks; `MERGE_MAIN_OFF` never merges.
3. **How.** As a task's branch is merged ([integration](#integration)): in the wish's integration worktree, never your
   checkout, `git merge --no-ff` of main's commit; a conflict only in `generated` files is settled by `generate`; the
   `setup` runs if needed, then each commit check through the gate of its name; green, the branch moves to the merge,
   git checking it is still where the merge started, and your clean checkout of it follows by a fast-forward (with
   changes not committed, it is left alone, and so is the branch, until they are committed or put aside).
4. **A conflict in code, or a red check**, leave the branch as it was and start a **correction worker**, as for a
   task: a work task of the project's provider, its worktree on the merge of main under way (its conflicts left in
   place), or on the merge where the check failed. Its branch integrates like any task's, and its success brings main
   in. Past `correction_attempts`, Djinn stops trying that merge: it merges main again once main moves.
5. **What it says.** Each merge is in the journal (`harness/main`, with main's commit, the commits it brought and the
   newest release among them); the wish's head shows the last one, "origin/main merged into feat/x: 4 commits, v0.2.0",
   its commits on hover, and why the next one waits while it does. A merge that brings a release into a project that
   names an `install` command proposes its build, as a push does. The merge goes to the remote with the next push.

**A merge, never a rebase.** A merge needs no model: git does it, and a model only settles a conflict, when there is
one. It keeps the branch's history as pushed: a rebase would rewrite every commit of the branch, which Djinn would then
have to push by force, which it never does; the people who fetched the branch, and its pull request's reviews, would
lose their place. The merge commit says which main came in, and when.

### Releases of Djinn, for those who build it

`go tool task install` builds the Djinn you use from your checkout of Djinn (version `local-<commit>`). That Djinn
watches its own file, as before, and the releases too, of the variant it would have been. When it finds a newer
release, it looks at the checkout its build comes from, in the project whose repository holds the build's commit:

- **On main** (your checkout is on the main branch, protected: nothing of yours is in the build that main lacks), a
  release that holds the build **installs by itself** when `install_releases` is on, the default: downloaded, verified
  and put at the path of the running Djinn, which then offers to restart on it. Nothing restarts without your click. A
  build with changes not committed (`local-<commit>-dirty`), or `install_releases: false`, only offers it.
- **A build that holds the release** already is offered nothing.
- **On a branch** with work the release lacks, the release is **never installed over the branch's build**: Djinn
  looks at main at once instead, and merges it into the wishes' integration branches as above; once the release is in,
  the window proposes the branch's build, with main's commits in **What changed**.
- **A release the checkout says nothing about** (no project holds the build, or its repository does not know the
  release) is offered, as to a Djinn installed from a release.

A release of Djinn starts from its notes, `docs/releases/<tag>.md`: the
[`release-notes`](../.agents/skills/release-notes/SKILL.md) skill says how to gather everything merged since the last
tag, group it by what the user gets, claim only what was checked, and which commands make the release (a dry run
first). Tagging and publishing stay the developer's moves.

## Left out, on purpose

Permissions stay in [`.agents/permissions.txtpb`](providers.md#the-agentspermissionstxtpb-format), templates in
the skills ([wish templates](wish-templates.md)), gates are named where they run (`djinn gate run <name>`). The
lead's agent, CPU limits and the machine's workers stay with each developer's `djinn up`.
The operating load cursor (minimal, medium, high, max, overclock, auto) is machine-wide and developer-controlled
(`djinn load get/set/watch`), not a repository setting in `.agents/settings.txtpb`.

## Cost estimates and model pricing

Djinn computes cost estimates for workers from an embedded price table (`internal/usage/prices.yaml`).

- **Estimated vs. reported cost**: When an agent's stream reports actual monetary cost (Claude Code), Djinn keeps and displays that cost labelled "reported" alongside the estimate. For other providers (Codex, Antigravity) that report tokens only, Djinn estimates cost using the embedded table, marked "estimated, prices of <table date>".
- **Local overrides**: The loader provides a seam for local overrides via `~/.config/djinn/prices.yaml` (or `$DJINN_HOME/prices.yaml`), allowing users to provide custom pricing tables in future updates without requiring CLI flags or UI today.

