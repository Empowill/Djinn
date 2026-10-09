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

# Djinn integrates their finished work by itself: go tool task gen makes gen/** and docs/openapi.json, go tool task
# test tests the project.
generated: "gen/**"
generated: "docs/openapi.json"
generate: "go tool task gen"
test: "go tool task test"
```

| Setting          | What it sets, when a task names none                                   | Not set                    |
| ---------------- | ---------------------------------------------------------------------- | -------------------------- |
| `provider`       | The agent of the project's workers: claude, codex, antigravity, fake.  | claude                     |
| `model`          | Their model, a model of that provider. `""`: the provider's default.   | the provider's default     |
| `max_budget_usd` | The most one worker may spend, when its provider can enforce it. `0`: no limit. | no limit          |
| `branch`         | The branch of a worker's worktree, a template: see [below](#branch-names). | `{code}-{slug}-{uuid8}` |
| `generated`      | The files code generation makes, as globs (`gen/**`), repeated: see [integration](#integration). | none |
| `generate`       | The command that makes them, in the project's folder.                   | none                       |
| `test`           | The command that tests the project, in the project's folder. Set, Djinn integrates finished work. | none: no integration |

A watcher (`--provider watch`) runs a command: no setting applies to it, and none can make one.

## Branch names

In a Git repository each worker edits its own worktree, on a new branch from the project's `HEAD`. `branch` names it,
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

## Integration

A worker's work counts once it is in its wish's branch, tested. In a project whose settings name a `test` command,
Djinn brings it there by itself, no model ([T30](../plan/43303f46-integration.md)):

1. When a worker ends done, its task's work is **pending** (`djinn task get` shows `integration`).
2. Djinn commits a **batch** when an azima ends (none of its parts is planned or under way any more), or once an hour
   has passed and three tasks are done since the last commit, whichever comes first. A task another task waits for
   is committed at once, alone. `djinn wish set-integration <wish> --commit-after-minutes 30 --commit-after-tasks 2`
   changes the hour and the count, for that wish.
3. In a worktree of its own per wish and project, under Djinn's data folder (`projects/<project id>/integration/<wish
   id>`), detached at the integration branch's tip, never in your checkout: it commits what each worker left in its
   worktree on the task's branch (its title as the message; workers never commit), then merges each branch with
   `--no-ff`, in the order the tasks ended. A conflict only in `generated` files takes the task's side and runs
   `generate` under the gate `gen`. Then `test` runs under the gate `test`.
4. **Green**: the integration branch moves to the result, Git checking it is still where the batch started; each task
   is **committed**, with the commit, and the journal records the batch. Your checkout of that branch, clean, follows
   by a fast-forward. With changes not committed, it is left as it is, and so is the branch: moved under it, your
   next commit would undo the batch. The tasks stay pending, saying why, and the branch moves once your changes are
   committed or put aside, without testing again.
5. **Red**, or a **conflict** in code: the branch stays as it was, and each task of the batch says what failed.

The commands' words are split on spaces, without a shell. The integration branch of a wish is, in each project, the
branch your checkout was on when the wish was made; `djinn wish set-integration <wish> --branch feat/x` changes it.

## Left out, on purpose

Permissions stay in [`.agents/permissions.txtpb`](providers.md#the-agentspermissionstxtpb-format), templates in
the skills ([wish templates](wish-templates.md)), gates are named where they run (`djinn gate run <name>`). The
lead's agent, warm workers, CPU limits and the machine's workers stay with each developer's `djinn up`.
