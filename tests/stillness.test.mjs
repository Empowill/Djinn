// What keeps the window's data and counters live (keepLive, src/stillness.ts): another window in front stops none of
// them (W290). Run with `go tool task test-ui`.
import test, { mock } from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const { keepLive, UNSEEN_EVERY } = await bundle(
  "stillness",
  `export { keepLive, UNSEEN_EVERY } from "@/src/stillness.ts";`,
);

// page is a document that keeps its listeners, to show, hide, focus or blur it by hand.
function page() {
  const listeners = new Map();
  return {
    hidden: false,
    focused: true,
    hasFocus() {
      return this.focused;
    },
    addEventListener(type, listener) {
      listeners.set(type, listener);
    },
    removeEventListener(type) {
      listeners.delete(type);
    },
    fire(type) {
      listeners.get(type)?.();
    },
    listeners,
  };
}

test("unfocused, the counters tick on; hidden, seldom; shown, at once", () => {
  mock.timers.enable({ apis: ["setInterval"] });
  try {
    const doc = page();
    let ticks = 0;
    const stop = keepLive(() => ticks++, 1_000, doc);

    mock.timers.tick(3_000);
    assert.equal(ticks, 3, "in front, every second");

    doc.focused = false;
    doc.fire("blur");
    mock.timers.tick(3_000);
    assert.equal(ticks, 6, "another window in front: every second still");

    doc.hidden = true;
    doc.fire("visibilitychange");
    mock.timers.tick(UNSEEN_EVERY - 1);
    assert.equal(ticks, 6, "hidden, no tick within a minute");
    mock.timers.tick(1);
    assert.equal(ticks, 7, "hidden, one tick a minute");

    mock.timers.tick(5_000);
    doc.hidden = false;
    doc.fire("visibilitychange");
    assert.equal(ticks, 8, "shown again, it catches up at once");
    mock.timers.tick(1_000);
    assert.equal(ticks, 9, "and ticks every second again");

    stop();
    mock.timers.tick(10_000);
    assert.equal(ticks, 9, "stopped, it ticks no more");
    assert.equal(doc.listeners.size, 0, "stopped, it listens no more");
  } finally {
    mock.timers.reset();
  }
});
