// The wish bar folded and unfolded (L21, Q62 A): each wish's icon, 24px, keeps its place and its size, the row built
// around it; only its states move, from the second line onto the icon's corners and back. Unfolded, the second line
// says them, the small hand and the questions' count; folded, the hand sits on the icon's corner. A paused wish's icon
// is grey; a selected row keeps its ground and takes no hover's tint.
import { type Page, expect, test } from "./test";
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

// The boxes of the wishes' icons whose titles carry tag, in the list's order, once the side panel's fold has ended. Only
// a finite animation is waited for: a loop never ends, such as a working project's flame an earlier spec left lit.
async function icons(page: Page, tag: string) {
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          document
            .querySelector(".sidebar")!
            .getAnimations({ subtree: true })
            .filter(
              (a) => a.effect?.getComputedTiming().iterations !== Infinity,
            ).length,
      ),
    )
    .toBe(0);
  return page.evaluate((tag) => {
    return [...document.querySelectorAll<HTMLElement>(".wish-list .wish-nav")]
      .filter((row) => row.title.includes(tag))
      .map((row) => {
        const box = row.querySelector("svg.wish-icon")!.getBoundingClientRect();
        return { x: box.x, y: box.y, width: box.width, height: box.height };
      });
  }, tag);
}

test("folding the bar moves no wish's icon, only its states", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const tag = randomUUID().slice(0, 6);
  const make = (title: string) =>
    JSON.parse(djinn("wish", "make", title, "--paused", "--json")).wish
      .id as string;
  const lamp = make(`Lamp ${tag}`);
  const mark = make(`Mark ${tag}`);
  const rest = make(`Rest ${tag}`);
  try {
    djinn("wish", "icon", lamp, "--name", "lamp");
    djinn("wish", "icon", rest, "--name", "lamp");
    djinn("wish", "move", mark, "--to", "1");
    djinn("wish", "move", lamp, "--to", "1");
    djinn("question", "ask", "Which brass?", lamp);
    djinn("question", "ask", "Which wick?", lamp);
    await page.setViewportSize({ width: 1200, height: 900 });
    await page.goto(process.env.DJINN_URL!);
    const row = (name: string) =>
      page.locator(`.wish-list .wish-nav[title*="${name} ${tag}"]`);
    const hand = row("Lamp").locator(".wish-emblem .wish-hand-corner");
    const sign = row("Lamp").locator(".wish-counts .wish-sign.tone-waiting");
    await expect(sign).toHaveText("2");
    await expect(hand).toBeHidden();
    await expect(row("Lamp").locator("svg.wish-icon")).toHaveAttribute(
      "width",
      "24",
    );
    await row("Mark").click();

    // Dark on a wide window, light under 1300px, where the side panel's padding is narrower: the icons keep their place.
    for (const [theme, wide] of [
      ["dark", 1400],
      ["light", 1200],
    ] as const) {
      await page.setViewportSize({ width: wide, height: 900 });
      await page.evaluate((v) => {
        document.documentElement.dataset.theme = v;
      }, theme);
      const open = await icons(page, tag);
      const width = await page
        .locator(".sidebar")
        .evaluate((el) => getComputedStyle(el).width);
      expect(open).toHaveLength(3);
      for (const box of open) expect(box.width).toBe(24);
      // The paused wish's icon gives up its colours: line and gilding in one grey.
      const greys = await row("Rest").evaluate((el) =>
        [...el.querySelectorAll(".wish-icon-line, .wish-icon-gild")].map(
          (g) => getComputedStyle(g).color,
        ),
      );
      expect(new Set(greys).size).toBe(1);
      // A selected row takes no hover's tint, a hovered one does, by its veil.
      await row("Mark").hover();
      await expect
        .poll(() =>
          row("Mark").evaluate((el) => getComputedStyle(el, "::after").opacity),
        )
        .toBe("0");
      await row("Lamp").hover();
      await expect
        .poll(() =>
          row("Lamp").evaluate((el) => getComputedStyle(el, "::after").opacity),
        )
        .toBe("1");
      await page.locator(".sidebar").screenshot({
        path: `test-results/e2e/wish-bar-fold-open-${theme}.png`,
      });

      await page.locator(".sidebar-toggle").click();
      await expect(page.locator(".sidebar")).toHaveCSS("width", "67px");
      await page.mouse.move(800, 600);
      const folded = await icons(page, tag);
      expect(folded).toEqual(open);
      await expect(hand).toBeVisible();
      await expect(hand).toHaveText("2");
      await expect(row("Lamp").locator(".wish-counts")).toHaveCount(0);
      await page.locator(".sidebar").screenshot({
        path: `test-results/e2e/wish-bar-fold-folded-${theme}.png`,
      });

      await page.locator(".sidebar-toggle").click();
      await expect(page.locator(".sidebar")).toHaveCSS("width", width);
      expect(await icons(page, tag)).toEqual(open);
      await expect(hand).toBeHidden();
    }
  } finally {
    for (const id of [lamp, mark, rest]) djinn("wish", "delete", id);
  }
  expect(errors).toEqual([]);
});
