"use strict";

// Clément's styles keep functional text at 12px, in every stylesheet of the interface.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const root = path.resolve(__dirname, "../src");

test("the shared CSS contract keeps functional text at 12px", () => {
  const css = fs
    .readdirSync(root)
    .filter((entry) => entry.endsWith(".css"))
    .map((entry) => fs.readFileSync(path.join(root, entry), "utf8"))
    .join("\n");
  assert.match(css, /--font-size-body:\s*12px/);
  assert.doesNotMatch(css, /font-size\s*:\s*(?:[1-9]|1[01])px/);
  assert.doesNotMatch(css, /font\s*:\s*(?:[1-9]|1[01])px/);
});

// The status language reads in both themes: every status colour on its badge (13% of itself over the page and over a
// card) keeps a contrast of 4.5:1 at least, the WCAG AA level for text.
test("the status colours keep their contrast in the dark and the light theme", () => {
  const css = fs.readFileSync(path.join(root, "review.css"), "utf8");
  const block = (selector) => {
    const start = css.indexOf(selector + " {");
    return css.slice(start, css.indexOf("}", start));
  };
  const tokens = (text) =>
    Object.fromEntries(
      [...text.matchAll(/--([\w-]+):\s*(#[0-9a-f]{6});/g)].map((m) => [
        m[1],
        m[2],
      ]),
    );
  const rgb = (hex) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
  const lum = (c) => {
    const [r, g, b] = c.map((v) => {
      v /= 255;
      return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const ratio = (a, b) => {
    const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
    return (x + 0.05) / (y + 0.05);
  };
  const mix = (a, b, share) => a.map((v, i) => v * share + b[i] * (1 - share));
  const themes = {
    dark: { ...tokens(block(":root")), grounds: ["#111111", "#191919"] },
    light: {
      ...tokens(block('html[data-theme="light"]')),
      grounds: ["#f6f6f4", "#ffffff"],
    },
  };
  for (const [name, theme] of Object.entries(themes)) {
    const states = Object.keys(theme).filter((k) => k.startsWith("st-"));
    assert.equal(states.length, 9, `${name}: the nine states have a colour`);
    for (const state of states)
      for (const ground of theme.grounds) {
        const tone = rgb(theme[state]);
        const badge = mix(tone, rgb(ground), 0.13);
        const r = ratio(tone, badge);
        assert.ok(
          r >= 4.5,
          `${name}: ${state} on ${ground} reads at ${r.toFixed(2)}:1`,
        );
      }
  }
});
