"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const ts = require("typescript");
const Module = require("node:module");
const filename = require("node:path").resolve("src/visualization-document.ts");
const loaded = new Module(filename, module);
loaded.filename = filename;
loaded.paths = Module._nodeModulePaths(require("node:path").dirname(filename));
loaded._compile(
  ts.transpileModule(fs.readFileSync(filename, "utf8"), {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText,
  filename,
);
const { visualizationDocument, visualizationMessage, VISUALIZATION_POLICY } =
  loaded.exports;
test("isolated document places restrictive policy before supplied markup and reports height/errors", () => {
  const html = visualizationDocument(
    "<h1>Local</h1><script>window.local=true</script>",
    "test-token",
  );
  assert.ok(html.indexOf("Content-Security-Policy") < html.indexOf("<h1>"));
  for (const directive of [
    "connect-src 'none'",
    "frame-src 'none'",
    "object-src 'none'",
    "form-action 'none'",
    "base-uri 'none'",
  ])
    assert.ok(VISUALIZATION_POLICY.includes(directive));
  assert.ok(html.includes("script-src 'unsafe-inline'"));
  assert.ok(html.includes("djinn:visualization-height"));
  assert.ok(html.includes("djinn:visualization-error"));
  assert.throws(() => visualizationDocument("x".repeat(500001), "token"));
  assert.throws(() => visualizationDocument("\0", "token"));
  assert.throws(() => visualizationDocument("ok", "</script>"));
});

test("parent CSP allows only isolated frames in development and production", () => {
  const p = require("node:path").resolve("src/renderer-policy.ts");
  const policyModule = new Module(p, module);
  policyModule._compile(
    ts.transpileModule(fs.readFileSync(p, "utf8"), {
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2022,
      },
    }).outputText,
    p,
  );
  for (const origin of [undefined, "http://127.0.0.1:4317"]) {
    const policy = policyModule.exports.rendererPolicy(origin);
    assert.equal(
      policy.split("; ").find((d) => d.startsWith("frame-src")),
      "frame-src blob: djinn-visualization:",
    );
    assert.ok(policy.includes("object-src 'none'"));
    assert.equal(policy.includes("ws://127.0.0.1:4317"), Boolean(origin));
    assert.equal(
      policy.includes("script-src 'self' 'unsafe-inline'"),
      Boolean(origin),
    );
  }
});
test("height channel rejects forged windows, origins, tokens, coercions and unbounded values", () => {
  const source = {};
  const event = (data) => ({
    source,
    origin: "null",
    data: {
      type: "djinn:visualization-height",
      token: "valid",
      height: 640,
      ...data,
    },
  });
  assert.deepEqual(visualizationMessage(event({}), source, ["valid"]), {
    height: 640,
  });
  for (const height of [0, 119, 4001, Infinity, NaN, "640", null, {}, true])
    assert.equal(
      visualizationMessage(event({ height }), source, ["valid"]),
      null,
    );
  assert.equal(
    visualizationMessage(event({ token: "other" }), source, ["valid"]),
    null,
  );
  assert.equal(
    visualizationMessage(event({ type: "djinn:open-external" }), source, [
      "valid",
    ]),
    null,
  );
  assert.equal(
    visualizationMessage({ ...event({}), source: {} }, source, ["valid"]),
    null,
  );
  assert.equal(
    visualizationMessage(
      { ...event({}), origin: "https://example.com" },
      source,
      ["valid"],
    ),
    null,
  );
  assert.equal(visualizationMessage(event({}), null, ["valid"]), null);
  assert.deepEqual(
    visualizationMessage(
      event({ type: "djinn:visualization-error", message: "Erreur locale" }),
      source,
      ["valid"],
    ),
    { error: "Erreur locale" },
  );
  assert.equal(
    visualizationMessage(
      event({ type: "djinn:visualization-error", message: "x".repeat(301) }),
      source,
      ["valid"],
    ),
    null,
  );
});

test("visualization iframe grants scripts only and removes native/browser permissions", () => {
  const path = require("node:path");
  const p = path.resolve("src/visualization-frame.tsx");
  const frame = new Module(p, module);
  frame.filename = p;
  frame.paths = Module._nodeModulePaths(path.dirname(p));
  const original = frame.require.bind(frame);
  frame.require = (name) =>
    name === "./visualization-document" ? loaded.exports : original(name);
  frame._compile(
    ts.transpileModule(fs.readFileSync(p, "utf8"), {
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2022,
        jsx: ts.JsxEmit.ReactJSX,
      },
    }).outputText,
    p,
  );
  const React = require("react");
  const { renderToStaticMarkup } = require("react-dom/server");
  const html = renderToStaticMarkup(
    React.createElement(frame.exports.VisualizationFrame, {
      artifact: {
        id: "local",
        title: "Simulation",
        content: "<button>Local</button>",
        type: "visualization",
      },
    }),
  );
  assert.ok(html.includes('sandbox="allow-scripts"'));
  assert.ok(html.includes('referrerPolicy="no-referrer"'));
  assert.ok(html.includes("camera &#x27;none&#x27;"));
  assert.ok(html.includes("Afficher la source"));
  assert.equal(html.includes("allow-same-origin"), false);
  assert.equal(html.includes("allow-top-navigation"), false);
  assert.equal(html.includes("allow-popups"), false);
  assert.equal(html.includes("allow-forms"), false);
});
