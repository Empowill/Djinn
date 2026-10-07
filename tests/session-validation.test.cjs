"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const Module = require("node:module");
const ts = require("typescript");

// Compile isolated source modules in memory; tests never rewrite application files.
function loadTypeScript(relativePath) {
  const filename = path.resolve(__dirname, "..", relativePath);
  const { outputText } = ts.transpileModule(fs.readFileSync(filename, "utf8"), {
    fileName: filename,
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
    },
  });
  const loaded = new Module(filename, module);
  loaded.filename = filename;
  loaded.paths = Module._nodeModulePaths(path.dirname(filename));
  const originalRequire = loaded.require.bind(loaded);
  loaded.require = (request) => {
    const source = path.resolve(path.dirname(filename), `${request}.ts`);
    return request.startsWith(".") && fs.existsSync(source)
      ? loadTypeScript(path.relative(path.resolve(__dirname, ".."), source))
      : originalRequire(request);
  };
  loaded._compile(outputText, filename);
  return loaded.exports;
}

const { validateTask, validateState, validateAction } = loadTypeScript(
  "src/session-validation.ts",
);
const { initialState } = loadTypeScript("src/data.ts");
const fixture = () => initialState();
const taskFixture = () => fixture().tasks[0];

test("observed Codex identity and multi-GB resources survive both session validators", () => {
  const native = require("../electron/session-validation.cjs");
  const task = taskFixture();
  task.agents = [{
    id: "codex-child", name: "Inspecteur", role: "Inspecter", model: "",
    status: "running", progress: 0, summary: "Activité observée",
    origin: "codex", provider: "codex", providerThreadId: "child-thread",
    parentAgentId: "lead", activity: "Inspection en cours",
    resources: { cpu: 1, memoryMb: 4096 },
  }];
  for (const validate of [validateTask, native.validateTask]) {
    const live = validate(JSON.parse(JSON.stringify(task)), false);
    assert.equal(live.agents[0].providerThreadId, "child-thread");
    assert.equal(live.agents[0].parentAgentId, "lead");
    assert.equal(live.agents[0].origin, "codex");
    assert.equal(live.agents[0].model, "");
    assert.equal(live.agents[0].resources.memoryMb, 4096);
    const restored = validate(JSON.parse(JSON.stringify(task)), true);
    assert.equal(restored.agents[0].status, "blocked");
    assert.match(restored.agents[0].waitReason, /confirmer/);
    assert.throws(() => validate({ ...task, agents: [{ ...task.agents[0], origin: "invented" }] }), /invalide/);
    assert.throws(() => validate({ ...task, agents: [{ ...task.agents[0], resources: { cpu: 1025 } }] }), /invalide/);
  }
});

test("a real demo session survives portable JSON export and restoration", () => {
  const original = fixture();
  const exported = JSON.parse(
    JSON.stringify({
      format: "djinn-session",
      version: 1,
      task: original.tasks[0],
    }),
  );
  const imported = validateTask(exported.task);
  assert.equal(imported.demo, true);
  assert.equal(imported.title, original.tasks[0].title);
  assert.deepEqual(
    JSON.parse(JSON.stringify(imported.questions)),
    original.tasks[0].questions,
  );
  assert.deepEqual(
    JSON.parse(JSON.stringify(imported.events)),
    original.tasks[0].events,
  );
  assert.deepEqual(
    JSON.parse(JSON.stringify(imported.artifacts)),
    original.tasks[0].artifacts,
  );
  const restored = validateState(
    JSON.parse(JSON.stringify({ ...original, tasks: [imported] })),
  );
  assert.equal(restored.selectedId, imported.id);
  assert.deepEqual(restored.settings, original.settings);
  assert.deepEqual(restored.tasks[0], imported);
});

test("null nested agent, feedback, and event entries are rejected before rendering", () => {
  for (const field of ["agents", "feedback", "events"]) {
    const task = taskFixture();
    task[field] = [null];
    assert.throws(
      () => validateTask(JSON.parse(JSON.stringify(task))),
      /invalide/,
      field,
    );
  }
});

test("import rejects unusable project paths and malformed decision/support boundaries", () => {
  const attacks = [
    (task) => {
      task.project = { path: "/tmp/project" };
    },
    (task) => {
      task.project = "relative/project";
    },
    (task) => {
      task.project = "/tmp/project\0other";
    },
    (task) => {
      task.questions[0].options = [null];
    },
    (task) => {
      task.questions[0].options[0].label = { text: "Run" };
    },
    (task) => {
      task.artifacts[0].content = { nodes: [] };
    },
    (task) => {
      task.artifacts[0].type = "html";
    },
    (task) => {
      task.events[0].detail = ["command output"];
    },
  ];
  for (const attack of attacks) {
    const task = taskFixture();
    attack(task);
    assert.throws(() => validateTask(task), /invalide/);
  }
  // An empty project is a valid portable mission awaiting local folder selection.
  assert.equal(validateTask(taskFixture()).project, "");
});

test("malformed workspace preferences fail validation without modifying recoverable input", () => {
  for (const preferences of [
    null,
    [],
    { provider: "unknown" },
    { reduceMotion: "false" },
  ]) {
    const saved = { ...fixture(), settings: preferences };
    const before = JSON.stringify(saved);
    assert.throws(() => validateState(saved), /invalide/);
    assert.equal(JSON.stringify(saved), before);
  }
});

test("restoring an interrupted run removes stale process identity and queues active agents", () => {
  const task = taskFixture();
  task.status = "running";
  task.runId = "process-on-another-machine";
  task.runMode = "execute";
  task.agents[0].status = "running";
  task.agents[1].status = "done";
  const restored = validateTask(JSON.parse(JSON.stringify(task)));
  assert.equal(restored.status, "paused");
  assert.equal(restored.runId, undefined);
  assert.equal(restored.runMode, "execute");
  assert.equal(restored.agents[0].status, "queued");
  assert.equal(restored.agents[1].status, "done");
  assert.equal(task.runId, "process-on-another-machine");
  assert.equal(task.agents[0].status, "running");
});

test("human review approval remains optional, preserves valid timestamps, and rejects invalid evidence", () => {
  const task = taskFixture();
  task.phase = "delivery";
  assert.equal(validateTask(task).reviewApprovedAt, undefined);
  task.reviewApprovedAt = "2026-10-05T14:25:00.000Z";
  assert.equal(
    validateTask(JSON.parse(JSON.stringify(task))).reviewApprovedAt,
    task.reviewApprovedAt,
  );
  for (const invalidApproval of ["not-a-date", 1234, true]) {
    task.reviewApprovedAt = invalidApproval;
    assert.throws(() => validateTask(task), /invalide/);
  }
});

test("portable sessions preserve automatic stage validation and its completion evidence", () => {
  const task = taskFixture();
  const completedAt = "2026-10-06T08:15:00.000Z";
  task.steps[0] = {
    ...task.steps[0],
    validation: "automatic",
    status: "completed",
    completedAt,
    approvedAt: undefined,
    approvedBy: undefined,
  };
  task.activeStepId = task.steps[1].id;
  task.selectedStepId = task.activeStepId;
  const imported = validateTask(JSON.parse(JSON.stringify(task)), false);
  assert.equal(imported.steps[0].validation, "automatic");
  assert.equal(imported.steps[0].completedAt, completedAt);
  assert.equal(imported.steps[0].approvedBy, undefined);
  assert.throws(
    () => validateTask({ ...task, steps: [{ ...task.steps[0], validation: "robot" }, ...task.steps.slice(1)] }),
    /invalide/,
  );
  assert.throws(
    () => validateTask({ ...task, steps: [{ ...task.steps[0], completedAt: undefined }, ...task.steps.slice(1)] }),
    /invalide/,
  );
});

test("portable sessions preserve a scoped pending workflow amendment", () => {
  const task = taskFixture();
  task.workflowMode = "flexible";
  task.workflowAmendment = {
    stepId: task.activeStepId,
    runId: "run-amend",
    reason: "Adapter le suffixe.",
    steps: [
      {
        type: "implementation",
        title: "Construire",
        objective: "Appliquer le périmètre.",
        validation: "automatic",
      },
    ],
  };
  task.runId = "run-amend";
  const restored = validateTask(JSON.parse(JSON.stringify(task)), false);
  assert.equal(restored.workflowAmendment.stepId, task.activeStepId);
  assert.equal(restored.workflowAmendment.runId, "run-amend");
  assert.equal(restored.workflowAmendment.steps[0].validation, "automatic");
});

test("action restore preserves manual steps and never claims a stopped server is live", () => {
  const task = taskFixture();
  const time = "2026-10-05T19:00:00.000Z";
  task.actions = [
    {
      id: "preview",
      kind: "server",
      title: "Aperçu",
      status: "ready",
      url: "http://127.0.0.1:5173",
      script: "dev",
      createdAt: time,
      updatedAt: time,
    },
    {
      id: "review",
      kind: "manual",
      title: "Vérifier",
      status: "done",
      createdAt: time,
      updatedAt: time,
    },
  ];
  const restored = validateTask(task);
  assert.equal(restored.actions[0].status, "stopped");
  assert.equal(restored.actions[0].url, undefined);
  assert.equal(restored.actions[0].script, "dev");
  assert.equal(restored.actions[1].status, "done");
  assert.equal(validateAction(task.actions[0]).status, "ready");
});

test("actions reject unsafe links and invalid timestamps; interventions keep their actual dates", () => {
  const time = "2026-10-05T19:00:00.000Z";
  const action = {
    id: "link",
    kind: "link",
    title: "Ouvrir",
    status: "pending",
    createdAt: time,
    updatedAt: time,
  };
  for (const url of [
    "javascript:alert(1)",
    "file:///tmp/project",
    "https://user:password@example.com",
  ])
    assert.throws(() => validateAction({ ...action, url }), /invalide/);
  assert.throws(
    () => validateAction({ ...action, updatedAt: "tomorrow" }),
    /invalide/,
  );
  const task = taskFixture();
  task.events.push({
    id: "human-feedback",
    type: "review",
    title: "Retour",
    detail: "Un retour",
    time,
    actor: "human",
    interventionId: "feedback-1",
  });
  task.feedback = [
    {
      id: "feedback-1",
      artifactId: task.artifacts[0].id,
      text: "Un retour",
      x: 10,
      y: 20,
      resolved: false,
      createdAt: time,
    },
  ];
  const restored = validateTask(task);
  assert.equal(restored.events.at(-1).time, time);
  assert.equal(restored.events.at(-1).actor, "human");
  assert.equal(restored.feedback[0].createdAt, time);
});

test("portable sessions retain workflow policy, canonical supports, and flexible proposals", () => {
  const task = taskFixture();
  task.workflowMode = "flexible";
  task.steps = [
    {
      id: "discussion",
      type: "specification",
      title: "Spécification",
      objective: "Décrire le contrat",
      status: "pending",
      exitCriteria: [],
      expectedArtifacts: [],
      skills: [],
    },
  ];
  task.activeStepId = "discussion";
  task.selectedStepId = "discussion";
  task.questions = [];
  task.events = [];
  task.agents = [];
  task.feedback = [];
  task.artifacts = [
    {
      id: "context",
      title: "Contexte",
      type: "document",
      content: "Décision humaine",
      updatedAt: "2026-10-06T08:00:00.000Z",
      sourceOfTruth: true,
    },
  ];
  task.nextStepProposal = {
    type: "review",
    title: "Relire",
    objective: "Vérifier le contrat",
    reason: "La discussion est prête à être challengée",
    stepId: "discussion",
  };
  task.projectId = "project:%2Ftmp%2Fproject";
  task.project = "/tmp/project";
  task.projectSnapshot = {
    id: task.projectId,
    name: "Projet",
    directory: task.project,
    conventions: "",
    locations: {},
    workflows: [],
    workflowPolicy: "flexible",
    sourcesOfTruth: [
      {
        id: "context",
        title: "Contexte",
        path: "docs/CONTEXT.md",
        description: "Décision",
      },
    ],
    updatedAt: task.createdAt,
    capturedAt: task.createdAt,
  };
  const restored = validateTask(JSON.parse(JSON.stringify(task)), false);
  assert.equal(restored.workflowMode, "flexible");
  assert.equal(restored.artifacts[0].sourceOfTruth, true);
  assert.equal(restored.nextStepProposal.type, "review");
  assert.equal(restored.projectSnapshot.workflowPolicy, "flexible");
  assert.equal(
    restored.projectSnapshot.sourcesOfTruth[0].path,
    "docs/CONTEXT.md",
  );
  assert.throws(
    () => validateTask({ ...task, workflowMode: "invalid" }),
    /invalide/,
  );
  assert.throws(
    () =>
      validateTask({
        ...task,
        projectSnapshot: {
          ...task.projectSnapshot,
          workflowPolicy: "enforced",
        },
      }),
    /invalide/,
  );
});
