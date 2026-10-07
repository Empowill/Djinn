"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const fs = require("node:fs");
const Module = require("node:module");
const ts = require("typescript");
const filename = path.resolve(__dirname, "../src/step-notifications.ts");
const loaded = new Module(filename, module);
loaded.filename = filename;
loaded._compile(
  ts.transpileModule(fs.readFileSync(filename, "utf8"), {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText,
  filename,
);
const { StepCompletionTracker, selectCompletedStep } = loaded.exports;
const task = (status, id = "mission") => ({
  id,
  title: "Harmoniser Djinn",
  steps: [{ id: "review", title: "Vérifier", type: "review", status }],
});

test("intermediate automatic transitions stay quiet while recipe and final results notify", () => {
  const tracker = new StepCompletionTracker();
  const mission = (status) => ({
    ...task("pending"),
    steps: [
      {
        id: "implementation",
        title: "Build",
        type: "implementation",
        status,
        validation: "automatic",
      },
      { id: "review", title: "Test", type: "review", status: "pending" },
    ],
  });
  tracker.sync([mission("running")]);
  assert.deepEqual(tracker.sync([mission("completed")]), []);
});

test("successful live steps notify once, including human gates and automatic progression", () => {
  const tracker = new StepCompletionTracker();
  assert.deepEqual(tracker.sync([task("running")]), []);
  const alerts = tracker.sync([task("awaiting_human")]);
  assert.equal(alerts.length, 1);
  assert.equal(alerts[0].questionId, "step:review");
  assert.match(alerts[0].body, /Harmoniser Djinn.*Vérifier.*à valider/);
  assert.deepEqual(tracker.sync([task("awaiting_human")]), []);
  assert.deepEqual(tracker.sync([task("completed")]), []); // Human approval is not a second run result.
  tracker.sync([task("running")]);
  assert.equal(tracker.sync([task("completed")]).length, 1); // A real rerun can finish again.
});

test("boot, imports, demo, failed and interrupted passages do not announce successful completion", () => {
  for (const status of ["error", "blocked", "paused", "pending"]) {
    const tracker = new StepCompletionTracker();
    tracker.sync([task("running")]);
    assert.deepEqual(tracker.sync([task(status)]), []);
  }
  const tracker = new StepCompletionTracker();
  assert.deepEqual(tracker.sync([task("completed")]), []);
  assert.deepEqual(
    tracker.sync([task("completed"), task("awaiting_human", "imported")]),
    [],
  );
  tracker.sync([{ ...task("running"), demo: true }]);
  assert.deepEqual(tracker.sync([{ ...task("completed"), demo: true }]), []);
});

test("notification clicks select their historical step and preserve active mission ownership", () => {
  const mission = {
    ...task("completed"),
    activeStepId: "delivery",
    selectedStepId: "delivery",
  };
  const unrelated = task("running", "unrelated");
  const state = { tasks: [mission, unrelated], selectedId: "unrelated" };
  const selected = selectCompletedStep(state, "mission", "review");
  assert.equal(selected.selectedId, "mission");
  assert.equal(selected.tasks[0].selectedStepId, "review");
  assert.equal(selected.tasks[0].activeStepId, "delivery");
  assert.equal(selected.tasks[1], unrelated);
  assert.equal(selectCompletedStep(state, "deleted", "review"), state);
  assert.equal(
    selectCompletedStep(state, "mission", "deleted").tasks,
    state.tasks,
  );
});
