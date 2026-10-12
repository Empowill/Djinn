import test from "node:test";
import assert from "node:assert/strict";
import { bundle } from "./bundle.mjs";

const { createDraft } = await bundle(
  "instruction-draft",
  `export { createDraft } from "@/src/instruction-draft.ts";`,
);

test("an in-flight draft cannot submit twice; a failed send keeps the text", () => {
  const draft = createDraft();
  draft.set({ text: "  Keep the complete brief\nAnd every detail  " });
  assert.equal(draft.begin(), "Keep the complete brief\nAnd every detail");
  assert.equal(draft.get().busy, true);
  assert.equal(draft.begin(), undefined);
  draft.set({ busy: false, error: "Response lost" });
  assert.equal(
    draft.get().text,
    "  Keep the complete brief\nAnd every detail  ",
  );
  assert.equal(draft.begin(), "Keep the complete brief\nAnd every detail");
  assert.equal(draft.get().error, "", "a new send clears the last error");
});

test("blank input sends nothing and drafts remain independent", () => {
  const a = createDraft();
  const b = createDraft();
  a.set({ text: "\n " });
  assert.equal(a.begin(), undefined);
  assert.equal(a.get().busy, false);
  a.set({ text: "For wish A" });
  assert.equal(b.get().text, "");
  assert.equal(a.begin(), "For wish A");
  assert.equal(b.begin(), undefined);
});
