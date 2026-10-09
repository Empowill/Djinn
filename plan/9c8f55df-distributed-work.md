---
id: 01a11880-4a06-7df5-947e-08089c8f55df
code: T15
phase: later
status: open
after: T07 T17 T18 T24
---

# T15 · Work spread over trusted machines

**Goal.** Later, after v1: a mission hands tasks to other machines its owner trusts, and shares
its flight plan with them, semi-automatically, project by project.

## Principles already set
- **A machine can take a task for a project only if it can contribute to that project**: it can
  clone it and push a branch to it. Being trusted by Djinn is not enough.
- **A project that is not a Git repository is not distributable.** Its tasks always run on the
  machine that holds the folder.
- Trusted machines are listed per project; every event carries its machine id; shared events are
  signed by the machine that made them, and replayed from a sequence number.
- The services that serve the window today serve a peer tomorrow, over TCP with mutual TLS.
- Never a secret in what travels between machines.


## Building blocks found
- Group membership and failure detection: `hashicorp/memberlist` (MPL-2.0, used as an unmodified
  dependency), with our own mutual-TLS transport.

## Agreeing on who runs what: what we know so far
- **A consensus algorithm (Raft, Paxos) does not choose which task to run**: the scheduler does.
  It makes several machines agree on the same log of decisions while some of them fail, so that no
  task runs twice.
- **On one machine there is nothing to agree on**: one process writes, SQLite orders every
  decision.
- **On a few trusted machines, a coordinator with leases may be enough**: the machine that holds
  the wish assigns each task with a lease and a fencing number; a worker that loses its lease
  stops, and a stale write is refused. The coordinator is a single point of failure, but the wish
  lives on its machine anyway.
- **Raft needs a majority**: three machines to survive one failure; with two, it survives none.
  Candidate: `hashicorp/raft` (MPL-2.0, used as an unmodified dependency).

## Open questions, to answer later
- **Rights to redefine.** On one machine, a worker's rights come from where it runs (the
  project's agent config, the wish's allowance, read-only outside a project). Sent to another
  machine, who allows what? The wish's allowance (edit, auto mode) was given by a person for their
  machine: does it travel, and does the other machine's owner have a say?
- A coordinator with leases, or Raft from the start? Does a wish need to survive the loss of its
  home machine without a human?
- How long is a lease, who renews it, and what does a worker do with half-finished work when it
  loses it?
- What travels between machines: the command journal, the entities, or both? Signed by whom, and
  replayed from which sequence number?
- How does a machine prove it can contribute to a project before it is offered a task (clone and
  push rights, the right toolchain, enough resources from T17)?
- What happens to a machine that goes offline in the middle of a task, and to its worktree?
- Clocks: leases and logs across machines need more than wall-clock time; do we use fencing
  numbers only, or a hybrid logical clock?
- Whose account pays when a task runs on another person's machine (see T18 for provider terms)?

## Done when
- [ ] A task of a Git project runs on a second trusted machine, which pushes its branch; the
      mission on the first machine follows it live. (needs: after v1; two trusted machines)
- [ ] A task of a non-Git project is never offered to another machine. (needs: after v1; nothing built)
