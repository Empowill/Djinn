# Team settings

A team that works on one repository shares a few defaults for its workers: which agent runs them, which model,
how much one may spend. The repository carries them in one file, next to the permissions:
`.agents/settings.txtpb`. Each developer may keep their own next to their Djinn's data, and theirs win.

## The file

A `plan.v1.ProjectSettings` in text protobuf ([`api/plan/v1/plan.proto`](../api/plan/v1/plan.proto)), committed
with the code:

```
# proto-file: api/plan/v1/plan.proto
# proto-message: plan.v1.ProjectSettings

# The team's workers run claude with opus, and spend at most 3 dollars each.
provider: PROVIDER_CLAUDE
model: "opus"
max_budget_usd: 3
```

| Setting          | What it sets, when a task names none                                   | Not set                    |
| ---------------- | ---------------------------------------------------------------------- | -------------------------- |
| `provider`       | The agent of the project's workers: claude, codex, antigravity, fake.  | claude                     |
| `model`          | Their model, a model of that provider. `""`: the provider's default.   | the provider's default     |
| `max_budget_usd` | The most one worker may spend, when its provider can enforce it. `0`: no limit. | no limit          |

A watcher (`--provider watch`) runs a command: no setting applies to it, and none can make one.

## Your own file

Your Djinn keeps yours in its data folder: `projects/<project id>/settings.txtpb` (by default under
`~/.config/djinn`). Same format, never committed. `djinn project show <project>` prints its path.

## Who wins

Setting by setting, the first that sets it:

1. **The task's own flags**: `djinn task spawn --model … --max-budget-usd …`, or what the lead asked.
2. **Your own file.**
3. **The repository's file.**
4. **Djinn's default**: claude, its default model, no limit.

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

## Left out, on purpose

Permissions stay in [`.agents/permissions.txtpb`](providers.md#the-agentspermissionstxtpb-format), templates in
the skills ([wish templates](wish-templates.md)), gates are named where they run (`djinn gate run <name>`). The
lead's agent, warm workers, CPU limits and the machine's workers stay with each developer's `djinn up`.
