// The quotes of a tilasm's passages and its elements (L08, steps 3 to 5). The bridge Djinn appends to a tilasm's pages
// (internal/plan/tilasm_bridge.js) lays out the page's text, quotes a selection and finds a quote or an element again,
// in a later version too: found, changed or lost. Run without a document, it hands those pure functions over. The
// window reads what the bridge sends, and sends it the anchors to place and highlight (src/data/bridge.ts). Run with
// `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";

import { bundle, root } from "./bundle.mjs";

const context = vm.createContext({});
vm.runInContext(
  fs.readFileSync(path.join(root, "internal/plan/tilasm_bridge.js"), "utf8"),
  context,
);
const b = context.tilasmBridge;

const w = await bundle(
  "bridge",
  `export * from "@/src/data/bridge.ts";
export { create } from "@bufbuild/protobuf";
export { CommentState, TilasmCommentSchema } from "@/gen/ts/plan/v1/tilasm_pb.ts";`,
);

test("the bridge sets no global name in a page", () => {
  // In a page it returns before handing anything over; here, with no document, it does.
  assert.deepEqual(Object.keys(context), ["tilasmBridge"]);
});

test("a page's text reads its white space as the page shows it, a new line between blocks", () => {
  const p = "p1";
  const layout = b.pageText([
    { data: "\n    Notices ", block: "h1" },
    { data: "group", block: "h1" },
    { data: "  by\n   wish.  ", block: p },
    { data: " Two ", block: "p2" },
    { data: " seconds.", block: "p2" },
    { data: "   ", block: "p3" },
  ]);
  assert.equal(layout.text, "Notices group\nby wish.\nTwo seconds.");
  assert.equal(layout.piece.length, layout.text.length);
  // Each character says where it comes from: "by" is in the third piece, at 2.
  const by = layout.text.indexOf("by");
  assert.equal(layout.piece[by], 2);
  assert.equal(layout.offset[by], 2);
  // A block's new line counts as the first character of the block's text.
  const nl = layout.text.indexOf("\n");
  assert.equal(layout.piece[nl], 2);
  assert.equal(layout.offset[nl], 2);
  // A paragraph's last word never runs into the next one's first.
  assert.ok(!layout.text.includes("wish.Two"));
});

test("a point in the pieces has its place in the text", () => {
  const layout = b.pageText([
    { data: "  ab  ", block: 1 },
    { data: "cd", block: 1 },
    { data: "ef", block: 2 },
  ]);
  assert.equal(layout.text, "ab cd\nef");
  assert.equal(b.textOffset(layout, 0, 0), 0); // before the leading space: the first character after it
  assert.equal(b.textOffset(layout, 0, 3), 1);
  assert.equal(b.textOffset(layout, 0, 4), 2); // the space kept
  assert.equal(b.textOffset(layout, 0, 6), 3); // the piece's end: the next one's start
  assert.equal(b.textOffset(layout, 1, 2), 5); // "cd" ends at the new line
  assert.equal(b.textOffset(layout, 2, 2), 8); // the text's end
});

test("a quote keeps the passage without its white space at the ends, and 32 characters on each side", () => {
  const text =
    "Notices of the lead\nNotices that come in less than two seconds leave together, in one line of the digest.";
  const start = text.indexOf("less") - 1;
  const end = text.indexOf("digest") + 7;
  const q = b.quote(text, start, end);
  assert.equal(
    q.exact,
    "less than two seconds leave together, in one line of the digest.",
  );
  assert.equal(q.start, start + 1);
  assert.equal(q.end, text.length);
  assert.equal(q.prefix, text.slice(q.start - 32, q.start));
  assert.equal(q.prefix.length, 32);
  assert.equal(q.suffix, "");
  // Near the start of the page, the prefix is what there is.
  assert.equal(b.quote(text, 0, 7).prefix, "");
  assert.equal(b.quote(text, 0, 7).suffix, text.slice(7, 39));
  // Nothing but white space quotes nothing.
  assert.equal(b.quote("a  \n b", 1, 4), null);
  assert.equal(b.quote("abc", 2, 2), null);
});

test("a quote keeps the first 2000 characters of a long selection", () => {
  const text = "word ".repeat(1000);
  const q = b.quote(text, 0, text.length);
  assert.ok(q.exact.length <= 2000);
  assert.ok(q.exact.length >= 1996);
  assert.equal(q.exact, text.slice(0, q.exact.length));
  assert.ok(!q.exact.endsWith(" "));
  assert.equal(q.end, q.exact.length);
});

test("a quote is found again where it is, by its context when it is there twice, else the nearest", () => {
  const text =
    "Grouping\nA notice waits two seconds.\nDisplay\nA notice waits two seconds, then shows.";
  const second = text.lastIndexOf("two seconds");
  const q = b.quote(text, second, second + "two seconds".length);
  assert.deepEqual({ ...b.locate(text, q) }, { start: second, end: q.end });
  // The page changed above it: the context still tells the second from the first.
  const moved = "A new paragraph.\n" + text;
  assert.equal(b.locate(moved, q).start, second + 17);
  // Same context both times: the nearest to where it was.
  const twice = "x two seconds y\nx two seconds y";
  const near = {
    exact: "two seconds",
    prefix: "x ",
    suffix: " y",
    start: 15,
    end: 26,
  };
  assert.equal(b.locate(twice, near).start, 18);
  assert.equal(b.locate(twice, { ...near, start: 0 }).start, 2);
  // Gone from the page: not found; relocate looks for it changed.
  assert.equal(b.locate(text, { ...q, exact: "three seconds" }), null);
  assert.equal(b.locate(text, { ...q, exact: "" }), null);
});

// The mockup's example tilasm (L08), in two versions: v4 adds a section on paused wishes above, moves Grouping below
// Display, rewords the failure line, drops the line on read notices, and draws the arrow to the window.
const v3 = [
  "Notices of the lead: specification",
  "Djinn tells the lead when a task ends, fails or merges. This page groups those notices by wish.",
  "Grouping",
  "The notices of one wish that come in less than two seconds leave together, in a single line of the digest.",
  "A failure notice never waits: it leaves alone and at once, even in the middle of a burst.",
  "Display",
  "The window shows how many notices wait in the wish's tab.",
  "A notice read leaves the attention bar after ten seconds.",
  "task done file of the lead watch",
  "Figure 1 - The path of a notice",
  "Cost",
  "No notice wakes the lead while it works: the watch keeps them.",
].join("\n");
const v4 = [
  "Notices of the lead: specification",
  "Djinn tells the lead when a task ends, fails or merges. This page groups those notices by wish.",
  "Paused wish",
  "The notices of a paused wish wait for it to resume, but failures.",
  "Display",
  "The window shows how many notices wait in the wish's tab.",
  "A notice read stays in the wish's journal.",
  "task done file of the lead watch to the window",
  "Figure 1 - The path of a notice, up to the window",
  "Grouping",
  "The notices of one wish that come in less than two seconds leave together, in a single line of the digest.",
  "A failure notice never waits: it leaves alone and without delay, even in the middle of a burst.",
  "Cost",
  "No notice wakes the lead while it works: the watch keeps them.",
].join("\n");
const quoted = (text, exact) => {
  const start = text.indexOf(exact);
  assert.ok(start >= 0, exact);
  return b.quote(text, start, start + exact.length);
};
const relocated = (text, anchor) => {
  const r = b.relocate(text, anchor);
  return r.status === "lost"
    ? ["lost"]
    : [r.status, text.slice(r.start, r.end)];
};

test("a new version finds a passage moved, changed, or lost: the mockup's pairs", () => {
  // Moved below Display, word for word: found where it went.
  const moved = quoted(v3, "in a single line of the digest");
  const found = b.relocate(v4, moved);
  assert.equal(found.status, "found");
  assert.equal(found.start, v4.indexOf("in a single line of the digest"));
  // Reworded: the run of its words that shares most of them, without the words at its ends it did not have.
  assert.deepEqual(
    relocated(
      v4,
      quoted(v3, "it leaves alone and at once, even in the middle of a burst"),
    ),
    [
      "changed",
      "it leaves alone and without delay, even in the middle of a burst",
    ],
  );
  // Its last words replaced: as many words stand in for them, within the block.
  assert.deepEqual(
    relocated("A failure: it leaves alone and without delay.\nDisplay", {
      exact: "it leaves alone and at once",
      start: 11,
    }),
    ["changed", "it leaves alone and without delay"],
  );
  assert.deepEqual(
    relocated("A failure: it leaves alone and\nDisplay settings", {
      exact: "it leaves alone and at once",
      start: 11,
    }),
    ["changed", "it leaves alone and"],
  );
  // Dropped: nothing shares enough of its words.
  assert.deepEqual(relocated(v4, quoted(v3, "after ten seconds")), ["lost"]);
  // The same page finds every quote as it was.
  for (const exact of ["after ten seconds", "two seconds leave together"])
    assert.equal(b.relocate(v3, quoted(v3, exact)).status, "found");
});

test("a changed passage keeps at least 60 % of its words, whatever their case and punctuation", () => {
  const anchor = { exact: "one two three four five", start: 0 };
  // Its last two words replaced: the two that stand in for them come with it, not the one after.
  assert.deepEqual(relocated("x One, two; THREE six seven y", anchor), [
    "changed",
    "One, two; THREE six seven",
  ]);
  // Two words of five: lost.
  assert.deepEqual(relocated("x one two six seven eight y", anchor), ["lost"]);
  // Letters beyond ASCII are letters: the word stays whole.
  assert.deepEqual(
    relocated("a jalape\u00f1o pepper, roasted", {
      exact: "jalape\u00f1o peppers, roasted",
    }),
    ["changed", "jalape\u00f1o pepper, roasted"],
  );
  // A word repeated counts as often as the quote has it.
  assert.deepEqual(relocated("wait wait wait wait", { exact: "wait for it" }), [
    "lost",
  ]);
  // Two runs as close: the nearest to where it was.
  const twice = "red green blue\nred green gold\nred green gold";
  const near = b.relocate(twice, {
    exact: "red green black",
    start: twice.lastIndexOf("red"),
  });
  assert.equal(near.status, "changed");
  assert.equal(near.start, twice.lastIndexOf("red"));
  // Nothing to look for, or nowhere: lost.
  assert.deepEqual(relocated("text", { exact: "  ,; " }), ["lost"]);
  assert.deepEqual(relocated("", { exact: "word" }), ["lost"]);
});

test("a new version finds an element by its id, its words at its path or elsewhere, changed, or lost", () => {
  const el = (path, fingerprint, id = "") => ({
    id,
    path,
    tag: path.split(">").pop().trim().split(":")[0],
    fingerprint,
  });
  const figure = {
    elementId: "",
    path: "figure:nth-of-type(1)",
    fingerprint:
      "task done file of the lead watch Figure 1 - The path of a notice",
  };
  // The mockup's figure, which v4 draws an arrow into and captions longer: changed.
  const v4figure = el(
    "figure:nth-of-type(1)",
    "task done file of the lead watch to the window Figure 1 - The path of a notice, up to the window",
  );
  assert.deepEqual(
    { ...b.findElement([v4figure], figure) },
    {
      status: "changed",
      index: 0,
      score: 1,
    },
  );
  // Moved after another figure, its words the same: found where it went.
  const other = el("figure:nth-of-type(1)", "A table of waits");
  const moved = el(
    "figure:nth-of-type(2)",
    "Task done, file of the lead: watch. Figure 1 - the path of a notice",
  );
  assert.equal(b.findElement([other, moved], figure).index, 1);
  assert.equal(b.findElement([other, moved], figure).status, "found");
  // Its id finds it, whatever its words.
  const byId = { ...figure, elementId: "path" };
  assert.deepEqual(
    {
      ...b.findElement(
        [other, el("figure:nth-of-type(2)", "New words", "path")],
        byId,
      ),
    },
    {
      status: "found",
      index: 1,
    },
  );
  // A paragraph is never found for a figure, and words too far are lost.
  assert.equal(
    b.findElement([el("p:nth-of-type(1)", figure.fingerprint)], figure).status,
    "lost",
  );
  assert.equal(b.findElement([other], figure).status, "lost");
  assert.equal(b.findElement([], figure).status, "lost");
});

test("a selection quoted from the pieces is found again in the same page", () => {
  const pieces = [
    { data: "Notices of the lead", block: "h1" },
    { data: "A notice ", block: "p" },
    { data: "waits", block: "p" },
    { data: " two seconds.", block: "p" },
  ];
  const layout = b.pageText(pieces);
  // From "waits" (piece 2, 0) to "two" (piece 3, 4).
  const q = b.quote(
    layout.text,
    b.textOffset(layout, 2, 0),
    b.textOffset(layout, 3, 4),
  );
  assert.equal(q.exact, "waits two");
  assert.equal(q.prefix, "Notices of the lead\nA notice ");
  const found = b.locate(layout.text, q);
  assert.equal(layout.piece[found.start], 2);
  assert.equal(layout.offset[found.start], 0);
  assert.equal(layout.piece[found.end - 1], 3);
  assert.equal(layout.offset[found.end - 1], 3);
});

test("the page is named within its tilasm, with or without its key", () => {
  const id = "01a1223a-ae45-728f-8c37-c005eee91edb";
  assert.equal(b.pageName(`/tilasm/${id}/`), "index.html");
  assert.equal(b.pageName(`/tilasm/${id}/@0123abcd/`), "index.html");
  assert.equal(
    b.pageName(`/tilasm/${id}/@0123abcd/notes/a%20b.html`),
    "notes/a b.html",
  );
  assert.equal(b.pageName(`/tilasm/${id}/docs/`), "docs/index.html");
  assert.equal(b.pageName(`/tilasm/${id}/index.html`), "index.html");
});

const passage = {
  page: "index.html",
  exact: "two seconds",
  prefix: "A notice waits ",
  suffix: ", then shows.",
  start: 40,
  end: 51,
  label: "§ Grouping",
};
const at = { left: 10, top: 20, right: 110, bottom: 38 };
const element = {
  page: "index.html",
  elementId: "",
  path: "section:nth-of-type(2) > figure:nth-of-type(1)",
  fingerprint: "task done lead queue watch Figure 1 - The path of a notice",
  label: "figure: Figure 1 - The path of a notice",
};

test("the window takes from the frame only what the bridge sends", () => {
  assert.deepEqual(
    w.fromFrame({ djinn: "tilasm", type: "ready", page: "index.html" }),
    {
      type: "ready",
      page: "index.html",
    },
  );
  assert.deepEqual(
    w.fromFrame({ djinn: "tilasm", type: "selection", passage, at }),
    {
      type: "selection",
      passage,
      at,
    },
  );
  assert.deepEqual(
    w.fromFrame({ djinn: "tilasm", type: "selection", passage: null }),
    {
      type: "selection",
      passage: null,
    },
  );
  assert.deepEqual(
    w.fromFrame({ djinn: "tilasm", type: "key", name: "comment" }),
    {
      type: "key",
      name: "comment",
    },
  );
  assert.deepEqual(
    w.fromFrame({ djinn: "tilasm", type: "key", name: "escape" }),
    { type: "key", name: "escape" },
  );
  assert.deepEqual(
    w.fromFrame({ djinn: "tilasm", type: "element", hover: at }),
    { type: "element", hover: at },
  );
  assert.deepEqual(
    w.fromFrame({ djinn: "tilasm", type: "element", hover: null }),
    { type: "element", hover: null },
  );
  assert.deepEqual(
    w.fromFrame({ djinn: "tilasm", type: "element", element, at }),
    { type: "element", element, at },
  );
  const anchors = [
    { id: "a", status: "found", top: 12, bottom: 30 },
    { id: "b", status: "changed", top: -40, bottom: -20, now: "sent alone" },
    { id: "c", status: "lost" },
  ];
  assert.deepEqual(
    w.fromFrame({ djinn: "tilasm", type: "placed", version: 4, anchors }),
    { type: "placed", version: 4, anchors },
  );
  // Only the fields it knows are kept.
  const extra = w.fromFrame({
    djinn: "tilasm",
    type: "selection",
    passage: { ...passage, html: "<b>" },
    at: { ...at, x: 1 },
  });
  assert.deepEqual(extra.passage, passage);
  assert.deepEqual(extra.at, at);
});

test("the window refuses a message of another shape, or a quote longer than an anchor holds", () => {
  const sel = (p, a = at) => ({
    djinn: "tilasm",
    type: "selection",
    passage: p,
    at: a,
  });
  for (const data of [
    null,
    "selection",
    { type: "ready", page: "index.html" },
    { djinn: "other", type: "ready", page: "index.html" },
    { djinn: "tilasm", type: "ready" },
    { djinn: "tilasm", type: "key", name: "send" },
    { djinn: "tilasm", type: "anchors", anchors: [] },
    sel({ ...passage, exact: "x".repeat(2001) }),
    sel({ ...passage, exact: "   " }),
    sel({ ...passage, prefix: "x".repeat(201) }),
    sel({ ...passage, label: "x".repeat(1001) }),
    sel({ ...passage, page: "x".repeat(4097) }),
    sel({ ...passage, start: -1 }),
    sel({ ...passage, start: 1.5 }),
    sel({ ...passage, end: 39 }),
    sel({ ...passage, exact: 3 }),
    sel(passage, { ...at, left: Infinity }),
    sel(passage, { ...at, top: "20" }),
    sel(passage, null),
    { djinn: "tilasm", type: "selection", passage },
    { djinn: "tilasm", type: "element", hover: { ...at, left: NaN } },
    { djinn: "tilasm", type: "element", element },
    { djinn: "tilasm", type: "element", element: { ...element, path: "" }, at },
    {
      djinn: "tilasm",
      type: "element",
      element: { ...element, fingerprint: "x".repeat(2001) },
      at,
    },
    { djinn: "tilasm", type: "element", element: { ...element, label: 1 }, at },
    { djinn: "tilasm", type: "placed", anchors: [] },
    { djinn: "tilasm", type: "placed", version: -1, anchors: [] },
    { djinn: "tilasm", type: "placed", version: 2, anchors: {} },
    {
      djinn: "tilasm",
      type: "placed",
      version: 2,
      anchors: [{ id: "a", status: "moved" }],
    },
    {
      djinn: "tilasm",
      type: "placed",
      version: 2,
      anchors: [{ id: "a", status: "found", top: 3 }],
    },
    {
      djinn: "tilasm",
      type: "placed",
      version: 2,
      anchors: [{ id: "a", status: "changed", now: "x".repeat(2001) }],
    },
    {
      djinn: "tilasm",
      type: "placed",
      version: 2,
      anchors: Array.from({ length: 10001 }, () => ({
        id: "a",
        status: "lost",
      })),
    },
  ])
    assert.equal(w.fromFrame(data), null, JSON.stringify(data)?.slice(0, 80));
  assert.notEqual(
    w.fromFrame(sel({ ...passage, exact: "x".repeat(2000) })),
    null,
  );
});

test("the marks are the anchors of the comments on a passage, and of the card being written, never their text", () => {
  const comment = (id, state, anchor) =>
    w.create(w.TilasmCommentSchema, {
      id,
      tilasmId: "t",
      code: "C1",
      text: "secret words",
      state,
      anchor,
    });
  const marks = w.marksOf(
    [
      comment("a", w.CommentState.DRAFT, { ...passage, page: "" }),
      comment("b", w.CommentState.DRAFT),
      comment("c", w.CommentState.DRAFT, { elementId: "fig", label: "figure" }),
      comment("d", w.CommentState.SENT, { ...passage, page: "notes.html" }),
      comment("e", w.CommentState.DRAFT, { ...element, page: "" }),
    ],
    { ...passage, exact: "notice" },
    "e",
  );
  assert.deepEqual(
    marks.map((m) => [m.id, m.state, m.page, m.exact, m.elementId, m.path]),
    [
      ["a", "draft", "index.html", "two seconds", "", ""],
      ["c", "draft", "index.html", "", "fig", ""],
      ["d", "sent", "notes.html", "two seconds", "", ""],
      ["e", "active", "index.html", "", "", element.path],
      ["", "writing", "index.html", "notice", "", ""],
    ],
  );
  // The card being written on an element.
  assert.deepEqual(
    w
      .marksOf([], element)
      .map((m) => [m.state, m.exact, m.path, m.fingerprint]),
    [["writing", "", element.path, element.fingerprint]],
  );
  assert.ok(!JSON.stringify(marks).includes("secret"));
  assert.deepEqual(w.marksOf([]), []);
});

test("Ctrl+Alt+M comments the selection, whatever the keyboard's layout", () => {
  const key = (k, code, mods = {}) => ({
    key: k,
    code,
    ctrlKey: true,
    altKey: true,
    shiftKey: false,
    metaKey: false,
    ...mods,
  });
  assert.ok(w.isCommentKey(key("m", "KeyM")));
  // AZERTY: the M key sits where QWERTY has the semicolon.
  assert.ok(w.isCommentKey(key("m", "Semicolon")));
  assert.ok(w.isCommentKey(key("µ", "KeyM")));
  assert.ok(!w.isCommentKey(key("m", "KeyM", { altKey: false })));
  assert.ok(!w.isCommentKey(key("m", "KeyM", { ctrlKey: false })));
  assert.ok(!w.isCommentKey(key("M", "KeyM", { shiftKey: true })));
  assert.ok(!w.isCommentKey(key("n", "KeyN")));
});
