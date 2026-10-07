"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const ts = require("typescript");
const runtime = require("../electron/runtime.cjs");
const w = runtime.workflow;
const { taskFixture } = require("./workflow-fixture.cjs");
const time = "2026-10-06T08:00:00.000Z";
const task = () => ({
  ...taskFixture(),
  steps: w.createDefaultSteps("t"),
  activeStepId: "t:step:1",
  selectedStepId: "t:step:1",
  titleSource: "placeholder",
});
const project = (directory) => ({
  id: "p",
  name: "Projet",
  directory,
  conventions: "# Conventions",
  locations: { prototype: "app/prototypes" },
  workflows: [],
  updatedAt: time,
});
const approve = (t, id) =>
  w.approveStep(
    w.finishStepRun(w.startStep(t, id, time), id, "completed"),
    id,
    "human",
    time,
  );

test("completion awaits explicit human approval; future stages and blocking answers cannot start", () => {
  let t = task();
  assert.equal(w.canStartStep(t, t.steps[1].id), false);
  t = w.startStep(t, t.activeStepId, time);
  t = w.finishStepRun(t, t.activeStepId, "completed", "Output");
  assert.equal(t.steps[0].status, "awaiting_human");
  assert.equal(w.canStartStep(t, t.steps[1].id), false);
  assert.throws(() => w.approveStep(t, t.activeStepId, "agent"), /invalide/);
  t.questions.push({ id: "q", blocking: true, stepId: t.activeStepId });
  assert.throws(() => w.approveStep(t, t.activeStepId, "human"), /invalide/);
  t.questions[0].answer = "Oui";
  t = w.approveStep(t, t.activeStepId, "human", time);
  assert.equal(w.canStartStep(t, t.steps[1].id), true);
  assert.equal(t.steps[1].status, "pending");
});

test("automatic implementation completion advances without approval while review still requires a human", () => {
  let t = task();
  t.steps[0].validation = "automatic";
  t = w.startStep(t, t.activeStepId, time);
  t = w.finishStepRun(t, t.activeStepId, "completed", "Built", undefined, time);
  assert.equal(t.steps[0].status, "completed");
  assert.equal(t.steps[0].completedAt, time);
  assert.equal(t.steps[0].approvedAt, undefined);
  assert.equal(t.steps[0].approvedBy, undefined);
  assert.equal(w.canStartStep(t, t.steps[1].id), true);
  t.steps[1].type = "review";
  assert.equal(w.canStartStep(t, t.steps[1].id), true);
});

test("review stages remain human validated", () => {
  let t = task();
  t.steps = [
    {
      ...t.steps[0],
      type: "review",
    },
  ];
  t.activeStepId = t.steps[0].id;
  t.selectedStepId = t.activeStepId;
  t = w.startStep(t, t.activeStepId, time);
  t = w.finishStepRun(t, t.activeStepId, "completed", "Checked", undefined, time);
  assert.equal(t.steps[0].status, "awaiting_human");
  assert.equal(t.steps[0].completedAt, undefined);
  assert.equal(w.approveStep(t, t.activeStepId, "human", time).steps[0].approvedBy, "human");
  assert.throws(
    () => w.validateStep({ ...t.steps[0], validation: "automatic" }),
    /validation/,
  );
});

test("the lead may revise only the pending suffix and the change is journaled", () => {
  let t = task();
  t.workflowMode = "flexible";
  t = approve(t, t.steps[0].id);
  const currentId = t.steps[1].id;
  const prefixIds = t.steps.slice(0, 2).map((step) => step.id);
  t.activeStepId = currentId;
  t.selectedStepId = currentId;
  const amended = w.amendWorkflow(
    t,
    {
      currentStepId: currentId,
      reason: "Le résultat appelle une implémentation plus courte.",
      steps: [
        {
          type: "implementation",
          title: "Implémentation ajustée",
          objective: "Appliquer le nouveau périmètre.",
          validation: "automatic",
        },
      ],
    },
    time,
  );
  assert.deepEqual(amended.steps.slice(0, 2).map((step) => step.id), prefixIds);
  assert.equal(amended.steps[2].validation, "automatic");
  assert.equal(amended.activeStepId, currentId);
  assert.equal(amended.events.at(-1).actor, "agent");
  assert.equal(amended.events.at(-1).time, time);
  assert.equal(w.validateTaskWorkflow(amended).steps.length, 3);
  assert.throws(
    () =>
      w.amendWorkflow(
        { ...t, projectSnapshot: { workflowPolicy: "enforced" } },
        { steps: [], reason: "Interdit" },
      ),
    /workflow\.amend\.active/,
  );
});

test("late run completion, error and pause never approve stages or alter another active stage", () => {
  const t = { ...w.startStep(task(), "t:step:1"), runId: "new" };
  assert.strictEqual(w.finishStepRun(t, "t:step:2", "completed"), t);
  assert.strictEqual(w.finishStepRun(t, "t:step:1", "completed", "", "old"), t);
  assert.equal(
    w.finishStepRun(t, "t:step:1", "error").steps[0].status,
    "error",
  );
  assert.equal(
    w.finishStepRun(t, "t:step:1", "cancelled").steps[0].status,
    "paused",
  );
});

test("delivery requires a human approved review after the latest code stage", () => {
  let t = task();
  t = approve(t, t.steps[0].id);
  t = approve(t, t.steps[1].id);
  const delivery = t.steps[3];
  t.steps = [t.steps[0], t.steps[1], delivery];
  assert.equal(w.canStartStep(t, delivery.id), false);
  t = task();
  for (const id of t.steps.slice(0, 3).map((s) => s.id)) t = approve(t, id);
  assert.equal(w.canStartStep(t, delivery.id), true);
  t.steps[2].needsRevalidation = true;
  assert.equal(w.canStartStep(t, delivery.id), false);
});

test("reopening preserves records and invalidates dependent accepted results", () => {
  let t = task();
  for (const id of t.steps.slice(0, 3).map((s) => s.id)) t = approve(t, id);
  t.events.push({ id: "history", detail: "Keep" });
  t = w.reopenStep(t, t.steps[1].id);
  assert.equal(t.steps[2].status, "completed");
  assert.equal(t.steps[2].needsRevalidation, true);
  assert.equal(w.canStartStep(t, t.steps[1].id), true);
  assert.equal(w.canStartStep(t, t.steps[3].id), false);
  assert.equal(w.validateSteps(t.steps).length, 4);
  assert.equal(t.events[0].detail, "Keep");
});

test("v1 migration deduplicates projects, preserves ambiguous history, and is idempotent", () => {
  const original = {
    version: 1,
    tasks: [
      taskFixture({ project: "/tmp/project", status: "running" }),
      taskFixture({ id: "task-2", project: "/tmp/project" }),
    ],
  };
  const copy = JSON.stringify(original);
  const migrated = runtime.validateState(original);
  assert.equal(migrated.version, 2);
  assert.equal(migrated.projects.length, 1);
  assert.equal(migrated.tasks[0].status, "paused");
  assert.equal(migrated.tasks[0].legacyHistory, true);
  assert.equal(migrated.tasks[0].steps[1].status, "pending");
  assert.deepEqual(runtime.validateState(migrated), migrated);
  assert.equal(JSON.stringify(original), copy);
});

test("native and renderer validation reject broken project, step references, and approvals", () => {
  for (const mutate of [
    (t) => (t.steps[1].id = t.steps[0].id),
    (t) => (t.steps[0].status = "completed"),
    (t) => (t.selectedStepId = t.steps[2].id),
    (t) => (t.questions = [{ id: "q", stepId: "missing" }]),
    (t) => (t.steps[0].approvedAt = time),
    (t) => (t.projectSnapshot = { ...project("/tmp"), capturedAt: time }),
  ]) {
    const t = task();
    mutate(t);
    assert.throws(() => runtime.validateTask(t));
  }
  assert.throws(
    () => runtime.validateState({ version: 2, tasks: [taskFixture()] }),
    /steps/,
  );
});

test("project validation checks locations including symbolic links escaping root", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-path-"));
  const outside = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-outside-"));
  try {
    assert.equal(
      runtime.validateProjectDirectory(project(root)).directory,
      fs.realpathSync(root),
    );
    for (const location of ["../escape", "/tmp", "C:\\other", "foo/../escape"])
      assert.throws(() =>
        runtime.validateProject({
          ...project(root),
          locations: { prototype: location },
        }),
      );
    fs.symlinkSync(outside, path.join(root, "escape"), "dir");
    assert.throws(
      () =>
        runtime.validateProjectDirectory({
          ...project(root),
          locations: { prototype: "escape/missing" },
        }),
      /root/,
    );
    assert.throws(
      () => runtime.validateProjectDirectory(project("")),
      /Associate/,
    );
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
    fs.rmSync(outside, { recursive: true, force: true });
  }
});

test("native binding uses the recorded step and configured provider/model; model scope is overwritten", () => {
  const t = { ...task(), project: "/tmp", model: "configured" };
  const input = runtime.validateRunInput({
    taskId: t.id,
    stepId: t.activeStepId,
    provider: t.provider,
    cwd: t.project,
    model: t.model,
    prompt: "Inspect",
    mode: "plan",
  });
  const bound = runtime.bindRunToTask(input, t);
  assert.equal(bound.step.type, "reflection");
  assert.throws(
    () => runtime.bindRunToTask({ ...input, mode: "execute" }, t),
    /mode/,
  );
  assert.throws(
    () => runtime.bindRunToTask({ ...input, model: "substitute" }, t),
    /configured/,
  );
  assert.throws(
    () => runtime.bindRunToTask({ ...input, stepId: t.steps[1].id }, t),
    /validation/,
  );
  const run = { kind: "lead", runId: "real-run", stepId: "real-step" };
  const event = runtime.scopeProviderEvent(run, {
    type: "artifact",
    data: { stepId: "forged", runId: "forged" },
  });
  assert.equal(event.data.stepId, "real-step");
  assert.equal(event.data.runId, "real-run");
  assert.equal(
    runtime.scopeProviderEvent(run, {
      type: "phase",
      data: { phase: "delivery", approvedBy: "human" },
    }).type,
    "note",
  );
});

test("mission titles come only from the lead and never overwrite a human rename", () => {
  const event = { type: "mission_metadata", data: { title: "Generated" } };
  assert.equal(
    runtime.scopeProviderEvent({ kind: "agent", agentId: "worker" }, event),
    null,
  );
  assert.equal(
    runtime.scopeProviderEvent({ kind: "lead", titleSource: "human" }, event),
    null,
  );
  assert.equal(
    w.applyMissionTitle(task(), "Generated", time).titleSource,
    "agent",
  );
  const renamed = { ...task(), title: "Mon titre", titleEditedAt: time };
  assert.strictEqual(w.applyMissionTitle(renamed, "Overwrite"), renamed);
  assert.deepEqual(
    runtime.parseDjinnEvents(
      'DJINN_EVENT:{"type":"mission_metadata","data":{"title":"Generated"}}',
    ).events[0].data,
    { title: "Generated" },
  );
});

test("more than three agents are configurable while unknown ownership remains exclusive", () => {
  const input = {
    taskId: "t",
    provider: "codex",
    cwd: "/tmp",
    prompt: "Inspect",
    mode: "plan",
    concurrency: 8,
    agents: Array.from({ length: 8 }, (_, i) => ({
      id: `a${i}`,
      name: `A${i}`,
      role: "Inspect",
      prompt: "Read",
    })),
  };
  assert.equal(runtime.buildExecutionPlan(input).concurrency, 8);
  assert.equal(
    runtime.buildExecutionPlan({ ...input, mode: "execute" }).concurrency,
    1,
  );
  assert.equal(
    runtime.validateTask(
      taskFixture({
        configuration: {
          prototype: "",
          review: "",
          deliverables: [],
          concurrency: 8,
        },
      }),
    ).configuration.concurrency,
    8,
  );
});

test("Electron workflow and session validators stay identical to their TypeScript sources", () => {
  for (const name of ["workflow", "session-validation"]) {
    const source = fs.readFileSync(
      path.resolve(__dirname, `../src/${name}.ts`),
      "utf8",
    );
    const expected =
      `// Generated from src/${name}.ts. Keep both in sync.\n` +
      ts
        .transpileModule(source, {
          compilerOptions: {
            module: ts.ModuleKind.CommonJS,
            target: ts.ScriptTarget.ES2022,
          },
        })
        .outputText.replace(
          'require("./workflow")',
          'require("./workflow.cjs")',
        );
    assert.equal(
      fs.readFileSync(
        path.resolve(__dirname, `../electron/${name}.cjs`),
        "utf8",
      ),
      expected,
    );
  }
});

test("human messages and all blocking answers resume only the current stage without approving it", () => {
  let t = w.startStep(task(), "t:step:1");
  t.questions = [
    { id: "q1", blocking: true, stepId: t.activeStepId },
    { id: "q2", blocking: true, stepId: t.activeStepId },
  ];
  t = w.finishStepRun(t, t.activeStepId, "completed");
  assert.equal(w.canResumeAfterHumanInput(t), false);
  t.questions[0].answer = "Yes";
  assert.equal(w.canResumeAfterHumanInput(t), false);
  t.questions[1].answer = "Yes";
  assert.equal(w.canResumeAfterHumanInput(t), true);
  assert.equal(t.steps[0].approvedAt, undefined);
  assert.equal(w.canStartStep(t, t.steps[1].id), false);
});
test("revalidation flag cannot authorize two simultaneous started stages", () => {
  const t = task();
  t.steps[0].status = "paused";
  t.steps[1].status = "running";
  t.steps[1].needsRevalidation = true;
  assert.throws(() => w.validateSteps(t.steps), /progression/);
});
test("model agent events are queued proposals; workers cannot invent other active agents", () => {
  const proposed = {
    type: "agent",
    data: { id: "new-worker", status: "running" },
  };
  assert.equal(
    runtime.scopeProviderEvent({ kind: "lead", runId: "r" }, proposed).data
      .status,
    "queued",
  );
  assert.equal(
    runtime.scopeProviderEvent(
      { kind: "agent", agentId: "worker", runId: "w" },
      proposed,
    ),
    null,
  );
});

test("native binding respects simultaneous limit, allows a bounded queue and rejects duplicate identities", () => {
  const t = {
    ...task(),
    project: "/tmp",
    configuration: {
      prototype: "",
      review: "",
      deliverables: [],
      concurrency: 4,
    },
  };
  const input = runtime.validateRunInput({
    taskId: t.id,
    stepId: t.activeStepId,
    provider: t.provider,
    cwd: t.project,
    prompt: "Inspect",
    mode: "plan",
    concurrency: 8,
  });
  assert.equal(runtime.bindRunToTask(input, t).concurrency, 4);
  const agents = Array.from({ length: 5 }, (_, i) => ({
    id: `worker-${i}`,
    name: "Worker",
    role: "Inspect",
    prompt: "Read only",
  }));
  assert.equal(runtime.bindRunToTask({ ...input, agents }, t).concurrency, 4);
  assert.throws(
    () =>
      runtime.validateRunInput({ ...input, agents: [agents[0], agents[0]] }),
    /duplicat/i,
  );
});

test("an active stage cannot point away from an unfinished started stage", () => {
  const t = task();
  t.steps[0] = {
    ...t.steps[0],
    status: "completed",
    approvedBy: "human",
    approvedAt: time,
  };
  t.steps[1].status = "paused";
  assert.throws(() => w.validateTaskWorkflow(t), /activeStepId/);
});

test("large history context stays bounded and delivery completion requires the human", () => {
  const input = {
    taskId: "t",
    provider: "codex",
    cwd: "/tmp",
    prompt: "X".repeat(100000),
    mode: "execute",
    concurrency: 16,
    priorSummaries: Array(100).fill("R".repeat(8000)),
    workerSummaries: Array(16).fill("W".repeat(8000)),
  };
  assert.ok(
    runtime.composeRunPrompt(input).length <= runtime.MAX_PROMPT_LENGTH + 20000,
  );
  let t = task();
  t.steps = [w.createDefaultSteps("d").at(-1)];
  t.activeStepId = t.steps[0].id;
  t.selectedStepId = t.activeStepId;
  t = w.startStep(t, t.activeStepId);
  t = w.finishStepRun(t, t.activeStepId, "completed");
  assert.throws(() => w.approveStep(t, t.activeStepId, "agent"));
  assert.equal(w.approveStep(t, t.activeStepId, "human").status, "done");
});

test("flexible discussions add only a human-approved continuation and keep specifications read-only", () => {
  let t = task();
  t.workflowMode = "flexible";
  t.steps = t.steps.slice(0, 1);
  t.activeStepId = t.steps[0].id;
  t.selectedStepId = t.activeStepId;
  t = approve(t, t.activeStepId);
  const proposal = w.createDiscussionStep("continuation", "specification");
  assert.equal(w.stepMode(proposal.type), "plan");
  const appended = w.appendStep(t, proposal);
  assert.equal(appended.steps.length, 2);
  assert.equal(appended.steps.at(-1).type, "specification");
  assert.equal(appended.activeStepId, proposal.id);
  assert.equal(appended.status, "idle");
  assert.throws(
    () => w.appendStep({ ...t, workflowMode: "fixed" }, proposal),
    /step\.append/,
  );
  assert.throws(
    () => w.appendStep({ ...t, status: "running" }, proposal),
    /step\.append/,
  );
  assert.throws(
    () => w.appendStep({ ...t, steps: [{ ...t.steps[0], approvedBy: "agent" }] }, proposal),
    /step\.append\.previous/,
  );
});

test("project sources and workflow proposals validate without changing legacy fixed semantics", () => {
  const p = runtime.validateProject({
    ...project("/tmp/project"),
    workflows: [{ id: "classic", title: "Classique", steps: w.createDefaultSteps("classic") }],
    sourcesOfTruth: [
      { id: "context", title: "Contexte", path: "docs/CONTEXT.md", description: "Décisions" },
    ],
    workflowPolicy: "enforced",
  });
  assert.equal(p.sourcesOfTruth[0].path, "docs/CONTEXT.md");
  assert.equal(p.workflowPolicy, "enforced");
  assert.throws(() => runtime.validateProject({ ...project("/tmp/project"), workflowPolicy: "enforced" }), /workflowPolicy/);
  assert.throws(() => runtime.validateProject({ ...p, sourcesOfTruth: [{ ...p.sourcesOfTruth[0], path: "../escape" }] }));
  const t = task();
  t.nextStepProposal = {
    type: "specification",
    title: "Clarifier",
    objective: "Décrire le contrat",
    reason: "La décision manque encore",
    stepId: t.activeStepId,
  };
  assert.equal(w.validateTaskWorkflow(t).nextStepProposal.type, "specification");
  assert.equal(w.validateTaskWorkflow({ ...t, workflowMode: "fixed" }).workflowMode, "fixed");
  assert.equal(w.validateTaskWorkflow({ ...t, workflowMode: undefined }).workflowMode, undefined);
});

test("pending workflow amendments are scoped to the active flexible stage", () => {
  const t = task();
  t.workflowMode = "flexible";
  t.workflowAmendment = {
    stepId: t.activeStepId,
    runId: "run-1",
    reason: "Réduire le suffixe.",
    steps: [
      {
        type: "implementation",
        title: "Construire",
        objective: "Appliquer le nouveau périmètre.",
        validation: "automatic",
      },
    ],
  };
  t.runId = "run-1";
  const restored = w.validateTaskWorkflow(t);
  assert.equal(restored.workflowAmendment.stepId, t.activeStepId);
  assert.equal(restored.workflowAmendment.steps[0].validation, "automatic");
  assert.throws(
    () => w.validateTaskWorkflow({ ...t, workflowAmendment: { ...t.workflowAmendment, stepId: "missing" } }),
    /workflowAmendment/,
  );
});
