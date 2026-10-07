import { test, expect } from "@playwright/test";
import { readFile } from "node:fs/promises";

for (const { preparation, failedSave } of [
  { preparation: false, failedSave: false },
  { preparation: true, failedSave: false },
  { preparation: false, failedSave: true },
])
  test(
    failedSave
      ? "failed pre-launch save leaves the stage in error and never launches a provider"
      : `provider tag resumes ${preparation ? "preparation" : "interrupted work"} without losing context`,
    async ({ page }) => {
      const { task } = JSON.parse(
        await readFile("docs/v0.2.1-session-harmonisation.djinn.json", "utf8"),
      );
      Object.assign(task, {
        demo: false,
        title: "Mission interrompue",
        project: "/tmp/djinn-provider-recovery",
        provider: "claude",
        model: "claude-opus-4-6",
        status: "error",
        runId: undefined,
        questions: [],
        actions: [],
        feedback: [],
        instructions: [],
        runtimeEventIds: [],
        providerSessions: { lead: "old-provider-session" },
      });
      task.steps[0].type = preparation ? "discussion" : "implementation";
      task.steps[0].status = "error";
      task.events = [
        {
          id: "quota",
          time: new Date().toISOString(),
          type: "error",
          title: "Quota Claude",
          detail: "out_of_credits",
          stepId: task.activeStepId,
        },
      ];
      task.agents = [
        {
          id: "worker",
          name: "Travail interrompu",
          role: "Développement",
          model: task.model,
          status: "error",
          progress: 40,
          summary: "Fichiers déjà modifiés",
          prompt: "Terminer le travail existant",
          writeScope: ["src/feature.ts"],
          stepId: task.activeStepId,
        },
      ];
      const state = {
        version: 2,
        tasks: [task],
        projects: [],
        selectedId: task.id,
        settings: {
          provider: "codex",
          model: "",
          reduceMotion: true,
          sound: false,
        },
      };
      await page.addInitScript(
        ({ state, failedSave }) => {
          const w = window as any;
          w.starts = [];
          w.djinn = {
            loadState: async () => state,
            saveState: async (value: any) => {
              if (
                failedSave &&
                value.tasks[0].steps.some(
                  (step: any) => step.status === "running",
                )
              )
                throw new Error(
                  "Sauvegarde impossible : state exceeds the 12000000 character limit",
                );
              w.savedState = value;
            },
            getRuntimeSnapshot: async () => ({
              capturedAt: new Date().toISOString(),
              runs: [],
            }),
            getActions: async () => [],
            getEnvironment: async () => ({
              platform: "darwin",
              appVersion: "0.2.1",
              providers: ["codex", "claude"].map((id) => ({
                id,
                name: id === "codex" ? "Codex" : "Claude Code",
                available: true,
                authenticated: true,
              })),
            }),
            startRun: async (input: unknown) => {
              w.starts.push(input);
              return { runId: "resumed-run" };
            },
            onEvent: (callback: unknown) => {
              w.emitRuntime = callback;
              return () => {};
            },
          };
        },
        { state, failedSave },
      );
      const errors: string[] = [];
      page.on("pageerror", (error) => errors.push(error.message));
      await page.goto("/");
      await page.locator(".provider-switch-summary").click();
      await page
        .getByRole("combobox", { name: "Fournisseur", exact: true })
        .selectOption("codex");
      await page
        .getByRole("button", { name: "Changer et reprendre", exact: true })
        .click();
      if (failedSave) {
        await expect
          .poll(() =>
            page.evaluate(() => (window as any).savedState?.tasks[0]?.status),
          )
          .toBe("error");
        const saved = await page.evaluate(
          () => (window as any).savedState.tasks[0],
        );
        expect(
          saved.steps.find((step: any) => step.id === task.activeStepId).status,
        ).toBe("error");
        expect(
          saved.agents.find((agent: any) => agent.id === "lead")?.status,
        ).not.toBe("running");
        expect(await page.evaluate(() => (window as any).starts.length)).toBe(
          0,
        );
        expect(errors).toEqual([]);
        return;
      }
      await expect
        .poll(() => page.evaluate(() => (window as any).starts.length))
        .toBe(1);
      const { input, saved } = await page.evaluate(() => ({
        input: (window as any).starts[0],
        saved: (window as any).savedState.tasks[0],
      }));
      expect(input.provider).toBe("codex");
      expect(input.model).toBeUndefined();
      expect(input.stepId).toBe(task.activeStepId);
      expect(input.cwd).toBe(task.project);
      expect(input.providerSessions).toBeUndefined();
      expect(input.prompt).toContain("out_of_credits");
      expect(input.prompt).toContain("Fournisseur changé");
      expect(saved.artifacts).toEqual(task.artifacts);
      expect(saved.brief).toBe(task.brief);
      expect(saved.steps.map((s: any) => s.id)).toEqual(
        task.steps.map((s: any) => s.id),
      );
      expect(saved.events.some((e: any) => e.id === "quota")).toBe(true);
      if (!preparation) expect(input.agents[0].id).toBe("worker");
      await expect(page.locator(".provider-switch-summary")).toHaveAttribute(
        "aria-disabled",
        "true",
      );
      expect(await page.evaluate(() => (window as any).starts.length)).toBe(1);
      expect(errors).toEqual([]);
    },
  );
