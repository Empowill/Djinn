import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";

import { bundle } from "./bundle.mjs";

const { SPLASH_ID, SPLASH_STYLE_ID, SPLASH_TEXT_ID, hideSplash, syncSplash } =
  await bundle("splash", `export * from "@/src/splash.ts";`);

test("index.html inlines the waking splash with composite-only motion, system themes, and stillness rules", () => {
  const html = fs.readFileSync("index.html", "utf8");
  assert.ok(html.includes(`id="${SPLASH_STYLE_ID}"`));
  assert.ok(html.includes(`id="${SPLASH_ID}"`));
  assert.ok(
    html.includes(
      `<p id="${SPLASH_TEXT_ID}" class="splash-line">Djinn wakes…</p>`,
    ),
  );
  assert.ok(html.includes("@media (prefers-color-scheme: light)"));
  assert.ok(html.includes('html[data-theme="light"]'));
  assert.ok(html.includes("@media (prefers-reduced-motion: reduce)"));
  assert.ok(html.includes('html[data-motion="reduced"]'));
  assert.ok(html.includes("html[data-still]"));

  // Every @keyframes rule in #djinn-splash-style animates transform and opacity only.
  const styleBlock = html.match(
    /<style id="djinn-splash-style">([\s\S]*?)<\/style>/,
  )?.[1];
  assert.ok(styleBlock, "splash style block present");
  const keyframes = [
    ...styleBlock.matchAll(/@keyframes\s+[^{]+\{([\s\S]*?\})\s*\}/g),
  ].map((m) => m[1]);
  assert.equal(keyframes.length, 2);
  for (const body of keyframes) {
    const props = [...body.matchAll(/([a-z-]+)\s*:/g)].map((m) => m[1]);
    assert.ok(props.length > 0);
    for (const prop of props) {
      assert.ok(
        prop === "transform" || prop === "opacity",
        `unexpected animated property in splash keyframes: ${prop}`,
      );
    }
  }

  const stylesCss = fs.readFileSync("src/styles.css", "utf8");
  assert.ok(stylesCss.includes("html[data-still] #djinn-splash *"));
});

test("syncSplash localizes the waking line and hideSplash removes the splash and its style sheet", () => {
  const line = { textContent: "" };
  let splashRemoved = false;
  let styleRemoved = false;
  const fakeDoc = {
    getElementById(id) {
      if (id === SPLASH_TEXT_ID) return line;
      if (id === SPLASH_ID)
        return {
          remove() {
            splashRemoved = true;
          },
        };
      if (id === SPLASH_STYLE_ID)
        return {
          remove() {
            styleRemoved = true;
          },
        };
      return null;
    },
  };

  syncSplash(fakeDoc);
  assert.equal(line.textContent, "Djinn wakes…");

  hideSplash(fakeDoc);
  assert.equal(splashRemoved, true);
  assert.equal(styleRemoved, true);
});
