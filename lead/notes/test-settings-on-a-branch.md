---
name: test-settings-on-a-branch
description: How a project's djinn.yaml (settings, gates, containers) and skills are tried from a branch before they merge into a protected branch, by the developer's own config_branch (djinn project set-branch --config), permissions staying on main (W178, L37 A, Q110)
type: reference
---

Djinn reads a project's `djinn.yaml` and skills on its reference branch (`origin/main`): a change committed on a wish's
branch does nothing until it merges, often behind a review on a protected branch. To try it before, the developer
points their own Djinn at the branch that holds it, on their machine only:

```
djinn project set-branch dolmen djinn/T17-dolmen-gate-stack --config
djinn project show dolmen
djinn project set-branch dolmen --config --unset
```

The first writes `config_branch` in the developer's own settings file (never the repository's: a team's
`config_branch` is ignored). Djinn then reads `djinn.yaml` (settings, gates, containers) and the skills on that
branch, as the copy last fetched it; `djinn project show` gives `reference.config_ref` and an information notice
"Configuration read from origin/<branch>, not origin/main", with the command that takes it back.
`.agents/permissions.txtpb` and `main_branch` stay read on the reference branch: an agent that commits to the branch
widens no right, moves no request base. Pull requests, `merge_main`, the merged detection and worktrees keep main.

The branch must be on the remote and fetched when it is set (else refused: the wish's push puts it there). Once it
merged and was deleted, Djinn reads main again by itself; a warning notice ("not on the remote") stays until the
developer unsets it.

**Why:** Q106/Q107: the developer wanted to try djinn's and dolmen's gate stacks before their MR or PR merged into a
protected main (tilasm L37, option A); Q110: a branch gone falls back to main.

**How to apply:** when a wish changes a project's `djinn.yaml` or skills and the developer wants to see it run before
the merge, ask a `--move` question with the first command above, the wish's integration branch named, once Djinn
pushed it; never run it yourself in the developer's Djinn. Remind them to unset it once the PR or MR merged. A worker
checks a file first in a scratch Djinn (its own `DJINN_HOME`), never in the developer's.
