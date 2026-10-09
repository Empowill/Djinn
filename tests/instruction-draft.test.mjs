import test from "node:test";
import assert from "node:assert/strict";
import { bundle } from "./bundle.mjs";

const { createDraft } = await bundle(
  "instruction-draft",
  `export { createDraft } from "@/src/instruction-draft.ts";`,
);

test("an in-flight draft cannot submit twice; retry keeps the write identity", () => {
  const draft = createDraft();
  draft.set({ text: "  Keep the complete brief\nAnd every detail  " });
  const first = draft.begin();
  assert.equal(first.text, "Keep the complete brief\nAnd every detail");
  assert.ok(first.requestId);
  assert.equal(draft.begin(), undefined);
  draft.set({ busy: false, error: "Response lost" });
  assert.equal(
    draft.get().text,
    "  Keep the complete brief\nAnd every detail  ",
  );
  const retry = draft.begin();
  assert.deepEqual(retry, first);
  draft.set({ busy: false, text: "A new indication" });
  assert.notEqual(draft.begin().requestId, first.requestId);
});

test("blank input creates no request and drafts remain independent", () => {
  const a = createDraft();
  const b = createDraft();
  a.set({ text: "\n " });
  assert.equal(a.begin(), undefined);
  a.set({ text: "For wish A" });
  assert.equal(b.get().text, "");
  const aFirst = a.begin();
  a.set({ busy: false, text: "", requestId: "", requestText: "" });
  a.set({ text: "For wish A" });
  assert.notEqual(
    a.begin().requestId,
    aFirst.requestId,
    "a successful identical next indication is a new write",
  );
});
