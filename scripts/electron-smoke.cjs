"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const fsp = fs.promises;
const os = require("node:os");
const path = require("node:path");
const { _electron: electron } = require("playwright");

const projectRoot = path.resolve(__dirname, "..");
const electronExecutable = require("electron");
const fixturePath = path.join(
  projectRoot,
  "tests",
  "fixtures",
  "fake-codex.cjs",
);

async function main() {
  const tempRoot = await fsp.realpath(
    await fsp.mkdtemp(path.join(os.tmpdir(), "djinn-electron-smoke-")),
  );
  const userData = path.join(tempRoot, "user-data");
  const fakeBin = path.join(tempRoot, "bin");
  const fakeCodex = path.join(fakeBin, "codex");
  await fsp.mkdir(fakeBin, { recursive: true });
  await fsp.writeFile(
    fakeCodex,
    `#!/bin/sh\nexec ${JSON.stringify(process.execPath)} ${JSON.stringify(fixturePath)} "$@"\n`,
    { mode: 0o755 },
  );

  const env = {
    ...process.env,
    PATH: `${fakeBin}${path.delimiter}${process.env.PATH || ""}`,
    ELECTRON_ENABLE_LOGGING: "1",
    DJINN_USER_DATA: userData,
  };
  if (process.env.DJINN_SMOKE_PRODUCTION === "1") delete env.DJINN_DEV_URL;
  else env.DJINN_DEV_URL = "http://127.0.0.1:4317";
  const app = await electron.launch({
    executablePath: electronExecutable,
    args: [projectRoot, `--user-data-dir=${userData}`],
    env,
  });

  try {
    const page = await app.firstWindow();
    page.on("crash", () => console.error("[electron-smoke] Renderer crashed"));
    page.on("framenavigated", (frame) => {
      if (frame === page.mainFrame())
        console.error(`[electron-smoke] Renderer navigation: ${frame.url()}`);
    });
    await page.waitForLoadState("domcontentloaded");
    await page.waitForSelector("#root");

    const bridge = await page.evaluate(() => ({
      djinnType: typeof window.djinn,
      getEnvironmentType: typeof window.djinn?.getEnvironment,
      nodeProcessType: typeof window.process,
      requireType: typeof window.require,
    }));
    assert.equal(bridge.djinnType, "object");
    assert.equal(bridge.getEnvironmentType, "function");
    assert.equal(bridge.nodeProcessType, "undefined");
    assert.equal(bridge.requireType, "undefined");

    const environment = await page.evaluate(() =>
      window.djinn.getEnvironment(),
    );
    const codex = environment.providers.find(
      (provider) => provider.id === "codex",
    );
    assert.equal(codex?.available, true);
    assert.equal(codex?.authenticated, true);
    assert.match(codex?.version || "", /codex-fixture/);

    const state = await page.evaluate(() => window.djinn.loadState());
    // The renderer may already have saved the initial demo in this fresh profile.
    assert.ok(
      state === null ||
        (state.version === 2 && state.tasks.every((task) => task.demo)),
      "the isolated profile must contain no user missions",
    );
    const saveState = await page.evaluate(async () =>
      window.djinn.saveState({ version: 1, tasks: [] }),
    );
    assert.equal(saveState.saved, true);
    const loadedState = await page.evaluate(() => window.djinn.loadState());
    assert.equal(loadedState.version, 2);
    assert.deepEqual(loadedState.tasks, []);
    assert.deepEqual(loadedState.projects, []);

    const artifactPath = path.join(tempRoot, "smoke.txt");
    const exportPath = path.join(tempRoot, "portable-session.json");
    const importPath = path.join(tempRoot, "portable-import.json");
    await app.evaluate(
      ({ dialog }, paths) => {
        dialog.showSaveDialog = async (_window, options) => ({
          canceled: false,
          filePath:
            options?.title === "Save Djinn artifact"
              ? paths.artifactPath
              : paths.exportPath,
        });
        dialog.showOpenDialog = async () => ({
          canceled: false,
          filePaths: [paths.importPath],
        });
      },
      { artifactPath, exportPath, importPath },
    );
    const artifact = await page.evaluate(() =>
      window.djinn.saveArtifact({
        name: "smoke.txt",
        content: "Djinn smoke artifact\n",
      }),
    );
    assert.equal(artifact.name, "smoke.txt");
    assert.equal(
      fs.readFileSync(artifact.path, "utf8"),
      "Djinn smoke artifact\n",
    );

    const portableTask = {
      id: "smoke-task",
      title: "Smoke mission",
      brief: "Bridge test",
      project: projectRoot,
      provider: "codex",
      model: "",
      phase: "brief",
      status: "idle",
      createdAt: new Date().toISOString(),
      questions: [],
      agents: [],
      events: [],
      artifacts: [],
      feedback: [],
      configuration: {
        prototype: "test",
        review: "test",
        deliverables: [],
        concurrency: 1,
      },
    };
    const exported = await page.evaluate(
      (task) =>
        window.djinn.exportSession({
          format: "djinn-session",
          version: 1,
          task,
        }),
      portableTask,
    );
    assert.equal(exported.filename, "portable-session.json");
    assert.equal(
      JSON.parse(await fsp.readFile(exportPath, "utf8")).task.title,
      "Smoke mission",
    );
    await fsp.copyFile(exportPath, importPath);
    const imported = await page.evaluate(() => window.djinn.importSession());
    assert.equal(imported.task.title, "Smoke mission");

    const run = await page.evaluate(async (cwd) => {
      const received = [];
      const guidanceEvents = [];
      const unsubscribe = window.djinn.onEvent((event) => {
        received.push(event);
        if (event.type === "guidance") guidanceEvents.push(event);
      });
      const started = await window.djinn.startRun({
        taskId: "smoke-task",
        provider: "codex",
        cwd,
        prompt: "Emit fixture events.",
        mode: "plan",
      });
      const guidance = await window.djinn.steerRun({
        runId: started.runId,
        id: "smoke-guidance",
        text: "Prioritize the smallest verifiable path.",
        agentId: "lead",
      });
      await new Promise((resolve) => setTimeout(resolve, 450));
      const cancelled = await window.djinn.cancelRun(started.runId);
      await new Promise((resolve) => setTimeout(resolve, 100));
      unsubscribe();
      return {
        started,
        guidance,
        cancelled,
        types: received.map((event) => event.type),
        guidanceEvents,
      };
    }, projectRoot);
    assert.equal(typeof run.started.runId, "string");
    assert.ok(
      run.started.runId.length >= 8,
      `unexpected run id: ${run.started.runId}`,
    );
    assert.equal(run.guidance.id, "smoke-guidance");
    assert.ok(["transmitted", "consumed"].includes(run.guidance.status));
    assert.equal(run.guidance.delivery, "app_server");
    assert.equal(run.cancelled.cancelled, true);
    assert.ok(
      run.types.includes("question"),
      `expected fixture question event, got ${run.types.join(", ")}`,
    );
    assert.ok(
      run.types.includes("artifact"),
      `expected fixture artifact event, got ${run.types.join(", ")}`,
    );
    assert.ok(
      run.types.includes("tool"),
      `expected fixture tool event, got ${run.types.join(", ")}`,
    );
    assert.ok(
      run.types.includes("guidance"),
      `expected scoped guidance event, got ${run.types.join(", ")}`,
    );
    assert.ok(
      run.guidanceEvents.some((event) => event.data?.agentId === "lead"),
      "expected guidance to retain its agent scope",
    );

    // Exercise the real execute-completion hook and native server controls with
    // an isolated project, without a paid provider or the user's sessions.
    const previewProject = path.join(tempRoot, "preview-project");
    await fsp.mkdir(previewProject);
    await fsp.writeFile(
      path.join(previewProject, "package.json"),
      JSON.stringify({
        name: "smoke-preview",
        scripts: { dev: "node server.cjs" },
      }),
    );
    await fsp.writeFile(
      path.join(previewProject, "server.cjs"),
      `
      const server = require('node:http').createServer((_req, res) => res.end('djinn-preview'));
      server.listen(0, '127.0.0.1', () => console.log('http://127.0.0.1:' + server.address().port + '/'));
    `,
    );
    await app.evaluate(({ shell }) => {
      const original = shell.openExternal;
      globalThis.__djinnSmokeOpenedUrls = [];
      shell.openExternal = async (url) => {
        globalThis.__djinnSmokeOpenedUrls.push(url);
      };
      globalThis.__djinnSmokeRestoreShell = () => {
        shell.openExternal = original;
      };
    });
    const preview = await page.evaluate(async (cwd) => {
      const actions = [];
      const unsubscribe = window.djinn.onEvent((event) => {
        if (event.taskId === "preview-smoke" && event.type === "action")
          actions.push(event.data);
      });
      await window.djinn.startRun({
        taskId: "preview-smoke",
        provider: "codex",
        cwd,
        prompt: "DJINN_SMOKE_AUTOPREVIEW",
        mode: "execute",
      });
      const deadline = Date.now() + 25_000;
      while (
        Date.now() < deadline &&
        !actions.some(
          (a) => a.kind === "server" && ["ready", "error"].includes(a.status),
        )
      ) {
        await new Promise((resolve) => setTimeout(resolve, 100));
      }
      unsubscribe();
      return {
        actions,
        registered: await window.djinn.getActions("preview-smoke"),
      };
    }, previewProject);
    const readyPreview = preview.actions.find(
      (action) => action.kind === "server" && action.status === "ready",
    );
    assert.ok(
      readyPreview,
      `automatic preview failed: ${JSON.stringify(preview.actions)}`,
    );
    assert.ok(
      preview.registered.some(
        (action) =>
          action.id === "preview-check" && action.status === "pending",
      ),
    );
    assert.equal(await (await fetch(readyPreview.url)).text(), "djinn-preview");
    const openedPreview = await page.evaluate(
      ({ cwd, action }) =>
        window.djinn.performAction({
          taskId: "preview-smoke",
          cwd,
          action,
          operation: "open",
        }),
      { cwd: previewProject, action: readyPreview },
    );
    assert.equal(openedPreview.status, "ready");
    assert.deepEqual(
      await app.evaluate(() => globalThis.__djinnSmokeOpenedUrls),
      [readyPreview.url],
    );
    const stoppedPreview = await page.evaluate(
      ({ cwd, action }) =>
        window.djinn.performAction({
          taskId: "preview-smoke",
          cwd,
          action,
          operation: "stop",
        }),
      { cwd: previewProject, action: openedPreview },
    );
    assert.equal(stoppedPreview.status, "stopped");
    await assert.rejects(fetch(readyPreview.url));
    const restartedPreview = await page.evaluate(
      ({ cwd, action }) =>
        window.djinn.performAction({
          taskId: "preview-smoke",
          cwd,
          action,
          operation: "run",
        }),
      { cwd: previewProject, action: stoppedPreview },
    );
    assert.equal(restartedPreview.status, "ready");
    assert.equal(
      await (await fetch(restartedPreview.url)).text(),
      "djinn-preview",
    );
    await page.evaluate(
      ({ cwd, action }) =>
        window.djinn.performAction({
          taskId: "preview-smoke",
          cwd,
          action,
          operation: "stop",
        }),
      { cwd: previewProject, action: restartedPreview },
    );
    await app.evaluate(() => globalThis.__djinnSmokeRestoreShell());

    const notificationPatch = await app.evaluate(({ Notification }) => {
      const prototype = Notification?.prototype;
      if (!prototype || typeof prototype.show !== "function") return false;
      const originalShow = prototype.show;
      try {
        Object.defineProperty(prototype, "show", {
          configurable: true,
          writable: true,
          value() {
            if (typeof this.emit === "function") this.emit("show");
            if (typeof this.emit === "function") this.emit("click");
          },
        });
        globalThis.__djinnSmokeRestoreNotification = () => {
          Object.defineProperty(prototype, "show", {
            configurable: true,
            writable: true,
            value: originalShow,
          });
          delete globalThis.__djinnSmokeRestoreNotification;
        };
        return true;
      } catch {
        return false;
      }
    });
    assert.equal(
      notificationPatch,
      true,
      "Electron notification mock could not be installed",
    );
    const notification = await page.evaluate(async () => {
      const clicked = new Promise((resolve) => {
        const unsubscribe = window.djinn.onEvent((event) => {
          if (event.type === "notification_clicked") {
            unsubscribe();
            resolve(event);
          }
        });
        setTimeout(() => {
          unsubscribe();
          resolve(null);
        }, 1_000);
      });
      const shown = await window.djinn.notifyQuestion({
        taskId: "smoke-task",
        questionId: "Q99",
        title: "Smoke mission",
        body: "Question à traiter.",
      });
      return { shown: shown.shown, clicked: await clicked };
    });
    assert.equal(notification.shown, true);
    assert.equal(notification.clicked?.type, "notification_clicked");
    assert.equal(notification.clicked?.taskId, "smoke-task");
    assert.equal(notification.clicked?.data?.questionId, "Q99");
    await app.evaluate(() => globalThis.__djinnSmokeRestoreNotification?.());

    console.log(
      JSON.stringify({
        ok: true,
        bridge: bridge.djinnType,
        codex: codex.version,
        saveState: saveState.saved,
        export: exported.filename,
        import: imported.task.title,
        guidance: run.guidance.status,
        cancellation: run.cancelled.cancelled,
        events: [...new Set(run.types)],
        notification: notification.shown,
        autoPreview: readyPreview.status,
        previewRestart: restartedPreview.status,
        userData,
      }),
    );
  } finally {
    await app.close();
  }
}

main().catch((error) => {
  console.error(`[electron-smoke] ${error.stack || error.message || error}`);
  process.exitCode = 1;
});
