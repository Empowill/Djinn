"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const Module = require("node:module");
const ts = require("typescript");
const { taskFixture } = require("./workflow-fixture.cjs");
const workflow = require("../electron/workflow.cjs");

// Run the real orchestration hook with a small deterministic hook scheduler.
// Only React scheduling and the Electron bridge are fixtures; routing, event
// filtering, state validation, persistence and run construction remain real.
async function renderer(provider, options = {}) {
  const slots = [],
    effects = [],
    starts = [];
  let index = 0,
    dirty = true,
    output,
    receiver;
  const same = (a, b) =>
    a && b && a.length === b.length && a.every((v, i) => Object.is(v, b[i]));
  const hooks = {
    useState(initial) {
      const i = index++;
      if (!slots[i])
        slots[i] = {
          value: typeof initial === "function" ? initial() : initial,
        };
      return [
        slots[i].value,
        (value) => {
          const next =
            typeof value === "function" ? value(slots[i].value) : value;
          if (!Object.is(next, slots[i].value)) {
            slots[i].value = next;
            dirty = true;
          }
        },
      ];
    },
    useRef(value) {
      const i = index++;
      return (slots[i] ||= { current: value });
    },
    useCallback(callback, deps) {
      const i = index++;
      if (!same(slots[i]?.deps, deps)) slots[i] = { value: callback, deps };
      return slots[i].value;
    },
    useEffect(callback, deps) {
      const i = index++;
      if (!same(slots[i]?.deps, deps)) {
        const previous = slots[i];
        slots[i] = { deps, cleanup: previous?.cleanup };
        effects.push(() => {
          previous?.cleanup?.();
          slots[i].cleanup = callback();
        });
      }
    },
  };
  const original = Module._extensions[".ts"];
  const hookPath = path.resolve(__dirname, "../src/use-djinn.ts");
  Module._extensions[".ts"] = (module, filename) => {
    const requireOriginal = module.require.bind(module);
    module.require = (id) =>
      id === "react" && filename === hookPath ? hooks : requireOriginal(id);
    module._compile(
      ts.transpileModule(fs.readFileSync(filename, "utf8"), {
        compilerOptions: {
          module: ts.ModuleKind.CommonJS,
          target: ts.ScriptTarget.ES2022,
        },
      }).outputText,
      filename,
    );
  };
  delete require.cache[hookPath];
  const { useDjinn } = require(hookPath);
  const project = {
    id: "p",
    name: "Produit",
    directory: "/tmp/djinn-renderer-fixture",
    conventions: "Décisions",
    locations: {},
    workflows: [],
    preferences: { provider, model: "fixture", concurrency: 5 },
    updatedAt: "2026-10-06T08:00:00.000Z",
  };
  const task = taskFixture({
    id: "existing",
    project: project.directory,
    provider,
    steps: workflow.createDefaultSteps("existing"),
    activeStepId: workflow.createDefaultSteps("existing")[0].id,
    ...(options.task || {}),
  });
  let saved = {
    version: 2,
    projects: [project],
    tasks: [task, ...(options.tasks || [])],
    selectedId: task.id,
    settings: { provider, model: "fixture", reduceMotion: true, sound: false },
  };
  const emit = (event) =>
    receiver({ timestamp: new Date().toISOString(), ...event });
  global.document = { documentElement: { dataset: {} } };
  global.window = {
    dispatchEvent() {},
    djinn: {
      loadState: async () => saved,
      saveState: async (state) => {
        saved = structuredClone(state);
        return { saved: true };
      },
      getEnvironment: async () => ({
        platform: "fixture",
        appVersion: "0.1.8",
        providers: [
          {
            id: provider,
            name: provider,
            available: true,
            authenticated: true,
          },
        ],
      }),
      getActions: async () => options.actions || [],
      notifyQuestion: async () => ({ shown: false }),
      validateProject: async (value) => value,
      onEvent: (callback) => {
        receiver = callback;
        return () => {};
      },
      startRun: async (input) => {
        starts.push(input);
        const runId = "run-" + starts.length;
        emit({
          taskId: input.taskId,
          stepId: input.stepId,
          runId,
          type: "status",
          data: { status: "running" },
        });
        return { runId };
      },
    },
  };
  async function flush() {
    for (let i = 0; i < 16; i++) {
      if (dirty) {
        dirty = false;
        index = 0;
        output = useDjinn();
        while (effects.length) effects.shift()();
      }
      await new Promise((r) => setImmediate(r));
    }
  }
  await flush();
  return {
    starts,
    project,
    emit,
    flush,
    get output() {
      return output;
    },
    get saved() {
      return saved;
    },
    dispose() {
      slots.forEach((s) => s?.cleanup?.());
      Module._extensions[".ts"] = original;
      delete global.window;
      delete global.document;
      delete require.cache[hookPath];
    },
  };
}

test("real renderer automatically qualifies and starts the selected stage, rejecting late routing events for Claude and Codex", async () => {
  for (const provider of ["claude", "codex"]) {
    for (const type of ["specification", "implementation"]) {
      const h = await renderer(provider);
      try {
        h.output.addTask(
          "",
          "Résultat demandé",
          h.project.directory,
          provider,
          "fixture",
          {
            projectRecord: h.project,
            concurrency: 5,
            workflowMode: "flexible",
            indication: "Suivre les décisions",
          },
        );
        await h.flush();
        assert.equal(h.starts.length, 1, h.output.toast);
        const initial = h.starts[0];
        assert.equal(initial.step.type, "discussion");
        assert.equal(initial.mode, "plan");
        assert.deepEqual(initial.agents, []);
        assert.equal(initial.guidance[0].text, "Suivre les décisions");
        const scope = { taskId: initial.taskId, stepId: initial.stepId };
        h.emit({
          ...scope,
          runId: "pass-1",
          type: "discussion_type",
          data: {
            agentId: "lead",
            parentRunId: "run-1",
            scope: "agent",
            type,
            title: "Discussion adaptée",
            objective: "Le besoin",
            reason: "Intention explicite",
          },
        });
        h.emit({
          ...scope,
          runId: "run-1",
          type: "status",
          data: { status: "completed" },
        });
        await h.flush();
        assert.equal(
          h.starts.length,
          2,
          `${provider}/${type} launches the chosen discussion`,
        );
        assert.equal(
          h.starts[1].mode,
          type === "implementation" ? "execute" : "plan",
        );
        assert.equal(h.starts[1].step.type, type);
        assert.equal(h.starts[1].stepId, initial.stepId);
        assert.equal(h.starts[1].providerSessions, undefined);
        assert.equal(h.output.task.steps.length, 1);
        assert.equal(h.output.task.steps[0].approvedAt, undefined);
        h.emit({
          ...scope,
          runId: "run-1",
          type: "status",
          data: { status: "completed" },
        });
        h.emit({
          ...scope,
          runId: "pass-1",
          type: "error",
          data: {
            agentId: "lead",
            parentRunId: "run-1",
            scope: "agent",
            message: "Tardif",
          },
        });
        await h.flush();
        assert.equal(h.output.task.runId, "run-2");
        assert.ok(!h.output.task.events.some((e) => e.detail === "Tardif"));
      } finally {
        h.dispose();
      }
    }
  }
});

test("a blocking routing question keeps the placeholder until the human answers", async () => {
  for (const provider of ["claude", "codex"]) {
    const h = await renderer(provider);
    try {
      h.output.addTask(
        "",
        "Besoin ambigu",
        h.project.directory,
        provider,
        "fixture",
        { projectRecord: h.project, workflowMode: "flexible" },
      );
      await h.flush();
      const initial = h.starts[0],
        scope = { taskId: initial.taskId, stepId: initial.stepId };
      h.emit({
        ...scope,
        runId: "pass-1",
        type: "question",
        data: {
          agentId: "lead",
          parentRunId: "run-1",
          scope: "agent",
          id: "clarify",
          title: "Spec ou code ?",
          blocking: true,
          options: [],
        },
      });
      h.emit({
        ...scope,
        runId: "run-1",
        type: "status",
        data: { status: "completed" },
      });
      await h.flush();
      assert.equal(h.starts.length, 1);
      assert.equal(h.output.task.steps[0].type, "discussion");
      h.output.answer("clarify", "Seulement la spécification");
      await h.flush();
      assert.equal(h.starts.length, 2);
      assert.equal(h.starts[1].step.type, "discussion");
      h.emit({
        ...scope,
        runId: "pass-2",
        type: "discussion_type",
        data: {
          agentId: "lead",
          parentRunId: "run-2",
          scope: "agent",
          type: "specification",
          title: "Spécification",
          objective: "Le besoin",
          reason: "Réponse humaine",
        },
      });
      h.emit({
        ...scope,
        runId: "run-2",
        type: "status",
        data: { status: "completed" },
      });
      await h.flush();
      assert.equal(h.starts.length, 3);
      assert.equal(h.starts[2].step.type, "specification");
      assert.equal(h.starts[2].mode, "plan");
    } finally {
      h.dispose();
    }
  }
});

test("a fresh mission asks the agent for the complete timeline before starting its first stage", async () => {
  for (const provider of ["claude", "codex"]) {
    const h = await renderer(provider);
    try {
      h.project.workflowPolicy = "enforced";
      h.project.workflows = [
        {
          id: "project-default",
          title: "Workflow hérité",
          steps: workflow.createDefaultSteps("project"),
        },
      ];
      h.output.addTask(
        "",
        "Définir le résultat demandé",
        h.project.directory,
        provider,
        "fixture",
        {
          projectRecord: h.project,
          concurrency: 5,
          workflowMode: "flexible",
          autoWorkflow: true,
        },
      );
      await h.flush();
      assert.equal(h.starts.length, 1);
      assert.equal(h.starts[0].workflowOrigin, "agent");
      assert.equal(h.starts[0].step.type, "discussion");
      assert.equal(h.starts[0].mode, "plan");
      assert.match(h.starts[0].prompt, /workflow_defined/);
      assert.doesNotMatch(h.starts[0].prompt, /emit discussion_type/);
      assert.doesNotMatch(h.starts[0].prompt, /choose exactly one/);
      assert.equal(h.starts[0].projectSnapshot.workflowPolicy, "flexible");
      assert.deepEqual(h.starts[0].projectSnapshot.workflows, []);
      assert.equal(h.saved.projects.at(-1).workflowPolicy, "enforced");
      assert.equal(h.saved.projects.at(-1).workflows.length, 1);

      const scope = { taskId: h.starts[0].taskId, stepId: h.starts[0].stepId };
      h.emit({
        ...scope,
        runId: "run-1",
        type: "workflow_defined",
        data: {
          agentId: "lead",
          steps: [
            {
              type: "specification",
              title: "Cadrer",
              objective: "Décrire le résultat",
            },
            {
              type: "implementation",
              title: "Construire",
              objective: "Appliquer la décision",
            },
            {
              type: "review",
              title: "Vérifier",
              objective: "Contrôler les preuves",
            },
          ],
          reason:
            "Une spécification précède l'implémentation et sa vérification.",
        },
      });
      h.emit({
        ...scope,
        runId: "run-1",
        type: "status",
        data: { status: "completed" },
      });
      await h.flush();
      assert.equal(h.starts.length, 2);
      assert.deepEqual(
        h.output.task.steps.map((step) => step.type),
        ["specification", "implementation", "review"],
      );
      assert.equal(h.starts[1].step.type, "specification");
      assert.equal(h.starts[1].mode, "plan");
      assert.equal(h.starts[1].providerSessions, undefined);
      assert.equal(h.output.task.workflowProposal.steps.length, 3);
    } finally {
      h.dispose();
    }
  }
});

test("a cancelled or blocked qualification clears stale proposals before it can resume", async () => {
  const h = await renderer("codex");
  try {
    h.output.addTask(
      "",
      "Choisir le bon parcours",
      h.project.directory,
      "codex",
      "fixture",
      {
        projectRecord: h.project,
        workflowMode: "flexible",
        autoWorkflow: true,
      },
    );
    await h.flush();
    const first = h.starts[0];
    const scope = { taskId: first.taskId, stepId: first.stepId };
    h.emit({
      ...scope,
      runId: "run-1",
      type: "workflow_defined",
      data: {
        agentId: "lead",
        reason: "Première hypothèse",
        steps: [
          {
            type: "specification",
            title: "Ancienne",
            objective: "À remplacer",
          },
        ],
      },
    });
    await h.flush();
    assert.ok(h.output.task.initialWorkflowProposal);
    h.emit({
      ...scope,
      runId: "run-1",
      type: "status",
      data: { status: "cancelled" },
    });
    await h.flush();
    assert.ok(h.output.task.initialWorkflowProposal);
    await h.output.start();
    await h.flush();
    assert.equal(h.starts.length, 2);
    assert.equal(h.output.task.initialWorkflowProposal, undefined);
    h.emit({
      ...scope,
      runId: "run-2",
      type: "workflow_defined",
      data: {
        agentId: "lead",
        reason: "Nouvelle hypothèse",
        steps: [
          {
            type: "reflection",
            title: "Nouvelle",
            objective: "Repartir du besoin",
          },
          {
            type: "specification",
            title: "Formaliser",
            objective: "Écrire le résultat",
          },
        ],
      },
    });
    h.emit({
      ...scope,
      runId: "run-2",
      type: "status",
      data: { status: "completed" },
    });
    await h.flush();
    assert.equal(h.starts.length, 3);
    assert.equal(h.output.task.steps[0].title, "Nouvelle");
    assert.equal(h.output.task.workflowProposal.reason, "Nouvelle hypothèse");
  } finally {
    h.dispose();
  }
});

test("une question bloquante pendant la qualification reprend la même discussion avant de définir la timeline", async () => {
  const h = await renderer("claude");
  try {
    h.output.addTask(
      "",
      "Clarifier une intention ambiguë",
      h.project.directory,
      "claude",
      "fixture",
      {
        projectRecord: h.project,
        workflowMode: "flexible",
        autoWorkflow: true,
      },
    );
    await h.flush();
    const first = h.starts[0];
    const scope = { taskId: first.taskId, stepId: first.stepId };
    h.emit({
      ...scope,
      runId: "run-1",
      type: "question",
      data: {
        agentId: "lead",
        id: "clarify-new",
        title: "Quel résultat ?",
        options: [],
      },
    });
    h.emit({
      ...scope,
      runId: "run-1",
      type: "status",
      data: { status: "completed" },
    });
    await h.flush();
    assert.equal(h.starts.length, 1);
    assert.equal(h.output.task.initialWorkflowProposal, undefined);
    h.output.answer("clarify-new", "Une spécification suffit");
    await h.flush();
    assert.equal(h.starts.length, 2);
    assert.equal(h.starts[1].step.type, "discussion");
    h.emit({
      ...scope,
      runId: "run-2",
      type: "workflow_defined",
      data: {
        agentId: "lead",
        reason: "Le besoin est documentaire.",
        steps: [
          {
            type: "specification",
            title: "Spécifier",
            objective: "Décrire le résultat",
          },
        ],
      },
    });
    h.emit({
      ...scope,
      runId: "run-2",
      type: "status",
      data: { status: "completed" },
    });
    await h.flush();
    assert.equal(h.starts.length, 3);
    assert.equal(h.output.task.steps[0].type, "specification");
  } finally {
    h.dispose();
  }
});

test("renderer merges observed Codex child updates without inheriting model or relaunching it", async () => {
  const h = await renderer("codex");
  try {
    const task = h.output.task;
    const envelope = { taskId: task.id, stepId: task.activeStepId, runId: "observed-run", type: "agent" };
    h.emit({ ...envelope, eventId: "observed-start", data: {
      id: "codex-child", name: "Inspecteur", role: "Inspection", origin: "codex",
      provider: "codex", providerThreadId: "child-thread", parentAgentId: "lead",
      status: "running", activity: "Inspection", prompt: "Must never be launched twice",
    } });
    await h.flush();
    h.emit({ ...envelope, eventId: "observed-update", data: { id: "codex-child", status: "queued", summary: "En attente d’un tour fournisseur" } });
    await h.flush();
    const cards = h.output.task.agents.filter(a => a.id === "codex-child");
    assert.equal(cards.length, 1);
    assert.equal(cards[0].origin, "codex");
    assert.equal(cards[0].providerThreadId, "child-thread");
    assert.equal(cards[0].parentAgentId, "lead");
    assert.equal(cards[0].model, "");
    await h.output.start(undefined, task.id, task.activeStepId);
    await h.flush();
    assert.equal(h.starts.length, 1);
    assert.equal(h.starts[0].agents.some(a => a.id === "codex-child"), false);
  } finally { h.dispose(); }
});

test("shareable harmonisation demo imports through the actual renderer without launching providers", async () => {
  const h = await renderer("codex");
  try {
    const payload = fs.readFileSync(path.resolve(__dirname, "../docs/v0.2.1-session-harmonisation.djinn.json"), "utf8");
    const before = h.output.state.tasks.length;
    await h.output.importTask({ size: Buffer.byteLength(payload), text: async () => payload });
    await h.flush();
    assert.equal(h.output.state.tasks.length, before + 1);
    assert.equal(h.output.task.demo, true);
    assert.equal(h.output.task.artifacts.length, 8);
    assert.equal(h.starts.length, 0);
  } finally { h.dispose(); }
});

test("lead-pass reports bind to the root run and needs_input survives completion", async () => {
  const h = await renderer("codex");
  try {
    await h.output.start();
    await h.flush();
    const input = h.starts[0];
    const scope = { taskId: input.taskId, stepId: input.stepId };
    h.emit({ ...scope, runId: "chief-pass", type: "step_result", data: {
      agentId: "lead", parentRunId: "run-1", scope: "agent", status: "needs_input",
      summary: "Le périmètre PDF reste à arbitrer.", completed: ["Entretien exploré"], remaining: ["Décision sur le PDF"], evidence: ["Lecture du modèle"], nextAction: "Répondre à la question PDF",
    }});
    h.emit({ ...scope, runId: "run-1", type: "status", data: { status: "completed" } });
    await h.flush();
    assert.equal(h.output.task.stepResult.runId, "run-1");
    assert.equal(h.output.task.stepResult.status, "needs_input");
    const step = h.output.task.steps.find((step) => step.id === input.stepId);
    assert.equal(step.status, "blocked");
    assert.deepEqual(step.report.completed, ["Entretien exploré"]);
    assert.deepEqual(step.report.remaining, ["Décision sur le PDF"]);
    assert.equal(h.starts.length, 1, "a business decision cannot be bypassed");
  } finally { h.dispose(); }
});

test("structured contributions preserve questions, tasks and independent recipes during the root passage", async () => {
  const h = await renderer("codex");
  try {
    await h.output.start();
    await h.flush();
    const input = h.starts[0], scope = { taskId: input.taskId, stepId: input.stepId };
    const child = { agentId: "ticket-a", scope: "agent", parentRunId: "run-1" };
    h.emit({ ...scope, runId: "worker-pass", type: "question", data: { ...child, id: "pdf", title: "Quel PDF ?", question: "Faut-il modifier le PDF exporté ou seulement la page ?", options: ["Les deux", "La page"], blocking: false } });
    h.emit({ ...scope, runId: "worker-pass", type: "work_item", data: { ...child, id: "ticket-a", title: "Recette participant", ticket: "ET-3083", status: "ready", branch: "codex/ET-3083", worktree: ".worktrees/ticket-a" } });
    h.emit({ ...scope, runId: "worker-pass", type: "report", data: { ...child, id: "report-a", status: "ready", summary: "Ticket prêt à tester", completed: ["Compteur corrigé"], remaining: ["Recette humaine"], evidence: ["Jest ciblé PASS"] } });
    h.emit({ ...scope, runId: "worker-pass", type: "action", data: { id: "test-a", agentId: "ticket-a", workItemId: "ticket-a", target: "ET-3083", kind: "server", title: "Tester ET-3083", status: "ready", stepId: input.stepId, runId: "worker-pass", url: "http://localhost:3000", createdAt: new Date().toISOString(), updatedAt: new Date().toISOString(), testInstructions: ["Cliquer l’alerte"], expectedResult: "Chaque participant n’est compté qu’une fois" } });
    await h.flush();
    assert.equal(h.output.task.runId, "run-1", "sibling work continues");
    assert.equal(h.output.task.questions[0].context, "Faut-il modifier le PDF exporté ou seulement la page ?");
    assert.equal(h.output.task.questions[0].options[0].label, "Les deux");
    assert.equal(h.output.task.status, "running", "a nonblocking question preserves velocity");
    assert.equal(h.output.task.workItems[0].status, "ready");
    assert.equal(h.output.task.reports[0].evidence[0], "Jest ciblé PASS");
    assert.equal(h.output.task.actions[0].workItemId, "ticket-a");
    assert.equal(await h.output.recordTestResult(h.output.task.actions[0], "passed"), true);
    await h.flush();
    assert.equal(h.output.task.actions[0].testResult.status, "passed");
    assert.equal(h.output.task.runId, "run-1", "recipe result does not complete the running mission");
    h.emit({ ...scope, runId: "spoof", type: "step_result", data: { ...child, status: "ready", summary: "Worker result is not a lead result" } });
    await h.flush();
    assert.equal(h.output.task.stepResult, undefined);
  } finally { h.dispose(); }
});

test('recipe progress and human outcomes survive reload when the native server confirms the same version',async()=>{
 const time=new Date().toISOString();
 const action={id:'test-version',kind:'server',title:'Tester',status:'ready',url:'http://localhost:3000',runId:'recipe-run',createdAt:time,updatedAt:time};
 const h=await renderer('codex',{task:{actions:[{...action,testStartedAt:time,testResult:{status:'deferred',recordedAt:time}}]},actions:[action]});
 try {
  assert.equal(h.output.task.actions[0].status,'ready');
  assert.equal(h.output.task.actions[0].testStartedAt,time);
  assert.equal(h.output.task.actions[0].testResult.status,'deferred');
 }finally{h.dispose();}
});

test('manual recipe starts and records feedback without completing the mission',async()=>{
 const action={id:'manual-recipe',kind:'manual',title:'Contrôler le PDF',status:'pending',testInstructions:['Exporter le PDF'],createdAt:new Date().toISOString(),updatedAt:new Date().toISOString()};
 const h=await renderer('codex',{task:{actions:[action]},actions:[action]});
 try {
  await h.output.startTest(h.output.task.actions[0]);
  await h.flush();
  assert.ok(h.output.task.actions[0].testStartedAt);
  assert.equal(await h.output.recordTestResult(h.output.task.actions[0],'passed'),true);
  await h.flush();
  assert.equal(h.output.task.actions[0].testStartedAt,undefined);
  assert.equal(h.output.task.actions[0].testResult.status,'passed');
  assert.equal(h.output.task.steps[0].approvedAt,undefined);
 }finally{h.dispose();}
});

test('permission notification selection survives synchronous focus of the previous mission',async()=>{
 const steps=workflow.createDefaultSteps('other');
 steps[0]={...steps[0],validation:'human',status:'completed',approvedBy:'human',approvedAt:new Date().toISOString()};
 const other=taskFixture({id:'other',project:'/tmp/djinn-renderer-fixture',steps,activeStepId:steps[1].id,selectedStepId:steps[0].id});
 const h=await renderer('codex',{tasks:[other]});
 try {
  global.window.dispatchEvent=(event)=>{if(event.type==='djinn:permission-focus')h.output.selectStep(h.output.task.activeStepId);};
  h.emit({taskId:'other',runId:'',type:'notification_clicked',data:{questionId:'permission:needed'}});
  await h.flush();
  assert.equal(h.output.state.selectedId,'other');
  assert.equal(h.output.task.selectedStepId,steps[1].id);
 }finally{h.dispose();}
});
