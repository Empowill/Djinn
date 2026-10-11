// A wish opens as a sparse summary and grows into a persistent, composable workspace. These tests use the browser
// transport's real data and geometry: every section starts closed, card drops can target another closed card, and
// global actions stay usable while the workspace is split or maximized.
import { expect, test, type Locator, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";

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
const sectionLabels: Record<string, string> = {
  tasks: "Tasks",
  decisions: "Decisions",
  tilasms: "Tilasms",
  agents: "For agents",
  journal: "Journal",
  instructions: "Indications",
  rights: "Rights",
};
const sectionButton = (page: Page, id: string) =>
  section(page, id).getByRole("button", {
    name: `Open ${sectionLabels[id] ?? id}`,
  });

async function openWish(page: Page, title: string) {
  await page.goto(process.env.DJINN_URL!);
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await expect(page.locator(".workspace-thread-head h1")).toHaveText(title);
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
  await current.getByRole("button", { name: /^Close / }).click();
  await expect(current).toHaveCount(0);
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
  return page.evaluate(
    ({ first, second }) => {
      const firstPanel = document.querySelector(`[data-wish-panel="${first}"]`);
      const secondPanel = document.querySelector(
        `[data-wish-panel="${second}"]`,
      );
      if (!firstPanel || !secondPanel) return null;
      let ancestor: Element | null = firstPanel.parentElement;
      while (ancestor && !ancestor.contains(secondPanel))
        ancestor = ancestor.parentElement;
      return ancestor?.matches(".workspace-split")
        ? ancestor.getAttribute("data-wish-split")
        : null;
    },
    { first, second },
  );
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

test("the summary opens every section and global actions survive composition and maximization", async ({
  page,
}) => {
  const sample = fixture();
  try {
    await openWish(page, sample.title);
    await expect(panel(page, "summary")).toHaveCount(1);
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
      await expect(panel(page, id)).toHaveCount(0);
    }
    await expect(
      page.locator(".workspace-global .question-card"),
    ).toContainText("May the worker edit the lamp?");
    await expect(page.locator(".workspace-global .plan-waiting")).toContainText(
      "W1",
    );
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
    await expect(
      page.locator(".workspace-global .question-card"),
    ).toBeVisible();
    await stage(page).scrollIntoViewIfNeeded();
    await page.screenshot({
      path: path.join(shots, "workspace-composed-tasks.png"),
      fullPage: true,
    });

    await openSection(page, "tilasms");
    await expect(panel(page, "tilasms")).toContainText("Workspace model");
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
    await taskPanel.getByRole("button", { name: "Maximize Tasks" }).click();
    await expect(stage(page)).toHaveAttribute("data-wish-maximized", "tasks");
    await expect(taskPanel).toContainText("Wait for the lamp answer");
    await expect(
      page.locator(".workspace-global .question-card"),
    ).toBeVisible();
    await expect(page.locator(".workspace-global .plan-waiting")).toBeVisible();
    await assertNoDuplicateIds(page);
    await page.screenshot({
      path: path.join(shots, "workspace-maximized-global.png"),
    });

    const globalQuestion = page
      .locator(".workspace-global .question-card")
      .filter({ hasText: "May the worker edit the lamp?" });
    await globalQuestion.getByRole("button", { name: /edit the lamp/ }).click();
    await globalQuestion.getByRole("button", { name: "Rub the lamp" }).click();
    await expect(globalQuestion).toHaveCount(0);
    await assertNoDuplicateIds(page);

    await taskPanel.getByRole("button", { name: "Restore Tasks" }).click();
    await expect(stage(page)).not.toHaveAttribute(
      "data-wish-maximized",
      "tasks",
    );

    await taskPanel.getByRole("button", { name: "Close Tasks" }).click();
    await expect(panel(page, "tasks")).toHaveCount(0);
    await expect(section(page, "tasks")).toHaveCount(1);
    await assertNoDuplicateIds(page);
  } finally {
    cleanup(sample);
  }
});

test("closed cards compose with actual pointer geometry and keyboard side controls", async ({
  page,
}) => {
  const sample = fixture();
  try {
    await openWish(page, sample.title);
    await clearWorkspace(page, sample.wishId, sample.title);
    const source = section(page, "tilasms").locator(".workspace-section-card");
    const target = section(page, "tasks");
    const sourceBox = await source.boundingBox();
    const targetBox = await target.boundingBox();
    expect(sourceBox).toBeTruthy();
    expect(targetBox).toBeTruthy();
    // Playwright's dragTo uses real mouse coordinates and dispatches HTML5 drag events. The target remains a summary
    // card throughout the gesture; the drop zone only becomes visible once the source is dragging.
    const right = target.locator('[data-wish-drop="right"]');
    await source.dragTo(right, { force: true });
    await expect(panel(page, "tasks")).toHaveCount(1);
    await expect(panel(page, "tilasms")).toHaveCount(1);
    const tasksBox = await panel(page, "tasks").boundingBox();
    const tilasmsBox = await panel(page, "tilasms").boundingBox();
    expect(tasksBox).toBeTruthy();
    expect(tilasmsBox).toBeTruthy();
    expect(tilasmsBox!.x).toBeGreaterThan(tasksBox!.x);
    expect(await sharedSplitDirection(page, "tasks", "tilasms")).toBe(
      "horizontal",
    );

    // Move an already-mounted panel by its draggable heading to the opposite side of its sibling.
    const mountedHeading = panel(page, "tilasms").locator(
      ".workspace-panel-heading",
    );
    const mountedTarget = panel(page, "tasks").locator(
      '[data-wish-drop="left"]',
    );
    await mountedHeading.dragTo(mountedTarget, { force: true });
    const movedTasksBox = await panel(page, "tasks").boundingBox();
    const movedTilasmsBox = await panel(page, "tilasms").boundingBox();
    expect(movedTasksBox).toBeTruthy();
    expect(movedTilasmsBox).toBeTruthy();
    expect(movedTilasmsBox!.x).toBeLessThan(movedTasksBox!.x);
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
    expect(reloadedTilasmsBox!.x).toBeLessThan(reloadedTasksBox!.x);
    expect(await sharedSplitDirection(page, "tasks", "tilasms")).toBe(
      "horizontal",
    );

    // A focused panel frame supports keyboard reorder without entering any content control, then Delete closes it.
    const keyboardPanel = panel(page, "tilasms");
    await keyboardPanel.focus();
    await expect(keyboardPanel).toBeFocused();
    await keyboardPanel.press("Alt+ArrowRight");
    const keyboardTasksBox = await panel(page, "tasks").boundingBox();
    const keyboardTilasmsBox = await panel(page, "tilasms").boundingBox();
    expect(keyboardTasksBox).toBeTruthy();
    expect(keyboardTilasmsBox).toBeTruthy();
    expect(keyboardTilasmsBox!.x).toBeGreaterThan(keyboardTasksBox!.x);
    await keyboardPanel.focus();
    await keyboardPanel.press("Delete");
    await expect(panel(page, "tilasms")).toHaveCount(0);
    await expect(section(page, "tilasms")).toHaveCount(1);
    await assertNoDuplicateIds(page);

    // The four side controls are keyboard-addressable menu actions. Rebuild a fresh closed-card tree before each
    // placement so each assertion proves a target card can be composed without first opening its content.
    for (const [placement, expectedDirection] of [
      ["left", "horizontal"],
      ["right", "horizontal"],
      ["top", "vertical"],
      ["bottom", "vertical"],
    ] as const) {
      await clearWorkspace(page, sample.wishId, sample.title);
      const composeMenu = section(page, "tilasms").locator(
        ".workspace-compose-menu",
      );
      await composeMenu.locator("summary").press("Enter");
      await composeMenu.locator("select").nth(0).selectOption("tasks");
      await composeMenu.locator("select").nth(1).selectOption(placement);
      await composeMenu
        .getByRole("button", { name: /Compose panels|Place panel/ })
        .press("Enter");
      await expect(panel(page, "tasks")).toHaveCount(1);
      await expect(panel(page, "tilasms")).toHaveCount(1);
      expect(await sharedSplitDirection(page, "tasks", "tilasms")).toBe(
        expectedDirection,
      );
    }
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
    await openSection(page, "tilasms");
    await page.evaluate((id) => {
      localStorage.setItem(`djinn.wish.workspace.${id}`, "{malformed");
    }, sample.wishId);
    await reloadWish(page, sample.title);
    await expect(panel(page, "summary")).toHaveCount(1);
    await assertNoDuplicateIds(page);

    await openSection(page, "tasks");
    await openSection(page, "decisions");
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
    await expect(panel(page, "tasks")).toHaveCount(0);
    await page.locator(".wish-nav").filter({ hasText: sample.title }).click();
    await expect(page.locator(".workspace-thread-head h1")).toHaveText(
      sample.title,
    );
    await expect(panel(page, "tasks")).toHaveCount(1);
    await expect(panel(page, "decisions")).toHaveCount(1);

    await page.setViewportSize({ width: 700, height: 1000 });
    await clearWorkspace(page, sample.wishId, sample.title);
    await openSection(page, "tasks");
    await openSection(page, "tilasms");
    const split = stage(page).locator(".workspace-split").first();
    await expect(split).toHaveCSS("flex-direction", "column");
    const narrowTasks = await panel(page, "tasks").boundingBox();
    const narrowTilasms = await panel(page, "tilasms").boundingBox();
    expect(narrowTasks).toBeTruthy();
    expect(narrowTilasms).toBeTruthy();
    expect(narrowTilasms!.y).toBeGreaterThan(narrowTasks!.y);
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
    await expect(panel(page, "tilasms")).toHaveCount(0);
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
