// Sleep, against the real djinn up --browser (see global-setup.ts): the moon in an open question's top right corner,
// between Later and Move, asks what it sleeps until; asleep, the question leaves the column for a grey count below it,
// whose list says what each waits for, and Wake brings it back first. One the lead puts to sleep until a time wakes by
// itself at a pass of the scheduler, above the questions asked before it. Every run makes fresh identifiers, so
// --repeat-each works on the same djinn.
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

test("Sleep puts a question out of the column until its conditions hold; it wakes first, by hand or by itself", async ({
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

  // The moon, between Later and Move, named by its tooltip, opens the dialog: nothing picked, nothing to send.
  const corner = open.locator(".question-corner button");
  await expect(corner).toHaveCount(3);
  const sleep = corner.nth(1);
  await expect(sleep).toHaveAccessibleName("Sleep");
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
  await expect(codes).toHaveText(["Q02", "Q03"]);
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
  await expect(codes).toHaveText(["Q01", "Q02", "Q03"]);
  await expect(asleep).toHaveCount(0);
  await column
    .locator(".question-card")
    .filter({ hasText: "Brass or copper?" })
    .click();
  await expect(open.locator(".question-asked")).toContainText("woke");

  // The lead puts Q03 to sleep until a time a few seconds away: it leaves, then wakes by itself, above Q02.
  const soon = new Date(Date.now() + 4000).toISOString();
  djinn("question", "sleep", "Q03", "--wish-id", wishId, "--until", soon);
  await expect(codes).toHaveText(["Q01", "Q02"]);
  await expect(codes).toHaveText(["Q01", "Q03", "Q02"], { timeout: 20_000 });
  await page.reload();
  await expect(codes).toHaveText(["Q01", "Q03", "Q02"]);
  expect(djinn("wish", "brief", wishId)).toMatch(
    /\*\*Q03\*\* Which wick\? \(can wait\)\n {2}- Woke [^\n]*, after sleeping until /,
  );
  expect(errors).toEqual([]);
});
