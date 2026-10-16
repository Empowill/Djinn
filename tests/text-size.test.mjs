// The window's text size (src/text-size.ts): kept in the page's storage, set on <html> as the theme is, and read back
// as a scale by what lays out in pixels. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const s = await bundle(
  "text-size",
  `export {
  applyTextSize,
  chosenTextSize,
  setTextSize,
  shownTextSize,
  textScale,
  textSizes,
} from "@/src/text-size.ts";`,
);

// A page of its own: a storage, an <html> whose font size the stylesheet's steps give (styles.css).
function page({ refusing = false } = {}) {
  const items = new Map();
  const no = () => {
    throw new Error("storage refused");
  };
  const dataset = {};
  const rootSize = {
    large: "18.6667px",
    compact: "14.6667px",
    "very-compact": "13.3333px",
  };
  globalThis.window = {
    localStorage: {
      getItem: refusing ? no : (k) => items.get(k) ?? null,
      setItem: refusing ? no : (k, v) => items.set(k, String(v)),
      removeItem: refusing ? no : (k) => items.delete(k),
    },
  };
  globalThis.document = { documentElement: { dataset } };
  globalThis.getComputedStyle = () => ({
    fontSize: rootSize[dataset.textSize] ?? "16px",
  });
  return { items, dataset };
}

test.afterEach(() => {
  delete globalThis.window;
  delete globalThis.document;
  delete globalThis.getComputedStyle;
});

test("the default is today's size: no step on the page, a scale of 1", () => {
  const { dataset } = page();
  assert.equal(s.chosenTextSize(), "");
  s.applyTextSize();
  assert.equal(dataset.textSize, undefined);
  assert.equal(s.shownTextSize(), "");
  assert.equal(s.textScale(), 1);
  assert.deepEqual(s.textSizes, ["large", "", "compact", "very-compact"]);
});

test("a step chosen is kept, set on the page, and scales what lays out in pixels", () => {
  const { items, dataset } = page();
  s.setTextSize("compact");
  assert.equal(items.get("djinn.text-size"), "compact");
  assert.equal(dataset.textSize, "compact");
  assert.equal(s.chosenTextSize(), "compact");
  assert.ok(Math.abs(s.textScale() - 11 / 12) < 1e-4);
  s.setTextSize("very-compact");
  assert.ok(Math.abs(s.textScale() - 10 / 12) < 1e-4);
  s.setTextSize("large");
  assert.ok(Math.abs(s.textScale() - 14 / 12) < 1e-4);
  // Back to the default: nothing kept, nothing on the page.
  s.setTextSize("");
  assert.equal(items.has("djinn.text-size"), false);
  assert.equal(dataset.textSize, undefined);
  assert.equal(s.textScale(), 1);
});

test("an unknown kept value and a refused storage read as the default", () => {
  const { items } = page();
  items.set("djinn.text-size", "huge");
  assert.equal(s.chosenTextSize(), "");
  page({ refusing: true });
  assert.equal(s.chosenTextSize(), "");
  // The step still shows, for this page only.
  s.setTextSize("compact");
  assert.equal(s.shownTextSize(), "compact");
});
