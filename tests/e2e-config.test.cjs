// Unit checks for e2e/playwright.config.ts (retries: 1 only on Windows CI, 0 on Linux, macOS and local runs)
// and e2e/reporter.ts (flaky tests printed in the summary and $GITHUB_STEP_SUMMARY).
"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const Module = require("node:module");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const ts = require("typescript");

function loadTsModule(relPath) {
  const filename = path.resolve(__dirname, "..", relPath);
  const savedEnv = { ...process.env };
  try {
    const mod = new Module(filename, module);
    mod.filename = filename;
    mod.paths = Module._nodeModulePaths(path.dirname(filename));
    const output = ts.transpileModule(fs.readFileSync(filename, "utf8"), {
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2022,
        esModuleInterop: true,
      },
      fileName: filename,
    }).outputText;
    mod._compile(output, filename);
    return mod.exports;
  } finally {
    for (const k of Object.keys(process.env)) {
      if (!(k in savedEnv)) delete process.env[k];
    }
    Object.assign(process.env, savedEnv);
  }
}

test("e2e retries stay 0 off Windows and locally, and are 1 only on Windows CI", () => {
  const configExports = loadTsModule("e2e/playwright.config.ts");
  const { e2eRetries } = configExports;
  const config = configExports.default;

  if (process.platform !== "win32") {
    assert.equal(
      config.retries,
      0,
      "default config retries must be 0 off Windows",
    );
  }

  assert.equal(e2eRetries({ CI: "true" }, "win32"), 1);
  assert.equal(e2eRetries({ CI: "1" }, "win32"), 1);

  // Local runs on Windows keep 0.
  assert.equal(e2eRetries({}, "win32"), 0);
  assert.equal(e2eRetries({ CI: "" }, "win32"), 0);
  assert.equal(e2eRetries({ CI: "0" }, "win32"), 0);
  assert.equal(e2eRetries({ CI: "false" }, "win32"), 0);

  // Linux and macOS keep 0 both on CI and locally.
  for (const platform of ["linux", "darwin"]) {
    assert.equal(e2eRetries({}, platform), 0);
    assert.equal(e2eRetries({ CI: "true" }, platform), 0);
    assert.equal(e2eRetries({ CI: "1" }, platform), 0);
  }
});

test("E2EReporter prints flaky tests (passed on retry) in console and $GITHUB_STEP_SUMMARY", () => {
  const reporterExports = loadTsModule("e2e/reporter.ts");
  const E2EReporter = reporterExports.default;
  const { flakyTests } = reporterExports;
  const rootDir = path.resolve(__dirname, "../e2e");

  const suite = {
    allTests() {
      return [
        {
          title: "renders wish",
          location: {
            file: path.join(rootDir, "wish-render.spec.ts"),
            line: 30,
          },
          titlePath: () => [
            "",
            "chromium",
            "wish-render.spec.ts",
            "renders wish",
          ],
          outcome: () => "expected",
        },
        {
          title: "stops drawing when hidden",
          location: {
            file: path.join(rootDir, "idle-frames.spec.ts"),
            line: 42,
          },
          titlePath: () => [
            "",
            "chromium",
            "idle-frames.spec.ts",
            "idle frames",
            "stops drawing when hidden",
          ],
          outcome: () => "flaky",
        },
        {
          title: "keeps focus on reply",
          location: {
            file: path.join(rootDir, "tilasm-comments.spec.ts"),
            line: 108,
          },
          titlePath: () => [
            "",
            "chromium",
            "tilasm-comments.spec.ts",
            "keeps focus on reply",
          ],
          outcome: () => "flaky",
        },
      ];
    },
  };

  assert.deepEqual(flakyTests(suite, rootDir), [
    "idle-frames.spec.ts:42 › idle frames › stops drawing when hidden",
    "tilasm-comments.spec.ts:108 › keeps focus on reply",
  ]);

  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-reporter-"));
  const summaryFile = path.join(tmpDir, "summary.md");
  const prevSummary = process.env.GITHUB_STEP_SUMMARY;
  const prevUpdate = process.env.UPDATE_BASELINE;
  const logs = [];
  const origLog = console.log;
  console.log = (...args) => logs.push(args.join(" "));
  process.env.GITHUB_STEP_SUMMARY = summaryFile;
  delete process.env.UPDATE_BASELINE;

  try {
    const reporter = new E2EReporter();
    reporter.onBegin({ rootDir }, suite);
    reporter.onEnd({ status: "passed" });

    const output = logs.join("\n");
    assert.match(output, /Flaky e2e tests \(2 passed on retry\):/);
    assert.match(
      output,
      /flaky: idle-frames\.spec\.ts:42 › idle frames › stops drawing when hidden/,
    );
    assert.match(
      output,
      /flaky: tilasm-comments\.spec\.ts:108 › keeps focus on reply/,
    );

    const summary = fs.readFileSync(summaryFile, "utf8");
    assert.match(summary, /### Flaky e2e tests \(2 passed on retry\)/);
    assert.match(
      summary,
      /- `idle-frames\.spec\.ts:42 › idle frames › stops drawing when hidden`/,
    );
    assert.match(
      summary,
      /- `tilasm-comments\.spec\.ts:108 › keeps focus on reply`/,
    );
  } finally {
    console.log = origLog;
    if (prevSummary === undefined) delete process.env.GITHUB_STEP_SUMMARY;
    else process.env.GITHUB_STEP_SUMMARY = prevSummary;
    if (prevUpdate === undefined) delete process.env.UPDATE_BASELINE;
    else process.env.UPDATE_BASELINE = prevUpdate;
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
});
