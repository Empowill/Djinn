import { test, expect } from "@playwright/test";
import { mkdir } from "node:fs/promises";

test("capture the public demonstration for the README", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "Espace projets", exact: true }),
  ).toBeVisible();
  await page.waitForFunction(() =>
    Boolean(localStorage.getItem("djinn.workspace.v1")),
  );
  await page.evaluate(() => {
    const state = JSON.parse(localStorage.getItem("djinn.workspace.v1")!);
    state.settings.reduceMotion = true;
    localStorage.setItem("djinn.workspace.v1", JSON.stringify(state));
  });
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-motion", "reduced");
  await expect(
    page.getByRole("heading", { name: "Espace projets", exact: true }),
  ).toBeVisible();
  await mkdir("docs/screenshots/readme", { recursive: true });
  await expect(page.locator(".tab-content")).toHaveCSS("opacity", "1");
  await expect(page.locator(".agent-card").first()).toHaveCSS("opacity", "1");
  await page.screenshot({ path: "docs/screenshots/readme/mission.png" });
  await page
    .getByRole("navigation", { name: "Vues de la mission" })
    .getByRole("button", { name: /^Restitution/ })
    .click();
  await page
    .locator(".artifact-result-card")
    .filter({ hasText: "Laboratoire interactif · graphiques et mini-app" })
    .getByRole("button", { name: "Ouvrir le support" })
    .click();
  await expect(page.locator(".art-main-heading h1")).toContainText(
    "Laboratoire interactif",
  );
  await expect(page.locator(".art-workspace iframe")).toBeVisible();
  await expect(page.locator(".tab-content")).toHaveCSS("opacity", "1");
  await page
    .locator(".art-workspace")
    .evaluate((element) => element.scrollIntoView({ block: "start" }));
  const bounds = await page.locator(".art-workspace").boundingBox();
  expect(bounds).not.toBeNull();
  const y = Math.max(0, bounds!.y);
  await page.screenshot({
    path: "docs/screenshots/readme/supports.png",
    clip: {
      x: bounds!.x,
      y,
      width: bounds!.width,
      height: Math.min(900, 1000 - y - 28),
    },
  });
  expect(errors).toEqual([]);
});
