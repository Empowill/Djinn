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

test("a key keeps its text and wakes no one, unless the pill shows something new", () => {
  const draft = createDraft();
  let heard = 0;
  draft.subscribe(() => heard++);
  draft.type("H");
  assert.equal(heard, 1, "the first letter enables Send");
  draft.type("He");
  draft.type("Hel\nlo");
  assert.equal(heard, 1);
  assert.equal(draft.get().text, "Hel\nlo", "read at once, as sent");
  draft.set({ error: "Lead stopped" });
  heard = 0;
  draft.type("Hel\nlo!");
  assert.equal(heard, 1, "a key clears the error");
  assert.equal(draft.get().error, "");
  draft.type(" ");
  assert.equal(heard, 2, "a blank box disables Send");
  assert.equal(draft.begin(), undefined);
});
