// The Comments column's model (src/data/comments.ts): the drafts of a tilasm in the order written, their counts, and
// what the column holds between two renders. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const c = await bundle(
  "comments",
  `export * from "@/src/data/comments.ts";
export { create } from "@bufbuild/protobuf";
export { CommentState, TilasmCommentSchema } from "@/gen/ts/plan/v1/tilasm_pb.ts";`,
);

const L01 = "01a1223a-ae45-728f-8c37-c005eee91edb";
const L02 = "01a1223a-ae45-728f-8c37-c005eee91edc";
const comment = (
  id,
  tilasmId,
  code,
  text,
  state = c.CommentState.DRAFT,
  anchor,
) =>
  c.create(c.TilasmCommentSchema, { id, tilasmId, code, text, state, anchor });

const c1 = comment("01a14600-0000-7000-8000-000000000001", L01, "C1", "First.");
const c2 = comment(
  "01a14600-0000-7000-8000-000000000002",
  L01,
  "C2",
  "Second.",
);
const c3 = comment(
  "01a14600-0000-7000-8000-000000000003",
  L02,
  "C1",
  "On L02.",
);
const sent = comment(
  "01a14600-0000-7000-8000-000000000004",
  L01,
  "C3",
  "Sent.",
  c.CommentState.SENT,
);

test("a tilasm's drafts show in the order written, never a sent one nor another tilasm's", () => {
  // Read out of order: the new one comes below, a draft put back by an Undo takes its place again.
  assert.deepEqual(
    c.draftsOn([c2, sent, c3, c1], L01).map((d) => d.code),
    ["C1", "C2"],
  );
  assert.deepEqual(c.draftsOn([], L01), []);
});

test("the drafts are counted by tilasm, for the head, the row and the tab", () => {
  const counts = c.draftCounts([c1, c2, c3, sent]);
  assert.equal(counts.get(L01), 2);
  assert.equal(counts.get(L02), 1);
  assert.equal(counts.size, 2);
});

test("a comment is on the whole tilasm, a passage or an element", () => {
  assert.equal(c.anchorKind(c1), "whole");
  assert.equal(
    c.anchorKind(
      comment("x", L01, "C9", "", c.CommentState.DRAFT, {
        exact: "two seconds",
      }),
    ),
    "passage",
  );
  assert.equal(
    c.anchorKind(
      comment("x", L01, "C9", "", c.CommentState.DRAFT, {
        path: "body>figure",
      }),
    ),
    "element",
  );
  assert.equal(
    c.anchorKind(
      comment("x", L01, "C9", "", c.CommentState.DRAFT, { page: "index.html" }),
    ),
    "whole",
  );
});

test("a new card is written, saved once it says something, or given up", () => {
  let s = c.column(c.emptyColumn, { type: "write" });
  assert.equal(s.editing, c.NEW);
  assert.equal(c.canSave(s), false);
  s = c.column(s, { type: "type", text: "  \n" });
  assert.equal(c.canSave(s), false, "a blank comment is not saved");
  s = c.column(s, { type: "type", text: "A paused wish?" });
  assert.equal(c.canSave(s), true);
  // A second click on "On the whole tilasm" keeps what is typed.
  assert.equal(c.column(s, { type: "write" }), s);
  assert.deepEqual(c.column(s, { type: "saved" }), { editing: "", text: "" });
  assert.deepEqual(c.column(s, { type: "cancel" }), { editing: "", text: "" });
  assert.equal(c.canSave(c.emptyColumn), false);
});

test("a draft is edited in place from its text", () => {
  let s = c.column(c.emptyColumn, { type: "edit", comment: c1 });
  assert.deepEqual(s, { editing: c1.id, text: "First." });
  s = c.column(s, { type: "type", text: "First, edited." });
  // Opening the same card again keeps the edit.
  assert.equal(c.column(s, { type: "edit", comment: c1 }), s);
  // Another card replaces it.
  assert.deepEqual(c.column(s, { type: "edit", comment: c2 }), {
    editing: c2.id,
    text: "Second.",
  });
});

test("a deleted draft can be put back for five seconds, then its Undo goes", () => {
  const now = 1_000_000;
  let s = c.column(c.column(c.emptyColumn, { type: "edit", comment: c2 }), {
    type: "deleted",
    comment: c1,
    now,
  });
  // Another card's edit stays; the Undo stands until UNDO_MS later.
  assert.equal(s.editing, c2.id);
  assert.equal(s.undo.comment, c1);
  assert.equal(s.undo.until, now + c.UNDO_MS);
  assert.equal(c.UNDO_MS, 5000);
  assert.equal(c.column(s, { type: "tick", now: now + 4999 }), s);
  assert.equal(c.column(s, { type: "tick", now: now + 5000 }).undo, undefined);
  assert.equal(c.column(s, { type: "undone" }).undo, undefined);
  assert.equal(c.column(c.emptyColumn, { type: "undone" }), c.emptyColumn);

  // The card being edited, deleted, closes.
  s = c.column(c.column(c.emptyColumn, { type: "edit", comment: c1 }), {
    type: "deleted",
    comment: c1,
    now,
  });
  assert.equal(s.editing, "");
  assert.equal(s.text, "");
  // A second delete takes the Undo over.
  s = c.column(s, { type: "deleted", comment: c2, now: now + 1000 });
  assert.equal(s.undo.comment, c2);
  assert.equal(s.undo.until, now + 1000 + c.UNDO_MS);
});
