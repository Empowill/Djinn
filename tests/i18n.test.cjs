"use strict";

// The interface texts live in locales/ (see src/i18n.ts). The Go test of locales/ checks the
// same rules plus the keys the Go code uses, and that no key is left unused.
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const locales = path.resolve(__dirname, "../locales");
const src = path.resolve(__dirname, "../src");
const catalogs = Object.fromEntries(
  fs
    .readdirSync(locales)
    .filter((name) => name.endsWith(".json"))
    .map((name) => [
      path.basename(name, ".json"),
      JSON.parse(fs.readFileSync(path.join(locales, name), "utf8")),
    ]),
);
const source = catalogs.en;

test("every language translates every key of en.json, and nothing else", () => {
  for (const [language, texts] of Object.entries(catalogs)) {
    const missing = Object.keys(source).filter((key) => !(key in texts));
    const extra = Object.keys(texts).filter((key) => !(key in source));
    assert.deepEqual(missing, [], `locales/${language}.json lacks keys`);
    assert.deepEqual(extra, [], `locales/${language}.json has keys en.json lacks`);
  }
});

test("the interface only uses keys of en.json", () => {
  const uses = /\bt\(\s*["'`]([^"'`$]+)["'`]/g;
  const unknown = [];
  for (const name of fs.readdirSync(src, { recursive: true })) {
    if (!/\.tsx?$/.test(name)) continue;
    const code = fs.readFileSync(path.join(src, name), "utf8");
    for (const [, key] of code.matchAll(uses)) {
      if (!(key in source) && !(`${key}.other` in source)) unknown.push(`${name}: ${key}`);
    }
  }
  assert.deepEqual(unknown, []);
});
