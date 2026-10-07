"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const Module = require("node:module");
const ts = require("typescript");

const filename = path.resolve(__dirname, "../src/project-setup.tsx");

function loadProjectSetup(hooks) {
  const original = { ...Module._extensions };
  const compile = (module, source) => {
    const requireOriginal = module.require.bind(module);
    module.require = (request) =>
      request === "react" && source === filename
        ? hooks
        : requireOriginal(request);
    module._compile(
      ts.transpileModule(fs.readFileSync(source, "utf8"), {
        fileName: source,
        compilerOptions: {
          module: ts.ModuleKind.CommonJS,
          target: ts.ScriptTarget.ES2022,
          jsx: ts.JsxEmit.ReactJSX,
          esModuleInterop: true,
        },
      }).outputText,
      source,
    );
  };
  Module._extensions[".tsx"] = compile;
  Module._extensions[".ts"] = compile;
  Module._extensions[".css"] = () => {};
  delete require.cache[filename];
  const exports = require(filename);
  return {
    ProjectSetup: exports.ProjectSetup,
    ProjectSetupReport: exports.ProjectSetupReport,
    restore() {
      Object.assign(Module._extensions, original);
      delete require.cache[filename];
    },
  };
}

function createHookHarness() {
  const slots = [];
  const effects = [];
  let hookIndex = 0;
  const hooks = {
    useState(initial) {
      const index = hookIndex++;
      if (!(index in slots))
        slots[index] = typeof initial === "function" ? initial() : initial;
      return [
        slots[index],
        (value) => {
          slots[index] =
            typeof value === "function" ? value(slots[index]) : value;
        },
      ];
    },
    useRef(initial) {
      const index = hookIndex++;
      if (!(index in slots)) slots[index] = { current: initial };
      return slots[index];
    },
    useMemo(factory, dependencies) {
      const index = hookIndex++;
      const previous = slots[index];
      const changed =
        !previous ||
        dependencies === undefined ||
        dependencies.some(
          (value, i) => !Object.is(value, previous.dependencies[i]),
        );
      if (changed) slots[index] = { value: factory(), dependencies };
      return slots[index].value;
    },
    useEffect(effect, dependencies) {
      const index = hookIndex++;
      const previous = effects[index];
      const changed =
        !previous ||
        dependencies === undefined ||
        dependencies.some(
          (value, i) => !Object.is(value, previous.dependencies[i]),
        );
      if (!changed) return;
      previous?.cleanup?.();
      effects[index] = {
        dependencies,
        cleanup: effect() || undefined,
      };
    },
  };
  return {
    hooks,
    render(Component, props) {
      hookIndex = 0;
      return Component(props);
    },
    unmount() {
      for (const effect of effects) effect?.cleanup?.();
    },
  };
}

function find(node, predicate) {
  if (!node || typeof node !== "object") return undefined;
  if (predicate(node)) return node;
  for (const child of [node.props?.children].flat(Infinity)) {
    const result = find(child, predicate);
    if (result) return result;
  }
  return undefined;
}

function makeStep(id, type = "implementation", title = "Implémentation") {
  return {
    id,
    type,
    title,
    objective: "",
    status: "pending",
    exitCriteria: [],
    expectedArtifacts: [],
    skills: [],
  };
}

function makeWorkflow(id, title, type = "implementation") {
  return {
    id,
    title,
    steps: [
      makeStep(`${id}:one`, type, title),
      makeStep(`${id}:review`, "review", "Review"),
    ],
  };
}

function existingProject() {
  return {
    id: "project:test",
    name: "Produit",
    directory: "/private/project",
    conventions: "Conventions manuelles",
    locations: { prototype: "app/prototypes" },
    workflows: [makeWorkflow("manual", "Manuel")],
    sourcesOfTruth: [
      {
        id: "context",
        title: "Contexte",
        path: "docs/CONTEXT.md",
        description: "Décisions acceptées",
      },
    ],
    preferences: {
      provider: "claude",
      model: "claude-existing-model",
      concurrency: 5,
    },
    workflowPolicy: "enforced",
    updatedAt: "2026-10-06T08:00:00.000Z",
  };
}

function report() {
  return {
    directory: "/private/project",
    name: "Produit",
    scannedAt: "2026-10-06T12:00:00.000Z",
    filesScanned: 12,
    historyCount: 2,
    summary: "Conventions et références repérées.",
    evidence: [],
    // A workflow suggestion is deliberately present to prove it is ignored
    // by the knowledge-only onboarding while the historical workflow stays.
    workflows: [makeWorkflow("detected", "Détecté", "specification")],
    sourcesOfTruth: [
      {
        id: "duplicate-source",
        title: "Contexte détecté",
        path: "docs/CONTEXT.md",
        description: "Même référence",
      },
      {
        id: "new-source",
        title: "Runbook",
        path: "docs/RUNBOOK.md",
        description: "Commandes utiles",
      },
    ],
    locations: { scripts: "scripts" },
    conventions: "Règles détectées",
    notes: [],
    analysis: { provider: "claude", status: "agent", detail: "Terminé" },
  };
}

function runtime() {
  const pending = [];
  const cancelled = [];
  global.window = {
    djinn: {
      discoverProject(input) {
        return new Promise((resolve, reject) => {
          pending.push({ input, resolve, reject });
        });
      },
      cancelProjectDiscovery(scanId) {
        cancelled.push(scanId);
        return Promise.resolve({ cancelled: true });
      },
      selectDirectory: async () => null,
      validateProject: async (value) => value,
    },
  };
  return { pending, cancelled };
}

function inputByValue(tree, value) {
  return find(
    tree,
    (node) => node.type === "input" && node.props?.value === value,
  );
}

function buttonContaining(tree, text) {
  return find(
    tree,
    (node) =>
      node.type === "button" &&
      [node.props?.children].flat(Infinity).join(" ").includes(text),
  );
}

async function flush() {
  await Promise.resolve();
  await new Promise((resolve) => setImmediate(resolve));
}

test("applying one report twice preserves history/settings and deduplicates sources", async () => {
  const runtimeState = runtime();
  const harness = createHookHarness();
  const loaded = loadProjectSetup(harness.hooks);
  try {
    const saved = [];
    const props = {
      project: existingProject(),
      defaultProvider: "codex",
      defaultModel: "codex-default-model",
      onClose() {},
      onToast() {},
      onSave: async (value) => saved.push(value),
    };
    const render = () => harness.render(loaded.ProjectSetup, props);
    let tree = render();
    const scanButton = buttonContaining(tree, "Ré-analyser le projet");
    assert.ok(scanButton);
    scanButton.props.onClick();
    assert.equal(runtimeState.pending.length, 1);
    assert.equal(runtimeState.pending[0].input.directory, "/private/project");
    assert.equal(runtimeState.pending[0].input.provider, "claude");
    assert.equal(runtimeState.pending[0].input.model, "claude-existing-model");
    assert.equal(runtimeState.pending[0].input.includeHistory, true);
    assert.match(runtimeState.pending[0].input.scanId, /.+/);
    runtimeState.pending[0].resolve(report());
    await flush();

    tree = render();
    const firstReport = find(
      tree,
      (node) => node.type?.name === "ProjectSetupReport",
    );
    assert.ok(firstReport);
    firstReport.props.onIncludeConventions(true);
    tree = render();
    find(
      tree,
      (node) => node.type?.name === "ProjectSetupReport",
    ).props.onApply();

    tree = render();
    const secondReport = find(
      tree,
      (node) => node.type?.name === "ProjectSetupReport",
    );
    assert.ok(secondReport);
    secondReport.props.onApply();

    await render().props.onSubmit({ preventDefault() {} });
    assert.equal(saved.length, 1);
    assert.equal(saved[0].directory, "/private/project");
    assert.deepEqual(saved[0].preferences, {
      provider: "claude",
      model: "claude-existing-model",
      concurrency: 5,
    });
    assert.equal(saved[0].workflows.length, 1);
    assert.equal(saved[0].workflows[0].id, "manual");
    assert.equal(saved[0].sourcesOfTruth.length, 2);
    assert.deepEqual(
      saved[0].sourcesOfTruth.map((source) => source.path),
      ["docs/CONTEXT.md", "docs/RUNBOOK.md"],
    );
    assert.match(saved[0].conventions, /Conventions manuelles/);
    assert.match(saved[0].conventions, /Règles détectées/);
    assert.equal(
      (saved[0].conventions.match(/Conventions détectées par le scan/g) || [])
        .length,
      1,
    );
  } finally {
    loaded.restore();
    delete global.window;
  }
});

test("changing the directory or closing cancels discovery and ignores late reports", async () => {
  const runtimeState = runtime();
  const harness = createHookHarness();
  const loaded = loadProjectSetup(harness.hooks);
  try {
    const props = {
      project: existingProject(),
      defaultProvider: "codex",
      defaultModel: "codex-default-model",
      onClose() {},
      onToast() {},
      onSave: async () => {},
    };
    const render = () => harness.render(loaded.ProjectSetup, props);
    let tree = render();
    const directory = inputByValue(tree, "/private/project");
    assert.ok(directory);
    buttonContaining(tree, "Ré-analyser le projet").props.onClick();
    const firstScanId = runtimeState.pending[0].input.scanId;
    directory.props.onChange({ target: { value: "/private/other-project" } });
    render();
    assert.deepEqual(runtimeState.cancelled, [firstScanId]);
    runtimeState.pending[0].resolve(report());
    await flush();
    tree = render();
    assert.equal(
      find(tree, (node) => node.type?.name === "ProjectSetupReport"),
      undefined,
    );

    const closeRuntime = runtime();
    const closeHarness = createHookHarness();
    const closeLoaded = loadProjectSetup(closeHarness.hooks);
    const closeProps = { ...props, project: existingProject() };
    const closeRender = () =>
      closeHarness.render(closeLoaded.ProjectSetup, closeProps);
    const closeTree = closeRender();
    closeHarness.render(closeLoaded.ProjectSetup, closeProps);
    buttonContaining(closeTree, "Ré-analyser le projet").props.onClick();
    const closeScanId = closeRuntime.pending[0].input.scanId;
    closeHarness.unmount();
    assert.deepEqual(closeRuntime.cancelled, [closeScanId]);
    closeRuntime.pending[0].resolve(report());
    await flush();
    closeLoaded.restore();
  } finally {
    loaded.restore();
    delete global.window;
  }
});
