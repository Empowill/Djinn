import { expect, test, type Locator, type Page } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

const screenshotsDir = path.resolve("docs/screenshots");

async function openDemo(page: Page) {
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "Espace projets" }),
  ).toBeVisible();
  await expect(page.locator(".tab-content")).toHaveCSS("opacity", "1");
}

async function selectWireframe(page: Page) {
  const wireframe = page
    .locator(".art-artifact-card")
    .filter({ hasText: "Espace projets · proposition" })
    .first();
  await expect(wireframe).toBeVisible();
  await wireframe.click();
  await expect(page.locator(".art-main-heading h1")).toHaveText(
    "Espace projets · proposition",
  );
}

async function answerRecommended(page: Page, questionId: string) {
  const card = page
    .locator(".question-card")
    .filter({ hasText: questionId })
    .first();
  await expect(card).toBeVisible();
  if (
    (await card.getByRole("button", { name: /Valider ce choix/ }).count()) === 0
  ) {
    await card.getByRole("button", { name: new RegExp(questionId) }).click();
  }
  await card.getByRole("button", { name: /Valider ce choix/ }).click();
}

async function answerQuestion(page: Page, questionId: string, label: string) {
  const card = page
    .locator(".question-card")
    .filter({ hasText: questionId })
    .first();
  await expect(card).toBeVisible();
  if (
    (await card.getByRole("button", { name: /Valider ce choix/ }).count()) === 0
  ) {
    await card.getByRole("button", { name: new RegExp(questionId) }).click();
  }
  await card.getByRole("button", { name: new RegExp(label) }).click();
  await card.getByRole("button", { name: /Valider ce choix/ }).click();
}

async function answerCustom(page: Page, questionId: string, value: string) {
  const card = page
    .locator(".question-card")
    .filter({ hasText: questionId })
    .first();
  await expect(card).toBeVisible();
  if (
    (await card.getByRole("button", { name: /Valider ce choix/ }).count()) === 0
  ) {
    await card.getByRole("button", { name: new RegExp(questionId) }).click();
  }
  await card.getByRole("button", { name: /J’ai une autre idée/ }).click();
  await card
    .getByRole("textbox", { name: "Votre réponse personnalisée" })
    .fill(value);
  await card.getByRole("button", { name: /Valider ce choix/ }).click();
}

async function completeDemoDecisions(page: Page) {
  await answerRecommended(page, "Q01");
  await answerRecommended(page, "Q02");
}

async function openGeneratedQuestion(page: Page) {
  await expect(
    page.getByRole("button", { name: "Questions et actions" }),
  ).toBeVisible();
  const alerts = page.getByRole("region", {
    name: "Nouvelles questions et actions",
  });
  await expect(alerts).toBeVisible({ timeout: 12_000 });
  const alert = alerts
    .locator(".question-alert")
    .filter({ hasText: "Comment signaler l’arrivée d’une question" })
    .first();
  await expect(alert).toBeVisible();
  await alert.click();
  await expect(page.locator("#question-Q03")).toBeVisible({ timeout: 12_000 });
}

async function canvasSignature(canvas: Locator) {
  return canvas.evaluate((node) => {
    const target = node as HTMLCanvasElement;
    const context = target.getContext("2d");
    if (!context || !target.width || !target.height) return null;
    const pixels = context.getImageData(0, 0, target.width, target.height).data;
    let hash = 2166136261;
    for (const pixel of pixels) hash = Math.imul(hash ^ pixel, 16777619);
    return `${target.width}x${target.height}:${hash >>> 0}`;
  });
}

test.describe("mission workspace", () => {
  test("answers decisions, updates the wireframe, and reopens a decision", async ({
    page,
  }) => {
    await openDemo(page);
    await page.evaluate(() =>
      window.scrollTo({ top: 0, left: 0, behavior: "instant" }),
    );
    await expect(page.locator(".question-card").first()).toHaveCSS(
      "opacity",
      "1",
    );
    await page.screenshot({
      path: path.join(screenshotsDir, "mission.png"),
      fullPage: true,
    });

    await answerQuestion(page, "Q01", "Par équipe");
    await page
      .getByRole("navigation", { name: "Vues de la mission" })
      .getByRole("button", { name: /^Supports(?:\s+\d+)?$/ })
      .click();
    await selectWireframe(page);
    await expect(
      page.locator(".art-mini-layout-toggle button.is-current"),
    ).toContainText("Équipe");

    await page
      .getByRole("navigation", { name: "Vues de la mission" })
      .getByRole("button", { name: /^Mission(?:\s+\d+)?$/ })
      .click();
    await answerCustom(
      page,
      "Q02",
      "Un espace dédié, avec une recherche globale en cas de besoin.",
    );
    await expect(page.locator(".decisions-section")).toHaveCount(0);
    await page
      .getByRole("button", { name: /2 décisions enregistrées/ })
      .click();
    await expect(
      page.locator(".question-card.answered .answer-value").filter({
        hasText:
          "Un espace dédié, avec une recherche globale en cas de besoin.",
      }),
    ).toBeVisible();

    const q02 = page
      .locator(".question-card")
      .filter({ hasText: "Quelle place pour les projets archivés ?" })
      .first();
    await q02.locator(".question-heading").click();
    await q02.getByRole("button", { name: /Revoir la décision/ }).click();
    await q02.locator(".question-heading").click();
    await expect(
      q02.getByRole("button", { name: /Valider ce choix/ }),
    ).toBeVisible();
  });

  test("runs the demo through its generated question and resumes into review", async ({
    page,
    browser,
  }) => {
    await openDemo(page);
    await completeDemoDecisions(page);
    await expect(
      page.locator(".mission-controls button.button.accent"),
    ).toContainText(/(?:Lancer|Reprendre) la démo/);
    await page.locator(".mission-controls button.button.accent").click();

    await openGeneratedQuestion(page);
    await answerRecommended(page, "Q03");
    await expect(
      page.locator(".mission-controls button.button.accent"),
    ).toContainText(/(?:Lancer|Reprendre) la démo/);
    await page.locator(".mission-controls button.button.accent").click();
    await expect(page.locator(".agent-card.done")).toHaveCount(4, {
      timeout: 12_000,
    });
    await page
      .getByRole("navigation", { name: "Vues de la mission" })
      .getByRole("button", { name: "Review", exact: true })
      .click();
    await expect(
      page.getByRole("heading", { name: "Review" }).first(),
    ).toBeVisible();

    const videoDir = path.resolve("test-results/demo-video");
    fs.mkdirSync(videoDir, { recursive: true });
    const videoContext = await browser.newContext({
      baseURL: "http://127.0.0.1:4317",
      viewport: { width: 1440, height: 1000 },
      colorScheme: "dark",
      locale: "fr-FR",
      recordVideo: { dir: videoDir },
    });
    const videoPage = await videoContext.newPage();
    const recordedVideo = videoPage.video();
    try {
      await openDemo(videoPage);
      await completeDemoDecisions(videoPage);
      await videoPage.locator(".mission-controls button.button.accent").click();
      await openGeneratedQuestion(videoPage);
      await answerRecommended(videoPage, "Q03");
      await videoPage.locator(".mission-controls button.button.accent").click();
      await expect(videoPage.locator(".agent-card.done")).toHaveCount(4, {
        timeout: 12_000,
      });
      await videoPage
        .getByRole("navigation", { name: "Vues de la mission" })
        .getByRole("button", { name: "Review", exact: true })
        .click();
      await expect(
        videoPage.getByRole("heading", { name: "Review" }).first(),
      ).toBeVisible();
    } finally {
      await videoPage.close();
      const recordedPath = recordedVideo
        ? await recordedVideo.path()
        : undefined;
      if (recordedPath)
        fs.copyFileSync(recordedPath, path.resolve("docs/demo.webm"));
      await videoContext.close();
    }
  });

  test("requires review comments to be resolved before approval", async ({
    page,
  }) => {
    await openDemo(page);
    await completeDemoDecisions(page);
    await page
      .getByRole("navigation", { name: "Vues de la mission" })
      .getByRole("button", { name: "Review", exact: true })
      .click();
    await expect(page.locator(".art-review-banner")).toContainText("Review");
    await expect(page.locator(".tab-content")).toHaveCSS("opacity", "1");
    await page
      .locator(".toast")
      .waitFor({ state: "hidden", timeout: 6_000 })
      .catch(() => undefined);
    await page.evaluate(() =>
      window.scrollTo({ top: 0, left: 0, behavior: "instant" }),
    );
    await page.screenshot({
      path: path.join(screenshotsDir, "review.png"),
      fullPage: true,
    });

    const surface = page.locator(".art-canvas-surface").first();
    await surface.click({ position: { x: 150, y: 150 } });
    await expect(page.getByText("Nouveau point de review")).toBeVisible();
    await page
      .getByPlaceholder("Décrivez ce qui doit évoluer…")
      .fill("Le point de départ doit être plus explicite.");
    await page.getByRole("button", { name: /Épingler le commentaire/ }).click();
    await expect(
      page
        .locator(".art-comment-copy > span")
        .filter({ hasText: "Le point de départ doit être plus explicite." }),
    ).toBeVisible();

    await page.locator(".review-actions button.button.accent").click();
    await expect(
      page.getByText(
        "Résolvez les retours de review avant de préparer la livraison.",
      ),
    ).toBeVisible();

    await page.getByRole("button", { name: /Résoudre/ }).click();
    await page.locator(".review-actions button.button.accent").click();
    await expect(
      page.getByRole("heading", { name: "Livrables" }),
    ).toBeVisible();
  });

  test("exports and imports a portable mission session", async ({ page }) => {
    await openDemo(page);
    const downloadPromise = page.waitForEvent("download");
    await page.getByRole("button", { name: "Partager" }).click();
    const download = await downloadPromise;
    const exportedPath = await download.path();
    expect(exportedPath).toBeTruthy();
    const exported = JSON.parse(fs.readFileSync(exportedPath!, "utf8")) as {
      format: string;
      version: number;
      task: { title: string };
    };
    expect(exported.format).toBe("djinn-session");
    expect(exported.version).toBe(1);
    expect(exported.task.title).toContain("Espace projets");

    const importInput = page.locator(
      'input[type="file"][accept=".json,.djinn"]',
    );
    await importInput.setInputFiles({
      name: "portable.djinn.json",
      mimeType: "application/json",
      buffer: Buffer.from(JSON.stringify(exported)),
    });
    await expect(page.getByText("Souhait importé.")).toBeVisible();
    await expect(page.locator(".mission-list .mission-nav")).toHaveCount(2);
  });

  test("persists a decision across a browser reload", async ({ page }) => {
    await openDemo(page);
    await answerQuestion(page, "Q01", "Par équipe");
    await page.waitForTimeout(1_500);
    await page.reload();
    await page
      .getByRole("navigation", { name: "Vues de la mission" })
      .getByRole("button", { name: /^Supports(?:\s+\d+)?$/ })
      .click();
    await selectWireframe(page);
    await expect(
      page.locator(".art-mini-layout-toggle button.is-current"),
    ).toContainText("Équipe");
  });

  test("creates a mission from the fullscreen intent after choosing a project", async ({
    page,
  }) => {
    await openDemo(page);
    await page.getByRole("button", { name: "Nouvelle mission" }).click();
    await expect(
      page.getByRole("heading", { name: "Qu’allez-vous créer ?" }),
    ).toBeVisible();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await page
      .getByRole("button", { name: "Créer un projet", exact: true })
      .last()
      .click();
    await expect(page.getByRole("dialog")).toBeVisible();
    await page
      .getByLabel("Nom du projet")
      .fill("Projet de test local");
    await page
      .getByLabel(/^Dossier du projet/)
      .fill("/tmp/djinn-project");
    await page.getByRole("button", { name: "Créer le projet" }).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(
      page.getByRole("heading", { name: "Qu’allez-vous créer ?" }),
    ).toBeVisible();
    await page
      .getByRole("textbox", { name: "Votre intention" })
      .fill("Vérifier la création locale et sa configuration.");
    await expect(
      page.getByText("Djinn définit le workflow avant de commencer."),
    ).toBeVisible();
    await page.getByRole("button", { name: "Préparer la mission" }).click();
    await expect(
      page.getByRole("heading", { name: "Préparation en pause" }),
    ).toBeVisible();
    await expect(
      page.getByText("Vérifier la création locale et sa configuration."),
    ).toBeVisible();
    await expect(page.locator(".mission-start-project")).toContainText(
      "Projet de test local",
    );
  });

  test("freezes completed orbs and resumes an agent on the next passage", async ({
    page,
  }) => {
    await openDemo(page);
    await completeDemoDecisions(page);
    await page.locator(".mission-controls button.button.accent").click();
    await openGeneratedQuestion(page);
    await answerRecommended(page, "Q03");

    const views = page.getByRole("navigation", { name: "Vues de la mission" });
    await views.getByRole("button", { name: "Timeline", exact: true }).click();
    const atlasRow = page.getByRole("button", {
      name: "Ouvrir le chat de Atlas",
    });
    const doneCanvas = atlasRow.locator(".orb-done canvas").first();
    await expect(doneCanvas).toBeVisible({ timeout: 6_000 });
    await page.waitForTimeout(250);
    const doneBefore = await canvasSignature(doneCanvas);
    await page.waitForTimeout(450);
    const doneAfter = await canvasSignature(doneCanvas);
    expect(doneBefore).toBeTruthy();
    expect(doneAfter).toBe(doneBefore);

    await views.getByRole("button", { name: "Mission", exact: true }).click();
    await page.locator(".mission-controls button.button.accent").click();
    await views.getByRole("button", { name: "Timeline", exact: true }).click();
    const runningCanvas = atlasRow.locator(".orb-running canvas").first();
    await expect(runningCanvas).toBeVisible({ timeout: 6_000 });
    await page.waitForTimeout(250);
    const runningBefore = await canvasSignature(runningCanvas);
    await page.waitForTimeout(450);
    const runningAfter = await canvasSignature(runningCanvas);
    expect(runningBefore).toBeTruthy();
    expect(runningAfter).not.toBe(runningBefore);

    await atlasRow.click();
    const chat = page.getByRole("dialog", { name: "Atlas" });
    await expect(chat).toBeVisible();
    await expect
      .poll(() => chat.evaluate((node) => getComputedStyle(node).opacity))
      .toBe("1");
    await expect
      .poll(() => chat.evaluate((node) => getComputedStyle(node).transform))
      .toMatch(/^(none|matrix\(1, 0, 0, 1, 0, 0\))$/);
    const toastClose = page.getByRole("button", {
      name: "Fermer la notification",
    });
    if (await toastClose.count()) await toastClose.click();
    await page.waitForTimeout(350);
    await page.screenshot({
      path: path.join(screenshotsDir, "agent-chat.png"),
      fullPage: true,
    });
  });

  test("keeps the compact mission header usable at desktop widths", async ({
    page,
  }) => {
    for (const width of [1440, 1024]) {
      await page.setViewportSize({ width, height: 1000 });
      await openDemo(page);
      const bar = page.locator(".mission-bar");
      const nav = bar.locator(".tabbar");
      const controls = bar.locator(".mission-controls");
      await expect(bar).toBeVisible();
      await expect(nav).toBeVisible();
      await expect(controls).toBeVisible();
      const geometry = await page.evaluate(() => {
        const read = (selector: string) => {
          const node = document.querySelector<HTMLElement>(selector);
          if (!node) return null;
          const rect = node.getBoundingClientRect();
          return {
            left: rect.left,
            right: rect.right,
            top: rect.top,
            bottom: rect.bottom,
          };
        };
        const tab = read(".mission-bar .tabbar");
        const actions = read(".mission-bar .mission-controls");
        const items = [
          ...document.querySelectorAll<HTMLElement>(
            ".mission-bar .tabbar > button",
          ),
        ].map((node) => {
          const rect = node.getBoundingClientRect();
          return { left: rect.left, right: rect.right };
        });
        return { tab, actions, items };
      });
      expect(geometry.tab).toBeTruthy();
      expect(geometry.actions).toBeTruthy();
      expect(geometry.tab!.right).toBeLessThanOrEqual(
        geometry.actions!.left + 1,
      );
      expect(
        geometry.items.every(
          (item) =>
            item.right <= geometry.actions!.left + 1 ||
            item.left >= geometry.actions!.right - 1,
        ),
      ).toBe(true);
      await expect(
        bar.locator(".mission-controls button.button.accent"),
      ).toBeVisible();
      if (width === 1440) {
        await page.screenshot({
          path: path.join(screenshotsDir, "mission.png"),
          fullPage: true,
        });
        await page
          .getByRole("navigation", { name: "Vues de la mission" })
          .getByRole("button", { name: "Timeline", exact: true })
          .click();
        await expect(
          page.getByRole("heading", { name: "Timeline" }),
        ).toBeVisible();
        await expect(page.locator(".tab-content")).toHaveCSS("opacity", "1");
        await page.screenshot({
          path: path.join(screenshotsDir, "timeline.png"),
          fullPage: true,
        });
      }
    }
  });

  test("shows the timeline, chats with an agent, and uses the command palette", async ({
    page,
  }) => {
    await openDemo(page);
    await page
      .getByRole("navigation", { name: "Vues de la mission" })
      .getByRole("button", { name: "Timeline", exact: true })
      .click();
    await expect(page.getByRole("heading", { name: "Timeline" })).toBeVisible();
    await expect(page.locator(".tab-content")).toHaveCSS("opacity", "1");
    await page.evaluate(() =>
      window.scrollTo({ top: 0, left: 0, behavior: "instant" }),
    );
    await page.screenshot({
      path: path.join(screenshotsDir, "timeline.png"),
      fullPage: true,
    });
    await page.getByRole("button", { name: "Ouvrir le chat de Atlas" }).click();
    const atlasChat = page.getByRole("dialog", { name: "Atlas" });
    await expect(atlasChat).toBeVisible();
    await expect(
      atlasChat.getByRole("log", { name: "Conversation avec Atlas" }),
    ).toBeVisible();
    await expect(atlasChat.locator("details.ac-tool").first()).toBeVisible();
    await atlasChat
      .getByPlaceholder("Message à Atlas…")
      .fill("Prioriser la lisibilité des états dans le prochain passage.");
    await atlasChat.getByRole("button", { name: "Envoyer à Atlas" }).click();
    await expect(atlasChat.locator(".ac-message-user")).toContainText(
      "Prioriser la lisibilité des états dans le prochain passage.",
    );
    await atlasChat
      .getByRole("button", { name: "Fermer la conversation" })
      .click();
    await page.getByRole("button", { name: "Ouvrir le chat de Djinn" }).click();
    const chat = page.getByRole("dialog", { name: "Djinn" });
    await expect(chat.locator(".ac-message-assistant").first()).toBeVisible();
    await page.waitForTimeout(800);
    await chat.getByRole("button", { name: "Fermer la conversation" }).click();
    await page.reload();
    await page
      .getByRole("navigation", { name: "Vues de la mission" })
      .getByRole("button", { name: "Timeline", exact: true })
      .click();
    await page.getByRole("button", { name: "Ouvrir le chat de Atlas" }).click();
    const restoredChat = page.getByRole("dialog", { name: "Atlas" });
    await expect(restoredChat.locator(".ac-message-user")).toContainText(
      "Prioriser la lisibilité des états dans le prochain passage.",
    );
    await restoredChat
      .getByRole("button", { name: "Fermer la conversation" })
      .click();

    await page.keyboard.press("Control+k");
    await expect(
      page.getByRole("dialog", { name: "Rechercher" }),
    ).toBeVisible();
    await page.getByLabel("Rechercher une commande").fill("review");
    await expect(
      page.getByRole("button", { name: /Ouvrir la review visuelle/ }),
    ).toBeVisible();
    await page
      .getByRole("button", { name: /Ouvrir la review visuelle/ })
      .click();
    await expect(
      page.getByRole("heading", { name: "Review" }).first(),
    ).toBeVisible();
  });

  test("places agents below the mission header and aligns parallel work with interventions", async ({
    page,
  }) => {
    await openDemo(page);
    const hero = await page.locator(".hero").boundingBox();
    const team = await page.locator(".team-section").boundingBox();
    expect(team!.y).toBeGreaterThanOrEqual(hero!.y + hero!.height - 1);
    await page.waitForFunction(
      () => !!localStorage.getItem("djinn.workspace.v1"),
    );
    await page.evaluate(() => {
      const state = JSON.parse(localStorage.getItem("djinn.workspace.v1")!);
      const task = state.tasks[0];
      const base = Date.now() - 120000;
      const at = (seconds: number) =>
        new Date(base + seconds * 1000).toISOString();
      task.status = "idle";
      task.questions = [];
      task.instructions = [];
      task.feedback = [];
      task.agents = task.agents.map((a: any) => ({
        ...a,
        status: "done",
        progress: 100,
      }));
      task.events = [];
      for (const [id, start, end] of [
        ["lead", 0, 60],
        ["design", 0, 30],
        ["build", 10, 50],
        ["review", 35, 60],
      ] as const) {
        task.events.push({
          id: `${id}-start`,
          agentId: id,
          type: "agent",
          title: `${id} démarre`,
          detail: "",
          time: at(start),
          lifecycle: "started",
          runId: `run-${id}`,
        });
        task.events.push({
          id: `${id}-end`,
          agentId: id,
          type: "agent",
          title: `${id} termine`,
          detail: "",
          time: at(end),
          lifecycle: "completed",
          runId: `run-${id}`,
        });
      }
      task.instructions = [
        {
          id: "instruction-1",
          text: "Rendre le parcours plus simple",
          time: at(15),
          agentId: "design",
        },
      ];
      task.events.push({
        id: "human-mirror",
        type: "note",
        title: "Vous",
        detail: "Rendre le parcours plus simple",
        time: at(15),
        actor: "human",
        interventionId: "instruction-1",
        agentId: "design",
      });
      task.questions = [
        {
          id: "Q01",
          title: "Quelle navigation ?",
          context: "",
          recommendation: "",
          options: [],
          blocking: false,
          unlocks: "",
          answer: "Par équipe",
          answeredAt: at(40),
          agentId: "design",
        },
      ];
      task.feedback = [
        {
          id: "feedback-1",
          artifactId: task.artifacts[0].id,
          text: "Aligner les espacements",
          x: 10,
          y: 10,
          resolved: false,
          createdAt: at(55),
        },
      ];
      localStorage.setItem("djinn.workspace.v1", JSON.stringify(state));
    });
    await page.reload();
    await page
      .getByRole("navigation", { name: "Vues de la mission" })
      .getByRole("button", { name: "Timeline", exact: true })
      .click();
    await expect(page.locator(".temporal-interval")).toHaveCount(4);
    await expect(page.locator(".temporal-human-marker")).toHaveCount(3);
    const positions = await page
      .locator(".temporal-interval-wrap")
      .evaluateAll((nodes) =>
        nodes.map((n) => parseFloat((n as HTMLElement).style.left)),
      );
    expect(positions[1]).toBe(0);
    expect(positions[2]).toBeCloseTo(100 / 6, 1);
    expect(positions[3]).toBeCloseTo(350 / 6, 1);
    const humans = await page
      .locator(".temporal-human-marker")
      .evaluateAll((nodes) =>
        nodes.map((n) => parseFloat((n as HTMLElement).style.left)),
      );
    expect(humans[0]).toBeCloseTo(25, 3);
    expect(humans[1]).toBeCloseTo((100 * 40) / 60, 3);
    expect(humans[2]).toBeCloseTo((100 * 55) / 60, 3);
    await page.locator(".temporal-human-marker.instruction button").click();
    await expect(page.locator(".temporal-popover-human")).toContainText(
      "Rendre le parcours plus simple",
    );
    await page.getByRole("button", { name: "Ouvrir le chat de Atlas" }).click();
    await expect(page.getByRole("dialog", { name: "Atlas" })).toBeVisible();
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: "Augmenter l’échelle" }).click();
    await expect(page.locator(".temporal-fit-button")).toBeEnabled();
    await page.getByRole("button", { name: "Ajuster", exact: true }).click();
    const fitted = await page
      .locator(".temporal-scroll")
      .evaluate((element) => ({
        viewport: element.clientWidth,
        canvas: element
          .querySelector(".temporal-canvas")!
          .getBoundingClientRect().width,
      }));
    expect(Math.abs(fitted.canvas - fitted.viewport)).toBeLessThan(2);
    await expect(page.locator(".tab-content")).toHaveCSS("opacity", "1");
    await page.screenshot({
      path: path.join(screenshotsDir, "temporal-timeline.png"),
      fullPage: true,
    });
  });

  test("groups restitution actions and supports, notifies, and keeps its stop control", async ({
    page,
  }) => {
    await page.addInitScript(() => {
      const w = window as any;
      w.__operations = [];
      w.__notifications = [];
      w.djinn = {
        loadState: async () => null,
        saveState: async (state: any) => {
          w.__savedState = state;
          return { saved: true };
        },
        getEnvironment: async () => ({
          platform: "darwin",
          appVersion: "0.1.2",
          providers: [],
        }),
        getActions: async () => [],
        onEvent: (callback: any) => {
          w.__emit = callback;
          return () => {};
        },
        notifyQuestion: async (input: any) => {
          w.__notifications.push(input);
          return { shown: true };
        },
        performAction: async (input: any) => {
          w.__operations.push(input);
          return {
            ...input.action,
            updatedAt: new Date().toISOString(),
            status:
              input.operation === "stop"
                ? "stopped"
                : input.operation === "complete"
                  ? "done"
                  : "ready",
          };
        },
      };
    });
    await openDemo(page);
    await page.waitForFunction(
      () => !!(window as any).__savedState && !!(window as any).__emit,
    );
    await page.evaluate(() => {
      const w = window as any;
      const time = new Date().toISOString();
      const taskId = w.__savedState.tasks[0].id;
      w.__emit({
        taskId,
        runId: "completed-run",
        type: "artifact",
        timestamp: time,
        data: {
          id: "result-support",
          title: "Rapport de vérification",
          type: "document",
          content: "# Rapport\n\nLa vérification est terminée.",
        },
      });
      w.__action = {
        id: "preview",
        kind: "server",
        title: "Aperçu du projet",
        status: "running",
        script: "dev",
        createdAt: time,
        updatedAt: time,
      };
      w.__emit({
        taskId,
        runId: "completed-run",
        type: "action",
        timestamp: time,
        data: w.__action,
      });
    });
    await expect(page.locator("#actions-section")).toContainText(
      "Supports produits",
    );
    await expect(page.locator("#actions-section")).toContainText(
      "Rapport de vérification",
    );
    await expect(page.locator("#actions-section")).toContainText("Démarrage…");
    expect(
      await page.evaluate(() => (window as any).__notifications.length),
    ).toBe(0);
    await page.evaluate(() => {
      const w = window as any;
      const time = new Date().toISOString();
      w.__action = {
        ...w.__action,
        status: "ready",
        url: "http://127.0.0.1:5273",
        updatedAt: time,
      };
      w.__emit({
        taskId: w.__savedState.tasks[0].id,
        runId: "completed-run",
        type: "action",
        timestamp: time,
        data: w.__action,
      });
    });
    await expect(page.locator(".question-alerts")).toContainText(
      "Aperçu du projet",
    );
    await expect
      .poll(() => page.evaluate(() => (window as any).__notifications.length))
      .toBe(1);
    await page
      .locator(".question-alert")
      .filter({ hasText: "Aperçu du projet" })
      .click();
    await page
      .locator("#actions-section")
      .getByRole("button", { name: "Ouvrir", exact: true })
      .click();
    await expect(
      page.getByRole("button", { name: "Arrêter Aperçu du projet" }),
    ).toBeVisible();
    await page
      .getByRole("button", { name: "Arrêter Aperçu du projet" })
      .click();
    await expect(page.locator("#actions-section")).toContainText("Arrêté");
    await page
      .locator("#actions-section")
      .getByRole("button", { name: "Relancer", exact: true })
      .click();
    await expect(
      page
        .locator("#actions-section")
        .getByRole("button", { name: "Ouvrir", exact: true }),
    ).toBeVisible();
    expect(
      await page.evaluate(() =>
        (window as any).__operations.map((a: any) => a.operation),
      ),
    ).toEqual(["open", "stop", "run"]);
    await page.evaluate(() => {
      const w = window as any;
      const time = new Date().toISOString();
      w.__emit({
        taskId: w.__savedState.tasks[0].id,
        runId: "completed-run",
        type: "action",
        timestamp: time,
        data: {
          id: "check",
          kind: "manual",
          title: "Vérifier le résultat",
          status: "pending",
          createdAt: time,
          updatedAt: time,
        },
      });
    });
    await page
      .locator("#actions-section")
      .getByRole("button", { name: "Fait", exact: true })
      .click();
    await expect(page.locator("#action-check")).toContainText("Terminé");
    await page.locator(".question-alert-heading button").click();
    await expect(page.locator(".tab-content")).toHaveCSS("opacity", "1");
    await page.screenshot({
      path: path.join(screenshotsDir, "actions.png"),
      fullPage: true,
    });
  });
});
