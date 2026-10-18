// Live update e2e test: creates an azima and groups a task into it from the CLI
// while the window is open, verifying it moves under the azima without a reload.
import { expect, test } from "./test";
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

test("makes an azima and groups a task into it from CLI without reload", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));

  const wishId = randomUUID();
  const taskId = randomUUID();
  const title = `Live group wish ${wishId.slice(0, 8)}`;

  const file = path.join(
    fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-live-group-")),
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
          id: taskId,
          wish_id: wishId,
          code: "W1",
          title: "Implement database layer",
          status: "TASK_STATUS_PENDING",
          create_time: "2026-10-07T21:00:00Z",
        },
      ],
    }),
  );

  djinn("wish", "import", file);

  try {
    await page.goto(process.env.DJINN_URL!);
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    // Navigate to the imported wish
    await page.locator(".wish-nav").filter({ hasText: title }).click();

    // Go to Tasks tab
    const tasksTab = page.getByRole("tab", { name: /^Tasks/ });
    await expect(tasksTab).toHaveText("Tasks1");
    await tasksTab.click();

    // Task W1 is initially loose in moving / waiting
    await expect(page.locator(".tasks-moving h2")).toHaveText(
      "Moving or waiting1",
    );
    await expect(page.locator(".tasks-moving .wish-task")).toContainText([
      "Implement database layer",
    ]);
    // No azimas exist yet
    await expect(page.locator(".tasks-azimas")).toHaveCount(0);

    // 1. Spawn an azima from CLI while the window stays open
    const spawnOutput = djinn(
      "task",
      "spawn",
      wishId,
      "--kind",
      "azima",
      "--title",
      "Storage Engine Phase",
      "--json",
    );
    const azima = JSON.parse(spawnOutput).task;
    expect(azima).toBeDefined();

    // Without reloading, the new azima appears in the Tasks tab
    const azimasSection = page.locator(".tasks-azimas");
    await expect(azimasSection).toBeVisible();
    await expect(
      azimasSection.locator(".azima-card .azima-heading"),
    ).toContainText("Storage Engine Phase");
    // Task W1 is still loose
    await expect(page.locator(".tasks-moving .wish-task")).toHaveCount(1);

    // 2. Group task W1 into the azima from CLI while the window stays open
    djinn("task", "group", taskId, "--part-of", azima.id, "--json");

    // Without reloading, W1 leaves the loose moving section and moves under the azima
    await expect(page.locator(".tasks-moving h2")).toHaveText(
      "Moving or waiting0",
    );
    await expect(page.locator(".tasks-moving .wish-task")).toHaveCount(0);

    // The azima card auto-expands and contains W1
    const azimaCard = azimasSection.locator(".azima-card").first();
    await expect(azimaCard.locator(".wish-task")).toContainText([
      "Implement database layer",
    ]);

    expect(errors).toEqual([]);
  } finally {
    try {
      djinn("wish", "pause", wishId);
    } catch {}
  }
});
