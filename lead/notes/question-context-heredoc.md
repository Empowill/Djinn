---
name: question-context-heredoc
description: Backticks or $ in a double-quoted --context run as shell commands; pass --context '...' in single quotes with ~~~ fences
type: feedback
---

`` djinn question ask ... --context "...`file`..." `` in a shell runs each backticked word as a command substitution (and
expands each `$`): Q10 of wish Dew (12/10/2026) lost its code spans, and the shell tried to execute a CSV file. Shell
heredocs (`cat > f <<'EOF'`) and command substitutions (`"$(cat f)"`) also trigger manual approval prompts in the lead's
terminal. Pass `--context '...'` in single quotes instead, using `~~~` fences and avoiding backticks or single-quote
escaping issues.

**Why:** a question is the developer's only channel ([[blockers-as-questions]]); a mangled one misleads them, and shell
redirections or command substitutions stall the lead on permission prompts.

**How to apply:** pass `--context '...'` in single quotes as a direct `djinn` argument, fence the code block of commands
with `~~~` lines ([[readable-questions]]), and avoid backticks or apostrophes inside the single-quoted string. Fix a
mangled question with `djinn question revise <code> --context '...'`.
