// The rank of the azimas of a wish, against the real djinn up --browser (see global-setup.ts): in the Tasks tab, the
// azimas not done show in their rank; the developer drags one by its grip, a thin line showing where it would land,
// or moves it one step with Alt+Arrow, and Djinn ranks it there (TaskService.Rank): the scheduler then starts the work
// of the first azimas first (W134). Every run makes fresh identifiers, so --repeat-each works on the same djinn.
import { expect, type Locator, type Page, test } from "@playwright/test";
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

// drag drags the azima whose grip is from onto the upper half of the card to, and drops it once the line shows
// above that card.
async function drag(page: Page, from: Locator, to: Locator) {
  const a = (await from.boundingBox())!;
  const b = (await to.boundingBox())!;
  await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2);
  await page.mouse.down();
  const x = b.x + b.width / 2;
  const y = b.y + Math.min(b.height / 4, 12);
  await page.mouse.move(x, y, { steps: 8 });
  const line = page.locator(".azima-list.ranked .azima-drop-line");
  // Chromium drops some of the drag moves Playwright sends: each try moves the pointer again by a pixel, until the
  // line shows, thin, across the card, just above it.
  let nudge = 0;
  await expect
    .poll(async () => {
      await page.mouse.move(x, y + (nudge++ % 2));
      const box = await line.boundingBox();
      return box !== null && Math.abs(box.y + box.height / 2 - b.y) <= 8;
    })
    .toBe(true);
  const box = (await line.boundingBox())!;
  expect(box.height).toBeLessThanOrEqual(3);
  expect(box.width).toBeGreaterThan(b.width / 2);
  await page.screenshot({ path: "test-results/e2e/azima-rank-drag.png" });
  await page.mouse.up();
  await expect(line).toHaveCount(0);
}

test("the developer ranks the azimas by dragging them, or with Alt+Arrow", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const wishId = randomUUID();
  const title = `Rank the plan ${wishId.slice(0, 8)}`;
  const ids = { T1: randomUUID(), T2: randomUUID(), T3: randomUUID() };
  const task = (
    code: string,
    taskTitle: string,
    extra: Record<string, unknown> = {},
  ) => ({
    id: ids[code as keyof typeof ids] ?? randomUUID(),
    wish_id: wishId,
    code,
    title: taskTitle,
    status: "TASK_STATUS_PENDING",
    create_time: "2026-10-17T09:00:00Z",
    ...extra,
  });
  const azima = { kind: "TASK_KIND_AZIMA" };
  const file = path.join(
    fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-azima-rank-")),
    "wish.json",
  );
  fs.writeFileSync(
    file,
    JSON.stringify({
      version: 1,
      create_time: "2026-10-17T09:10:00Z",
      wish: { id: wishId, title, create_time: "2026-10-17T09:00:00Z" },
      tasks: [
        task("T1", "Lay the ground", azima),
        task("T2", "Build on it", azima),
        task("T3", "Polish it", azima),
        task("W1", "Pour the base", { part_of: ids.T1 }),
        task("W2", "Raise the walls", { part_of: ids.T2 }),
      ],
    }),
  );
  djinn("wish", "import", file);
  // The ranks as Djinn stores them, by code.
  const ranks = () => {
    const out: Record<string, number> = {};
    for (const [code, id] of Object.entries(ids))
      out[code] = JSON.parse(djinn("task", "get", id, "--json")).task.rank ?? 0;
    return out;
  };
  try {
    // Tall: the terminal panel takes the lower part of the window, and the mouse does not scroll.
    await page.setViewportSize({ width: 1440, height: 2400 });
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await page.getByRole("tab", { name: /^Tasks/ }).click();
    const list = page.locator(".tasks-azimas .azima-list.ranked");
    const codes = list.locator(".azima-heading .agent-code");
    // Never ranked: by code.
    await expect(codes).toHaveText(["T1", "T2", "T3"]);
    const card = (code: string) =>
      list.locator(".azima-card").filter({
        has: page.locator(".agent-code", { hasText: new RegExp(`^${code}$`) }),
      });
    const grip = (code: string) => card(code).locator(".azima-grip");
    await expect(grip("T1")).toHaveAttribute(
      "title",
      "1. T1 (drag, or Alt+Arrow, to change its rank: the work of the first azimas starts first)",
    );

    // T3 dragged above T1: first, and every azima of the wish gets its rank.
    await card("T3").scrollIntoViewIfNeeded();
    await drag(page, grip("T3"), card("T1"));
    await expect(codes).toHaveText(["T3", "T1", "T2"]);
    await expect.poll(ranks).toEqual({ T3: 1, T1: 2, T2: 3 });

    // Alt+ArrowDown moves it one step, after T1; Alt+ArrowUp back.
    await grip("T3").focus();
    await page.keyboard.press("Alt+ArrowDown");
    await expect(codes).toHaveText(["T1", "T3", "T2"]);
    await expect.poll(ranks).toEqual({ T1: 1, T3: 2, T2: 3 });

    // The lead ranks from the command line: the window follows.
    djinn("task", "rank", ids.T2, "--top");
    await expect(codes).toHaveText(["T2", "T1", "T3"]);
    await page.screenshot({ path: "test-results/e2e/azima-rank.png" });
    expect(errors).toEqual([]);
  } finally {
    // It may have been imported paused, when three wishes were active.
    try {
      djinn("wish", "pause", wishId);
    } catch {}
  }
});
