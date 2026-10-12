---
name: release-notes
description: Write the notes of Djinn's next release (docs/releases/<tag>.md) from everything merged since the last
  tag, grouped by what the user gets, each claim checked, then hand the developer the dry run and the release command.
  Never tags, never publishes. Use when asked to write, complete or check release notes, a changelog, or to prepare a
  release.
---

# Write the release notes

Release notes are what a stranger reads before installing Djinn: everything that changed since the last release, said
for a user, nothing claimed that nobody saw work. The model is the latest file in
[`docs/releases/`](../../../docs/releases/) ([v0.1.1](../../../docs/releases/v0.1.1.md)); keep its shape and voice.

## Gather what changed

The range is the last tag to what main will hold when the release is made: main, plus any open pull request into it
that the release waits for (the notes themselves reach main through one).

```sh
git fetch origin --tags
git describe --tags --abbrev=0 origin/main                     # the last tag, e.g. v0.1.1
gh release view <last-tag> --json body -q .body                # what the last notes already said
git log --merges --format='%s' <last-tag>..origin/main         # one merge per task: W<n> and the start of its title
git log --no-merges --format='%h %s' <last-tag>..origin/main   # the commits, `<area>: what it changes`
git diff --stat <last-tag>..origin/main -- docs/ plan/         # what the docs and the plans' proof boxes say now
```

- A merge's branch is cut from its task's title: `djinn task list --project-id <id> --json` gives the whole title.
- The why lives in the decisions: `djinn question list --wish-id <wish> --since <tag's date>` (answered questions)
  and `djinn block list <wish> --kind decision`. Read them before deciding what a change is for.
- What a user sees is in [`docs/user-guide.md`](../../../docs/user-guide.md): a capability it does not describe is
  either internal or not finished; check the code before writing it.

## Group by capability

One section per thing the user gets, never per task, per commit or per area of the code. Twenty tasks may make one
section (a new look); one task may make one (a log to send when Djinn is stuck). Leave out what no user notices: merges,
conflicts settled, CI repairs, test speed. Those go in **Under the hood** in one or two lines at most, or nowhere.

## The template

```markdown
# Djinn vX.Y.Z

<one paragraph: what this release is, in a sentence or two; what Djinn is, for a newcomer>

## Install
<the one-line installs of the README, unchanged; a word on what is not signed yet>

## <a heading that says what the user gets, in their words>
<one paragraph: what it does, how to reach it (button, command), why it matters>

**Still needs:**
- <what nobody has checked yet: on macOS, on Windows, with a real paid agent run…>

…one section per capability…

## Under the hood            (optional, short)
## Fixed                     (a short list: what was wrong, now right, said as the user met it)
## Upgrading from vA.B.C     (what the user does, or "nothing": data kept, settings renamed, commands gone)
## Credits                   (contributors by their GitHub identity, @login, and what they brought)
## License
**Full changelog**: https://github.com/Empowill/Djinn/compare/vA.B.C...vX.Y.Z
```

## Only what is verified is claimed

- A sentence in a section says what was seen working: by a test that runs in CI, by a person on a machine, by a real
  run. The plans' proof boxes (`plan/*.md`, `- [x]` with who and when) and the CI's systems tell which.
- What was not seen goes under **Still needs**, plainly: "Not checked yet on Windows", "not run with a real paid
  agent". Built by CI is not "works on": an archive nobody opened on that system is a still-needs.
- No feature from a plan that is not merged; no number not measured (say where it was measured).

## Words

- English only (`TestNoFrench` fails on a French letter anywhere but `locales/fr.json`).
- No internal jargon a user would not know: explain the first time (an *azima*, a part of the lead's plan; a
  *tilasm*, a page that explains a wish), or avoid it. No task codes (W123, Q45) in the notes: they mean nothing
  outside the developer's Djinn.
- No personal data (no email, no machine name, no local path), no secret, no customer.
- Links are absolute (`https://github.com/Empowill/Djinn/blob/main/…`): a release page does not resolve a relative one.
- One line per paragraph and per list item: a release page keeps every line break of the file.

## Check, then hand it over

Point `TestReleaseNotes` (`tools/releasepack/readme_test.go`) at the new file and its tags, then:

```sh
go test -tags headless ./tools/releasepack/ ./locales/   # TestReleaseNotes, TestReleaseWorkflow, TestNoFrench
```

Commit the file (`docs: the release notes of vX.Y.Z`). The rest is the developer's: ask it with `djinn question ask
… --move`, with the commands below, once the notes are on main.

## The release, the developer's move

Never tag, push a tag, create, edit or publish a release yourself: a published release is public, installed by
`install.sh`, and offered to every running Djinn.

1. **Dry run** from main, nothing published; every target built, the archives and `install.sh` checked:
   ```sh
   gh workflow run release.yml --ref main -f version=v0.0.0-dryrun
   gh run watch "$(gh run list --workflow release.yml --limit 1 --json databaseId -q '.[0].databaseId')"
   ```
2. **The release.** The tag carries the built interface (`dist/`), so `go install …@vX.Y.Z` builds too, and the
   draft is published only once its binaries are attached:
   ```sh
   git switch main && git pull && npm ci
   go tool task release VERSION=vX.Y.Z   # builds dist/, tags a commit holding it, off any branch
   git push origin vX.Y.Z                # the workflow builds and opens a draft with docs/releases/vX.Y.Z.md
   gh release view vX.Y.Z                # read the draft, its assets listed
   gh release edit vX.Y.Z --draft=false  # publish: it becomes the latest
   ```
   Simpler, from main without a local build: `gh release create vX.Y.Z --target main --title 'Djinn vX.Y.Z'
   --notes-file docs/releases/vX.Y.Z.md`. It publishes at once and the workflow attaches the binaries some 10 minutes
   later (until then `install.sh` finds none); its tag has no `dist/`, so `go install …@vX.Y.Z` fails.
3. After: `curl -fsSL https://github.com/Empowill/Djinn/releases/latest/download/install.sh | sh` on a clean
   account, then `djinn version`.
