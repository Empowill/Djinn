// The Markdown of a tilasm as the window's editor reads and writes it (src/tilasm-markdown.ts): what it keeps, what it
// would lose, and its title line. The editor's own Markdown manager, without a page.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const s = await bundle(
  "tilasm-markdown",
  `export { MarkdownManager } from "@tiptap/markdown";
export {
  tilasmExtensions,
  tilasmLoss,
  splitTitle,
  joinTitle,
} from "@/src/tilasm-markdown.ts";`,
);

const manager = new s.MarkdownManager({ extensions: s.tilasmExtensions() });
const roundTrip = (markdown) => manager.serialize(manager.parse(markdown));

test("the editor keeps every block and mark it offers through a round trip", () => {
  const documents = [
    "# Title\n\nA paragraph with *em*, **bold**, ~~strike~~, `code` and [a link](https://example.com).",
    "## Second\n\n### Third\n\n#### Fourth",
    "- one\n- two\n  - nested\n\n1. first\n2. second",
    "* star bullets\n* are written as dashes\n\n1) parenthesised\n2) numbers",
    "- [ ] to do\n- [x] done",
    "> a quote\n> on two lines",
    "```go\nfunc main() {}\n```\n\n    indented code",
    "Before\n\n---\n\nAfter\n\n***",
    "Line one  \nline two\\\nline three",
    "| a | b |\n|---|:-:|\n| 1 | 2 |\n| x *y* | `z` |",
    "Escapes: \\* not a list, 2 < 3 & 4 > 1, a_b_c, `a < b`.",
    "A reference [link][ref].\n\n[ref]: https://example.com/ref",
    "Autolink <https://example.com> and https://example.org.",
    "Setext\n======\n\nSub\n---",
    "Accents: ñ, ø, ß, emoji 🧞, and “quotes”.",
  ];
  for (const markdown of documents)
    assert.deepEqual(
      s.tilasmLoss(manager, markdown),
      [],
      `${markdown}\n→\n${roundTrip(markdown)}`,
    );
  assert.deepEqual(s.tilasmLoss(manager, ""), []);
});

test("the editor names what it would lose: raw HTML, images, footnotes", () => {
  assert.deepEqual(s.tilasmLoss(manager, "Text\n\n<div>raw</div>"), ["html"]);
  assert.deepEqual(
    s.tilasmLoss(manager, "A <b>bold</b> word").includes("html"),
    true,
  );
  assert.ok(s.tilasmLoss(manager, "![wick](img/wick.svg)").includes("image"));
  assert.ok(
    s.tilasmLoss(manager, "A note[^1].\n\n[^1]: the note").includes("footnote"),
  );
});

test("the title line comes out of the body when the first heading says it, and goes back on save", () => {
  assert.deepEqual(s.splitTitle("# Notes\n\nBody", "Notes"), {
    headed: true,
    body: "Body",
  });
  assert.deepEqual(s.splitTitle("# Other\n\nBody", "Notes"), {
    headed: false,
    body: "# Other\n\nBody",
  });
  assert.deepEqual(s.splitTitle("Body only", "Notes"), {
    headed: false,
    body: "Body only",
  });
  // A document with no text yet takes its title as its first heading.
  assert.deepEqual(s.splitTitle("", "Untitled"), { headed: true, body: "" });
  assert.equal(s.joinTitle(true, "New  title", "Body"), "# New title\n\nBody");
  assert.equal(s.joinTitle(true, "Title", ""), "# Title\n");
  assert.equal(s.joinTitle(false, "Title", "Body"), "Body");
  assert.equal(s.joinTitle(true, " ", "Body"), "Body");
});
