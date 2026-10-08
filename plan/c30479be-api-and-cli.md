---
id: 01a1184f-cf16-7cc3-9181-dd6ec30479be
code: T02
phase: 1
status: done
---

# T02 · Protos, API and command line

**Goal.** Everything is generated from `api/`: the server, the interface clients, the command
line agents use, and the public API documentation.

## Decided
- `buf` with the generators as tools of the Go module (`go tool task gen`).
- `api/ui/v1` is reserved to the window; the other packages form the public API.
- Validation with [protovalidate](https://github.com/bufbuild/protovalidate), written once in
  the protos, checked by the CLI before calling and by the server again.
- **The command line follows a convention**, with no CLI option in the protos:
  - command = service without `Service`, then the method, in kebab-case
    (`djinn question answer`); any unambiguous prefix works (`djinn q answer`);
  - `required` fields are positional arguments, in field-number order; other fields are
    `--kebab-case` flags;
  - an enum is typed without its type prefix, case-insensitive (`CHOICE_B` → `b`);
  - a `oneof` of scalars takes one input, stored in the first member whose rule passes;
  - help text comes from the proto comments; `--json` switches the output to JSON.
- MCP later, as a thin layer over the same calls.

## Done when
- [x] `djinn question answer Q03 b` is validated, sent, and printed. (08/10, on a `djinn up` with its own
  `DJINN_HOME`: `djinn question answer Q01 b` prints the question with `answer: choice: b`; `TestRun`)
- [x] A wrong input says, field by field, what was expected. (08/10: `djinn question ask x not-a-uuid` with five
  `--options` prints one line for `--options` and one for `<wish-id>`, exit 2; `TestParseErrors`)
- [x] Adding a method to a proto adds its command, with no hand-written CLI code. (`internal/cli` reads the
  commands from the embedded descriptors; `TestEveryPublicMethodIsExpressible`, `TestEmbeddedDescriptorsAreFresh`)

## Open questions

## Decided along the way
- The command line finds the server through `server.addr` in the data folder (`unix://` on macOS
  and Linux, `http://127.0.0.1:PORT/?token=…` on Windows and with `--browser`); `--addr` and
  `DJINN_ADDR` still force an address.
- A generic runtime by protobuf reflection, no generator of our own: `buf build` keeps the proto
  comments in `gen/djinn.binpb`, embedded in the binary, and a test checks it is fresh.
- Request messages are named `<Service><Method>Request`, so two `List` methods never collide.
- An answered question carries an `Answer` message (choice, note, time): its presence makes the decision.
