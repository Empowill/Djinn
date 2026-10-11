// A wish opens as a sparse summary and grows into a persistent, composable workspace. These tests use the browser
// transport's real data and geometry: every section starts closed, content panels compose by deliberate drag, and
// global actions stay usable while the workspace is split or maximized.
import { create, toBinary } from "@bufbuild/protobuf";
import {
  expect,
  test,
  type Locator,
  type Page,
  type Route,
} from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { UiServiceWatchShowResponseSchema } from "../gen/ts/ui/v1/ui_pb";
import { portable } from "./portable";

portable();

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
const shots = path.resolve(__dirname, "../test-results/e2e");

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

type WorkspaceFixture = {
  wishId: string;
  otherWishId: string;
  title: string;
  otherTitle: string;
  qOpen: string;
  taskId: string;
  tilasmId: string;
  projectName: string;
  directory: string;
  importDirectory: string;
  tilasmDirectory: string;
};

function pauseActiveWishes() {
  const listed = (JSON.parse(djinn("wish", "list", "--json")).wishes ?? []) as {
    id: string;
    state: string;
  }[];
  for (const wish of listed.filter(
    (item) => item.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", wish.id);
}

function fixture(): WorkspaceFixture {
  pauseActiveWishes();
  const suffix = randomUUID().slice(0, 8);
  const directory = fs.realpathSync(
    fs.mkdtempSync(
      path.join(process.env.DJINN_E2E_HOME!, "workspace-project-"),
    ),
  );
  const projectName = `Workspace project ${suffix}`;
  const projectId = JSON.parse(
    djinn("project", "add", directory, "--name", projectName, "--json"),
  ).project.id as string;
  const wishId = randomUUID();
  const title = `Compose the lamp ${suffix}`;
  const otherTitle = `Keep another lamp ${suffix}`;
  const qOpen = randomUUID();
  const qDecision = randomUUID();
  const taskId = randomUUID();
  const at = "2026-10-09T08:00:00Z";
  const importDirectory = fs.mkdtempSync(
    path.join(process.env.DJINN_E2E_HOME!, "workspace-import-"),
  );
  const importFile = path.join(importDirectory, "workspace.json");
  fs.writeFileSync(
    importFile,
    JSON.stringify({
      version: 1,
      create_time: at,
      wish: {
        id: wishId,
        title,
        project_ids: [projectId],
        create_time: at,
      },
      projects: [
        {
          id: projectId,
          name: projectName,
        },
      ],
      tasks: [
        {
          id: taskId,
          wish_id: wishId,
          project_id: projectId,
          code: "W1",
          title: "Wait for the lamp answer",
          status: "TASK_STATUS_WAITING",
          provider: "PROVIDER_FAKE",
          edit_question_id: qOpen,
          decision: "Q02",
          create_time: at,
        },
      ],
      questions: [
        {
          id: qOpen,
          wish_id: wishId,
          code: "Q01",
          text: "May the worker edit the lamp?",
          options: ["Yes, edit the lamp", "No, keep it read only"],
          recommendation: "A, the waiting worker needs the answer.",
          create_time: at,
        },
        {
          id: qDecision,
          wish_id: wishId,
          code: "Q02",
          text: "Which oil should the lamp use?",
          options: ["Olive", "Paraffin"],
          answer: { choice: "CHOICE_B", create_time: at },
          create_time: at,
        },
      ],
      blocks: [
        {
          id: randomUUID(),
          wish_id: wishId,
          kind: "report",
          title: "Worker report",
          content: "The worker measured the lamp.",
          create_time: at,
        },
        {
          id: randomUUID(),
          wish_id: wishId,
          kind: "decision",
          title: "Ship the paraffin lamp",
          content: "The answer is recorded here.",
          create_time: at,
        },
        {
          id: randomUUID(),
          wish_id: wishId,
          kind: "log",
          title: "The first journal entry",
          content: "The workspace was created.",
          create_time: at,
        },
      ],
      instructions: [
        {
          id: randomUUID(),
          wish_id: wishId,
          code: "I01",
          text: "Keep the workspace arrangement.",
          status: "INSTRUCTION_STATUS_PENDING",
          create_time: at,
          update_time: at,
        },
      ],
    }),
  );
  djinn("wish", "import", importFile);
  const tilasmDirectory = fs.mkdtempSync(
    path.join(process.env.DJINN_E2E_HOME!, "workspace-tilasm-"),
  );
  fs.writeFileSync(
    path.join(tilasmDirectory, "index.html"),
    "<!doctype html><title>Workspace model</title><h1 id=workspace-model>Workspace model</h1>",
  );
  const tilasmId = JSON.parse(
    djinn("tilasm", "put", tilasmDirectory, "--wish", wishId, "--json"),
  ).tilasm.id as string;
  // A link gives the repeated deep-link check a real source after the first Tilasm panel is closed.
  djinn(
    "block",
    "put",
    wishId,
    "--kind",
    "log",
    "--title",
    "Open the workspace model",
    "--content",
    `See [the workspace model](djinn://tilasm/${tilasmId}).`,
  );
  const other = JSON.parse(
    djinn("wish", "make", otherTitle, "--paused", "--json"),
  ).wish.id as string;
  return {
    wishId,
    otherWishId: other,
    title,
    otherTitle,
    qOpen,
    taskId,
    tilasmId,
    projectName,
    directory,
    importDirectory,
    tilasmDirectory,
  };
}

function cleanup(sample: WorkspaceFixture) {
  for (const id of [sample.wishId, sample.otherWishId]) {
    try {
      djinn("wish", "pause", id);
    } catch {
      // The test may already have deleted or paused the wish.
    }
  }
  fs.rmSync(sample.importDirectory, { recursive: true, force: true });
  fs.rmSync(sample.tilasmDirectory, { recursive: true, force: true });
  fs.rmSync(sample.directory, { recursive: true, force: true });
}

const stage = (page: Page) =>
  page.locator('.wish-workspace[data-wish-workspace="stage"]');
const panel = (page: Page, id: string) =>
  stage(page).locator(`[data-wish-panel="${id}"]`);
const section = (page: Page, id: string) =>
  stage(page).locator(`[data-wish-section="${id}"]`);
const sectionButton = (page: Page, id: string) => section(page, id);

const globalTrigger = (page: Page) => page.locator(".workspace-action-trigger");

const globalDialog = (page: Page) =>
  page.getByRole("dialog", { name: /^(Your move|À toi de jouer)$/ });

async function assertGlobalTriggerClearsToolbar(page: Page) {
  const toolbar = await page.locator(".app-toolbar").boundingBox();
  const trigger = await globalTrigger(page).boundingBox();
  expect(toolbar).not.toBeNull();
  expect(trigger).not.toBeNull();
  if (
    toolbar &&
    trigger &&
    toolbar.y < trigger.y + trigger.height &&
    trigger.y < toolbar.y + toolbar.height
  )
    expect(trigger.x).toBeGreaterThanOrEqual(toolbar.x + toolbar.width);
}

async function openGlobalActions(page: Page) {
  await globalTrigger(page).click();
  await expect(globalDialog(page)).toBeVisible();
}

async function closeGlobalActions(page: Page) {
  const dialog = globalDialog(page);
  if (await dialog.isVisible().catch(() => false)) {
    await dialog
      .getByRole("button", { name: /Close the window|Close window|Fermer/ })
      .click();
    await expect(dialog).toHaveCount(0);
  }
}

async function openWish(page: Page, title: string) {
  await page.goto(process.env.DJINN_URL!);
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await expect(page.locator(".workspace-thread-head h1")).toHaveText(title);
}

function connectEnvelope(flags: number, payload: Uint8Array): Buffer {
  const header = Buffer.alloc(5);
  header[0] = flags;
  header.writeUInt32BE(payload.byteLength, 1);
  return Buffer.concat([header, Buffer.from(payload)]);
}

function watchShowBody(wishId: string, target: string): Buffer {
  const response = create(UiServiceWatchShowResponseSchema, {
    wishId,
    target,
  });
  const message = toBinary(UiServiceWatchShowResponseSchema, response);
  return Buffer.concat([
    connectEnvelope(0, message),
    connectEnvelope(2, Buffer.from("{}")),
  ]);
}

async function focusOnNextWatchShow(
  page: Page,
  wishId: string,
  target: string,
): Promise<() => Promise<void>> {
  let inject = true;
  const handler = async (route: Route) => {
    if (!inject) {
      await route.continue();
      return;
    }
    inject = false;
    await route.fulfill({
      status: 200,
      contentType: "application/connect+proto",
      body: watchShowBody(wishId, target),
    });
  };
  await page.route("**/ui.v1.UiService/WatchShow", handler);
  return async () => page.unroute("**/ui.v1.UiService/WatchShow", handler);
}

async function openSection(page: Page, id: string) {
  const maximized = await stage(page).getAttribute("data-wish-maximized");
  if (maximized && maximized !== id)
    await panel(page, maximized)
      .getByTitle(/^(Restore|Restaurer)$/)
      .click();
  const current = panel(page, id);
  if (!(await current.isVisible().catch(() => false))) {
    await sectionButton(page, id).click();
  }
  await expect(current).toHaveCount(1);
  return current;
}

async function closeSection(page: Page, id: string) {
  const current = panel(page, id);
  await current.getByRole("button", { name: /^(Close|Fermer) / }).click();
  await expect(current).toBeHidden();
}

async function assertNoDuplicateIds(page: Page) {
  const duplicates = await page.evaluate(() => {
    const seen = new Map<string, number>();
    for (const element of document.querySelectorAll("[id]"))
      seen.set(element.id, (seen.get(element.id) ?? 0) + 1);
    return [...seen].filter(([, count]) => count > 1);
  });
  expect(duplicates).toEqual([]);
}

async function assertUsablePanelBody(
  panelLocator: Locator,
  bodySelector: string,
  titleSelector?: string,
) {
  const content = panelLocator.locator(".workspace-panel-content");
  const body = content.locator(bodySelector).first();
  await expect(body).toHaveCount(1);
  const contentBox = await content.boundingBox();
  const bodyBox = await body.boundingBox();
  expect(contentBox).toBeTruthy();
  expect(bodyBox).toBeTruthy();
  expect(contentBox!.height).toBeGreaterThan(40);
  expect(bodyBox!.height).toBeGreaterThan(40);
  const overlap =
    Math.min(bodyBox!.y + bodyBox!.height, contentBox!.y + contentBox!.height) -
    Math.max(bodyBox!.y, contentBox!.y);
  expect(overlap).toBeGreaterThan(0);
  if (titleSelector) {
    const title = body.locator(titleSelector).first();
    await expect(title).toHaveCount(1);
    const titleBox = await title.boundingBox();
    expect(titleBox).toBeTruthy();
    expect(titleBox!.width).toBeGreaterThan(40);
    expect(titleBox!.height).toBeGreaterThan(10);
    const titleOverlap =
      Math.min(
        titleBox!.y + titleBox!.height,
        contentBox!.y + contentBox!.height,
      ) - Math.max(titleBox!.y, contentBox!.y);
    expect(titleOverlap).toBeGreaterThan(0);
  }
}

async function sharedSplitDirection(
  page: Page,
  first: string,
  second: string,
): Promise<string | null> {
  const firstBox = await panel(page, first).boundingBox();
  const secondBox = await panel(page, second).boundingBox();
  if (!firstBox || !secondBox) return null;
  if (Math.abs(firstBox.x - secondBox.x) > 2) return "horizontal";
  if (Math.abs(firstBox.y - secondBox.y) > 2) return "vertical";
  return null;
}

async function reloadWish(page: Page, title: string) {
  await page.reload();
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await expect(page.locator(".workspace-thread-head h1")).toHaveText(title);
}

async function clearWorkspace(page: Page, wishId: string, title: string) {
  await page.evaluate(
    (id) => localStorage.removeItem(`djinn.wish.workspace.${id}`),
    wishId,
  );
  await reloadWish(page, title);
  await expect(panel(page, "summary")).toHaveCount(1);
}

test("WatchShow opens and focuses hidden workspace targets", async ({
  page,
}) => {
  const sample = fixture();
  try {
    await openWish(page, sample.title);
    const stopQuestionRoute = await focusOnNextWatchShow(
      page,
      sample.wishId,
      `question-${sample.qOpen}`,
    );
    await page.reload();
    await expect(page.locator(".workspace-thread-head h1")).toHaveText(
      sample.title,
    );
    await expect(globalDialog(page)).toBeVisible();
    await expect(
      globalDialog(page).locator(`#question-${sample.qOpen}`),
    ).toBeFocused();
    await closeGlobalActions(page);
    await stopQuestionRoute();

    const stopTaskRoute = await focusOnNextWatchShow(
      page,
      sample.wishId,
      `task-${sample.taskId}`,
    );
    await page.reload();
    await expect(panel(page, "tasks")).toBeVisible();
    await expect(
      panel(page, "tasks").locator(`#task-${sample.taskId}`),
    ).toBeFocused();
    await stopTaskRoute();
    await assertNoDuplicateIds(page);
  } finally {
    cleanup(sample);
  }
});

test("the summary opens every section and global actions survive composition and maximization", async ({
  page,
}) => {
  const sample = fixture();
  try {
    await openWish(page, sample.title);
    await expect(panel(page, "summary")).toHaveCount(1);
    const sidebarToggle = page.locator(".app-toolbar-toggle");
    await sidebarToggle.click();
    await expect(page.locator(".wish-app")).toHaveClass(/sidebar-collapsed/);
    await assertGlobalTriggerClearsToolbar(page);
    await page.evaluate(() =>
      document.documentElement.classList.add("native-mac"),
    );
    await assertGlobalTriggerClearsToolbar(page);
    await page.evaluate(() =>
      document.documentElement.classList.remove("native-mac"),
    );
    await page.setViewportSize({ width: 700, height: 1000 });
    await assertGlobalTriggerClearsToolbar(page);
    await page.evaluate(() =>
      document.documentElement.classList.add("native-mac"),
    );
    await assertGlobalTriggerClearsToolbar(page);
    await page.evaluate(() =>
      document.documentElement.classList.remove("native-mac"),
    );
    await page.setViewportSize({ width: 1440, height: 1000 });
    await sidebarToggle.click();
    await expect(page.locator(".wish-app")).not.toHaveClass(
      /sidebar-collapsed/,
    );
    for (const id of [
      "tasks",
      "decisions",
      "tilasms",
      "agents",
      "journal",
      "instructions",
      "rights",
    ]) {
      await expect(section(page, id)).toHaveCount(1);
      await expect(panel(page, id)).toBeHidden();
    }
    await expect(globalTrigger(page)).toContainText("Your move");
    await expect(globalTrigger(page)).toContainText("2");
    await expect(page.locator(".workspace-global-overlay")).toBeHidden();
    await openGlobalActions(page);
    await expect(globalDialog(page).locator(".question-card")).toContainText(
      "May the worker edit the lamp?",
    );
    await expect(globalDialog(page).locator(".plan-waiting")).toContainText(
      "W1",
    );
    await closeGlobalActions(page);
    await page.screenshot({
      path: path.join(shots, "workspace-summary-default.png"),
    });

    const rights = await openSection(page, "rights");
    const allowance = rights.getByRole("combobox").first();
    await expect(allowance).toBeVisible();
    await allowance.selectOption("1");
    await expect(allowance).toHaveValue("1");
    await closeSection(page, "rights");

    await openSection(page, "tasks");
    await expect(panel(page, "tasks")).toContainText(
      "Wait for the lamp answer",
    );
    await openGlobalActions(page);
    await expect(globalDialog(page).locator(".question-card")).toBeVisible();
    await closeGlobalActions(page);
    await stage(page).scrollIntoViewIfNeeded();
    await page.screenshot({
      path: path.join(shots, "workspace-composed-tasks.png"),
      fullPage: true,
    });

    await openSection(page, "tilasms");
    await expect(panel(page, "tilasms")).toContainText("Workspace model");
    await panel(page, "tilasms")
      .getByRole("button", { name: "Restore Tilasms" })
      .click();
    await assertUsablePanelBody(
      panel(page, "tasks"),
      ".wish-task",
      ".wish-task-heading strong",
    );
    await assertUsablePanelBody(panel(page, "tilasms"), ".tilasm-row");
    await stage(page).scrollIntoViewIfNeeded();
    await page.screenshot({
      path: path.join(shots, "workspace-composed-supports.png"),
      fullPage: true,
    });

    // Exercise each lazy section through its summary card while closing the prior panel so the check does not build
    // an accidental eight-panel layout. These are content assertions, not just selector existence checks.
    await closeSection(page, "tilasms");
    await closeSection(page, "tasks");
    for (const [id, text] of [
      ["decisions", "Which oil should the lamp use?"],
      ["agents", "Worker report"],
      ["journal", "The first journal entry"],
      ["instructions", "Keep the workspace arrangement."],
    ] as const) {
      const opened = await openSection(page, id);
      await expect(opened).toContainText(text);
      await closeSection(page, id);
    }

    const taskPanel = await openSection(page, "tasks");
    const maximizeTasks = taskPanel.getByRole("button", {
      name: "Maximize Tasks",
    });
    if (await maximizeTasks.count()) await maximizeTasks.click();
    await expect(stage(page)).toHaveAttribute("data-wish-maximized", "tasks");
    await expect(taskPanel).toContainText("Wait for the lamp answer");
    await openGlobalActions(page);
    await expect(globalDialog(page).locator(".question-card")).toBeVisible();
    await expect(globalDialog(page).locator(".plan-waiting")).toBeVisible();
    await assertNoDuplicateIds(page);
    await page.screenshot({
      path: path.join(shots, "workspace-maximized-global.png"),
    });

    const globalQuestion = globalDialog(page)
      .locator(".question-card")
      .filter({ hasText: "May the worker edit the lamp?" });
    await globalQuestion.locator("textarea").fill("keep this answer draft");
    await closeGlobalActions(page);
    await openGlobalActions(page);
    await expect(
      globalDialog(page)
        .locator(".question-card")
        .filter({ hasText: "May the worker edit the lamp?" })
        .locator("textarea"),
    ).toHaveValue("keep this answer draft");
    const reopenedQuestion = globalDialog(page)
      .locator(".question-card")
      .filter({ hasText: "May the worker edit the lamp?" });
    await reopenedQuestion
      .getByRole("button", { name: /edit the lamp/ })
      .click();
    await reopenedQuestion
      .getByRole("button", { name: "Rub the lamp" })
      .click();
    await expect(reopenedQuestion).toHaveCount(0);
    await assertNoDuplicateIds(page);
    await closeGlobalActions(page);

    await taskPanel.getByRole("button", { name: "Restore Tasks" }).click();
    await expect(stage(page)).not.toHaveAttribute(
      "data-wish-maximized",
      "tasks",
    );

    await taskPanel.getByRole("button", { name: "Close Tasks" }).click();
    await expect(panel(page, "tasks")).toBeHidden();
    await expect(section(page, "tasks")).toHaveCount(1);
    await assertNoDuplicateIds(page);
  } finally {
    cleanup(sample);
  }
});

test("content panels compose by pointer geometry and keyboard controls", async ({
  page,
}) => {
  const sample = fixture();
  try {
    await openWish(page, sample.title);
    await clearWorkspace(page, sample.wishId, sample.title);
    const taskPanel = await openSection(page, "tasks");
    await taskPanel.getByRole("button", { name: "Mark done" }).click();
    const taskDraft = taskPanel.getByRole("textbox", {
      name: "Why, in a few words (optional)",
    });
    await taskDraft.fill("preserve this task draft");
    await taskPanel.getByRole("button", { name: "Restore Tasks" }).click();
    await taskPanel.getByRole("button", { name: "Close Tasks" }).click();
    await expect(taskPanel).toBeHidden();
    const reopenedTask = await openSection(page, "tasks");
    await expect(
      reopenedTask.getByRole("textbox", {
        name: "Why, in a few words (optional)",
      }),
    ).toHaveValue("preserve this task draft");
    await reopenedTask.getByRole("button", { name: "Cancel" }).click();
    await reopenedTask.getByRole("button", { name: "Restore Tasks" }).click();
    const tilasmPanel = await openSection(page, "tilasms");
    await tilasmPanel.getByRole("button", { name: "Restore Tilasms" }).click();

    // Only mounted content panels participate in drag composition. The drop point on the target's right edge makes
    // the geometry assertion deterministic without adding placement controls to the navigation card.
    const source = panel(page, "tilasms").locator(".workspace-panel-heading");
    const target = panel(page, "tasks");
    const targetBox = await target.boundingBox();
    expect(targetBox).toBeTruthy();
    await source.dragTo(target, {
      force: true,
      targetPosition: {
        x: Math.max(2, targetBox!.width - 4),
        y: Math.round(targetBox!.height / 2),
      },
    });
    await expect(panel(page, "tasks")).toHaveCount(1);
    await expect(panel(page, "tilasms")).toHaveCount(1);
    const tasksBox = await panel(page, "tasks").boundingBox();
    const tilasmsBox = await panel(page, "tilasms").boundingBox();
    expect(tasksBox).toBeTruthy();
    expect(tilasmsBox).toBeTruthy();
    expect(tilasmsBox!.x).toBeGreaterThan(tasksBox!.x);
    expect(tilasmsBox!.x).toBeGreaterThanOrEqual(
      tasksBox!.x + tasksBox!.width - 2,
    );
    expect(await sharedSplitDirection(page, "tasks", "tilasms")).toBe(
      "horizontal",
    );

    // The composed tree survives a reload with the same geometric order.
    await reloadWish(page, sample.title);
    await expect(panel(page, "tasks")).toHaveCount(1);
    await expect(panel(page, "tilasms")).toHaveCount(1);
    const reloadedTasksBox = await panel(page, "tasks").boundingBox();
    const reloadedTilasmsBox = await panel(page, "tilasms").boundingBox();
    expect(reloadedTasksBox).toBeTruthy();
    expect(reloadedTilasmsBox).toBeTruthy();
    expect(reloadedTilasmsBox!.x).toBeGreaterThan(reloadedTasksBox!.x);
    expect(await sharedSplitDirection(page, "tasks", "tilasms")).toBe(
      "horizontal",
    );

    // Legacy trees can nest vertical and horizontal branches. The flat stage planner keeps every resulting row
    // readable and globally caps it at two columns instead of letting nested branches create four narrow panels.
    const decisionsPanel = await openSection(page, "decisions");
    await decisionsPanel
      .getByRole("button", { name: "Restore Decisions" })
      .click();
    const tasksTarget = panel(page, "tasks");
    const tasksTargetBox = await tasksTarget.boundingBox();
    expect(tasksTargetBox).toBeTruthy();
    await decisionsPanel
      .locator(".workspace-panel-heading")
      .dragTo(tasksTarget, {
        force: true,
        targetPosition: {
          x: Math.round(tasksTargetBox!.width / 2),
          y: Math.max(2, tasksTargetBox!.height - 4),
        },
      });
    const nestedTasks = await panel(page, "tasks").boundingBox();
    const nestedDecisions = await panel(page, "decisions").boundingBox();
    const nestedTilasms = await panel(page, "tilasms").boundingBox();
    expect(nestedTasks).toBeTruthy();
    expect(nestedDecisions).toBeTruthy();
    expect(nestedTilasms).toBeTruthy();
    expect(nestedTasks!.height).toBeGreaterThan(480);
    expect(nestedDecisions!.height).toBeGreaterThan(480);
    expect(nestedDecisions!.y).toBeGreaterThan(nestedTasks!.y + 480);
    expect(Math.abs(nestedTilasms!.y - nestedTasks!.y)).toBeLessThan(4);

    const instructionsPanel = await openSection(page, "instructions");
    await instructionsPanel
      .getByRole("button", { name: "Restore Indications" })
      .click();
    const decisionTarget = panel(page, "decisions");
    const decisionTargetBox = await decisionTarget.boundingBox();
    expect(decisionTargetBox).toBeTruthy();
    await instructionsPanel
      .locator(".workspace-panel-heading")
      .dragTo(decisionTarget, {
        force: true,
        targetPosition: {
          x: Math.max(2, decisionTargetBox!.width - 4),
          y: Math.round(decisionTargetBox!.height / 2),
        },
      });
    const nestedBoxes = await Promise.all(
      ["tasks", "tilasms", "decisions", "instructions"].map(async (id) =>
        panel(page, id).boundingBox(),
      ),
    );
    nestedBoxes.forEach((box) => expect(box).toBeTruthy());
    const nestedTaskBox = nestedBoxes[0]!;
    const nestedDecisionBox = nestedBoxes[2]!;
    const nestedInstructionBox = nestedBoxes[3]!;
    expect(nestedTaskBox.height).toBeGreaterThan(480);
    expect(nestedDecisionBox.height).toBeGreaterThan(480);
    expect(nestedInstructionBox.y).toBeCloseTo(nestedTaskBox.y, 0);
    expect(nestedInstructionBox.x).toBeGreaterThanOrEqual(
      nestedTaskBox.x + nestedTaskBox.width - 2,
    );
    for (let first = 0; first < nestedBoxes.length; first += 1) {
      for (let second = first + 1; second < nestedBoxes.length; second += 1) {
        const left = nestedBoxes[first]!;
        const right = nestedBoxes[second]!;
        const horizontalOverlap =
          Math.min(left.x + left.width, right.x + right.width) -
          Math.max(left.x, right.x);
        const verticalOverlap =
          Math.min(left.y + left.height, right.y + right.height) -
          Math.max(left.y, right.y);
        expect(horizontalOverlap <= 2 || verticalOverlap <= 2).toBe(true);
      }
    }
    await closeSection(page, "instructions");
    await closeSection(page, "decisions");

    // A focused panel frame supports keyboard reorder without entering any content control, then Delete closes it.
    const keyboardPanel = panel(page, "tilasms");
    await keyboardPanel.focus();
    await expect(keyboardPanel).toBeFocused();
    await keyboardPanel.press("Alt+ArrowLeft");
    const keyboardTasksBox = await panel(page, "tasks").boundingBox();
    const keyboardTilasmsBox = await panel(page, "tilasms").boundingBox();
    expect(keyboardTasksBox).toBeTruthy();
    expect(keyboardTilasmsBox).toBeTruthy();
    expect(keyboardTilasmsBox!.x).toBeLessThan(keyboardTasksBox!.x);
    await keyboardPanel.focus();
    await keyboardPanel.press("Delete");
    await expect(panel(page, "tilasms")).toBeHidden();
    await expect(section(page, "tilasms")).toHaveCount(1);
    await assertNoDuplicateIds(page);
    await expect(
      section(page, "tasks").locator('[draggable="true"]'),
    ).toHaveCount(0);
    await expect(
      section(page, "tilasms").locator('[draggable="true"]'),
    ).toHaveCount(0);
    await expect(stage(page).locator(".workspace-compose-menu")).toHaveCount(0);
    await assertNoDuplicateIds(page);
  } finally {
    cleanup(sample);
  }
});

test("workspace storage is malformed-safe, denied-safe, isolated, narrow and persistent", async ({
  page,
}) => {
  const sample = fixture();
  try {
    await openWish(page, sample.title);
    await openSection(page, "tasks");
    await panel(page, "tasks")
      .getByRole("button", { name: "Restore Tasks" })
      .click();
    await openSection(page, "tilasms");
    await panel(page, "tilasms")
      .getByRole("button", { name: "Restore Tilasms" })
      .click();
    await page.evaluate((id) => {
      localStorage.setItem(`djinn.wish.workspace.${id}`, "{malformed");
    }, sample.wishId);
    await reloadWish(page, sample.title);
    await expect(panel(page, "summary")).toHaveCount(1);
    await assertNoDuplicateIds(page);

    await openSection(page, "tasks");
    await panel(page, "tasks")
      .getByRole("button", { name: "Restore Tasks" })
      .click();
    await openSection(page, "decisions");
    await panel(page, "decisions")
      .getByRole("button", { name: "Restore Decisions" })
      .click();
    await expect(panel(page, "decisions")).toContainText(
      "Which oil should the lamp use?",
    );
    await reloadWish(page, sample.title);
    await expect(panel(page, "tasks")).toHaveCount(1);
    await expect(panel(page, "decisions")).toHaveCount(1);

    await page
      .locator(".wish-nav")
      .filter({ hasText: sample.otherTitle })
      .click();
    await expect(page.locator(".workspace-thread-head h1")).toHaveText(
      sample.otherTitle,
    );
    await expect(panel(page, "tasks")).toBeHidden();
    await page.locator(".wish-nav").filter({ hasText: sample.title }).click();
    await expect(page.locator(".workspace-thread-head h1")).toHaveText(
      sample.title,
    );
    await expect(panel(page, "tasks")).toHaveCount(1);
    await expect(panel(page, "decisions")).toHaveCount(1);

    await page.setViewportSize({ width: 700, height: 1000 });
    await clearWorkspace(page, sample.wishId, sample.title);
    await openSection(page, "tasks");
    await panel(page, "tasks")
      .getByRole("button", { name: "Restore Tasks" })
      .click();
    await openSection(page, "tilasms");
    await panel(page, "tilasms")
      .getByRole("button", { name: "Restore Tilasms" })
      .click();
    const narrowTasks = await panel(page, "tasks").boundingBox();
    const narrowTilasms = await panel(page, "tilasms").boundingBox();
    expect(narrowTasks).toBeTruthy();
    expect(narrowTilasms).toBeTruthy();
    expect(narrowTilasms!.y).toBeGreaterThan(narrowTasks!.y);
    expect(await sharedSplitDirection(page, "tasks", "tilasms")).toBe(
      "vertical",
    );
    await stage(page).scrollIntoViewIfNeeded();
    await page.screenshot({
      path: path.join(shots, "workspace-narrow-stack.png"),
      fullPage: true,
    });

    await page.addInitScript(() => {
      const denied = () => {
        throw new DOMException("Storage denied", "SecurityError");
      };
      Object.defineProperty(Storage.prototype, "getItem", {
        configurable: true,
        value: denied,
      });
      Object.defineProperty(Storage.prototype, "setItem", {
        configurable: true,
        value: denied,
      });
    });
    await page.reload();
    await page.locator(".wish-nav").filter({ hasText: sample.title }).click();
    await expect(panel(page, "summary")).toHaveCount(1);
    await expect(page.locator(".workspace-thread-head h1")).toHaveText(
      sample.title,
    );
    await assertNoDuplicateIds(page);
  } finally {
    cleanup(sample);
  }
});

test("task decisions and the same Tilasm deep link work repeatedly after close and restore", async ({
  page,
}) => {
  const sample = fixture();
  try {
    await openWish(page, sample.title);
    const decisions = await openSection(page, "decisions");
    await expect(decisions.locator(".decision-row")).toHaveCount(2);
    const decisionTask = decisions
      .locator(".decision-row")
      .filter({ hasText: "Which oil should the lamp use?" });
    await decisionTask.getByRole("link", { name: "W1" }).click();
    await expect(panel(page, "tasks")).toHaveCount(1);
    await expect(panel(page, "tasks").locator(".wish-task")).toContainText(
      "Wait for the lamp answer",
    );
    await expect(
      panel(page, "tasks").locator(".wish-task-heading").first(),
    ).toBeFocused();
    await panel(page, "tasks")
      .locator(".wish-task")
      .getByRole("link", { name: /From Q02/ })
      .click();
    await expect(
      panel(page, "decisions").locator(".decision-row.focused"),
    ).toContainText("Which oil should the lamp use?");
    // Repeat both directions after the state already has both panels open.
    await panel(page, "decisions")
      .locator(".decision-row")
      .filter({ hasText: "Which oil should the lamp use?" })
      .getByRole("link", { name: "W1" })
      .click();
    await panel(page, "tasks")
      .locator(".wish-task")
      .getByRole("link", { name: /From Q02/ })
      .click();
    const maximized = await stage(page).getAttribute("data-wish-maximized");
    if (maximized)
      await panel(page, maximized)
        .getByTitle(/^(Restore|Restaurer)$/)
        .click();
    await expect(panel(page, "tasks")).toHaveCount(1);
    await expect(panel(page, "decisions")).toHaveCount(1);

    const journal = await openSection(page, "journal");
    const tilasmLink = journal.getByRole("link", {
      name: "the workspace model",
    });
    await tilasmLink.click();
    await expect(panel(page, "tilasms")).toHaveCount(1);
    await expect(
      page.frameLocator("iframe.tilasm-frame").locator("#workspace-model"),
    ).toHaveText("Workspace model");
    await panel(page, "tilasms")
      .getByRole("button", { name: "Close Tilasms" })
      .click();
    await expect(panel(page, "tilasms")).toBeHidden();
    await panel(page, "tasks")
      .getByRole("button", { name: "Maximize Tasks" })
      .click();
    await expect(stage(page)).toHaveAttribute("data-wish-maximized", "tasks");
    await panel(page, "tasks")
      .getByRole("button", { name: "Restore Tasks" })
      .click();
    await journal.getByRole("link", { name: "the workspace model" }).click();
    await expect(panel(page, "tilasms")).toHaveCount(1);
    await expect(
      page.frameLocator("iframe.tilasm-frame").locator("#workspace-model"),
    ).toHaveText("Workspace model");
    await assertNoDuplicateIds(page);
  } finally {
    cleanup(sample);
  }
});
