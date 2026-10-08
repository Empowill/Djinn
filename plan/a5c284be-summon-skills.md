---
id: 01a118f2-6c13-7079-8190-b542a5c284be
code: T23
phase: 3
status: open
---

# T23 · Summon a skill from another project

**Goal.** Use a project's skill in another project without copying it. Djinn knows every
project of a wish, and the skills each one holds: it can bring one where it is missing.

## Decided
- **The word is "summon"** (« invoquer »). `djinn summon app/babysit-mr --into infra`.
  Easy to say aloud. The skill keeps its standard name: *skill*.
- **Nothing is copied.** A summoned skill stays bound to its source project and follows its
  updates.
- **A skill tied to a tool is its own project.** A MR babysitter depends on its forge (GitLab,
  GitHub): formalizing it well is a project in itself.
- **Each agent gets the skill by its own path**, without writing into the target project
  (cross-agent: see `CONTRIBUTING.md`).

## Done when
- [ ] A skill of one project is summoned into another, and an agent there uses it.
- [ ] A change to the source skill reaches the target without a copy.
- [ ] Unsummoning leaves the target project as it was.

## Open questions
- Follow the source as it moves, or pin a commit and show the diff before updating? Skills can
  carry code.
- Where Djinn exposes a summoned skill to each agent (Claude, Codex, Antigravity) without
  touching the project.
