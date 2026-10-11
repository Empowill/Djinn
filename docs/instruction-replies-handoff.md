# Instruction replies: backend handoff

To W16 successor task `01a12c28-96a6-77a5-b2b9-ff2df9cd4765`, which owns the remaining translated interface for I04.

The backend adds an additive `InstructionReply` message and a public `InstructionService.Reply` RPC. The generated
TypeScript client is included with this backend contract and must stay aligned with the committed proto; W16 should
use these generated files without restoring stale outputs. If the proto changes later, run exactly one gated generation
with `djinn gate run codegen -- go tool task gen` before making display changes. W16 owns all remaining translated
display and synced-page rendering for the reply history.

```proto
message InstructionReply {
  string text = 1;                         // 1–2000 characters, nonblank
  google.protobuf.Timestamp create_time = 2; // required; chronological append order
}

message Instruction {
  repeated InstructionReply replies = 10;  // oldest first; existing replies are preserved
}

rpc Reply(InstructionServiceReplyRequest) returns (InstructionServiceReplyResponse);

message InstructionServiceReplyRequest {
  string instruction = 1; // UUID or case-insensitive I01 code
  string reply = 2;       // 1–2000 characters, nonblank
  string wish_id = 3;     // optional for a UUID; needed to disambiguate a code
}

message InstructionServiceReplyResponse {
  Instruction instruction = 1;
}
```

The command is `djinn instruction reply <instruction> "<reply>" [--wish-id <uuid>]`. A UUID may omit `--wish-id`;
an Icode that occurs in several wishes must provide it. A supplied wish that does not own the instruction is rejected.
The server resolves the reference, appends one reply in chronological order, journals the canonical instruction and
wish IDs in the same transaction, and returns the full instruction. It preserves all earlier replies and leaves the
instruction status and worker assignment unchanged. Empty, too-long, malformed instruction references or wish UUIDs,
and UUID wrong-wish references return `invalid_argument`; a missing instruction or code outside the selected wish
returns `not_found`; an ambiguous code without `--wish-id` returns `failed_precondition`. CLI validation exits 2 for
these local shape errors; server failures exit 1. Repeating the same reply intentionally appends another entry because
Reply has no retry ID. Invalid inputs leave no journal entry or storage change.

Replies are part of snapshots, exports/imports and restart persistence. Export scrubbing removes local paths and URL
credentials from reply text and from the reply journal request, while retaining the reply content. The interface task
owns rendering the reply history in the wish view and any synced-page presentation.
