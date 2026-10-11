// Smoke tests of the browser mode: djinn serves the built interface itself (see global-setup.ts). Run them with
// `go tool task e2e`, which builds what they need first.
import { defineConfig, devices } from "@playwright/test";

// A spec's djinn is not the one a Djinn worker running it reports to: the commands the specs run must not name that
// worker's task ((djinn.v1.env) fills --task-id from it).
delete process.env.DJINN_TASK_ID;
delete process.env.DJINN_WISH_ID;
// The render performance spec (e2e/wish-render.spec.ts) runs on demand on the real-size wish of W174:
// `go tool task e2e -- e2e/wish-render.spec.ts` (or --grep @render). It is kept out of the default e2e run.
const args = process.argv.slice(2);
const runRenderSpec = args.some(
  (arg) => arg.includes("wish-render") || arg.includes("@render"),
);

export default defineConfig({
  testDir: ".",
  testMatch: /.*\.spec\.ts/,
  testIgnore: runRenderSpec ? undefined : [/.*wish-render\.spec\.ts/],
  outputDir: "../test-results/e2e",
  globalSetup: "./global-setup.ts",
  timeout: 30_000,
  expect: { timeout: 6_000 },
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  reporter: [["list"]],
  use: {
    ...devices["Desktop Chrome"],
    viewport: { width: 1440, height: 1000 },
    colorScheme: "dark",
    locale: "en-US",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "off",
  },
});
