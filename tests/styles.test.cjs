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
