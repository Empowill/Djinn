// Mark a task done from the window, against the real djinn up --browser (see global-setup.ts): in the Tasks tab, an
// interrupted task, imported with its wish, leaves what moves or waits and tops the finished tasks, saying who closed
// it and why. Every run makes
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

test("a task marked done leaves what moves and tops the finished tasks", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const wishId = randomUUID();
  const azimaId = randomUUID();
  const title = `Close old work ${wishId.slice(0, 8)}`;
  const task = (
    code: string,
    taskTitle: string,
    status: string,
    partOf?: string,
    end?: string,
  ) => ({
    id: randomUUID(),
    wish_id: wishId,
    code,
    title: taskTitle,
    status: `TASK_STATUS_${status}`,
    create_time: "2026-10-07T21:00:00Z",
    ...(partOf ? { part_of: partOf } : {}),
    ...(end ? { end_time: end } : {}),
  });
  const file = path.join(
    fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-done-")),
    "wish.json",
  );
  fs.writeFileSync(
    file,
    JSON.stringify({
      version: 1,
      create_time: "2026-10-07T21:10:00Z",
      wish: { id: wishId, title, create_time: "2026-10-07T21:00:00Z" },
      tasks: [
        {
          id: azimaId,
          wish_id: wishId,
          code: "T1",
          title: "First milestone",
          kind: "TASK_KIND_AZIMA",
          status: "TASK_STATUS_PENDING",
          create_time: "2026-10-07T21:00:00Z",
        },
        task("W1", "Merged long ago", "DONE", azimaId, "2026-10-07T21:05:00Z"),
        task("W2", "Failed on a flaky test", "FAILED", azimaId),
        task("W3", "Still planned", "PENDING", azimaId),
      ],
    }),
  );
  djinn("wish", "import", file);
  try {
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    const tasksTab = page.getByRole("tab", { name: /^Tasks/ });
    await expect(tasksTab).toHaveText("Tasks3");
    await tasksTab.click();
    const moving = page.locator(".tasks-moving");
    await expect(moving.locator("h2")).toHaveText("Moving or waiting0");
    await expect(page.locator(".tasks-finished")).toHaveCount(0);

    const azima = page.locator(".azima-card");
    await expect(azima.locator(".azima-heading strong")).toHaveText(
      "First milestone",
    );
    // Under the azima: moving parts W2 and W3; W1 is folded.
    await expect(azima.locator(".wish-task")).toHaveCount(2);
    await expect(azima.locator(".wish-task").first()).toContainText(
      "Failed on a flaky test",
    );
    await expect(azima.locator(".wish-task").nth(1)).toContainText(
      "Still planned",
    );
    await expect(azima.locator(".fold-line")).toHaveText("Show 1 finished");

    const card = azima.locator(".wish-task").filter({
      hasText: "Failed on a flaky test",
    });
    await card.getByRole("button", { name: "Mark done" }).click();
    await card
      .getByLabel("Why, in a few words (optional)")
      .fill("merged in Git");
    await card.getByRole("button", { name: "Mark done" }).last().click();

    // It leaves moving parts and joins the folded finished parts at the top.
    await expect(azima.locator(".wish-task")).toHaveCount(1);
    await expect(azima.locator(".wish-task")).toContainText("Still planned");
    await expect(azima.locator(".fold-line")).toHaveText("Show the 2 finished");
    await azima.locator(".fold-line").click();

    const allTasks = azima.locator(".wish-task");
    await expect(allTasks).toHaveCount(3);
    const first = allTasks.nth(1);
    await expect(first).toContainText("Failed on a flaky test");
    await expect(first.locator(".wish-task-closed")).toContainText(
      /Closed by you, .+: merged in Git/,
    );
    await expect(allTasks.nth(2)).toContainText("Merged long ago");
    await expect(page.locator(".tasks-finished")).toHaveCount(0);
    const listed = JSON.parse(
      djinn("task", "list", "--wish-id", wishId, "--json"),
    ).tasks as { code: string; status: string; closed?: { actor: string } }[];
    const closed = listed.find((t) => t.code === "W2");
    expect(closed?.status).toBe("TASK_STATUS_DONE");
    expect(closed?.closed?.actor).toBe("CLOSER_DEVELOPER");
    expect(errors).toEqual([]);
  } finally {
    // It may have been imported paused, when three wishes were active.
    try {
      djinn("wish", "pause", wishId);
    } catch {}
  }
});
