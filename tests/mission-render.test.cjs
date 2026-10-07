"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const ts = require("typescript");
const Module = require("node:module");
const React = require("react");
const { renderToStaticMarkup } = require("react-dom/server");
const root = require("node:path").resolve(__dirname, "../src");
const originals = { ...Module._extensions };
const compile = (module, filename) => {
  const originalRequire = module.require.bind(module);
  module.require = (id) =>
    id === "./visuals"
      ? {
          Orb: () => null,
          Machine: () => null,
          Wave: () => null,
          Signal: () => null,
          agentColor: () => "#aaa",
          agentOrbState: () => "connecting",
        }
      : originalRequire(id);
  module._compile(
    ts.transpileModule(fs.readFileSync(filename, "utf8"), {
      fileName: filename,
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2022,
        jsx: ts.JsxEmit.ReactJSX,
        esModuleInterop: true,
      },
    }).outputText,
    filename,
  );
};
Module._extensions[".tsx"] = compile;
Module._extensions[".ts"] = compile;
Module._extensions[".css"] = () => {};
global.window = {
  matchMedia: () => ({ matches: false }),
  addEventListener() {},
  removeEventListener() {},
};
global.document = { documentElement: { dataset: { motion: "reduced" } } };
const App = require(root + "/app.tsx").default;
const { StepTimeline } = require(root + "/step-timeline.tsx");
const { NextStepModal, NewMission, ProjectSourcesEditor } = require(
  root + "/app.tsx",
);
const { agentWaitSummary } = require(root + "/mission-panels.tsx");
test("free timeline exposes continuation only after the final result is validated", () => {
  const { taskFixture } = require("./workflow-fixture.cjs");
  const workflow = require("../electron/workflow.cjs");
  let task = {
    ...taskFixture(),
    workflowMode: "flexible",
    steps: [workflow.createDiscussionStep("free", "specification")],
  };
  task.activeStepId = task.selectedStepId = task.steps[0].id;
  const render = () =>
    renderToStaticMarkup(
      React.createElement(StepTimeline, {
        task,
        onSelect() {},
        onStart() {},
        onReopen() {},
        onConfigure() {},
        onAdd() {},
      }),
    );
  assert.doesNotMatch(render(), /Ajouter une étape/);
  task = workflow.startStep(task, task.activeStepId);
  task = workflow.finishStepRun(task, task.activeStepId, "completed");
  assert.doesNotMatch(render(), /Ajouter une étape/);
  task = workflow.approveStep(task, task.activeStepId, "human");
  assert.match(render(), /Ajouter une étape/);
  task.workflowMode = "fixed";
  assert.doesNotMatch(render(), /Ajouter une étape/);
});
test("continuation dialog uses the chief proposal and preserves the specification explanation", () => {
  const task = {
    activeStepId: "current",
    nextStepProposal: {
      stepId: "current",
      type: "specification",
      title: "Formaliser le besoin",
      objective: "Critères d’acceptation",
      reason: "Décisions produit disponibles",
    },
  };
  const markup = renderToStaticMarkup(
    React.createElement(NextStepModal, { task, onClose() {}, onAdd() {} }),
  );
  assert.match(markup, /value="specification" selected=""/);
  assert.match(markup, /Décisions produit disponibles/);
  assert.match(markup, /Elle ne déclenche pas de code ni de prototype/);
  assert.match(markup, /Ajouter à la timeline/);
  assert.doesNotMatch(markup, /Créer et démarrer/);
});
test("mission creation inherits project context with a model choice and one intention field", () => {
  const project = {
    id: "p",
    name: "Produit",
    directory: "/tmp/project",
    conventions: "Once",
    locations: {},
    workflows: [],
    updatedAt: "2026-10-06T08:00:00Z",
  };
  const props = {
    projects: [project],
    selectedProjectId: "p",
    provider: "codex",
    model: "",
    onClose() {},
    onCreate() {},
    onToast() {},
  };
  const markup = renderToStaticMarkup(React.createElement(NewMission, props));
  assert.match(markup, /Produit/);
  assert.match(markup, /Le modèle choisi prépare votre mission/);
  assert.match(markup, /id="mission-provider"/);
  assert.match(markup, /Choisir le modèle/);
  assert.match(markup, /class="mission-start-screen"/);
  assert.match(markup, /id="mission-prompt"/);
  assert.match(markup, /Préparer la mission/);
  assert.doesNotMatch(markup, /role="dialog"|aria-modal|Workflow à configurer/);
  assert.doesNotMatch(markup, /Indications complémentaires/);
  assert.doesNotMatch(
    markup,
    /Type de discussion|Dossier du projet|Nombre maximal de sous-agents|Conventions du projet/,
  );
  const empty = renderToStaticMarkup(
    React.createElement(NewMission, { ...props, projects: [] }),
  );
  assert.match(empty, /Créer un projet/);
  assert.doesNotMatch(empty, /textarea/);
  const sources = renderToStaticMarkup(
    React.createElement(ProjectSourcesEditor, {
      sources: [
        {
          id: "product",
          title: "Produit",
          path: "docs/produit.md",
          description: "Décisions acceptées",
        },
      ],
      onChange() {},
    }),
  );
  assert.match(sources, /Sources de vérité du projet/);
  assert.match(sources, /docs\/produit.md/);
});
test("sidebar displays empty projects, nested missions and project actions", () => {
  const { ProjectSidebar } = require(root + "/project-sidebar.tsx");
  const projects = [
    { id: "p", name: "Produit", directory: "/tmp/product" },
    { id: "e", name: "Vide", directory: "/tmp/empty" },
  ];
  const tasks = [
    { id: "t", projectId: "p", title: "Mission produit", status: "idle" },
    {
      id: "legacy",
      project: "/tmp/product",
      title: "Mission héritée",
      status: "waiting",
    },
    { id: "demo", title: "Démo", status: "idle", demo: true },
  ];
  const noop = () => {};
  const markup = renderToStaticMarkup(
    React.createElement(ProjectSidebar, {
      projects,
      tasks,
      selectedTaskId: "t",
      selectedProjectId: "p",
      collapsed: false,
      onSelectTask: noop,
      onSelectProject: noop,
      onNewProject: noop,
      onEditProject: noop,
      onNewMission: noop,
    }),
  );
  assert.match(markup, /Créer un projet/);
  assert.match(markup, /Réglages de Produit/);
  assert.match(markup, /Nouvelle mission dans Vide/);
  assert.match(markup, /Créer une première mission/);
  assert.ok(markup.indexOf("Mission produit") < markup.indexOf("Vide"));
  assert.match(markup, /Mission héritée/);
  assert.match(markup, /AUTRES MISSIONS/);
});
test("waiting cards show the native conflict and do not count the read-only chief as a queued worker", () => {
  const a = {
    id: "one",
    status: "queued",
    waitReason: "Périmètre partagé avec Two",
    waitingForAgentIds: ["two"],
  };
  const agents = [a, { id: "two", name: "Two", status: "running" }];
  assert.deepEqual(agentWaitSummary(a, agents, [], true), {
    title: "Périmètre partagé avec Two",
    detail: "Attend : Two",
  });
  assert.equal(
    agentWaitSummary({ id: "lead", status: "queued" }, agents, [], true),
    null,
  );
});
test("mission renders the global header, mission header, workflow, tabs and agents in that order", () => {
  const markup = renderToStaticMarkup(React.createElement(App));
  const sections = [
    markup.indexOf('class="topbar"'),
    markup.indexOf('class="hero'),
    markup.indexOf("step-timeline"),
    markup.indexOf('aria-label="Vues de la mission"'),
    markup.indexOf('aria-label="Agents de la mission"'),
  ];
  assert.ok(
    sections.every((index) => index >= 0),
    "all mission sections are rendered",
  );
  assert.ok(
    sections.every((index, i) => !i || index > sections[i - 1]),
    "mission sections follow the reading order",
  );
  assert.ok(
    markup.indexOf("step-timeline") <
      markup.indexOf('aria-label="Vues de la mission"'),
  );
  assert.match(markup, /aria-label="Étapes de la mission"/);
  assert.doesNotMatch(markup, /disabled=""[^>]*class="step-item[^\"]*pending/);
});
test("historical stage navigation retains the active stage and never starts a process during rendering", () => {
  const task = {
    activeStepId: "current",
    selectedStepId: "past",
    steps: [
      {
        id: "past",
        title: "Reflexion",
        status: "completed",
        type: "reflection",
      },
      {
        id: "current",
        title: "Implementation",
        status: "paused",
        type: "implementation",
      },
      { id: "future", title: "Review", status: "pending", type: "review" },
    ],
    questions: [],
    status: "paused",
  };
  const fail = () => assert.fail("Rendering must not dispatch an action");
  const markup = renderToStaticMarkup(
    React.createElement(StepTimeline, {
      task,
      onStart: fail,
      onSelect: fail,
      onReopen: fail,
      onConfigure: fail,
    }),
  );
  assert.doesNotMatch(markup, /step-context/);
  assert.match(markup, /aria-current="step"/);
  assert.match(markup, /Revenir à l’étape active/);
});
test.after(() => {
  Object.assign(Module._extensions, originals);
  delete global.window;
  delete global.document;
});

test("preparation screen keeps intent visible and exposes recovery and blocking questions", () => {
  const { MissionPreparation } = require(root + "/app.tsx");
  const { taskFixture } = require("./workflow-fixture.cjs");
  const task = {
    ...taskFixture(),
    provider: "codex",
    brief: "Une spécification sans code",
    status: "running",
    runId: "prepare",
  };
  const props = {
    task,
    onPause() {},
    onRetry() {},
    onAnswer() {},
    onSend: async () => true,
  };
  const render = () =>
    renderToStaticMarkup(React.createElement(MissionPreparation, props));
  assert.match(render(), /Djinn prépare votre timeline/);
  assert.match(render(), /Une spécification sans code/);
  assert.match(render(), /Mettre en pause/);
  assert.doesNotMatch(render(), /role="dialog"|aria-modal/);
  task.runId = undefined;
  task.status = "error";
  task.events.push({
    id: "error",
    type: "error",
    title: "Échec",
    detail: "Transport interrompu",
    time: "2026-10-06T12:00:00Z",
  });
  assert.match(render(), /Transport interrompu/);
  assert.match(render(), /Reprendre la préparation/);
  task.status = "waiting";
  task.questions = [
    {
      id: "scope",
      title: "Quel périmètre ?",
      context: "Le besoin comporte deux options",
      options: [{ label: "Produit", description: "La spec" }],
      blocking: true,
      unlocks: "La timeline",
    },
  ];
  assert.match(render(), /Quel périmètre/);
  assert.match(render(), /disabled=""[^>]*>.*Reprendre la préparation/s);
});

test("question sans options ouvre directement la réponse libre sans recommandation inventée", () => {
  const { QuestionCard } = require(root + "/mission-panels.tsx");
  const context = "Contexte complet à conserver, avec la contrainte finale.";
  const markup = renderToStaticMarkup(
    React.createElement(QuestionCard, {
      question: {
        id: "scope",
        title: "Quel périmètre ?",
        context,
        recommendation: "",
        options: [],
        blocking: true,
        unlocks: "La suite de la mission",
      },
      onAnswer() {},
      onReopen() {},
    }),
  );
  assert.match(markup, new RegExp(context));
  assert.match(markup, /aria-label="Votre réponse personnalisée"/);
  assert.match(markup, /Valider la réponse/);
  assert.doesNotMatch(markup, /Recommandé|Recommandation/);
});

test("StageReport expose clairement le fait, le reste et les preuves repliables", () => {
  const { StageReport } = require(root + "/mission-progress.tsx");
  const markup = renderToStaticMarkup(
    React.createElement(StageReport, {
      step: {
        id: "implementation",
        type: "implementation",
        title: "Implémentation",
        objective: "Livrer le changement",
        status: "awaiting_human",
        exitCriteria: [],
        expectedArtifacts: [],
        skills: [],
        report: {
          status: "needs_input",
          summary: "Le changement est prêt à vérifier.",
          completed: ["Le composant est rendu"],
          remaining: ["Vérifier le parcours"],
          evidence: ["tests/ui.log"],
          nextAction: "Testez le parcours principal.",
        },
      },
    }),
  );
  assert.match(markup, /Le changement est prêt à vérifier/);
  assert.match(markup, />Fait</);
  assert.match(markup, />Reste</);
  assert.match(markup, /Vérifier le parcours/);
  assert.match(markup, /Éléments de preuve/);
  assert.match(markup, /tests\/ui\.log/);
});

test("une étape courante en pause conserve son compte rendu hérité dans Mission", () => {
  const hook = require(root + "/use-djinn.ts");
  const original = hook.useDjinn;
  const { taskFixture } = require("./workflow-fixture.cjs");
  const step = { id: "legacy-review", type: "review", title: "Intégrer les corrections", objective: "Vérifier ensemble", status: "paused", validation: "human", summary: "La recette date reste à vérifier.", exitCriteria: [], expectedArtifacts: [], skills: [] };
  const task = taskFixture({ status: "paused", steps: [step], activeStepId: step.id, selectedStepId: step.id });
  hook.useDjinn = () => {
    const d = original();
    return { ...d, task, state: { ...d.state, tasks: [task], selectedId: task.id } };
  };
  try {
    const markup = renderToStaticMarkup(React.createElement(App));
    assert.match(markup, /Compte rendu de l’étape Intégrer les corrections/);
    assert.match(markup, /La recette date reste à vérifier/);
  } finally { hook.useDjinn = original; }
});

test("la section permissions disparaît quand il ne reste aucune demande en attente", () => {
  const { PermissionPanel } = require(root + "/permission-panel.tsx");
  const markup = renderToStaticMarkup(
    React.createElement(PermissionPanel, {
      requests: [
        {
          id: "permission-done",
          taskId: "task-1",
          runId: "run-1",
          agentId: "build",
          provider: "codex",
          method: "workspace.write",
          title: "Modifier le fichier",
          status: "accepted",
          createdAt: "2026-10-07T10:00:00Z",
          updatedAt: "2026-10-07T10:01:00Z",
          canAcceptForSession: false,
        },
      ],
      onRespond: async () => true,
    }),
  );
  assert.equal(markup, "");
});

test("la timeline affiche les work items et l’état d’autorisation sans réécrire l’état source", () => {
  const { Timeline } = require(root + "/temporal-timeline.tsx");
  const { taskFixture } = require("./workflow-fixture.cjs");
  const markup = renderToStaticMarkup(
    React.createElement(Timeline, {
      task: taskFixture({
        activeStepId: "step-1",
        selectedStepId: "step-1",
        agents: [
          {
            id: "build",
            name: "Build",
            role: "Worker",
            model: "test",
            status: "running",
            summary: "Travail en cours",
            progress: 40,
          },
          {
            id: "review",
            name: "Review",
            role: "Reviewer",
            model: "test",
            status: "running",
            summary: "Vérification",
            progress: 20,
          },
        ],
        workItems: [
          {
            id: "ticket-42",
            title: "Préparer le test WEB-42",
            status: "ready",
            ticket: "WEB-42",
            agentId: "build",
            updatedAt: "2026-10-07T10:00:00Z",
          },
          {
            id: "review-1",
            title: "Relire le résultat",
            status: "running",
            agentId: "review",
            updatedAt: "2026-10-07T10:00:00Z",
          },
        ],
        permissions: [
          {
            id: "permission-1",
            taskId: "task-1",
            runId: "run-1",
            agentId: "build",
            provider: "codex",
            method: "workspace.write",
            title: "Écrire dans le projet",
            status: "pending",
            createdAt: "2026-10-07T10:00:00Z",
            canAcceptForSession: false,
          },
        ],
      }),
      onAgent() {},
    }),
  );
  assert.match(markup, /Tâches de l’étape/);
  assert.match(markup, /Préparer le test WEB-42/);
  assert.match(markup, /En attente d’autorisation/);
  assert.match(markup, /Relire le résultat/);
  assert.match(markup, /En cours/);
});

test("un ancien tour dont le test est prêt reste ouvert indépendamment du tour courant", () => {
  const { ActionsPanel } = require(root + "/actions-panel.tsx");
  const base = {
    kind: "server",
    title: "Aperçu WEB-42",
    createdAt: "2026-10-07T10:00:00Z",
    updatedAt: "2026-10-07T10:00:00Z",
  };
  const markup = renderToStaticMarkup(
    React.createElement(ActionsPanel, {
      actions: [
        { ...base, id: "new", runId: "run-new", status: "pending" },
        {
          ...base,
          id: "old",
          runId: "run-old",
          status: "ready",
          url: "http://127.0.0.1:4317",
        },
      ],
      runOrder: ["run-old", "run-new"],
      onAction: async () => true,
    }),
  );
  const oldStart = markup.indexOf('data-turn-key="turn:run-old"');
  const oldTag = markup.slice(
    markup.lastIndexOf("<details", oldStart),
    markup.indexOf(">", oldStart) + 1,
  );
  assert.match(oldTag, /open=""/);
});
