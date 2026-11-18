---
name: direct-commands
description: Never pipe, chain, loop, redirect or substitute around djinn; each is a manual approval prompt. The direct flags that replace them, and the right form of the calls Djinn refused
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
| `djinn task watch <task> \| tail`, `timeout 5 djinn task watch ...` | `djinn task watch <task> --last 5`: its last events, its final note among them, at once even while it runs |
| `djinn question list ... \| grep` | `djinn question list --wish-id <wish> --open`, or the brief's open questions |
| `djinn question list --wish-id <wish>` to read one answer | `djinn question get <question> --wish-id <wish>`: that question as it stands, its answer and note |
| `djinn task watch <task>` for a note the digest cuts | `djinn lead event <event>`, the command the digest prints under the cut note |
| `cat > f <<'EOF'` then `"$(cat f)"` | `--context '...'` in single quotes ([[question-context-heredoc]]) |

**Why:** on 15/10/2026 an Antigravity lead's `run_command` kept only the end of `djinn brief` (about 260 lines, its
first 181 cut): it lost both the active wishes and the rule on direct commands, then ran `djinn brief | head -n 200`,
`djinn brief | sed -n ...` and a `for` loop over `djinn lead notes`, each a permission prompt in the developer's
terminal. The brief now starts and ends with the rule, and ends with a line per active wish.

**How to apply:** when an output is too long, cut it with a flag, never with a pipe; when a flag is missing, run the
commands one by one, then spawn a task that adds the flag to Djinn and a line to this note.

## Calls refused for their form, and the form that passes

The lead's calls refused from 16 to 18/11/2026 (W117), read in its transcripts: about a hundred in three days. Djinn now
takes the first one as typed; the others are the lead's to write right the first time.

| Refused | Passes |
| --- | --- |
| `project-id: dolmen` in a plan (`--plan`, `--plan-file`), "not one of the wish's" though the wish had dolmen | Passes since W117: a plan names its project by name, as `--project-id` does. When the wish has not the project, the refusal gives `djinn wish add-project` |
| `djinn task spawn <wish> --kind azima ... --tilasm L95` | An azima has no worker: no `--tilasm`, `--prompt`, `--write-scopes`, `--minutes`; link the tilasm in `--description` (`djinn://tilasm/<id>`) and give it to the parts |
| `djinn task spawn <wish> --part-of T92 --after W875` (W875 part of T85), in a wish that pushes per azima | A task waits only for tasks of its own azima: drop the `--after`, or spawn it `--part-of T85` |
| `djinn task spawn` or `--kind azima` with no `--project-id` in a wish of several projects | Always give `--project-id` (or `--part-of` an azima) when the wish has several projects |
| `djinn question launch Q12 --write-scopes ...` or `--urgent` | Not flags of `launch`; `djinn question launch --help` lists what it takes in place of the plan's |
| `djinn question revise Q12 --text ...` or `--title ...` | `revise` changes context, options, recommendation and plans, never the question's text: ask a new question |
| `djinn question launch Q12` with a code in several wishes | Add `--wish-id <wish>` |
| a Write in `/tmp/...`, a `grep` on a tool result file | Write only in your own folder, the session's working directory; read a long output with a flag of the command, never with `grep` |
