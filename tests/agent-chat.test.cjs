"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const Module = require("node:module");
const ts = require("typescript");
const React = require("react");
const { renderToStaticMarkup } = require("react-dom/server");

// New Markdown supports are real source modules too. Resolve their relative
// TypeScript and JSON imports from the importing module, without emitting files.
const originalExtensions = { ...Module._extensions };
Module._extensions[".tsx"] = Module._extensions[".ts"] = (child, p) => {
  child._compile(
    ts.transpileModule(fs.readFileSync(p, "utf8"), {
      fileName: p,
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2022,
        jsx: ts.JsxEmit.ReactJSX,
        esModuleInterop: true,
      },
    }).outputText,
    p,
  );
};
Module._extensions[".css"] = () => {};
test.after(() => Object.assign(Module._extensions, originalExtensions));

const filename = path.resolve(__dirname, "../src/agent-chat.tsx");
const compiled = ts.transpileModule(fs.readFileSync(filename, "utf8"), {
  fileName: filename,
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2022,
    jsx: ts.JsxEmit.ReactJSX,
    esModuleInterop: true,
  },
});
const loaded = new Module(filename, module);
loaded.filename = filename;
loaded.paths = Module._nodeModulePaths(path.dirname(filename));
const originalRequire = loaded.require.bind(loaded);
loaded.require = (id) => {
  if (id.endsWith(".css")) return {};
  if (id === "./markdown-body") {
    const p = path.resolve(__dirname, "../src/markdown-body.tsx");
    const child = new Module(p, module);
    child.filename = p;
    child.paths = loaded.paths;
    child.require = (name) =>
      name.endsWith(".css") ? {} : originalRequire(name);
    child._compile(
      ts.transpileModule(fs.readFileSync(p, "utf8"), {
        fileName: p,
        compilerOptions: {
          module: ts.ModuleKind.CommonJS,
          target: ts.ScriptTarget.ES2022,
          jsx: ts.JsxEmit.ReactJSX,
          esModuleInterop: true,
        },
      }).outputText,
      p,
    );
    return child.exports;
  }
  if (id === "./visualization-frame")
    return {
      VisualizationFrame: ({ artifact }) =>
        React.createElement("div", { "data-visualization-id": artifact.id }),
    };
  if (id === "./data")
    return {
      event: () => {
        throw new Error("No event creation during read-only chat rendering");
      },
    };
  if (id === "./visuals")
    return {
      Orb: () => null,
      agentColor: () => "#aaa",
      agentOrbState: () => "connecting",
    };
  return originalRequire(id);
};
loaded._compile(compiled.outputText, filename);
const { entriesFor, MarkdownBody } = loaded.exports;
const agent = {
  id: "build",
  name: "Nova",
  role: "Code",
  model: "configured",
  status: "queued",
  progress: 0,
  summary: "",
};
const time = "2026-10-05T10:00:00.000Z";
const event = (id, title, detail, actor = "build") => ({
  id,
  title,
  detail,
  agentId: actor,
  type: "note",
  time,
});
const task = (overrides) => ({
  questions: [],
  instructions: [],
  events: [],
  ...overrides,
});

test("sending one instruction produces one human bubble and never an assistant echo", () => {
  const instruction = {
    id: "human-1",
    text: "Corrige le filtre",
    time,
    agentId: "build",
  };
  const entries = entriesFor(
    task({
      instructions: [instruction],
      events: [
        event("echo", "Vous", instruction.text),
        event("receipt", "Indication prise en compte", instruction.text),
      ],
    }),
    agent,
  );
  assert.equal(entries.length, 1);
  assert.equal(entries[0].kind, "user");
  assert.equal(entries[0].text, instruction.text);
  assert.equal(entries[0].pending, true);
  const consumed = entriesFor(
    task({ instructions: [{ ...instruction, appliedAt: time }] }),
    agent,
  );
  assert.equal(consumed[0].pending, false);
});

test("agent chat isolates targeted messages and keeps real assistant/tool output chronological", () => {
  const events = [
    event("reply", "Résultat", "Le filtre est corrigé."),
    {
      ...event("command", "Vérification", "Commande : npm test\nSortie : PASS"),
      type: "tool",
      time: "2026-10-05T09:59:00.000Z",
    },
    event("other", "Autre agent", "Autre travail", "review"),
  ];
  const instructions = [
    { id: "other-user", text: "Revois le résultat", time, agentId: "review" },
    { id: "global", text: "Contexte de mission", time },
  ];
  const entries = entriesFor(task({ events, instructions }), agent);
  assert.deepEqual(
    entries.map((entry) => entry.kind),
    ["tool", "assistant"],
  );
  assert.equal(entries[0].text, "Commande : npm test\nSortie : PASS");
  assert.equal(entries[1].text, "Le filtre est corrigé.");
});

test("agent threads hide known provider diagnostics while preserving real errors", () => {
  const entries = entriesFor(
    task({
      events: [
        {
          ...event(
            "stdin",
            "L’agent a rencontré une erreur",
            "Reading additional input from stdin...",
          ),
          type: "error",
        },
        {
          ...event(
            "skills",
            "L’agent a rencontré une erreur",
            "2026-10-06T07:36:00Z WARN codex_skills::interface: ignoring interface.icon_small",
          ),
          type: "error",
        },
        {
          ...event(
            "real",
            "L’agent a rencontré une erreur",
            "The configured model is unavailable.",
          ),
          type: "error",
        },
        event(
          "diagnostic",
          "Diagnostic du fournisseur",
          "WARN codex_skills::interface",
        ),
      ],
    }),
    agent,
  );
  assert.deepEqual(
    entries.map((entry) => entry.id),
    ["event-real"],
  );
  assert.equal(entries[0].kind, "error");
  assert.match(entries[0].text, /configured model/);
});

test("markdown renders useful formatting while removing executable HTML and unsafe URLs", () => {
  const text =
    '**Validé**\n\n<script>alert(1)</script>\n\n[attaque](javascript:alert(1))\n\n[documentation](https://example.com/docs)\n\n```js\nconst value = "<script>";\n```';
  const html = renderToStaticMarkup(
    React.createElement(MarkdownBody, { text }),
  );
  assert.ok(html.includes("<strong>Validé</strong>"));
  assert.ok(html.includes('href="https://example.com/docs"'));
  assert.ok(html.includes("&lt;script&gt;"));
  assert.ok(html.includes("Copier le code"));
  assert.equal(html.includes("<script>"), false);
  assert.equal(html.includes("javascript:"), false);
  assert.equal(html.includes("alert(1)</script>"), false);
});

test("artifact cards associate the support with its real agent, step and run", () => {
  const artifact = {
    id: "asset",
    title: "Simulation",
    type: "visualization",
    content: "<b>Local</b>",
    revision: 2,
    stepId: "s1",
    runId: "r1",
  };
  const support = {
    ...event("support", "Support disponible : Simulation", ""),
    stepId: "s1",
    runId: "r1",
  };
  const entries = entriesFor(
    task({ artifacts: [artifact], events: [support] }),
    agent,
  );
  assert.equal(entries.length, 1);
  assert.equal(entries[0].kind, "artifact");
  assert.equal(entries[0].artifact.id, "asset");
  assert.equal(
    entriesFor(
      task({
        artifacts: [artifact],
        events: [{ ...support, agentId: "review" }],
      }),
      agent,
    ).length,
    0,
  );
  assert.equal(
    entriesFor(
      task({ artifacts: [artifact], events: [{ ...support, stepId: "s2" }] }),
      agent,
    ).length,
    0,
  );
  assert.equal(
    entriesFor(
      task({ artifacts: [artifact], events: [{ ...support, runId: "r2" }] }),
      agent,
    ).length,
    0,
  );
  const card = renderToStaticMarkup(
    React.createElement(loaded.exports.ArtifactChatCard, { artifact }),
  );
  assert.ok(card.includes("Simulation"));
  assert.ok(card.includes("Visualisation interactive"));
  assert.ok(card.includes('aria-expanded="true"'));
  assert.ok(card.includes('data-visualization-id="asset"'));
  assert.equal(card.includes("<iframe"), false);
});

test("shared Markdown renders GFM tables, disabled task lists and headings", () => {
  const html = renderToStaticMarkup(
    React.createElement(MarkdownBody, {
      text: "# Résultat\n\n| Choix | Valeur |\n| --- | --- |\n| A | 42 |\n\n- [x] Validé\n- [ ] À discuter",
    }),
  );
  assert.ok(html.includes("<h1>Résultat</h1>"));
  assert.ok(html.includes("<table>"));
  assert.ok(html.includes('checked=""'));
  assert.ok(html.includes('disabled=""'));
});
