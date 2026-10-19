// The window's touches (UiService.Touch) on the developer's actions. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const { followPresence, TOUCH_EVERY } = await bundle(
  "presence",
  `export { followPresence, TOUCH_EVERY } from "@/src/data/presence.ts";`,
);

// target is a window that keeps its listeners, to fire them by hand.
function target() {
  const listeners = new Map();
  return {
    listeners,
    addEventListener(type, listener) {
      listeners.set(type, listener);
    },
    removeEventListener(type) {
      listeners.delete(type);
    },
    fire(type) {
      listeners.get(type)?.();
    },
  };
}

test("a key, a click or a scroll in the window in front touches, at most every 30 s", () => {
  let now = 1_000_000;
  let focused = true;
  let touches = 0;
  const window = target();
  const stop = followPresence(
    async () => touches++,
    window,
    () => focused,
    () => now,
  );

  window.fire("keydown");
  assert.equal(touches, 1, "the first key touches");
  now += 1000;
  window.fire("pointerdown");
  window.fire("scroll");
  assert.equal(touches, 1, "within 30 s, no other touch");

  now += TOUCH_EVERY;
  focused = false;
  window.fire("wheel");
  assert.equal(touches, 1, "the window in the background never touches");

  focused = true;
  window.fire("wheel");
  assert.equal(touches, 2, "30 s later, in front, a scroll touches");

  stop();
  assert.equal(window.listeners.size, 0, "stopped, it listens no more");
});

test("a touch that fails is no error", async () => {
  const window = target();
  followPresence(
    () => Promise.reject(new Error("djinn is gone")),
    window,
    () => true,
  );
  window.fire("keydown");
  await new Promise((resolve) => setTimeout(resolve, 10));
});
