// Lead replies are durable additions to an indication. They arrive through the wish watch, keep the indication's
// status and worker link, and travel through a wish export/import unchanged.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import {
  openWorkspaceSection,
  workspacePanel,
  wishHeading,
} from "./workspace-selectors";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
const fr = JSON.parse(
  fs.readFileSync(path.join(__dirname, "../locales/fr.json"), "utf8"),
) as Record<string, string>;

function djinn(...args: string[]) {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

function fixture() {
  for (const wish of JSON.parse(djinn("wish", "list", "--json")).wishes ?? [])
    if (wish.state === "WISH_STATE_ACTIVE") djinn("wish", "pause", wish.id);
  const wishId = randomUUID();
  const taskId = randomUUID();
  const title = `Instruction replies ${wishId.slice(0, 8)}`;
  const folder = fs.mkdtempSync(
    path.join(os.tmpdir(), "djinn-instruction-replies-"),
  );
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
          code: "I03",
          text: "Delegated indication",
          status: "INSTRUCTION_STATUS_PROCESSING",
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
    title,
    cleanup: () => {
      try {
        djinn("wish", "pause", wishId);
      } catch {
        // The test may have removed the wish while checking export/import.
      }
      fs.rmSync(folder, { recursive: true, force: true });
    },
  };
}

async function openWish(page: import("@playwright/test").Page, title: string) {
  await page.goto(process.env.DJINN_URL!);
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await expect(wishHeading(page)).toHaveText(title);
}

test("lead replies arrive live, keep lifecycle data, and survive export/import in English and French", async ({
  page,
}) => {
  const sample = fixture();
  const exportDir = fs.mkdtempSync(
    path.join(os.tmpdir(), "djinn-instruction-replies-export-"),
  );
  const exportFile = path.join(exportDir, "wish.json");
  try {
    await openWish(page, sample.title);
    const instructionsPanel = await openWorkspaceSection(page, "instructions");
    const history = instructionsPanel.getByRole("region", {
      name: "Indications",
    });
    await expect(history.getByText("Delegated indication")).toBeVisible();
    await expect(history.locator(".instruction-replies")).toHaveCount(0);

    djinn(
      "instruction",
      "reply",
      "I03",
      "First lead reply",
      "--wish-id",
      sample.wishId,
    );
    const delegated = history
      .locator(".instruction-entry")
      .filter({ hasText: "Delegated indication" });
    await expect(delegated.locator(".instruction-reply p")).toHaveText([
      "First lead reply",
    ]);
    await expect(
      history.getByText("Processing by", { exact: true }),
    ).toBeVisible();
    await expect(delegated.getByRole("link", { name: "W1" })).toBeVisible();
    await expect(
      delegated.locator(".instruction-reply time").first(),
    ).toHaveAttribute("datetime", /T/);

    djinn(
      "instruction",
      "reply",
      "I03",
      "Second lead reply\nwith necessary details on a second line.",
      "--wish-id",
      sample.wishId,
    );
    await expect(delegated.locator(".instruction-reply p")).toHaveText([
      "First lead reply",
      "Second lead reply\nwith necessary details on a second line.",
    ]);

    await delegated.getByRole("link", { name: "W1" }).click();
    await expect(workspacePanel(page, "tasks")).toBeVisible();
    await expect(
      workspacePanel(page, "tasks").locator(".wish-task-heading").first(),
    ).toBeFocused();
    await openWorkspaceSection(page, "instructions");

    djinn("wish", "export", sample.wishId, "--file", exportFile);
    expect(fs.statSync(exportFile).size).toBeGreaterThan(0);
    djinn("wish", "delete", sample.wishId);
    djinn("wish", "import", exportFile);
    await openWish(page, sample.title);

    const restoredInstructionsPanel = await openWorkspaceSection(
      page,
      "instructions",
    );
    const restored = restoredInstructionsPanel.getByRole("region", {
      name: "Indications",
    });
    const restoredDelegated = restored
      .locator(".instruction-entry")
      .filter({ hasText: "Delegated indication" });
    await expect(restoredDelegated.locator(".instruction-reply p")).toHaveText([
      "First lead reply",
      "Second lead reply\nwith necessary details on a second line.",
    ]);
    await expect(
      restored.getByText("Processing by", { exact: true }),
    ).toBeVisible();
    await expect(
      restoredDelegated.getByRole("link", { name: "W1" }),
    ).toBeVisible();
    await expect(
      restoredDelegated.locator(".instruction-reply time").first(),
    ).toHaveAttribute("datetime", /T/);

    djinn("instruction", "complete", "I03", "--wish-id", sample.wishId);
    await expect(
      restoredDelegated.getByText("Done", { exact: true }),
    ).toBeVisible();
    await expect(restoredDelegated.locator(".instruction-reply p")).toHaveCount(
      2,
    );
    await page.screenshot({
      path: path.join(
        __dirname,
        "../",
        "test-results/e2e/instruction-replies-desktop.png",
      ),
      fullPage: true,
    });

    await page.evaluate(() => localStorage.setItem("djinn.language", "fr"));
    await page.setViewportSize({ width: 700, height: 1000 });
    await page.reload();
    await page.locator(".wish-nav").filter({ hasText: sample.title }).click();
    const frenchPanel = await openWorkspaceSection(page, "instructions");
    const french = frenchPanel.getByRole("region", {
      name: fr["instruction.title"],
    });
    const frenchDelegated = french
      .locator(".instruction-entry")
      .filter({ hasText: "Delegated indication" });
    await expect(french).toHaveAttribute("aria-label", fr["instruction.title"]);
    await expect(
      french.getByRole("list", { name: fr["instruction.replies"] }),
    ).toHaveCount(1);
    await expect(
      frenchDelegated.locator(".instruction-reply-label"),
    ).toHaveText([fr["instruction.reply"], fr["instruction.reply"]]);
    await expect(frenchDelegated.locator(".instruction-reply p")).toHaveText([
      "First lead reply",
      "Second lead reply\nwith necessary details on a second line.",
    ]);
    await expect(
      frenchDelegated.locator(".instruction-reply time").first(),
    ).toHaveAttribute("datetime", /T/);
    const lastFrenchReply = frenchDelegated
      .locator(".instruction-reply")
      .last();
    await lastFrenchReply.evaluate((element) =>
      element.scrollIntoView({ block: "center", inline: "nearest" }),
    );
    await expect(
      frenchDelegated.locator(".instruction-reply").first(),
    ).toBeVisible();
    await expect(lastFrenchReply).toBeVisible();
    await page.screenshot({
      path: path.join(
        __dirname,
        "../",
        "test-results/e2e/instruction-replies-narrow-viewport.png",
      ),
    });
    await french.screenshot({
      path: path.join(
        __dirname,
        "../",
        "test-results/e2e/instruction-replies-narrow.png",
      ),
    });
  } finally {
    sample.cleanup();
    fs.rmSync(exportDir, { recursive: true, force: true });
  }
});
