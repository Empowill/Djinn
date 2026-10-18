// Where the Djinn's eye rests, the path it flies, and its calm gate (src/data/eye.ts). Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const { eyeTarget, eyeLook, flight, calmWait, clampTo, easeInOut } =
  await bundle("eye", `export * from "@/src/data/eye.ts";`);

const all = new Set(["home", "pill", "q:a", "q:b", "tab:main", "tab:tasks"]);
const base = {
  presence: "alive",
  live: true,
  stopped: false,
  blocking: false,
  needs: false,
  perches: all,
};

test("the eye rests by the element that has the focus, and only there", () => {
  // A question has the focus: it reads it, whatever else the column holds.
  assert.deepEqual(eyeTarget({ ...base, focus: "q:b", blocking: true }), {
    perch: "q:b",
    mood: "reading",
    shine: true,
  });
  assert.deepEqual(eyeTarget({ ...base, focus: "pill" }), {
    perch: "pill",
    mood: "listening",
    shine: false,
  });
  assert.deepEqual(eyeTarget({ ...base, focus: "tab:tasks" }), {
    perch: "tab:tasks",
    mood: "watching",
    shine: false,
  });
});

test("with the focus on nothing that has a place, the eye listens on Write to the Djinn, else rests home", () => {
  for (const focus of [undefined, "q:gone"])
    assert.deepEqual(eyeTarget({ ...base, focus }), {
      perch: "pill",
      mood: "listening",
      shine: false,
    });
  assert.deepEqual(
    eyeTarget({ ...base, focus: "q:a", perches: new Set(["home"]) }),
    { perch: "home", mood: "resting", shine: false },
  );
  // While the questions' reading line holds a perch and wishes are read, the eye flies to it.
  assert.deepEqual(
    eyeTarget({
      ...base,
      perches: new Set(["home", "reading"]),
      wishesReading: false,
    }),
    { perch: "reading", mood: "reading", shine: false },
  );
  assert.deepEqual(
    eyeTarget({
      ...base,
      perches: new Set(["home", "reading"]),
      wishesReading: true,
    }),
    { perch: "home", mood: "resting", shine: false },
  );
});

test("the eye wakes before the first stream, sleeps home while stopped, pales while cut", () => {
  const focused = { ...base, focus: "q:a", blocking: true };
  assert.deepEqual(eyeTarget({ ...focused, stopped: true }), {
    perch: "home",
    mood: "asleep",
    shine: false,
  });
  assert.deepEqual(eyeTarget({ ...focused, live: false, everLive: false }), {
    perch: "home",
    mood: "waking",
    shine: false,
  });
  assert.deepEqual(eyeTarget({ ...focused, live: false, everLive: true }), {
    perch: "home",
    mood: "restarting",
    shine: false,
  });
  const waking = eyeTarget({ ...base, live: false, everLive: false });
  assert.deepEqual(eyeLook({ ...base, wishesReading: true }, waking), {
    x: 1,
    y: 1.5,
  });
  const onQ = eyeTarget({ ...base, focus: "q:a" });
  assert.deepEqual(eyeLook({ ...base, runsReading: true }, onQ), {
    x: 2.2,
    y: -0.4,
  });
  assert.equal(eyeLook(base, onQ), undefined);
});

test("discreet keeps the eye home, its flame still shining; none shows no eye", () => {
  const focused = { ...base, focus: "q:a" };
  assert.deepEqual(
    eyeTarget({ ...focused, needs: true, presence: "discreet" }),
    { perch: "home", mood: "resting", shine: true },
  );
  assert.equal(eyeTarget({ ...focused, presence: "none" }).perch, "");
});

test("a flight curves, eases, and lasts with its length", () => {
  const from = { x: 20, y: 30 };
  const to = { x: 620, y: 430 };
  const f = flight(from, to, 0.82, 1);
  assert.equal(f.steps.length, 25);
  assert.deepEqual(f.steps[0], { ...from, scale: 0.82 });
  const last = f.steps.at(-1);
  assert.ok(Math.abs(last.x - to.x) < 1e-9 && Math.abs(last.y - to.y) < 1e-9);
  assert.ok(Math.abs(last.scale - 1) < 1e-9);
  // Its top lifts above the straight line: the middle step is above the chord's middle.
  const mid = f.steps[12];
  assert.ok(mid.y < (from.y + to.y) / 2 - 20, `middle at ${mid.y}`);
  // Eased: the first and the last steps are short, the middle ones long.
  const step = (i) =>
    Math.hypot(
      f.steps[i + 1].x - f.steps[i].x,
      f.steps[i + 1].y - f.steps[i].y,
    );
  assert.ok(step(0) < step(11) / 4 && step(23) < step(11) / 4);
  // Never a jump: no step is a fifth of the way.
  const length = Math.hypot(to.x - from.x, to.y - from.y);
  for (let i = 0; i < 24; i++) assert.ok(step(i) < length / 5);
  // 0.8 s for 720 px: a longer path takes longer, up to 1.1 s.
  assert.equal(f.duration, Math.round(480 + length * 0.45));
  // Down the column, it bows into the margin on its left, a little, and is quick but followable.
  const glide = flight({ x: 16, y: 100 }, { x: 16, y: 220 }, 1, 1);
  assert.ok(glide.control.x < 16 && glide.control.x >= 16 - 7);
  assert.ok(glide.duration >= 520 && glide.duration < f.duration);
  assert.equal(easeInOut(0), 0);
  assert.equal(easeInOut(1), 1);
});

test("the calm gate holds the eye while you type, press, read a dialog, or the window rests", () => {
  const calm = {
    now: 1000,
    typing: false,
    pressed: false,
    dialog: false,
    still: false,
    quietUntil: 0,
    flyingUntil: 0,
  };
  assert.equal(calmWait(calm), 0);
  for (const held of ["typing", "pressed", "dialog", "still"])
    assert.equal(calmWait({ ...calm, [held]: true }), Infinity, held);
  assert.equal(calmWait({ ...calm, quietUntil: 1700 }), 700);
  assert.equal(calmWait({ ...calm, quietUntil: 1200, flyingUntil: 1500 }), 500);
});

test("an eye out of sight leaves from the edge of the view", () => {
  assert.deepEqual(
    clampTo({ x: 30, y: -80 }, { width: 800, height: 600 }, 12),
    { x: 30, y: 12 },
  );
});
