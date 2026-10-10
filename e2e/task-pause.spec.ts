// Pause a worker from the window, then let it go on, against the real djinn up --browser (see global-setup.ts): the
// button on the task's card pauses it, the card says it is paused, the other button resumes it. The worker is the fake
// agent: no model is called. Not on Windows, where djinn refuses to pause and the window shows no button.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
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

test.skip(process.platform === "win32", "no pause on Windows yet");

test("a worker paused from its card holds, then goes on once resumed", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);

  const title = `Hold still ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  const taskId = JSON.parse(
    djinn(
      "task",
      "spawn",
      wishId,
      "--title",
      "Work a long while",
      "--provider",
      "fake",
      "--prompt",
      "text working\nsleep 60s\ntext after",
      "--json",
    ),
  ).task.id as string;
  try {
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await page.getByRole("tab", { name: /^Tasks/ }).click();
    const card = page.locator(`#task-${taskId}`);
    const badge = card.locator(".wish-task-heading .status-badge");
    await expect(badge).toHaveText("Running");

    await card.getByRole("button", { name: "Pause the worker" }).click();
    await expect(badge).toHaveText("Paused");
    await expect(
      card.getByRole("button", { name: "Pause the worker" }),
    ).toHaveCount(0);
    await card.getByRole("button", { name: /Work a long while/ }).click();
    await expect(
      card.locator(".wish-event").getByText(/^paused: /),
    ).toBeVisible();

    await card.getByRole("button", { name: "Resume the worker" }).click();
    await expect(badge).toHaveText("Running");
    await expect(
      card.locator(".wish-event").getByText("resumed", { exact: true }),
    ).toBeVisible();
    await expect(
      card.getByRole("button", { name: "Pause the worker" }),
    ).toBeVisible();
    expect(errors).toEqual([]);
  } finally {
    djinn("task", "stop", taskId);
    djinn("wish", "pause", wishId);
  }
});
