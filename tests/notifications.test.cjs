"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { Module, createRequire } = require("node:module");
const { EventEmitter } = require("node:events");

// Load the real native handler with controlled Electron events and timers.
// No operating-system notification or provider is launched by this harness.
function harness({ supported = true, throws = false } = {}) {
  const instances = [],
    events = [],
    archived = [],
    timers = new Map();
  const windowCalls = [];
  let timerId = 0;
  class Notification extends EventEmitter {
    static isSupported() {
      return supported;
    }
    constructor(options) {
      super();
      this.options = options;
      instances.push(this);
    }
    show() {
      if (throws) throw new Error("native show failed");
    }
  }
  const filename = path.resolve(__dirname, "../electron/main.cjs");
  const realRequire = createRequire(filename);
  const compiled = new Module(filename);
  compiled.require = (name) =>
    name === "electron"
      ? {
          Notification,
          app: {
            on() {},
            whenReady: () => new Promise(() => {}),
            getPath: () => "/tmp",
            quit() {},
          },
          BrowserWindow: {},
          dialog: {},
          ipcMain: {},
          shell: {},
        }
      : realRequire(name);
  compiled.testBindings = {
    events,
    archived,
    timers,
    windowCalls,
    setTimeout: (fn) => {
      timers.set(++timerId, fn);
      return timerId;
    },
    clearTimeout: (id) => timers.delete(id),
  };
  const source = fs
    .readFileSync(filename, "utf8")
    .replace(
      "function sendEvent(event) {",
      "function sendEvent(event) { events.push(event); return;",
    );
  compiled._compile(
    "const { events, archived, timers, windowCalls, setTimeout, clearTimeout } = module.testBindings;\n" +
      source +
      "\nmissionJournal = { append: async (taskId, event) => archived.push({taskId,event}) };" +
      "\nmainWindow = { isDestroyed: () => false, isMinimized: () => true, restore: () => windowCalls.push('restore'), show: () => windowCalls.push('show'), focus: () => windowCalls.push('focus') };" +
      "\nmodule.exports.pendingClicks = pendingNotificationClicks;",
    filename,
  );
  return {
    api: compiled.exports,
    instances,
    events,
    archived,
    timers,
    windowCalls,
  };
}
const input = {
  taskId: "mission",
  questionId: "question",
  title: "Décision",
  body: "Une réponse est attendue.",
};

test("completed-stage notification preserves its exact target through the native click", async () => {
  const h = harness();
  const pending = h.api.notifyQuestion({
    ...input,
    questionId: "step:review",
    title: "Étape terminée",
    body: "Harmoniser Djinn — Vérifier · Résultat à valider",
  });
  h.instances[0].emit("show");
  assert.equal((await pending).shown, true);
  h.instances[0].emit("click");
  assert.equal(h.events[0].data.questionId, "step:review");
  assert.equal(h.events[0].taskId, "mission");
  h.instances[0].emit("close");
});

test("native notification waits for show, keeps click routing and releases on close", async () => {
  const h = harness();
  let settled = false;
  const result = h.api.notifyQuestion(input).then((value) => {
    settled = true;
    return value;
  });
  await Promise.resolve();
  assert.equal(settled, false);
  const n = h.instances[0];
  assert.deepEqual(n.options, { title: input.title, body: input.body });
  assert.equal(h.api.activeNotifications.has(n), true);
  n.emit("show");
  assert.deepEqual(await result, { shown: true });
  assert.equal(h.timers.size, 0);
  assert.equal(h.api.activeNotifications.has(n), true);
  n.emit("click");
  assert.deepEqual(h.windowCalls, ["restore", "show", "focus"]);
  const click = h.events[0];
  assert.equal(click.type, "notification_clicked");
  assert.equal(click.taskId, input.taskId);
  assert.equal(click.data.questionId, input.questionId);
  assert.equal(h.api.pendingClicks.get(input.taskId), click);
  assert.equal(h.archived[0].event, click);
  n.emit("close");
  assert.equal(h.api.activeNotifications.size, 0);
  assert.equal(n.eventNames().length, 0);
});

test("native notification reports failed, early close and timeout without success", async () => {
  for (const [event, reason] of [
    ["failed", "failed"],
    ["close", "closed"],
    ["timeout", "timeout"],
  ]) {
    const h = harness();
    const pending = h.api.notifyQuestion(input);
    const n = h.instances[0];
    if (event === "timeout") [...h.timers.values()][0]();
    else n.emit(event, new Error("delivery failed"));
    const result = await pending;
    assert.equal(result.shown, false);
    assert.equal(result.reason, reason);
    if (event === "failed") assert.equal(result.message, "delivery failed");
    assert.equal(h.api.activeNotifications.size, 0);
    assert.equal(h.timers.size, 0);
    assert.equal(n.eventNames().length, 0);
    n.emit("click");
    assert.equal(h.events.length, 0);
  }
});

test("native notification handles unsupported platforms, show exceptions and malformed input", async () => {
  const unsupported = harness({ supported: false });
  assert.deepEqual(await unsupported.api.notifyQuestion(input), {
    shown: false,
    reason: "unsupported",
  });
  assert.equal(unsupported.instances.length, 0);
  const h = harness({ throws: true });
  assert.deepEqual(await h.api.notifyQuestion(input), {
    shown: false,
    reason: "error",
    message: "native show failed",
  });
  assert.equal(h.api.activeNotifications.size, 0);
  assert.equal(h.timers.size, 0);
  await assert.rejects(
    h.api.notifyQuestion({ ...input, questionId: "" }),
    /questionId is invalid/,
  );
});
