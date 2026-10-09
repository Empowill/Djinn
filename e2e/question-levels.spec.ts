// Three levels for an open question, against the real djinn up --browser (see global-setup.ts): blocking when a task
// waits for its answer, before X when the asker said so (djinn question ask --before), can wait otherwise. A revision
// with --before "" makes a question one that can wait. The window's cards, its attention bar, the page and the brief
// list them in that order. Every run makes fresh identifiers, so --repeat-each works on the same djinn.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

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

const at = "2026-10-08T09:10:00Z";

test("open questions come blocking, then before X, then can wait, in the window, the page and the brief", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  for (const w of wishes().filter((w) => w.state === "WISH_STATE_ACTIVE"))
    djinn("wish", "pause", w.id);

  // W1 waits for Q01, which blocks it.
  const wishId = randomUUID();
  const title = `Three levels ${randomUUID().slice(0, 8)}`;
  const q1 = randomUUID();
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-levels-"));
  const file = path.join(dir, "levels.json");
  fs.writeFileSync(
    file,
    JSON.stringify({
      version: 1,
      create_time: at,
      wish: { id: wishId, title, create_time: at },
      questions: [
        {
          id: q1,
          wish_id: wishId,
          code: "Q01",
          text: "May W1 edit the lamp?",
          create_time: at,
        },
      ],
      tasks: [
        {
          id: randomUUID(),
          wish_id: wishId,
          code: "W1",
          title: "Polish the lamp",
          status: "TASK_STATUS_WAITING",
          provider: "PROVIDER_CLAUDE",
          edit_question_id: q1,
          create_time: at,
        },
      ],
    }),
  );
  djinn("wish", "import", file);
  // Q02 can wait; Q03 is needed before the first light; Q04 was needed before the merge, then the lead let it wait.
  djinn("question", "ask", "Brass or copper?", wishId);
  djinn(
    "question",
    "ask",
    "Which oil?",
    wishId,
    "--before",
    "before the first light",
  );
  djinn(
    "question",
    "ask",
    "Which wick?",
    wishId,
    "--before",
    "before the merge",
  );
  djinn("question", "revise", "Q04", "--wish-id", wishId, "--before", "");

  const order = ["Q01", "Q03", "Q02", "Q04"];
  await page.goto(process.env.DJINN_URL!);
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await expect(page.locator(".hero h1")).toHaveText(title);

  // The cards: red with the task it blocks, orange under its words, grey "Can wait".
  const cards = page.locator(".decisions-section .question-card");
  await expect(cards.locator(".question-id")).toHaveText(order);
  await expect(cards.nth(0)).toHaveClass(/is-blocking/);
  await expect(cards.nth(0).locator(".status-badge")).toHaveText("Blocks W1");
  await expect(cards.nth(1).locator(".status-badge")).toHaveText(
    "before the first light",
  );
  await expect(cards.nth(1)).toHaveCSS("opacity", "1");
  for (const i of [2, 3]) {
    await expect(cards.nth(i)).toHaveClass(/can-wait/);
    await expect(cards.nth(i).locator(".status-badge")).toHaveText("Can wait");
  }

  // The attention bar, in the same order, each line under its level's words.
  const bar = page.locator(".attention-bar .attention-item");
  await expect(bar.locator(".attention-code")).toHaveText(order);
  await expect(bar.locator(".attention-level")).toHaveText([
    "Blocking",
    "before the first light",
    "Can wait",
    "Can wait",
  ]);

  // The page, rendered in Go, and the brief follow.
  const html = path.join(dir, "page.html");
  djinn("wish", "render", wishId, "--file", html);
  const rendered = fs.readFileSync(html, "utf8");
  const cardsAt = order.map((code) => rendered.indexOf(`id="q-${code}"`));
  expect(cardsAt.every((i) => i >= 0)).toBe(true);
  expect([...cardsAt].sort((a, b) => a - b)).toEqual(cardsAt);
  expect(rendered).toContain(`<details class="q wait" id="q-Q03">`);
  expect(rendered).toContain(`<details class="q later" id="q-Q04">`);
  const brief = djinn("wish", "brief", wishId);
  const linesAt = [
    "**Q01** May W1 edit the lamp? (blocking: W1 waits)",
    "**Q03** Which oil? (before the first light)",
    "**Q02** Brass or copper? (can wait)",
    "**Q04** Which wick? (can wait)",
  ].map((line) => brief.indexOf(line));
  expect(linesAt.every((i) => i >= 0)).toBe(true);
  expect([...linesAt].sort((a, b) => a - b)).toEqual(linesAt);
  expect(errors).toEqual([]);
});
