// Durable indications against the test server. Imported task results and the fake lead never call a model.
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
function djinn(...args: string[]) {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}
function instructions(wishId: string) {
  return (
    JSON.parse(djinn("instruction", "list", wishId, "--json")).instructions ??
    []
  );
}
function fixture() {
  for (const wish of JSON.parse(djinn("wish", "list", "--json")).wishes ?? [])
    if (wish.state === "WISH_STATE_ACTIVE") djinn("wish", "pause", wish.id);
  const wishId = randomUUID();
  const taskId = randomUUID();
  const title = `Instructions ${wishId.slice(0, 8)}`;
  const folder = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-instructions-"));
  const file = path.join(folder, "wish.json");
  const at = "2026-10-09T08:00:00Z";
  fs.writeFileSync(
    file,
    JSON.stringify({
      version: 1,
      create_time: at,
      wish: { id: wishId, title, create_time: at },
      tasks: [
        {
          id: taskId,
          wish_id: wishId,
          code: "W1",
          title: "Simulated successful worker",
          status: "TASK_STATUS_DONE",
          provider: "PROVIDER_FAKE",
          create_time: at,
        },
      ],
      blocks: [
        {
          id: randomUUID(),
          wish_id: wishId,
          kind: "section",
          title: "Notes before indications",
          content: "Keep this note.",
          position: "1000",
          create_time: at,
        },
      ],
      instructions: [
        {
          id: randomUUID(),
          wish_id: wishId,
          code: "I01",
          text: "Waiting indication",
          status: "INSTRUCTION_STATUS_PENDING",
          create_time: at,
          update_time: at,
        },
        {
          id: randomUUID(),
          wish_id: wishId,
          code: "I02",
          text: "Considering indication",
          status: "INSTRUCTION_STATUS_REFLECTING",
          create_time: at,
          update_time: at,
        },
        {
          id: randomUUID(),
          wish_id: wishId,
          code: "I03",
          text: "Delegated indication\nEvery detail remains.",
          status: "INSTRUCTION_STATUS_PROCESSING",
          task_id: taskId,
          create_time: at,
          update_time: at,
        },
        {
          id: randomUUID(),
          wish_id: wishId,
          code: "I04",
          text: "Verified indication",
          status: "INSTRUCTION_STATUS_DONE",
          task_id: taskId,
          create_time: at,
          update_time: at,
        },
      ],
    }),
  );
  djinn("wish", "import", file);
  return {
    wishId,
    taskId,
    title,
    cleanup: () => {
      djinn("wish", "pause", wishId);
      fs.rmSync(folder, { recursive: true, force: true });
    },
  };
}

async function foldTerminal(page: import("@playwright/test").Page) {
  const collapse = page
    .locator(".lead-terminal-bar button[title='Collapse the terminal']")
    .last();
  if (await collapse.isVisible()) await collapse.click();
}

test("a card saves offline without navigation; drafts survive folding and opening the wish", async ({
  page,
}) => {
  const sample = fixture();
  try {
    await page.goto(process.env.DJINN_URL!);
    await foldTerminal(page);
    const card = page.locator(".plan-wish").filter({ hasText: sample.title });
    const toggle = card.getByRole("button", {
      name: `Add an indication to ${sample.title}`,
    });
    await expect(card.locator("button button")).toHaveCount(0);
    await toggle.focus();
    await toggle.press("Enter");
    await expect(page.locator(".hero h1")).toHaveText("Flight plan");
    const box = card.getByLabel("An indication for this wish");
    await expect(box).toBeFocused();
    await expect(box).toBeEnabled();
    await expect(card.getByText(/The lead is stopped/)).toBeVisible();
    await box.fill("Offline indication");
    await box.press("Shift+Enter");
    await box.pressSequentially("Full detail");
    await box.press("Escape");
    await expect(toggle).toBeFocused();
    await toggle.press("Space");
    await expect(box).toHaveValue("Offline indication\nFull detail");
    await box.press("Enter");
    await expect(box).toHaveValue("");
    await expect(box).toBeFocused();
    await expect(card.getByText("Saved · pending")).toBeVisible();
    expect(instructions(sample.wishId)).toHaveLength(5);
    await expect(card.locator(".lead-message-panel")).toHaveCSS("opacity", "1");
    await card.screenshot({
      path: "test-results/e2e/flight-card-indication.png",
    });
    await box.fill("Draft carried to wish");
    await box.press("Escape");
    const open = card.getByRole("button", {
      name: `Open this wish: ${sample.title}`,
    });
    await open.focus();
    await open.press("Enter");
    await expect(page.locator(".hero h1")).toHaveText(sample.title);
    await page
      .getByRole("button", { name: "Add an indication", exact: true })
      .click();
    await expect(page.getByLabel("An indication for this wish")).toHaveValue(
      "Draft carried to wish",
    );
    await page.getByLabel("An indication for this wish").press("Escape");
    await expect(
      page.getByRole("dialog", { name: "Indication for the lead" }),
    ).toHaveCount(0);
    const history = page.getByRole("region", { name: "Indications" });
    await expect(history).toContainText("Offline indication\nFull detail");
    await expect(history.getByText("Pending", { exact: true })).toHaveCount(2);
    await expect(
      history.getByText("Reflecting", { exact: true }),
    ).toBeVisible();
    await expect(
      history.getByText("Processing by", { exact: true }),
    ).toBeVisible();
    await expect(history.getByText("Done", { exact: true })).toBeVisible();
    await history.screenshot({ path: "test-results/e2e/wish-indications.png" });
    const order = await page
      .locator(".wish-block, .wish-instructions")
      .evaluateAll((nodes) => nodes.map((node) => node.className));
    expect(order.at(-1)).toContain("wish-instructions");
    await history.getByRole("link", { name: "W1" }).first().click();
    await expect(
      page.locator(`#task-${sample.taskId} .wish-task-heading`),
    ).toBeFocused();
    await page.evaluate(() => localStorage.setItem("djinn.language", "fr"));
    await page.reload();
    await page.locator(".wish-nav").filter({ hasText: sample.title }).click();
    const french = page.getByRole("region", { name: "Indications" });
    await expect(french.getByText("En attente", { exact: true })).toHaveCount(
      2,
    );
    await expect(
      french.getByText("En réflexion", { exact: true }),
    ).toBeVisible();
    await expect(
      french.getByText("Traitement par", { exact: true }),
    ).toBeVisible();
    await expect(french.getByText("Faite", { exact: true })).toBeVisible();
    await page.evaluate(() => localStorage.removeItem("djinn.language"));
    await page.reload();
    await page.locator(".wish-nav").filter({ hasText: sample.title }).click();
    // Processing remains processing after the simulated worker's successful result; only Complete changes it.
    djinn("instruction", "complete", "I03", "--wish-id", sample.wishId);
    await expect(history.getByText("Done", { exact: true })).toHaveCount(2);
    await page.reload();
    await page.locator(".wish-nav").filter({ hasText: sample.title }).click();
    await expect(
      page.getByRole("region", { name: "Indications" }),
    ).toContainText("Offline indication");
  } finally {
    sample.cleanup();
  }
});

test("failed sends retain text and focus; a lost response retries once without duplication", async ({
  page,
}) => {
  const sample = fixture();
  try {
    await page.goto(process.env.DJINN_URL!);
    await foldTerminal(page);
    const card = page.locator(".plan-wish").filter({ hasText: sample.title });
    await card
      .getByRole("button", { name: `Add an indication to ${sample.title}` })
      .click();
    const box = card.getByLabel("An indication for this wish");
    let sends = 0;
    await page.route("**/plan.v1.InstructionService/Send", async (route) => {
      sends++;
      if (sends === 1) {
        await route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({
            code: "unavailable",
            message: "Simulated save error",
          }),
        });
      } else if (sends === 2) {
        await route.fetch(); // The write committed, but its response never reached the page.
        await route.abort("connectionreset");
      } else {
        const response = await route.fetch();
        await new Promise((resolve) => setTimeout(resolve, 250));
        await route.fulfill({ response });
      }
    });
    await box.fill("Keep this draft after errors");
    await box.press("Enter");
    await expect(card.getByRole("alert")).toHaveText("Simulated save error");
    await expect(box).toHaveValue("Keep this draft after errors");
    await expect(box).toBeFocused();
    await expect(box).toHaveAttribute("aria-invalid", "true");
    await box.press("Enter");
    await expect.poll(() => instructions(sample.wishId).length).toBe(5);
    await expect(box).not.toHaveAttribute("readonly", "");
    await expect(box).toHaveValue("Keep this draft after errors");
    await expect(card.getByRole("alert")).toBeVisible();
    await box.press("Enter");
    await box.press("Enter");
    await expect(box).toHaveValue("");
    await expect(box).toBeFocused();
    expect(sends).toBe(3);
    expect(instructions(sample.wishId)).toHaveLength(5);
    expect(
      instructions(sample.wishId).filter(
        (instruction: { text: string }) =>
          instruction.text === "Keep this draft after errors",
      ),
    ).toHaveLength(1);
    await expect(card.getByRole("alert")).toHaveCount(0);
  } finally {
    sample.cleanup();
  }
});
