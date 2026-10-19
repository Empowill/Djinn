// The wish bar under 820px wide: the CSS narrows it to its icons, the wish unfolded, and hides an active wish's second
// line; the wish's own icon shows, as on the folded rail (L09 v6), with the small hand and the questions' count on its
// corner. Wider, the icon is the same, the hand on its corner stays hidden and the second line says the count.
import { expect, test } from "./test";
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

test("under 820px wide, an active wish with open questions shows its icon and the small hand", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const title = `Narrow ${randomUUID().slice(0, 8)}`;
  const id = JSON.parse(djinn("wish", "make", title, "--paused", "--json")).wish
    .id as string;
  try {
    djinn("wish", "move", id, "--to", "1");
    djinn("question", "ask", "Which version wins?", id);
    await page.setViewportSize({ width: 1200, height: 800 });
    await page.goto(process.env.DJINN_URL!);
    const row = page.locator(`.wish-list .wish-nav[title*="${title}"]`);
    const icon = row.locator(".wish-emblem");
    const hand = icon.locator(".wish-hand-corner");
    const count = row.locator(".wish-counts .wish-sign.tone-waiting");

    // Wide: the second line says the question, the small hand and its count; the hand on the icon stays hidden.
    await expect(count).toBeVisible();
    await expect(count).toHaveText("1");
    await expect(count.locator("svg.waiting-hand")).toBeVisible();
    await expect(hand).toBeHidden();
    await expect(icon.locator("svg.wish-mark")).toBeVisible();

    for (const theme of ["light", "dark"] as const) {
      await page.evaluate((v) => {
        document.documentElement.dataset.theme = v;
      }, theme);
      await page.setViewportSize({ width: 800, height: 800 });
      // Narrow: the second line hides; the wish's mark shows, the hand and its count on its corner.
      await expect(row.locator(".wish-counts")).toBeHidden();
      await expect(icon.locator("svg.wish-mark")).toBeVisible();
      await expect(hand).toBeVisible();
      await expect(hand.locator("svg.waiting-hand")).toBeVisible();
      await expect(hand).toHaveText("1");
      await page.locator(".sidebar").screenshot({
        path: `test-results/e2e/wish-bar-narrow-${theme}.png`,
      });
      await page.setViewportSize({ width: 1200, height: 800 });
      await expect(hand).toBeHidden();
    }
  } finally {
    djinn("wish", "delete", id);
  }
  expect(errors).toEqual([]);
});
