# The lead's notes

How to lead wishes with Djinn, learnt from past wishes: what the developer expects of a lead, and why. Any lead reads
them, whatever its agent (Claude, Codex, Antigravity), and any contributor may add or fix one by pull request, like any
doc.

- One note per Markdown file of [`notes/`](notes), in English, named after the note: `notes/<name>.md` (`notes.go` reads them).
- A front matter, then the note:

  ```markdown
  ---
  name: <name>
  description: <one line: what it is about, for a lead to tell whether to open it>
  type: feedback | project | reference
  ---

  The rule or the fact. Then **Why:** (the incident or the developer's words) and **How to apply:**.
  ```

- `[[other-name]]` links to another note.

Djinn builds the folder into its binary (`go:embed`): at run time the notes are read only. The brief gives every lead
the index, a line per note with its name and description, never the notes themselves; `djinn lead notes` prints the
index, `djinn lead notes <name>` one note, which a lead opens before it acts in that note's area, and `djinn lead notes
a,b` or `djinn lead notes --all` several in one command. A lead that may
change Djinn adds or fixes a note by spawning a task that edits this folder; nothing writes here at run time.

The pitfalls of the codebase, for workers, are not lead notes.
