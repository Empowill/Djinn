// The text size of the personal space (W101): today's size by default, then Compact and Very compact condense the whole
// window, a question card with it, and Large grows it; the step is kept by the page and set before its first paint, so a
// reload draws at once at the chosen size. Shots of each step go to test-results/e2e/text-size-*.png.
import { type Page, expect, test } from "@playwright/test";
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

type Wish = { id: string; state: string };
const wishes = (): Wish[] =>
  JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];

// choose picks a step in the personal space, then closes it.
async function choose(page: Page, label: string) {
  await page.locator(".profile").click();
  const select = page.getByRole("combobox", { name: "Text size" });
  await select.selectOption({ label });
  await page.keyboard.press("Escape");
  await expect(select).toHaveCount(0);
}

const rootSize = (page: Page) =>
  page.evaluate(() => getComputedStyle(document.documentElement).fontSize);

test("the text size condenses the window, kept and set before the first paint", async ({
  page,
}) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  for (const w of wishes().filter((w) => w.state === "WISH_STATE_ACTIVE"))
    djinn("wish", "pause", w.id);
  const title = `Text size lamp ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  try {
    djinn(
      "question",
      "ask",
      "Which oil for the lamp?",
      wishId,
      "--options",
      "Olive",
      "--options",
      "Paraffin",
      "--context",
      "**Why**: the lamp burns one oil at a time.\n\n- Olive burns *slowly*.\n- Paraffin burns *bright*.",
    );
    // Before the page's script runs: the root's font size when the app first draws into the page.
    await page.addInitScript(() => {
      new MutationObserver((_, observer) => {
        const app = document.getElementById("root");
        if (!app?.firstElementChild) return;
        (window as unknown as { firstDraw: string }).firstDraw =
          getComputedStyle(document.documentElement).fontSize;
        observer.disconnect();
      }).observe(document, { childList: true, subtree: true });
    });
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto(process.env.DJINN_URL!);
    await page.evaluate(() => localStorage.removeItem("djinn.text-size"));
    await page.reload();
    await expect(page.locator(".hero h1")).toHaveText("Flying carpet");
    const card = page
      .locator(".question-column .question-row.current")
      .filter({ hasText: "Which oil for the lamp?" });
    await expect(card).toBeVisible();
    const height = () => card.evaluate((e) => e.getBoundingClientRect().height);
    const shot = (step: string) =>
      page.screenshot({
        path: path.join(__dirname, `../test-results/e2e/text-size-${step}.png`),
      });

    // Today's size by default: 16px to the rem, 12px of body text.
    expect(await rootSize(page)).toBe("16px");
    await expect(page.locator("body")).toHaveCSS("font-size", "12px");
    await expect(page.locator("html")).not.toHaveAttribute("data-text-size");
    const normal = await height();
    await shot("default");

    // Compact: at once, the card shorter, its text 11px.
    await choose(page, "Compact");
    await expect(page.locator("html")).toHaveAttribute(
      "data-text-size",
      "compact",
    );
    await expect(page.locator("body")).toHaveCSS("font-size", "11px");
    const compact = await height();
    expect(compact).toBeLessThan(normal * 0.97);
    await shot("compact");

    // Kept: a reload draws at the chosen size from its first frame.
    await page.reload();
    await expect(card).toBeVisible();
    expect(
      await page.evaluate(
        () => (window as unknown as { firstDraw: string }).firstDraw,
      ),
    ).toBe("14.6667px");
    expect(await height()).toBeCloseTo(compact, 0);

    // Very compact, shorter still; Large, taller than the default.
    await choose(page, "Very compact");
    await expect(page.locator("body")).toHaveCSS("font-size", "10px");
    const veryCompact = await height();
    expect(veryCompact).toBeLessThan(compact);
    await shot("very-compact");
    await choose(page, "Large");
    await expect(page.locator("body")).toHaveCSS("font-size", "14px");
    expect(await height()).toBeGreaterThan(normal);
    await shot("large");

    // Back to the default: nothing kept, the card as it was.
    await choose(page, "Default");
    await expect(page.locator("html")).not.toHaveAttribute("data-text-size");
    expect(
      await page.evaluate(() => localStorage.getItem("djinn.text-size")),
    ).toBeNull();
    expect(await height()).toBeCloseTo(normal, 0);
    expect(errors).toEqual([]);
  } finally {
    await page.evaluate(() => localStorage.removeItem("djinn.text-size"));
    djinn("wish", "delete", wishId);
  }
});
