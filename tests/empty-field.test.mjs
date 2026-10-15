// The empty panels' data field: each pattern draws a fixed-size, deterministic frame that moves with time, and the
// component renders its still frame hidden from assistive technologies, its line and actions readable.
import assert from "node:assert/strict";
import test from "node:test";

import { bundle } from "./bundle.mjs";

const f = await bundle(
  "empty-field",
  `export { createElement } from "react";
export { renderToStaticMarkup } from "react-dom/server";
export {
  EmptyField,
  FIELD_COLUMNS,
  FIELD_ROWS,
  FIELD_STILL_TIME,
  fieldFrame,
  pulseFrame,
  pulseStateOf,
  PULSE_COLUMNS,
  PULSE_ROWS,
  WishPulse,
} from "@/src/empty-field.tsx";`,
);

const patterns = ["queue", "timeline", "page"];
const flat = (layers) => layers.map((lines) => lines.join("\n")).join("\n\n");

test("each pattern draws three layers of the same size, never two marks in one cell", () => {
  for (const pattern of patterns) {
    for (const time of [0, 1.3, f.FIELD_STILL_TIME, 37.9]) {
      const layers = f.fieldFrame(pattern, time);
      assert.equal(layers.length, 3);
      for (const lines of layers) {
        assert.equal(lines.length, f.FIELD_ROWS);
        for (const line of lines) assert.equal(line.length, f.FIELD_COLUMNS);
      }
      for (let row = 0; row < f.FIELD_ROWS; row++)
        for (let col = 0; col < f.FIELD_COLUMNS; col++)
          assert.ok(
            layers.filter((lines) => lines[row][col] !== " ").length <= 1,
            `${pattern} at ${time}: ${row},${col}`,
          );
    }
  }
});

test("a frame is the same at the same time, moves with time, and each pattern is its own", () => {
  for (const pattern of patterns) {
    assert.equal(
      flat(f.fieldFrame(pattern, 2.5)),
      flat(f.fieldFrame(pattern, 2.5)),
    );
    assert.notEqual(
      flat(f.fieldFrame(pattern, 2.5)),
      flat(f.fieldFrame(pattern, 3.5)),
    );
  }
  const still = patterns.map((pattern) =>
    flat(f.fieldFrame(pattern, f.FIELD_STILL_TIME)),
  );
  assert.equal(new Set(still).size, patterns.length);
});

test("the field renders its still frame aria-hidden, the line and the actions outside it", () => {
  const html = f.renderToStaticMarkup(
    f.createElement(
      f.EmptyField,
      { pattern: "page", message: "No document yet" },
      f.createElement("button", { type: "button" }, "New document"),
    ),
  );
  assert.match(html, /data-empty-field="page"/);
  assert.match(html, /<div class="empty-field-grid" aria-hidden="true">/);
  const [rest, live, hot] = f.fieldFrame("page", f.FIELD_STILL_TIME);
  assert.ok(html.includes(rest.join("\n")));
  assert.ok(html.includes(live.join("\n")));
  assert.ok(html.includes(hot.join("\n").replaceAll("&", "&amp;")));
  assert.match(
    html,
    /<\/div><p class="empty-field-message">No document yet<\/p><div class="empty-field-actions"><button type="button">New document<\/button><\/div>/,
  );
});

test("the pulse reads the wish: waiting first, then workers, then the lead; paused or idle is still", () => {
  const state = (signals) =>
    f.pulseStateOf({
      active: true,
      leadRunning: false,
      workers: 0,
      waiting: 0,
      ...signals,
    });
  assert.equal(state({}), "still");
  assert.equal(state({ leadRunning: true }), "lead");
  assert.equal(state({ leadRunning: true, workers: 2 }), "workers");
  assert.equal(state({ workers: 2, waiting: 1 }), "waiting");
  assert.equal(
    state({ active: false, leadRunning: true, workers: 2 }),
    "still",
  );
  assert.equal(state({ active: false, waiting: 1 }), "waiting");
});

test("a still pulse never moves, the others do; more workers stream denser", () => {
  const ink = (layers) => layers.flat().join("").replaceAll(" ", "").length;
  for (const [state, workers] of [
    ["still", 0],
    ["lead", 0],
    ["workers", 1],
    ["waiting", 0],
  ]) {
    const layers = f.pulseFrame(state, workers, 2);
    assert.equal(layers.length, 3);
    for (const lines of layers) {
      assert.equal(lines.length, f.PULSE_ROWS);
      for (const line of lines) assert.equal(line.length, f.PULSE_COLUMNS);
    }
    const later = f.pulseFrame(state, workers, 3.1);
    if (state === "still") assert.deepEqual(later, layers);
    else assert.notDeepEqual(later, layers);
  }
  assert.ok(
    ink(f.pulseFrame("workers", 4, 2)) > ink(f.pulseFrame("workers", 1, 2)),
  );
  // Waiting blinks on the second: its hot crosshair shows, then rests.
  const hot = (time) => f.pulseFrame("waiting", 0, time)[2].join("").trim();
  assert.ok(hot(2.2).length > 0);
  assert.notEqual(hot(2.2), hot(3.2));
});

test("the pulse renders its state, its frame hidden, its caption readable", () => {
  const html = f.renderToStaticMarkup(
    f.createElement(f.WishPulse, {
      state: "workers",
      workers: 2,
      caption: "2 workers running · lead running",
    }),
  );
  assert.match(html, /data-wish-pulse="workers"/);
  assert.match(html, /<div class="empty-field-grid" aria-hidden="true">/);
  assert.match(
    html,
    /<p class="wish-pulse-caption">2 workers running · lead running<\/p>/,
  );
});
