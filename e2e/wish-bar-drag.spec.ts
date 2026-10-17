// The wish bar's drag and drop (Q01): every wish is dragged by its row; while it is dragged, a
// thin line shows where it would land. A paused wish dropped among the paused ones changes place there, an order the
// server keeps; dropped among the active ones, it becomes active at that rank, and the last active wish is paused,
// first among the paused ones.
import { type Locator, type Page, expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
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

interface Listed {
  id: string;
  title: string;
  state?: string;
  rank?: number;
}

// The titles of the wishes in a state, as djinn lists them.
function listed(state: string, titles: string[]): string[] {
  const wishes = (JSON.parse(djinn("wish", "list", "--json")).wishes ??
    []) as Listed[];
  return wishes
    .filter((w) => w.state === state && titles.includes(w.title))
    .map((w) => w.title);
}

// Drags the row from onto the upper or lower half of the row to, by the mouse; the drop line shows on the edge it
// points at, where a screenshot catches it, before the drop.
async function drag(
  page: Page,
  from: Locator,
  to: Locator,
  half: "upper" | "lower",
  shot: string,
) {
  const a = (await from.boundingBox())!;
  const b = (await to.boundingBox())!;
  await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2);
  await page.mouse.down();
  const x = b.x + b.width / 2;
  const y = b.y + b.height * (half === "upper" ? 0.25 : 0.75);
  await page.mouse.move(x, y, { steps: 8 });
  const line = page.locator(".wish-list .wish-drop-line");
  // Thin, across the row, on the edge between the two wishes. Chromium drops some of the drag moves Playwright sends:
  // each try moves the pointer again by a pixel, until the line follows it.
  const edge = half === "upper" ? b.y : b.y + b.height;
  let nudge = 0;
  await expect
    .poll(async () => {
      await page.mouse.move(x, y + (nudge++ % 2));
      const box = await line.boundingBox();
      return box !== null && Math.abs(box.y + box.height / 2 - edge) <= 3;
    })
    .toBe(true);
  const box = (await line.boundingBox())!;
  expect(box.height).toBeLessThanOrEqual(3);
  expect(box.width).toBeGreaterThan(b.width / 2);
  await page.screenshot({ path: `test-results/e2e/${shot}` });
  await page.mouse.up();
  await expect(line).toHaveCount(0);
}

test("a paused wish is dragged among the paused ones, then among the active ones", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const tag = randomUUID().slice(0, 6);
  const make = (title: string) =>
    JSON.parse(djinn("wish", "make", title, "--paused", "--json")).wish
      .id as string;
  // Three active wishes, A1 first: each moved to the first rank pushes the others down, and those of earlier specs out.
  const [a1, a2, a3] = ["A1", "A2", "A3"].map((n) => `${n} ${tag}`);
  for (const title of [a3, a2, a1])
    djinn("wish", "move", make(title), "--to", "1");
  // Three paused ones, made last: each comes first among the paused, in sight under the active ones.
  const [p1, p2, p3] = ["P1", "P2", "P3"].map((n) => `${n} ${tag}`);
  for (const title of [p1, p2, p3]) make(title);
  const all = [a1, a2, a3, p1, p2, p3];
  expect(listed("WISH_STATE_ACTIVE", all)).toEqual([a1, a2, a3]);
  expect(listed("WISH_STATE_PAUSED", all)).toEqual([p3, p2, p1]);

  await page.goto(process.env.DJINN_URL!);
  const row = (title: string) =>
    page.locator(".wish-list .wish-nav").filter({ hasText: title });
  await expect(row(p1)).toBeVisible();
  const shown = async () =>
    (await page.locator(".wish-list .wish-title").allTextContents()).filter(
      (t) => all.includes(t),
    );
  await expect.poll(shown).toEqual([a1, a2, a3, p3, p2, p1]);

  // No grip: the row itself is dragged, the wish's icon first on it.
  for (const title of [a1, p1]) {
    await expect(row(title).locator(".wish-grip")).toHaveCount(0);
    await expect(row(title).locator("svg.wish-icon")).toBeVisible();
  }

  // P1 dropped above P3: first among the paused, for the server too; nothing becomes active.
  await drag(page, row(p1), row(p3), "upper", "wish-bar-drag-paused.png");
  await expect.poll(shown).toEqual([a1, a2, a3, p1, p3, p2]);
  expect(listed("WISH_STATE_PAUSED", all)).toEqual([p1, p3, p2]);
  expect(listed("WISH_STATE_ACTIVE", all)).toEqual([a1, a2, a3]);

  // P2 dropped between A1 and A2: active at rank 2; A3, pushed past the third place, comes first among the paused.
  await drag(page, row(p2), row(a2), "upper", "wish-bar-drag-active.png");
  await expect.poll(shown).toEqual([a1, p2, a2, a3, p1, p3]);
  expect(listed("WISH_STATE_ACTIVE", all)).toEqual([a1, p2, a2]);
  expect(listed("WISH_STATE_PAUSED", all)).toEqual([a3, p1, p3]);

  // An active wish keeps its drag: A2 dropped above A1 takes the first rank.
  await drag(page, row(a2), row(a1), "upper", "wish-bar-drag-rank.png");
  await expect.poll(shown).toEqual([a2, a1, p2, a3, p1, p3]);

  // Alt+Arrow moves a paused wish one place down its list.
  await row(a3).focus();
  await page.keyboard.press("Alt+ArrowDown");
  await expect.poll(shown).toEqual([a2, a1, p2, p1, a3, p3]);
  expect(listed("WISH_STATE_PAUSED", all)).toEqual([p1, a3, p3]);

  expect(errors).toEqual([]);
});
