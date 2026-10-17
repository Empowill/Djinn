# Skills and templates

A skill is what a project's agents know how to do: a job that comes back, written down once. It is a folder in the
project, in the open Agent Skills format: a page that says when to use it and how, and maybe the scripts and
references it needs. Djinn reads a project's skills, and a skill may tell Djinn more: a **wish template** that turns
a recurring request into a wish that already knows how to work, a **babysit** that watches the pull requests the
project's tasks open, and a **source** that feeds the [inbox](#inbox).

## Why they exist

Some work comes back every week: babysit a pull request until it is merged, run the QA of a feature, empty a queue of
tickets. Explaining it to an agent each time costs your words and its tokens, and each time it is done a little
differently.

A skill says it once, in the repository, where every worker of the project finds it and every teammate's Djinn reads
the same thing. Its extra lines for Djinn need no code: a few lines declare the template, and Djinn does the rest.

## Where Djinn finds them

A project's skills are the folders of its agents' skills directory, or of Claude's: when the same name is in both,
the agents' one wins. Each skill has a name and a one-line description.

To see them, click a project under **Projects** in the side panel: its panel lists its **Skills**, each with its
description, or why it cannot be found. **No skill in this project.** when it has none.

The workers find the project's own skills in their worktree, as their agent does in any repository.

### Summoning another project's skill

A project can **summon** a skill of another project: its workers then use it as if it were their own, from their next
start, without a copy. Djinn gives them the skill where it lies; nothing is written in either project. The skill keeps
following its source: a fix there reaches every project that summons it.

A summoned skill shows in the project's panel with **from** and its source. Whether a project summons a skill is kept
in Djinn's data on this machine, not in the repository. A project cannot summon a skill whose name it already has.

If the source is gone, the worker starts without it, and its task says so.

## Wish templates

A wish template turns a request that comes back into a wish that already knows how to work. A skill declares it:

- **The words it recognises**, such as "babysit" followed by a pull request's number.
- **The title** of the wish it makes, filled from those words: "Babysit PR #12".
- **A watcher**, if it has one: a command that follows the work, with no agent and no model. See [Watcher](#watcher).
- **Its done line**: what the watcher prints once the work is done, "MERGED" for instance.

### What happens

1. A request reaches Djinn: you ask the Djinn for something outside its wish, or an inbox item is routed.
2. Djinn asks you **Where does this request go?**, and a request that matches a template proposes a new wish made
   from it, **from the skill** named: its reason says that the skill's watcher starts at once and the Djinn follows the
   skill. A wish already on the same piece of work, the same pull request, still takes the request.
3. You rub the lamp. The wish is made, its watcher starts in the skill's project, and the window opens it. The Djinn is
   told to read the skill and follow it.
4. Each change the watcher prints, a failed check, a new comment, wakes the Djinn, which acts on it as the skill says.
5. When the watcher prints its done line, Djinn asks you whether to grant the wish: **Grant the wish** or **Keep it
   open**. Djinn never grants a wish by itself.

Templates are only for routed requests: a wish you make with **New wish** never takes one.

## Babysitting what a task opens

A skill may also say how to babysit the pull or merge requests a task opens. Once a task of the project ends done
having opened one, Djinn finds it and starts a watcher on it in the same wish, part of the task's azima, and tells the
Djinn. Each change the watcher reports wakes the Djinn, which follows the skill; the watcher ends once the request is
merged. A request already watched is not watched twice.

Only a project's own skills babysit, never a summoned one, and a project's settings can turn it off.

## Sources: what comes from outside

A skill may declare a source: a command that prints what comes from outside, the merge requests assigned to you, a
mention in a thread. Each item becomes a card in the [inbox](#inbox), with the route Djinn proposes. A source runs only
once you plug it in, on this machine, from its project's panel: declaring one in a repository runs nothing on a
teammate's Djinn. Djinn only
reads a source; it never answers, comments or reacts.

## What to expect

- **Nothing to code per template.** A template is a few lines in a skill; Djinn makes the wish, starts the watcher and
  asks you at the end.
- **The whole team gets them.** Skills live in the repository: every Djinn on every machine reads the same ones.
- **A watcher's command must be allowed.** A watcher has no reviewer. Its command runs when the project lists it among
  the commands its workers may run, when a skill of the project itself declares it, or when you allow it for yourself
  in the project's panel, **Watcher commands you allow**. See [Watcher](#watcher).

## Common mistakes

- **A template that never matches.** A template with a mistake in it is skipped without a word when a request is
  routed. Ask the Djinn to list the project's skills: it shows each template, or why it cannot be used.
- **A summoned template's watcher in the wrong folder.** It runs in the folder of the project that summons the skill:
  its command must be found there, not only in the skill's own project.
- **Expecting a summoned skill to babysit.** Babysitting reads the project's own skills only.
- **A refused watcher.** A watcher whose command no rule allows fails at its start, and its error says what would let
  it run. A command you typed by hand is not "declared" even if it starts the same way.
- **Declaring a source and waiting for items.** It runs once plugged in, from its project's panel, on your machine.

## Read next

- [Watcher](#watcher): the commands that follow a template's work.
- [Inbox](#inbox): where a source's items arrive.
- [Pull requests](#pull-requests): what Djinn does with the requests a task opens.
