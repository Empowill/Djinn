// Where the Djinn's eye rests, the path it flies, and its calm gate (src/data/eye.ts). Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const {
  LEAD_SAY_WINDOW_MS,
  advanceLeadSay,
  bubblePlace,
  calmWait,
  clampTo,
  easeInOut,
  eyeLook,
  eyeTarget,
  flight,
  isLeadSayBlock,
  leadSayQueue,
  readLastSent,
  readSeenIds,
  writeLastSent,
  writeSeenIds,
} = await bundle("eye", `export * from "@/src/data/eye.ts";`);

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
  // While the lead speaks in its bubble on Write to the Djinn, the eye comes to its socket.
  assert.deepEqual(
    eyeTarget({ ...base, focus: "q:b", speaking: true, blocking: true }),
    {
      perch: "pill",
      mood: "listening",
      shine: true,
    },
  );
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

test("unseen reply and decided blocks queue in order, click advances and persists seen ids", () => {
  const now = 1_700_000_000_000;
  const ts = (ms) => ({
    seconds: BigInt(Math.floor(ms / 1000)),
    nanos: (ms % 1000) * 1_000_000,
  });
  const blocks = [
    {
      id: "old",
      kind: "reply",
      content: "Too old",
      createTime: ts(now - LEAD_SAY_WINDOW_MS - 1000),
      position: 1,
    },
    {
      id: "worker",
      kind: "reply",
      taskId: "t1",
      content: "From worker",
      createTime: ts(now - 5000),
      position: 2,
    },
    {
      id: "note",
      kind: "decision",
      content: "Regular decision block",
      createTime: ts(now - 4000),
      position: 3,
    },
    {
      id: "b2",
      kind: "decided",
      content: "Picked the uniform border.",
      createTime: ts(now - 2000),
      position: 5,
    },
    {
      id: "b1",
      kind: "reply",
      content: "Two workers run on the wish.",
      createTime: ts(now - 3000),
      position: 4,
    },
  ];
  assert.equal(isLeadSayBlock(blocks[1]), false);
  assert.equal(isLeadSayBlock(blocks[2]), false);
  assert.equal(isLeadSayBlock(blocks[3]), true);
  assert.equal(isLeadSayBlock(blocks[4]), true);

  const mem = new Map();
  const store = {
    getItem: (k) => mem.get(k) ?? null,
    setItem: (k, v) => mem.set(k, v),
  };

  let seen = readSeenIds(store);
  let q = leadSayQueue(blocks, seen, now);
  assert.deepEqual(
    q.map((b) => b.id),
    ["b1", "b2"],
  );

  // First click marks b1 seen and advances to b2.
  const step1 = advanceLeadSay(q, seen);
  writeSeenIds(step1.seen, store);
  assert.equal(step1.next?.id, "b2");
  assert.deepEqual(
    step1.remaining.map((b) => b.id),
    ["b2"],
  );

  // Reloading within 24h brings back only the unseen b2.
  seen = readSeenIds(store);
  q = leadSayQueue(blocks, seen, now + 60_000);
  assert.deepEqual(
    q.map((b) => b.id),
    ["b2"],
  );

  // Second click marks b2 seen and empties the queue.
  const step2 = advanceLeadSay(q, seen);
  writeSeenIds(step2.seen, store);
  assert.equal(step2.next, undefined);
  assert.equal(
    leadSayQueue(blocks, readSeenIds(store), now + 60_000).length,
    0,
  );

  // Last-sent message is trimmed and stored per wish, and bubblePlace chooses side vs above.
  writeLastSent("w1", "  how many   workers\nrun? ", store);
  assert.equal(readLastSent("w1", store), "how many workers run?");
  assert.equal(bubblePlace(false, 420), "side");
  assert.equal(bubblePlace(false, 280), "above");
  assert.equal(bubblePlace(true, 600), "above");
});

test("the eye follows a driven lead's batch: thinking, done, failed, while keeping the reader first", () => {
  // Listening when no batch runs.
  assert.deepEqual(eyeTarget(base), {
    perch: "pill",
    mood: "listening",
    shine: false,
  });

  // Thinking: on Write to the Djinn, on the seal when the pill is absent, and in discreet mode.
  const thinking = eyeTarget({ ...base, batch: "thinking" });
  assert.deepEqual(thinking, {
    perch: "pill",
    mood: "thinking",
    shine: false,
  });
  assert.equal(eyeLook({ ...base, wishesReading: true }, thinking), undefined);
  assert.deepEqual(
    eyeTarget({
      ...base,
      batch: "thinking",
      perches: new Set(["home"]),
    }),
    { perch: "home", mood: "thinking", shine: false },
  );
  assert.deepEqual(
    eyeTarget({
      ...base,
      batch: "thinking",
      presence: "discreet",
      needs: true,
    }),
    { perch: "home", mood: "thinking", shine: true },
  );

  // On a question the developer reads, the reader comes first: the eye keeps reading with them.
  assert.deepEqual(eyeTarget({ ...base, focus: "q:a", batch: "thinking" }), {
    perch: "q:a",
    mood: "reading",
    shine: false,
  });

  // Done: back to listening (or resting in discreet mode) with end: "done" so the flame burst plays once.
  assert.deepEqual(eyeTarget({ ...base, batch: "done" }), {
    perch: "pill",
    mood: "listening",
    shine: false,
    end: "done",
  });
  assert.deepEqual(
    eyeTarget({ ...base, batch: "done", presence: "discreet" }),
    {
      perch: "home",
      mood: "resting",
      shine: false,
      end: "done",
    },
  );

  // Failed: red mood on the porthole and the seal, gaze lowered by CSS (eyeLook undefined).
  const failed = eyeTarget({ ...base, batch: "failed" });
  assert.deepEqual(failed, {
    perch: "pill",
    mood: "failed",
    shine: false,
  });
  assert.equal(eyeLook({ ...base, runsReading: true }, failed), undefined);
  assert.deepEqual(
    eyeTarget({ ...base, batch: "failed", presence: "discreet" }),
    { perch: "home", mood: "failed", shine: false },
  );
});
