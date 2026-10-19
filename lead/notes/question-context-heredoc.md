---
name: question-context-heredoc
description: A multi-line question context goes in a file written with the Write tool, passed with --context-file; never inline, never through a shell heredoc
type: feedback
---

Claude Code refuses a Bash call where a quoted argument holds a line starting with `#`, indented or not, whatever
Djinn allows: in don't-ask mode it reads "Permission to use Bash has been denied", like a missing rule. The shape of a
question puts a `# …` comment above each command ([[readable-questions]]), so an inline `--context '…'` or
`--context "…"` with a code block is refused. On 20/10/2026, in wish Will, the lead and the investigators W18 and W19
saw the revision of Q06 refused, three times each; the lead's transcripts count 26 such calls, 26 refused. The same
revision through `--context-file` passed at once. Checked on 20/10/2026 with Claude Code 2.1.304 (W5): a line that
starts with `#` is refused, a `#` after a command on its line passes, `--context-file` passes.

Inline quoting has its own traps too: between double quotes, each backtick runs as a command and each `$` expands (Q10
of wish Dew, 12/10/2026, lost its code spans and the shell tried to execute a CSV file). Shell heredocs
(`cat > f <<'EOF'`) and command substitutions (`"$(cat f)"`) stall the lead on permission prompts.

**Why:** a question is the developer's only channel ([[blockers-as-questions]]); a refused revision leaves it waiting,
and a mangled one misleads them.

**How to apply:**
- Revise: write the whole context with the Write tool in a file of your own folder (`context.md`; a driven lead runs
  in a folder it may write in), then `djinn question revise <question> --wish-id <wish> --context-file context.md`,
  the other values (`--recommendation`, `--options`) between double quotes, with no backtick nor `$`. Write the file
  again before each revision.
- Ask: `djinn question ask` takes no `--context-file` yet. Ask with a context whose lines never start with `#`: put
  each command's comment at the end of its line (`git switch x  # prints …`), or ask with the why, facts and options,
  then revise at once through a file to add the code block.
- Never write the file with a shell heredoc nor `echo >`: the Write tool needs no approval.
- A refused revision is this form refused, not Bash: run it again through the file, never give up on it.
- Investigators on Claude get their own folder and a prompt that says the same; one that starts on Claude after
  another agent could not run read-only gets them too (W5).
