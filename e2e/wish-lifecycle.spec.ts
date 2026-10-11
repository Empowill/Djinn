// A wish set aside and taken back, from the top of its screen: Pause, then Resume; a paused wish sent first from the
// side panel; and a wish deleted with its trash, its tasks and blocks with it.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
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

test("a wish is paused, resumed, sent first and deleted from the window", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(process.env.DJINN_URL!);
  await expect(page.locator(".app-statusbar").getByText("Live")).toBeVisible();

  const title = `Set aside ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish.id as string;
  const item = page.locator(".wish-nav").filter({ hasText: title });
  await item.click();
  await expect(page.locator(".hero h1")).toHaveText(title);
  const top = page.locator(".topbar-actions");
  const badge = page.locator(".review-pills .status-badge").first();

  // Paused: Resume shows, alone; it makes the wish active.
  await expect(top.getByRole("button", { name: "Pause" })).toHaveCount(0);
  await top.getByRole("button", { name: "Resume" }).click();
  await expect(badge).toContainText("Active");
  await expect(top.getByRole("button", { name: "Resume" })).toHaveCount(0);

  // Pause sets it aside again.
  await top.getByRole("button", { name: "Pause" }).click();
  await expect(badge).toContainText("Paused");

  // From the side panel, a paused wish goes first: active, ranked 1.
  const row = page.locator(".wish-row").filter({ hasText: title });
  await row.hover();
  await row
    .getByRole("button", { name: "Make it the first active wish" })
    .click();
  await expect(badge).toContainText("Active · 1");
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes as {
    id: string;
    rank?: number;
    state?: string;
  }[];
  expect(listed.find((w) => w.id === wishId)?.rank).toBe(1);
  expect(
    listed.filter((w) => w.state === "WISH_STATE_ACTIVE").length,
  ).toBeLessThanOrEqual(3);

  // The trash asks first, then the wish goes, with its task and its block.
  djinn(
    "task",
    "spawn",
    wishId,
    "--title",
    "Polish the lamp",
    "--provider",
    "fake",
    "--later",
  );
  djinn(
    "block",
    "put",
    wishId,
    "--kind",
    "note",
    "--title",
    "Oil",
    "--content",
    "Whale oil, no more.",
  );
  await top.getByRole("button", { name: "Delete this wish" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText(`Delete “${title}”?`);
  await expect(dialog).toContainText("1 task");
  await dialog.getByRole("button", { name: "Delete" }).click();
  await expect(item).toHaveCount(0);
  const after = (JSON.parse(djinn("wish", "list", "--json")).wishes ?? []) as {
    id: string;
  }[];
  expect(after.some((w) => w.id === wishId)).toBe(false);
  expect(
    JSON.parse(djinn("block", "list", wishId, "--json")).blocks ?? [],
  ).toEqual([]);
  expect(
    JSON.parse(djinn("task", "list", "--wish-id", wishId, "--json")).tasks ??
      [],
  ).toEqual([]);

  expect(errors).toEqual([]);
});
