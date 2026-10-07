import { test, expect } from "@playwright/test";
import { build } from "esbuild";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

let offlineScript = "", offlineStyles = "";
test.beforeAll(async () => {
  if (!process.env.DJINN_OFFLINE_E2E) return;
  const bundle = await build({
    entryPoints: ["src/main.tsx"], bundle: true, format: "esm", write: false,
    outfile: "/private/tmp/djinn-review-app.js",
    loader: { ".woff2": "dataurl", ".woff": "dataurl" },
    define: { "process.env.NODE_ENV": '"development"' },
    plugins: [{ name: "raw", setup(b) {
      b.onResolve({ filter: /\?raw$/ }, ({ path, resolveDir }) => ({ path: resolve(resolveDir, path.replace(/\?raw$/, "")), namespace: "raw" }));
      b.onLoad({ filter: /.*/, namespace: "raw" }, async ({ path }) => ({ contents: await readFile(path, "utf8"), loader: "text" }));
    } }],
  });
  for (const output of bundle.outputFiles || []) {
    if (output.path.endsWith(".css")) offlineStyles = output.text;
    else offlineScript = output.text;
  }
});

for (const width of [1440, 1024]) {
  test(`composer and simplified workspace at ${width}px`, async ({ page }) => {
    const pageErrors: string[] = [];
    page.on("pageerror", error => pageErrors.push(error.message));
    await page.setViewportSize({ width, height: 1000 });
    await page.emulateMedia({ reducedMotion: "no-preference" });
    if (process.env.DJINN_OFFLINE_E2E) {
      await page.route("**/review-app.js", r => r.fulfill({ contentType: "text/javascript", body: offlineScript }));
      await page.route("**/review-app.css", r => r.fulfill({ contentType: "text/css", body: offlineStyles }));
      await page.route("http://127.0.0.1:4317/", r => r.fulfill({ contentType: "text/html", body: '<!doctype html><html lang="fr"><head><link rel="stylesheet" href="/review-app.css"></head><body><div id="root"></div><script type="module" src="/review-app.js"></script></body></html>' }));
    }
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Espace projets" })).toBeVisible();
    await page.waitForFunction(() => Boolean(localStorage.getItem("djinn.workspace.v1")));
    await page.evaluate(() => {
      const state = JSON.parse(localStorage.getItem("djinn.workspace.v1")!);
      const task = state.tasks.find((t: { id: string }) => t.id === state.selectedId);
      task.workflowOrigin = "agent";
      task.workflowProposal = {
        stepId: task.activeStepId,
        steps: [{ type: "implementation", title: "Harmoniser", objective: "Interface cohérente" }],
        reason: "Motif historique conservé dans les données",
      };
      state.settings.reduceMotion = true;
      localStorage.setItem("djinn.workspace.v1", JSON.stringify(state));
    });
    await page.reload();
    await expect(page.getByRole("heading", { name: "Espace projets" })).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("data-motion", "reduced");
    expect(await page.evaluate(() => matchMedia("(prefers-reduced-motion: reduce)").matches)).toBe(false);
    await expect(page.getByText("Parcours défini par Djinn", { exact: true })).toHaveCount(0);
    const capsule = page.locator(".composer-capsule");
    await capsule.click();
    const input = page.locator(".mission-composer textarea");
    await expect(input).toBeFocused();
    await expect(input).toHaveCSS("font-size", "12px");
    await input.fill("Brouillon de vérification");
    await page.keyboard.press("Escape");
    await expect(capsule).toBeVisible();
    await expect(capsule).toBeFocused();
    await capsule.click();
    await expect(input).toHaveValue("Brouillon de vérification");
    await expect(input).toBeFocused();
    await page.getByTitle("Connexions et préférences", { exact: true }).click();
    const motionSwitch = page.locator(".setting-row").filter({ hasText: "Réduire les animations" }).getByRole("switch");
    await motionSwitch.click();
    await expect(page.locator("html")).toHaveAttribute("data-motion", "full");
    await motionSwitch.click();
    await expect(page.locator("html")).toHaveAttribute("data-motion", "reduced");
    await page.getByRole("button", { name: "Fermer la fenêtre", exact: true }).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(input).toHaveValue("Brouillon de vérification");
    await input.focus();
    await page.keyboard.press("Escape");
    await expect(capsule).toBeVisible();
    await expect(input).toHaveCount(0);
    await expect(capsule).toHaveCSS("transform", "none");
    await expect(capsule.locator("..")).toHaveCSS("transform", "none");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    expect(pageErrors).toEqual([]);
    await page.screenshot({ path: `/private/tmp/djinn-review-${width}.png`, fullPage: true });
  });
}
