"use strict";

// Clément's styles keep functional text at 12px, in every stylesheet of the interface: 0.75rem at the default text
// size, where 1rem is 16px (styles.css); the smaller steps of the personal space shrink it on purpose.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const root = path.resolve(__dirname, "../src");

// The ::after rule of one family of the hover (styles.css, L17 proposal 1), found by the words of its comment.
const hoverFamily = (words) => {
  const css = fs.readFileSync(path.join(root, "styles.css"), "utf8");
  const from = css.indexOf(words);
  assert.ok(from >= 0, `the hover's family "${words}" is described`);
  const start = css.indexOf(":where(", from);
  return css.slice(start, css.indexOf("}", css.indexOf("::after", start)));
};

const stylesheets = () =>
  fs
    .readdirSync(root)
    .filter((entry) => entry.endsWith(".css"))
    .map((entry) => [entry, fs.readFileSync(path.join(root, entry), "utf8")]);

test("the shared CSS contract keeps functional text at 12px", () => {
  const css = stylesheets()
    .map(([, text]) => text)
    .join("\n");
  assert.match(css, /--font-size-body:\s*0\.75rem/);
  assert.doesNotMatch(css, /font-size\s*:\s*(?:[1-9]|1[01])px/);
  assert.doesNotMatch(css, /font\s*:\s*(?:[1-9]|1[01])px/);
  // The same, written in rem: 1px to 11px.
  const under = Array.from({ length: 11 }, (_, i) =>
    String((i + 1) / 16).replace(".", "\\."),
  ).join("|");
  assert.doesNotMatch(css, new RegExp(`font-size\\s*:\\s*(?:${under})rem`));
  assert.doesNotMatch(css, new RegExp(`font\\s*:\\s*(?:${under})rem`));
});

// The text size of the personal space (text-size.ts) scales the root font size: every length of the stylesheets is in
// rem or em, or it would stay put while the text shrinks. Stay in px: hairlines (1px and less), borders and outlines,
// SVG strokes and filters, media queries (they read the browser's own size, never the root's), the smoke's measures
// (wish-smoke.tsx lays it out in px), the root's own steps, and the text of SVG diagrams, in their drawing's units.
test("every length of the stylesheets follows the text size", () => {
  const kept =
    /^(?:border(?:-(?:top|right|bottom|left|block|inline)(?:-(?:start|end))?)?(?:-width)?|outline(?:-width|-offset)?|stroke-\w+|(?:-webkit-)?(?:backdrop-)?filter|text-decoration-thickness|--smoke-glyph-size|--smoke-row-height)$/;
  const fixed = [];
  for (const [entry, text] of stylesheets()) {
    const css = text.replace(/\/\*[\s\S]*?\*\//g, "");
    for (const [, prop, value] of css.matchAll(
      /(?<=[;{])\s*([-\w]+)\s*:\s*([^;{}]+)(?=[;}])/g,
    )) {
      if (kept.test(prop)) continue;
      if (entry === "svg-diagrams.css" && /^font/.test(prop)) continue;
      if (
        entry === "styles.css" &&
        prop === "font-size" &&
        /^1[3-9]\.?\d*px$/.test(value.trim())
      )
        continue;
      for (const [length, n] of value.matchAll(/(?<![\w.-])(\d*\.?\d+)px\b/g))
        if (Number(n) > 1) fixed.push(`${entry}: ${prop}: ${length}`);
    }
  }
  assert.deepEqual(fixed, [], "lengths in px: write them in rem (16px = 1rem)");
});

// An icon's size is its width and height attributes, in px: styles.css gives each size the components ask a rule in
// rem, so the icons follow the text size too.
test("every icon size the components give follows the text size", () => {
  const css = fs.readFileSync(path.join(root, "styles.css"), "utf8");
  const sizes = new Set();
  for (const entry of fs.readdirSync(root).filter((e) => e.endsWith(".tsx"))) {
    const text = fs.readFileSync(path.join(root, entry), "utf8");
    for (const [, sizesText] of text.matchAll(/\bsize=\{([^}]*)\}/g))
      for (const [n] of sizesText.matchAll(/\b\d+\b/g)) sizes.add(Number(n));
    for (const [, n] of text.matchAll(/\bsize = (\d+)/g)) sizes.add(Number(n));
  }
  const missing = [...sizes]
    .filter((n) => n < 30)
    .filter((n) => {
      const rule = new RegExp(
        `:where\\(svg\\.lucide\\[width="${n}"\\]\\) \\{\\s*width: ${n / 16}rem;\\s*height: ${n / 16}rem;`,
      );
      return !rule.test(css);
    });
  assert.deepEqual(missing, [], "icon sizes without their rule in styles.css");
});

// The steps of the text size: one root font size each, the same in the window and in the documentation site, which
// the window opens at its own step (docs.tsx, docs/site/theme.js).
test("each text size has its root font size, the same in the documentation site", () => {
  const source = fs.readFileSync(path.join(root, "text-size.ts"), "utf8");
  const steps = [
    ...source.match(/textSizes[^=]*=\s*\[([^\]]*)\]/)[1].matchAll(/"([^"]*)"/g),
  ].map((m) => m[1]);
  // The steps themselves, in order: text-size.test.mjs.
  const sizes = (css) =>
    Object.fromEntries(
      [
        ...css.matchAll(
          /^html(?:\[data-text-size="([\w-]+)"\])? \{\s*font-size: ([\d.]+px);/gm,
        ),
      ].map((m) => [m[1] ?? "", m[2]]),
    );
  const window = sizes(fs.readFileSync(path.join(root, "styles.css"), "utf8"));
  assert.deepEqual(Object.keys(window).sort(), [...steps].sort());
  assert.equal(window[""], "16px");
  const site = sizes(
    fs.readFileSync(path.join(root, "../docs/site/site.css"), "utf8"),
  );
  assert.deepEqual(site, window);
  const theme = fs.readFileSync(
    path.join(root, "../docs/site/theme.js"),
    "utf8",
  );
  for (const step of steps.filter(Boolean))
    assert.match(theme, new RegExp(`"${step}"`));
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
    dark: { ...tokens(block(":root")), grounds: ["#0e0e10", "#151517"] },
    light: {
      ...tokens(block('html[data-theme="light"]')),
      grounds: ["#f6f6f8", "#ffffff"],
    },
  };
  for (const [name, theme] of Object.entries(themes)) {
    const states = Object.keys(theme).filter((k) => k.startsWith("st-"));
    assert.equal(
      states.length,
      14,
      `${name}: the fourteen states have a colour`,
    );
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

// The wish icons' colours (L09 v6) read on the menu in both themes, at rest and on a selected row: 3:1 at least, the
// WCAG level for a graphic. Each is its own token, none of the signs' colours: the lamp's gold, the ifrit's vermilion,
// the blue and violet of what runs and investigates.
test("the wish icons' colours keep their contrast on the menu and stay apart from the signs' colours", () => {
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
  const themes = {
    dark: { ...tokens(block(":root")), grounds: ["#141414", "#1e1e1e"] },
    light: {
      ...tokens(block('html[data-theme="light"]')),
      grounds: ["#f0f0f3", "#e3e3e8"],
    },
  };
  for (const [name, theme] of Object.entries(themes)) {
    const colours = ["wi-emerald", "wi-saffron", "wi-mint"];
    for (const colour of colours) {
      assert.match(theme[colour] ?? "", /^#/, `${name}: --${colour} is set`);
      for (const sign of ["lamp", "st-ifrit", "st-running", "st-investigating"])
        assert.notEqual(
          theme[colour],
          theme[sign],
          `${name}: --${colour} is --${sign}`,
        );
      for (const ground of theme.grounds) {
        const r = ratio(theme[colour], ground);
        assert.ok(
          r >= 3,
          `${name}: --${colour} on ${ground} reads at ${r.toFixed(2)}:1`,
        );
      }
    }
  }
  // Every duo of the bank has its class, and every class takes its colours from these tokens.
  const bank = JSON.parse(
    fs.readFileSync(path.join(root, "../wishicons/bank.json"), "utf8"),
  );
  for (const duo of new Set(bank.icons.map((icon) => icon.duo)))
    assert.match(
      css,
      new RegExp(
        `\\.wish-duo-${duo.toLowerCase()} \\{\\s*--il-line: var\\(--wi-\\w+\\);\\s*--il-gild: var\\(--wi-\\w+\\);`,
      ),
    );
});

// A wish's row in the side panel (L21): each state of its second line, its title and its rank read at 4.5:1 on the menu,
// at rest, under the hover's veil, and on a selected row's ground, which takes no veil, in both themes; the grey of a
// resting wish's icon and of a mark reads at 3:1, a graphic's level, on the same grounds.
test("a wish's row keeps its contrast at rest, hovered and selected, in the dark and the light theme", () => {
  const read = (file, selector) => {
    const css = fs.readFileSync(path.join(root, file), "utf8");
    const start = css.indexOf(selector + " {");
    return Object.fromEntries(
      [
        ...css
          .slice(start, css.indexOf("\n}", start))
          .matchAll(/--([\w-]+):\s*(#[0-9a-f]{6}|\d+%|var\(--[\w-]+\));/g),
      ].map((m) => [m[1], m[2]]),
    );
  };
  const wish = fs.readFileSync(path.join(root, "wish.css"), "utf8");
  const review = fs.readFileSync(path.join(root, "review.css"), "utf8");
  // The grounds as the stylesheets give them: the menu's, the selected row's (no veil on it), the veil's strength.
  assert.match(
    fs.readFileSync(path.join(root, "styles.css"), "utf8"),
    /\.mission-nav\.selected \{\s*background: var\(--n-1e\);/,
  );
  assert.match(
    review,
    /html\[data-theme="light"\] \.mission-nav\.selected,\nhtml\[data-theme="light"\] \.nav-item\.selected \{\s*background: var\(--line\);/,
  );
  assert.match(
    review,
    /html\[data-theme="light"\] \.sidebar \{\s*background: #f0f0f3;/,
  );
  assert.match(
    wish,
    /\.mission-nav\.wish-nav\.selected::after \{\s*opacity: 0;/,
  );
  assert.match(
    wish,
    /\.wish-nav\.rest \.wish-icon \{\s*--il-line: var\(--wi-rest\);\s*--il-gild: var\(--wi-rest\);/,
  );
  assert.match(review, /\.wish-mark \{\s*color: var\(--wi-rest\);/);
  const dark = {
    ...read("styles.css", ":root"),
    ...read("review.css", ":root"),
    ...read("theme.css", ":root"),
  };
  const themes = {
    dark: { theme: dark, menu: "n-14", selected: "n-1e", rank: "muted" },
    light: {
      theme: {
        ...dark,
        ...read("styles.css", 'html[data-theme="light"]'),
        ...read("review.css", 'html[data-theme="light"]'),
        ...read("theme.css", 'html[data-theme="light"]'),
      },
      menu: "#f0f0f3",
      selected: "line",
      rank: "n-a9",
    },
  };
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
  for (const [name, { theme, menu, selected, rank }] of Object.entries(
    themes,
  )) {
    const value = (token) => {
      if (token.startsWith("#")) return token;
      const v = theme[token];
      assert.ok(v, `${name}: --${token} is set`);
      const ref = /^var\(--([\w-]+)\)$/.exec(v);
      return ref ? value(ref[1]) : v;
    };
    const colour = (token) => rgb(value(token));
    const strength = parseFloat(value("hover-strength")) / 100;
    const veil = colour("n-ff");
    const rest = colour(menu);
    const grounds = {
      rest,
      hovered: rest.map((v, i) => veil[i] * strength + v * (1 - strength)),
      selected: colour(selected),
    };
    const texts = [
      "st-ifrit",
      "st-waiting",
      "st-running",
      "st-investigating",
      "st-paused",
      "st-planned",
      "st-watching",
      "st-proof",
      "n-9c",
      "n-e7",
      rank,
    ];
    if (name === "light") texts.push("muted", "text");
    for (const [ground, back] of Object.entries(grounds)) {
      for (const text of texts) {
        const r = ratio(colour(text), back);
        assert.ok(
          r >= 4.5,
          `${name}: --${text} ${ground} reads at ${r.toFixed(2)}:1`,
        );
      }
      const r = ratio(colour("wi-rest"), back);
      assert.ok(
        r >= 3,
        `${name}: --wi-rest ${ground} reads at ${r.toFixed(2)}:1`,
      );
    }
  }
});

// Every surface follows the theme through the tokens of theme.css. The light ramp lifts the texts to 4.5:1 on the page
// and on a card, in order; the terminal's colours read at 4.5:1 on its light ground; the family of what waits and the
// danger colour read on their grounds in both themes. Clément's dim greys (65 to 88) stay quiet in the dark theme.
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
// The update banner reads as Djinn's (src/update-banner.css): on its ground, the raised surface (no warm tint, which
// turned brown in the dark, L17), the violet of Dismiss on its 10% tint and its quiet text (--n-9c) keep 4.5:1 in both
// themes. The brass button's ink on the lamp's gradient is the pill's label at rest: Write to the Djinn's test checks it.
test("the update banner's buttons keep their contrast in the dark and the light theme", () => {
  const read = (file, selector) => {
    const css = fs.readFileSync(path.join(root, file), "utf8");
    const start = css.indexOf(selector + " {");
    return Object.fromEntries(
      [
        ...css
          .slice(start, css.indexOf("}", start))
          .matchAll(/--([\w-]+):\s*(#[0-9a-f]{6});/g),
      ].map((m) => [m[1], m[2]]),
    );
  };
  const banner = fs.readFileSync(path.join(root, "update-banner.css"), "utf8");
  assert.match(banner, /background: var\(--surface-raised\);/);
  assert.match(banner, /border: 1px solid var\(--lamp-edge\);/);
  assert.match(
    banner,
    /background: color-mix\(in srgb, var\(--st-investigating\) 10%, transparent\)/,
  );
  assert.match(
    banner,
    /background: linear-gradient\(var\(--lamp-hi\), var\(--lamp-lo\)\)/,
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
  const dark = {
    ...read("theme.css", ":root"),
    ...read("styles.css", ":root"),
    ...read("review.css", ":root"),
  };
  const themes = {
    dark,
    light: {
      ...dark,
      ...read("theme.css", 'html[data-theme="light"]'),
      ...read("review.css", 'html[data-theme="light"]'),
    },
  };
  for (const [name, theme] of Object.entries(themes)) {
    const ground = rgb(theme["surface-raised"]);
    const violet = rgb(theme["st-investigating"]);
    const pairs = {
      Dismiss: [violet, mix(violet, ground, 0.1)],
      "the quiet text": [rgb(theme["n-9c"]), ground],
      "the title": [rgb(theme.text), ground],
    };
    for (const [what, [fore, back]] of Object.entries(pairs)) {
      const r = ratio(fore, back);
      assert.ok(r >= 4.5, `${name}: ${what} reads at ${r.toFixed(2)}:1`);
    }
  }
});

// Write to the Djinn, proposal 1 Lampe of L19 (Q58 C): its ink on the lamp's gradient at rest, hovered (Calm's tint in
// the pill's own ink) and pressed (a second tint over it), the field's placeholder and text once open, keep 4.5:1 in
// both themes; the eye's line, the socket's icon and the focus ring keep 3:1 on what is around them.
test("Write to the Djinn's pill keeps its contrast in every state, in the dark and the light theme", () => {
  const read = (file, selector) => {
    const css = fs.readFileSync(path.join(root, file), "utf8");
    const start = css.indexOf(selector + " {");
    return Object.fromEntries(
      [
        ...css
          .slice(start, css.indexOf("\n}", start))
          .matchAll(/--([\w-]+):\s*(#[0-9a-f]{6}|\d+%|var\(--[\w-]+\));/g),
      ].map((m) => [m[1], m[2]]),
    );
  };
  const pill = fs.readFileSync(path.join(root, "instruction.css"), "utf8");
  assert.match(
    pill,
    /\.workspace-action-lead > \.workspace-action-surface \{\s*border: 1px solid var\(--lamp-edge\);\s*background: var\(--pill-field\);/,
  );
  assert.match(
    pill,
    /\.workspace-action-lead > \.workspace-action-surface::before \{[^}]*background: linear-gradient\(var\(--lamp-hi\), var\(--lamp-lo\)\);/,
  );
  assert.match(
    pill,
    /\.workspace-action-lead:not\(\.is-expanded\) \{\s*--workspace-action-ink: var\(--lamp-ink\);/,
  );
  assert.match(pill, /outline-color: var\(--pill-focus\);/);
  // The closed label of the pill is painted in its ink: only the wish creation pill blends.
  assert.match(
    pill,
    /\.workspace-action-primary\s+>\s+\.workspace-action-content:has\(\s*>\s+\.workspace-action-control\s*\) \{[^}]*mix-blend-mode: difference;/,
  );
  const eye = fs.readFileSync(path.join(root, "djinn-eye.css"), "utf8");
  assert.match(
    eye,
    /\.lead-socket \{[^}]*background: var\(--pill-socket\);\s*color: var\(--eye-line\);/,
  );
  assert.match(
    eye,
    /\.lead-socket-ring \{[^}]*border: 0\.09375rem solid var\(--eye-line\);/,
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
  const over = (fore, share, back) =>
    fore.map((v, i) => v * share + back[i] * (1 - share));
  const dark = {
    ...read("styles.css", ":root"),
    ...read("review.css", ":root"),
    ...read("theme.css", ":root"),
  };
  const themes = {
    dark,
    light: {
      ...dark,
      ...read("review.css", 'html[data-theme="light"]'),
      ...read("theme.css", 'html[data-theme="light"]'),
    },
  };
  for (const [name, theme] of Object.entries(themes)) {
    const value = (token) => {
      const v = theme[token];
      assert.ok(v, `${name}: --${token} is set`);
      const ref = /^var\(--([\w-]+)\)$/.exec(v);
      return ref ? value(ref[1]) : v;
    };
    const colour = (token) => rgb(value(token));
    const share = (token) => parseFloat(value(token)) / 100;
    const ink = colour("lamp-ink");
    const field = colour("pill-field");
    const texts = {};
    for (const end of ["lamp-hi", "lamp-lo"]) {
      const rest = colour(end);
      const hover = over(ink, share("hover-strength"), rest);
      const pressed = over(ink, share("press-strength"), hover);
      Object.assign(texts, {
        [`the label at rest on --${end}`]: [ink, rest],
        [`the label hovered on --${end}`]: [ink, hover],
        [`the label pressed on --${end}`]: [ink, pressed],
      });
    }
    texts["the field's placeholder"] = [colour("muted"), field];
    texts["the field's text"] = [colour("text"), field];
    for (const [what, [fore, back]] of Object.entries(texts)) {
      const r = ratio(fore, back);
      assert.ok(r >= 4.5, `${name}: ${what} reads at ${r.toFixed(2)}:1`);
    }
    const lines = {
      "the eye's line on the socket": ["eye-line", "pill-socket"],
      "the eye's line on its white": ["eye-line", "eye-white"],
      "the socket's icon": ["eye-line", "pill-socket"],
      "the focus ring on the page": ["pill-focus", "bg"],
      "the field's edge on the page": ["lamp-edge", "bg"],
    };
    for (const [what, [fore, back]] of Object.entries(lines)) {
      const r = ratio(colour(fore), colour(back));
      assert.ok(r >= 3, `${name}: ${what} stands at ${r.toFixed(2)}:1`);
    }
  }
});

// The task row's model identifier uses Djinn's violet (--st-investigating). Its WCAG AA contrast (4.5:1) on the page
// and on a card, in both themes, is the status colours' test's: it reads --st-investigating on a 13% badge of itself
// over the same grounds, which is closer to the violet than the bare ground.
test("the task model uses Djinn's violet token", () => {
  const css = fs.readFileSync(path.join(root, "wish.css"), "utf8");
  assert.match(
    css,
    /\.task-model\s*\{[^}]*color:\s*var\(--st-investigating\);/,
    "task-model uses var(--st-investigating)",
  );
});

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
    for (const m of css.matchAll(
      /var\(--(n-[0-9a-f]{2}|term-[\w-]+|sand[\w-]*)\b/g,
    ))
      assert.ok(defined.has(m[1]), `${entry}: --${m[1]} is a token`);
    if (entry === "theme.css") continue;
    // Outside the blocks of tokens, the rules of the light theme alone and the masks, a grey is a token.
    const rules = css
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .replace(/(^|\n)(:root|html\[data-theme="light"\][^{]*) \{[^}]*\}/g, "")
      .replace(/mask[\w-]*:[^;]*;/g, "");
    for (const m of rules.matchAll(/#([0-9a-f]{3,8})\b/gi)) {
      const hex =
        m[1].length <= 4 ? [...m[1]].map((c) => c + c).join("") : m[1];
      const grey =
        hex.slice(0, 2) === hex.slice(2, 4) &&
        hex.slice(2, 4) === hex.slice(4, 6);
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
    /^wish-duo-[slem]$/, // wish-icon.tsx: wish-duo-${duo}, the colours of a wish icon of the bank
    /^lucide$/, // lucide-react's icons carry it (styles.css sizes them in rem)
  ];
  const components = fs
    .readdirSync(root, { recursive: true })
    .filter((entry) => entry.endsWith(".tsx"))
    .map((entry) => fs.readFileSync(path.join(root, entry), "utf8"))
    .join("\n");
  const unused = [];
  for (const entry of fs.readdirSync(root).filter((e) => e.endsWith(".css"))) {
    // svg-diagrams.css styles SVG diagram blocks rendered by SafeSvg (drawn by tools/svgdiagrams);
    // its classes are used inside the stored SVG markup rather than by a React component.
    if (entry === "svg-diagrams.css") continue;

    // The selectors only: comments, strings and declarations hold dots that are no class.
    const selectors = fs
      .readFileSync(path.join(root, entry), "utf8")
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .replace(/"[^"]*"|'[^']*'/g, '""')
      .matchAll(/(?<=^|[;{}])([^;{}]*)\{/g);
    const classes = new Set();
    for (const [, selector] of selectors) {
      if (selector.trim().startsWith("@")) continue;
      for (const [, name] of selector.matchAll(/\.(-?[_a-zA-Z][\w-]*)/g))
        classes.add(name);
    }
    for (const name of classes) {
      if (dynamic.some((family) => family.test(name))) continue;
      const word = new RegExp(`(?<![\\w-])${name}(?![\\w-])`);
      if (!word.test(components)) unused.push(`${entry}: .${name}`);
    }
  }
  assert.deepEqual(unused, [], "classes no component uses: remove their rules");
});

// A wish's price is the figure that matters on its Spent line: it takes a grey of the theme, which reads at 4.5:1 on
// the page and on a card in the dark and the light theme, and marks its hover (the split) with a dotted underline.
test("the price of what a wish spent reads in the dark and the light theme", () => {
  const wish = fs.readFileSync(path.join(root, "wish.css"), "utf8");
  const start = wish.indexOf(".wish-spent .spent-price {");
  assert.ok(start >= 0, "the price has its rule");
  const rule = wish.slice(start, wish.indexOf("}", start));
  assert.match(rule, /cursor: help/);
  assert.match(rule, /text-decoration: underline dotted/);
  const colour = rule.match(/color: var\(--([\w-]+)\)/)?.[1];
  assert.ok(colour, "the price's colour is a token");
  const theme = fs.readFileSync(path.join(root, "theme.css"), "utf8");
  const tokens = (selector) => {
    const from = theme.indexOf(selector + " {");
    return Object.fromEntries(
      [
        ...theme
          .slice(from, theme.indexOf("\n}", from))
          .matchAll(/--([\w-]+):\s*(#[0-9a-f]{6});/g),
      ].map((m) => [m[1], m[2]]),
    );
  };
  const lum = (hex) => {
    const [r, g, b] = [1, 3, 5].map((i) => {
      const v = parseInt(hex.slice(i, i + 2), 16) / 255;
      return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  for (const [name, values] of [
    ["dark", tokens(":root")],
    ["light", tokens('html[data-theme="light"]')],
  ])
    for (const ground of ["n-11", "n-19"]) {
      const [x, y] = [lum(values[colour]), lum(values[ground])].sort(
        (p, q) => q - p,
      );
      const r = (x + 0.05) / (y + 0.05);
      assert.ok(r >= 4.5, `${name}: ${colour} on ${ground} reads at ${r}:1`);
    }
});

// Wishes and Projects headers in the sidebar share the layout of Flying carpet and Hourglass (.nav-item), and the
// active wish counter (.nav-counter, .nav-counter.full) stays on a transparent ground without a white fill.
test("the sidebar section labels align with nav-item and the wish counter stays without a white background", () => {
  const styles = fs.readFileSync(path.join(root, "styles.css"), "utf8");
  const wish = fs.readFileSync(path.join(root, "wish.css"), "utf8");
  const review = fs.readFileSync(path.join(root, "review.css"), "utf8");
  const rule = (css, selector) => {
    const from = css.indexOf(selector + " {");
    assert.ok(from >= 0, `${selector} has its rule`);
    return css.slice(from, css.indexOf("}", from));
  };
  const label = rule(styles, ".sidebar-section-label");
  const nav = rule(styles, ".nav-item");
  for (const decl of [
    "display: flex;",
    "align-items: center;",
    "gap: 0.625rem;",
    "width: 100%;",
    "padding: 0.6875rem 0.625rem;",
    "color: var(--n-9c);",
    "font-size: var(--font-size-body, 0.75rem);",
  ]) {
    assert.ok(label.includes(decl), `.sidebar-section-label has ${decl}`);
    assert.ok(nav.includes(decl), `.nav-item has ${decl}`);
  }
  const counter = rule(styles, ".nav-counter");
  const full = rule(wish, ".nav-counter.full");
  assert.match(counter, /background:\s*transparent;/);
  assert.match(full, /background:\s*transparent;/);
  assert.doesNotMatch(full, /background:\s*var\(--accent\)/);
  assert.doesNotMatch(review, /html\[data-theme="light"\]\s*\.nav-counter/);
});

// The Tilasms tab lays out a clear drop zone and a single left-aligned column of regular cards, with uniform 4-sided
// borders on rounded elements.
test("the Tilasms tab lays out a single left column of regular cards and a drop zone with uniform borders", () => {
  const css = fs.readFileSync(path.join(root, "tilasms.css"), "utf8");
  const rule = (selector) => {
    const from = css.indexOf(selector + " {");
    assert.ok(from >= 0, `${selector} has its rule`);
    return css.slice(from, css.indexOf("}", from));
  };
  const drop = rule(".tilasm-drop");
  assert.match(drop, /border:\s*1px dashed var\(--line\);/);
  assert.match(drop, /border-radius:\s*0\.5rem;/);
  assert.match(drop, /cursor:\s*pointer;/);
  assert.doesNotMatch(drop, /border-(?:top|right|bottom|left)/);

  const list = rule(".tilasm-list");
  assert.match(list, /display:\s*flex;/);
  assert.match(list, /flex-direction:\s*column;/);
  assert.match(list, /max-width:\s*28\.75rem;/);

  const row = rule(".tilasm-row");
  assert.match(row, /border:\s*1px solid var\(--line\);/);
  assert.match(row, /border-radius:\s*0\.5rem;/);
  assert.doesNotMatch(row, /border-(?:top|right|bottom|left)/);

  const open = rule(".tilasm-row.open");
  assert.match(open, /border-color:/);
  assert.doesNotMatch(open, /border-(?:top|right|bottom|left)/);
});

// W28: the load gauge's stops are round buttons, a dot of one uniform border each; the limits it forecasts are laid
// over the page, hidden until the gauge is hovered or has the keyboard.
test("the load gauge's stops read as round buttons and its limits show on hover only", () => {
  const css = fs.readFileSync(path.join(root, "operating-load.css"), "utf8");
  const rule = (selector) => {
    const from = css.indexOf("\n" + selector + " {");
    assert.ok(from >= 0, `${selector} has its rule`);
    return css.slice(from, css.indexOf("}", from));
  };
  const stop = rule(".operating-load-stop");
  assert.match(stop, /border:\s*none;/);
  assert.match(stop, /border-radius:\s*50%;/);
  assert.match(stop, /cursor:\s*pointer;/);
  const dot = rule(".operating-load-stop::before");
  assert.match(dot, /border:\s*1.5px solid var\(--muted\);/);
  assert.match(dot, /border-radius:\s*50%;/);
  for (const selector of [
    ".operating-load-stop:focus-visible",
    ".operating-load-stop.current::before",
  ])
    rule(selector);
  // Hovered, a stop takes the round of an icon alone (styles.css, the hover's families).
  assert.match(
    hoverFamily("An icon alone:"),
    /\.operating-load-stop\b/,
    "a stop's hover is the round tint",
  );
  assert.match(
    rule(".operating-load-stop.current::before"),
    /background:\s*var\(--notch-color\);/,
  );
  for (const selector of [
    ".operating-load-stop",
    ".operating-load-stop::before",
  ])
    assert.doesNotMatch(rule(selector), /border-(?:top|right|bottom|left)/);

  const forecast = rule(".operating-load-forecast");
  assert.match(forecast, /position:\s*absolute;/);
  assert.match(forecast, /visibility:\s*hidden;/);
  assert.match(forecast, /opacity:\s*0;/);
  const shown = rule(
    ".operating-load:hover .operating-load-forecast,\n.operating-load:has(:focus-visible) .operating-load-forecast",
  );
  assert.match(shown, /visibility:\s*visible;/);
  assert.match(shown, /opacity:\s*1;/);

  // W37: the gauge's fill moves by its width only (no shadow animated: WebKit burns CPU on it) and has no border; the
  // Overclock warning is framed by one uniform border in the theme's danger colour.
  const fill = rule(".operating-load-fill,\n.operating-load-fill-reach");
  assert.match(fill, /transition:\s*width [^;,]*;/);
  assert.doesNotMatch(
    fill,
    /border(?:-(?:top|right|bottom|left))?:|box-shadow/,
  );
  const risk = rule(".operating-load-forecast-risk");
  assert.match(risk, /border:\s*1px solid [^;]*var\(--danger\)/);
  assert.match(risk, /color:\s*var\(--danger\);/);
  assert.doesNotMatch(risk, /border-(?:top|right|bottom|left)/);
  const note = rule(".operating-load-forecast-note");
  assert.match(note, /white-space:\s*normal;/);
});

// Running work is blue, an investigator Enlighten me started is violet as its button (W40): the two never share a
// colour, in either theme, and each tone reads its own token.
test("running workers stay blue, apart from the investigators' violet, in both themes", () => {
  const css = fs.readFileSync(path.join(root, "review.css"), "utf8");
  assert.match(css, /\.tone-running\s*\{\s*--tone:\s*var\(--st-running\);/);
  assert.match(
    css,
    /\.tone-investigating\s*\{\s*--tone:\s*var\(--st-investigating\);/,
  );
  const block = (selector) => {
    const start = css.indexOf(selector + " {");
    return Object.fromEntries(
      [
        ...css
          .slice(start, css.indexOf("}", start))
          .matchAll(/--([\w-]+):\s*(#[0-9a-f]{6});/g),
      ].map((m) => [m[1], m[2]]),
    );
  };
  const hue = (hex) => {
    const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
    const max = Math.max(r, g, b);
    const span = max - Math.min(r, g, b);
    if (!span) return NaN;
    const h =
      max === r
        ? (g - b) / span
        : max === g
          ? 2 + (b - r) / span
          : 4 + (r - g) / span;
    return (h * 60 + 360) % 360;
  };
  for (const [name, theme] of Object.entries({
    dark: block(":root"),
    light: block('html[data-theme="light"]'),
  })) {
    const running = hue(theme["st-running"]);
    const investigating = hue(theme["st-investigating"]);
    assert.ok(
      running >= 200 && running <= 235,
      `${name}: running (${theme["st-running"]}) is blue, its hue ${running.toFixed(0)}°`,
    );
    assert.ok(
      investigating >= 245 && investigating <= 290,
      `${name}: investigating (${theme["st-investigating"]}) is violet, its hue ${investigating.toFixed(0)}°`,
    );
  }
});

// W59: the question of the column that has the focus, the one Enter acts on, shows a gold ring around the whole of it,
// uniform on its rounded edge, still (no box-shadow that moves, which repaints in WebKit); the hover only tints the
// ground of a closed one, and no other state draws the ring.
test("the focused question of the column, and it alone, shows a gold ring distinct from the hover", () => {
  const css = fs
    .readFileSync(path.join(root, "review.css"), "utf8")
    .replace(/\/\*[\s\S]*?\*\//g, "");
  const rule = (selector) => {
    const from = css.indexOf(selector + " {");
    assert.ok(from >= 0, `${selector} has its rule`);
    return css.slice(from, css.indexOf("}", from));
  };
  const ring = rule(".review .question-card.question-row:focus");
  assert.match(ring, /outline:\s*2px solid var\(--sand\);/);
  assert.match(ring, /outline-offset:\s*2px;/);
  assert.doesNotMatch(
    ring,
    /border-(?:top|right|bottom|left)|box-shadow|animation|transition/,
  );
  const row = rule(".review .question-card.question-row");
  assert.match(row, /border:\s*1px solid transparent;/);
  assert.match(row, /border-radius:/);
  assert.doesNotMatch(
    row,
    /border-(?:top|right|bottom|left)|outline|box-shadow/,
  );
  // The hover only tints a closed one, in its radius, with no ring, border or shadow (styles.css's hover families).
  const tint = hoverFamily("An object, a row:");
  assert.match(tint, /\.review \.question-row\.closed\b/);
  assert.match(tint, /background: var\(--hover-tint\);/);
  assert.match(tint, /border-radius: inherit;/);
  assert.doesNotMatch(tint, /outline|border:|box-shadow/);
  // The ring is the focus's alone: no other rule of a question row draws an outline.
  const others = [
    ...css.matchAll(/([^{}]*\.question-row[^{}]*)\{([^}]*)\}/g),
  ].filter(([, selector]) => !/:focus\s*$/.test(selector.trim()));
  for (const [, selector, body] of others)
    assert.doesNotMatch(
      body,
      /outline:\s*(?!none)/,
      `${selector.trim()} draws no ring`,
    );
});

test("the empty states' drawings never loop, and move by transform and opacity alone", () => {
  // Shown wherever a list is empty, often for hours: a loop there would draw frames while nothing changes
  // (e2e/idle-frames.spec.ts), and WebKit repaints a whole SVG for any of its parts (W513).
  const css = fs
    .readFileSync(path.join(root, "empty-art.css"), "utf8")
    .replace(/\/\*[\s\S]*?\*\//g, "");
  assert.doesNotMatch(css, /infinite/);
  for (const [, body] of css.matchAll(/@keyframes[^{]*\{([\s\S]*?\})\s*\}/g))
    for (const [, property] of body.matchAll(/([a-z-]+)\s*:/g))
      assert.ok(["opacity", "transform"].includes(property), property);
});
