"use strict";
const test = require("node:test"),
  assert = require("node:assert/strict"),
  ts = require("typescript"),
  fs = require("node:fs"),
  Module = require("node:module"),
  path = require("node:path");
const filename = path.resolve(__dirname, "../src/mission-view.ts"),
  loaded = new Module(filename, module);
loaded.filename = filename;
loaded.paths = Module._nodeModulePaths(path.dirname(filename));
// The module imports ./i18n at run time: load sibling TypeScript files the same way.
Module._extensions[".ts"] ??= (mod, file) =>
  mod._compile(
    ts.transpileModule(fs.readFileSync(file, "utf8"), {
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2022,
        esModuleInterop: true,
      },
    }).outputText,
    file,
  );
loaded._compile(
  ts.transpileModule(fs.readFileSync(filename, "utf8"), {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText,
  filename,
);
const { stepView, mergeStepView, legacyView, reconnectNativeRun } =
  loaded.exports;
test("reconnection preserves observed child identity and does not assign the lead model", () => {
  const task = { id: "m", provider: "codex", model: "lead-model", agents: [], questions: [], steps: [{ id: "s", status: "paused" }] };
  const run = { taskId: "m", stepId: "s", runId: "r", mode: "execute", status: "running", phase: "lead", startedAt: "2026-10-06T10:00:00Z", activeAgents: [{ id: "child", name: "Inspecteur", task: "Inspection", runId: "r", readOnly: false, origin: "codex", provider: "codex", providerThreadId: "thread-child", parentAgentId: "lead", status: "blocked" }], events: [] };
  const reconnected = reconnectNativeRun(task, run);
  assert.equal(reconnected.agents[0].model, "");
  assert.equal(reconnected.agents[0].status, "blocked");
  assert.equal(reconnected.agents[0].providerThreadId, "thread-child");
  assert.equal(reconnected.agents[0].parentAgentId, "lead");
  const known = reconnectNativeRun(task, { ...run, activeAgents: [{ ...run.activeAgents[0], model: "child-model" }] });
  assert.equal(known.agents[0].model, "child-model");
  const replay = reconnectNativeRun(task, { ...run, activeAgents: [], observedAgents: [{ ...run.activeAgents[0], role: "Inspection", status: "done" }] });
  assert.equal(replay.agents[0].status, "done");
  assert.equal(replay.agents[0].model, "");
  assert.equal(replay.agents[0].progress, 100);
});
test("reload reconnects only authoritative live passes without replaying or changing the viewed step", () => {
  const task = {
    id: "mission",
    status: "paused",
    provider: "codex",
    model: "configured",
    selectedStepId: "past",
    activeStepId: "current",
    questions: [],
    steps: [
      { id: "past", status: "completed" },
      { id: "current", status: "paused" },
    ],
    agents: [
      {
        id: "worker",
        status: "queued",
        writeScope: ["owned.ts"],
        progress: 30,
      },
    ],
    runtimeEventIds: ["already-saved"],
    events: [{ id: "already-saved" }],
  };
  const run = {
    taskId: "mission",
    stepId: "current",
    runId: "native-run",
    mode: "execute",
    status: "running",
    phase: "workers",
    startedAt: "2026-10-06T08:00:00Z",
    lastActivityAt: "2026-10-06T09:00:00Z",
    events: [],
    activeAgents: [
      {
        id: "worker",
        name: "Worker",
        task: "Owned task",
        runId: "native-child",
        readOnly: false,
      },
    ],
  };
  const restored = reconnectNativeRun(task, run);
  assert.equal(restored.runId, "native-run");
  assert.equal(restored.selectedStepId, "past");
  assert.equal(restored.agents[0].status, "running");
  assert.equal(restored.agents[0].runId, "native-child");
  assert.deepEqual(restored.agents[0].writeScope, ["owned.ts"]);
  assert.equal(restored.activity.lead, "supervises");
  assert.equal(restored.activity.lastActivityAt, run.lastActivityAt);
  assert.deepEqual(restored.events, task.events);
  assert.deepEqual(restored.runtimeEventIds, ["already-saved"]);
  assert.equal(
    reconnectNativeRun(task, { ...run, taskId: "other" }),
    undefined,
  );
  assert.equal(
    reconnectNativeRun(task, { ...run, stepId: "unknown" }),
    undefined,
  );
  assert.equal(task.status, "paused", "snapshot application is pure");
});
test("selection scopes all records and preserves active agent history without starting anything", () => {
  const task = {
    activeStepId: "current",
    selectedStepId: "past",
    agents: [{ id: "lead", stepId: "current" }],
    agentHistory: { past: [{ id: "lead", stepId: "past", name: "Earlier" }] },
    questions: [{ id: "q", stepId: "past" }],
    events: [
      { id: "legacy" },
      { id: "old", stepId: "past" },
      { id: "live", stepId: "current" },
    ],
    artifacts: [
      { id: "a", stepId: "past" },
      { id: "b", stepId: "current" },
    ],
    feedback: [],
    instructions: [],
    actions: [],
  };
  const view = stepView(task);
  assert.deepEqual(
    view.events.map((e) => e.id),
    ["old"],
  );
  assert.equal(view.agents[0].name, "Earlier");
  assert.equal(view.activeStepId, "current");
  assert.deepEqual(
    view.artifacts.map((a) => a.id),
    ["a"],
  );
  const updated = mergeStepView(task, {
    ...view,
    artifacts: [{ ...view.artifacts[0], content: "Human edit" }],
  });
  assert.deepEqual(
    updated.artifacts.map((a) => a.id),
    ["b", "a"],
  );
  assert.equal(updated.agents[0].stepId, "current");
  assert.equal(updated.events.find((e) => e.id === "legacy").stepId, undefined);
});
test("a late native message is preserved when a human edits a support from an older render", () => {
  const previous = {
    activeStepId: "s",
    selectedStepId: "s",
    questions: [],
    agents: [],
    events: [
      { id: "old", stepId: "s" },
      { id: "new", stepId: "s" },
    ],
    artifacts: [],
    feedback: [],
    instructions: [{ id: "human", stepId: "s" }],
    actions: [],
  };
  const edited = {
    ...previous,
    events: [previous.events[0]],
    instructions: [],
  };
  const merged = mergeStepView(previous, edited);
  assert.ok(merged.events.some((e) => e.id === "new"));
  assert.ok(merged.instructions.some((i) => i.id === "human"));
});

test("migrated history is explicitly accessible without inventing stage scope", () => {
  const task = {
    activeStepId: "s",
    selectedStepId: "s",
    questions: [],
    agents: [],
    events: [{ id: "legacy" }, { id: "current", stepId: "s" }],
    artifacts: [{ id: "legacy-doc" }],
    feedback: [],
  };
  const history = legacyView(task);
  assert.deepEqual(history.events, [{ id: "legacy" }]);
  assert.equal(history.artifacts[0].stepId, undefined);
  assert.equal(history.activeStepId, "s");
});
