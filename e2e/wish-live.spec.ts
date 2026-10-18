// The window and the command line on one djinn (see global-setup.ts): a wish made by the command line shows in the
// window as it is made, and a question answered in the window, option B picked over the recommended A and the lamp
// rubbed, reads as answered by the command line. Shots of the card, dark and light, go to test-results/e2e/.
import { type Page, expect, test } from "./test";
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

const SHOTS = Boolean(process.env.WISH_LIVE_SHOTS);

// theme switches the page to a theme, "" for the default dark one; the page reloads.
async function theme(page: Page, value: "" | "light") {
  await page.evaluate((v) => {
    if (v) localStorage.setItem("djinn.theme", v);
    else localStorage.removeItem("djinn.theme");
  }, value);
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme",
    value || "dark",
  );
}

test("a wish made by the command line and its question show live in the window", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(process.env.DJINN_URL!);
  await expect(page.locator(".app-statusbar").getByText("Live")).toBeVisible();

  // Made while the page is open: no reload.
  const title = `Light the way ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish.id as string;
  const item = page.locator(".wish-nav").filter({ hasText: title });
  try {
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
      "**A**: it is tested.",
    );
    const card = page
      .locator(".question-card")
      .filter({ hasText: "Which lamp first?" });
    await expect(card).toBeVisible();
    await expect(card.getByText("it is tested.")).toBeVisible();
    // Three gestures at the bottom: Withdraw on the left, Enlighten in the middle, the lamp on the right.
    await expect(card.locator(".question-actions button")).toHaveText([
      /Withdraw question…/,
      /Enlighten me/,
      /Rub the lamp/,
    ]);

    const brass = card.getByRole("button", { name: /Brass — the old one/ });
    const glass = card.getByRole("button", { name: /Glass — the new one/ });
    await expect(brass).toHaveAttribute("aria-pressed", "true");

    if (SHOTS) {
      const pick = async () => {
        await expect(brass).toHaveAttribute("aria-pressed", "true");
        await glass.click();
        await expect(glass).toHaveAttribute("aria-pressed", "true");
        await expect(brass).toHaveAttribute("aria-pressed", "false");
        await card
          .getByLabel("Note with the answer")
          .fill("Glass, then brass.");
        await expect(card).toHaveCSS("opacity", "1");
        await page
          .locator(".mission-scroll")
          .evaluate((el) => el.scrollTo(0, el.scrollHeight));
      };
      const shots = path.join(__dirname, "../test-results/e2e");
      await pick();
      await card.screenshot({
        path: path.join(shots, "question-card-dark.png"),
      });
      await theme(page, "light");
      await item.click();
      await pick();
      await card.screenshot({
        path: path.join(shots, "question-card-light.png"),
      });
      await card.getByRole("button", { name: "Rub the lamp" }).click();
      await expect(page.locator(".question-column .question-card")).toHaveCount(
        0,
      );
      await expect(page.getByRole("tab", { name: /^Decisions/ })).toHaveText(
        "Decisions1",
      );
      await page.screenshot({
        path: path.join(__dirname, "../test-results/e2e/wish-live.png"),
      });
      await theme(page, "");
    }

    expect(errors).toEqual([]);
  } finally {
    try {
      djinn("wish", "delete", wishId);
    } catch {}
  }
});
