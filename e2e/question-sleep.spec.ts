// Later and Sleep, against the real djinn up --browser (see global-setup.ts): the clock in an open question's top right
// corner puts it off (later_time), sending it below those asked before, kept across a reload and in the brief, while
// one asked after comes below it; pressed again, it gives the question its place back. Beside it, the moon asks what
// the question sleeps until; asleep, it leaves the column for a grey count below it, whose list says what each waits
// for, and Wake brings it back first. One the lead puts to sleep until a time wakes by itself at a pass of the
// scheduler, above the questions asked before it. Every run makes fresh identifiers, so --repeat-each works on the same
// djinn.
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

const at = "2026-10-16T09:10:00Z";

// What each test makes, paused once it ends: the specs after find the place as they left it (e2e/test.ts).
const made: string[] = [];
test.afterEach(() => {
  for (const id of made.splice(0)) djinn("wish", "pause", id);
});

test("Later sends a question to the bottom and back; Sleep puts a question out of the column until its conditions hold and wakes it first", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  for (const w of wishes().filter((w) => w.state === "WISH_STATE_ACTIVE"))
    djinn("wish", "pause", w.id);

  const wishId = randomUUID();
  made.push(wishId);
  const title = `Sleep ${randomUUID().slice(0, 8)}`;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-sleep-"));
  const file = path.join(dir, "sleep.json");
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

  // Later sits first, then Sleep and Move, named by its tooltip; a click sends Q01 below Q02 and Q03, and Q02 opens.
  const corner = open.locator(".question-corner button");
  await expect(corner).toHaveCount(3);
  await expect(corner.nth(2)).toHaveAccessibleName("Move to another wish");
  const later = open.getByRole("button", { name: "Later", exact: true });
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
  const briefLater = djinn("wish", "brief", wishId);
  const linesAt = [
    "**Q02** Which oil? (can wait)",
    "**Q03** Which wick? (can wait)",
    "**Q01** Brass or copper? (can wait)\n  - Put off for later",
    "**Q04** Which flame? (can wait)",
  ].map((line) => briefLater.indexOf(line));
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

  // The moon, between Later and Move, named by its tooltip, opens the dialog: nothing picked, nothing to send.
  const sleep = corner.nth(1);
  await expect(sleep).toHaveAccessibleName("Sleep");
  await sleep.focus();
  await sleep.hover();
  await expect(page.getByRole("tooltip")).toHaveText(
    "Sleep until tasks are over, a request is merged or a time comes: out of the list until then",
  );
  await sleep.click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("Put Q01 to sleep");
  const confirm = dialog.getByRole("button", { name: "Put to sleep" });
  await expect(confirm).toBeDisabled();
  await dialog.getByLabel("Other conditions").fill("3d");
  await confirm.click();
  await expect(dialog).toBeHidden();

  // Out of the column; a grey count below lists it on demand, with what it waits for.
  await expect(codes).toHaveText(["Q02", "Q03", "Q04"]);
  await expect(open.locator(".question-id")).toHaveText("Q02");
  const asleep = column.locator(".question-asleep");
  const toggle = asleep.getByRole("button", { name: "1 question asleep" });
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await toggle.click();
  const row = asleep.locator("li");
  await expect(row).toHaveCount(1);
  await expect(row.locator(".question-id")).toHaveText("Q01");
  await expect(row.locator(".question-asleep-until")).toContainText("until ");
  expect(djinn("wish", "brief", wishId)).toContain(
    "## Asleep questions\n\nOut of the developer's questions",
  );

  // Wake brings it back now, first, its grey line saying so.
  await row.getByRole("button", { name: "Wake" }).click();
  await expect(codes).toHaveText(["Q01", "Q02", "Q03", "Q04"]);
  await expect(asleep).toHaveCount(0);
  await column
    .locator(".question-card")
    .filter({ hasText: "Brass or copper?" })
    .click();
  await expect(open.locator(".question-asked")).toContainText("woke");

  // The lead puts Q03 to sleep until a time a few seconds away: it leaves, then wakes by itself, above Q02.
  const soon = new Date(Date.now() + 2500).toISOString();
  djinn("question", "sleep", "Q03", "--wish-id", wishId, "--until", soon);
  await expect(codes).toHaveText(["Q01", "Q02", "Q04"]);
  await expect(codes).toHaveText(["Q01", "Q03", "Q02", "Q04"], {
    timeout: 20_000,
  });
  await page.reload();
  await expect(codes).toHaveText(["Q01", "Q03", "Q02", "Q04"]);
  expect(djinn("wish", "brief", wishId)).toMatch(
    /\*\*Q03\*\* Which wick\? \(can wait\)\n {2}- Woke [^\n]*, after sleeping until /,
  );
  expect(errors).toEqual([]);
});
