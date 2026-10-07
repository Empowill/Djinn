"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const Module = require("node:module");
const ts = require("typescript");
const React = require("react");
const { renderToStaticMarkup } = require("react-dom/server");

const root = path.resolve(__dirname, "../src");
const originals = { ...Module._extensions };
const compile = (module, filename) => {
  const originalRequire = module.require.bind(module);
  module.require = (id) => originalRequire(id);
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

const { ProjectSetup, ProjectSetupReport } = require(
  root + "/project-setup.tsx",
);

test("project setup keeps the first view small and exposes scan controls", () => {
  const markup = renderToStaticMarkup(
    React.createElement(ProjectSetup, {
      onClose() {},
      onSave() {},
      onToast() {},
      defaultProvider: "codex",
      defaultModel: "",
    }),
  );
  assert.match(markup, /aria-label="Créer un projet"/);
  assert.match(markup, /Nom du projet/);
  assert.match(markup, /Dossier du projet/);
  assert.match(markup, /Analyser le projet/);
  assert.match(markup, /Réglages avancés/);
  assert.doesNotMatch(markup, /Lecture du dépôt et analyse avec/);
});

test("discovery reports show a compact knowledge summary and references", () => {
  const report = {
    directory: "/tmp/project",
    name: "Projet",
    scannedAt: "2026-10-06T12:00:00.000Z",
    filesScanned: 42,
    historyCount: 8,
    summary: "Le dépôt contient des conventions et des références utiles.",
    evidence: [
      { path: "AGENTS.md", kind: "instructions", excerpt: "Règles du dépôt" },
      {
        path: "scripts/check.mjs",
        kind: "automation",
        excerpt: "Vérification",
      },
      { path: ".agents/skills/review", kind: "skill", excerpt: "Review" },
      { path: "app/prototypes", kind: "prototype", excerpt: "Prototype" },
    ],
    workflows: [],
    sourcesOfTruth: [
      {
        id: "source",
        title: "Contexte",
        path: "docs/CONTEXT.md",
        description: "",
      },
    ],
    locations: { prototype: "app/prototypes" },
    conventions: "Commits courts",
    notes: [],
    analysis: {
      provider: "codex",
      status: "agent",
      detail: "Analyse terminée",
    },
  };
  const markup = renderToStaticMarkup(
    React.createElement(ProjectSetupReport, {
      report,
      selectedSources: new Set(["source"]),
      includeConventions: false,
      onSourceSelection() {},
      onIncludeConventions() {},
      onApply() {},
    }),
  );
  assert.match(markup, /Rapport de découverte/);
  assert.match(markup, /42/);
  assert.match(markup, /Analyse terminée/);
  assert.match(markup, /Connaissances/);
  assert.match(markup, /Scripts/);
  assert.match(markup, /Skills/);
  assert.match(markup, /Références/);
  assert.doesNotMatch(markup, /Workflow/);
  assert.match(markup, /Appliquer les suggestions/);
});

test("fallback reports are labelled as local discovery", () => {
  const report = {
    directory: "/tmp/project",
    name: "Projet",
    scannedAt: "2026-10-06T12:00:00.000Z",
    filesScanned: 0,
    historyCount: 0,
    summary: "Repérage local",
    evidence: [],
    workflows: [],
    sourcesOfTruth: [],
    locations: {},
    conventions: "",
    notes: ["Pont indisponible"],
    analysis: {
      provider: "codex",
      status: "fallback",
      detail: "Repérage local",
    },
  };
  const markup = renderToStaticMarkup(
    React.createElement(ProjectSetupReport, {
      report,
      selectedSources: new Set(),
      includeConventions: false,
      onSourceSelection() {},
      onIncludeConventions() {},
      onApply() {},
    }),
  );
  assert.match(markup, /Repérage local/);
  assert.doesNotMatch(markup, /Analyse Codex/);
});

test.after(() => {
  Object.assign(Module._extensions, originals);
  delete global.window;
  delete global.document;
});
