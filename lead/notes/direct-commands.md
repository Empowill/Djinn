---
name: direct-commands
description: Never pipe, chain, loop, redirect or substitute around djinn; each is a manual approval prompt. The direct flags that replace them
type: feedback
---

Run each `djinn` command alone, as one direct command: no pipe (`| head`, `| tail`, `| sed`, `| grep`), no chain
(`&&`, `||`, `;`), no loop (`for n in ...; do ...; done`), no redirection (`>`, `<<`) and no substitution (`$(...)`, or a
backtick between double quotes). The lead's agent pre-authorizes `djinn` commands one at a time: anything around one
makes the developer approve it by hand in the lead's terminal, which they never read.

Each need has a direct flag instead:

| Instead of | Run |
| --- | --- |
| `djinn brief \| head -n 200`, `djinn brief \| sed -n ...` | `djinn brief --wishes`: where the wishes stand, without the rules |
| `for n in a b; do djinn lead notes "$n"; done` | `djinn lead notes a,b`, or `djinn lead notes --all` |
| `djinn task list --wish-id <wish> \| grep ...` | `djinn task list --wish-id <wish> --short` (`--failed`, `--page-size`) |
| `djinn task watch <task> \| tail` | `djinn task watch <task> --last 5`: its last events, its final note among them |
| `djinn question list ... \| grep` | `djinn question list --wish-id <wish> --open`, or the brief's open questions |
| `cat > f <<'EOF'` then `"$(cat f)"` | `--context '...'` in single quotes ([[question-context-heredoc]]) |

**Why:** on 15/10/2026 an Antigravity lead's `run_command` kept only the end of `djinn brief` (about 260 lines, its
first 181 cut): it lost both the active wishes and the rule on direct commands, then ran `djinn brief | head -n 200`,
`djinn brief | sed -n ...` and a `for` loop over `djinn lead notes`, each a permission prompt in the developer's
terminal. The brief now starts and ends with the rule, and ends with a line per active wish.

**How to apply:** when an output is too long, cut it with a flag, never with a pipe; when a flag is missing, run the
commands one by one, then spawn a task that adds the flag to Djinn and a line to this note.
