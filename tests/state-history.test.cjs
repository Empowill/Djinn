"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const fsp = fs.promises;
const os = require("node:os");
const path = require("node:path");
const { createRequire, Module } = require("node:module");
const { EventEmitter } = require("node:events");
const runtime = require("../electron/runtime.cjs");
const { taskFixture } = require("./workflow-fixture.cjs");
const {
  ARCHIVE_MARKER,
  MAX_TOOL_DETAIL_BUDGET,
  MAX_TOOL_EVENT_DETAIL_LENGTH,
  archiveDetail,
  compactStateHistory,
} = require("../electron/state-history.cjs");

function deepFreeze(value) {
  if (!value || typeof value !== "object" || Object.isFrozen(value)) return value;
  Object.freeze(value);
  for (const child of Object.values(value)) deepFreeze(child);
  return value;
}

function validTask(project, events, overrides = {}) {
  const steps = runtime.workflow.createDefaultSteps("history-task");
  return {
    ...taskFixture({ project, events }),
    steps,
    activeStepId: steps[0].id,
    selectedStepId: steps[0].id,
    ...overrides,
  };
}

function event(id, type, detail, extra = {}) {
  const ordinal = Number(id.replace(/\D/g, "")) || 0;
  return {
    id,
    title: `${type}-${id}`,
    time: new Date(Date.parse("2026-10-07T00:00:00.000Z") + ordinal * 1_000).toISOString(),
    type,
    detail,
    ...extra,
  };
}

function persistenceHarness(directory) {
  const root = directory || fs.mkdtempSync(path.join(os.tmpdir(), "djinn-state-history-"));
  const appEvents = new Map();
  const app = {
    on(name, callback) { appEvents.set(name, callback); },
    whenReady: () => new Promise(() => {}),
    getPath: () => root,
    setPath() {},
    quit() {},
  };
  class FakeWindow extends EventEmitter {
    constructor() {
      super();
      this.webContents = new EventEmitter();
      this.webContents.mainFrame = { routingId: 1 };
      this.webContents.setWindowOpenHandler = () => {};
    }
    loadURL() { return Promise.resolve(); }
    loadFile() { return Promise.resolve(); }
    isDestroyed() { return false; }
  }
  const electron = {
    app,
    BrowserWindow: FakeWindow,
    ipcMain: { handle() {} },
    shell: { openExternal: async () => undefined },
    dialog: {},
    Notification: {},
    protocol: {
      registerSchemesAsPrivileged() {},
      handle() {},
    },
  };
  const fakeProcess = Object.create(process);
  const realRequire = createRequire(path.resolve(__dirname, "../electron/main.cjs"));
  const source =
    fs
      .readFileSync(path.resolve(__dirname, "../electron/main.cjs"), "utf8")
      .replace(
        "function sendEvent(event) {",
        "function sendEvent(event) { testEvents.push(event); return;",
      ) +
    "\nmodule.exports.testSaveState = saveState; module.exports.testLoadState = loadState;";
  const compiled = new Module(path.resolve(__dirname, "../electron/main.cjs"));
  compiled.testBindings = { testEvents: [] };
  compiled.require = (name) =>
    name === "electron"
      ? electron
      : name === "node:child_process"
        ? { spawn: () => { throw new Error("provider launch forbidden"); }, spawnSync: () => ({ status: 1 }) }
        : realRequire(name);
  compiled._compile(
    "const { testEvents } = module.testBindings;\n" + source,
    path.resolve(__dirname, "../electron/main.cjs"),
  );
  return {
    root,
    api: compiled.exports,
    async dispose(remove = true) {
      await compiled.exports.flushMissionJournal?.();
      if (remove) fs.rmSync(root, { recursive: true, force: true });
    },
  };
}

test("history compaction is pure, stable, metadata preserving, and leaves human tool events intact", () => {
  const original = {
    version: 2,
    tasks: [
      validTask("", [
        event("e1", "note", "keep non-tool output", { runId: "run-1", stepId: "step-1" }),
        event("e2", "tool", "human full output", { actor: "human", runId: "run-1" }),
        event("e3", "tool", "x".repeat(4_500), { runId: "run-2", stepId: "step-2" }),
      ]),
      validTask("", [
        event("e4", "tool", "y".repeat(4_500), { runId: "run-3", stepId: "step-3" }),
      ]),
    ],
  };
  const before = JSON.stringify(original);
  deepFreeze(original);
  const unchanged = { version: 2, tasks: [validTask("", [event("small", "tool", "small")])] };
  assert.strictEqual(compactStateHistory(unchanged), unchanged);
  const compacted = compactStateHistory(original, { detailBudget: 4_200 });
  assert.equal(JSON.stringify(original), before);
  assert.equal(compacted.tasks[0].events[0].detail, "keep non-tool output");
  assert.equal(compacted.tasks[0].events[1].detail, "human full output");
  assert.notStrictEqual(compacted, original);
  assert.notStrictEqual(compacted.tasks[0], original.tasks[0]);
  assert.strictEqual(compacted.tasks[0].artifacts, original.tasks[0].artifacts);
  assert.strictEqual(compacted.tasks[0].events[0], original.tasks[0].events[0]);
  assert.ok(compacted.tasks[0].events[2].detail.includes(ARCHIVE_MARKER));
  assert.ok(compacted.tasks[0].events[2].detail.length <= MAX_TOOL_EVENT_DETAIL_LENGTH);
  assert.ok(compacted.tasks[1].events[0].detail.includes(ARCHIVE_MARKER));
  for (const task of compacted.tasks)
    for (const entry of task.events)
      assert.deepEqual(
        { id: entry.id, title: entry.title, time: entry.time, runId: entry.runId, stepId: entry.stepId },
        { id: original.tasks.flatMap((t) => t.events).find((e) => e.id === entry.id).id,
          title: original.tasks.flatMap((t) => t.events).find((e) => e.id === entry.id).title,
          time: original.tasks.flatMap((t) => t.events).find((e) => e.id === entry.id).time,
          runId: original.tasks.flatMap((t) => t.events).find((e) => e.id === entry.id).runId,
          stepId: original.tasks.flatMap((t) => t.events).find((e) => e.id === entry.id).stepId },
      );
});

test("oversized persisted tool history compacts, reloads, and preserves exact pre-compaction bytes", async () => {
  const h = persistenceHarness();
  try {
    const events = Array.from({ length: 3_000 }, (_, index) =>
      event(`tool-${index}`, "tool", "z".repeat(5_000), {
        runId: `run-${index}`,
        stepId: "history-task:step:1",
      }),
    );
    const rawState = { version: 2, tasks: [validTask(h.root, events)] };
    const originalBytes = `${JSON.stringify(rawState, null, 2)}\n`;
    assert.ok(Buffer.byteLength(originalBytes) > runtime.MAX_SESSION_LENGTH);
    await fsp.writeFile(path.join(h.root, "state.json"), originalBytes);
    const loaded = await h.api.testLoadState();
    assert.equal(loaded.tasks[0].events.length, events.length);
    await h.api.testSaveState(rawState);
    const compactBytes = await fsp.readFile(path.join(h.root, "state.json"));
    const backupBytes = await fsp.readFile(
      path.join(h.root, "state.json.before-history-compaction.backup"),
    );
    assert.deepEqual(backupBytes, Buffer.from(originalBytes));
    assert.ok(compactBytes.length <= runtime.MAX_SESSION_LENGTH);
    assert.ok(compactBytes.length < originalBytes.length);
    const saved = JSON.parse(compactBytes);
    assert.equal(saved.tasks[0].events.length, events.length);
    assert.equal(saved.tasks[0].events[0].runId, "run-0");
    assert.ok(saved.tasks[0].events.at(-1).detail.includes(ARCHIVE_MARKER));
  } finally {
    await h.dispose(false);
  }
  const reloaded = persistenceHarness(h.root);
  try {
    const state = await reloaded.api.testLoadState();
    assert.equal(state.tasks[0].events.length, 3_000);
    assert.ok(JSON.stringify(state).length <= runtime.MAX_SESSION_LENGTH);
  } finally {
    await reloaded.dispose();
  }
});

test("invalid history is rejected without masking bytes and non-history oversize remains rejected", async () => {
  const malformed = { version: 2, tasks: [{ events: [null] }] };
  assert.throws(() => compactStateHistory(malformed), /events\.0 must be an object/);
  const h = persistenceHarness();
  try {
    const invalidBytes = Buffer.from("{invalid persisted bytes");
    await fsp.writeFile(path.join(h.root, "state.json"), invalidBytes);
    await assert.rejects(h.api.testLoadState(), /malformed/);
    assert.deepEqual(
      await fsp.readFile(path.join(h.root, "state.json")),
      invalidBytes,
    );
    const task = validTask(h.root, [event("artifact", "note", "safe")]);
    task.artifacts = [{
      id: "too-large",
      title: "Too large",
      type: "document",
      content: "a".repeat(12_000_001),
      updatedAt: "2026-10-07T00:00:00.000Z",
    }];
    await assert.rejects(h.api.testSaveState({ version: 2, tasks: [task] }), /large|limit/i);
    assert.deepEqual(
      await fsp.readFile(path.join(h.root, "state.json")),
      invalidBytes,
    );
  } finally {
    await h.dispose();
  }
});

test("history compaction rejects malformed tool details instead of deleting them", () => {
  const state = {
    version: 2,
    tasks: [validTask("", [event("bad", "tool", "ok")])],
  };
  state.tasks[0].events[0].detail = { output: "not a string" };
  assert.throws(() => compactStateHistory(state), /detail must be a string/);
  assert.equal(state.tasks[0].events[0].detail.output, "not a string");
  assert.equal(MAX_TOOL_DETAIL_BUDGET, 1_000_000);
});

test("tool detail budget is strict at zero, stable across tasks, and idempotent", () => {
  const state = {
    version: 2,
    tasks: [
      validTask("", [
        event("old", "tool", "o".repeat(900)),
        event("new", "tool", "n".repeat(900)),
      ]),
    ],
  };
  const zero = compactStateHistory(state, { detailBudget: 0 });
  assert.equal(
    zero.tasks[0].events
      .filter((entry) => entry.type === "tool" && entry.actor !== "human")
      .reduce((total, entry) => total + entry.detail.length, 0),
    0,
  );
  assert.equal(zero.tasks[0].events[0].detail, "");
  assert.strictEqual(compactStateHistory(zero, { detailBudget: 0 }), zero);
  assert.equal(archiveDetail("output", 0), "");
  assert.ok(archiveDetail("output", ARCHIVE_MARKER.length + 1).includes(ARCHIVE_MARKER));

  const bounded = compactStateHistory(state, { detailBudget: 123 });
  const detailLength = bounded.tasks[0].events
    .filter((entry) => entry.type === "tool" && entry.actor !== "human")
    .reduce((total, entry) => total + entry.detail.length, 0);
  assert.ok(detailLength <= 123);
  assert.strictEqual(compactStateHistory(bounded, { detailBudget: 123 }), bounded);
});
