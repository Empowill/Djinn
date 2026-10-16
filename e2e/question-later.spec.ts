// Later, against the real djinn up --browser (see global-setup.ts): the clock in an open question's top right corner,
// beside Move, puts it off. Djinn keeps when (later_time), so the question goes below those asked before, stays there
// after a reload and in the brief, and one asked after still comes below it; the one after it opens. Pressed again,
// it gives the question its place back. Every run makes fresh identifiers, so --repeat-each works on the same djinn.
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

const at = "2026-10-16T09:10:00Z";

test("Later sends a question to the bottom, kept across a reload and in the brief; pressed again, back in place", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  for (const w of wishes().filter((w) => w.state === "WISH_STATE_ACTIVE"))
    djinn("wish", "pause", w.id);

  const wishId = randomUUID();
  const title = `Later ${randomUUID().slice(0, 8)}`;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-later-"));
  const file = path.join(dir, "later.json");
  fs.writeFileSync(
    file,
    JSON.stringify({
      version: 1,
      create_time: at,
      wish: { id: wishId, title, create_time: at },
    }),
  );
  djinn("wish", "import", file);
  djinn("question", "ask", "Brass or copper?", wishId);
  djinn("question", "ask", "Which oil?", wishId);
  djinn("question", "ask", "Which wick?", wishId);

  await page.goto(process.env.DJINN_URL!);
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await expect(page.locator(".hero h1")).toHaveText(title);
  const column = page.locator(".question-column");
  const codes = column.locator(".question-card .question-id");
  const open = column.locator(".question-row.current");
  await expect(codes).toHaveText(["Q01", "Q02", "Q03"]);
  await expect(open.locator(".question-id")).toHaveText("Q01");

  // Later sits before Move, named by its tooltip; a click sends Q01 below Q02 and Q03, and Q02 opens.
  const later = open.getByRole("button", { name: "Later", exact: true });
  await expect(open.locator(".question-corner button")).toHaveCount(2);
  await expect(
    open.locator(".question-corner button").nth(1),
  ).toHaveAccessibleName("Move to another wish");
  await expect(later).toHaveAttribute("aria-pressed", "false");
  await later.hover();
  await expect(page.getByRole("tooltip")).toHaveText(
    "Later: to the bottom of the list, where it stays",
  );
  await later.click();
  await expect(codes).toHaveText(["Q02", "Q03", "Q01"]);
  await expect(open.locator(".question-id")).toHaveText("Q02");

  // Djinn keeps it: after a reload, and for the lead; a question asked after comes below it.
  djinn("question", "ask", "Which flame?", wishId);
  await page.reload();
  await expect(codes).toHaveText(["Q02", "Q03", "Q01", "Q04"]);
  const brief = djinn("wish", "brief", wishId);
  const linesAt = [
    "**Q02** Which oil? (can wait)",
    "**Q03** Which wick? (can wait)",
    "**Q01** Brass or copper? (can wait)\n  - Put off for later",
    "**Q04** Which flame? (can wait)",
  ].map((line) => brief.indexOf(line));
  expect(linesAt.every((i) => i >= 0)).toBe(true);
  expect([...linesAt].sort((a, b) => a - b)).toEqual(linesAt);

  // Pressed on Q01, the button gives it its place back, and it stays open.
  await column
    .locator(".question-card")
    .filter({ hasText: "Brass or copper?" })
    .click();
  await expect(open.locator(".question-id")).toHaveText("Q01");
  await expect(later).toHaveAttribute("aria-pressed", "true");
  await expect(open.locator(".question-asked")).toContainText("put off");
  await later.click();
  await expect(codes).toHaveText(["Q01", "Q02", "Q03", "Q04"]);
  await expect(open.locator(".question-id")).toHaveText("Q01");
  await expect(later).toHaveAttribute("aria-pressed", "false");
  await page.reload();
  await expect(codes).toHaveText(["Q01", "Q02", "Q03", "Q04"]);
  expect(errors).toEqual([]);
});
