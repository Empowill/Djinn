import fs from "node:fs";
import path from "node:path";
import { test, expect, type Page } from "@playwright/test";
const time = "2026-10-06T08:00:00.000Z";
const step = (id: string, type = "reflection", status = "pending") => ({
  id,
  type,
  title:
    type === "reflection"
      ? "Réflexion"
      : type === "implementation"
        ? "Implémentation"
        : type === "review"
          ? "Review"
          : "Livraison",
  objective: "Objectif " + id,
  status,
  exitCriteria: [],
  expectedArtifacts: [],
  skills: [],
  ...(status === "completed"
    ? {
        approvedBy: "human",
        approvedAt: time,
        completedAt: time,
        startedAt: time,
      }
    : {}),
});
const base = () => ({
  version: 2,
  projects: [
    {
      id: "p",
      name: "Djinn",
      directory: "/tmp/djinn-fixture",
      conventions: "# Conventions\nPrototypes dans app/prototypes.",
      locations: { prototype: "app/prototypes" },
      workflows: [],
      updatedAt: time,
    },
  ],
  selectedId: "task",
  tasks: [
    {
      id: "task",
      title: "Nouvelle mission",
      titleSource: "placeholder",
      brief: "Améliorer le suivi.",
      project: "/tmp/djinn-fixture",
      projectId: "p",
      provider: "codex",
      model: "configured",
      phase: "brief",
      status: "idle",
      createdAt: time,
      steps: [
        step("s1"),
        step("s2", "implementation"),
        step("s3", "review"),
        step("s4", "delivery"),
      ],
      activeStepId: "s1",
      selectedStepId: "s1",
      progressionPolicy: "manual",
      questions: [],
      agents: [],
      events: [],
      artifacts: [],
      feedback: [],
      configuration: {
        prototype: "Interface locale",
        review: "Review",
        deliverables: [],
        concurrency: 8,
      },
    },
  ],
  settings: {
    provider: "codex",
    model: "configured",
    reduceMotion: true,
    sound: false,
  },
});
async function setup(page: Page, state = base()) {
  await page.addInitScript((fixture) => {
    const w = window as any,
      listeners = new Set<(e: any) => void>();
    w.__starts = [];
    w.__steers = [];
    w.__saved = fixture;
    w.__emit = (e: any) => {
      for (const f of listeners)
        f({ timestamp: new Date().toISOString(), ...e });
    };
    w.djinn = {
      loadState: async () => fixture,
      saveState: async (s: any) => {
        w.__saved = JSON.parse(JSON.stringify(s));
        return { saved: true };
      },
      getEnvironment: async () => ({
        platform: "fixture",
        appVersion: "0.2.0",
        providers: [
          { id: "codex", name: "Codex", available: true, authenticated: true },
          {
            id: "claude",
            name: "Claude Code",
            available: true,
            authenticated: true,
          },
        ],
      }),
      getProviderModels: async (provider: string) => ({
        provider,
        source: "cli",
        models: w.__modelError
          ? []
          : [{ id: "fixture-model", name: "Fixture Model", isDefault: true }],
        ...(w.__modelError
          ? { error: "Catalogue indisponible pour le test" }
          : {}),
      }),
      getActions: async () => [],
      notifyQuestion: async () => ({ shown: false }),
      selectDirectory: async () => "/tmp/djinn-fixture",
      validateProject: async (p: any) => p,
      startRun: async (input: any) => {
        w.__starts.push(input);
        const runId = "run-" + w.__starts.length;
        w.__emit({
          taskId: input.taskId,
          runId,
          stepId: input.stepId,
          type: "status",
          data: {
            status: "running",
            activeAgents: [],
            waitingForAgents: false,
          },
        });
        return { runId };
      },
      steerRun: async (input: any) => {
        w.__steers.push(input);
        return { id: input.id, status: "transmitted", delivery: "app_server" };
      },
      cancelRun: async () => ({ cancelled: true }),
      onEvent: (callback: any) => {
        listeners.add(callback);
        return () => listeners.delete(callback);
      },
      openExternal: async () => {},
      exportSession: async () => {},
      saveArtifact: async () => {},
      performAction: async () => {},
    };
  }, state);
  if (process.env.DJINN_OFFLINE_E2E)
    await page.route("http://127.0.0.1:4317/**", (route) => {
      const pathname = new URL(route.request().url()).pathname;
      const file = path.resolve(
        "dist",
        "." + (pathname === "/" ? "/index.html" : pathname),
      );
      return route.fulfill({ path: file });
    });
  await page.goto("/");
  await expect(
    page.getByRole("navigation", { name: "Étapes de la mission" }),
  ).toBeVisible();
}
async function send(page: Page, text: string) {
  await page
    .getByRole("button", { name: "Donner une indication", exact: false })
    .first()
    .click();
  await page
    .getByRole("textbox", { name: "Indications pour la mission" })
    .fill(text);
  await page.getByRole("button", { name: "Envoyer l’indication" }).click();
}
async function emit(
  page: Page,
  type: string,
  data: any,
  stepId = "s1",
  runId = "run-1",
) {
  await page.evaluate(
    ({ type, data, stepId, runId }) =>
      (window as any).__emit({ type, data, stepId, runId, taskId: "task" }),
    { type, data, stepId, runId },
  );
}

test("message launches current stage, title arrives from harness, completion awaits human validation", async ({
  page,
}) => {
  await setup(page);
  await expect(
    page
      .getByRole("navigation", { name: "Étapes de la mission" })
      .getByRole("button", { name: /Implémentation/ }),
  ).toBeDisabled();
  await send(page, "Commençons la réflexion.");
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts.length))
    .toBe(1);
  await emit(page, "mission_metadata", { title: "Suivi des missions" });
  await expect(page.locator(".hero h1")).toContainText("Suivi des missions");
  await emit(page, "artifact", {
    id: "plan",
    title: "Résultat",
    type: "document",
    content: "# Plan\n\n| Choix | État |\n| --- | --- |\n| Timeline | Prêt |",
  });
  await emit(page, "status", { status: "completed" });
  await expect(
    page.getByRole("button", { name: "Valider ce résultat" }),
  ).toBeVisible();
  await expect(
    page
      .getByRole("navigation", { name: "Étapes de la mission" })
      .getByRole("button", { name: /Implémentation/ }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "Supports", exact: false })
    .first()
    .click();
  await expect(page.locator(".art-text-preview table")).toBeVisible();
  await page.getByRole("button", { name: "Mission", exact: true }).click();
  await page.getByRole("button", { name: "Valider ce résultat" }).click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts.length))
    .toBe(1);
  await page.getByRole("button", { name: "Lancer Implémentation" }).click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts.length))
    .toBe(2);
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts[1].stepId))
    .toBe("s2");
});

test("all blocking answers resume same stage automatically, without approving it", async ({
  page,
}) => {
  await setup(page);
  await send(page, "Lancer la réflexion.");
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts.length))
    .toBe(1);
  for (const id of ["q1", "q2"])
    await emit(page, "question", {
      id,
      title: "Décision " + id,
      context: "Contexte",
      recommendation: "Oui",
      options: [{ id: "a", label: "Oui", description: "Avancer" }],
      blocking: true,
      unlocks: "Reprise",
      agentId: "lead",
    });
  await emit(page, "status", { status: "completed" });
  await page
    .locator("#question-q1")
    .getByRole("button", { name: "Valider ce choix" })
    .click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts.length))
    .toBe(1);
  await page
    .locator("#question-q2")
    .getByRole("button", { name: "Valider ce choix" })
    .click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts.length))
    .toBe(2);
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts[1].stepId))
    .toBe("s1");
  await expect(
    page
      .getByRole("navigation", { name: "Étapes de la mission" })
      .getByRole("button", { name: /Implémentation/ }),
  ).toBeDisabled();
});

test("guidance during workers reaches chief immediately without another start; stderr warning stays a note", async ({
  page,
}) => {
  await setup(page);
  await send(page, "Commencer.");
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts.length))
    .toBe(1);
  await emit(page, "agent", {
    id: "worker",
    name: "Socle",
    role: "Runtime",
    status: "running",
    runId: "child-1",
    lifecycle: "agent_started",
  });
  await emit(page, "status", {
    status: "running",
    waitingForAgents: true,
    activeAgents: [{ id: "worker", task: "Runtime", runId: "child-1" }],
  });
  await emit(
    page,
    "note",
    {
      title: "Diagnostic Codex",
      detail: "WARN codex_skills missing icon",
      severity: "warning",
      agentId: "worker",
      scope: "agent",
      parentRunId: "run-1",
    },
    "s1",
    "child-1",
  );
  await expect(page.locator(".team-section")).toContainText("Socle");
  await expect(page.locator(".execution-status")).toHaveCount(0);
  await send(page, "Où en est le travail ?");
  await expect
    .poll(() => page.evaluate(() => (window as any).__steers.length))
    .toBe(1);
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts.length))
    .toBe(1);
  const id = await page.evaluate(() => (window as any).__steers[0].id);
  await emit(page, "guidance", {
    id,
    status: "consumed",
    text: "Où en est le travail ?",
  });
  await emit(
    page,
    "note",
    {
      title: "Réponse du chef",
      detail: "Socle travaille sur le runtime.",
      agentId: "lead",
      scope: "agent",
      parentRunId: "run-1",
    },
    "s1",
    "chief-1",
  );
  await page.getByRole("button", { name: "Parler au chef" }).click();
  await expect(page.locator(".agent-chat")).toContainText(
    "Socle travaille sur le runtime.",
  );
  await expect(page.locator(".team-section")).toContainText("Runtime");
  await expect
    .poll(() => page.evaluate(() => (window as any).__saved.tasks[0].status))
    .not.toBe("error");
});

test("past step selection changes underlying context and never launches a process", async ({
  page,
}) => {
  const state = base(),
    t = state.tasks[0];
  t.steps = [
    step("s1", "reflection", "completed"),
    step("s2", "reflection", "paused"),
    step("s3", "review"),
  ];
  t.activeStepId = "s2";
  t.selectedStepId = "s2";
  (t as any).agentHistory = {
    s1: [
      {
        id: "lead",
        name: "Ancien chef",
        role: "Réflexion passée",
        model: "configured",
        status: "done",
        summary: "Historique",
        progress: 100,
        stepId: "s1",
      },
    ],
  };
  (t as any).artifacts = [
    {
      id: "old",
      title: "Support passé",
      type: "document",
      content: "# Ancien",
      updatedAt: time,
      stepId: "s1",
    },
    {
      id: "current",
      title: "Support actuel",
      type: "document",
      content: "# Actuel",
      updatedAt: time,
      stepId: "s2",
    },
  ];
  await setup(page, state);
  await page
    .getByRole("navigation", { name: "Étapes de la mission" })
    .getByRole("button", { name: /Réflexion.*Validée/ })
    .click();
  await expect(page.locator(".team-section")).toContainText("Ancien chef");
  await page
    .getByRole("button", { name: "Supports", exact: false })
    .first()
    .click();
  await expect(page.locator(".art-artifact-card")).toContainText(
    "Support passé",
  );
  await expect(page.locator(".art-artifact-card")).not.toContainText(
    "Support actuel",
  );
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts.length))
    .toBe(0);
  await expect(
    page
      .getByRole("navigation", { name: "Étapes de la mission" })
      .getByRole("button", { name: /Review/ }),
  ).toBeDisabled();
});

test("new missions use the fullscreen intent and let the agent define a timeline", async ({
  page,
}) => {
  const state = base();
  (state.projects[0] as any).preferences = { concurrency: 8 };
  state.projects[0].workflows = [
    {
      id: "w",
      title: "Produit",
      steps: [step("a"), step("b"), step("c", "review")],
    },
  ] as any;
  await setup(page, state);
  await page
    .getByRole("button", { name: /^Nouvelle mission/ })
    .first()
    .click();
  await expect(
    page.getByRole("heading", { name: "Qu’allez-vous créer ?" }),
  ).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(
    page.getByRole("combobox", { name: "Type de discussion" }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("spinbutton", { name: "Nombre maximal de sous-agents" }),
  ).toHaveCount(0);
  await page
    .getByRole("textbox", { name: "Votre intention" })
    .fill("Préparer une timeline adaptée à ce projet.");
  await expect(
    page.getByText("Djinn définit le workflow avant de commencer."),
  ).toBeVisible();
  await page.getByRole("button", { name: "Préparer la mission" }).click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts.length))
    .toBe(1);
  const initial = await page.evaluate(() => ({
    task: (window as any).__saved.tasks.at(-1),
    run: (window as any).__starts[0],
  }));
  expect(initial.task.projectId).toBe("p");
  expect(initial.task.steps[0].type).toBe("discussion");
  expect(initial.task.workflowMode).toBe("flexible");
  expect(initial.task.workflowOrigin).toBe("agent");
  expect(initial.task.configuration.concurrency).toBe(8);
  expect(initial.task.projectSnapshot.workflows).toEqual([]);
  expect(initial.run.mode).toBe("plan");

  await page.evaluate(({ taskId, stepId }) => {
    const w = window as any;
    w.__emit({
      taskId,
      stepId,
      runId: "run-1",
      type: "workflow_defined",
      data: {
        steps: [
          {
            type: "reflection",
            title: "Clarifier le besoin",
            objective: "Poser les décisions nécessaires avant de produire.",
          },
          {
            type: "implementation",
            title: "Construire le résultat",
            objective: "Réaliser le résultat validé par la mission.",
          },
        ],
        reason: "La demande nécessite une clarification puis une réalisation.",
      },
    });
    w.__emit({
      taskId,
      stepId,
      runId: "run-1",
      type: "status",
      data: { status: "completed" },
    });
  }, { taskId: initial.task.id, stepId: initial.task.steps[0].id });
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts.length))
    .toBe(2);
  const result = await page.evaluate((taskId) => {
    const w = window as any;
    return {
      task: w.__saved.tasks.find((candidate: any) => candidate.id === taskId),
      project: w.__saved.projects.find((candidate: any) => candidate.id === "p"),
      run: w.__starts[1],
    };
  }, initial.task.id);
  expect(result.task.steps.map((s: any) => s.type)).toEqual([
    "reflection",
    "implementation",
  ]);
  expect(result.task.workflowOrigin).toBe("agent");
  expect(result.run.step.type).toBe("reflection");
  expect(result.project.workflows).toHaveLength(1);
});
for (const provider of ["codex", "claude"] as const) {
  for (const type of ["specification", "implementation"] as const) {
    test(`qualification defines a ${type} timeline with a separate run (${provider})`, async ({
      page,
    }) => {
      const state = base();
      Object.assign(state.projects[0], {
        preferences: { provider, model: "project-model" },
        sourcesOfTruth: [
          {
            id: "product",
            title: "Décisions",
            path: "docs/produit.md",
            description: "Approuvé",
          },
        ],
      });
      await setup(page, state);
      await page
        .getByRole("button", { name: /^Nouvelle mission/ })
        .first()
        .click();
      await page
        .getByRole("textbox", { name: "Votre intention" })
        .fill(
          type === "specification"
            ? "Une spec sans code."
            : "Implémenter ce besoin.",
        );
      await page
        .getByRole("textbox", { name: /Indications complémentaires/ })
        .fill("Conserver les décisions approuvées.");
      await page.getByRole("button", { name: "Préparer la mission" }).click();
      await expect
        .poll(() => page.evaluate(() => (window as any).__starts.length))
        .toBe(1);
      const initial = await page.evaluate(() => ({
        task: (window as any).__saved.tasks.at(-1),
        run: (window as any).__starts[0],
      }));
      expect(initial.task.steps[0].type).toBe("discussion");
      expect(initial.run.mode).toBe("plan");
      expect(initial.run.provider).toBe(provider);
      expect(initial.run.model).toBe("project-model");
      expect(initial.run.agents).toHaveLength(0);
      expect(initial.run.guidance[0].text).toContain("Conserver");
      await page.evaluate(
        ({ taskId, stepId, type }) => {
          const w = window as any;
          w.__emit({
            taskId,
            stepId,
            runId: "run-1",
            type: "workflow_defined",
            data: {
              steps: [
                {
                  type,
                  title: "Étape qualifiée",
                  objective: "Résultat attendu",
                },
                {
                  type: "review",
                  title: "Vérifier le résultat",
                  objective: "Confirmer le résultat avec la personne.",
                },
              ],
              reason: "La timeline découle de l’intention de la mission.",
            },
          });
          w.__emit({
            taskId,
            stepId,
            runId: "run-1",
            type: "status",
            data: { status: "completed" },
          });
        },
        { taskId: initial.task.id, stepId: initial.run.stepId, type },
      );
      await expect
        .poll(() => page.evaluate(() => (window as any).__starts.length))
        .toBe(2);
      const next = await page.evaluate(() => ({
        task: (window as any).__saved.tasks.at(-1),
        run: (window as any).__starts[1],
      }));
      expect(next.task.steps).toHaveLength(2);
      expect(next.task.steps[0].id).toBe(initial.task.steps[0].id);
      expect(next.task.steps.map((s: any) => s.type)).toEqual([type, "review"]);
      expect(next.run.mode).toBe(
        type === "implementation" ? "execute" : "plan",
      );
      expect(next.run.step.type).toBe(type);
      expect(next.run.providerSessions).toBeUndefined();
      expect(next.task.projectSnapshot.sourcesOfTruth[0].path).toBe(
        "docs/produit.md",
      );
      await page.evaluate(
        ({ taskId, stepId }) =>
          (window as any).__emit({
            taskId,
            stepId,
            runId: "run-1",
            type: "status",
            data: { status: "completed" },
          }),
        { taskId: next.task.id, stepId: next.run.stepId },
      );
      await page.evaluate(
        ({ taskId, stepId }) =>
          (window as any).__emit({
            taskId,
            stepId,
            runId: "lead-pass-1",
            type: "error",
            data: {
              scope: "agent",
              agentId: "lead",
              parentRunId: "run-1",
              message: "Ancien passage",
            },
          }),
        { taskId: next.task.id, stepId: next.run.stepId },
      );
      expect(await page.evaluate(() => (window as any).__starts.length)).toBe(
        2,
      );
      expect(
        await page.evaluate(() =>
          (window as any).__saved.tasks
            .at(-1)
            .events.some((e: any) => e.detail === "Ancien passage"),
        ),
      ).toBe(false);
    });
  }
}

test("chief continuation is a proposal; human approval and addition never launch it automatically", async ({
  page,
}) => {
  const state = base();
  const task = state.tasks[0] as any;
  task.workflowMode = "flexible";
  task.steps = [step("s1")];
  await setup(page, state);
  await send(page, "Clarifier le besoin avant de spécifier.");
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts.length))
    .toBe(1);
  await emit(
    page,
    "next_step",
    {
      type: "implementation",
      title: "Code interdit",
      objective: "",
      reason: "Worker",
      agentId: "worker",
      scope: "agent",
      parentRunId: "run-1",
    },
    "s1",
    "child-1",
  );
  expect(
    await page.evaluate(
      () => (window as any).__saved.tasks[0].nextStepProposal,
    ),
  ).toBeUndefined();
  await emit(page, "next_step", {
    type: "specification",
    title: "Formaliser le besoin",
    objective: "Décrire les critères d’acceptation.",
    reason:
      "Les décisions produit sont suffisantes pour une spec, sans prototype.",
  });
  await emit(page, "status", { status: "completed" });
  await expect(
    page.getByRole("button", { name: "Choisir la suite" }),
  ).toBeDisabled();
  expect(
    await page.evaluate(() => (window as any).__saved.tasks[0].steps.length),
  ).toBe(1);
  await page.getByRole("button", { name: "Valider ce résultat" }).click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__saved.tasks[0].status))
    .toBe("done");
  fs.mkdirSync("docs/screenshots/v017", { recursive: true });
  await page.screenshot({
    path: "docs/screenshots/v017/next-step-proposal.png",
  });
  await page.getByRole("button", { name: "Choisir la suite" }).click();
  await expect(
    page.getByRole("combobox", { name: "Type de la prochaine étape" }),
  ).toHaveValue("specification");
  await page.getByRole("button", { name: "Ajouter à la timeline" }).click();
  await expect
    .poll(() =>
      page.evaluate(() => (window as any).__saved.tasks[0].steps.length),
    )
    .toBe(2);
  expect(await page.evaluate(() => (window as any).__starts.length)).toBe(1);
  await page
    .getByRole("button", { name: "Lancer Formaliser le besoin" })
    .click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts.length))
    .toBe(2);
  const run = await page.evaluate(() => (window as any).__starts[1]);
  expect(run.mode).toBe("plan");
  expect(run.step.type).toBe("specification");
});

test("canonical support survives provider revision and remains in context across steps", async ({
  page,
}) => {
  const state = base();
  const task = state.tasks[0] as any;
  task.workflowMode = "flexible";
  task.steps = [step("s1")];
  task.artifacts = [
    {
      id: "spec",
      title: "Spécification produit",
      type: "document",
      content: "# Décisions produit",
      revision: 1,
      editedBy: "agent",
      updatedAt: time,
      stepId: "s1",
    },
  ];
  await setup(page, state);
  await page
    .getByRole("button", { name: "Supports", exact: false })
    .first()
    .click();
  await page
    .getByRole("button", { name: "Définir comme source de vérité" })
    .click();
  await expect(
    page.getByRole("button", { name: "Source de vérité", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  fs.mkdirSync("docs/screenshots/v017", { recursive: true });
  await page.screenshot({
    path: "docs/screenshots/v017/canonical-support.png",
  });
  await send(page, "Actualiser le document.");
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts.length))
    .toBe(1);
  await emit(page, "artifact", {
    id: "spec",
    title: "Spécification produit",
    type: "document",
    content: "# Version consolidée",
  });
  await expect
    .poll(() =>
      page.evaluate(
        () => (window as any).__saved.tasks[0].artifacts[0].sourceOfTruth,
      ),
    )
    .toBe(true);
  await emit(page, "status", { status: "completed" });
  await page.getByRole("button", { name: "Mission", exact: true }).click();
  await page.getByRole("button", { name: "Valider ce résultat" }).click();
  await page.getByRole("button", { name: "Choisir la suite" }).click();
  await page.getByRole("button", { name: "Ajouter à la timeline" }).click();
  await page.getByRole("button", { name: "Lancer Réflexion" }).click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__starts.length))
    .toBe(2);
  const prompt = await page.evaluate(() => (window as any).__starts[1].prompt);
  expect(prompt).toContain('"sourceOfTruth":true');
  expect(prompt).toContain("Version consolidée");
});

test("model picker preserves legacy selections, retries discovery, and filters by readable name", async ({
  page,
}) => {
  await setup(page);
  await page.evaluate(() => {
    (window as any).__modelError = true;
  });
  await page
    .getByRole("button", { name: "Options de la mission", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Configurer la mission", exact: true })
    .click();
  const picker = page.getByRole("button", {
    name: "Choisir le modèle",
    exact: true,
  });
  await expect(picker).toContainText("configured");
  await picker.click();
  await expect(
    page.getByText("Catalogue indisponible pour le test"),
  ).toBeVisible();
  await expect(page.getByRole("option", { name: /configured/ })).toBeVisible();
  await page.evaluate(() => {
    (window as any).__modelError = false;
  });
  await page.getByRole("button", { name: /Réessayer|Actualiser/ }).click();
  const search = page.getByRole("combobox", { name: "Rechercher un modèle" });
  await search.fill("does not exist");
  await expect(
    page
      .getByRole("listbox", { name: "Modèles disponibles" })
      .getByRole("option"),
  ).toHaveCount(0);
  await search.fill("fixture model");
  await page.getByRole("option", { name: /Fixture Model/ }).click();
  await expect(picker).toContainText("Fixture Model");
  expect(
    await page.evaluate(
      () =>
        (window as any).__saved.tasks.find((t: any) => t.id === "task").model,
    ),
  ).toBe("configured");
});
