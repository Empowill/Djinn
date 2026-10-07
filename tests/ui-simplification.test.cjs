"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const Module = require("node:module");
const path = require("node:path");
const test = require("node:test");
const React = require("react");
const { renderToStaticMarkup } = require("react-dom/server");
const ts = require("typescript");

const root = path.resolve(__dirname, "../src");
const original = Module._extensions;
const iconMocks = new Proxy(
  {},
  {
    get: (_, name) => (props) => React.createElement("span", props, name),
  },
);
const motion = new Proxy(
  {},
  {
    get:
      (_, tag) =>
      ({ children, ...props }) =>
        React.createElement(tag, props, children),
  },
);
const compile = (child, filename) => {
  const originalRequire = child.require.bind(child);
  child.require = (id) => {
    if (id === "lucide-react") return iconMocks;
    if (id === "motion/react")
      return { AnimatePresence: ({ children }) => children, motion };
    if (id === "./visuals")
      return { agentColor: () => "#aaa", agentOrbState: () => "connecting" };
    return originalRequire(id);
  };
  child._compile(
    ts.transpileModule(fs.readFileSync(filename, "utf8"), {
      fileName: filename,
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2022,
        jsx: ts.JsxEmit.ReactJSX,
        esModuleInterop: true,
      },
    }).outputText,
    filename,
  );
};
Module._extensions[".tsx"] = compile;
Module._extensions[".ts"] = compile;
Module._extensions[".css"] = () => {};
test.after(() => Object.assign(Module._extensions, original));

test("provider errors expose the concrete cause and a recovery action in the action center", () => {
  const { Overview } = require(`${root}/mission-panels.tsx`);
  const { taskFixture } = require("./workflow-fixture.cjs");
  const task = taskFixture({
    status: "error",
    activeStepId: "ticket",
    steps: [{ id: "ticket", status: "error", title: "Ticket" }],
    events: [
      { type: "error", stepId: "ticket", detail: "Quota fournisseur atteint" },
    ],
  });
  const markup = renderToStaticMarkup(
    React.createElement(Overview, {
      task,
      onAnswer() {},
      onReopen() {},
      onAgent() {},
      onTab() {},
      onDemo() {},
      onResume() {},
    }),
  );
  assert.match(markup, /À toi de jouer/);
  assert.match(markup, /Quota fournisseur atteint/);
  assert.match(markup, /Votre travail est conservé/);
  assert.match(markup, /Reprendre cette étape/);
});

test("mission avatars keep order and collapse only after the third agent", () => {
  const { AgentAvatars } = require(`${root}/agent-avatars.tsx`);
  const agents = ["Chief", "Atlas", "Nova", "Iris"].map((name, index) => ({
    id: `agent-${index}`,
    name,
    role: "Worker",
    model: "test",
    status: index === 1 ? "running" : "queued",
    summary: "",
    progress: 0,
  }));
  const markup = renderToStaticMarkup(
    React.createElement(AgentAvatars, {
      agents,
      onAgent() {},
      onOverflow() {},
    }),
  );
  assert.equal((markup.match(/class="agent-avatar-button/g) || []).length, 3);
  assert.match(markup, />\+1</);
  assert.ok(markup.indexOf("Chief") < markup.indexOf("Atlas"));
  assert.match(markup, /data-tooltip="Atlas · en cours"/);
});

test("permission cards expose provider, command, question input and scoped decisions", () => {
  const { PermissionPanel } = require(`${root}/permission-panel.tsx`);
  const markup = renderToStaticMarkup(
    React.createElement(PermissionPanel, {
      requests: [
        {
          id: "permission-1",
          taskId: "task-1",
          runId: "run-1",
          agentId: "build",
          agentName: "Nova",
          provider: "codex",
          method: "workspace.write",
          title: "Modifier le serveur de test",
          reason: "Le résultat nécessite une vérification locale.",
          command: "npm run dev",
          cwd: "/tmp/project",
          paths: ["src/server.ts"],
          status: "pending",
          createdAt: "2026-10-07T10:00:00Z",
          canAcceptForSession: true,
          questions: [
            {
              id: "scope",
              question: "Quel périmètre autoriser ?",
              options: [{ label: "Le serveur", description: "Dossier local" }],
            },
          ],
        },
      ],
      onRespond: async () => true,
    }),
  );
  assert.match(markup, /Nova/);
  assert.match(markup, /Codex/);
  assert.match(markup, /npm run dev/);
  assert.match(markup, /Quel périmètre autoriser/);
  assert.match(markup, /Autoriser une fois/);
  assert.match(markup, /Autoriser pour la session/);
  assert.match(markup, /Refuser/);
});

test("server action labels make preparation, error retry and ready test states explicit", () => {
  const { ActionsPanel } = require(`${root}/actions-panel.tsx`);
  const base = {
    kind: "server",
    createdAt: "2026-10-07T10:00:00Z",
    updatedAt: "2026-10-07T10:00:00Z",
    title: "Ticket WEB-42 · Aperçu",
    directory: "/tmp/project",
    runId: "run-1",
  };
  const markup = renderToStaticMarkup(
    React.createElement(ActionsPanel, {
      actions: [
        { ...base, id: "pending", status: "pending" },
        {
          ...base,
          id: "error",
          status: "error",
          error: "Port 4317 déjà utilisé",
        },
        {
          ...base,
          id: "ready",
          status: "ready",
          url: "http://127.0.0.1:4317",
          detail: "Serveur vérifié et accessible.",
          expectedResult: "La page d’accueil s’affiche.",
        },
      ],
      onAction: async () => true,
      onRecordTestResult: async () => true,
    }),
  );
  assert.match(markup, /Préparer le test/);
  assert.match(markup, /Réessayer/);
  assert.match(markup, /Port 4317 déjà utilisé/);
  assert.match(markup, /Ouvrir le test/);
  assert.match(markup, /Ticket \/ titre/);
  assert.match(markup, /La page d’accueil s’affiche/);
  assert.match(markup, /Ça fonctionne/);
  assert.match(markup, /Signaler un problème/);
  assert.match(markup, /Tester plus tard/);
});
