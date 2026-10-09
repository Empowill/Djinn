---
id: 01a1223a-ae45-728f-8c37-c005eee91edb
code: T29
phase: 2
status: open
after: T01 T08
---

# T29 · Tilasms: what explains a wish, kept, linked, opened anywhere

**Goal.** A wish keeps the material that explains it: a concept drawn, a data model walked through, a comparison
laid out. A tilasm (Arabic ṭilasm, the engraved object that holds a power; the origin of "talisman") is a folder with
an `index.html` and its sources, kept on this machine, in its own tab of the wish. It is research material: the
developer and the agents read it, link to it, and a link opens it in the app from anywhere.

**The developer's words.** "A tab with tilasms (artifacts): files or folders with an HTML file and other sources,
saved on the machine, to explain the concepts, exportable and importable. It will be research material, and it must
be referenced by simple links that open it natively in the app from anywhere."

## Decided (09/10/2026, the developer's answers)

- **A tilasm belongs to a wish**: a tab *Tilasms* in the wish, beside Tasks and Decisions. Its code is `L01`, `L02`…
  in the wish (T is the azimas').
- **Stored in Djinn's data folder**: `~/.config/djinn/tilasms/<id>/`, the folder as given (`index.html` and its
  sources) and a manifest (title, wish, author, dates, what it cites). Nothing goes into a project's repository.
- **Two links**: `djinn://tilasm/<id>`, a scheme registered with the system on Linux, macOS and Windows, opens Djinn
  on the tilasm (starting Djinn if it does not run) from a browser, Slack, a terminal or a Markdown file; and a local
  http address of the same tilasm, for a browser or an agent that wants the HTML. `djinn tilasm open <code>` too.
- **Scripts, no network**: a tilasm opens in an isolated frame, as the Mermaid diagrams do (a policy of its own,
  served by Djinn): its own scripts and local files run; no network, no access to Djinn nor its data.
- **The agents and the developer make them**: the lead and the workers with `djinn tilasm put <folder> --wish …`;
  the developer by importing, or dropping a folder or a `.zip` on the tab.
- **Exported with the wish**: `djinn wish export` carries its tilasms, and its import brings them back; a tilasm
  also exports alone, as a `.zip` (the folder and its manifest), and imports into the wish shown.
- **Replaced, with a history**: putting a tilasm again replaces it; its link stays and shows the latest version;
  the earlier versions can be read and restored.
- **Research material**: a `djinn://tilasm/…` link in a block, a question, a decision or the brief opens in the app;
  the brief lists the wish's tilasms; a full-text search in the tab, on titles and text, within the wish;
  `djinn tilasm get <code>` gives an agent the text and the path of the files; a tilasm cites the azimas and tasks it
  explains, and each of them shows its tilasms.

- **"Talisman" counts too**: the word the developer may use, in English and French. `djinn talisman …` is a synonym
  of `djinn tilasm …`, the tab's search finds "talisman", and the lead understands both.
- **The first tilasm** shows the objects stored in the database, drawn from the protos (`api/plan/v1/plan.proto` and
  the others): each entity, its fields and their meaning, and how they link (wish, azima, task, question, block,
  tilasm…). It is made once the tilasms work, and proves them.

## Done when

- [x] `djinn tilasm put|list|get|open|history|restore|export|import` (`TilasmService`), journaled; a put replaces,
  keeping the earlier version. (needs: the tests) — 09/10: `open` came with the links (`TestTilasmOpenAndLinks`: by
  code or identifier, in its wish's Tilasms tab, refused without a window; `get` gives the `djinn://` link and the
  local http address). `TestTilasmPutReplaceHistoryRestore` (put, a put by code is a new version, get with the text, history, restore, the
  journal), `TestTilasmExportImport` (a `.zip` with `tilasm.json`, imported into its wish, another wish, another
  machine), `TestTilasmCeiling` (50 MiB, a field, refused beyond, saying so), `TestTilasmSearch`.
- [x] The *Tilasms* tab of a wish: list, open in an isolated frame (scripts run, the network is refused), search,
  history, export, import by drop. (needs: a screens test and an e2e) — 09/10: `/tilasm/<id>/` serves the latest
  version with `TilasmPolicy` (`TestTilasmFilesServeTheLatestVersionWithTheirPolicy`: `connect-src 'none'`,
  `default-src 'none'`, `sandbox allow-scripts`, a new version at the same address); in the browser the frame's
  files load from an address with a key of their own, the frame's opaque origin carrying no cookie
  (`TestGuardTilasmFrame`); a drop goes through `PutData` (`TestTilasmPutData`: a folder, a `.zip`, an export
  imported). Screens: "the Tilasms tab lists each tilasm…", "a wish's view has a Tilasms tab…", "a folder dropped on
  the Tilasms tab is read…". e2e `tilasms.spec.ts`: put by the CLI, opened in the tab (its script runs, a fetch is
  refused by `connect-src`, the parent page is out of reach), searched ("talisman" too), a version restored. Export
  and a real drop are not clicked in the e2e (export writes to the Downloads folder); the native window is not
  checked yet (`go tool task e2e-native`).
- [ ] `djinn://tilasm/<id>` opens the app on the tilasm from outside, Djinn running or not, on Linux (a desktop entry
  for `x-scheme-handler/djinn`), macOS (`CFBundleURLTypes` in Djinn.app) and Windows (the registry, by the
  installer). (needs: Linux by test; a Mac and a Windows machine by hand) — 09/10, Linux done: the system runs
  `djinn open <link>`, which hands the link to the running Djinn (one per data folder), starting it when none runs
  (`TestOpenHandsTheLinkToTheRunningDjinn`: started then shown, handed over without a second Djinn, an unknown link
  or tilasm said on the command line and in the window, an https link refused); `UiService.OpenLink`
  (`TestOpenLink`), `internal/link` (`TestParse`: `djinn://tilasm/<id>`, `djinn://wish/<id>`, `talisman`, any case, a
  trailing slash, a query); the menu entry of `tools/icons desktop` runs `djinn open %u` with
  `MimeType=x-scheme-handler/djinn;` (`TestDesktopPutsTheIconAndTheEntryInTheUsersFolder`, with
  `desktop-file-validate`), made the handler by `xdg-mime default` (`TestDesktopEntryHandlesTheLinks`, in the test's
  own folders). **macOS and Windows: built and unit-tested here, to check by hand there.** macOS: `CFBundleURLTypes`
  in Djinn.app's Info.plist (`TestBundleLayout`), the link handled through Wails' `ApplicationLaunchedWithUrl`; to
  check: a link starts Djinn.app and shows the tilasm, and reaches a running one; a Djinn started from a terminal is
  not the app macOS knows, and a link then starts Djinn.app, which finds it running and brings it forward without the
  link. Windows: `HKCU\Software\Classes\djinn` (`TestRegistryValues`), written by `go tool task install` (`tools/icons
  desktop`) and by `djinn up` of a release when the user has none (a development build never takes it); to check: a
  link clicked starts Djinn or reaches it, and whether `djinn open` flashes a console.
- [x] Links inside the app (blocks, questions, decisions, the brief) open the tilasm in place. (09/10: Markdown keeps a
  `djinn://` link, which opens the tilasm in its wish's Tilasms tab, or the wish, or says the window does not know it;
  screens "a djinn:// link in Markdown stays a link that opens what it names in place…". The brief keeps the
  link as written, and the page rendered in Go too (`TestPageKeepsDjinnLinks`): opened from there, the system runs
  `djinn open`.)
- [x] `djinn wish export` and import carry the tilasms. (09/10, `TestWishExportCarriesTilasms`: every version's files
  in the export, back on another machine under the same identifiers; the journal keeps the manifests, not the files; a
  replace leaves only the file's tilasms; a deleted wish takes its tilasms' folders.)
- [x] `djinn talisman` answers as `djinn tilasm`, and the search finds "talisman". (09/10, `TestRun`: "talisman
  answers as tilasm", "an alias by a prefix no command takes", from `option (djinn.v1.alias)`; `TestTilasmSearch`:
  "talisman" or "tilasm", in English or French, finds them all, the search the tab will call.)
- [ ] The first tilasm, the objects in the database drawn from the protos, is put and opens from its link.
- [ ] The brief lists the wish's tilasms, and the rules tell the lead to make one to explain a concept, and to cite
  it.

## Tests must be fast

No real sleep, fake clocks, milliseconds: a test over 1 s is a bug.

## Open questions

- The local http address of a tilasm exists while Djinn serves http (`--browser`, Windows). The window on macOS and
  Linux listens on a socket only, by design (`docs/transport.md`): there `djinn tilasm get` gives the folder of the
  files and the `djinn://` link, no http address. Open a loopback port for the tilasms alone?

- Size: a ceiling per tilasm, 50 MiB (`Tilasm.max_bytes`, decided 09/10); large binary files (videos) are refused past
  it, saying so. A way to raise one tilasm's ceiling is still open.
- A tilasm shared by several wishes: a link from one wish to another's tilasm is enough for now; a library across
  wishes could come later.
