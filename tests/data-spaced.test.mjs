// The spaced read of the History tab (src/data/spaced.ts). Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const { spaced } = await bundle(
  "spaced",
  `export { spaced } from "@/src/data/spaced.ts";`,
);

// reads counts the reads spaced starts, each ended by the test.
function reads() {
  const going = [];
  return {
    going,
    read: () => new Promise((resolve) => going.push(resolve)),
  };
}

const flush = () => new Promise((resolve) => setImmediate(resolve));

// W149: a busy wish changed several times a second; each change aborted the read going and started one more, which
// never ended. Changes during a read wait for it, then make one read, spaced from the last.
test("changes during a read make one read after it, spaced", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout", "Date"] });
  const r = reads();
  const s = spaced(r.read, 5000, () => Date.now());
  assert.equal(r.going.length, 1, "the first read starts at once");
  for (let i = 0; i < 50; i++) {
    s.changed();
    t.mock.timers.tick(100);
  }
  assert.equal(r.going.length, 1, "no read while one goes");
  r.going[0]();
  await flush();
  t.mock.timers.tick(0);
  assert.equal(
    r.going.length,
    2,
    "past the spacing, one read for the 50 changes",
  );
  s.changed();
  t.mock.timers.tick(100);
  r.going[1]();
  await flush();
  t.mock.timers.tick(4800);
  assert.equal(r.going.length, 2, "the next read waits the spacing");
  t.mock.timers.tick(100);
  assert.equal(r.going.length, 3);
  s.stop();
});

test("no change, no read; a failed read is read again on a change; stop drops the next", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout", "Date"] });
  let calls = 0;
  const s = spaced(
    () => {
      calls++;
      return Promise.reject(new Error("down"));
    },
    1000,
    () => Date.now(),
  );
  await flush();
  t.mock.timers.tick(10_000);
  assert.equal(calls, 1);
  s.changed();
  t.mock.timers.tick(0);
  assert.equal(calls, 2);
  await flush();
  s.changed();
  s.stop();
  t.mock.timers.tick(10_000);
  assert.equal(calls, 2);
});
