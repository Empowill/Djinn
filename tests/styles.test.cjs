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
    assert.equal(states.length, 11, `${name}: the eleven states have a colour`);
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

// Every surface follows the theme through the tokens of theme.css. The light ramp lifts the texts to 4.5:1 on the page
// and on a card, in order; the terminal's colours read at 4.5:1 on its light ground; the warm family and the danger
// colour read on their grounds in both themes. Clément's dim greys (65 to 88) stay quiet in the dark theme.
test("the theme's tokens keep their contrast in the dark and the light theme", () => {
  const css = fs.readFileSync(path.join(root, "theme.css"), "utf8");
  const block = (selector) => {
    const start = css.indexOf(selector + " {");
    return css.slice(start, css.indexOf("\n}", start));
  };
  const tokens = (text) =>
    Object.fromEntries(
      [...text.matchAll(/--([\w-]+):\s*(#[0-9a-f]{6,8});/g)].map((m) => [
        m[1],
        m[2],
      ]),
    );
  const dark = tokens(block(":root"));
  const light = tokens(block('html[data-theme="light"]'));
  assert.deepEqual(
    Object.keys(light).sort(),
    Object.keys(dark).sort(),
    "each token has a light value",
  );
  const lum = (hex) => {
    const [r, g, b] = [1, 3, 5].map((i) => {
      const v = parseInt(hex.slice(i, i + 2), 16) / 255;
      return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const ratio = (a, b) => {
    const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
    return (x + 0.05) / (y + 0.05);
  };
  const reads = (name, theme, colour, grounds) => {
    for (const ground of grounds) {
      const r = ratio(theme[colour], theme[ground]);
      assert.ok(
        r >= 4.5,
        `${name}: ${colour} on ${ground} reads at ${r.toFixed(2)}:1`,
      );
    }
  };
  const ramp = Object.keys(dark)
    .filter((k) => k.startsWith("n-"))
    .sort();
  const level = (k) => parseInt(k.slice(2), 16);
  for (const k of ramp) {
    assert.equal(dark[k], "#" + k.slice(2).repeat(3), `${k} is its own grey`);
    if (level(k) >= 0x65) reads("light", light, k, ["n-11", "n-19"]);
    if (level(k) >= 0x8f) reads("dark", dark, k, ["n-11", "n-19"]);
  }
  const texts = ramp.filter((k) => level(k) >= 0x65);
  for (let i = 1; i < texts.length; i++)
    assert.ok(
      lum(light[texts[i]]) <= lum(light[texts[i - 1]]),
      `light: ${texts[i]} is no lighter than ${texts[i - 1]}`,
    );
  for (const colour of Object.keys(light).filter(
    (k) => k.startsWith("term-") && !/bg|cursor|selection/.test(k),
  ))
    reads("light", light, colour, ["term-bg"]);
  reads("dark", dark, "term-fg", ["term-bg"]);
  for (const theme of [
    ["dark", dark],
    ["light", light],
  ]) {
    for (const colour of ["sand", "sand-ink", "sand-text"])
      reads(theme[0], theme[1], colour, ["sand-bg", "sand-raised"]);
    reads(theme[0], theme[1], "danger", ["n-11", "n-19"]);
  }
});

// No grey is written by hand outside the tokens: a new rule takes a token, and so follows the theme.
test("the stylesheets take their greys from the tokens", () => {
  const defined = new Set(
    [
      ...fs
        .readFileSync(path.join(root, "theme.css"), "utf8")
        .matchAll(/^\s+--([\w-]+):/gm),
    ].map((m) => m[1]),
  );
  for (const entry of fs.readdirSync(root).filter((e) => e.endsWith(".css"))) {
    const css = fs.readFileSync(path.join(root, entry), "utf8");
    for (const m of css.matchAll(/var\(--(n-[0-9a-f]{2}|term-[\w-]+|sand[\w-]*)\b/g))
      assert.ok(defined.has(m[1]), `${entry}: --${m[1]} is a token`);
    if (entry === "theme.css") continue;
    // Outside the blocks of tokens, the rules of the light theme alone and the masks, a grey is a token.
    const rules = css
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .replace(/(^|\n)(:root|html\[data-theme="light"\][^{]*) \{[^}]*\}/g, "")
      .replace(/mask[\w-]*:[^;]*;/g, "");
    for (const m of rules.matchAll(/#([0-9a-f]{3,8})\b/gi)) {
      const hex = m[1].length <= 4 ? [...m[1]].map((c) => c + c).join("") : m[1];
      const grey = hex.slice(0, 2) === hex.slice(2, 4) && hex.slice(2, 4) === hex.slice(4, 6);
      assert.ok(!grey, `${entry}: #${m[1]} is a grey written by hand`);
    }
  }
});

// No style outlives its element: every class a stylesheet names is written in some component of src/. A class built
// at run time takes its family's prefix here; a class left only in a :not() would always match, so it counts too.
test("every class of the stylesheets is used by a component", () => {
  const dynamic = [
    /^tone-/, // status.tsx and the task, wish and plan lines: tone-${tone}
    /^level-/, // attention.tsx: level-${item.level}
    /^kind-\d+$/, // wish-task.tsx: kind-${event.kind}, a TaskEvent kind
    /^work-/, // wish-task.tsx: work-${work.tone}, where a task's work stands
  ];
  const components = fs
    .readdirSync(root, { recursive: true })
    .filter((entry) => entry.endsWith(".tsx"))
    .map((entry) => fs.readFileSync(path.join(root, entry), "utf8"))
    .join("\n");
  const unused = [];
  for (const entry of fs.readdirSync(root).filter((e) => e.endsWith(".css"))) {
    // The selectors only: comments, strings and declarations hold dots that are no class.
    const selectors = fs
      .readFileSync(path.join(root, entry), "utf8")
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .replace(/"[^"]*"|'[^']*'/g, '""')
      .matchAll(/(?<=^|[;{}])([^;{}]*)\{/g);
    const classes = new Set();
    for (const [, selector] of selectors) {
      if (selector.trim().startsWith("@")) continue;
      for (const [, name] of selector.matchAll(/\.(-?[_a-zA-Z][\w-]*)/g)) classes.add(name);
    }
    for (const name of classes) {
      if (dynamic.some((family) => family.test(name))) continue;
      const word = new RegExp(`(?<![\\w-])${name}(?![\\w-])`);
      if (!word.test(components)) unused.push(`${entry}: .${name}`);
    }
  }
  assert.deepEqual(unused, [], "classes no component uses: remove their rules");
});
