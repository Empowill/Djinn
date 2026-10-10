// The lists that only grow fold their older items (src/older.tsx): a wish of 45 decisions and 40 journal entries shows
// the latest 30 of each, and a line unfolds the others below them. Unfolding does not move what you read on screen,
// not even for a frame (src/scroll-anchor.ts): twice, with Chromium's own scroll anchoring, and with the script that
// stands in for it where WebKit lacks it (the window on Linux).
import { type Locator, type Page, expect, test } from "@playwright/test";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { portable } from "./portable";

portable();

const start = Date.parse("2026-10-07T21:10:00Z");
// The i-th minute after start, the later the newer.
const minute = (i: number) => new Date(start + i * 60_000).toISOString();

// steady runs act, and checks that row did not move on screen in any frame meanwhile.
async function steady(page: Page, row: Locator, act: () => Promise<void>) {
  await row.evaluate((el) => {
    const w = window as unknown as { tops: number[]; sampling: boolean };
    w.tops = [];
    w.sampling = true;
    const step = () => {
      w.tops.push(el.getBoundingClientRect().top);
      if (w.sampling) requestAnimationFrame(step);
    };
    step();
  });
  await act();
  const tops = await page.evaluate(async () => {
    const w = window as unknown as { tops: number[]; sampling: boolean };
    await new Promise((r) => requestAnimationFrame(() => r(undefined)));
    w.sampling = false;
    return w.tops;
  });
  expect(tops.length).toBeGreaterThan(1);
  expect(
    Math.max(...tops.map((top) => Math.abs(top - tops[0]))),
  ).toBeLessThanOrEqual(1);
}

for (const engine of ["native", "script"] as const)
  test(`the older decisions and journal entries unfold below what you read (${engine})`, async ({
    page,
  }) => {
    if (engine === "script")
      await page.addInitScript(() => {
        const supports = CSS.supports.bind(CSS);
        CSS.supports = ((...args: [string, string?]) =>
          args[0] === "overflow-anchor"
            ? false
            : supports(...(args as [string, string]))) as typeof CSS.supports;
      });
    const errors: string[] = [];
    page.on("pageerror", (err) => errors.push(err.message));
    const wishId = randomUUID();
    const title = `Fold the old ${wishId.slice(0, 8)}`;
    const questions = Array.from({ length: 45 }, (_, i) => ({
      id: randomUUID(),
      code: `Q${String(i + 1).padStart(2, "0")}`,
      wish_id: wishId,
      text: `Decision number ${i + 1}?`,
      create_time: minute(i),
      answer: { choice: "CHOICE_YES", create_time: minute(i) },
    }));
    const blocks = Array.from({ length: 40 }, (_, i) => ({
      id: randomUUID(),
      wish_id: wishId,
      kind: "log",
      title: `Entry number ${i + 1}`,
      content: "",
      position: i + 1,
      create_time: minute(100 + i),
    }));
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-older-"));
    const file = path.join(dir, "older.json");
    fs.writeFileSync(
      file,
      JSON.stringify({
        version: 1,
        create_time: minute(0),
        wish: { id: wishId, title, create_time: minute(0) },
        questions,
        blocks,
      }),
    );

    await page.goto(process.env.DJINN_URL!);
    const importButton = page
      .locator(".sidebar")
      .getByRole("button", { name: "Import a wish" });
    await expect(importButton).toBeEnabled();
    const chooser = page.waitForEvent("filechooser");
    await importButton.click();
    await (await chooser).setFiles(file);
    await expect(page.locator(".hero h1")).toHaveText(title);
    const scroller = page.locator(".mission-scroll");
    await expect(scroller).toHaveCSS(
      "overflow-anchor",
      engine === "script" ? "none" : "auto",
    );

    // The journal: the 30 latest entries, then the line for the 10 others.
    await page.getByRole("button", { name: /^Journal/ }).click();
    const entries = page.locator(".journal-list tr");
    await expect(entries).toHaveCount(30);
    await expect(entries.first()).toContainText("Entry number 40");
    await expect(entries.last()).toContainText("Entry number 11");
    const moreEntries = page.getByRole("button", {
      name: "Show the 10 older entries",
    });
    await moreEntries.evaluate((el) => el.scrollIntoView({ block: "end" }));
    await steady(page, entries.last(), async () => {
      await moreEntries.click();
      await expect(entries).toHaveCount(40);
    });
    await expect(entries.last()).toContainText("Entry number 1");
    await expect(moreEntries).toHaveCount(0);

    // The decisions: the 30 latest, then a batch at a time; the line goes once all show.
    await page.getByRole("tab", { name: /^Decisions/ }).click();
    const log = page.getByRole("tabpanel", { name: "Decisions" });
    const rows = log.locator(".decision-row");
    await expect(rows).toHaveCount(30);
    await expect(log.locator("h2 .count")).toHaveText("45");
    await expect(rows.last()).toContainText("Decision number 16?");
    const moreDecisions = log.getByRole("button", {
      name: "Show the 15 older decisions",
    });
    await moreDecisions.evaluate((el) => el.scrollIntoView({ block: "end" }));
    // Not at the top: the page is scrolled, a row read above the line.
    expect(await scroller.evaluate((el) => el.scrollTop)).toBeGreaterThan(0);
    await steady(page, rows.last(), async () => {
      await moreDecisions.click();
      await expect(rows).toHaveCount(45);
    });
    await expect(rows.last()).toContainText("Decision number 1?");
    await expect(moreDecisions).toHaveCount(0);

    await page.screenshot({
      path: path.join(__dirname, `../test-results/e2e/older-${engine}.png`),
    });
    fs.rmSync(dir, { recursive: true, force: true });
    expect(errors).toEqual([]);
  });
