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
- [ ] No French left in the repository outside `locales/fr.json`: left are the legacy Electron app
  (`electron/`, `scripts/`), the window check page (`tools/windowcheck/page/`), the test fixtures and
  assertions (the Node tests render the interface in French), two Electron-era README
  screenshots, and stored values the interface still reads (below). (08/10: `electron/` is gone; French is left in
  `tools/windowcheck/page/`, `internal/server/transport_bench_test.go`, the e2e and Node assertions,
  `docs/v0.2.1-session-harmonisation.djinn.json` and the two screenshots. needs: an agent for the text, a person
  for the screenshots)
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
- **The Node tests render the interface in French**, the language their assertions were written
  in: `npm test` preloads `tests/setup-language.cjs`. The Playwright configs already ask for
  `fr-FR`.
- **"Mission" is "Wish" in English and « souhait » in French** in every text; code identifiers,
  CSS classes, stored data and protocol names (`mission_metadata`, …) keep "mission".
  "Support" (a document a wish produces) is "artifact" in English.
- **Journals already stored hold French titles.** The interface recognises some events by their
  title (`agent-chat.tsx`, `temporal-layout.ts`, `mission-context.ts`): it now matches the French
  legacy title and the title in the page's language. Better later: an event kind, not a title.
  The artifact workspace statuses (`Planifié`, `En cours`, `Archivé`, filter `Tous`) are stored
  values and stay; only their labels are translated.
- **The Electron copies** of `workflow.ts` and `session-validation.ts` are regenerated, with
  `electron/i18n.cjs`, so the parity test holds until Electron is removed.
