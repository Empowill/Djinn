"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const ts = require("typescript");

function loadTypeScriptModule(filename) {
  const source = fs.readFileSync(filename, "utf8");
  const output = ts.transpileModule(source, {
    compilerOptions: {
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.CommonJS,
    },
    fileName: filename,
  }).outputText;
  const module = { exports: {} };
  new Function("module", "exports", output)(module, module.exports);
  return module.exports;
}

const layoutModule = loadTypeScriptModule(
  path.resolve(__dirname, "../src/wish-smoke-layout.ts"),
);
const {
  SMOKE_BASE_FONT_SIZE,
  SMOKE_BASE_LINE_HEIGHT,
  SMOKE_MAX_CANVAS_DIMENSION,
  SMOKE_MAX_CANVAS_PIXELS,
  SMOKE_MAX_CELLS,
  SMOKE_MAX_COLUMNS,
  SMOKE_MAX_ROWS,
  computeWishSmokeLayout,
} = layoutModule;

function assertLayoutInvariants(layout, width, height, dpr, baseCharWidth = 6) {
  assert.ok(layout.fontSize >= SMOKE_BASE_FONT_SIZE);
  assert.equal(layout.fontSize * 2, Math.round(layout.fontSize * 2));
  assert.ok(layout.charWidth > 0);
  assert.ok(layout.lineHeight > 0);
  assert.ok(layout.gridWidth >= 1 && layout.gridWidth <= SMOKE_MAX_COLUMNS);
  assert.ok(layout.gridHeight >= 1 && layout.gridHeight <= SMOKE_MAX_ROWS);
  assert.ok(layout.gridWidth * layout.gridHeight <= SMOKE_MAX_CELLS);
  assert.equal(layout.canvasWidth, Math.round(layout.canvasWidth));
  assert.equal(layout.canvasHeight, Math.round(layout.canvasHeight));
  assert.ok(
    layout.canvasWidth >= 1 && layout.canvasWidth <= SMOKE_MAX_CANVAS_DIMENSION,
  );
  assert.ok(
    layout.canvasHeight >= 1 &&
      layout.canvasHeight <= SMOKE_MAX_CANVAS_DIMENSION,
  );
  assert.ok(
    layout.canvasWidth * layout.canvasHeight <= SMOKE_MAX_CANVAS_PIXELS,
  );
  assert.ok(layout.backgroundPixelRatio > 0);
  assert.ok(layout.backgroundPixelRatio <= dpr);
  assert.ok(
    Math.abs(layout.canvasWidth - width * layout.backgroundPixelRatio) <= 1,
  );
  assert.ok(
    Math.abs(layout.canvasHeight - height * layout.backgroundPixelRatio) <= 1,
  );
  assert.equal(layout.coverage.length, 2);
  assert.ok(layout.coverage[0] >= 1);
  assert.ok(layout.coverage[1] >= 1);
  assert.ok(
    Math.abs(layout.charWidth - (baseCharWidth * layout.fontSize) / 10) < 1e-9,
  );
  assert.ok(
    Math.abs(
      layout.lineHeight - (SMOKE_BASE_LINE_HEIGHT * layout.fontSize) / 10,
    ) < 1e-9,
  );
  assert.ok(width > 0 && height > 0);
}

test("keeps the 10px reference on a normal surface when the budgets fit", () => {
  const layout = computeWishSmokeLayout(1280, 800, 6, 1);
  assert.equal(layout.fontSize, 10);
  assert.equal(layout.charWidth, 6);
  assert.equal(layout.lineHeight, SMOKE_BASE_LINE_HEIGHT);
  assert.deepEqual([layout.gridWidth, layout.gridHeight], [214, 71]);
  assertLayoutInvariants(layout, 1280, 800, 1);
});

test("larger surfaces quantize upward smoothly and remain within all HTML budgets", () => {
  const surfaces = [
    [320, 180],
    [1280, 800],
    [1920, 1080],
    [3840, 2160],
    [6016, 3384],
  ];
  let previousFont = 0;
  for (const [width, height] of surfaces) {
    const layout = computeWishSmokeLayout(width, height, 6, 1);
    assertLayoutInvariants(layout, width, height, 1);
    assert.ok(layout.fontSize >= previousFont);
    previousFont = layout.fontSize;
  }
  assert.equal(computeWishSmokeLayout(6016, 3384, 6, 1).fontSize, 42);
});

test("DPR changes the background backing only, never the CSS grid", () => {
  const layouts = [1, 2, 3].map((dpr) =>
    computeWishSmokeLayout(3840, 2160, 6, dpr),
  );
  const htmlLayout = (layout) => [
    layout.fontSize,
    layout.charWidth,
    layout.lineHeight,
    layout.gridWidth,
    layout.gridHeight,
    layout.coverage,
  ];
  assert.deepEqual(htmlLayout(layouts[1]), htmlLayout(layouts[0]));
  assert.deepEqual(htmlLayout(layouts[2]), htmlLayout(layouts[0]));
  for (const [dpr, layout] of [1, 2, 3].map((dpr) => [
    dpr,
    computeWishSmokeLayout(3840, 2160, 6, dpr),
  ])) {
    assertLayoutInvariants(layout, 3840, 2160, dpr);
  }
});

test("ultrawide, tall, 4K, 6K, and tiny surfaces all stay bounded", () => {
  const cases = [
    [3840, 2160, 1],
    [6016, 3384, 2],
    [5120, 1440, 3],
    [1000, 5000, 1],
    [1, 1, 3],
    [320, 180, 2],
  ];
  for (const [width, height, dpr] of cases) {
    const layout = computeWishSmokeLayout(width, height, 6, dpr);
    assertLayoutInvariants(layout, width, height, dpr);
  }
});

test("canvas backing keeps one CSS aspect ratio on ultrawide and portrait surfaces", () => {
  const ultrawide = computeWishSmokeLayout(5120, 1440, 6, 3);
  assert.deepEqual(
    [ultrawide.canvasWidth, ultrawide.canvasHeight],
    [2048, 576],
  );
  assert.equal(ultrawide.canvasWidth * ultrawide.canvasHeight, 1_179_648);
  assert.ok(Math.abs(ultrawide.backgroundPixelRatio - 0.4) < 1e-12);
  assertLayoutInvariants(ultrawide, 5120, 1440, 3);

  const portrait = computeWishSmokeLayout(1000, 5000, 6, 3);
  assert.deepEqual([portrait.canvasWidth, portrait.canvasHeight], [410, 2048]);
  assert.equal(portrait.canvasWidth * portrait.canvasHeight, 839_680);
  assert.ok(Math.abs(portrait.backgroundPixelRatio - 0.4096) < 1e-12);
  assertLayoutInvariants(portrait, 1000, 5000, 3);
});

test("same inputs produce the same layout across repeated resize measurements", () => {
  const first = computeWishSmokeLayout(6016, 3384, 6.125, 2);
  for (let index = 0; index < 20; index++)
    assert.deepEqual(computeWishSmokeLayout(6016, 3384, 6.125, 2), first);
});

test("random CSS surfaces preserve the layout invariants", () => {
  let state = 0x6d2b79f5;
  for (let index = 0; index < 200; index++) {
    state = Math.imul(state ^ (state >>> 15), 2246822519);
    const width = 1 + ((state >>> 0) % 8000);
    state = Math.imul(state ^ (state >>> 13), 3266489917);
    const height = 1 + ((state >>> 0) % 6000);
    state = Math.imul(state ^ (state >>> 16), 668265263);
    const dpr = 1 + ((state >>> 0) % 3);
    const layout = computeWishSmokeLayout(width, height, 5.75, dpr);
    assertLayoutInvariants(layout, width, height, dpr, 5.75);
  }
});

test("rejects invalid dimensions and measurements", () => {
  for (const args of [
    [0, 100, 6, 1],
    [100, 0, 6, 1],
    [100, 100, 0, 1],
    [100, 100, 6, 0],
    [Number.NaN, 100, 6, 1],
  ]) {
    assert.throws(() => computeWishSmokeLayout(...args), RangeError);
  }
});
