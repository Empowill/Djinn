---
name: watchers
description: Set a watcher right the first time - the project must allow its command (a wish's allowance adds none), the project's versioned tool, never a copied script, a state that survives restarts, --babysit only where a skill declares it
type: feedback
---

A watcher (`--provider watch`) has no reviewer: Djinn runs its command only when the project lets it, and judges the
command once its variables are expanded. Before you spawn one, know which of these lets it run:

| What lets it run | How to tell | How to spawn |
| --- | --- | --- |
| A skill of the project declares it (`metadata.djinn.babysit`, `metadata.djinn.wish`) | `djinn skill list --project <project>` shows each skill's babysit or template command | `--babysit` (or the template's wish): Djinn builds the command itself |
| The project lists it in its `.agents/permissions.txtpb` (`commands`) | the file's `commands`, a start of the command each; no file, no command | `--provider watch --prompt '<command>'` |
| The developer allows it for themselves | `djinn project allow <project>` lists those commands | `--provider watch --prompt '<command>'` |

None of them: the watcher fails at start, and its error says what the project lacks and the exact command that would
let it run. Never spawn it to see. A wish's allowance (`djinn wish allow`, edit or auto) adds no command: in a project
with no `.agents/permissions.txtpb`, a command typed by hand runs nowhere but in the developer's own allowance. The
project's denied commands win over all three.

**A command no one allows.**

- For a look at one time (a branch, a pipeline's state, a request's threads): an investigation (`--kind
  investigation`), whose agent runs its commands under review. A watcher is for what lasts.
- For a lasting watch: a `--move` question with the developer's exact command in its code block, `djinn project allow
  <project> --watch '<the command, or its start before the first option>'`, the start the refusal names. It stays in
  their own settings, never in the repository. Spawn the watcher once they say it is done.
- For a project you may change, and a watch that comes back (each request it opens, each run of the same kind):
  propose a task that declares it in one of the project's skills (below); the declaration lets every Djinn run it,
  whoever leads.

**`--babysit` needs a declaration.** It is refused when no skill of the project declares `metadata.djinn.babysit`:
babysitting by hand, a worker that polls the request, costs a slot and tokens each time. The declaration, in the
`SKILL.md` front matter of a skill of the project itself (a summoned skill's does not count):

```yaml
metadata:
  djinn:
    babysit:
      forge: gitlab
      watch: "go run ./.agents/skills/<skill>/<tool> -mr {mr} -watch -state {state}"
      restart: true
      done_when: MERGED
```

`{pr}` or `{mr}` is the request's number (one is required), `{state}` a file of the watcher's own that outlives its
runs; `forge` (`github` or `gitlab`) only when the project declares one of each. `djinn skill list` says whether it
reads, or why not. Djinn then babysits each request a task opens, by itself (`docs/wish-templates.md`).

**The command is the project's versioned tool.** A program or script of the repository, the one its skill names (`go
run ./...`, `sh .agents/skills/<skill>/watch.sh {pr}`), run in the project's folder. Never a script copied from a
tilasm, a note or another Djinn into a local folder: it drifts from the skill, nothing updates it, and nobody reviewed
its copy. When the tool lacks something, a task changes the tool in the repository.

**A watcher that restarts keeps a state.** Djinn starts a dying command again, and `--restart` starts again one that
exits on each change (a `-watch` that returns once something moved): without a state, each run says everything again.
Pass the tool its state file, `{state}` in a declaration, or by hand a file of the watcher's own folder, made before
it starts:

```
djinn task spawn <wish> --title 'Watch !41' --provider watch --restart --prompt '<tool> -mr 41 -watch -state "$DJINN_HOME/watchers/$DJINN_TASK_ID/mr-41"'
```

A tool with no state option is changed by a task first. A command that keeps running and prints on each change needs
no `--restart`.

**A command given to copy is plain ASCII.** In a prompt, a question's code block or a note, quotes are `'` and `"`,
never typographic quotes, which a shell reads as letters: the command fails, or does something else.

**Why:** a lead spawned a watcher on a project with no permissions file, trusting the wish's allowance; it failed at
start, and the lead fell back on an investigation. `--babysit` was refused on a project whose babysit skill declared
nothing, so a worker babysat the request by hand. Another lead, with no declaration to follow, gave the developer a
shell script copied from a tilasm into a local folder, its commands in typographic quotes: it duplicated the skill's
versioned tool, had no state to survive a restart, and failed once pasted (decision Q501, 16/11/2026: teach any lead
to set a watcher right, beyond babysits).

**How to apply:** read this note before any `--provider watch` or `--babysit` spawn. Name the command's tool from the
project's skill, give it its state, settle who allows it, then spawn. See also [[babysit-conflict]],
[[direct-commands]].
