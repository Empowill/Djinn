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
  const title = `Close old work ${wishId.slice(0, 8)}`;
  const task = (
    code: string,
    taskTitle: string,
    status: string,
    end?: string,
  ) => ({
    id: randomUUID(),
    wish_id: wishId,
    code,
    title: taskTitle,
    status: `TASK_STATUS_${status}`,
    create_time: "2026-10-07T21:00:00Z",
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
        task("W1", "Merged long ago", "DONE", "2026-10-07T21:05:00Z"),
        task("W2", "Cut short by a restart", "INTERRUPTED"),
        task("W3", "Still planned", "PENDING"),
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
    const finished = page.locator(".tasks-finished");
    await expect(moving.locator("h2")).toHaveText("Moving or waiting2");
    await expect(finished.locator("h2")).toHaveText("Finished1");
    // Cut short before planned; the finished task at the bottom.
    await expect(moving.locator(".wish-task").first()).toContainText(
      "Cut short by a restart",
    );
    await expect(moving.locator(".wish-task").nth(1)).toContainText(
      "Still planned",
    );
    await expect(finished.locator(".wish-task")).toContainText([
      "Merged long ago",
    ]);

    const card = moving.locator(".wish-task").filter({
      hasText: "Cut short by a restart",
    });
    await card.getByRole("button", { name: "Mark done" }).click();
    await card
      .getByLabel("Why, in a few words (optional)")
      .fill("merged in Git");
    await card.getByRole("button", { name: "Mark done" }).last().click();

    // It leaves the top section and tops the finished ones.
    await expect(moving.locator("h2")).toHaveText("Moving or waiting1");
    await expect(finished.locator("h2")).toHaveText("Finished2");
    await expect(card).toHaveCount(0);
    const first = finished.locator(".wish-task").first();
    await expect(first).toContainText("Cut short by a restart");
    await expect(first.locator(".wish-task-closed")).toContainText(
      /Closed by you, .+: merged in Git/,
    );
    await expect(finished.locator(".wish-task").nth(1)).toContainText(
      "Merged long ago",
    );
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
