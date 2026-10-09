// Smoke tests of the browser mode: djinn serves the built interface itself (see global-setup.ts). Run them with
// `go tool task e2e`, which builds what they need first.
import { defineConfig, devices } from "@playwright/test";

// A spec's djinn is not the one a Djinn worker running it reports to: the commands the specs run must not name that
// worker's task ((djinn.v1.env) fills --task-id from it).
delete process.env.DJINN_TASK_ID;
delete process.env.DJINN_WISH_ID;

export default defineConfig({
  testDir: ".",
  testMatch: /.*\.spec\.ts/,
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
