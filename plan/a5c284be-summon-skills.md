---
id: 01a118f2-6c13-7079-8190-b542a5c284be
code: T23
phase: 3
status: in-progress
---

# T23 · Summon a skill from another project

**Goal.** Use a project's skill in another project without copying it. Djinn knows every
project of a wish, and the skills each one holds: it can bring one where it is missing.

## Decided
- **A summoned skill follows its source as it is** (Q42). Pinning a commit with a diff to accept can
  come later, if a need shows.
- **The word is "summon"** (« invoquer »). `djinn summon app/babysit-mr --into infra`.
  Easy to say aloud. The skill keeps its standard name: *skill*.
- **Nothing is copied.** A summoned skill stays bound to its source project and follows its
  updates.
- **A skill tied to a tool is its own project.** A MR babysitter depends on its forge (GitLab,
  GitHub): formalizing it well is a project in itself.
- **Each agent gets the skill by its own path**, without writing into the target project
  (cross-agent: see `CONTRIBUTING.md`).

## Done when
- [ ] A skill of one project is summoned into another, and an agent there uses it. Djinn hands
  it to each agent (tested with fake agents); a real agent using it is still to see. (needs: a real run of each
  agent, paid)
- [x] A change to the source skill reaches the target without a copy: a link to its folder.
- [x] Unsummoning leaves the target project as it was: nothing was ever written there.

## Decided along the way
- **The command is `djinn skill summon app/babysit-mr --into infra`**, with `djinn skill list`
  and `djinn skill unsummon app/babysit-mr --from infra`. Every command comes from a service
  (`SkillService`), by the command line convention: no code per command.
- **A summon lives on the project** (`Project.summons`): the source's project id and the skill's
  folder name. Any wish working in infra gets it.
- **A project's own skill wins.** Summoning a name the project already holds is refused, as is a
  second skill of the same name from another project.
- **Each agent by its own path, nothing in the project nor in the user's folders**
  ([providers](../docs/providers.md#skills-summoned-from-another-project)): Claude and
  Antigravity add a folder of Djinn's own, `<data>/skills/<task>`, of links to the sources;
  codex has no per-session skill folder, so it is told each `SKILL.md`'s path in its thread's
  developer instructions. Claude may read the sources and edit neither them nor the links.
- **A missing source never blocks a worker.** It starts without the skill, and the task says why.
- **The skill follows its source as it is** for now: the simplest, and what was decided first.

## Open questions
- A real run of each agent with a summoned skill, to turn the supposed rows of
  `docs/providers.md` into verified ones (Antigravity's `--add-dir` above all).
