// The window and the command line on one djinn (see global-setup.ts): a wish made by the command line shows in the
// window as it is made, and a question answered in the window reads as answered by the command line.
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

test("a wish made by the command line shows live, and its question is answered from the window", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(process.env.DJINN_URL!);
  await expect(
    page.locator(".app-statusbar").getByText("En direct"),
  ).toBeVisible();

  // Made while the page is open: no reload.
  const title = `Light the way ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish.id as string;
  const item = page.locator(".wish-nav").filter({ hasText: title });
  await expect(item).toBeVisible();
  await item.click();
  await expect(page.locator(".hero h1")).toHaveText(title);

  // A question asked by the command line comes in, open, with its options.
  djinn(
    "question",
    "ask",
    "Which lamp first?",
    wishId,
    "--options",
    "Brass — the old one",
    "--options",
    "Glass — the new one",
    "--recommendation",
    "**B**: it is lighter.",
  );
  const card = page
    .locator(".question-card")
    .filter({ hasText: "Which lamp first?" });
  await expect(card).toBeVisible();
  await expect(card.getByText("it is lighter.")).toBeVisible();

  // Answered in the window: B, with a note.
  await card.getByRole("button", { name: /Glass — the new one/ }).click();
  await card.getByLabel("Note jointe à la réponse").fill("Glass, then brass.");
  await card.getByRole("button", { name: "Valider ce choix" }).click();
  await expect(page.locator(".decisions-section")).toHaveCount(0);
  await expect(page.getByText("1 décision enregistrée")).toBeVisible();

  // The command line reads it answered.
  const [question] = JSON.parse(
    djinn("question", "list", "--wish-id", wishId, "--json"),
  ).questions;
  expect(question.answer.choice).toBe("CHOICE_B");
  expect(question.answer.note).toBe("Glass, then brass.");

  await page.screenshot({
    path: path.join(__dirname, "../test-results/e2e/wish-live.png"),
  });
  expect(errors).toEqual([]);
});
