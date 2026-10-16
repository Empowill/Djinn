---
name: readable-questions
description: A question's context reads at a glance - every command in one code block at the top, then a one-line why, bullets of facts, the decision
type: feedback
---

A question's context has one shape. First, every command the developer must run, in one fenced code block, in run
order, a `# …` comment above each saying what it prints and what to expect. Then short Markdown: a one-line why, a few
bullets of facts (verified or not), the decision with its options. No command, flag, path or hash inside a sentence:
it goes in the code block (a branch or a commit as a comment) or in a table. No paragraph over three lines.

**Why:** on 13/10/2026 the developer got revisions of questions, after an "Enlighten me", that read as one long
paragraph with the commands to run lost inside: "you often send me unreadable question revisions, with the commands
you expect from me getting lost: that will not do at all!" (W575).

**How to apply:**
- Fence the block with `~~~` lines: they need no backtick, so a context written between double quotes (an
  investigator's `--context "…"`) keeps it. Elsewhere, write the context through a quoted heredoc
  ([[question-context-heredoc]]).
- `djinn question ask` and `djinn question revise` print a `warning:` line for each break of the shape (a command or a
  flag in the text, a path or a hash in a sentence, commands in two blocks or after text, a paragraph over 400
  characters, 800 characters with no list): revise the context at once, and check what an investigator revised.
- A question with nothing to run has no code block: it starts with its why.
- Short, too: outside its code block a context fits on one screen, its details in a tilasm it links
  ([[short-questions]]).

The developer's example, a revised context (translated from French; the commands and options as they were):

````markdown
Branch: ET-3510-old-role-types (local and on origin; commit 5d254fef6 at its head for ET-1624). Run git switch ET-3510-old-role-types, or a worktree on it, then from the repo root. 1) Dry-run migrate-system-roles (exists ONLY on the branch, writes nothing): go run ./cmd/stork migrate-system-roles --dry-run --pg_url="postgres://:@:/syn?sslmode=disable" . It lists the references (company, system role) and ends with a line: migrate-system-roles dry-run: N references to move, M system role rows to delete. 2) Dry-run cleanup-legacy-roles (…): go run ./cmd/stork cleanup-legacy-roles --dry-run --pg_url="…" . Last line: … If the dry runs find something (B): run the SAME commands again without --dry-run … Guard: never migrate-system-roles from main … A: dry runs done, 0 and 0; B: references found, migrations applied; C: we wait.
````

The same context in the shape (the why and the facts are illustrative):

````markdown
~~~sh
# Branch ET-3510-old-role-types, head 5d254fef6 (ET-1624). From the repo root,
# on this branch only: the commands below do not exist on main.
git switch ET-3510-old-role-types

# 1. Dry run, writes nothing: lists each reference (company, system role), then ends with
#    "migrate-system-roles dry-run: N references to move, M system role rows to delete"
go run ./cmd/stork migrate-system-roles --dry-run --pg_url="postgres://:@:/syn?sslmode=disable"

# 2. Dry run, writes nothing: its last line gives what it would remove
go run ./cmd/stork cleanup-legacy-roles --dry-run --pg_url="postgres://:@:/syn?sslmode=disable"

# 3. Only when step 1 or 2 counts more than 0 (answer B): the same commands, without the dry run
go run ./cmd/stork migrate-system-roles --pg_url="postgres://:@:/syn?sslmode=disable"
go run ./cmd/stork cleanup-legacy-roles --pg_url="postgres://:@:/syn?sslmode=disable"
~~~

**Why:** before the branch merges, the dry runs say whether the database still holds old role types to move.

- Verified: the branch is on origin, its head carries the fix for ET-1624.
- Not verified: what the database holds; only the dry runs on it tell.

**Your answer**

- **A**: dry runs done, both counts at 0: nothing to migrate.
- **B**: references found, both migrations applied.
- **C**: not now, we wait.
````

See also [[blockers-as-questions]], [[question-context-heredoc]], [[short-questions]].
