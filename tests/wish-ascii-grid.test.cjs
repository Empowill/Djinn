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
  // The kernel is dependency-free. Evaluating the transpiled module keeps
  // this focused test runnable with the repository's existing Node harness.
  new Function("module", "exports", output)(module, module.exports);
  return module.exports;
}

const kernelModule = loadTypeScriptModule(
  path.resolve(__dirname, "../src/wish-ascii-grid.ts"),
);
const {
  ASCII_CHARS,
  ASCII_LAYER_COUNT,
  ASCII_REFERENCE,
  ASCII_TONES,
  createAsciiGrid,
} = kernelModule;

function originalAscii(pixels, width, height, trail, now) {
  const rows = Array.from({ length: ASCII_LAYER_COUNT }, () =>
    Array.from({ length: height }, () => ""),
  );
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const level = pixels[((height - 1 - y) * width + x) * 4] / 255;
      const noise = (((x * 37 + y * 91) % 17) / 17) * 0.065 - 0.0325;
      let index = Math.max(
        0,
        Math.min(
          ASCII_CHARS.length - 1,
          Math.floor((Math.pow(level, 0.52) + noise) * ASCII_CHARS.length),
        ),
      );
      let fade = Math.max(0, Math.min(1, (level - 0.003) / 0.027));
      fade = fade * fade * (3 - 2 * fade);
      const brightness = (0.22 + Math.pow(level, 0.5) * 0.78) * fade;
      let lower = 0;
      while (
        lower < ASCII_TONES.length - 1 &&
        ASCII_TONES[lower + 1] < brightness
      )
        lower++;
      const upper = Math.min(lower + 1, ASCII_TONES.length - 1);
      let cellHash = Math.imul(x + 1, 374761393) ^ Math.imul(y + 1, 668265263);
      cellHash = Math.imul(cellHash ^ (cellHash >>> 13), 1274126177);
      const blend =
        upper === lower
          ? 0
          : Math.max(
              0,
              Math.min(
                1,
                (brightness - ASCII_TONES[lower]) /
                  (ASCII_TONES[upper] - ASCII_TONES[lower]),
              ),
            );
      let tone = (cellHash >>> 0) / 4294967296 < blend ? upper : lower;
      const influence = trail ? trail[y * width + x] : 0;
      if (index && ((x * 73 + y * 37) % 101) / 101 < influence * 0.95) {
        const shift = (Math.floor(now / 160) + x * 17 + y * 13) % 2 ? 2 : -2;
        index = Math.max(1, Math.min(ASCII_CHARS.length - 1, index + shift));
        tone = ASCII_TONES.length + (influence > 0.5 ? 0 : 1);
      }
      for (let layer = 0; layer < ASCII_LAYER_COUNT; layer++)
        rows[layer][y] +=
          layer === tone && brightness > 0.035 ? ASCII_CHARS[index] : " ";
    }
  }
  return rows.map((rowsForLayer) => rowsForLayer.join("\n"));
}

function randomBytes(width, height, seed = 0x12345678) {
  const pixels = new Uint8Array(width * height * 4);
  let value = seed >>> 0;
  for (let index = 0; index < pixels.length; index++) {
    value = Math.imul(value ^ (value >>> 15), 2246822519);
    value = (value + 3266489917) >>> 0;
    pixels[index] = value >>> 24;
  }
  return pixels;
}

function randomTrail(width, height, seed = 0x87654321) {
  const trail = new Float32Array(width * height);
  let value = seed >>> 0;
  for (let index = 0; index < trail.length; index++) {
    value = Math.imul(value ^ (value >>> 13), 1274126177);
    trail[index] = ((value >>> 8) % 1001) / 1000;
  }
  return trail;
}

function applyPatches(strings, patches, width) {
  const rows = strings.map((layer) => layer.split("\n"));
  for (const patch of patches) {
    assert.equal(patch.text.length, patch.end - patch.start);
    const row = rows[patch.layer][patch.row];
    assert.equal(row.length, width);
    rows[patch.layer][patch.row] =
      row.slice(0, patch.start) + patch.text + row.slice(patch.end);
  }
  return rows.map((layer) => layer.join("\n"));
}

test("matches the independent reference loop for random fields and bottom-up pixels", () => {
  const width = 17;
  const height = 11;
  const pixels = randomBytes(width, height);
  const trail = randomTrail(width, height);
  const now = 1297.25;
  const grid = createAsciiGrid(width, height);
  const frame = grid.render(pixels, trail, now);

  assert.equal(frame.full, true);
  assert.equal(frame.sequence, 1);
  assert.equal(frame.baseSequence, 0);
  assert.deepEqual(
    frame.fullStrings,
    originalAscii(pixels, width, height, trail, now),
  );
  assert.deepEqual(grid.fullStrings(), frame.fullStrings);
});

test("zero and full levels preserve blank output, tone selection, and visibility", () => {
  const width = 9;
  const height = 4;
  const zeros = new Uint8Array(width * height * 4);
  const full = new Uint8Array(width * height * 4).fill(255);
  const trail = new Float32Array(width * height);
  const grid = createAsciiGrid(width, height);

  assert.deepEqual(
    grid.render(zeros, trail, 0).fullStrings,
    originalAscii(zeros, width, height, trail, 0),
  );
  assert.deepEqual(
    grid.render(full, trail, 0, { forceFull: true }).fullStrings,
    originalAscii(full, width, height, trail, 0),
  );
});

test("hover glyphs and tone layers match at both trail thresholds", () => {
  const width = 13;
  const height = 7;
  const pixels = randomBytes(width, height, 0x2ab31f09);
  const trail = new Float32Array(width * height).fill(1);
  const grid = createAsciiGrid(width, height);

  for (const now of [159.9, 160, 319.9, 320]) {
    const frame = grid.render(pixels, trail, now, { forceFull: true });
    assert.deepEqual(
      frame.fullStrings,
      originalAscii(pixels, width, height, trail, now),
      `hover output at ${now}ms`,
    );
  }

  const priorStrings = grid.fullStrings();
  trail.fill(0.5);
  const frame = grid.render(pixels, trail, 321);
  assert.deepEqual(
    applyPatches(priorStrings, frame.patches, width),
    originalAscii(pixels, width, height, trail, 321),
  );
});

test("unchanged fields emit no patches, changed cells emit row chunks, and gaps recover with a full frame", () => {
  const width = 12;
  const height = 5;
  const pixels = randomBytes(width, height, 0xdeadbeef);
  const trail = new Float32Array(width * height);
  const grid = createAsciiGrid(width, height, { chunkWidth: 4 });
  const first = grid.render(pixels, trail, 0);
  const same = grid.render(pixels, trail, 0);
  assert.equal(same.changed, false);
  assert.deepEqual(same.patches, []);
  assert.equal(same.sequence, first.sequence);

  // A single bottom-up pixel changes one top-down cell. Patches reconstruct
  // the same strings as the independent reference without replacing layers.
  pixels.fill(0);
  const blank = grid.render(pixels, trail, 0, { forceFull: true });
  pixels[(height - 1 - 2) * width * 4 + 7 * 4] = 255;
  const changed = grid.render(pixels, trail, 0);
  assert.equal(changed.full, false);
  assert.equal(changed.baseSequence, blank.sequence);
  assert.ok(changed.patches.length > 0);
  assert.deepEqual(
    applyPatches(blank.fullStrings, changed.patches, width),
    originalAscii(pixels, width, height, trail, 0),
  );

  const recovered = grid.render(pixels, trail, 0, {
    appliedSequence: blank.sequence,
  });
  assert.equal(recovered.full, true);
  assert.equal(recovered.baseSequence, blank.sequence);
  assert.deepEqual(
    recovered.fullStrings,
    originalAscii(pixels, width, height, trail, 0),
  );
});

test("resize invalidates the prior frame and keeps the reference surface constants", () => {
  assert.deepEqual(ASCII_REFERENCE, {
    zoom: 10,
    fontSize: 10,
    lineHeight: 11.4,
  });
  const grid = createAsciiGrid(3, 2);
  const pixels = new Uint8Array(3 * 2 * 4).fill(200);
  const trail = new Float32Array(3 * 2);
  grid.render(pixels, trail, 0);
  grid.resize(4, 3);
  assert.equal(grid.width, 4);
  assert.equal(grid.height, 3);
  const resizedPixels = new Uint8Array(4 * 3 * 4).fill(100);
  const resizedTrail = new Float32Array(4 * 3);
  const frame = grid.render(resizedPixels, resizedTrail, 0);
  assert.equal(frame.full, true);
  assert.equal(frame.baseSequence, 0);
  assert.deepEqual(
    frame.fullStrings,
    originalAscii(resizedPixels, 4, 3, resizedTrail, 0),
  );
});
