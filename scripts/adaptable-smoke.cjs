"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const os = require("node:os");
const { _electron: electron } = require("playwright");
const { expect } = require("@playwright/test");
const waitNative = (page, predicate, argument) => expect.poll(() => page.evaluate(predicate, argument), { timeout: 30000 }).toBe(true);

async function main() {
  const root = path.resolve(__dirname, "..");
  const temp = await fs.realpath(
    await fs.mkdtemp(path.join(os.tmpdir(), "djinn-adaptable-smoke-")),
  );
  const profile = path.join(temp, "profile"),
    bin = path.join(temp, "bin"),
    project = path.join(temp, "project");
  await Promise.all([fs.mkdir(profile), fs.mkdir(bin), fs.mkdir(project)]);
  const hiddenDirectory = ".worktrees/ticket/app/apps/console";
  const hiddenPackage = path.join(project, hiddenDirectory);
  await fs.mkdir(hiddenPackage, { recursive: true });
  await fs.writeFile(
    path.join(hiddenPackage, "package.json"),
    JSON.stringify({
      name: "isolated-preview",
      scripts: { dev: "node server.cjs" },
    }),
  );
  await fs.writeFile(
    path.join(hiddenPackage, "server.cjs"),
    "const s=require('node:http').createServer((_,r)=>r.end('isolated-preview'));s.listen(0,'127.0.0.1',()=>console.log('http://127.0.0.1:'+s.address().port+'/'));\n",
  );
  const fixture = path.join(root, "tests/fixtures/permission-codex.cjs");
  await fs.writeFile(
    path.join(bin, "codex"),
    `#!/bin/sh\nexec '${process.execPath.replaceAll("'", "'\\''")}' '${fixture.replaceAll("'", "'\\''")}' "$@"\n`,
    { mode: 0o755 },
  );
  const claudeFixture = path.join(root, "tests/fixtures/permission-claude.cjs");
  await fs.writeFile(
    path.join(bin, "claude"),
    `#!/bin/sh\nexec '${process.execPath.replaceAll("'", "'\\''")}' '${claudeFixture.replaceAll("'", "'\\''")}' "$@"\n`,
    { mode: 0o755 },
  );
  const date = new Date().toISOString();
  const task = {
    id: "adaptable-smoke",
    title: "Mission isolée",
    brief: "Vérifier le parcours d’autorisation.",
    project,
    provider: "codex",
    model: "fixture-model",
    phase: "brief",
    status: "idle",
    createdAt: date,
    workflowMode: "flexible",
    activeStepId: "stage",
    selectedStepId: "stage",
    steps: [
      {
        id: "stage",
        type: "reflection",
        title: "Préparation",
        objective: "Vérifier le parcours",
        status: "pending",
        exitCriteria: [],
        expectedArtifacts: [],
        skills: [],
        validation: "human",
      },
    ],
    questions: [],
    events: [],
    artifacts: [
      {
        id: "full-contract",
        title: "Contrat complet",
        type: "document",
        sourceOfTruth: true,
        content:
          "Contexte détaillé\n".repeat(3000) + "Critère final à conserver",
        createdAt: date,
        updatedAt: date,
      },
    ],
    feedback: [],
    agents: ["Chef", "Interface", "Runtime", "Vérification"].map((name, i) => ({
      id: i ? "agent-" + i : "lead",
      name,
      role: "Test isolé",
      model: "fixture-model",
      status: "done",
      summary: "Résultat conservé",
      progress: 100,
    })),
    configuration: {
      prototype: "test",
      review: "test",
      deliverables: [],
      concurrency: 2,
    },
    actions: [
      {
        id: "manual-test",
        kind: "manual",
        title: "Contrôler le résultat",
        status: "pending",
        stepId: "stage",
        testInstructions: ["Inspecter le résultat du test isolé"],
        expectedResult: "La carte affiche un retour clair",
        createdAt: date,
        updatedAt: date,
      },
    ],
    workItems: [
      { id: "ticket-a", title: "Ticket A · Correction", status: "running", agentId: "agent-1", updatedAt: date },
      { id: "ticket-b", title: "Ticket B · Recette indépendante", status: "ready", agentId: "agent-2", updatedAt: date },
    ],
  };
  task.actions.push({ id: "feedback-a", title: "Recette A", kind: "manual", status: "pending", workItemId: "ticket-a", createdAt: date, updatedAt: date,
    testResult: { status: "problem", detail: "Le premier ticket nécessite une correction.", recordedAt: new Date(Date.now() - 60000).toISOString() } });
  task.actions.push({
    id: "hidden-test",
    kind: "server",
    title: "Ticket isolé · Aperçu",
    status: "pending",
    directory: hiddenDirectory,
    script: "dev",
    workItemId: "ticket-b",
    stepId: "stage",
    testInstructions: ["Vérifier que le serveur répond"],
    expectedResult: "Le résultat isolé est accessible",
    createdAt: date,
    updatedAt: date,
  });
  await fs.writeFile(
    path.join(profile, "state.json"),
    JSON.stringify({
      version: 2,
      selectedId: task.id,
      tasks: [task],
      projects: [],
    }),
  );
  const trace = path.join(temp, "native.jsonl"),
    claudeTrace = path.join(temp, "claude.jsonl");
  const app = await electron.launch({
    executablePath: process.env.DJINN_SMOKE_EXECUTABLE || require("electron"),
    args: process.env.DJINN_SMOKE_EXECUTABLE ? [] : [root],
    env: {
      ...process.env,
      PATH: `${bin}${path.delimiter}${process.env.PATH || ""}`,
      DJINN_DEV_URL: "",
      DJINN_USER_DATA: profile,
      DJINN_PERMISSION_TRACE: trace,
      DJINN_CLAUDE_TRACE: claudeTrace,
      DJINN_SMOKE_STRUCTURED: "1",
    },
  });
  let page;
  try {
    page = await app.firstWindow();
    await app.evaluate(({ Notification }) => {
      Notification.prototype.show = function () {
        globalThis.__isolatedNotification = this;
        this.emit("show");
      };
    });
    await page.getByText("Mission isolée", { exact: true }).first().waitFor();
    await page.waitForSelector(".agent-avatars");
    assert.equal(
      await page.locator(".agent-avatar-list .agent-avatar-button").count(),
      3,
    );
    const overflow = page.getByRole("button", {
      name: "Afficher les 1 autres agents",
      exact: true,
    });
    await overflow.click();
    await page.getByRole("dialog", { name: "Choisir un agent" }).waitFor();
    assert.equal(await page.locator(".agent-picker-item").count(), 4);
    await page.keyboard.press("Escape");
    await page
      .getByRole("dialog", { name: "Choisir un agent" })
      .waitFor({ state: "hidden" });
    const started = await page.evaluate(
      (cwd) =>
        window.djinn.startRun({
          taskId: "adaptable-smoke",
          provider: "codex",
          model: "fixture-model",
          cwd,
          prompt: "Permission smoke",
          mode: "plan",
          stepId: "stage",
          step: {
            id: "stage",
            type: "reflection",
            title: "Préparation",
            objective: "Permission",
            status: "running",
            exitCriteria: [],
            expectedArtifacts: [],
            skills: [],
          },
        }),
      project,
    );
    await page.locator(".permission-card.is-pending").waitFor();
    const pending = await page.evaluate(() =>
      window.djinn.getPendingPermissions(),
    );
    assert.equal(pending.length, 1);
    assert.equal(pending[0].taskId, "adaptable-smoke");
    assert.ok(pending[0].runId);
    assert.ok(started.runId);
    await page
      .getByRole("button", { name: "Autoriser une fois", exact: true })
      .click();
    await page.locator(".permission-card.is-pending").waitFor({ state: "hidden" });
    await page.locator(".permission-panel").waitFor({ state: "hidden" });
    assert.deepEqual(
      await page.evaluate(() => window.djinn.getPendingPermissions()),
      [],
    );
    const duplicate = await page.evaluate(
      (requestId) =>
        window.djinn.respondPermission({
          taskId: "adaptable-smoke",
          requestId,
          decision: "accept",
        }),
      pending[0].id,
    );
    assert.equal(duplicate.resolved, false);
    const messages = (await fs.readFile(trace, "utf8"))
      .trim()
      .split("\n")
      .map(JSON.parse);
    const response = messages.find(
      (message) => message.id === 9001 && !message.method,
    );
    assert.equal(response?.result?.decision, "accept");
    assert.equal(
      messages.find((message) => message.method === "thread/start")?.params
        ?.approvalPolicy,
      "on-request",
    );
    assert.equal(
      messages.filter((message) => message.method === "turn/start").length,
      1,
    );
    const contextRoot = path.join(profile, "mission-supports");
    const contextDirectory = (await fs.readdir(contextRoot))[0];
    const contextIndex = path.join(contextRoot, contextDirectory, "index.json");
    const context = JSON.parse(await fs.readFile(contextIndex, "utf8"));
    assert.equal(context.taskId, "adaptable-smoke");
    assert.equal(context.supports.length, 1);
    assert.ok(
      (await fs.readFile(context.supports[0].path, "utf8")).endsWith(
        "Critère final à conserver",
      ),
    );
    assert.ok(
      JSON.stringify(
        messages.find((message) => message.method === "turn/start"),
      ).includes(contextIndex),
    );
    await waitNative(page,
      async (runId) =>
        !(await window.djinn.getRuntimeSnapshot()).runs.some(
          (run) => run.runId === runId,
        ),
      started.runId,
    );
    const structuredResponses = (await fs.readFile(trace, "utf8")).trim().split("\n").map(JSON.parse);
    for (const id of [12000, 12001, 12002]) assert.equal(structuredResponses.find((message) => message.id === id && !message.method)?.result?.success, true);
    await page.locator("#question-structured-question").waitFor();
    await page.locator("#question-structured-question .question-heading").click();
    await page.getByText("La date devient définitive après validation.", { exact: false }).waitFor();
    await page.locator("#question-structured-question textarea").fill("RH et évaluateur principal avant validation.");
    await page.locator("#question-structured-question").getByRole("button", { name: "Valider la réponse", exact: true }).click();
    await page.locator(".decisions-section #question-structured-question").waitFor({ state: "hidden" });
    const interactions = await page.evaluate(() => window.djinn.getMissionInteractions("adaptable-smoke"));
    assert.ok(interactions.events.some((event) => event.type === "artifact" && event.data.content.endsWith("Fin du rapport complet")));
    await page.getByText("Correction en cours", { exact: true }).first().waitFor();
    await waitNative(page, async () => !(await window.djinn.getRuntimeSnapshot()).runs.some((run) => run.taskId === "adaptable-smoke"));
    await page.locator(".mission-test-row").filter({ hasText: "Ticket B · Recette indépendante" }).getByRole("button", { name: "Préparer le test", exact: true }).click();
    await page
      .locator("#action-hidden-test")
      .getByRole("button", { name: "Préparer le test", exact: true })
      .click();
    try {
      await page
        .locator("#action-hidden-test")
        .getByRole("button", { name: "Ouvrir le test", exact: true })
        .waitFor();
    } catch (error) {
      console.error(
        "Isolated action failure:",
        (await page.locator("#action-hidden-test").innerText()).slice(0, 2500),
      );
      await page.screenshot({ path: path.join(temp, "failure.png") });
      throw error;
    }
    const ready = await page.evaluate(async () =>
      (await window.djinn.getActions("adaptable-smoke")).find(
        (action) => action.id === "hidden-test",
      ),
    );
    assert.equal(ready.status, "ready");
    assert.equal(await (await fetch(ready.url)).text(), "isolated-preview");
    await page.getByText("Prête à tester", { exact: true }).first().waitFor();
    await page.locator(".mission-test-row").filter({ hasText: "Ticket B · Recette indépendante" }).getByRole("button", { name: "Tester", exact: true }).click();
    await page.getByText("En cours de test", { exact: true }).first().waitFor();
    await page.locator(".mission-test-board").screenshot({ path: path.join(temp, "parallel-tests.png") });
    await page
      .locator("#action-hidden-test")
      .getByRole("button", { name: "Ça fonctionne", exact: true })
      .click();
    await waitNative(page,
      async () =>
        (await window.djinn.loadState()).tasks
          .find((t) => t.id === "adaptable-smoke")
          ?.actions?.find((a) => a.id === "hidden-test")?.testResult?.status ===
        "passed",
    );
    const saved = await page.evaluate(() => window.djinn.loadState());
    assert.equal(
      saved.tasks.find((t) => t.id === "adaptable-smoke").steps[0].approvedAt,
      undefined,
    );
    await page.evaluate(
      ({ cwd, action }) =>
        window.djinn.performAction({
          taskId: "adaptable-smoke",
          cwd,
          action,
          operation: "stop",
        }),
      { cwd: project, action: ready },
    );
    await page.evaluate(async () => {
      const state = await window.djinn.loadState();
      const original = state.tasks.find(
        (task) => task.id === "adaptable-smoke",
      );
      state.tasks.push({
        ...original,
        id: "claude-smoke",
        title: "Mission isolée Claude",
        provider: "claude",
        permissions: [],
        actions: [],
        events: [],
        stepResult: undefined,
        runtimeEventIds: [],
        agents: [
          { ...original.agents[0], name: "Chef Claude", status: "done" },
        ],
      });
      state.selectedId = "claude-smoke";
      await window.djinn.saveState(state);
    });
    await page.reload();
    await page
      .getByText("Mission isolée Claude", { exact: true })
      .first()
      .waitFor();
    const claudeRun = await page.evaluate(
      (cwd) =>
        window.djinn.startRun({
          taskId: "claude-smoke",
          provider: "claude",
          model: "fixture-model",
          cwd,
          prompt: "Claude permission smoke",
          mode: "plan",
        }),
      project,
    );
    await page.locator(".permission-card.is-pending").waitFor();
    await page
      .getByRole("button", { name: "Autoriser une fois", exact: true })
      .click();
    await waitNative(page,
      async (runId) =>
        !(await window.djinn.getRuntimeSnapshot()).runs.some(
          (run) => run.runId === runId,
        ),
      claudeRun.runId,
    );
    const claudeMessages = (await fs.readFile(claudeTrace, "utf8"))
      .trim()
      .split("\n")
      .map(JSON.parse);
    const claudeResponse = claudeMessages.find(
      (message) => message.type === "control_response",
    );
    assert.equal(claudeResponse?.response?.request_id, "claude-native-request");
    assert.equal(claudeResponse?.response?.response?.behavior, "allow");
    const cancelledRun = await page.evaluate(
      (cwd) =>
        window.djinn.startRun({
          taskId: "adaptable-smoke",
          provider: "codex",
          model: "fixture-model",
          cwd,
          prompt: "Cancellation smoke",
          mode: "plan",
          stepId: "stage",
          step: {
            id: "stage",
            type: "reflection",
            title: "Préparation",
            objective: "Permission",
            status: "running",
            exitCriteria: [],
            expectedArtifacts: [],
            skills: [],
          },
        }),
      project,
    );
    await waitNative(page,
      async () => (await window.djinn.getPendingPermissions()).length === 1,
    );
    const cancelledRequest = (
      await page.evaluate(() => window.djinn.getPendingPermissions())
    )[0];
    await waitNative(page, async (id) => (await window.djinn.loadState()).tasks.some((task) => task.permissions?.some((permission) => permission.id === id && permission.status === "pending")), cancelledRequest.id);
    assert.equal(await app.evaluate(() => globalThis.__isolatedNotification?.title), "Autorisation attendue");
    await app.evaluate(() => globalThis.__isolatedNotification?.emit("click"));
    await page
      .locator(`#permission-${cancelledRequest.id}.is-pending`)
      .waitFor();
    assert.equal(
      (await page.getByText("Mission isolée", { exact: true }).count()) > 0,
      true,
    );
    await page.evaluate(
      (runId) => window.djinn.cancelRun(runId),
      cancelledRun.runId,
    );
    await waitNative(page,
      async () => (await window.djinn.getPendingPermissions()).length === 0,
    );
    const stale = await page.evaluate(
      (requestId) =>
        window.djinn.respondPermission({
          taskId: "adaptable-smoke",
          requestId,
          decision: "accept",
        }),
      cancelledRequest.id,
    );
    assert.equal(stale.resolved, false);
    await page.locator(".agent-avatars").scrollIntoViewIfNeeded();
    await page.screenshot({
      path: path.join(temp, "permission-accepted.png"),
      fullPage: true,
    });
    console.log(
      JSON.stringify({
        ok: true,
        avatars: "3 +1",
        nativeApproval: "Codex and Claude same request",
        duplicate: "rejected",
        cancelled: "rejected",
        notificationClick: "correct mission and card",
        hiddenWorktree: "ready",
        testFeedback: "persisted without approving stage",
        structuredTools: "complete question, full durable report, work item",
        independentTickets: "A correction alongside B testing",
        resolvedSections: "permissions and questions disappear",
        userData: profile,
        screenshot: path.join(temp, "permission-accepted.png"),
      }),
    );
  } catch (error) {
    if (page) {
      await page.screenshot({ path: path.join(temp, "failure.png"), fullPage: true });
      const runtime = await page.evaluate(() => window.djinn.getRuntimeSnapshot());
      console.error(JSON.stringify({ temp, runs: runtime.runs.map(({ runId, taskId, status }) => ({ runId, taskId, status })), permissions: runtime.permissions?.map(({ id, taskId, status }) => ({ id, taskId, status })), body: (await page.locator("body").innerText()).slice(-5000) }));
    }
    throw error;
  } finally {
    await app.close();
  }
}
main().catch((error) => {
  console.error(error.stack || error);
  process.exitCode = 1;
});
