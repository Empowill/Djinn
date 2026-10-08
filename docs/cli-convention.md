# The command line convention

Every command of `djinn` comes from a proto in `api/`. No CLI option lives in the proto: the command line is
read from the request messages by convention, at runtime, through `protoreflect`. Adding a public method to a
service adds a command, with its help, its arguments and their validation.

## Commands

- Only methods marked `option (djinn.v1.visibility) = VISIBILITY_PUBLIC` become commands. Unmarked and
  `VISIBILITY_INTERNAL` methods (the window's `UiService`) never do.
- The command is the service name without `Service`, then the method, both in kebab-case:
  `QuestionService.Answer` is `djinn question answer`.
- A unique prefix is enough: `djinn q answer`, `djinn p l`. An ambiguous one is an error that lists the
  candidates (`djinn question a` matches `ask` and `answer`).
- `djinn version` prints the version.
- A method marked `option (djinn.v1.autostart) = true` starts `djinn up` in the background when no djinn
  answers (no address file, or an address nothing answers any more), waits for it, then calls it: `djinn wish
  resume` and `djinn wish set-lead`. The background djinn is detached from the terminal, writes to `djinn.log` in
  the data directory, and the command prints its PID. `--addr` or `$DJINN_ADDR` turns this off. The other
  methods fail with a hint to run `djinn up`.

## Arguments

The arguments are the fields of the request.

| Field                                       | On the command line                                      |
| ------------------------------------------- | -------------------------------------------------------- |
| `(buf.validate.field).required = true`      | Positional, in field-number order: `<question> <choice>` |
| any other field                             | `--kebab-case value` or `--kebab-case=value`             |
| `bool`                                      | `--open`, without a value (`--open=false` also works)    |
| `repeated`                                  | A repeatable flag: `--options a --options b`             |
| enum                                        | The value without its type prefix, any case: `b`, `B`    |
| `google.protobuf.Timestamp`                 | RFC 3339: `2026-10-07T09:00:00Z`                         |
| a message holding only a `oneof` of scalars | One input, stored in the first member whose rules pass   |
| `string directory` or `*_directory`         | A folder: a relative path is made absolute, from the current directory, before sending |
| `string file` or `*_file`                   | A file, made absolute the same way: the server reads or writes it on this machine |

The `oneof` rule is how a question is named by its code or its identifier: `QuestionRef` has `code` (pattern
`^Q[0-9]{2,3}$`) then `id` (a UUID), so `Q03` lands in `code` and a UUID in `id`. An input no member accepts
is an error that gives each member's reason.

`--` ends the flags: what follows is positional, even when it starts with `--`. The zero value of an enum
(`*_UNSPECIFIED`) cannot be typed. Maps, `bytes` and other nested messages are not supported; a test fails if a
public method needs one.

## Validation

The request is validated with protovalidate before it is sent. Each violation is reported against the
argument the user typed, with what was expected:

```
$ djinn question answer
error: <question>: value is required; expected a match of ^Q[0-9]{2,3}$ or a UUID
  <choice>: value is required; expected one of yes, a, b, c, d
  run djinn question answer --help for the arguments
```

Exit codes: `0` success, `1` the call failed, `2` the command line is wrong.

## Help and output

The help is the proto comments: the service comment for `djinn question`, the method and field comments for
`djinn question answer --help`. Write them for the person typing the command.

The output is text by default: one `name: value` line per field set, nested messages indented, lists as
`- ` items, and the single field of a response shown directly. `--json` prints the response as protobuf JSON
with the proto field names.

## Global flags

`--json`, `--addr URL` (the server: `unix:///path/to/djinn.sock`, or `http://127.0.0.1:PORT/?token=…` whose
token goes in an `Authorization: Bearer` header; default `$DJINN_ADDR`, then the address `djinn up` writes in
`server.addr` of the data directory) and `-h`/`--help` may appear anywhere before `--`, so a request field cannot be
named `json`, `addr` or `help`.

## MCP

`djinn mcp` serves the same commands to an agent that speaks the [Model Context
Protocol](https://modelcontextprotocol.io), on stdin and stdout (its stdio transport, JSON-RPC 2.0 one message
per line). Add it to an agent as a stdio server whose command is `djinn mcp`; `--addr` and `$DJINN_ADDR` work
as above.

- **One tool per public method that answers once**, named like its command in snake_case: `djinn wish set-lead`
  is `wish_set_lead`. The streaming methods (`gate hold`, `wish watch`, `task watch`) stay on the command line: a
  tool call answers once.
- **The input schema is the request**, field by field, by proto name: what is positional on the command line is
  `required`; an enum lists its short values; a `oneof` reference and a time are strings; a repeated field is an
  array. The description is the proto comment, with what the field expects.
- **The same reading and the same checks.** Each value is read as the command line reads its text (a relative
  path starts from the folder `djinn mcp` runs in), then protovalidate checks the request before it is sent. A
  faulty argument is a tool error named by its field: `question: value is required; expected a match of
  ^Q[0-9]{2,3}$ or a UUID`.
- **The answer is the response in protobuf JSON**, with the proto field names, as `--json` prints it. A call that
  fails is a tool error with the server's message (`not_found: no question Q99`).
- **Autostart** works as on the command line: `wish_resume` starts `djinn up` when none answers.

No MCP library: the server is `internal/cli/mcp.go`, the protocol's `initialize`, `ping`, `tools/list`,
`tools/call` and `notifications/cancelled`.

## OpenAPI

[`openapi.json`](openapi.json) describes the same public methods for any HTTP client, in OpenAPI 3.1: each is a
`POST` of the [Connect protocol](https://connectrpc.com/docs/protocol) on `/<package>.<Service>/<Method>`, its body
the request in protobuf JSON. `go tool task gen` writes it (`tools/openapi`), and `TestOpenAPIIsFresh` fails when it
lags behind the protos. Streaming methods are named there, not described.

## How it works

`go tool task gen` runs `buf generate` (Go messages and Connect handlers) and writes `gen/djinn.binpb`, the
descriptors of `api/` with their comments, which `protoc-gen-go` strips. The command line embeds that file,
builds requests as `dynamicpb` messages and calls the server with a generic Connect client in binary protobuf.
A test checks that the embedded descriptors match the generated code.
