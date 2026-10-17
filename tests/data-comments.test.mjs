// The Comments column's model (src/data/comments.ts): the drafts of a tilasm in the order written, their counts, and
// what the column holds between two renders. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const c = await bundle(
  "comments",
  `export * from "@/src/data/comments.ts";
export { create } from "@bufbuild/protobuf";
export {
  AnchorStatus,
  CommentState,
  TilasmCommentSchema,
  TilasmHoldReason,
  TilasmHoldSchema,
} from "@/gen/ts/plan/v1/tilasm_pb.ts";`,
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

test("a new card on a passage or an element keeps it until saved, and takes another with what is typed", () => {
  const passage = {
    page: "index.html",
    exact: "two seconds",
    prefix: "waits ",
    suffix: ".",
    start: 40,
    end: 51,
    label: "§ Grouping",
  };
  let s = c.column(c.emptyColumn, { type: "write", anchor: passage });
  assert.deepEqual(s, { editing: c.NEW, text: "", anchor: passage });
  s = c.column(s, { type: "type", text: "Say where it comes from." });
  assert.equal(c.column(s, { type: "write", anchor: passage }), s);
  const other = { ...passage, exact: "one line", start: 80, end: 88 };
  assert.deepEqual(c.column(s, { type: "write", anchor: other }), {
    editing: c.NEW,
    text: "Say where it comes from.",
    anchor: other,
  });
  // An element aimed at takes the card, with what is typed.
  const figure = {
    page: "index.html",
    elementId: "",
    path: "figure:nth-of-type(1)",
    fingerprint: "The path of a notice",
    label: "figure: The path of a notice",
  };
  assert.deepEqual(c.column(s, { type: "write", anchor: figure }), {
    editing: c.NEW,
    text: "Say where it comes from.",
    anchor: figure,
  });
  // "On the whole tilasm" puts the card on the whole of it.
  assert.deepEqual(c.column(s, { type: "write" }), {
    editing: c.NEW,
    text: "Say where it comes from.",
  });
  assert.deepEqual(c.column(s, { type: "saved" }), { editing: "", text: "" });
  assert.deepEqual(c.column(s, { type: "cancel" }), { editing: "", text: "" });
  // Editing a draft leaves what the new card was on.
  assert.deepEqual(c.column(s, { type: "edit", comment: c1 }), {
    editing: c1.id,
    text: "First.",
  });
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

test("the foot sends the drafts and the last word, unless the tilasm is held", () => {
  const hold = (reason) =>
    c.create(c.TilasmHoldSchema, { tilasmId: L01, reason, code: "W60" });
  // Launch goes without a comment; Iterate needs one, a draft or the last word.
  assert.deepEqual(c.sendFoot(0, "  ", undefined), {
    count: 0,
    launch: true,
    iterate: false,
    greyed: false,
  });
  assert.deepEqual(c.sendFoot(0, "Faster.", undefined), {
    count: 1,
    launch: true,
    iterate: true,
    greyed: false,
  });
  assert.equal(c.sendFoot(2, "Faster.", undefined).count, 3);
  // A task on it, or its work launched: the buttons and the comments wait.
  for (const reason of [
    c.TilasmHoldReason.REWORK,
    c.TilasmHoldReason.WORK,
    c.TilasmHoldReason.LAUNCHED,
  ])
    assert.deepEqual(c.sendFoot(2, "", hold(reason)), {
      count: 2,
      launch: false,
      iterate: false,
      greyed: true,
    });
  // A send the Djinn has not read yet holds the buttons alone.
  assert.deepEqual(c.sendFoot(2, "", hold(c.TilasmHoldReason.SENT)), {
    count: 2,
    launch: false,
    iterate: false,
    greyed: false,
  });
});

// A draft written on version 1, with its anchor as a later version placed it.
const placedDraft = (n, anchor, version = 1) =>
  c.create(c.TilasmCommentSchema, {
    id: `01a14600-0000-7000-8000-00000000010${n}`,
    tilasmId: L01,
    code: `C${n}`,
    text: `Draft ${n}.`,
    state: c.CommentState.DRAFT,
    version,
    anchor,
  });

test("a draft a later version lost waits under Detached, each group in the order written", () => {
  const found = placedDraft(1, {
    exact: "two seconds",
    status: c.AnchorStatus.FOUND,
    statusVersion: 2,
  });
  const lost = placedDraft(2, {
    exact: "ten seconds",
    status: c.AnchorStatus.LOST,
    statusVersion: 2,
  });
  const changed = placedDraft(3, {
    path: "figure:nth-of-type(1)",
    status: c.AnchorStatus.CHANGED,
    statusVersion: 2,
  });
  const lostToo = placedDraft(4, {
    path: "table:nth-of-type(1)",
    status: c.AnchorStatus.LOST,
    statusVersion: 2,
  });
  const groups = c.columnGroups([c1, found, lost, changed, lostToo]);
  assert.deepEqual(
    groups.attached.map((d) => d.code),
    ["C1", "C1", "C3"],
  );
  assert.deepEqual(
    groups.detached.map((d) => d.code),
    ["C2", "C4"],
  );
  assert.equal(c.detached(lost), true);
  assert.equal(c.detached(c1), false);
});

test("the window writes where each anchor stands once per later version, for those the frame placed", () => {
  const passage = { exact: "two seconds" };
  const onV1 = placedDraft(1, passage);
  const onV2 = placedDraft(2, passage, 2);
  const placedOnV2 = placedDraft(3, {
    ...passage,
    status: c.AnchorStatus.CHANGED,
    statusVersion: 2,
  });
  const placedOnEarlier = placedDraft(4, {
    ...passage,
    status: c.AnchorStatus.LOST,
    statusVersion: 1,
  });
  const otherPage = placedDraft(5, { ...passage, page: "notes.html" });
  const placed = [
    { id: onV1.id, status: "changed", top: 3, bottom: 20 },
    { id: onV2.id, status: "found", top: 30, bottom: 40 },
    { id: placedOnV2.id, status: "changed" },
    { id: placedOnEarlier.id, status: "found", top: 50, bottom: 60 },
    { id: c1.id, status: "lost" },
  ];
  assert.deepEqual(
    c.placesToWrite(
      [onV1, onV2, placedOnV2, placedOnEarlier, otherPage, c1],
      placed,
      2,
    ),
    [
      { id: onV1.id, status: c.AnchorStatus.CHANGED },
      { id: placedOnEarlier.id, status: c.AnchorStatus.FOUND },
    ],
  );
  // Once written, nothing more to write.
  assert.deepEqual(c.placesToWrite([placedOnV2], placed, 2), []);
  assert.deepEqual(c.placesToWrite([onV1], [], 2), []);
});

test("the dots stand at their anchors' heights, stacked on one line, only within the frame", () => {
  const drafts = [1, 2, 3, 4, 5].map((n) => placedDraft(n, { exact: "x" }));
  const sentOne = c.create(c.TilasmCommentSchema, {
    ...drafts[4],
    state: c.CommentState.SENT,
  });
  const dots = c.dotsOf(
    [...drafts.slice(0, 4), sentOne],
    [
      { id: drafts[1].id, status: "found", top: 100, bottom: 118 },
      { id: drafts[0].id, status: "found", top: 100, bottom: 118 },
      { id: drafts[2].id, status: "changed", top: -40, bottom: -10 },
      { id: drafts[3].id, status: "lost" },
      { id: sentOne.id, status: "found", top: -5, bottom: 12 },
      { id: "elsewhere", status: "found", top: 10, bottom: 20 },
    ],
    400,
    22,
  );
  assert.deepEqual(
    dots.map((d) => [d.code, d.top, d.sent]),
    [
      ["C5", 0, true],
      ["C2", 100, false],
      ["C1", 122, false],
    ],
  );
  // Past the bottom, a dot does not show.
  assert.deepEqual(
    c.dotsOf(
      drafts,
      [{ id: drafts[0].id, status: "found", top: 390, bottom: 410 }],
      400,
      22,
    ),
    [],
  );
});
