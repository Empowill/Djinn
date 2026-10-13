// An instruction sent from the window to a running worker: an event of the task at once, then "received" once the
// worker says something after it. The worker is the fake agent: no model is called. It stays a while after, since a
// finished task folds its events.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import path from "node:path";
import { portable } from "./portable";
import { openWorkspaceSection } from "./workspace-selectors";

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

type Task = {
  id: string;
  status: string;
  machine_wait?: boolean;
};

test("an instruction sent to a running worker is recorded, then acknowledged", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const title = `Send a word ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  try {
    const spawned = JSON.parse(
      djinn(
        "task",
        "spawn",
        wishId,
        "--title",
        "Work a while",
        "--provider",
        "fake",
        "--prompt",
        "text working\nsleep 6s\ntext after\nsleep 8s",
        "--json",
      ),
    ).task as Task;
    const taskState = () =>
      JSON.parse(djinn("task", "get", spawned.id, "--json")).task as Task;
    // The fake worker can be held by host memory. Starting a machine-wait task explicitly matches the Planned card
    // action and leaves the assertions below focused on instruction delivery and acknowledgement.
    await expect
      .poll(
        () => {
          const current = taskState();
          return current.status === "TASK_STATUS_RUNNING" ||
            current.machine_wait
            ? current.status
            : "";
        },
        { timeout: 10_000 },
      )
      .not.toBe("");
    if (taskState().machine_wait) djinn("task", "start", spawned.id);
    await expect
      .poll(() => taskState().status, { timeout: 10_000 })
      .toBe("TASK_STATUS_RUNNING");
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await openWorkspaceSection(page, "tasks");
    const task = page.locator(".wish-task").filter({ hasText: "Work a while" });
    await task.getByRole("button", { name: /Work a while/ }).click();
    await expect(
      page.locator(".wish-event").getByText("working", { exact: true }),
    ).toBeVisible();

    const box = page.getByLabel(
      "An instruction for the worker, while it works",
    );
    await box.fill("Also update the docs");
    await box.press("Enter");
    await expect(box).toHaveValue("");
    await expect(
      page.locator(".wish-event.kind-11").getByText("Also update the docs"),
    ).toBeVisible();
    await expect(
      page
        .locator(".wish-event.kind-12")
        .getByText("Received: Also update the docs"),
    ).toBeVisible({ timeout: 15_000 });
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "pause", wishId);
  }
});
