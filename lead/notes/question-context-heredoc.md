---
name: question-context-heredoc
description: Backticks or $ in a double-quoted --context run as shell commands; write a question's context to a file with a quoted heredoc
type: feedback
---

`` djinn question ask ... --context "...`file`..." `` in a shell runs each backticked word as a command substitution (and
expands each `$`): Q10 of wish Dew (12/10/2026) lost its code spans, and the shell tried to execute a CSV file. Write
the context to a file with a quoted delimiter, `cat > f <<'EOF' ... EOF`, then pass `--context "$(cat f)"`.

**Why:** a question is the developer's only channel ([[blockers-as-questions]]); a mangled one misleads them.

**How to apply:** any Markdown with backticks or `$` goes through a quoted heredoc file. Fix a mangled question with
`djinn question revise <code> --context "$(cat f)"`.
