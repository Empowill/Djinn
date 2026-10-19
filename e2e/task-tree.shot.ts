// Screenshots of the Tasks tab as a tree on a wish of real size (bigwish.ts), its dates' column left of the lanes, dark
// and light, in French in Paris's time zone and in English, into test-results/tree-shots/ (or TREE_SHOTS): to look at
// the tree before and after a change. Run with `go tool task screenshots -- e2e/task-tree.shot.ts`.
import { type Page, expect, test } from "@playwright/test";
import path from "node:path";

import { importBigWish, removeBigWish } from "./bigwish";

const shots =
  process.env.TREE_SHOTS ?? path.join(__dirname, "../test-results/tree-shots");

test.use({ timezoneId: "Europe/Paris" });

async function shoot(page: Page, language: "fr" | "en") {
  for (const theme of ["dark", "light"] as const) {
    await page.evaluate(
      ([v, l]) => {
        localStorage.setItem("djinn.theme", v);
        localStorage.setItem("djinn.language", l);
      },
      [theme, language],
    );
    await page.reload();
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
    await page.locator("#view-tab-tasks").click();
    await expect(page.locator(".task-tree .tree-date").first()).toBeVisible();
    // A row hovered, its tooltip open: which moment its date is, and its end.
    const row = page.locator(".task-tree .tree-row").nth(-6);
    await row.evaluate((el) => {
      el.style.scrollMarginTop = "200px";
      el.scrollIntoView({ block: "center" });
    });
    await row.hover();
    await expect(page.locator(".tree-tip")).toBeVisible();
    await page.screenshot({
      path: path.join(shots, `tree-${language}-${theme}.png`),
      style: ".app-statusbar > span:nth-child(2) { visibility: hidden }",
    });
  }
}

test("the Tasks tab as a tree, its dates' column", async ({ page }) => {
  test.setTimeout(180_000);
  const wishId = importBigWish("real");
  try {
    await page.goto(process.env.DJINN_URL!);
    await page.evaluate((id) => {
      localStorage.setItem("djinn.wish", id);
      localStorage.setItem("djinn.terminal.collapsed", "1");
      localStorage.setItem("djinn.tasks.layout", "tree");
    }, wishId);
    await page.setViewportSize({ width: 1440, height: 1000 });
    await shoot(page, "fr");
    await shoot(page, "en");
  } finally {
    await page.evaluate(() => {
      localStorage.removeItem("djinn.language");
      localStorage.removeItem("djinn.tasks.layout");
    });
    removeBigWish(wishId);
  }
});
