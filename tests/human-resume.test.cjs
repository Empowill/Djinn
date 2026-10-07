"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const Module = require("node:module");
const ts = require("typescript");
const { taskFixture } = require("./workflow-fixture.cjs");
const workflow = require("../electron/workflow.cjs");

// Run the real hook with deterministic state/effect scheduling and a controlled
// native bridge. This reaches receipt/event races without a browser or provider.
function harness() {
  const slots = [],
    effects = [],
    timers = new Set(),
    listeners = new Set();
  let cursor = 0,
    dirty = false,
    result;
  const hooks = {
    useState(initial) {
      const index = cursor++;
      if (!(index in slots))
        slots[index] = typeof initial === "function" ? initial() : initial;
      return [
        slots[index],
        (value) => {
          const next =
            typeof value === "function" ? value(slots[index]) : value;
          if (!Object.is(next, slots[index])) {
            slots[index] = next;
            dirty = true;
          }
        },
      ];
    },
    useRef(initial) {
      const index = cursor++;
      return (slots[index] ||= { current: initial });
    },
    useCallback(callback, deps) {
      const index = cursor++;
      if (
        !slots[index] ||
        deps.some((d, i) => !Object.is(d, slots[index].deps[i]))
      )
        slots[index] = { value: callback, deps };
      return slots[index].value;
    },
    useEffect(callback, deps) {
      const index = cursor++;
      if (
        !slots[index] ||
        deps.some((d, i) => !Object.is(d, slots[index].deps[i]))
      ) {
        const old = slots[index];
        slots[index] = { deps };
        effects.push(() => {
          old?.cleanup?.();
          slots[index].cleanup = callback();
        });
      }
    },
  };
  const steps = workflow.createDefaultSteps("task");
  const state = {
    version: 2,
    projects: [],
    selectedId: "task",
    tasks: [
      {
        ...taskFixture(),
        id: "task",
        project: "/tmp/djinn-fixture",
        demo: false,
        steps,
        activeStepId: steps[0].id,
        selectedStepId: steps[0].id,
      },
    ],
  };
  const starts = [],
    steers = [];
  const receipts = [];
  const bridge = {
    loadState: async () => state,
    saveState: async () => ({ saved: true }),
    getActions: async () => [],
    getEnvironment: async () => ({
      platform: "test",
      appVersion: "test",
      providers: [{ id: "codex", available: true, authenticated: true }],
    }),
    notifyQuestion: async () => ({ shown: false }),
    onEvent(callback) {
      listeners.add(callback);
      return () => listeners.delete(callback);
    },
    async startRun(input) {
      starts.push(input);
      return { runId: `run-${starts.length}` };
    },
    steerRun(input) {
      steers.push(input);
      return new Promise((resolve) => {
        receipts.push(resolve);
      });
    },
  };
  const previousWindow = global.window;
  const previousDocument = global.document;
  global.window = {
    djinn: bridge,
    addEventListener() {},
    removeEventListener() {},
  };
  global.document = { documentElement: { dataset: {} } };
  const cache = new Map();
  function load(filename) {
    if (cache.has(filename)) return cache.get(filename).exports;
    const mod = new Module(filename, module);
    cache.set(filename, mod);
    mod.require = (id) =>
      id === "react"
        ? hooks
        : id.startsWith("./")
          ? load(path.resolve(path.dirname(filename), id + ".ts"))
          : require(id);
    // Track the hook's UI timers so the test cannot leave background work.
    mod._compile(
      "const setTimeout=(...args)=>{const timer=global.setTimeout(...args);module.timers.add(timer);return timer};\n" +
        ts.transpileModule(fs.readFileSync(filename, "utf8"), {
          compilerOptions: {
            module: ts.ModuleKind.CommonJS,
            target: ts.ScriptTarget.ES2022,
          },
        }).outputText,
      filename,
    );
    return mod.exports;
  }
  // The timeout closure is called after compilation, once all modules exist.
  const { useDjinn } = load(path.resolve("src/use-djinn.ts"));
  for (const mod of cache.values()) mod.timers = timers;
  function render() {
    dirty = false;
    cursor = 0;
    result = useDjinn();
    while (effects.length) effects.shift()();
  }
  render();
  return {
    starts,
    steers,
    get current() {
      return result;
    },
    receipt(value, index = receipts.length - 1) {
      receipts[index](value);
    },
    emit(type, data, runId = "run-1") {
      for (const callback of listeners)
        callback({
          taskId: "task",
          runId,
          stepId: steps[0].id,
          timestamp: new Date().toISOString(),
          type,
          data,
        });
    },
    async flush() {
      for (let i = 0; i < 15; i++) {
        await new Promise((resolve) => setImmediate(resolve));
        if (dirty) render();
      }
    },
    dispose() {
      for (const slot of slots) slot?.cleanup?.();
      for (const timer of timers) clearTimeout(timer);
      global.window = previousWindow;
      global.document = previousDocument;
    },
  };
}

for (const receiptFirst of [true, false])
  test(`message received during cancellation resumes once (${receiptFirst ? "receipt" : "close"} first)`, async () => {
    const h = harness();
    try {
      await h.flush();
      await h.current.start();
      await h.flush();
      const sending = h.current.indicate(
        "Préserver les fichiers et poursuivre.",
      );
      if (!receiptFirst) {
        h.emit("status", { status: "cancelled" });
        await h.flush();
      }
      h.receipt({ status: "prevented", reason: "run_cancelled" });
      await sending;
      if (receiptFirst) {
        await h.flush();
        assert.equal(h.starts.length, 1);
        h.emit("status", { status: "cancelled" });
      }
      await h.flush();
      assert.equal(h.starts.length, 2);
      assert.equal(h.starts[1].stepId, h.starts[0].stepId);
      assert.equal(
        h.starts[1].guidance[0].text,
        "Préserver les fichiers et poursuivre.",
      );
      assert.equal(h.current.task.steps[0].approvedAt, undefined);
      assert.equal(h.current.task.steps[1].status, "pending");
    } finally {
      h.dispose();
    }
  });

test("all blocking answers arriving before native close resume automatically once", async () => {
  const h = harness();
  try {
    await h.flush();
    await h.current.start();
    await h.flush();
    for (const id of ["q1", "q2"])
      h.emit("question", { id, title: id, blocking: true, agentId: "lead" });
    await h.flush();
    h.current.answer("q1", "Oui");
    await h.flush();
    assert.equal(h.starts.length, 1);
    h.current.answer("q2", "Oui");
    await h.flush();
    assert.equal(h.starts.length, 1, "writer remains owned until native close");
    h.emit("status", { status: "completed" });
    await h.flush();
    assert.equal(h.starts.length, 2);
    assert.equal(h.current.task.questions.filter((q) => q.answer).length, 2);
    assert.equal(h.current.task.steps[0].approvedAt, undefined);
  } finally {
    h.dispose();
  }
});

test("late cancellation receipt cannot restart a newer completed passage", async () => {
  const h = harness();
  try {
    await h.flush();
    await h.current.start();
    await h.flush();
    const first = h.current.indicate("Première indication.");
    const second = h.current.indicate("Deuxième indication.");
    h.receipt({ status: "prevented", reason: "run_cancelled" }, 0);
    await first;
    h.emit("status", { status: "cancelled" });
    await h.flush();
    assert.equal(h.starts.length, 2);
    assert.equal(h.starts[1].guidance.length, 2);
    h.emit("status", { status: "completed" }, "run-2");
    await h.flush();
    h.receipt({ status: "prevented", reason: "run_cancelled" }, 1);
    await second;
    await h.flush();
    assert.equal(h.starts.length, 2);
  } finally {
    h.dispose();
  }
});
