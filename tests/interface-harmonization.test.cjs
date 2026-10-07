"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const Module = require("node:module");
const test = require("node:test");
const React = require("react");
const { renderToStaticMarkup } = require("react-dom/server");
const ts = require("typescript");

const root = path.resolve(__dirname, "../src");
const originals = { ...Module._extensions };

const compile = (module, filename) => {
  const originalRequire = module.require.bind(module);
  module.require = (request) =>
    request === "./visuals"
      ? { Orb: () => null, Machine: () => null, Wave: () => null }
      : originalRequire(request);
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

test("the shared CSS contract keeps functional text at 12px", () => {
  const css = fs
    .readdirSync(root)
    .filter((entry) => entry.endsWith(".css"))
    .map((entry) => fs.readFileSync(path.join(root, entry), "utf8"))
    .join("\n");
  assert.match(css, /--font-size-body:\s*12px/);
  assert.doesNotMatch(css, /font-size\s*:\s*(?:[1-9]|1[01])px/);
  assert.doesNotMatch(css, /font\s*:\s*(?:[1-9]|1[01])px/);
});

test("new mission exposes a model choice and no extra indication field", () => {
  const { NewMission } = require(path.join(root, "app.tsx"));
  const project = {
    id: "project",
    name: "Produit",
    directory: "/tmp/product",
    preferences: { provider: "claude", model: "claude-model" },
  };
  const markup = renderToStaticMarkup(
    React.createElement(NewMission, {
      projects: [project],
      selectedProjectId: project.id,
      provider: "codex",
      model: "codex-model",
      onClose() {},
      onToast() {},
      onCreate() {},
    }),
  );

  assert.match(markup, /id="mission-provider"/);
  assert.match(markup, /aria-label="Choisir le modèle"/);
  assert.match(markup, /claude-model/);
  assert.doesNotMatch(markup, /Indications complémentaires/);
  assert.doesNotMatch(markup, /class="mission-extra"/);
  assert.match(
    fs.readFileSync(path.join(root, "app.tsx"), "utf8"),
    /selectedProvider,\s*\n\s*selectedModel/,
  );
});

test("new mission forwards the selected provider and model without indication state", () => {
  const appFile = path.join(root, "app.tsx");
  const originalExtensions = { ...Module._extensions };
  const originalReact = require("react");
  const slots = [];
  let hookIndex = 0;
  const hooks = {
    ...originalReact,
    useState(initial) {
      const slot = hookIndex++;
      if (!(slot in slots))
        slots[slot] = typeof initial === "function" ? initial() : initial;
      return [
        slots[slot],
        (value) => {
          slots[slot] =
            typeof value === "function" ? value(slots[slot]) : value;
        },
      ];
    },
  };
  const compileHarness = (module, filename) => {
    const originalRequire = module.require.bind(module);
    module.require = (request) => {
      if (filename === appFile && request === "react") return hooks;
      if (filename === appFile && request === "./model-picker")
        return {
          ModelPicker: ({ provider, value, onChange }) =>
            React.createElement("button", {
              type: "button",
              "aria-label": "Choisir le modèle",
              "data-provider": provider,
              "data-value": value,
              onClick: () => onChange(value),
            }),
        };
      if (request === "./visuals")
        return { Orb: () => null, Machine: () => null, Wave: () => null };
      return originalRequire(request);
    };
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
  const find = (node, predicate) => {
    if (!node || typeof node !== "object") return undefined;
    if (predicate(node)) return node;
    for (const child of [node.props?.children].flat(Infinity)) {
      const result = find(child, predicate);
      if (result) return result;
    }
    return undefined;
  };
  Module._extensions[".tsx"] = compileHarness;
  Module._extensions[".ts"] = compileHarness;
  Module._extensions[".css"] = () => {};
  delete require.cache[appFile];
  try {
    const { NewMission } = require(appFile);
    const calls = [];
    const project = {
      id: "project",
      name: "Produit",
      directory: "/tmp/product",
      preferences: { provider: "claude", model: "claude-model" },
    };
    const props = {
      projects: [project],
      selectedProjectId: project.id,
      provider: "codex",
      model: "codex-model",
      onClose() {},
      onToast() {},
      onCreate: (...args) => calls.push(args),
    };
    const render = () => {
      hookIndex = 0;
      return NewMission(props);
    };
    let tree = render();
    find(tree, (node) => node.props?.id === "mission-prompt").props.onChange({
      target: { value: "Construire une timeline" },
    });
    tree = render();
    find(tree, (node) => node.type === "select").props.onChange({
      target: { value: "codex" },
    });
    tree = render();
    find(tree, (node) => node.type === "form").props.onSubmit({
      preventDefault() {},
    });
    assert.equal(calls.length, 1);
    assert.equal(calls[0][3], "codex");
    assert.equal(calls[0][4], "");
    assert.equal(calls[0][5].indication, undefined);
  } finally {
    Object.assign(Module._extensions, originalExtensions);
    delete require.cache[appFile];
  }
});

test("mission composer keeps spring layout, focus, and reduced-motion contracts", () => {
  const app = fs.readFileSync(path.join(root, "app.tsx"), "utf8");
  assert.match(app, /layout\s*\n/);
  assert.match(app, /type:\s*"spring"/);
  assert.match(app, /requestAnimationFrame\(\(\) =>/);
  assert.match(app, /prefers-reduced-motion/);
});

test("settings exposes an explicit complete demo loader", () => {
  const app = fs.readFileSync(path.join(root, "app.tsx"), "utf8");
  assert.match(app, /Charger la démo complète/);
  assert.match(app, /demoLoader/);
  assert.match(app, /await demoLoader\(\)/);
});

test.after(() => {
  Object.assign(Module._extensions, originals);
  delete global.window;
});
