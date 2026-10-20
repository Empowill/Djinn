// Smoke tests of the browser mode: djinn serves the built interface itself (see global-setup.ts). Run them with
// `go tool task e2e`, which builds what they need first.
import path from "node:path";
import { defineConfig, devices } from "@playwright/test";

// A spec's djinn is not the one a Djinn worker running it reports to: the commands the specs run must not name that
// worker's task ((djinn.v1.env) fills --task-id from it), nor, run from a lead's terminal, its lead (DJINN_LEAD makes
// an answer the lead's, which djinn tells no lead).
delete process.env.DJINN_TASK_ID;
delete process.env.DJINN_WISH_ID;
delete process.env.DJINN_ADDR;
delete process.env.DJINN_LEAD;
process.env.DJINN_TEST = "1";
// The specs' djinns read the machine calm, its cores and memory but no load nor pressure (machine.Calm): other
// workers' builds and tests put the machine under pressure, and a djinn under pressure starts no worker, so the fake
// tasks the specs wait for stayed pending (W181). Only a test build honours it.
process.env.DJINN_TEST_MACHINE = "calm";

// One djinn per worker (global-setup.ts starts them, DJINN_URL_i and DJINN_E2E_HOME_i): a worker's specs share theirs,
// and two workers never touch the same one. This file is loaded again in each worker process, where
// TEST_PARALLEL_INDEX is its slot: the specs read DJINN_URL and DJINN_E2E_HOME as they run. Three workers on the
// developer's machine (W246: the e2e from 19.2 to 6.3 min), two on CI's four-core runners, one on Windows (ci.yml,
// W36); DJINN_E2E_WORKERS or --workers moves it.
const slot = process.env.TEST_PARALLEL_INDEX;
if (slot !== undefined && process.env[`DJINN_URL_${slot}`]) {
  process.env.DJINN_URL = process.env[`DJINN_URL_${slot}`];
  process.env.DJINN_E2E_HOME = process.env[`DJINN_E2E_HOME_${slot}`];
}
const workers =
  Number(process.env.DJINN_E2E_WORKERS) || (process.env.CI ? 2 : 3);

// The render performance spec (e2e/wish-render.spec.ts) runs on demand on the real-size wish of W174:
// `go tool task e2e -- e2e/wish-render.spec.ts` (or --grep @render). It is kept out of the default e2e run.
const args = process.argv.slice(2);
const runRenderSpec = args.some(
  (arg) => arg.includes("wish-render") || arg.includes("@render"),
);
// The cases that run the creation page's smoke solver (tag @solver) wait about a minute for its first glyphs on a
// software WebGL2: they run on demand too, `go tool task e2e -- e2e/wish-composer.spec.ts --grep @solver`.
const runSolver = args.some((arg) => arg.includes("@solver"));
// The cases whose point is a time budget (tag @budget: milliseconds a key, a chunk, a frame takes) measure the machine
// as much as Djinn: other workers' load fails them whatever the change, so the push check leaves them out (W181). They
// run on demand, `go tool task e2e -- --grep @budget`, and with the spec that holds them when it is named.
const runBudget = args.some(
  (arg) => arg.includes("@budget") || /\.spec\.ts/.test(arg),
);
const left = [
  ...(runSolver ? [] : [/@solver/]),
  ...(runBudget ? [] : [/@budget/]),
];

export default defineConfig({
  testDir: ".",
  testMatch: /.*\.spec\.ts/,
  testIgnore: runRenderSpec ? undefined : [/.*wish-render\.spec\.ts/],
  grepInvert: left.length ? left : undefined,
  outputDir: "../test-results/e2e",
  globalSetup: "./global-setup.ts",
  timeout: 30_000,
  expect: { timeout: 6_000 },
  // A spec's tests run in its order, in one worker, on one djinn; the specs spread over the workers.
  fullyParallel: false,
  workers,
  forbidOnly: Boolean(process.env.CI),
  // The wall clock against the baseline first, then the list's summary: a push check keeps only the last lines of
  // its output (W124), and they must name the tests that failed, not the baseline's warning.
  reporter: [[path.resolve(__dirname, "reporter.ts")], ["list"]],
  use: {
    ...devices["Desktop Chrome"],
    viewport: { width: 1440, height: 1000 },
    colorScheme: "dark",
    locale: "en-US",
    // The trace of a red test, without a snapshot of the DOM at each step: on a big page under other workers' load
    // they took 300-600 ms a step, long specs past their 30 s (W212, W249). A red one keeps its actions, sources,
    // screencast, screenshot and error-context.md (the page's accessibility tree).
    trace: {
      mode: "retain-on-failure",
      snapshots: false,
      screenshots: true,
      sources: true,
    },
    screenshot: "only-on-failure",
    video: "off",
  },
});
