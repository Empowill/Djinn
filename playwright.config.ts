import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  testMatch: /.*\.spec\.ts/,
  timeout: 35_000,
  expect: { timeout: 6_000 },
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  reporter: process.env.CI ? [["line"]] : [["list"]],
  use: {
    ...devices["Desktop Chrome"],
    viewport: { width: 1440, height: 1000 },
    baseURL: "http://127.0.0.1:4317",
    colorScheme: "dark",
    locale: "fr-FR",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "off",
  },
  webServer: process.env.DJINN_OFFLINE_E2E
    ? undefined
    : {
        command:
          "./node_modules/.bin/vite --host 127.0.0.1 --port 4317 --clearScreen false",
        url: "http://127.0.0.1:4317",
        reuseExistingServer: true,
        timeout: 30_000,
        stdout: "ignore",
        stderr: "pipe",
      },
});
