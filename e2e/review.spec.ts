// Review and decide at a glance, against the real djinn up --browser (see global-setup.ts): the bar of what waits,
// "Enlighten me" read by the lead in the brief, a revision from the command line, "Rub the lamp" read back from the
// command line, a block marked read and seen in the brief. Screenshots of the flight plan and of the wish, dark and
// light, go to test-results/e2e/.
import { type Page, expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
const shots = path.join(__dirname, "../test-results/e2e");

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

type Wish = { id: string; state: string };
const wishes = (): Wish[] =>
  JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];

const at = "2026-10-08T09:10:00Z";
// A wish whose tasks went every way: done, failed, planned, cut short.
const lampOf = (wishId: string, title: string) => ({
  version: 1,
  create_time: at,
  wish: { id: wishId, title, create_time: at },
  tasks: [
    ["W1", "Trim the wick", "TASK_STATUS_DONE"],
    ["W2", "Polish the brass", "TASK_STATUS_FAILED"],
    ["W3", "Fill the oil", "TASK_STATUS_INTERRUPTED"],
    ["W4", "Light it", "TASK_STATUS_PENDING"],
  ].map(([code, title, status]) => ({
    id: randomUUID(),
    wish_id: wishId,
    code,
    title,
    status,
    provider: "PROVIDER_CLAUDE",
    create_time: at,
    ...(status === "TASK_STATUS_FAILED"
      ? { error: "the brass was lacquered" }
      : {}),
  })),
});

async function shoot(page: Page, name: string) {
  // The cards fade in: the shot waits for them.
  for (const card of await page.locator(".question-card").all())
    await expect(card).toHaveCSS("opacity", "1");
  await page.screenshot({ path: path.join(shots, `${name}.png`) });
}

async function theme(page: Page, value: "" | "light") {
  await page.evaluate((v) => {
    if (v) localStorage.setItem("djinn.theme", v);
    else localStorage.removeItem("djinn.theme");
  }, value);
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme",
    value || "dark",
  );
}

test("a question is enlightened, revised by the lead, then rubbed in one click; a block is marked read", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  for (const w of wishes().filter((w) => w.state === "WISH_STATE_ACTIVE"))
    djinn("wish", "pause", w.id);

  const tag = randomUUID().slice(0, 8);
  const wishId = randomUUID();
  const title = `Light the lamp ${tag}`;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-review-"));
  const file = path.join(dir, "lamp.json");
  fs.writeFileSync(file, JSON.stringify(lampOf(wishId, title)));
  djinn("wish", "import", file);
  djinn(
    "question",
    "ask",
    "Which oil for the wick?",
    wishId,
    "--options",
    "Olive — the classic",
    "--options",
    "Paraffin — brighter",
    "--recommendation",
    "A: it smells good, and the wick lasts.",
    "--context",
    "## What it changes\n\nThe oil sets how long the lamp burns.\n\n- Olive: softer light.\n- Paraffin: brighter, smells.",
  );
  djinn(
    "question",
    "ask",
    "May the worker polish the brass again?",
    wishId,
    "--recommendation",
    "Yes: the lacquer is off now.",
  );
  djinn(
    "block",
    "put",
    wishId,
    "--kind",
    "section",
    "--title",
    "Ship on Friday",
    "--content",
    "We ship **on Friday**, unless it rains.\n\n| Day | Weather |\n| --- | --- |\n| Fri | Sun |",
  );

  // Tall enough for a screenshot to hold most of the page: it scrolls inside the window.
  await page.setViewportSize({ width: 1440, height: 2200 });
  await page.goto(process.env.DJINN_URL!);
  await expect(page.locator(".hero h1")).toHaveText("Flight plan");
  // The bar of what waits: one line per question, a click goes there. W3, cut short and not resumed by Djinn, asks
  // nothing: it is history.
  const bar = page.locator(".attention-bar");
  await expect(bar).toContainText("2 things wait for you");
  await expect(bar.locator(".attention-item")).toHaveCount(2);
  await expect(bar).not.toContainText("W3");
  await expect(
    page.locator(".plan-wish").filter({ hasText: title }),
  ).toContainText("2 questions");
  await shoot(page, "review-flight-plan-dark");
  await theme(page, "light");
  await shoot(page, "review-flight-plan-light");
  await theme(page, "");

  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await expect(page.locator(".hero h1")).toHaveText(title);
  const card = page
    .locator(".question-card")
    .filter({ hasText: "Which oil for the wick?" });
  await bar
    .locator(".attention-item")
    .filter({ hasText: "Which oil for the wick?" })
    .click();
  await expect(card).toBeFocused();
  await expect(card.locator(".question-context h2")).toHaveText(
    "What it changes",
  );
  await shoot(page, "review-wish-dark");
  await theme(page, "light");
  await page.getByRole("tab", { name: /^Tasks/ }).click();
  await page
    .locator(".wish-task")
    .filter({ hasText: "Polish the brass" })
    .locator(".wish-task-heading")
    .click();
  await shoot(page, "review-wish-light");
  await page.getByRole("tab", { name: /^Wish/ }).click();
  await theme(page, "");

  // Enlighten me: what to dig into goes to the lead; the question waits for the lead, not for you.
  await card.getByRole("button", { name: "Enlighten me" }).click();
  await card
    .getByLabel("What the lead should dig into")
    .fill("How long does each burn?");
  await card
    .getByRole("button", { name: "Ask the lead to investigate" })
    .click();
  const digging = page
    .locator(".investigating-section .question-card")
    .filter({ hasText: "Which oil for the wick?" });
  await expect(digging).toContainText("Being investigated");
  await expect(bar.locator(".attention-item")).toHaveCount(1);
  const brief = djinn("wish", "brief", wishId);
  expect(brief).toContain("## To investigate");
  expect(brief).toContain("How long does each burn?");

  // The lead revises from the command line: the question waits for you again, revised, its history folded.
  djinn(
    "question",
    "revise",
    "Q01",
    "--wish-id",
    wishId,
    "--context",
    "## What we found\n\nOlive burns 6 hours, paraffin 9.",
    "--recommendation",
    "B: it lasts half as long again.",
  );
  const revised = page
    .locator(".decisions-section .question-card")
    .filter({ hasText: "Which oil for the wick?" });
  await expect(revised.locator(".revised-badge")).toHaveText("Revised");
  await expect(revised.locator(".question-rounds summary")).toHaveText(
    "History: 2 rounds",
  );
  await expect(revised.locator(".option-recommended")).toHaveCount(1);
  await expect(
    revised.locator(".option", { has: page.locator(".option-recommended") }),
  ).toContainText("Paraffin");
  // The new recommendation is the option chosen until you pick another.
  await expect(
    revised.getByRole("button", { name: /Paraffin/ }),
  ).toHaveAttribute("aria-pressed", "true");
  await revised.scrollIntoViewIfNeeded();
  await shoot(page, "review-wish-revised-dark");

  // Rub the lamp: one click answers with the option selected, here the recommended one, and the lead reads it from
  // the command line.
  await revised.getByRole("button", { name: "Rub the lamp" }).click();
  await expect(revised).toHaveCount(0);
  const [q01] = JSON.parse(
    djinn("question", "list", "--wish-id", wishId, "--json"),
  ).questions;
  expect(q01.answer.choice).toBe("CHOICE_B");
  expect(q01.revision).toBe(1);

  // A block marked read, seen in the brief.
  const block = page
    .locator(".wish-block")
    .filter({ hasText: "Ship on Friday" });
  await block.getByRole("button", { name: "Mark read" }).click();
  await expect(block.getByRole("button", { name: "Read" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  expect(djinn("wish", "brief", wishId)).toContain(
    "- **read** block Ship on Friday (section), ",
  );

  djinn("wish", "pause", wishId);
  fs.rmSync(dir, { recursive: true, force: true });
  expect(errors).toEqual([]);
});
