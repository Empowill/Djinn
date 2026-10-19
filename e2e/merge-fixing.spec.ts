// A task whose merge failed while a correction task fixes it shows orange, being fixed, naming its correction; one
// whose correction failed shows red, merge failed (W8). The wish is imported, its tasks as Djinn leaves them: no
// worker runs, the correction waits unscheduled. MERGE_SHOTS=<folder> also writes the Tasks tab, both states in sight, in both themes there.
// Run alone with: go tool task e2e -- e2e/merge-fixing.spec.ts
import { type Page, expect, test } from "./test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import { portable } from "./portable";

portable();

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

const at = "2026-10-14T21:00:00Z";

// W1's merge conflicted, W2 corrects it and waits to start; W3's checks were red, W4 corrected it and failed.
const exportOf = (wishId: string, title: string, ids: string[]) => {
  const [w1, w2, w3, w4] = ids;
  const task = (id: string, code: string, name: string, extra: object) => ({
    id,
    wish_id: wishId,
    code,
    title: name,
    create_time: at,
    ...extra,
  });
  const failure = (state: string, taskId: string) => ({
    state,
    task_ids: [taskId],
    reason: "the merge failed",
  });
  return {
    version: 1,
    create_time: at,
    wish: { id: wishId, title, create_time: at },
    tasks: [
      task(w1, "W1", "Pour the oil", {
        status: "TASK_STATUS_DONE",
        integration: {
          state: "INTEGRATION_STATE_CONFLICT",
          branch: "feat/lamp",
          reason: "lamp.go conflicts with feat/lamp",
          corrected_by: w2,
          attempts: 1,
        },
      }),
      task(w2, "W2", "Correct the merge of W1", {
        status: "TASK_STATUS_PENDING",
        correction: {
          attempt: 1,
          failure: failure("INTEGRATION_STATE_CONFLICT", w1),
        },
      }),
      task(w3, "W3", "Trim the wick", {
        status: "TASK_STATUS_DONE",
        integration: {
          state: "INTEGRATION_STATE_RED",
          branch: "feat/lamp",
          reason: "go test exited 1",
          corrected_by: w4,
          attempts: 1,
        },
      }),
      task(w4, "W4", "Correct the checks of W3", {
        status: "TASK_STATUS_FAILED",
        error: "the tests stay red",
        correction: {
          attempt: 1,
          failure: failure("INTEGRATION_STATE_RED", w3),
        },
      }),
    ],
  };
};

// colour is the text colour of the element.
const colour = (page: Page, selector: string) =>
  page
    .locator(selector)
    .first()
    .evaluate((el) => getComputedStyle(el).color);

test("a failed merge an agent fixes shows orange and names its correction; red once its correction failed", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const tag = randomUUID().slice(0, 8);
  const wishId = randomUUID();
  const ids = [randomUUID(), randomUUID(), randomUUID(), randomUUID()];
  const title = `Light the lamp ${tag}`;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-fixing-"));
  const file = path.join(dir, "lamp.json");
  fs.writeFileSync(file, JSON.stringify(exportOf(wishId, title, ids)));
  djinn("wish", "import", file);
  fs.rmSync(dir, { recursive: true, force: true });
  try {
    // The show of the last minute is this wish's: the window gets it again as it connects (UiService.Watch).
    djinn("open", `djinn://wish/${wishId}`);
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await expect(page.locator("h1.wish-title")).toHaveText(title);

    // The head counts it apart, orange, a wrench: an agent fixes it.
    const pill = page.locator(".review-pills .count-pill.tone-fixing");
    await expect(pill).toHaveAttribute(
      "title",
      "1 merge being fixed: an agent corrects it, nothing to do",
    );
    await expect(pill).toContainText("being fixed");

    await page.locator("#view-tab-tasks").click();
    const fixing = page.locator(`#task-${ids[0]}`);
    const failed = page.locator(`#task-${ids[2]}`);
    await expect(fixing).toHaveClass(/tone-fixing/);
    await expect(fixing.locator(".status-badge").first()).toHaveText(
      "Merge being fixed by W2",
    );
    const work = fixing.locator(".wish-task-work");
    await expect(work).toHaveClass(/work-fixing/);
    await expect(work).toContainText("being fixed by W2");
    await expect(work.locator("button.task-link")).toHaveText("W2");
    await expect(failed).toHaveClass(/tone-failed/);
    await expect(failed.locator(".status-badge").first()).toHaveText(
      "Merge failed",
    );
    await expect(failed.locator(".wish-task-work")).toHaveClass(/work-failed/);

    // Orange as what waits, never the red of a failure (their contrast: tests/styles.test.cjs).
    const orange = await colour(page, `#task-${ids[0]} .status-badge`);
    expect(orange).toBe(
      await colour(page, ".review-pills .count-pill.tone-fixing"),
    );
    expect(orange).not.toBe(
      await colour(page, `#task-${ids[2]} .status-badge`),
    );

    const shots = process.env.MERGE_SHOTS;
    if (shots)
      for (const theme of ["dark", "light"] as const) {
        await page.evaluate((v) => {
          localStorage.setItem("djinn.theme", v);
          document.documentElement.dataset.theme = v;
          return Promise.all(
            document
              .getAnimations()
              .filter((a) => a instanceof CSSTransition)
              .map((a) => a.finished.catch(() => {})),
          );
        }, theme);
        await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
        // Both cards in sight, no title's tooltip over them.
        await page.mouse.move(0, 0);
        await page.screenshot({
          path: path.join(shots, `merge-states-${theme}.png`),
        });
      }
    expect(errors).toEqual([]);
  } finally {
    await page.evaluate(() => localStorage.removeItem("djinn.theme"));
    djinn("wish", "delete", wishId);
  }
});
