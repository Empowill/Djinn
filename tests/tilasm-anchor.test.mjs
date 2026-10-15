// The anchors of discussions and proposals on the window's side (src/tilasm-anchor.ts): the rendered text read from
// the editor's document, as Djinn reads it from the Markdown (internal/plan/tilasm_anchor.go), the anchor of a range of
// the editor, and the range an anchor finds again, identical passages never confused.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";

import { bundle } from "./bundle.mjs";

const s = await bundle(
  "tilasm-anchor",
  `export { MarkdownManager } from "@tiptap/markdown";
export { getSchema } from "@tiptap/core";
export { Node } from "@tiptap/pm/model";
export { tilasmExtensions } from "@/src/tilasm-markdown.ts";
export * from "@/src/tilasm-anchor.ts";`,
);

const extensions = s.tilasmExtensions();
const manager = new s.MarkdownManager({ extensions });
const schema = s.getSchema(extensions);
const docOf = (markdown) => s.Node.fromJSON(schema, manager.parse(markdown));

// The text of the editor's range.
const textAt = (doc, range) => doc.textBetween(range.from, range.to, " ", "");

// The editor's range of the first or nth occurrence of words in the document.
function rangeOfWords(doc, words, nth = 0) {
  const r = s.renderDoc(doc);
  let at = -1;
  for (let k = 0; k <= nth; k++) at = r.text.indexOf(words, at + 1);
  assert.ok(at >= 0, `${words} is in ${r.text}`);
  return s.rangeOf(r, { at, end: at + words.length }, doc.content.size);
}

// find is where the anchor of a range is found again, in the same or another document.
function find(doc, anchor, title = "") {
  const r = s.renderDoc(doc, title);
  const place = s.resolve(r.text, anchor, true);
  return {
    state: place.state,
    range:
      place.state === "anchored" ? s.rangeOf(r, place, doc.content.size) : null,
  };
}

test("the rendered text drops the markup, squeezes white space and leaves out what has no text", () => {
  const cases = [
    [
      "Hello **bold** and *em* `code` [link](https://x.y).",
      "Hello bold and em code link.",
    ],
    [
      "# Heading\n\nA paragraph\nwith a soft break.",
      "Heading A paragraph with a soft break.",
    ],
    ["Line one  \nline two", "Line one line two"],
    [
      "- one\n- two\n  - nested\n\n1. first\n2. second",
      "one two nested first second",
    ],
    ["- [ ] to do\n- [x] done", "to do done"],
    ["> a quote\n> on two lines", "a quote on two lines"],
    ["| a | b |\n|---|---|\n| 1 | *2* |", "a b 1 2"],
    ["```js\nlet  x =\n  1;\n```", "let x = 1;"],
    ["Before\n\n---\n\nAfter", "Before After"],
    ["Text\n\n```html live\n<b>chart</b>\n```\n\nMore", "Text More"],
    [
      "Escapes: \\* not a list, 2 &lt; 3 &amp; 4.",
      "Escapes: * not a list, 2 < 3 & 4.",
    ],
    ["Accents: ñ, emoji 🧞 here.", "Accents: ñ, emoji 🧞 here."],
  ];
  for (const [markdown, text] of cases)
    assert.equal(s.renderDoc(docOf(markdown)).text, text, markdown);
});

// The cases Djinn's rendered text must match too (internal/plan/tilasm_anchor_test.go reads the same file).
test("the rendered text agrees with Djinn's: a live HTML block gives none, any other code its text", () => {
  const cases = JSON.parse(
    fs.readFileSync(
      new URL(
        "../internal/plan/testdata/tilasm_rendered_text.json",
        import.meta.url,
      ),
      "utf8",
    ),
  );
  for (const c of cases)
    assert.equal(s.renderDoc(docOf(c.markdown)).text, c.text, c.name);
});

test("the title, a heading of the Markdown, starts the rendered text but maps to no place of the editor", () => {
  const doc = docOf("First words here.");
  const r = s.renderDoc(doc, "The  lamp");
  assert.equal(r.text, "The lamp First words here.");
  assert.equal(r.from[0], -1);
  const anchor = s.anchorOf(r, 1, 6);
  assert.deepEqual(anchor, {
    quote: "First",
    prefix: "The lamp ",
    suffix: " words here.",
  });
  assert.equal(s.rangeOf(r, { at: 0, end: 3 }, doc.content.size), null);
});

test("an anchor of a range quotes its text with context, and finds the same range again", () => {
  const doc = docOf("Hello **bold** world.\n\nNext paragraph.");
  const range = rangeOfWords(doc, "bold world");
  assert.equal(textAt(doc, range), "bold world");
  const anchor = s.anchorOf(s.renderDoc(doc), range.from, range.to);
  assert.deepEqual(anchor, {
    quote: "bold world",
    prefix: "Hello ",
    suffix: ". Next paragraph.",
  });
  assert.deepEqual(find(doc, anchor), { state: "anchored", range });
});

test("identical sentences are told apart by their context, even as the text moves", () => {
  const twice =
    "The lamp is lit and the room is warm.\n\nA wick carries the oil up from the bowl to the flame.\n\n" +
    "The lamp is lit and the room is warm.\n\nThe djinn waits.";
  const doc = docOf(twice);
  const second = rangeOfWords(doc, "The lamp is lit", 1);
  const anchor = s.anchorOf(s.renderDoc(doc), second.from, second.to);
  assert.equal(anchor.quote, "The lamp is lit");
  assert.deepEqual(find(doc, anchor).range, second);

  // Text added at the start moves both: the anchor still finds the second.
  const moved = docOf(`A new opening line.\n\n${twice}`);
  const got = find(moved, anchor);
  assert.equal(got.state, "anchored");
  assert.deepEqual(got.range, rangeOfWords(moved, "The lamp is lit", 1));

  // Without context, the two cannot be told apart.
  assert.equal(
    s.resolve(
      s.renderDoc(doc).text,
      { quote: "The lamp is lit", prefix: "", suffix: "" },
      false,
    ).state,
    "ambiguous",
  );
});

test("a passage in a list, a table or under marks maps back to its own text", () => {
  const doc = docOf(
    "- first item\n- second *item* here\n\n| name | role |\n|---|---|\n| wick | carries **oil** |",
  );
  for (const words of ["second item here", "carries oil", "wick carries"]) {
    const range = rangeOfWords(doc, words);
    const anchor = s.anchorOf(s.renderDoc(doc), range.from, range.to);
    assert.equal(anchor.quote, words);
    assert.deepEqual(find(doc, anchor).range, range, words);
  }
  // Across two cells the range spans their boundary; the text between them is one space.
  const across = rangeOfWords(doc, "wick carries");
  assert.equal(textAt(doc, across), "wick carries");
});

test("a range without text anchors the place where the next text starts", () => {
  const doc = docOf("One paragraph.\n\nTwo paragraph.");
  const two = rangeOfWords(doc, "Two");
  const anchor = s.anchorOf(s.renderDoc(doc), two.from, two.from);
  assert.equal(anchor.quote, "");
  assert.equal(anchor.prefix, "One paragraph. ");
  const got = find(doc, anchor);
  assert.equal(got.state, "anchored");
  assert.deepEqual(got.range, { from: two.from, to: two.from });
});

test("a caret at the end of a paragraph anchors the place after its last word, not the next paragraph's start", () => {
  const doc = docOf("The flame needs air.\n\nThe glass keeps the wind out.");
  const words = rangeOfWords(doc, "needs air.");
  const anchor = s.anchorOf(s.renderDoc(doc), words.to, words.to);
  assert.equal(anchor.quote, "");
  assert.equal(anchor.prefix, "The flame needs air.");
  assert.ok(anchor.suffix.startsWith(" The glass"), anchor.suffix);
  assert.deepEqual(find(doc, anchor).range, { from: words.to, to: words.to });
});

test("a passage edited, deleted or repeated is never placed elsewhere", () => {
  const doc = docOf(
    "Intro words that set the scene for the reader.\n\nThe wick carries the oil.\n\nOutro words that close the scene for good.",
  );
  const range = rangeOfWords(doc, "The wick carries the oil.");
  const anchor = s.anchorOf(s.renderDoc(doc), range.from, range.to);

  const edited = docOf(
    "Intro words that set the scene for the reader.\n\nThe wick drinks the oil.\n\nOutro words that close the scene for good.",
  );
  assert.equal(find(edited, anchor).state, "changed");

  const deleted = docOf(
    "Intro words that set the scene for the reader.\n\nOutro words that close the scene for good.",
  );
  assert.equal(find(deleted, anchor).state, "missing");

  const repeated = docOf(
    "Intro words that set the scene for the reader.\n\nThe wick carries the oil.\n\nThe wick carries the oil.\n\n" +
      "Outro words that close the scene for good.",
  );
  assert.notEqual(find(repeated, anchor).state, "anchored");
});
