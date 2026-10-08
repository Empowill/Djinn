// An instruction sent from the window to a running worker: an event of the task at once, then "received" once the
// worker says something after it. The worker is the fake agent: no model is called.
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

test("an instruction sent to a running worker is recorded, then acknowledged", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const title = `Send a word ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  try {
    djinn(
      "task",
      "spawn",
      wishId,
      "--title",
      "Work a while",
      "--provider",
      "fake",
      "--prompt",
      "text working\nsleep 6s\ntext after",
    );
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await page.getByRole("button", { name: /Work a while/ }).click();
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
