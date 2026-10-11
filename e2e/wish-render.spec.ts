// End-to-end render performance on a real-size wish (W174): measures opening the wish, switching between tabs
// (tasks, decisions, journal, blocks), opening a task, and change arrival over the live watch stream.
// Run alone with: go tool task e2e -- e2e/wish-render.spec.ts
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import path from "node:path";
import { importBigWish, removeBigWish } from "./bigwish";
import { portable } from "./portable";

// Budgets in milliseconds, set from first measurements on Linux with margin:
// open ~68 ms (budget 300 ms), tasks ~55 ms (budget 250 ms), decisions ~46 ms (budget 250 ms),
// journal ~34 ms (budget 200 ms), blocks ~60 ms (budget 250 ms), task open ~20 ms (budget 200 ms),
// change ~125 ms (budget 500 ms).
export const BUDGET_OPEN_MS = 300;
export const BUDGET_TAB_TASKS_MS = 250;
export const BUDGET_TAB_DECISIONS_MS = 250;
export const BUDGET_TAB_JOURNAL_MS = 200;
export const BUDGET_TAB_BLOCKS_MS = 250;
export const BUDGET_TASK_OPEN_MS = 200;
export const BUDGET_CHANGE_ARRIVAL_MS = 500;

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

function median(values: number[]): number {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 !== 0
    ? sorted[mid]
    : (sorted[mid - 1] + sorted[mid]) / 2;
}

test.describe("wish render performance @render", () => {
  test.setTimeout(120_000);

  let wishId: string;

  test.beforeAll(async () => {
    portable();
    wishId = importBigWish("real");
  });

  test.afterAll(async () => {
    if (wishId) {
      removeBigWish(wishId);
    }
  });

  test("measure render times against budgets on real-size wish", async ({
    page,
  }) => {
    portable();
    const errors: string[] = [];
    page.on("pageerror", (err) => errors.push(err.message));

    await page.goto(process.env.DJINN_URL!);
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    const runs = 3;

    // 1. Measure opening the wish (from flight plan to wish view)
    const openMeasures: number[] = [];
    for (let i = 0; i < runs; i++) {
      // Return to flight plan first
      const planBtn = page.locator(".plan-nav");
      if (await planBtn.isVisible()) {
        await planBtn.click();
        await expect(page.locator(".flight-plan")).toBeVisible();
      }

      const startMark = `open-start-${i}`;
      const endMark = `open-end-${i}`;
      const measureName = `open-duration-${i}`;

      const duration = await page.evaluate(
        async ({ startMark, endMark, measureName }) => {
          const wishBtn =
            document.querySelector<HTMLButtonElement>(".wish-nav");
          if (!wishBtn) throw new Error(".wish-nav button not found");

          performance.mark(startMark);
          const t0 = performance.now();
          wishBtn.click();

          await new Promise<void>((resolve) => {
            const isReady = () => {
              const h1 = document.querySelector(".hero h1");
              const tabs = document.querySelector(".view-tabs");
              const journal = document.querySelector(".wish-journal");
              return h1 && tabs && journal;
            };

            if (isReady()) {
              requestAnimationFrame(() =>
                requestAnimationFrame(() => resolve()),
              );
              return;
            }

            const observer = new MutationObserver(() => {
              if (isReady()) {
                observer.disconnect();
                requestAnimationFrame(() =>
                  requestAnimationFrame(() => resolve()),
                );
              }
            });
            observer.observe(document.body, { childList: true, subtree: true });
          });

          performance.mark(endMark);
          performance.measure(measureName, startMark, endMark);
          const entries = performance.getEntriesByName(measureName);
          return (
            entries[entries.length - 1]?.duration ?? performance.now() - t0
          );
        },
        { startMark, endMark, measureName },
      );

      await expect(page.locator(".hero h1")).toHaveText(
        "A wish of real size (real)",
      );
      openMeasures.push(duration);
    }

    // 2. Measure tab switches: tasks, decisions, journal, blocks
    const tabMeasures = {
      tasks: [] as number[],
      decisions: [] as number[],
      journal: [] as number[],
      blocks: [] as number[],
    };

    const tabsToTest: Array<{
      id: "tasks" | "decisions" | "journal" | "blocks";
      buttonId: string;
      readySelector: string;
    }> = [
      {
        id: "tasks",
        buttonId: "view-tab-tasks",
        readySelector: ".task-sections, .tasks-moving, .tasks-azimas",
      },
      {
        id: "decisions",
        buttonId: "view-tab-decisions",
        readySelector: ".decision-log",
      },
      {
        id: "journal",
        buttonId: "view-tab-main",
        readySelector: ".wish-journal",
      },
      {
        id: "blocks",
        buttonId: "view-tab-agents",
        readySelector: ".agent-blocks",
      },
    ];

    for (let i = 0; i < runs; i++) {
      for (const tab of tabsToTest) {
        const startMark = `tab-${tab.id}-start-${i}`;
        const endMark = `tab-${tab.id}-end-${i}`;
        const measureName = `tab-${tab.id}-duration-${i}`;

        const duration = await page.evaluate(
          async ({
            buttonId,
            readySelector,
            startMark,
            endMark,
            measureName,
          }) => {
            const btn = document.getElementById(buttonId);
            if (!btn) throw new Error(`Tab button #${buttonId} not found`);

            performance.mark(startMark);
            const t0 = performance.now();
            btn.click();

            await new Promise<void>((resolve) => {
              const isReady = () =>
                document.querySelector(readySelector) !== null;
              if (isReady()) {
                requestAnimationFrame(() =>
                  requestAnimationFrame(() => resolve()),
                );
                return;
              }
              const observer = new MutationObserver(() => {
                if (isReady()) {
                  observer.disconnect();
                  requestAnimationFrame(() =>
                    requestAnimationFrame(() => resolve()),
                  );
                }
              });
              observer.observe(document.body, {
                childList: true,
                subtree: true,
              });
            });

            performance.mark(endMark);
            performance.measure(measureName, startMark, endMark);
            const entries = performance.getEntriesByName(measureName);
            return (
              entries[entries.length - 1]?.duration ?? performance.now() - t0
            );
          },
          {
            buttonId: tab.buttonId,
            readySelector: tab.readySelector,
            startMark,
            endMark,
            measureName,
          },
        );

        tabMeasures[tab.id].push(duration);
      }
    }

    // 3. Measure opening of a task (on the tasks tab)
    const tasksTab = page.locator("#view-tab-tasks");
    if ((await tasksTab.getAttribute("aria-selected")) !== "true") {
      await tasksTab.click();
      await expect(page.locator(".tasks-moving")).toBeVisible();
    }

    const taskOpenMeasures: number[] = [];
    for (let i = 0; i < runs; i++) {
      const startMark = `task-open-start-${i}`;
      const endMark = `task-open-end-${i}`;
      const measureName = `task-open-duration-${i}`;

      const duration = await page.evaluate(
        async ({ startMark, endMark, measureName }) => {
          const closedTask = document.querySelector<HTMLElement>(
            ".wish-task:not(.open)",
          );
          if (!closedTask) throw new Error("No closed .wish-task found");
          const headingBtn = closedTask.querySelector<HTMLButtonElement>(
            "button.wish-task-heading",
          );
          if (!headingBtn)
            throw new Error("No button.wish-task-heading found in closed task");

          performance.mark(startMark);
          const t0 = performance.now();
          headingBtn.click();

          await new Promise<void>((resolve) => {
            const isReady = () =>
              closedTask.querySelector(".wish-task-body") !== null;
            if (isReady()) {
              requestAnimationFrame(() =>
                requestAnimationFrame(() => resolve()),
              );
              return;
            }
            const observer = new MutationObserver(() => {
              if (isReady()) {
                observer.disconnect();
                requestAnimationFrame(() =>
                  requestAnimationFrame(() => resolve()),
                );
              }
            });
            observer.observe(closedTask, { childList: true, subtree: true });
          });

          performance.mark(endMark);
          performance.measure(measureName, startMark, endMark);
          const entries = performance.getEntriesByName(measureName);
          const dur =
            entries[entries.length - 1]?.duration ?? performance.now() - t0;

          // Close the task card again so it can be re-opened
          headingBtn.click();
          await new Promise<void>((resolve) => {
            const isClosed = () =>
              closedTask.querySelector(".wish-task-body") === null;
            if (isClosed()) {
              requestAnimationFrame(() =>
                requestAnimationFrame(() => resolve()),
              );
              return;
            }
            const observer = new MutationObserver(() => {
              if (isClosed()) {
                observer.disconnect();
                requestAnimationFrame(() =>
                  requestAnimationFrame(() => resolve()),
                );
              }
            });
            observer.observe(closedTask, { childList: true, subtree: true });
          });

          return dur;
        },
        { startMark, endMark, measureName },
      );

      taskOpenMeasures.push(duration);
    }

    // 4. Measure change arrival (one change arriving over watch stream)
    // Make sure we are on the main/journal tab where questions are shown in the action center
    const mainTab = page.locator("#view-tab-main");
    if ((await mainTab.getAttribute("aria-selected")) !== "true") {
      await mainTab.click();
      await expect(page.locator(".wish-journal")).toBeVisible();
    }

    const changeMeasures: number[] = [];
    for (let i = 0; i < runs; i++) {
      const questionTitle = `Measure Question ${i + 1} ${randomUUID().slice(0, 8)}`;
      const startMark = `change-start-${i}`;
      const endMark = `change-end-${i}`;
      const measureName = `change-duration-${i}`;

      // Start observer in the page before triggering the command
      const waitPromise = page.evaluate(
        async ({ questionTitle, startMark, endMark, measureName }) => {
          return new Promise<number>((resolve) => {
            const check = () => {
              const cards = Array.from(
                document.querySelectorAll(".question-card"),
              );
              const found = cards.some((c) =>
                c.textContent?.includes(questionTitle),
              );
              if (found) {
                performance.mark(endMark);
                performance.measure(measureName, startMark, endMark);
                const entries = performance.getEntriesByName(measureName);
                resolve(entries[entries.length - 1]?.duration ?? 0);
                return true;
              }
              return false;
            };

            if (check()) return;

            const observer = new MutationObserver(() => {
              if (check()) {
                observer.disconnect();
              }
            });
            observer.observe(document.body, { childList: true, subtree: true });
          });
        },
        { questionTitle, startMark, endMark, measureName },
      );

      await page.evaluate(
        ({ startMark }) => {
          performance.mark(startMark);
        },
        { startMark },
      );

      djinn(
        "question",
        "ask",
        questionTitle,
        wishId,
        "--options",
        "A",
        "--options",
        "B",
      );

      const duration = await waitPromise;
      changeMeasures.push(duration);
    }

    const medianOpen = median(openMeasures);
    const medianTasks = median(tabMeasures.tasks);
    const medianDecisions = median(tabMeasures.decisions);
    const medianJournal = median(tabMeasures.journal);
    const medianBlocks = median(tabMeasures.blocks);
    const medianTaskOpen = median(taskOpenMeasures);
    const medianChange = median(changeMeasures);

    console.log("MEASUREMENTS (runs = 3):");
    console.log(
      `Open wish: raw=[${openMeasures.map((n) => n.toFixed(1)).join(", ")}] ms, median=${medianOpen.toFixed(1)} ms`,
    );
    console.log(
      `Tab tasks: raw=[${tabMeasures.tasks.map((n) => n.toFixed(1)).join(", ")}] ms, median=${medianTasks.toFixed(1)} ms`,
    );
    console.log(
      `Tab decisions: raw=[${tabMeasures.decisions.map((n) => n.toFixed(1)).join(", ")}] ms, median=${medianDecisions.toFixed(1)} ms`,
    );
    console.log(
      `Tab journal: raw=[${tabMeasures.journal.map((n) => n.toFixed(1)).join(", ")}] ms, median=${medianJournal.toFixed(1)} ms`,
    );
    console.log(
      `Tab blocks: raw=[${tabMeasures.blocks.map((n) => n.toFixed(1)).join(", ")}] ms, median=${medianBlocks.toFixed(1)} ms`,
    );
    console.log(
      `Open task: raw=[${taskOpenMeasures.map((n) => n.toFixed(1)).join(", ")}] ms, median=${medianTaskOpen.toFixed(1)} ms`,
    );
    console.log(
      `Change arrival: raw=[${changeMeasures.map((n) => n.toFixed(1)).join(", ")}] ms, median=${medianChange.toFixed(1)} ms`,
    );

    expect(errors).toEqual([]);
    expect(medianOpen).toBeLessThanOrEqual(BUDGET_OPEN_MS);
    expect(medianTasks).toBeLessThanOrEqual(BUDGET_TAB_TASKS_MS);
    expect(medianDecisions).toBeLessThanOrEqual(BUDGET_TAB_DECISIONS_MS);
    expect(medianJournal).toBeLessThanOrEqual(BUDGET_TAB_JOURNAL_MS);
    expect(medianBlocks).toBeLessThanOrEqual(BUDGET_TAB_BLOCKS_MS);
    expect(medianTaskOpen).toBeLessThanOrEqual(BUDGET_TASK_OPEN_MS);
    expect(medianChange).toBeLessThanOrEqual(BUDGET_CHANGE_ARRIVAL_MS);
  });
});
