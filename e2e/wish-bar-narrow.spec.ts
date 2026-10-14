// The wish bar under 820px wide: the CSS narrows it to its icons, the wish unfolded, and hides an active wish's second
// line; its icon, which leaves the raised hand to the question count on that line, gives way to the hand, as on the
// folded rail. Wider, the hand stays hidden and the count says it.
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

test("under 820px wide, an active wish with open questions shows the raised hand", async ({
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
    const icon = row.locator(".wish-tone");
    const hand = icon.locator(".wish-hand");
    const count = row.locator(".wish-counts .count-pill.tone-waiting");

    // Wide: the count says the question on the second line; the hand stays hidden.
    await expect(count).toBeVisible();
    await expect(count).toHaveText("1");
    await expect(hand).toBeHidden();

    for (const theme of ["light", "dark"] as const) {
      await page.evaluate((v) => {
        document.documentElement.dataset.theme = v;
      }, theme);
      await page.setViewportSize({ width: 800, height: 800 });
      // Narrow: the second line hides, the hand takes the icon's place, in the questions' colour.
      await expect(row.locator(".wish-counts")).toBeHidden();
      await expect(hand).toBeVisible();
      await expect(hand.locator("svg.lucide-hand")).toBeVisible();
      expect(
        await icon.evaluate(
          (el) =>
            [...el.children].filter(
              (c) => getComputedStyle(c).display !== "none",
            ).length,
        ),
      ).toBe(1);
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
