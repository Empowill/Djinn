// A question's option that launches a task once picked (decision Q25, W99), against the real djinn up --browser (see
// global-setup.ts): its card says so on one grey line under the option, Launches: its title and its azima, and nothing
// under an option without a plan. Rubbing the lamp on it without a note launches the task at once; the Decisions tab
// then links the answered question to the task it launched. The worker is the fake agent: no model is called. Every
// run makes fresh identifiers, so --repeat-each works on the same djinn. A screenshot of the card goes to
// test-results/e2e/.
import { expect, test } from "./test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { portable } from "./portable";

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

test("an option with a plan says what it launches; the answer launches it and links it", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const title = `Plan the oil ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  try {
    const azima = JSON.parse(
      djinn(
        "task",
        "spawn",
        wishId,
        "--kind",
        "azima",
        "--title",
        "Lighting",
        "--json",
      ),
    ).task as { code: string };
    // A long title: the line stays one line, cut short.
    const planned =
      `Fill the lamp with olive oil ${"and trim the wick ".repeat(12)}`.trim();
    const file = path.join(
      fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-plans-")),
      "a.md",
    );
    fs.writeFileSync(
      file,
      `option: A\ntitle: ${planned}\npart-of: ${azima.code}\nprovider: fake\n\ntext filled\n`,
    );
    djinn(
      "question",
      "ask",
      "Which oil for the wick?",
      wishId,
      "--options",
      "Olive",
      "--options",
      "Paraffin",
      "--recommendation",
      "A: it smells better.",
      "--plan-file",
      file,
    );
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();

    const card = page
      .locator(".question-card")
      .filter({ hasText: "Which oil for the wick?" });
    const options = card.locator(".option");
    await expect(options).toHaveCount(2);
    const line = options.nth(0).locator(".option-plan");
    await expect(line).toHaveText(
      `Launches: ${planned}· ${azima.code} Lighting`,
    );
    // The title is cut short, the azima never.
    await expect(line.locator(".option-plan-azima")).toBeInViewport({
      ratio: 1,
    });
    const azimaBox = await line.locator(".option-plan-azima").boundingBox();
    await expect(options.nth(1).locator(".option-plan")).toHaveCount(0);
    // One line, under the option's text, inside its box: cut short, the whole of it in its tooltip.
    const box = await line.boundingBox();
    expect(box!.height).toBeLessThanOrEqual(20);
    const option = await options.nth(0).boundingBox();
    expect(box!.x + box!.width).toBeLessThanOrEqual(
      option!.x + option!.width + 0.5,
    );
    expect(azimaBox!.x + azimaBox!.width).toBeLessThanOrEqual(
      box!.x + box!.width + 0.5,
    );
    await expect(line).toHaveAttribute(
      "title",
      `Launches: ${planned} · ${azima.code} Lighting`,
    );

    await card.screenshot({ path: path.join(shots, "question-plans.png") });

    // A plain answer on A launches its plan at once.
    await card.getByRole("button", { name: "Rub the lamp" }).click();
    await expect(card).toHaveCount(0);
    const tab = page.getByRole("tab", { name: /^Decisions/ });
    await tab.click();
    const row = page
      .getByRole("tabpanel", { name: "Decisions" })
      .locator(".decision-row")
      .filter({ hasText: "Which oil for the wick?" });
    const launched = row.locator(".decision-launched");
    await expect(launched).toContainText("Launched as planned");
    await expect(launched.getByRole("link")).toHaveText(/^W\d+$/);
    // The launched task is on its own line, not again among the tasks it led to.
    await expect(row.locator(".decision-tasks")).toHaveCount(1);
    const code = (await launched.getByRole("link").textContent())!;
    await launched.getByRole("link").click();
    await expect(page.getByRole("tab", { name: /^Tasks/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    const task = page.locator(".wish-task").filter({ hasText: code });
    await expect(task).toHaveClass(/focused/);
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "pause", wishId);
  }
});
