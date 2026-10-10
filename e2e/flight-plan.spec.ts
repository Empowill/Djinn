// The flight plan of the active wishes, against the real djinn up --browser (see global-setup.ts): two wishes, a
// question each, merged in one view; a question answered there reads as answered by the command line. A wish's
// tasks show what they spent: tokens, and the cost where the agent gives one.
import { expect, test } from "@playwright/test";
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
  return {
    version: 1,
    create_time: at,
    wish: { id: wishId, title, create_time: at },
    tasks: [
      {
        id: w1,
        wish_id: wishId,
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
        code: "W2",
        title: "Read the map",
        status: "TASK_STATUS_DONE",
        provider: "PROVIDER_CODEX",
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

test("the flight plan merges two wishes, and a question is answered from it", async ({
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
  // With wishes active, the window opens on their flight plan.
  await expect(page.locator(".hero h1")).toHaveText("Flight plan");
  await expect(
    page.locator(".plan-nav").filter({ hasText: "Flight plan" }),
  ).toHaveClass(/selected/);

  // Both questions, each marked with its wish.
  const oilCard = page
    .locator(".decisions-section .question-card")
    .filter({ hasText: "May the worker pour the oil?" });
  const lampCard = page
    .locator(".decisions-section .question-card")
    .filter({ hasText: "Which oil for the wick?" });
  await expect(oilCard.locator(".wish-origin")).toHaveText(`2${oilTitle}`);
  await expect(lampCard.locator(".wish-origin")).toHaveText(`1${lampTitle}`);
  // Each wish's card says what it spent: tokens, the cost where it exists, and what has none.
  const lampSpent = page
    .locator(".plan-wish")
    .filter({ hasText: lampTitle })
    .locator(".wish-spent");
  await expect(lampSpent).toContainText("cache read");
  await expect(lampSpent).toContainText("0.42");
  await expect(lampSpent).toContainText(
    "1 task without a cost: its agent gives tokens only",
  );
  await page.screenshot({
    path: path.join(__dirname, "../test-results/e2e/flight-plan.png"),
    fullPage: true,
  });

  // Answered from the merged view: the answer goes to its own wish.
  await oilCard.getByLabel("Note with the answer").fill("Pour it.");
  await oilCard.getByRole("button", { name: "Rub the lamp" }).click();
  await expect(oilCard).toHaveCount(0);
  // It is a decision of its wish, taken by you, in the Decisions tab.
  await page.getByRole("tab", { name: /^Decisions/ }).click();
  const decided = page
    .locator(".decision-row")
    .filter({ hasText: "May the worker pour the oil?" });
  await expect(decided.locator(".wish-origin")).toHaveText(`2${oilTitle}`);
  await expect(decided.locator(".status-badge")).toHaveText("Decided by you");
  await page.getByRole("tab", { name: /^Flight plan/ }).click();
  const [answered] = JSON.parse(
    djinn("question", "list", "--wish-id", oilId, "--json"),
  ).questions;
  expect(answered.answer.choice).toBe("CHOICE_YES");
  expect(answered.answer.note).toBe("Pour it.");
  await page.screenshot({
    path: path.join(__dirname, "../test-results/e2e/flight-plan-answered.png"),
    fullPage: true,
  });
  // The other wish's question still waits.
  await expect(lampCard).toBeVisible();
  const [open] = JSON.parse(
    djinn("question", "list", "--wish-id", lampId, "--json"),
  ).questions;
  expect(open.answer).toBeUndefined();

  // The wish's own view stays: its tasks with their tokens in their own tab, its note about W1, its journal.
  await page.locator(".plan-wish").filter({ hasText: lampTitle }).click();
  await expect(page.locator(".hero h1")).toHaveText(lampTitle);
  await page.getByRole("tab", { name: /^Tasks/ }).click();
  const w2 = page.locator(".wish-task").filter({ hasText: "Read the map" });
  await expect(w2.locator(".task-usage")).toHaveText(/^6\.5\s?K tokens$/);
  await page.getByRole("tab", { name: /^Wish/ }).click();
  await expect(page.locator(".wish-block")).toContainText("About W1");
  // The journal is folded: a click opens it.
  await page.getByRole("button", { name: /^Journal/ }).click();
  await expect(page.locator(".journal-list")).toContainText("Kick-off");
  await page.getByRole("tab", { name: /^Tasks/ }).click();
  await page
    .locator(".wish-task")
    .filter({ hasText: "Trim the wick" })
    .locator(".wish-task-heading")
    .click();
  await page.screenshot({
    path: path.join(__dirname, "../test-results/e2e/flight-plan-wish.png"),
    fullPage: true,
  });

  // The journal of the other wish: the commands that changed it, once asked for.
  await page.locator(".wish-nav").filter({ hasText: oilTitle }).click();
  await expect(page.locator(".hero h1")).toHaveText(oilTitle);
  await page.getByRole("button", { name: /^Journal/ }).click();
  await page.getByRole("button", { name: "Show the commands" }).click();
  const journal = page.locator(".journal-list");
  await expect(journal).toContainText("wish make");
  await expect(journal).toContainText("question answer");
  await expect(journal).toContainText("Q01 yes Pour it.");

  // The next specs find the places free.
  djinn("wish", "pause", lampId);
  djinn("wish", "pause", oilId);
  fs.rmSync(dir, { recursive: true, force: true });
  expect(errors).toEqual([]);
});
