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
// Load the other TypeScript files the tests require the same way.
Module._extensions[".ts"] ??= (mod, file) =>
  mod._compile(
    ts.transpileModule(fs.readFileSync(file, "utf8"), {
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2022,
        esModuleInterop: true,
      },
    }).outputText,
    file,
  );
loaded._compile(
  ts.transpileModule(fs.readFileSync(filename, "utf8"), {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText,
  filename,
);
const { visualizationMessage } = loaded.exports;

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
      "frame-src 'self'",
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

test("the Mermaid frame runs only its two scripts, allowed by their hashes", async () => {
  const { createHash } = require("node:crypto");
  const { mermaidFrame, MERMAID_FRAME } = require("../src/mermaid-frame.ts");
  assert.equal(MERMAID_FRAME, "mermaid-frame.html");
  const html = await mermaidFrame("window.mermaid={}");
  const policy = html.match(
    /http-equiv="Content-Security-Policy" content="([^"]+)"/,
  )[1];
  assert.ok(html.indexOf("Content-Security-Policy") < html.indexOf("<script"));
  const scripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map(
    (m) => m[1],
  );
  assert.equal(scripts.length, 2);
  assert.equal(scripts[0], "window.mermaid={}");
  const allowed = policy.split("; ").find((d) => d.startsWith("script-src"));
  assert.equal(
    allowed,
    "script-src " +
      scripts
        .map(
          (s) => `'sha256-${createHash("sha256").update(s).digest("base64")}'`,
        )
        .join(" "),
  );
  for (const directive of [
    "default-src 'none'",
    "frame-src 'none'",
    "object-src 'none'",
    "form-action 'none'",
    "base-uri 'none'",
  ])
    assert.ok(policy.split("; ").includes(directive), directive);
  // No connect-src: default-src 'none' forbids connections, and the test build's window rewrites the first one.
  assert.ok(!policy.includes("connect-src"));
  assert.ok(!allowed.includes("unsafe"));
  // The bootstrap answers only its parent, and only in a sandboxed (opaque) frame.
  assert.ok(scripts[1].includes('self.origin!=="null"'));
  assert.ok(scripts[1].includes("event.source!==parent"));
  assert.ok(scripts[1].includes('securityLevel:"strict"'));
  await assert.rejects(mermaidFrame("a</script>b"));
  await assert.rejects(mermaidFrame("a<SCRIPT>b"));
});
