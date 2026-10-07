"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const ts = require("typescript");
const Module = require("node:module");
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

const { ProjectSettings } = require(root + "/project-settings.tsx");
const project = {
  id: "project:test",
  name: "Djinn",
  directory: "/tmp/djinn",
  conventions: "# Conventions",
  locations: { prototype: "app/prototypes" },
  sourcesOfTruth: [
    {
      id: "context",
      title: "Contexte produit",
      path: "docs/CONTEXT.md",
      description: "Décisions acceptées",
    },
  ],
  workflows: [],
  preferences: { provider: "codex", model: "", concurrency: 4 },
  workflowPolicy: "enforced",
  updatedAt: "2026-10-06T12:00:00.000Z",
};

test("project settings render project fields and sources without workflow controls", () => {
  const markup = renderToStaticMarkup(
    React.createElement(ProjectSettings, {
      project,
      onClose() {},
      onSave() {},
      onToast() {},
      defaultProvider: "codex",
      defaultModel: "",
    }),
  );
  assert.match(markup, /aria-label="Paramètres du projet"/);
  assert.match(markup, /Nom du projet/);
  assert.match(markup, /Conventions du projet/);
  assert.match(markup, /Emplacements relatifs \(optionnel\)/);
  assert.match(markup, /Sources de vérité/);
  assert.match(markup, /docs\/CONTEXT\.md/);
  assert.match(markup, /Fournisseur/);
  assert.doesNotMatch(markup, /Workflows du projet/);
  assert.doesNotMatch(markup, /Importer JSON/);
  assert.doesNotMatch(markup, /Modifier le workflow/);
  assert.doesNotMatch(markup, /Type de discussion/);
  assert.doesNotMatch(markup, /L’intention/);
});

test("project creation keeps the form focused on reusable project configuration", () => {
  const markup = renderToStaticMarkup(
    React.createElement(ProjectSettings, {
      onClose() {},
      onSave() {},
      onToast() {},
      defaultProvider: "claude",
      defaultModel: "claude-opus-5",
    }),
  );
  assert.match(markup, /aria-label="Créer un projet"/);
  assert.match(markup, /Créer le projet/);
  assert.match(markup, /Mon projet/);
  assert.doesNotMatch(markup, /Type de discussion/);
  assert.doesNotMatch(markup, /Créer et démarrer/);
});

test.after(() => {
  Object.assign(Module._extensions, originals);
  delete global.window;
  delete global.document;
});
