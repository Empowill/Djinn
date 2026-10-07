import { test, expect, type Page } from "@playwright/test";
import { build } from "esbuild";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
let bundle = "", styles = "";
test.beforeAll(async () => {
  const result = await build({
    stdin: { contents: `export {default as React} from 'react';export {createRoot} from 'react-dom/client';export {MarkdownBody} from './src/markdown-body';export {ArtifactChatCard} from './src/agent-chat';export {demoVisualization,enrichDemoState,demoArtifacts} from './src/demo-supports';`, resolveDir: process.cwd() },
    bundle: true, format: "esm", write: false, outfile: "/tmp/djinn-visual-tests.js",
    define: { "process.env.NODE_ENV": '"development"' },
    plugins: [{ name: "raw", setup(b) {
      b.onResolve({ filter: /\?raw$/ }, ({ path, resolveDir }) => ({ path: resolve(resolveDir, path.replace(/\?raw$/, "")), namespace: "raw" }));
      b.onLoad({ filter: /.*/, namespace: "raw" }, async ({ path }) => ({ contents: await readFile(path, "utf8"), loader: "text" }));
    } }],
  });
  for (const output of result.outputFiles || []) {
    if (output.path.endsWith(".css")) styles = output.text;
    else bundle = output.text;
  }
});
async function harness(page: Page, mode: "visual" | "mermaid" = "visual", source = "flowchart LR\n  A[Mission] --> B[Review]") {
  await page.route("**/visual-tests.js", r => r.fulfill({ contentType: "text/javascript", body: bundle }));
  await page.route("**/visual-tests.css", r => r.fulfill({ contentType: "text/css", body: styles }));
  const markdownLiteral = JSON.stringify("```mermaid\n" + source + "\n```").replaceAll("<", "\\u003c");
  await page.route("**/__visual_tests__", r => r.fulfill({ contentType: "text/html", body: `<!doctype html><html><head><link rel="stylesheet" href="/visual-tests.css"></head><body><main id="root"></main><script type="module">import {React,createRoot,MarkdownBody,ArtifactChatCard,demoVisualization,enrichDemoState,demoArtifacts} from '/visual-tests.js';window.enrichDemoState=enrichDemoState;window.demoArtifacts=demoArtifacts;createRoot(document.getElementById('root')).render(React.createElement(${mode === "visual" ? "ArtifactChatCard,{artifact:{id:'lab',title:'Laboratoire',type:'visualization',content:demoVisualization}}" : `MarkdownBody,{text:${markdownLiteral}}`}));</script></body></html>` }));
  await page.goto("/__visual_tests__");
}

test("inline demo charts, filters and mini-app work at 12px without native access", async ({ page }) => {
  await harness(page);
  const frame = page.frameLocator('iframe[title="Laboratoire"]');
  await expect(frame.getByRole("heading", { name: "Graphique interactif" })).toBeVisible();
  await expect(frame.locator("#total")).toHaveText("Total simulé : 129 tâches.");
  await frame.getByLabel("Volume du scénario").fill("150");
  await expect(frame.locator("#total")).toHaveText("Total simulé : 195 tâches.");
  await frame.getByRole("button", { name: "Courbe", exact: true }).click();
  await expect(frame.locator("#curve")).toBeVisible();
  await expect(frame.locator("#bars")).toBeHidden();
  await frame.getByLabel("Rechercher un projet").fill("Nova");
  await expect(frame.locator("#projects tr")).toHaveCount(1);
  await frame.getByLabel("Statut", { exact: true }).selectOption("done");
  await expect(frame.locator("#count")).toHaveText("Aucun projet correspondant.");
  await frame.getByRole("button", { name: "Faire avancer" }).first().click();
  await frame.getByRole("button", { name: "Faire avancer" }).first().click();
  await expect(frame.locator("#progress")).toHaveText("1 tâche(s) terminée(s) sur 3.");
  await frame.getByRole("button", { name: "Réinitialiser" }).click();
  await expect(frame.locator("#total")).toHaveText("Total simulé : 129 tâches.");
  await expect(frame.locator("#projects tr")).toHaveCount(4);
  expect(await frame.locator("button").first().evaluate(el => getComputedStyle(el).fontSize)).toBe("12px");
  expect(await frame.locator("body").evaluate(() => {
    let parentAccessible = false;
    try { parentAccessible = Boolean(parent.document); } catch { /* opaque sandbox origin */ }
    return { bridge: Boolean((window as any).djinn), parentAccessible };
  })).toEqual({ bridge: false, parentAccessible: false });
  await page.getByRole("button", { name: "Afficher la source" }).click();
  await expect(page.locator(".visualization-source")).toContainText("Graphique interactif");
});

test("Mermaid renders locally, exposes source and expands with safe strict output", async ({ page }) => {
  await harness(page, "mermaid");
  const frame = page.frameLocator('iframe[title="Diagramme Mermaid"]').first();
  await expect(frame.locator("svg")).toBeVisible();
  await expect(frame.locator("svg")).toContainText("Mission");
  await page.getByRole("button", { name: "Afficher la source" }).click();
  await expect(page.locator(".mermaid-support pre")).toContainText("flowchart LR");
  await page.getByRole("button", { name: "Agrandir" }).click();
  await expect(page.getByRole("dialog", { name: "Diagramme agrandi" })).toBeVisible();
  await expect(page.frameLocator('dialog iframe').locator("svg")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toBeHidden();
  expect(await frame.locator("body").evaluate(() => Boolean((window as any).djinn))).toBe(false);
});

test("invalid Mermaid keeps readable source and fails without executing markup", async ({ page }) => {
  await harness(page, "mermaid", 'not a diagram </script><script>parent.document.body.dataset.injected="yes"</script>');
  await expect(page.getByRole("alert")).toContainText("Mermaid ne peut pas rendre");
  await page.getByRole("button", { name: "Afficher la source" }).click();
  await expect(page.locator(".mermaid-support pre")).toContainText("not a diagram");
  expect(await page.locator("body").getAttribute("data-injected")).toBeNull();
});

test("saved demos gain examples idempotently without overwriting human edits", async ({ page }) => {
  await harness(page);
  const result = await page.evaluate(() => {
    const w = window as any;
    const task = { id: "demo", demo: true, createdAt: "2026-10-06T08:00:00.000Z", activeStepId: "s1", artifacts: [{ id: "demo-visualization", content: "Édition humaine", sourceOfTruth: true, revision: 3, editedBy: "human" }], events: [], steps: [{ id: "s1" }] };
    const first = w.enrichDemoState({ tasks: [task, { ...task, id: "real", demo: false }] });
    const second = w.enrichDemoState(first);
    return { human: second.tasks[0].artifacts[0], ids: second.tasks[0].artifacts.map((a: any) => a.id), count: first.tasks[0].events.length, secondCount: second.tasks[0].events.length, real: second.tasks[1].artifacts.length };
  });
  expect(result.human).toMatchObject({ content: "Édition humaine", sourceOfTruth: true, revision: 3 });
  expect(new Set(result.ids).size).toBe(5);
  expect(result.count).toBe(result.secondCount);
  expect(result.real).toBe(1);
});
