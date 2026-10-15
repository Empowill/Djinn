// The decision log, against the real djinn up --browser (see global-setup.ts): a question answered in the window, a
// task spawned from it with --decision, a decision block the lead wrote. The Decisions tab lists both without a button,
// the developer's with the human label; its link opens the task in the Tasks tab, and the task links back. The worker
// is the fake agent: no model is called. Every run makes fresh identifiers, so --repeat-each works on the same djinn.
// Screenshots of the tab, dark and light, go to test-results/e2e/.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import path from "node:path";
import { portable } from "./portable";
import {
  openWorkspaceActions,
  openWorkspaceSection,
} from "./workspace-selectors";

portable();

const shots = path.join(__dirname, "../test-results/e2e");
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

test("a decision answered in the window leads to a task, both ways, in the Decisions tab", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const title = `Decide the oil ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  try {
    djinn(
      "question",
      "ask",
      "Which oil for the wick?",
      wishId,
      "--options",
      "Olive",
      "--options",
      "Paraffin",
      "--icon",
      "🔒",
    );
    djinn(
      "block",
      "put",
      wishId,
      "--kind",
      "decision",
      "--title",
      "Ship on Friday",
      "--content",
      "Unless it rains.",
      "--icon",
      "🧱",
    );
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();

    // The developer answers in the window.
    const actions = await openWorkspaceActions(page);
    // In dock mode the question text moves to the dock header, so it is not
    // part of the question-card subtree. Use the dock's semantic Options group
    // and keep the option text assertion tied to the requested choice.
    const options = actions
      .getByRole("group", { name: /Options/ })
      .getByRole("button");
    await expect(options).toHaveCount(2);
    await expect(options.nth(1)).toContainText("Paraffin");
    await options.nth(1).click();
    await actions.getByRole("button", { name: /^Respond/ }).click();
    await expect(actions.getByRole("group", { name: /Options/ })).toHaveCount(
      0,
    );

    // The lead spawns a task from that decision.
    djinn(
      "task",
      "spawn",
      wishId,
      "--title",
      "Fill with paraffin",
      "--provider",
      "fake",
      "--prompt",
      "text filled",
      "--decision",
      "Q01",
    );

    const tab = page.locator('[data-wish-section="activity"]');
    await expect(tab.locator("strong")).toHaveText("Activity");
    await expect(tab.locator(".count")).toHaveText("2");
    const log = await openWorkspaceSection(page, "decisions");
    await expect(log.locator('.decision-row[role="button"]')).toHaveCount(2);
    const rows = log.locator(".decision-row");
    // The latest first: the answer came after the block.
    await expect(rows.first()).toContainText("Which oil for the wick?");
    const answered = rows.first();
    await expect(answered).toHaveClass(/tone-human/);
    await expect(answered.locator(".decision-icon")).toHaveText("🔒");
    await expect(answered.locator(".status-badge.tone-human")).toHaveText(
      "Decided by you",
    );
    await expect(answered).toContainText("→ B · Paraffin");
    const block = rows.filter({ hasText: "Ship on Friday" });
    await expect(block.locator(".decision-icon")).toHaveText("🧱");
    await expect(block.locator(".decision-by")).toHaveText("By the lead");
    await page.screenshot({ path: path.join(shots, "decision-log-dark.png") });

    // The decision opens its task in the Tasks tab, and the task leads back.
    await answered.getByRole("link", { name: "W1" }).click();
    await expect(
      page.locator('[data-wish-section="tasks"] .workspace-section-toggle'),
    ).toHaveAttribute("aria-current", "true");
    const task = page
      .locator(".wish-task")
      .filter({ hasText: "Fill with paraffin" });
    await expect(task).toHaveClass(/focused/);
    await expect(task).toBeInViewport();
    const taskDrawer = page.locator("dialog.task-drawer-shell");
    await expect(taskDrawer).toBeVisible();
    await expect(
      taskDrawer.getByRole("button", { name: /Close|Fermer/ }),
    ).toBeFocused();
    await page
      .locator("dialog.task-drawer-shell")
      .getByRole("link", { name: /From Q01/ })
      .click();
    await expect(
      page.locator('[data-wish-section="activity"] .workspace-section-toggle'),
    ).toHaveAttribute("aria-current", "true");
    await expect(
      page.locator(".decision-row.focused").filter({
        hasText: "Which oil for the wick?",
      }),
    ).toBeInViewport();

    await page.evaluate(() => localStorage.setItem("djinn.theme", "light"));
    await page.reload();
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await openWorkspaceSection(page, "decisions");
    await expect(page.locator(".decision-row")).toHaveCount(2);
    await page.screenshot({ path: path.join(shots, "decision-log-light.png") });
    await page.evaluate(() => localStorage.removeItem("djinn.theme"));
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "pause", wishId);
  }
});
