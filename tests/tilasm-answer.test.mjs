// Whether the window says a discussion has no reply yet (src/tilasm-answer.ts), as Djinn decides it
// (internal/plan/tilasm_agent.go, AwaitsAgent): a proposal linked to the discussion after the developer's last
// message answers it, as a message by someone else does.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const s = await bundle(
  "tilasm-answer",
  `export * from "@/src/tilasm-answer.ts";`,
);

const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
const msg = (author, seconds) => ({
  author,
  text: "…",
  createTime: at(seconds),
});
const thread = (messages, resolved = false) => ({
  id: "t1",
  resolved,
  messages,
});
const proposal = (threadId, seconds) => ({ threadId, createTime: at(seconds) });

test("the developer's last message waits for an answer", () => {
  assert.equal(s.waitsForAnswer(thread([msg("developer", 1)]), []), true);
});

test("a reply or a resolved discussion waits for nothing", () => {
  assert.equal(
    s.waitsForAnswer(thread([msg("developer", 1), msg("document", 2)]), []),
    false,
  );
  assert.equal(
    s.waitsForAnswer(thread([msg("developer", 1)], true), []),
    false,
  );
});

test("a proposal linked after the developer's message answers it", () => {
  assert.equal(
    s.waitsForAnswer(thread([msg("developer", 1)]), [proposal("t1", 2)]),
    false,
  );
});

test("a proposal before the last message, or of another discussion, does not", () => {
  assert.equal(
    s.waitsForAnswer(thread([msg("developer", 1), msg("developer", 3)]), [
      proposal("t1", 2),
    ]),
    true,
  );
  assert.equal(
    s.waitsForAnswer(thread([msg("developer", 1)]), [proposal("t2", 2)]),
    true,
  );
});
