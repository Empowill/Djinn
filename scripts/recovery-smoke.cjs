"use strict";
const fs = require("node:fs/promises"), path = require("node:path"), os = require("node:os");
const { _electron } = require("playwright");
const { expect } = require("@playwright/test");

async function main() {
  const root = path.resolve(__dirname, "..");
  const temp = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), "djinn-recovery-smoke-")));
  const profile = path.join(temp, "profile");
  await fs.mkdir(profile);
  const time = "2026-10-07T18:07:12.065Z";
  const { taskFixture } = require("../tests/workflow-fixture.cjs");
  const marker = "DJINN_EVENT:" + JSON.stringify({ type: "artifact", data: { id: "legacy-report", type: "markdown", title: "Restitution récupérée", content: "Recette date à vérifier." } });
  const stage = { id: "legacy-review", type: "review", title: "Intégrer les corrections", objective: "Vérifier les deux corrections", status: "paused", validation: "human", summary: "La recette date reste à vérifier.", skills: [], exitCriteria: [], expectedArtifacts: [] };
  const task = taskFixture({ id: "legacy-recovery", title: "Récupération isolée", status: "paused", project: temp, activeStepId: stage.id, selectedStepId: stage.id, steps: [stage], events: [{ id: "legacy-note", type: "note", title: "Chef", detail: marker, time, stepId: stage.id, agentId: "lead", runId: "old-run" }] });
  const state = { version: 2, projects: [], tasks: [task], selectedId: task.id, settings: { provider: "codex", reduceMotion: true, sound: false } };
  await fs.writeFile(path.join(profile, "state.json"), JSON.stringify(state));
  let app;
  try {
    app = await _electron.launch({ executablePath: process.env.DJINN_SMOKE_EXECUTABLE || require("electron"), args: process.env.DJINN_SMOKE_EXECUTABLE ? [] : [root], env: { ...process.env, DJINN_DEV_URL: "", DJINN_USER_DATA: profile } });
    const page = await app.firstWindow();
    await expect(page.getByLabel("Compte rendu de l’étape Intégrer les corrections")).toContainText("La recette date reste à vérifier.");
    await expect.poll(() => page.evaluate(async () => {
      const state = await window.djinn.loadState();
      return state.tasks[0]?.artifacts?.some(a => a.id === "legacy-report" && a.type === "document");
    })).toBe(true);
    await expect(page.getByRole("button", { name: /^Restitution\b/ })).toBeVisible();
    await page.screenshot({ path: path.join(temp, "legacy-report.png") });
    console.log(JSON.stringify({ result: "PASS", profile, screenshot: path.join(temp, "legacy-report.png"), checks: ["paused legacy stage report", "actorless native artifact recovery", "saved normalized publication"] }));
  } finally { await app?.close(); }
}
main().catch(error => { console.error(error.message); process.exitCode = 1; });
