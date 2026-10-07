"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const Module = require("node:module");
const path = require("node:path");
const test = require("node:test");
const React = require("react");
const { renderToStaticMarkup } = require("react-dom/server");
const typescript = require("typescript");

const root = path.resolve(__dirname, "../src");
const original = Module._extensions;
const compile = (module, filename) => {
  const originalRequire = module.require.bind(module);
  module.require = (id) => {
    if (id === "lucide-react")
      return new Proxy(
        {},
        {
          get: (_, name) => (props) => React.createElement("span", props, name),
        },
      );
    return originalRequire(id);
  };
  module._compile(
    typescript.transpileModule(fs.readFileSync(filename, "utf8"), {
      fileName: filename,
      compilerOptions: {
        module: typescript.ModuleKind.CommonJS,
        target: typescript.ScriptTarget.ES2022,
        jsx: typescript.JsxEmit.ReactJSX,
        esModuleInterop: true,
      },
    }).outputText,
    filename,
  );
};
Module._extensions[".tsx"] = compile;
Module._extensions[".ts"] = compile;
Module._extensions[".css"] = () => {};

const { MissionTestBoard } = require(`${root}/mission-test-board.tsx`);

test.after(() => Object.assign(Module._extensions, original));

test("mission test board shows correction, active testing, and unmatched legacy test", () => {
  const task = {
    id: "task-1",
    title: "Mission de test",
    questions: [],
    agents: [
      { id: "build", name: "Build", role: "Worker", status: "running" },
      { id: "review", name: "Review", role: "QA", status: "running" },
    ],
    workItems: [
      {
        id: "item-a",
        title: "Corriger le ticket A",
        ticket: "TICKET-A",
        status: "running",
        agentId: "build",
        updatedAt: "2026-10-07T12:00:00Z",
      },
      {
        id: "item-b",
        title: "Recette du ticket B",
        ticket: "TICKET-B",
        status: "ready",
        agentId: "review",
        updatedAt: "2026-10-07T11:00:00Z",
      },
    ],
    actions: [],
  };
  const markup = renderToStaticMarkup(
    React.createElement(MissionTestBoard, {
      task,
      actions: [
        {
          id: "action-a",
          kind: "server",
          title: "Test ticket A",
          target: "TICKET-A",
          status: "ready",
          createdAt: "2026-10-07T10:00:00Z",
          updatedAt: "2026-10-07T10:30:00Z",
          testResult: {
            status: "problem",
            detail: "Le bouton ne répond pas.",
            recordedAt: "2026-10-07T11:00:00Z",
          },
        },
        {
          id: "action-b",
          kind: "server",
          title: "Test ticket B",
          workItemId: "item-b",
          status: "ready",
          createdAt: "2026-10-07T11:00:00Z",
          updatedAt: "2026-10-07T11:05:00Z",
          testStartedAt: "2026-10-07T11:10:00Z",
        },
        {
          id: "legacy",
          kind: "server",
          title: "Aperçu historique",
          status: "pending",
          createdAt: "2026-10-07T09:00:00Z",
          updatedAt: "2026-10-07T09:00:00Z",
        },
      ],
      onAction() {},
    }),
  );
  assert.match(markup, /Corriger le ticket A/);
  assert.match(markup, /Correction en cours/);
  assert.match(markup, /Recette du ticket B/);
  assert.match(markup, /En cours de test/);
  assert.match(markup, /Aperçu historique/);
  assert.match(markup, /À préparer/);
});

test("une recette prête expose le callback de focus de la carte native", () => {
  const markup = renderToStaticMarkup(
    React.createElement(MissionTestBoard, {
      task: {
        id: "task-2",
        title: "Mission",
        questions: [],
        agents: [],
        workItems: [
          {
            id: "item-ready",
            title: "Tester la page",
            status: "ready",
            updatedAt: "2026-10-07T10:00:00Z",
          },
        ],
      },
      actions: [
        {
          id: "action-ready",
          kind: "server",
          title: "Serveur de test",
          workItemId: "item-ready",
          status: "ready",
          createdAt: "2026-10-07T10:00:00Z",
          updatedAt: "2026-10-07T10:00:00Z",
        },
      ],
      onAction() {},
    }),
  );
  assert.match(markup, /Prête à tester/);
  assert.match(markup, /Tester/);
  assert.match(markup, /aria-controls="action-action-ready"/);
});

test("une nouvelle version testable remplace le test et le retour de l'ancienne version", () => {
  for (const previous of [
    { testStartedAt: "2026-10-07T10:10:00Z" },
    { testResult: { status: "problem", recordedAt: "2026-10-07T11:10:00Z" } },
  ]) {
    const markup = renderToStaticMarkup(React.createElement(MissionTestBoard, {
      task: { id: "versions", questions: [], agents: [], workItems: [{ id: "ticket", title: "Ticket", status: "ready" }] },
      actions: [
        { id: "old", kind: "server", title: "Ancienne recette", workItemId: "ticket", status: "ready", createdAt: "2026-10-07T10:00:00Z", ...previous },
        { id: "new", kind: "server", title: "Correction", workItemId: "ticket", status: "ready", createdAt: "2026-10-07T12:00:00Z" },
      ],
      onAction() {},
    }));
    assert.match(markup, /Prête à tester/);
    assert.match(markup, /aria-controls="action-new"/);
    assert.doesNotMatch(markup, /En cours de test|Retour à traiter/);
  }
});

test("une recette manuelle est testable et peut être en cours sans serveur", () => {
  for (const started of [false, true]) {
    const markup = renderToStaticMarkup(React.createElement(MissionTestBoard, {
      task: { id: "manual", questions: [], agents: [], workItems: [] },
      actions: [{ id: "recipe", kind: "manual", title: "Contrôler le PDF", status: "pending", testInstructions: ["Exporter et lire le PDF"], ...(started ? { testStartedAt: "2026-10-07T12:00:00Z" } : {}) }],
      onAction() {},
    }));
    assert.match(markup, started ? /En cours de test/ : /Prête à tester/);
    if (!started) assert.match(markup, /aria-controls="action-recipe"/);
  }
});
