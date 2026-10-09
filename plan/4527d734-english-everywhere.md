---
id: 01a11852-f378-7235-a5df-26f74527d734
code: T10
phase: 1
status: in-progress
---

# T10 · English everywhere

**Goal.** Djinn is open source: everything in the repository is in English, so that anyone can
read, install and contribute.

## Decided
- **The simplest translation that works in Go and in TypeScript**: one source file in English
  for the whole app, and one file per language next to it (`locales/en.json`,
  `locales/fr.json`), keys shared by the Go server and the interface. No namespaces, no
  translation platform, no generated copies.
- **The model translates.** Whoever adds or changes a text (an agent, most of the time) writes
  the English source and its translations in the same change. A test fails when a key is
  missing in a language.

## What to do
- Translate the README, starting with its top: what Djinn is, the one-line install, the prompt
  for your agent.
- Translate the user guide and the agent protocol in `docs/`.
- Historical notes in `docs/` (version logs, verification reports): translate what is still
  useful, delete the rest.
- Interface strings in `src/`: list them, then decide with the interface maintainer how to
  handle languages. "Mission" becomes "Wish" (French: « souhait »).

## Done when
- [x] The README, the user guide and the agent protocol are in English; the historical notes are
  folded into them or deleted.
- [x] The interface texts go through `locales/` (1261 keys, English and French), with a test that
  fails on a missing or unused key.
- [x] No French left in the repository outside `locales/fr.json`.
  - [x] The text: code, tests, fixtures and docs. (6d3c599: the benchmark payloads of `tools/windowcheck/page/`
    and `internal/server/transport_bench_test.go`, the e2e assertions and a recorded answer are in English;
    `docs/v0.2.1-session-harmonisation.djinn.json` is deleted, nothing read it. French is tested from
    `locales/fr.json` only: `TestFrench` and `TestWishState` in `internal/render`, e2e "in French › the interface
    follows the system's language". `plan/` quotes the French words it decides on, « souhait », « invoquer »)
  - [x] A test keeps it so. (`TestNoFrench` in `locales/french_test.go` reads every file of the repository, tracked
    or new, and fails on a French letter or quote mark, or on two French words on one line; `TestFrenchSigns` checks
    the heuristic. Allowed: `locales/fr.json`, the accent folding of branch names in `internal/harness/worktree.go`
    and its test, the name Clément, and « souhait », « invoquer » in `plan/`. The last French test data,
    `TestNoticesTranslateAndClip` in `internal/ui`, now reads its French from `locales/fr.json`)
  - [x] The two README screenshots, taken in French on the Electron app. (retaken in English, dark and light, on the
    browser build: `go tool task screenshots` runs `e2e/readme.shot.ts` on a demonstration wish imported into a djinn
    of its own, and writes `docs/screenshots/readme/{questions,tasks}-{dark,light}.png`; the README shows the one of
    the reader's theme. `mission.png` and `supports.png` are deleted)
- [ ] The interface maintainer has reviewed the keys and the English wording. (needs: the interface maintainer)

## Decided along the way
- **One catalog per language, flat keys.** `locales/en.json` is the source, `locales/fr.json` its
  translation; keys are `area.name` in snake case, the area being the screen or component
  (`setup.*`, `timeline.*`, `common.*` for a few shared words). Plurals are `key.one` and
  `key.other`, chosen with `Intl.PluralRules` from a numeric `count`; placeholders are `{name}`.
- **Interface: a 90-line `t()` of our own** (`src/i18n.ts`), no library. The compiler refuses a key
  missing from `en.json` (`TextKey = keyof typeof en`). The language is resolved once at page
  load: the one chosen in Connections & preferences (stored in the browser), else the system's
  (`navigator.languages`, which the native window takes from the OS), else English. Changing it
  reloads the page, so module-level texts are fine.
- **Go: package `locales`** at the root of the module, which embeds the JSON files next to it
  (`go:embed` cannot reach a parent folder). `locales.T(language, key, params)` and
  `locales.Match(tags...)`; no plural until a Go text needs one. The server returns no text to a
  person today, so nothing calls it yet.
- **Tests.** `locales/locales_test.go`: every language has exactly the keys of `en.json` with the
  same placeholders; every key the Go code (`locales.T`) or the interface (`t("…")`) uses exists;
  no key of `en.json` is unused (its quoted name must appear in `src/` or the Go code, so no key
  is built at run time). `tests/i18n.test.cjs` checks parity and the interface's keys for those
  who only run `npm test`.
- **Tests read the interface in English** (superseded: they rendered it in French until 08/10). Node reports
  English, the Playwright config asks for `en-US`. One e2e test asks for `fr-FR`, and the French it expects comes
  from `locales/fr.json`: no French string in a test.
- **"Mission" is "Wish" in English and « souhait » in French** in every text; code identifiers,
  CSS classes, stored data and protocol names (`mission_metadata`, …) keep "mission".
  "Support" (a document a wish produces) is "artifact" in English.
- **Journals with French titles, the stored artifact statuses and the Electron copies** went with the mission
  model and Electron (be8f52c, T03): the interface reads the Go services, whose events have a kind.
