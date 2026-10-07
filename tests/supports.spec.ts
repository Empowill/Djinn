import { test, expect, type Page } from "@playwright/test";
import { build } from "esbuild";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
let bundle = "",
  styles = "";
test.beforeAll(async () => {
  const result = await build({
    stdin: {
      contents: `export {default as React} from 'react';export {createRoot} from 'react-dom/client';export {default as Workspace} from './src/artifact-workspace';export {VisualizationFrame} from './src/visualization-frame';export {ArtifactChatCard} from './src/agent-chat';`,
      resolveDir: process.cwd(),
    },
    bundle: true,
    format: "esm",
    write: false,
    outfile: "/tmp/djinn-supports.js",
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
const artifact = (
  id = "doc",
  content = "# Plan\n\n| Choix | Valeur |\n| --- | --- |\n| A | 42 |\n\n- [x] Validé",
) => ({
  id,
  title: id === "doc" ? "Plan Markdown" : "Autre document",
  type: "document",
  content,
  updatedAt: "2026-10-06T08:00:00.000Z",
  revision: 1,
  editedBy: "agent",
  stepId: "s1",
});
const fixture = () => ({
  id: "test",
  title: "Supports",
  phase: "brief",
  status: "idle",
  project: "/tmp",
  provider: "codex",
  model: "configured",
  createdAt: "2026-10-06T08:00:00.000Z",
  activeStepId: "s1",
  selectedStepId: "s1",
  artifacts: [artifact(), artifact("other", "# Autre")],
  feedback: [],
  agents: [],
  questions: [],
  events: [],
  brief: "Test",
  configuration: {
    concurrency: 1,
    prototype: "",
    review: "",
    deliverables: [],
  },
});
async function harness(page: Page, kind = "workspace", data: any = fixture()) {
  await page.route("**/supports-bundle.js", (route) =>
    route.fulfill({ contentType: "text/javascript", body: bundle }),
  );
  await page.route("**/supports-bundle.css", (route) =>
    route.fulfill({ contentType: "text/css", body: styles }),
  );
  await page.route("**/__supports_test__", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><html data-motion="reduced"><head><link rel="stylesheet" href="/supports-bundle.css"><style>body{margin:0;background:#171717;color:#eee;font-family:system-ui}button{cursor:pointer}*{box-sizing:border-box}</style></head><body><div id="root"></div><script type="module">
import {React,createRoot,Workspace,VisualizationFrame,ArtifactChatCard} from '/supports-bundle.js';
const root=createRoot(document.getElementById('root'));
window.__data=${JSON.stringify(data).replace(/</g, "\\u003c")};
window.__saveCount=0;
window.__hide=()=>root.render(null);
window.__render=()=>root.render(React.createElement(${kind === "workspace" ? "Workspace" : kind === "card" ? "ArtifactChatCard" : "VisualizationFrame"},${kind === "workspace" ? "{task:window.__data,onUpdate:next=>{window.__data=next;window.__saveCount++;window.__render()},onToast:()=>{}}" : "{artifact:window.__data}"}));window.__render();
</script></body></html>`,
    }),
  );
  await page.goto("/__supports_test__");
}

test("Lecture, Édition, Aperçu preserve draft, GFM and human revision history", async ({
  page,
}) => {
  await harness(page);
  await expect(page.locator(".ac-markdown table")).toContainText("42");
  await expect(page.locator(".ac-markdown input")).toBeDisabled();
  await page.getByRole("button", { name: "Édition", exact: true }).click();
  await page
    .getByRole("textbox", { name: "Contenu du support" })
    .fill("# Modifié par vous\n\n**Brouillon**");
  await page.evaluate(() => (window as any).__hide());
  await expect(
    page.getByRole("textbox", { name: "Contenu du support" }),
  ).toHaveCount(0);
  await page.evaluate(() => (window as any).__render());
  await page.getByRole("button", { name: "Aperçu", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Modifié par vous" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Lecture", exact: true }).click();
  await expect(page.locator(".ac-markdown h1")).toHaveText("Plan");
  await page
    .locator(".art-artifact-card")
    .filter({ hasText: "Autre document" })
    .click();
  await page
    .locator(".art-artifact-card")
    .filter({ hasText: "Plan Markdown" })
    .click();
  await page.getByRole("button", { name: "Édition", exact: true }).click();
  await expect(
    page.getByRole("textbox", { name: "Contenu du support" }),
  ).toHaveValue("# Modifié par vous\n\n**Brouillon**");
  await page.getByRole("button", { name: "Enregistrer", exact: true }).click();
  const saved = await page.evaluate(() => (window as any).__data.artifacts[0]);
  expect(saved.content).toContain("Modifié par vous");
  expect(saved.editedBy).toBe("human");
  expect(saved.revision).toBe(2);
  expect(saved.revisions[0].content).toContain("# Plan");
});

test("incoming revision never replaces a dirty human draft and is saved as distinct proposal", async ({
  page,
}) => {
  await harness(page);
  await page.getByRole("button", { name: "Édition", exact: true }).click();
  await page
    .getByRole("textbox", { name: "Contenu du support" })
    .fill("# Brouillon humain");
  await page.evaluate(() => {
    const w = window as any;
    w.__data = {
      ...w.__data,
      artifacts: w.__data.artifacts.map((a: any) =>
        a.id === "doc"
          ? { ...a, content: "# Nouvelle version agent", revision: 2 }
          : a,
      ),
    };
    w.__render();
  });
  await expect(page.getByRole("alert")).toContainText("pendant votre édition");
  await expect(
    page.getByRole("textbox", { name: "Contenu du support" }),
  ).toHaveValue("# Brouillon humain");
  await expect(
    page.getByRole("button", { name: "Enregistrer", exact: true }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "Conserver mon brouillon comme révision" })
    .click();
  await expect(
    page.getByRole("region", { name: "Proposition de révision" }),
  ).toBeVisible();
  const saved = await page.evaluate(() => (window as any).__data.artifacts);
  expect(saved.find((a: any) => a.id === "doc").content).toBe(
    "# Nouvelle version agent",
  );
  expect(saved.find((a: any) => a.id.includes(":proposal:")).content).toBe(
    "# Brouillon humain",
  );
});

test("agent revision proposal compares original sources without overwriting them", async ({
  page,
}) => {
  const task = fixture();
  task.artifacts.push({
    ...artifact("doc:proposal:agent", "# Proposition"),
    title: "Plan — proposition de révision",
  });
  await harness(page, "workspace", task);
  await page
    .locator(".art-artifact-card")
    .filter({ hasText: "Plan — proposition" })
    .click();
  await page.getByText("Comparer les sources", { exact: true }).click();
  await expect(page.locator(".art-revision-comparison")).toContainText(
    "# Plan",
  );
  await expect(page.locator(".art-revision-comparison")).toContainText(
    "# Proposition",
  );
  expect(await page.evaluate(() => (window as any).__saveCount)).toBe(0);
});

test("local scripts are interactive, resources/network blocked, no bridge or parent access, forged height ignored", async ({
  page,
}) => {
  const outbound: string[] = [];
  await page.route("https://isolated.invalid/**", (route) => {
    outbound.push(route.request().url());
    return route.abort();
  });
  const html = `<button id="count">Compteur 0</button><p id="proof"></p><script>
let count=0;document.querySelector('#count').onclick=()=>document.querySelector('#count').textContent='Compteur '+(++count);
let parentBlocked=false;try{parent.document.body}catch(e){parentBlocked=true}
fetch('https://isolated.invalid/fetch').catch(()=>document.body.dataset.fetch='blocked');
const img=document.createElement('img');img.src='https://isolated.invalid/img';document.body.append(img);
const frame=document.createElement('iframe');frame.src='https://isolated.invalid/frame';document.body.append(frame);
const script=document.createElement('script');script.src='https://isolated.invalid/script';document.body.append(script);
document.querySelector('#proof').textContent='bridge:'+typeof window.djinn+' require:'+typeof require+' parent:'+parentBlocked;
</script>`;
  await harness(page, "visualization", {
    ...artifact(),
    type: "visualization",
    content: html,
  });
  const frame = page.frameLocator('iframe[title="Plan Markdown"]');
  await expect(frame.locator("#proof")).toHaveText(
    "bridge:undefined require:undefined parent:true",
  );
  await frame.getByRole("button", { name: "Compteur 0" }).click();
  await expect(frame.getByRole("button", { name: "Compteur 1" })).toBeVisible();
  await expect(frame.locator("body")).toHaveAttribute("data-fetch", "blocked");
  const height = await page
    .locator('iframe[title="Plan Markdown"]')
    .evaluate((node) => node.style.height);
  await page.evaluate(() =>
    window.postMessage(
      { type: "djinn:visualization-height", token: "forged", height: 3999 },
      "*",
    ),
  );
  await page.waitForTimeout(150);
  expect(
    await page
      .locator('iframe[title="Plan Markdown"]')
      .evaluate((node) => node.style.height),
  ).toBe(height);
  expect(outbound).toEqual([]);
  await page.getByRole("button", { name: "Afficher la source" }).click();
  await expect(page.locator(".visualization-source")).toContainText(
    "isolated.invalid",
  );
});

test("visualization script errors remain readable with access to source", async ({
  page,
}) => {
  await harness(page, "visualization", {
    ...artifact(),
    type: "visualization",
    content: '<h1>Rendu partiel</h1><script>throw new Error("oops")</script>',
  });
  await expect(page.getByRole("alert")).toContainText("erreur de script");
  await page.getByRole("button", { name: "Afficher la source" }).click();
  await expect(page.locator(".visualization-source")).toContainText(
    "throw new Error",
  );
});

test("chat visualization card shows the interactive reply directly and can collapse", async ({
  page,
}) => {
  await harness(page, "card", {
    ...artifact(),
    type: "visualization",
    title: "Simulation locale",
    content: "<button onclick=\"this.textContent='Manipulé'\">Essayer</button>",
  });
  await expect(page.locator("iframe")).toHaveCount(1);
  await page
    .frameLocator("iframe")
    .getByRole("button", { name: "Essayer" })
    .click();
  await expect(
    page.frameLocator("iframe").getByRole("button", { name: "Manipulé" }),
  ).toBeVisible();
  await page.getByRole("button", { name: /Simulation locale/ }).click();
  await expect(page.locator("iframe")).toHaveCount(0);
});
