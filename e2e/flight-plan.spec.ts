// The flying carpet of the active wishes, against the real djinn up --browser (see global-setup.ts): two wishes, a
// question each, merged in one view; a question answered there reads as answered by the command line. A wish's
// tasks show what they spent: tokens, and one price.
import { expect, test } from "./test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { portable } from "./portable";

portable();

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

type Wish = { id: string; state: string };
const wishes = (): Wish[] =>
  JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];

// A wish whose workers finished, as an export carries it: a Claude task with its cost, a Codex one with tokens only,
// a note about the first task, a line of journal, and a question still open.
const at = "2026-10-08T09:10:00Z";
const lampOf = (wishId: string, title: string) => {
  const w1 = randomUUID();
  const azimaId = randomUUID();
  return {
    version: 1,
    create_time: at,
    wish: { id: wishId, title, create_time: at },
    tasks: [
      {
        id: azimaId,
        wish_id: wishId,
        code: "T1",
        title: "The lamp",
        kind: "TASK_KIND_AZIMA",
        status: "TASK_STATUS_PENDING",
        create_time: at,
      },
      {
        id: w1,
        wish_id: wishId,
        part_of: azimaId,
        code: "W1",
        title: "Trim the wick",
        status: "TASK_STATUS_DONE",
        provider: "PROVIDER_CLAUDE",
        model: "haiku",
        usage: {
          input_tokens: "4",
          output_tokens: "409",
          cache_read_tokens: "38153",
          cache_write_tokens: "12845",
          cost_usd: 0.42,
        },
        create_time: at,
      },
      {
        id: randomUUID(),
        wish_id: wishId,
        part_of: azimaId,
        code: "W2",
        title: "Read the map",
        status: "TASK_STATUS_DONE",
        provider: "PROVIDER_CODEX",
        model: "fixture-unpriced",
        usage: {
          input_tokens: "1200",
          output_tokens: "300",
          cache_read_tokens: "5000",
        },
        create_time: at,
      },
    ],
    questions: [
      {
        id: randomUUID(),
        code: "Q01",
        wish_id: wishId,
        text: "Which oil for the wick?",
        options: ["Olive — the classic", "Paraffin — brighter"],
        recommendation: "**A**, for the smell.",
        create_time: at,
      },
    ],
    blocks: [
      {
        id: randomUUID(),
        wish_id: wishId,
        task_id: w1,
        kind: "report",
        title: "What W1 found",
        position: "1000",
        content: "The wick was **too short**: trimmed to 4 mm.",
        create_time: at,
      },
      {
        id: randomUUID(),
        wish_id: wishId,
        kind: "log",
        title: "Kick-off",
        position: "2000",
        content: "Two workers: the wick, then the map.",
        create_time: at,
      },
    ],
  };
};

// The page header's row is the window's toolbar's, its breadcrumbs where the page's content starts, past the
// sidebar or its rail, past the traffic lights in the macOS window, where the row is the lights' and the gauge
// hangs from it. The toolbar holds only the sidebar's toggle until the creation page brings Back (P23).
// fits is false where the header is narrower than its content: the operating-load gauge does not narrow (as on
// feat/wails-go's topbar), and pushes the actions past the window's edge.
async function assertHeaderChrome(
  page: import("@playwright/test").Page,
  left: number,
  size = 32,
  fits = true,
) {
  const pageHeader = page.locator(".page-header");
  const appToolbar = page.locator(".app-toolbar");
  // The sidebar's width glides for 180 ms when it folds: the breadcrumbs reach their place at its end.
  const breadcrumbs = pageHeader.locator(".breadcrumbs");
  await expect
    .poll(async () => (await breadcrumbs.boundingBox())?.x)
    .toBeCloseTo(left, 0);
  const headerBox = await pageHeader.boundingBox();
  const breadcrumbsBox = await breadcrumbs.boundingBox();
  const toggleBox = await appToolbar
    .locator(".app-toolbar-toggle")
    .boundingBox();
  expect(headerBox).not.toBeNull();
  expect(breadcrumbsBox).not.toBeNull();
  expect(toggleBox).not.toBeNull();
  expect(breadcrumbsBox!.x).toBeCloseTo(left, 0);
  await expect(pageHeader).toHaveCSS("height", "64px");
  expect(toggleBox).toMatchObject({ width: size, height: size });
  const rowCenter = toggleBox!.y + toggleBox!.height / 2;
  expect(
    Math.abs(rowCenter - (breadcrumbsBox!.y + breadcrumbsBox!.height / 2)),
  ).toBeLessThan(1);
  const actions = pageHeader.locator(".topbar-actions");
  if (await actions.count()) {
    const actionsBox = await actions.boundingBox();
    expect(actionsBox).not.toBeNull();
    // The icon actions of a wish (the flying carpet has only Talk to the Djinn and its arrow).
    const iconAction = actions.locator(".page-header-action").first();
    if (await iconAction.count()) {
      const iconActionBox = await iconAction.boundingBox();
      expect(iconActionBox).toMatchObject({ width: size, height: size });
      expect(
        Math.abs(rowCenter - (iconActionBox!.y + iconActionBox!.height / 2)),
      ).toBeLessThan(1);
    }
    // The labelled actions too: Pause, Talk to the Djinn and its arrow.
    for (const box of await actions
      .locator("button:visible")
      .evaluateAll((els) => els.map((el) => el.getBoundingClientRect())))
      expect(Math.abs(rowCenter - (box.y + box.height / 2))).toBeLessThan(1);
    const rightInset = await pageHeader.evaluate((element) =>
      Number.parseFloat(getComputedStyle(element).paddingRight),
    );
    if (fits)
      expect(actionsBox!.x + actionsBox!.width).toBeCloseTo(
        headerBox!.x + headerBox!.width - rightInset,
        0,
      );
  }
}

test("the flying carpet merges two wishes, and a question is answered from it", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  // Three wishes at a time: the earlier specs' wishes make way.
  for (const w of wishes().filter((w) => w.state === "WISH_STATE_ACTIVE"))
    djinn("wish", "pause", w.id);

  const tag = randomUUID().slice(0, 8);
  const lampId = randomUUID();
  const lampTitle = `Ship the lamp ${tag}`;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-plan-"));
  const file = path.join(dir, "lamp.json");
  fs.writeFileSync(file, JSON.stringify(lampOf(lampId, lampTitle)));
  djinn("wish", "import", file);
  const oilTitle = `Find the oil ${tag}`;
  const oilId = JSON.parse(djinn("wish", "make", oilTitle, "--json")).wish
    .id as string;
  try {
    djinn(
      "question",
      "ask",
      "May the worker pour the oil?",
      oilId,
      "--recommendation",
      "Yes: it is measured.",
    );

    // Tall enough for the screenshots to hold the whole plan: the page scrolls inside the window.
    await page.setViewportSize({ width: 1440, height: 1900 });
    await page.goto(process.env.DJINN_URL!);
    // With wishes active, the window opens on their flying carpet.
    await expect(page.locator(".hero h1")).toHaveText("Flying carpet");

    await assertHeaderChrome(page, 248);
    await page.locator(".sidebar-toggle").click();
    await expect(page.locator(".wish-app")).toHaveClass(/sidebar-collapsed/);
    await assertHeaderChrome(page, 100);
    await page.locator(".sidebar-toggle").click();
    await expect(page.locator(".wish-app")).not.toHaveClass(
      /sidebar-collapsed/,
    );
    await assertHeaderChrome(page, 248);

    await expect(
      page.locator(".plan-nav").filter({ hasText: "Flying carpet" }),
    ).toHaveClass(/selected/);

    // Both questions, each under its wish's title, by rank (question-column.tsx).
    const group = (title: string) =>
      page.locator(".question-group").filter({
        has: page.locator(".question-group-head", { hasText: title }),
      });
    await expect(
      page.locator(".question-column .question-group-head .name"),
    ).toHaveText([lampTitle, oilTitle]);
    const oilCard = group(oilTitle)
      .locator(".question-card")
      .filter({ hasText: "May the worker pour the oil?" });
    const lampCard = group(lampTitle)
      .locator(".question-card")
      .filter({ hasText: "Which oil for the wick?" });
    await expect(oilCard).toBeVisible();
    await expect(lampCard).toBeVisible();
    // On the right, the glance (Decision Q08): each wish under its title, its workers and nothing else.
    await expect(page.locator(".glance-head button")).toHaveText([
      lampTitle,
      oilTitle,
    ]);
    await expect(page.locator(".plan-wish")).toHaveCount(0);
    if (process.env.PLAN_SHOTS)
      await page.screenshot({
        path: path.join(__dirname, "../test-results/e2e/flight-plan.png"),
        fullPage: true,
      });

    // Answered from the merged view: the answer goes to its own wish. Closed, a click opens it.
    await oilCard.click();
    await expect(oilCard).toHaveClass(/current/);
    await oilCard.getByLabel("Note with the answer").fill("Pour it.");
    await oilCard.getByRole("button", { name: "To the lead" }).click();
    await expect(oilCard).toHaveCount(0);
    // It is a decision of its wish, taken by you, in the Decisions tab.
    await page.getByRole("tab", { name: /^Decisions/ }).click();
    const decided = page
      .locator(".decision-row")
      .filter({ hasText: "May the worker pour the oil?" });
    await expect(decided.locator(".wish-origin")).toHaveText(`2${oilTitle}`);
    await expect(decided.locator(".status-badge")).toHaveText("Decided by you");
    await page.getByRole("tab", { name: /^Flying carpet/ }).click();
    const [answered] = JSON.parse(
      djinn("question", "list", "--wish-id", oilId, "--json"),
    ).questions;
    expect(answered.answer.choice).toBe("CHOICE_YES");
    expect(answered.answer.note).toBe("Pour it.");
    if (process.env.PLAN_SHOTS)
      await page.screenshot({
        path: path.join(
          __dirname,
          "../test-results/e2e/flight-plan-answered.png",
        ),
        fullPage: true,
      });
    // The other wish's question still waits.
    await expect(lampCard).toBeVisible();
    const [open] = JSON.parse(
      djinn("question", "list", "--wish-id", lampId, "--json"),
    ).questions;
    expect(open.answer).toBeUndefined();

    // The history of the other wish: with no log blocks, unfolding shows the commands that changed it at once.
    await page.locator(".wish-nav").filter({ hasText: oilTitle }).click();
    await expect(page.locator(".hero h1")).toHaveText(oilTitle);
    await page.getByRole("button", { name: /^History/ }).click();
    const journal = page.locator(".journal-list");
    await expect(journal).toContainText("wish make");
    await expect(journal).toContainText("question answer");
    await expect(journal).toContainText("Q01 yes Pour it.");
  } finally {
    // The next specs find the places free.
    djinn("wish", "delete", lampId);
    djinn("wish", "delete", oilId);
    fs.rmSync(dir, { recursive: true, force: true });
  }
  expect(errors).toEqual([]);
});

test("a wish's page keeps its header chrome, its tasks' tokens and price across themes, and its blocks and journal", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  for (const w of wishes().filter((w) => w.state === "WISH_STATE_ACTIVE"))
    djinn("wish", "pause", w.id);

  const tag = randomUUID().slice(0, 8);
  const lampId = randomUUID();
  const lampTitle = `Ship the lamp ${tag}`;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-plan-"));
  const file = path.join(dir, "lamp.json");
  fs.writeFileSync(file, JSON.stringify(lampOf(lampId, lampTitle)));
  djinn("wish", "import", file);
  try {
    await page.setViewportSize({ width: 1440, height: 1900 });
    await page.goto(process.env.DJINN_URL!);
    // The wish's own view stays: its tasks with their tokens in their own tab, its block about W1 in the agents' tab,
    // its journal.
    await page
      .locator(".glance-head button")
      .filter({ hasText: lampTitle })
      .click();
    await expect(page.locator(".hero h1")).toHaveText(lampTitle);
    const pageHeader = page.locator(".page-header");
    // Share and Delete are icons; Pause and Talk to the Djinn keep their words. The operating-load gauge sits between.
    await expect(pageHeader.locator(".page-header-action")).toHaveCount(2);
    await expect(
      pageHeader.getByRole("button", { name: "Pause", exact: true }),
    ).toBeVisible();
    await expect(
      pageHeader.getByRole("group", { name: "Operating load" }),
    ).toBeVisible();
    // The icon actions name themselves in a tooltip, on hover and on focus; they have no shortcut.
    const shareAction = pageHeader.getByRole("button", {
      name: "Share",
      exact: true,
    });
    await shareAction.hover();
    await expect(page.locator('[role="tooltip"]')).toHaveText("Share");
    await expect(page.locator('[role="tooltip"] kbd')).toHaveCount(0);
    await page.mouse.move(10, 300);
    await expect(page.locator('[role="tooltip"]')).toHaveCount(0);
    await pageHeader.locator(".page-header-action.danger").focus();
    await expect(page.locator('[role="tooltip"]')).toHaveText(
      "Delete this wish",
    );
    await pageHeader.locator(".page-header-action.danger").blur();
    await assertHeaderChrome(page, 248);
    await page.setViewportSize({ width: 760, height: 1000 });
    await assertHeaderChrome(page, 100, 32, false);
    await page.evaluate(() =>
      document.documentElement.classList.add("native-mac"),
    );
    await assertHeaderChrome(page, 166, 20, false);
    await page.locator(".sidebar-toggle").click();
    await expect(page.locator(".wish-app")).toHaveClass(/sidebar-collapsed/);
    await assertHeaderChrome(page, 166, 20, false);
    await page.locator(".sidebar-toggle").click();
    await expect(page.locator(".wish-app")).not.toHaveClass(
      /sidebar-collapsed/,
    );
    await page.evaluate(() =>
      document.documentElement.classList.remove("native-mac"),
    );
    await page.setViewportSize({ width: 1440, height: 1900 });
    await page.getByRole("tab", { name: /^Tasks/ }).click();
    const azimaCard = page.locator(".azima-card");
    await azimaCard.locator(".azima-heading").click();
    const w2 = page.locator(".wish-task").filter({ hasText: "Read the map" });
    await expect(w2.locator(".task-usage")).toContainText("6.5K tokens");
    // W1's report is a note for agents: it waits in their tab with the other blocks, no section of its own.
    await page.getByRole("tab", { name: /^For agents/ }).click();
    await expect(page.locator(".wish-block")).toContainText("About W1");
    await page.getByRole("tab", { name: /^Wish/ }).click();
    await expect(page.locator("#reports")).toHaveCount(0);
    // The history is folded: a click opens it.
    await page.getByRole("button", { name: /^History/ }).click();
    await expect(page.locator(".journal-list")).toContainText("Kick-off");
    await page.getByRole("tab", { name: /^Tasks/ }).click();
    // Its Tasks tab says what it spent: one price, then its tokens; the price's hover tells what was reported and what
    // has no price (its grey in both themes: tests/styles.test.cjs).
    const lampSpent = page.locator(".wish-spent");
    await expect(lampSpent).toContainText("cache read");
    const lampPrice = lampSpent.locator(".spent-price");
    await expect(lampPrice).toHaveText("$0.42");
    await expect(lampPrice).toHaveAttribute(
      "title",
      "$0.42 reported\n1 task without a price in the table\nclaude: $0.42",
    );
    await expect(lampSpent).not.toContainText("reported");
    await expect(lampSpent).not.toContainText("without a price");
    if (process.env.PLAN_SHOTS) {
      const shown = await page.evaluate(
        () => document.documentElement.dataset.theme,
      );
      const colours: string[] = [];
      for (const theme of ["dark", "light"]) {
        await page.evaluate(
          (t) => document.documentElement.setAttribute("data-theme", t),
          theme,
        );
        await expect(lampPrice).toBeVisible();
        colours.push(
          await lampPrice.evaluate((e) => getComputedStyle(e).color),
        );
        await lampSpent.screenshot({
          path: path.join(
            __dirname,
            `../test-results/e2e/flight-plan-wish-spent-${theme}.png`,
          ),
        });
      }
      expect(colours[0]).not.toBe(colours[1]);
      await page.evaluate(
        (t) => document.documentElement.setAttribute("data-theme", t ?? "dark"),
        shown,
      );
    }
    await azimaCard.locator(".azima-heading").click();
    await page
      .locator(".wish-task")
      .filter({ hasText: "Trim the wick" })
      .locator(".wish-task-heading")
      .click();
    if (process.env.PLAN_SHOTS)
      await page.screenshot({
        path: path.join(__dirname, "../test-results/e2e/flight-plan-wish.png"),
        fullPage: true,
      });
  } finally {
    djinn("wish", "delete", lampId);
    fs.rmSync(dir, { recursive: true, force: true });
  }
  expect(errors).toEqual([]);
});
