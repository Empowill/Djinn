// A wish opens as a sparse summary and reveals one persistent detail panel at a time. These tests use the browser
// transport's real data: panel selection, draft preservation, activity tabs, drawers, and global actions remain usable
// while the workspace switches views.
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
import {
  workspaceGlobalDialog,
  workspaceLocalePattern,
  workspaceLocalePrefixPattern,
} from "./workspace-selectors";

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

type ReadyFixture = {
  wishId: string;
  title: string;
  file: string;
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

function readyFixture(): ReadyFixture {
  pauseActiveWishes();
  const wishId = randomUUID();
  const title = `Grant the lamp ${wishId.slice(0, 8)}`;
  const file = path.join(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "workspace-ready-")),
    "ready.json",
  );
  const at = "2026-10-09T08:00:00Z";
  fs.writeFileSync(
    file,
    JSON.stringify({
      version: 1,
      create_time: at,
      wish: { id: wishId, title, create_time: at },
      tasks: [
        {
          id: randomUUID(),
          wish_id: wishId,
          code: "W1",
          title: "Finish the lamp",
          status: "TASK_STATUS_DONE",
          create_time: at,
        },
      ],
      questions: [
        {
          id: randomUUID(),
          wish_id: wishId,
          code: "Q01",
          text: "Keep the lamp?",
          answer: { choice: "CHOICE_YES", create_time: at },
          create_time: at,
        },
      ],
    }),
  );
  djinn("wish", "import", file);
  return { wishId, title, file };
}

function cleanupReady(sample: ReadyFixture) {
  try {
    djinn("wish", "pause", sample.wishId);
  } catch {
    // The test may already have granted or removed the wish.
  }
  fs.rmSync(path.dirname(sample.file), { recursive: true, force: true });
}

function installPromptingCodex(): string {
  const file = path.join(process.env.DJINN_E2E_HOME!, "bin", "codex");
  fs.writeFileSync(
    file,
    String.raw`#!/bin/sh
stty raw -echo
printf '\033[2J\033[HWould you like to run the following command?\r\n\r\n$ djinn wish brief\r\n\r\n› 1. Yes, proceed (y)\r\n  2. No, cancel\r\n\r\nPress enter to confirm or esc to cancel\r\n'
dd bs=1 count=1 >/dev/null 2>&1
stty -raw echo
exec cat
`,
    { mode: 0o755 },
  );
  return file;
}

function startPromptingLead(sample: WorkspaceFixture) {
  djinn(
    "wish",
    "set-lead",
    sample.wishId,
    randomUUID(),
    "--provider",
    "codex",
    "--directory",
    sample.directory,
  );
  djinn("wish", "resume", sample.wishId);
}

const stage = (page: Page) =>
  page.locator('.wish-workspace[data-wish-workspace="stage"]');
const panel = (page: Page, id: string) =>
  stage(page).locator(`[data-wish-panel="${id}"]`);
const section = (page: Page, id: string) =>
  stage(page).locator(`[data-wish-section="${id}"]`);
const sectionButton = (page: Page, id: string) => section(page, id);

const globalTrigger = (page: Page) =>
  page.locator(".workspace-action-attention-pending");

const globalDialog = workspaceGlobalDialog;

async function assertGlobalTriggerClearsToolbar(page: Page) {
  const trigger = await globalTrigger(page).boundingBox();
  expect(trigger).not.toBeNull();
  await expect(globalDialog(page)).toHaveClass(/is-collapsed/);
}

async function openGlobalActions(page: Page) {
  const dock = globalDialog(page);
  if ((await dock.getAttribute("class"))?.includes("is-collapsed"))
    await globalTrigger(page).click();
  await expect(dock).not.toHaveClass(/is-collapsed/);
}

async function collapseGlobalActions(page: Page) {
  const dock = globalDialog(page);
  // The dock is mounted only after its first action arrives. Collapse that
  // mounted dock before asserting on the small pending trigger.
  await expect(dock).toHaveCount(1);
  if (!((await dock.getAttribute("class")) ?? "").includes("is-collapsed")) {
    await dock
      .getByRole("button", {
        name: workspaceLocalePattern("attention.close"),
      })
      .click();
  }
  await expect(dock).toHaveClass(/is-collapsed/);
  await expect(globalTrigger(page)).toBeVisible();
}

async function closeGlobalActions(page: Page) {
  const dialog = globalDialog(page);
  if ((await dialog.count()) === 0) return;
  if (!(await dialog.getAttribute("class"))?.includes("is-collapsed")) {
    await dialog
      .getByRole("button", {
        name: workspaceLocalePattern("attention.close"),
      })
      .click();
    await expect(dialog).toHaveClass(/is-collapsed/);
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

function watchShowEndBody(): Buffer {
  return connectEnvelope(2, Buffer.from("{}"));
}

async function focusOnNextWatchShow(
  page: Page,
  wishId: string,
  target: string,
): Promise<() => Promise<void>> {
  let inject = true;
  let navigated = false;
  const mainFrame = page.mainFrame();
  const onFrameNavigated = (frame: typeof mainFrame) => {
    if (frame === mainFrame) navigated = true;
  };
  page.on("framenavigated", onFrameNavigated);
  const handler = async (route: Route) => {
    // A reload can leave the old document's reconnect in flight. Let that
    // request finish; only the first WatchShow request of the new document is
    // the signal this test installed.
    if (!navigated) {
      try {
        await route.continue();
      } catch {
        // The old document may have been canceled during navigation.
      }
      return;
    }
    if (!inject) {
      await route.fulfill({
        status: 200,
        contentType: "application/connect+proto",
        body: watchShowEndBody(),
      });
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
  return async () => {
    page.off("framenavigated", onFrameNavigated);
    if (!page.isClosed())
      await page.unroute("**/ui.v1.UiService/WatchShow", handler);
  };
}

async function holdQuestionList(page: Page) {
  let resolveSeen!: () => void;
  const seen = new Promise<void>((resolve) => {
    resolveSeen = resolve;
  });
  let releaseGate!: () => void;
  const released = new Promise<void>((resolve) => {
    releaseGate = resolve;
  });
  let releasedOnce = false;
  const release = () => {
    if (releasedOnce) return;
    releasedOnce = true;
    releaseGate();
  };
  const handler = async (route: Route) => {
    const response = await route.fetch();
    resolveSeen();
    await released;
    await route.fulfill({ response });
  };
  await page.route("**/plan.v1.QuestionService/List", handler);
  return {
    seen,
    release,
    stop: async () => {
      release();
      if (!page.isClosed())
        await page.unroute("**/plan.v1.QuestionService/List", handler);
    },
  };
}

async function holdTaskList(page: Page) {
  let resolveSeen!: () => void;
  const seen = new Promise<void>((resolve) => {
    resolveSeen = resolve;
  });
  let releaseGate!: () => void;
  const released = new Promise<void>((resolve) => {
    releaseGate = resolve;
  });
  let releasedOnce = false;
  const release = () => {
    if (releasedOnce) return;
    releasedOnce = true;
    releaseGate();
  };
  let navigated = false;
  const mainFrame = page.mainFrame();
  const onFrameNavigated = (frame: typeof mainFrame) => {
    if (frame === mainFrame) navigated = true;
  };
  page.on("framenavigated", onFrameNavigated);
  const handler = async (route: Route) => {
    if (!navigated) {
      try {
        await route.continue();
      } catch {
        // A reconnect from the old document can be canceled by navigation.
      }
      return;
    }
    try {
      const response = await route.fetch();
      resolveSeen();
      await released;
      if (!page.isClosed()) await route.fulfill({ response });
    } catch {
      // A canceled request must not satisfy the gate for the new document.
    }
  };
  await page.route("**/plan.v1.TaskService/List", handler);
  return {
    seen,
    release,
    stop: async () => {
      release();
      page.off("framenavigated", onFrameNavigated);
      if (!page.isClosed())
        await page.unroute("**/plan.v1.TaskService/List", handler);
    },
  };
}

async function openSection(page: Page, id: string) {
  const current = panel(page, id);
  if (!(await current.isVisible().catch(() => false))) {
    await sectionButton(page, id).locator(".workspace-section-toggle").click();
  }
  await expect(current).toHaveCount(1);
  await expect(current).toBeVisible();
  return current;
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
  let stopQuestionRoute: (() => Promise<void>) | undefined;
  let stopTaskRoute: (() => Promise<void>) | undefined;
  let questionGate: Awaited<ReturnType<typeof holdQuestionList>> | undefined;
  let taskGate: Awaited<ReturnType<typeof holdTaskList>> | undefined;
  try {
    await openWish(page, sample.title);
    stopQuestionRoute = await focusOnNextWatchShow(
      page,
      sample.wishId,
      `question-${sample.qOpen}`,
    );
    const gate = await holdQuestionList(page);
    questionGate = gate;
    try {
      await page.reload();
      await expect(page.locator(".workspace-thread-head h1")).toHaveText(
        sample.title,
      );
      await gate.seen;
      const question = globalDialog(page).locator(`#question-${sample.qOpen}`);
      await expect(question).toHaveCount(0);
      gate.release();
      await expect(globalDialog(page)).toBeVisible();
      await expect(question).toBeVisible();
      await expect(question.getByRole("button").first()).toBeFocused();
      // Answering the focused question removes its card; the ordinary close action then closes the remaining drawer.
      await question.getByRole("button", { name: /edit the lamp/ }).click();
      await globalDialog(page)
        .getByRole("button", { name: /^Respond/ })
        .click();
      await expect(question).toHaveCount(0);
      await closeGlobalActions(page);

      // Install the next stream handler before removing this one: a reconnect in the gap must not replay another
      // wish while the task focus check is being prepared.
      stopTaskRoute = await focusOnNextWatchShow(
        page,
        sample.wishId,
        `waiting-${sample.taskId}`,
      );
      taskGate = await holdTaskList(page);
      await stopQuestionRoute();
      stopQuestionRoute = undefined;
    } finally {
      await gate.stop();
      questionGate = undefined;
    }

    await page.reload();
    await expect(panel(page, "tasks")).toBeVisible();
    const delayedTask = panel(page, "tasks").locator(`#task-${sample.taskId}`);
    await taskGate?.seen;
    await expect(delayedTask).toHaveCount(0);
    taskGate?.release();
    await expect(delayedTask).toBeVisible();
    const taskDrawer = page.locator("dialog.task-drawer-shell");
    await expect(taskDrawer).toBeVisible();
    await expect(
      taskDrawer.getByRole("button", { name: /Close|Fermer/ }),
    ).toBeFocused();
    await expect(delayedTask.locator(".wish-task-heading")).toHaveAttribute(
      "aria-expanded",
      "true",
    );
    await assertNoDuplicateIds(page);
    await stopTaskRoute();
    stopTaskRoute = undefined;
  } finally {
    await stopTaskRoute?.();
    await stopQuestionRoute?.();
    await questionGate?.stop();
    await taskGate?.stop();
    cleanup(sample);
  }
});

test("WatchShow focuses lead prompts and manual dock reopening drops stale targets", async ({
  page,
}) => {
  test.skip(
    process.platform === "win32",
    "the prompt fixture uses a POSIX PTY",
  );
  const sample = fixture();
  const codex = installPromptingCodex();
  let stopFocusRoute: (() => Promise<void>) | undefined;
  try {
    startPromptingLead(sample);
    await openWish(page, sample.title);
    const dock = page.locator('[data-wish-action-dock="true"]');
    const promptItem = dock.locator(
      `[data-action-dock-item^="lead-prompt-${sample.wishId}:"]`,
    );
    // The action is mounted immediately, but the current WatchShow target may
    // select another item in the dock.  Verify mounting separately from the
    // explicit focus target below.
    await expect(promptItem).toHaveCount(1, { timeout: 10_000 });

    // Explicitly target the lead prompt before checking visibility/focus; the
    // test must not depend on the dock's initial carousel selection.
    stopFocusRoute = await focusOnNextWatchShow(
      page,
      sample.wishId,
      `lead-prompt-${sample.wishId}`,
    );
    await page.reload();
    await expect(promptItem).toBeVisible();
    await expect(promptItem.getByRole("button").first()).toBeFocused();
    await stopFocusRoute();
    stopFocusRoute = undefined;

    // A later signal can arrive while the dock is already mounted: it must
    // focus the requested question card's first usable control.
    stopFocusRoute = await focusOnNextWatchShow(
      page,
      sample.wishId,
      `question-${sample.qOpen}`,
    );
    await page.reload();
    const questionItem = dock.locator(
      `[data-action-dock-item^="question-${sample.qOpen}:"]`,
    );
    await expect(questionItem).toBeVisible();
    await expect(questionItem.getByRole("button").first()).toBeFocused();

    // Remove the focused question through the real service, leaving the prompt as the only action. Reopening the
    // dock by hand must focus its current prompt control rather than replaying the removed question target.
    await dock.locator(".workspace-action-attention-close").click();
    await expect(
      page.locator(".workspace-action-attention-pending"),
    ).toBeVisible();
    djinn("question", "answer", sample.qOpen, "A");
    await expect(questionItem).toHaveCount(0, { timeout: 6_000 });
    await page.locator(".workspace-action-attention-pending").click();
    await expect(dock).toBeVisible();
    await expect(promptItem).toBeVisible();
    await expect(promptItem.getByRole("button").first()).toBeFocused();
  } finally {
    await stopFocusRoute?.();
    cleanup(sample);
    fs.rmSync(codex, { force: true });
  }
});

test("WatchShow focuses a ready wish's grant control", async ({ page }) => {
  const sample = readyFixture();
  let stopFocusRoute: (() => Promise<void>) | undefined;
  try {
    await openWish(page, sample.title);
    stopFocusRoute = await focusOnNextWatchShow(
      page,
      sample.wishId,
      `grant-${sample.wishId}`,
    );
    await page.reload();
    const dock = page.locator('[data-wish-action-dock="true"]');
    const grantItem = dock.locator(
      `[data-action-dock-item^="grant-${sample.wishId}"]`,
    );
    await expect(grantItem).toBeVisible();
    await expect(grantItem.getByRole("button").first()).toBeFocused();
  } finally {
    await stopFocusRoute?.();
    cleanupReady(sample);
  }
});

test("the summary switches one detail panel while global actions and settings stay usable", async ({
  page,
}) => {
  const sample = fixture();
  try {
    await openWish(page, sample.title);
    await collapseGlobalActions(page);
    await expect(panel(page, "summary")).toHaveCount(1);
    const sidebarToggle = page.locator(".app-toolbar-toggle");
    await sidebarToggle.click();
    await expect(page.locator(".wish-app")).toHaveClass(/sidebar-collapsed/);
    await assertGlobalTriggerClearsToolbar(page);
    await page.setViewportSize({ width: 700, height: 1000 });
    await assertGlobalTriggerClearsToolbar(page);
    await page.setViewportSize({ width: 1440, height: 1000 });
    await sidebarToggle.click();
    await expect(page.locator(".wish-app")).not.toHaveClass(
      /sidebar-collapsed/,
    );

    for (const id of ["tasks", "tilasms", "activity"]) {
      await expect(section(page, id)).toHaveCount(1);
      await expect(panel(page, id)).toBeHidden();
    }
    await expect(globalTrigger(page)).toContainText("1");
    await expect(globalDialog(page)).toHaveClass(/is-collapsed/);
    await openGlobalActions(page);
    await expect(
      globalDialog(page).getByRole("heading", {
        name: /May the worker edit the lamp\?/,
      }),
    ).toBeVisible();
    await expect(
      globalDialog(page).locator("[data-action-dock-item]"),
    ).toHaveCount(1);
    await closeGlobalActions(page);

    const settings = page.locator(".workspace-settings-trigger");
    await settings.click();
    await expect(page.getByRole("menu")).toBeVisible();
    await page.getByRole("menuitem", { name: /Rights|Droits/ }).click();
    const rights = page.getByRole("dialog", { name: /Rights|Droits/ });
    await expect(rights).toBeVisible();
    const allowance = rights.getByRole("combobox").first();
    await expect(allowance).toBeVisible();
    await allowance.selectOption("1");
    await expect(allowance).toHaveValue("1");
    await rights.getByRole("button", { name: /Close|Fermer/ }).click();
    await expect(rights).toHaveCount(0);
    await expect(settings).toBeFocused();

    await settings.click();
    const agentsMenuLabel = workspaceLocalePrefixPattern("tabs.agents");
    const agentsDialogLabel = workspaceLocalePattern("tabs.agents");
    await page.getByRole("menuitem", { name: agentsMenuLabel }).click();
    const agents = page.getByRole("dialog", { name: agentsDialogLabel });
    await expect(agents).toBeVisible();
    await agents.getByRole("button", { name: /Close|Fermer/ }).click();
    await expect(agents).toHaveCount(0);
    await expect(settings).toBeFocused();

    await openSection(page, "tasks");
    await expect(panel(page, "tasks")).toContainText(
      "Wait for the lamp answer",
    );
    // The dock carries the actionable question; the waiting worker remains in
    // the Tasks panel (legacy uses .plan-waiting, workspace hierarchy uses its
    // moving subgroup).
    await expect(
      panel(page, "tasks").locator(".plan-waiting, .tasks-free-moving"),
    ).toContainText("W1");
    await openGlobalActions(page);
    await expect(globalDialog(page).locator(".question-card")).toBeVisible();
    await closeGlobalActions(page);
    await page.screenshot({
      path: path.join(shots, "workspace-tasks.png"),
      fullPage: true,
    });

    await openSection(page, "tilasms");
    await expect(panel(page, "tasks")).toBeHidden();
    await expect(panel(page, "tilasms")).toContainText("Workspace model");
    await assertUsablePanelBody(panel(page, "tilasms"), ".tilasm-frame");
    await page.screenshot({
      path: path.join(shots, "workspace-tilasms.png"),
      fullPage: true,
    });

    await openSection(page, "activity");
    await expect(panel(page, "tilasms")).toBeHidden();
    await expect(panel(page, "activity")).toContainText(
      "The first journal entry",
    );
    await expect(panel(page, "activity").locator('[role="tab"]')).toHaveCount(
      3,
    );
    await panel(page, "activity")
      .getByRole("button", { name: /Close Activity|Fermer Activité/ })
      .click();
    await expect(panel(page, "activity")).toBeHidden();
    await expect(stage(page)).not.toHaveAttribute("data-wish-maximized");
    await expect(stage(page)).not.toHaveAttribute(
      "data-wish-active-panel",
      "activity",
    );
    await assertNoDuplicateIds(page);
  } finally {
    cleanup(sample);
  }
});

test("panel selection starts on pointer down, keeps drafts mounted, and closes by keyboard", async ({
  page,
}) => {
  const sample = fixture();
  try {
    await openWish(page, sample.title);
    await clearWorkspace(page, sample.wishId, sample.title);
    await expect(panel(page, "tasks")).toBeHidden();

    const taskToggle = section(page, "tasks").locator(
      ".workspace-section-toggle",
    );
    const toggleBox = await taskToggle.boundingBox();
    expect(toggleBox).toBeTruthy();
    await page.mouse.move(
      toggleBox!.x + toggleBox!.width / 2,
      toggleBox!.y + toggleBox!.height / 2,
    );
    await page.mouse.down();
    await expect(panel(page, "tasks")).toBeVisible();
    await expect(stage(page)).toHaveAttribute(
      "data-wish-active-panel",
      "tasks",
    );
    await page.mouse.up();
    await expect(taskToggle).toHaveAttribute("aria-current", "true");

    const taskPanel = panel(page, "tasks");
    await taskPanel.getByRole("button", { name: "Mark done" }).click();
    const taskDraft = page
      .locator("dialog.task-drawer-shell")
      .getByRole("textbox", {
        name: "Why, in a few words (optional)",
      });
    await expect(taskDraft).toBeVisible();
    await taskDraft.fill("preserve this task draft");

    const taskDrawer = page.locator("dialog.task-drawer-shell");
    await taskDrawer.getByRole("button", { name: /Close|Fermer/ }).click();
    await expect(taskDrawer).toBeHidden();
    await openSection(page, "tilasms");
    await expect(taskPanel).toBeHidden();
    await expect(panel(page, "tilasms")).toBeVisible();
    await expect(stage(page)).toHaveAttribute(
      "data-wish-active-panel",
      "tilasms",
    );
    await expect(stage(page)).not.toHaveAttribute("data-wish-maximized");
    await expect(
      stage(page).locator("main [data-wish-panel]:not([hidden])"),
    ).toHaveCount(1);

    await openSection(page, "tasks");
    await taskPanel.getByRole("button", { name: "Mark done" }).click();
    await expect(taskDrawer).toBeVisible();
    await expect(taskDraft).toHaveValue("preserve this task draft");
    await taskDrawer.getByRole("button", { name: /Close|Fermer/ }).focus();
    await page.keyboard.press("Escape");
    await expect(taskDrawer).toBeHidden();
    await taskPanel.focus();
    await taskPanel.press("Escape");
    await expect(taskPanel).toBeHidden();
    await expect(stage(page)).not.toHaveAttribute("data-wish-active-panel");
    await expect(taskToggle).toBeFocused();
    await assertNoDuplicateIds(page);
  } finally {
    cleanup(sample);
  }
});

test("malformed workspace storage falls back to a safe summary", async ({
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
    await expect(panel(page, "tasks")).toBeHidden();
    await expect(panel(page, "tilasms")).toBeHidden();
    await expect(panel(page, "activity")).toBeHidden();
    await expect(stage(page)).not.toHaveAttribute("data-wish-active-panel");
    await assertNoDuplicateIds(page);
  } finally {
    cleanup(sample);
  }
});

test("workspace storage persists one selected panel and stays isolated per wish", async ({
  page,
}) => {
  const sample = fixture();
  try {
    await openWish(page, sample.title);
    await openSection(page, "tasks");
    await reloadWish(page, sample.title);
    await expect(panel(page, "tasks")).toBeVisible();
    await expect(panel(page, "tilasms")).toBeHidden();
    await expect(panel(page, "activity")).toBeHidden();

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
    await expect(panel(page, "tasks")).toBeVisible();
    await expect(panel(page, "tilasms")).toBeHidden();
    await expect(panel(page, "activity")).toBeHidden();
    await expect(
      stage(page).locator("main [data-wish-panel]:not([hidden])"),
    ).toHaveCount(1);
  } finally {
    cleanup(sample);
  }
});

test("narrow workspace uses the icon rail and survives denied storage", async ({
  page,
}) => {
  const sample = fixture();
  try {
    await openWish(page, sample.title);
    await page.setViewportSize({ width: 700, height: 1000 });
    await clearWorkspace(page, sample.wishId, sample.title);
    await expect(stage(page)).toHaveAttribute("data-wish-nav-compact", "true");
    await openSection(page, "tasks");
    await openSection(page, "tilasms");
    await expect(panel(page, "tasks")).toBeHidden();
    await expect(panel(page, "tilasms")).toBeVisible();
    await expect(
      stage(page).locator("main [data-wish-panel]:not([hidden])"),
    ).toHaveCount(1);
    await stage(page).scrollIntoViewIfNeeded();
    await page.screenshot({
      path: path.join(shots, "workspace-narrow-rail.png"),
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
    await expect(panel(page, "tasks")).toBeHidden();
    await expect(panel(page, "tilasms")).toBeHidden();
    await assertNoDuplicateIds(page);
  } finally {
    cleanup(sample);
  }
});

test("task decisions and the same Tilasm deep link work repeatedly after closing panels", async ({
  page,
}) => {
  const sample = fixture();
  try {
    await openWish(page, sample.title);
    const activity = await openSection(page, "activity");
    await activity.getByRole("tab", { name: "Decisions" }).click();
    await expect(activity.locator(".decision-row")).toHaveCount(2);
    const decisionTask = activity
      .locator(".decision-row")
      .filter({ hasText: "Which oil should the lamp use?" });
    await decisionTask.getByRole("link", { name: "W1" }).click();
    await expect(panel(page, "activity")).toBeHidden();
    await expect(panel(page, "tasks")).toBeVisible();
    await expect(panel(page, "tasks").locator(".wish-task")).toContainText(
      "Wait for the lamp answer",
    );
    const taskDrawer = page.locator("dialog.task-drawer-shell");
    await expect(taskDrawer).toBeVisible();
    await expect(
      taskDrawer.getByRole("button", { name: /Close|Fermer/ }),
    ).toBeFocused();

    await page
      .locator("dialog.task-drawer-shell")
      .getByRole("link", { name: /From Q02/ })
      .click();
    await expect(panel(page, "tasks")).toBeHidden();
    await expect(panel(page, "activity")).toBeVisible();
    await expect(
      panel(page, "activity").locator(".decision-row.focused"),
    ).toContainText("Which oil should the lamp use?");

    const decisionDrawer = page.locator("dialog.wish-settings-drawer");
    await expect(decisionDrawer).toBeVisible();
    await decisionDrawer.getByRole("button", { name: /Close|Fermer/ }).click();
    await expect(decisionDrawer).toHaveCount(0);

    const activityAgain = panel(page, "activity");
    await activityAgain.getByRole("tab", { name: "Journal" }).click();
    const journalEntry = activityAgain
      .locator(".workspace-journal-entry")
      .filter({ hasText: "Open the workspace model" });
    await expect(journalEntry).toHaveCount(1);
    await journalEntry.click();
    const journalDrawer = page.getByRole("dialog", {
      name: "Open the workspace model",
    });
    await expect(journalDrawer).toBeVisible();
    await journalDrawer
      .getByRole("link", { name: "the workspace model" })
      .click();
    await expect(journalDrawer).toHaveCount(0);
    await expect(panel(page, "tilasms")).toBeVisible();
    await expect(
      page.frameLocator("iframe.tilasm-frame").locator("#workspace-model"),
    ).toHaveText("Workspace model");

    const reopenedActivity = await openSection(page, "activity");
    await reopenedActivity.getByRole("tab", { name: "Journal" }).click();
    const reopenedJournalEntry = reopenedActivity
      .locator(".workspace-journal-entry")
      .filter({ hasText: "Open the workspace model" });
    await expect(reopenedJournalEntry).toHaveCount(1);
    await reopenedJournalEntry.click();
    const reopenedJournalDrawer = page.getByRole("dialog", {
      name: "Open the workspace model",
    });
    await expect(reopenedJournalDrawer).toBeVisible();
    await reopenedJournalDrawer
      .getByRole("link", { name: "the workspace model" })
      .click();
    await expect(reopenedJournalDrawer).toHaveCount(0);
    await expect(panel(page, "tilasms")).toBeVisible();
    await expect(
      page.frameLocator("iframe.tilasm-frame").locator("#workspace-model"),
    ).toHaveText("Workspace model");
    await expect(stage(page)).not.toHaveAttribute("data-wish-maximized");
    await assertNoDuplicateIds(page);
  } finally {
    cleanup(sample);
  }
});
