"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");

const runtime = require("../electron/runtime.cjs");
const workflow = require("../electron/workflow.cjs");
const { taskFixture } = require("./workflow-fixture.cjs");

const definition = {
  reason:
    "Le besoin demande d’abord de cadrer le résultat, puis de livrer une preuve vérifiée.",
  steps: [
    {
      type: "specification",
      title: "Cadrer le résultat",
      objective: "Rendre les décisions et critères de réussite explicites.",
    },
    {
      type: "implementation",
      title: "Construire la solution",
      objective: "Appliquer la spécification dans le projet.",
    },
    {
      type: "review",
      title: "Vérifier le résultat",
      objective: "Contrôler le comportement et les preuves.",
    },
    {
      type: "delivery",
      title: "Préparer la livraison",
      objective: "Rendre le résultat local directement testable.",
    },
  ],
};

test("workflow_defined est borné et rejette les étapes de discussion", () => {
  const normalized = runtime.validateProtocolData(
    "workflow_defined",
    definition,
  );
  assert.equal(normalized.steps.length, 4);
  assert.deepEqual(normalized.steps[0].exitCriteria, []);
  assert.throws(
    () =>
      runtime.validateProtocolData("workflow_defined", {
        ...definition,
        steps: [{ type: "discussion", title: "Non", objective: "Non" }],
      }),
    /Unknown workflow_defined type/,
  );
  assert.throws(
    () =>
      runtime.validateProtocolData("workflow_defined", {
        ...definition,
        steps: Array.from({ length: 9 }, () => definition.steps[0]),
      }),
    /between 1 and 8/,
  );
});

test("le routage complet est réservé au lead agent et bloque les sorties de qualification", () => {
  const run = {
    kind: "lead-pass",
    agentId: "lead",
    parent: { kind: "lead" },
    runId: "run-1",
    stepId: "task-1:discussion",
    stepType: "discussion",
    workflowMode: "flexible",
    workflowOrigin: "agent",
  };
  const accepted = runtime.scopeProviderEvent(run, {
    type: "workflow_defined",
    data: definition,
  });
  assert.equal(accepted.type, "workflow_defined");
  assert.equal(accepted.data.stepId, run.stepId);
  assert.equal(accepted.data.runId, run.runId);
  assert.equal(
    runtime.scopeProviderEvent(run, {
      type: "discussion_type",
      data: {
        type: "specification",
        title: "Legacy",
        objective: "x",
        reason: "x",
      },
    }),
    null,
  );
  assert.equal(
    runtime.scopeProviderEvent(run, {
      type: "artifact",
      data: {
        id: "premature",
        title: "Trop tôt",
        type: "document",
        content: "x",
      },
    }),
    null,
  );
  assert.equal(
    runtime.scopeProviderEvent(
      { ...run, kind: "agent", agentId: "worker" },
      { type: "workflow_defined", data: definition },
    ),
    null,
  );
});

test("la timeline acceptée remplace le slot de qualification et conserve sa raison", () => {
  const initial = workflow.createDiscussionStep("task-1", "discussion");
  const task = taskFixture({
    workflowMode: "flexible",
    workflowOrigin: "agent",
    steps: [initial],
    activeStepId: initial.id,
    selectedStepId: initial.id,
  });
  const proposal = workflow.validateWorkflowProposal(
    { ...definition, stepId: initial.id },
    true,
  );
  const applied = workflow.applyWorkflowProposal(task, {
    ...proposal,
    stepId: initial.id,
  });
  assert.deepEqual(
    applied.steps.map((step) => step.type),
    ["specification", "implementation", "review", "delivery"],
  );
  assert.equal(applied.steps[0].id, initial.id);
  assert.equal(applied.steps[0].status, "pending");
  assert.equal(applied.steps[1].id, "task-1:workflow:2");
  assert.equal(applied.workflowProposal.reason, definition.reason);
  assert.equal(applied.initialWorkflowProposal, undefined);
  assert.equal(workflow.validateTaskWorkflow(applied).steps.length, 4);
});

test("les deux harnesses reçoivent l’origine de workflow et le contexte de qualification", () => {
  const step = workflow.createDiscussionStep("task-1", "discussion");
  const input = runtime.validateRunInput({
    taskId: "task-1",
    provider: "codex",
    cwd: "/tmp/project",
    prompt: "Qualifier",
    mode: "plan",
    stepId: step.id,
    step,
    workflowMode: "flexible",
    workflowOrigin: "agent",
    initialWorkflowProposal: { ...definition, stepId: step.id },
  });
  assert.equal(input.workflowOrigin, "agent");
  assert.equal(input.initialWorkflowProposal.steps.length, 4);
  assert.match(
    runtime.composeRunPrompt({ ...input, initialWorkflowProposal: undefined }),
    /workflow_defined/,
  );
});
