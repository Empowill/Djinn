import { test, expect } from "@playwright/test";
import { readFile } from "node:fs/promises";

test("completed native step sends one notification and its click opens the result", async ({
  page,
}) => {
  const { task } = JSON.parse(
    await readFile("docs/v0.2.1-session-harmonisation.djinn.json", "utf8"),
  );
  task.demo = false;
  task.title = "Notifications étapes";
  task.status = "paused";
  task.phase = "review";
  task.questions = [];
  task.agents = [];
  task.actions = [];
  task.events = [];
  task.feedback = [];
  task.instructions = [];
  task.runtimeEventIds = [];
  task.steps[0].type = "review";
  task.steps[0].title = "Vérifier le résultat";
  const state = {
    version: 2,
    tasks: [task],
    projects: [],
    selectedId: task.id,
    settings: {
      provider: "codex",
      model: "",
      reduceMotion: true,
      sound: false,
    },
  };
  const run = {
    taskId: task.id,
    stepId: task.activeStepId,
    runId: "notification-run",
    mode: "review",
    startedAt: new Date().toISOString(),
    status: "running",
    phase: "lead",
    activeAgents: [],
    events: [],
  };
  await page.addInitScript(
    ({ state, run }) => {
      const w = window as any;
      w.notificationInputs = [];
      w.djinn = {
        loadState: async () => state,
        saveState: async (value: unknown) => {
          w.savedState = value;
        },
        getRuntimeSnapshot: async () => ({
          capturedAt: new Date().toISOString(),
          runs: [run],
        }),
        getActions: async () => [],
        getEnvironment: async () => ({
          platform: "darwin",
          appVersion: "0.2.1",
          providers: [
            {
              id: "codex",
              name: "Codex",
              available: true,
              authenticated: true,
            },
          ],
        }),
        notifyQuestion: async (input: unknown) => {
          w.notificationInputs.push(input);
          return { shown: true };
        },
        onEvent: (callback: unknown) => {
          w.emitRuntime = callback;
          return () => {
            if (w.emitRuntime === callback) w.emitRuntime = undefined;
          };
        },
      };
    },
    { state, run },
  );
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: task.title, exact: true }),
  ).toBeVisible();
  await page.waitForFunction(
    () => (window as any).savedState?.tasks[0].steps[0].status === "running",
  );
  await page.evaluate(
    ({ taskId, stepId, runId }) =>
      (window as any).emitRuntime({
        eventId: "blocked-notification-test",
        taskId,
        stepId,
        runId,
        type: "status",
        timestamp: new Date().toISOString(),
        data: { status: "blocked" },
      }),
    run,
  );
  await page.waitForFunction(
    () => (window as any).savedState?.tasks[0].status === "waiting",
  );
  expect(
    await page.evaluate(() => (window as any).notificationInputs.length),
  ).toBe(0);
  const completed = {
    eventId: "completed-notification-test",
    taskId: task.id,
    stepId: task.activeStepId,
    runId: run.runId,
    type: "status",
    timestamp: new Date().toISOString(),
    data: { status: "completed" },
  };
  await page.evaluate((event) => (window as any).emitRuntime(event), completed);
  await expect
    .poll(() => page.evaluate(() => (window as any).notificationInputs.length))
    .toBe(1);
  const alert = await page.evaluate(
    () => (window as any).notificationInputs[0],
  );
  expect(alert.title).toBe("Étape terminée");
  expect(alert.body).toContain(
    "Notifications étapes — Vérifier le résultat · Résultat à valider",
  );
  await page.evaluate((event) => (window as any).emitRuntime(event), completed);
  await page.evaluate(
    ({ taskId, questionId }) =>
      (window as any).emitRuntime({
        eventId: "notification-click-test",
        type: "notification_clicked",
        taskId,
        data: { questionId },
      }),
    alert,
  );
  await expect(
    page.getByRole("heading", { name: "Review", exact: true, level: 1 }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Valider", exact: true }),
  ).toBeEnabled();
  expect(
    await page.evaluate(() => (window as any).notificationInputs.length),
  ).toBe(1);
  expect(errors).toEqual([]);
});
