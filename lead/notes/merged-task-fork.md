---
name: merged-task-fork
description: A task whose work merged has no worktree - continue is refused; fork it into its azima. And the spellings a call takes as typed
type: reference
---

`djinn task continue` resumes a task's session in the task's own worktree. Once its work merges into its azima's
branch, Djinn removes that worktree, so continuing it is refused. Fork it: `djinn task spawn <wish> --title '...'
--part-of T44 --fork W588 --after W588 --prompt '...'` starts a new task in the azima from W588's session. The refusal
prints this spawn with the task's azima, write scopes and your prompt, ready to run. For a prompt of several lines it
gives `--prompt-file <file>`: write the file with your Write tool first.

Every call takes these as typed:

| What | Takes |
| --- | --- |
| `--project-id`, `--project-ids` | a project's name (`djinn`), case ignored, or the start of only one name |
| a task (`W588`, `T44`) | its code; in several wishes, add `--wish-id <wish>` (or `--wish`) |
| a wish | its UUID, its title, or the start of only one title |
| a spawn's project | `--part-of <azima>` alone: the part works in the azima's project, an azima part of one too |

In a wish of several projects, a spawn without `--project-id` or `--part-of` is refused. The refusal prints the command
once per project, ready to run.

**Why:** on 07/11/2026 at 00:28 and 00:29, two leads ran a Djinn built before W474 (T45's branch was not merged yet).
Seven calls failed: `--project-id djinn` (a name where a UUID was expected), spawns without a project in a wish of
three, and `continue W588` (no `--wish-id`, no code, then no worktree: W588 had merged into T44's branch). The leads
read only `--fork W588` in the refusal, which names no project and so is refused in a wish of three. W592 made the
refusal print the whole spawn.

**How to apply:** after a task merges, never continue it: fork it into its azima, with the command the refusal
prints. If a call is refused, run the command the refusal gives before you rewrite it yourself.
