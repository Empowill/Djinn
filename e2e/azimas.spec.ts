// The azimas of a wish in the window, against the real djinn up --browser (see global-setup.ts): in the Tasks tab, the
// work part of an azima is grouped under it, each azima says what it waits for and its progress, and none is ever
// said to wait for you. What is finished folds, the done azimas and the finished parts, until a click shows it. Opened,
// an azima's Markdown description is on the left, its parts on the right; a narrow window stacks them. Every run makes
// fresh identifiers, so --repeat-each works on the same djinn.
import { expect, test } from "@playwright/test";
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

test("the Tasks tab groups work under its azima, and no azima waits for you", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const wishId = randomUUID();
  const title = `Plan the lamp ${wishId.slice(0, 8)}`;
  const ids = {
    T1: randomUUID(),
    T2: randomUUID(),
    T3: randomUUID(),
  };
  const task = (
    code: string,
    taskTitle: string,
    status: string,
    extra: Record<string, unknown> = {},
  ) => ({
    id: ids[code as keyof typeof ids] ?? randomUUID(),
    wish_id: wishId,
    code,
    title: taskTitle,
    status: `TASK_STATUS_${status}`,
    create_time: "2026-10-07T21:00:00Z",
    ...extra,
  });
  const azima = { kind: "TASK_KIND_AZIMA" };
  const file = path.join(
    fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-azimas-")),
    "wish.json",
  );
  fs.writeFileSync(
    file,
    JSON.stringify({
      version: 1,
      create_time: "2026-10-07T21:10:00Z",
      wish: { id: wishId, title, create_time: "2026-10-07T21:00:00Z" },
      tasks: [
        task("T1", "Lay the ground", "DONE", {
          ...azima,
          end_time: "2026-10-07T21:01:00Z",
        }),
        task("T2", "The orchestrator", "PENDING", {
          ...azima,
          depends_on: [ids.T1],
          description:
            "Split the **orchestrator** in two:\n\n- the scheduler\n- the runner",
        }),
        task("T3", "Spread the work", "PENDING", {
          ...azima,
          depends_on: [ids.T1, ids.T2],
        }),
        task("W1", "Schedule the workers", "DONE", {
          part_of: ids.T2,
          end_time: "2026-10-07T21:05:00Z",
        }),
        task("W2", "Pause a worker", "PENDING", { part_of: ids.T2 }),
        // Cancelled: no work left, out of the counts, listed among the finished parts.
        task("W4", "Drop the old runner", "CANCELLED", {
          part_of: ids.T2,
          end_time: "2026-10-07T21:06:00Z",
        }),
        task("W3", "Polish the window", "PENDING"),
      ],
    }),
  );
  djinn("wish", "import", file);
  try {
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    // Nothing waits for you: no azima is your move.
    await expect(page.locator("#action-center")).toHaveCount(0);
    const tasksTab = page.getByRole("tab", { name: /^Tasks/ });
    // The work only: an azima is the plan, a cancelled task no work left.
    await expect(tasksTab).toHaveText("Tasks3");
    await tasksTab.click();

    await expect(page.locator(".tasks-moving h2")).toHaveText(
      "Moving or waiting1",
    );
    await expect(page.locator(".tasks-moving .wish-task")).toContainText([
      "Polish the window",
    ]);
    const azimas = page.locator(".tasks-azimas");
    await expect(azimas.locator("h2")).toHaveText("Azimas3");
    // Under way first, then the blocked one; the done one folded, not rendered.
    await expect(azimas.locator(".azima-heading .agent-code")).toHaveText([
      "T2",
      "T3",
    ]);
    const t2 = azimas.locator(".azima-card").first();
    await expect(t2.locator(".azima-heading")).toContainText("In progress");
    // Its heading reads at a glance: what comes before it, its parts by state, on hover only; W4, cancelled, is
    // not one of them.
    await expect(t2.locator(".azima-progress")).toHaveText("1/2");
    await expect(t2.locator(".azima-progress")).toHaveAttribute(
      "title",
      "1 of 2 parts done\n1 done, 0 running, 1 waiting",
    );
    await expect(t2.locator(".azima-heading strong")).toHaveAttribute(
      "title",
      "The orchestrator\nafter T1",
    );
    await expect(t2.locator(".azima-heading")).not.toContainText("after T1");
    await expect(t2.locator(".azima-heading")).not.toContainText("1 done");
    // Opened: its description, Markdown rendered, on the left, at most 40% of the card; its parts on the right.
    await expect(t2.locator(".azima-description strong")).toHaveText(
      "orchestrator",
    );
    await expect(t2.locator(".azima-description li")).toHaveText([
      "the scheduler",
      "the runner",
    ]);
    // Measured at once: the page may scroll between two calls.
    const columns = () =>
      t2.locator(".azima-body").evaluate((body) => {
        const rect = (selector: string) =>
          body.querySelector(selector)!.getBoundingClientRect();
        const [side, work] = [rect(".azima-side"), rect(".azima-parts")];
        return {
          width: body.getBoundingClientRect().width,
          sticky: getComputedStyle(body.querySelector(".azima-side")!).position,
          side: {
            x: side.x,
            y: side.y,
            width: side.width,
            bottom: side.bottom,
          },
          work: { x: work.x, y: work.y },
        };
      });
    const wide = await columns();
    expect(wide.side.x + wide.side.width).toBeLessThanOrEqual(wide.work.x);
    expect(wide.side.width).toBeLessThanOrEqual(wide.width * 0.4 + 1);
    expect(Math.abs(wide.side.y - wide.work.y)).toBeLessThan(2);
    // In sight while the parts scroll.
    expect(wide.sticky).toBe("sticky");
    // A narrow window stacks them: the description, then the parts.
    await page.setViewportSize({ width: 700, height: 1000 });
    await expect
      .poll(async () => {
        const narrow = await columns();
        return (
          narrow.work.y >= narrow.side.bottom - 1 && narrow.sticky === "static"
        );
      })
      .toBe(true);
    await page.setViewportSize({ width: 1440, height: 1000 });
    // Opened on its work: what waits, what is done folded under it; a click shows it.
    await expect(t2.locator(".wish-task .agent-code")).toHaveText(["W2"]);
    await t2.locator(".fold-line").click();
    await expect(t2.locator(".wish-task .agent-code")).toHaveText([
      "W2",
      "W4",
      "W1",
    ]);
    await expect(t2.locator(".fold-line")).toHaveText("Hide the finished ones");
    await expect(
      t2.locator(".wish-task").filter({ hasText: "Drop the old runner" }),
    ).toContainText("Cancelled");
    const doneFold = azimas.locator(":scope > .fold-line");
    await expect(doneFold).toHaveText("Show 1 finished");
    await doneFold.click();
    await expect(azimas.locator(".azima-heading .agent-code")).toHaveText([
      "T2",
      "T3",
      "T1",
    ]);
    // What a click unfolded stays so, the Tasks tab left and back.
    await page.getByRole("tab", { name: /^Decisions/ }).click();
    await tasksTab.click();
    await expect(azimas.locator(".azima-heading .agent-code")).toHaveText([
      "T2",
      "T3",
      "T1",
    ]);
    await expect(t2.locator(".wish-task .agent-code")).toHaveText([
      "W2",
      "W4",
      "W1",
    ]);
    const t3 = azimas.locator(".azima-card").nth(1);
    await expect(t3.locator(".azima-waits")).toHaveText("Waits for T2");
    await expect(t3.locator(".azima-heading")).toContainText("Open");
    await t3.locator(".azima-heading").click();
    await expect(t3.locator(".azima-parts")).toContainText(
      "No work is part of it yet.",
    );
    // No description: its parts take the whole width.
    await expect(t3.locator(".azima-side")).toHaveCount(0);
    const t3Body = (await t3.locator(".azima-body").boundingBox())!;
    const t3Parts = (await t3.locator(".azima-parts").boundingBox())!;
    expect(t3Parts.width).toBeGreaterThan(t3Body.width * 0.9);
    await expect(page.locator(".tasks-finished")).toHaveCount(0);
    await expect(page.locator(".tone-waiting")).toHaveCount(0);

    const listed = JSON.parse(
      djinn("task", "list", "--wish-id", wishId, "--json"),
    ).tasks as {
      code: string;
      azima?: { state: string; ready?: boolean };
    }[];
    expect(listed.find((t) => t.code === "T2")?.azima).toEqual({
      branch: "djinn/T2-the-orchestrator",
      base_branch: "djinn/T1-lay-the-ground",
      state: "AZIMA_STATE_IN_PROGRESS",
      ready: true,
      parts: 2,
      parts_done: 1,
    });
    expect(listed.find((t) => t.code === "T3")?.azima?.ready).toBeFalsy();
    expect(errors).toEqual([]);
  } finally {
    // It may have been imported paused, when three wishes were active.
    try {
      djinn("wish", "pause", wishId);
    } catch {}
  }
});
