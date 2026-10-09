// The README's screenshots (readme.shot.ts), on the e2e's setup: a djinn up --browser of its own, in a temporary
// DJINN_HOME, stopped by its PID. Run them with `go tool task screenshots`; `go tool task e2e` never runs them.
import { defineConfig } from "@playwright/test";

import e2e from "./playwright.config";

export default defineConfig({
  ...e2e,
  testMatch: /.*\.shot\.ts/,
  outputDir: "../test-results/screenshots",
  timeout: 60_000,
  use: { ...e2e.use, viewport: { width: 1440, height: 1000 } },
});
